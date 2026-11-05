package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errSnapshotTestBoom = errors.New("boom")

// waitSnapshotIdle waits until no refresh is running, including its emit.
func waitSnapshotIdle[T any](t *testing.T, r *snapshotRefresher[T]) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		idle := !r.running
		r.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("snapshot refresh did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

// waitSnapshotValue waits until a value exists. A cold Get never waits, so the
// first Get is the only kick; polling Get again would queue follow-up loads
// while the first one runs, so this polls the stored state instead.
func waitSnapshotValue[T any](t *testing.T, r *snapshotRefresher[T]) T {
	t.Helper()
	r.Get()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		has, value, lastErr := r.has, r.value, r.lastErr
		r.mu.Unlock()
		if has {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot never produced a value: %v", lastErr)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitSnapshotCount(t *testing.T, n *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("loader ran %d times, want %d", n.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSnapshotRefresherGetReturnsWhileRefreshBlocked(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	r := newSnapshotRefresher(func() (int, error) {
		n := calls.Add(1)
		if n > 1 {
			<-release
		}
		return int(n), nil
	}, nil, nil, 0)
	if v := waitSnapshotValue(t, r); v != 1 {
		t.Fatalf("first value = %d, want 1", v)
	}
	r.Kick()
	waitSnapshotCount(t, &calls, 2)
	start := time.Now()
	v, err := r.Get()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Get waited %v behind a blocked refresh", elapsed)
	}
	if err != nil || v != 1 {
		t.Fatalf("Get during refresh = %d, %v; want the last good value 1", v, err)
	}
}

// Cold reads must not block: the bindings answer in under 50 ms, so a Get with
// no value starts a refresh and returns errSnapshotPending at once, even while
// the loader is still blocked. The refresh's emit then tells the page to read.
func TestSnapshotRefresherColdGetReturnsPendingWhileLoaderBlocked(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	var emits atomic.Int32
	r := newSnapshotRefresher(func() (int, error) {
		calls.Add(1)
		<-gate
		return 7, nil
	}, nil, func() { emits.Add(1) }, 0)

	start := time.Now()
	v, err := r.Get()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("cold Get blocked for %v behind the loader", elapsed)
	}
	if !errors.Is(err, errSnapshotPending) || v != 0 {
		t.Fatalf("cold Get = %d, %v; want 0, errSnapshotPending", v, err)
	}
	waitSnapshotCount(t, &calls, 1)
	if got := emits.Load(); got != 0 {
		t.Fatalf("emit ran before the loader returned: %d", got)
	}

	close(gate)
	waitSnapshotIdle(t, r)
	if got := emits.Load(); got != 1 {
		t.Fatalf("emits after the first load = %d, want exactly 1", got)
	}
	if v, err := r.Get(); err != nil || v != 7 {
		t.Fatalf("read after the refresh = %d, %v; want 7", v, err)
	}
}

// A cold read that failed reports the refresh error, and still does not wait
// for the next refresh.
func TestSnapshotRefresherColdFailureReportsLastError(t *testing.T) {
	var calls atomic.Int32
	r := newSnapshotRefresher(func() (int, error) {
		calls.Add(1)
		return 0, errSnapshotTestBoom
	}, nil, nil, 0)
	if _, err := r.Get(); !errors.Is(err, errSnapshotPending) {
		t.Fatalf("first cold Get error = %v, want errSnapshotPending", err)
	}
	waitSnapshotIdle(t, r)
	start := time.Now()
	_, err := r.Get()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Get after a failed refresh waited %v", elapsed)
	}
	if !errors.Is(err, errSnapshotTestBoom) {
		t.Fatalf("Get after a failed refresh = %v, want the refresh error", err)
	}
}

func TestSnapshotRefresherSingleflightAndOneFollowUp(t *testing.T) {
	var running, maxRunning, runs atomic.Int32
	gate := make(chan struct{})
	r := newSnapshotRefresher(func() (int, error) {
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		runs.Add(1)
		<-gate
		running.Add(-1)
		return 1, nil
	}, nil, nil, 0)

	r.Kick()
	waitSnapshotCount(t, &runs, 1)
	const kicks = 50
	var wg sync.WaitGroup
	for i := 0; i < kicks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Kick()
		}()
	}
	wg.Wait()
	close(gate)
	waitSnapshotIdle(t, r)
	time.Sleep(20 * time.Millisecond)
	waitSnapshotIdle(t, r)

	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("max concurrent loader runs = %d, want 1", got)
	}
	// One run for the first kick and exactly one follow-up for the burst.
	if got := runs.Load(); got != 2 {
		t.Fatalf("loader ran %d times for %d kicks, want 2", got, kicks+1)
	}
}

func TestSnapshotRefresherEmitsOnlyOnChange(t *testing.T) {
	values := []string{"a", "a", "b"}
	var calls, emits atomic.Int32
	r := newSnapshotRefresher(func() (string, error) {
		i := int(calls.Add(1)) - 1
		return values[i], nil
	}, nil, func() { emits.Add(1) }, 0)

	waitSnapshotValue(t, r)
	waitSnapshotIdle(t, r)
	if got := emits.Load(); got != 1 {
		t.Fatalf("first load emitted %d times, want 1", got)
	}
	r.Kick()
	waitSnapshotCount(t, &calls, 2)
	waitSnapshotIdle(t, r)
	if got := emits.Load(); got != 1 {
		t.Fatalf("equal value emitted again: %d emits", got)
	}
	r.Kick()
	waitSnapshotCount(t, &calls, 3)
	waitSnapshotIdle(t, r)
	if got := emits.Load(); got != 2 {
		t.Fatalf("changed value emits = %d, want 2", got)
	}
}

func TestSnapshotRefresherDropFencesRunningRefresh(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	r := newSnapshotRefresher(func() (string, error) {
		switch calls.Add(1) {
		case 1:
			return "before withdrawal", nil
		case 2:
			// Started before the withdrawal; its result must never be stored.
			<-gate
			return "stale", nil
		default:
			return "after withdrawal", nil
		}
	}, nil, nil, 0)

	if v := waitSnapshotValue(t, r); v != "before withdrawal" {
		t.Fatalf("first value = %q", v)
	}
	r.Kick()
	waitSnapshotCount(t, &calls, 2)
	r.Drop()
	close(gate)
	waitSnapshotIdle(t, r)

	v, err := r.Get()
	if err == nil || v != "" {
		t.Fatalf("read after withdrawal served %q, %v; want nothing until a fresh refresh", v, err)
	}
	waitSnapshotIdle(t, r)
	if v, err := r.Get(); err != nil || v != "after withdrawal" {
		t.Fatalf("fresh read = %q, %v; want the post-withdrawal value", v, err)
	}
}

func TestSnapshotRefresherMaxAgeRevalidatesWithoutBlocking(t *testing.T) {
	var calls atomic.Int32
	r := newSnapshotRefresher(func() (int, error) {
		return int(calls.Add(1)), nil
	}, nil, nil, 20*time.Millisecond)
	if v := waitSnapshotValue(t, r); v != 1 {
		t.Fatalf("first value = %d", v)
	}
	time.Sleep(30 * time.Millisecond)
	start := time.Now()
	v, _ := r.Get()
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("stale read blocked for %v", elapsed)
	}
	if v != 1 {
		t.Fatalf("stale read = %d, want the last good value while revalidating", v)
	}
	waitSnapshotIdle(t, r)
	if v, _ := r.Get(); v != 2 {
		t.Fatalf("revalidated read = %d, want 2", v)
	}
}

func TestSnapshotCacheInvalidateAndDropReachEveryKey(t *testing.T) {
	var cache snapshotCache[int]
	var calls atomic.Int32
	build := func() *snapshotRefresher[int] {
		return newSnapshotRefresher(func() (int, error) { return int(calls.Add(1)), nil }, nil, nil, 0)
	}
	a := cache.get("2026-10-23", build)
	b := cache.get("2026-10-24", build)
	if a != cache.get("2026-10-23", build) || a == b {
		t.Fatal("cache must return one refresher per key")
	}
	a.Get()
	b.Get()
	waitSnapshotValue(t, a)
	waitSnapshotValue(t, b)
	cache.dropAll()
	if v, err := a.Get(); err == nil || v != 0 {
		t.Fatalf("dropped key served %d, %v", v, err)
	}
	cache.invalidateAll()
	waitSnapshotIdle(t, a)
	if v, _ := a.Get(); v == 0 {
		t.Fatal("invalidated key did not refresh")
	}
}

func TestSnapshotRefresherReadAfterWriteLoadsBeforeAnswering(t *testing.T) {
	var calls atomic.Int32
	r := newSnapshotRefresher(func() (int, error) {
		return int(calls.Add(1)), nil
	}, nil, nil, 0).readAfterWrites()
	if v := waitSnapshotValue(t, r); v != 1 {
		t.Fatalf("first value = %d", v)
	}
	// A write makes the next read answer with the write, not the old value.
	r.Invalidate()
	if v, err := r.Get(); err != nil || v != 2 {
		t.Fatalf("read after write = %d, %v; want the reloaded value 2", v, err)
	}
	waitSnapshotIdle(t, r)
}
