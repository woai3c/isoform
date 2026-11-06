package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// holdFirstMemoryRun makes the first folder read after this call wait until
// release; started closes when that read is inside the hold.
func holdFirstMemoryRun() (started <-chan struct{}, release func()) {
	s := make(chan struct{})
	r := make(chan struct{})
	var once sync.Once
	memoryRunGate = func() { once.Do(func() { close(s); <-r }) }
	return s, func() { close(r) }
}

func countMemoryFolderReads(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	prev := memoryExternalReadLoad
	memoryExternalReadLoad = func(kind string) {
		if kind == "entries" {
			n.Add(1)
		}
		if prev != nil {
			prev(kind)
		}
	}
	t.Cleanup(func() { memoryExternalReadLoad = prev; memoryRunGate = nil })
	return &n
}

// Concurrent readers share one folder read: the folder is read once, and the
// rest wait for it, with no reader queued behind another's I/O.
func TestConcurrentMemoryReadsShareOneFolderRead(t *testing.T) {
	rig := newHandoffTestRig(t)
	a := rig.app
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	writeMemoryFixture(t, "shared-note", "Shared note")
	settleMemoryFolder(t, dir)
	a.invalidateMemoryExternalReads()
	reads := countMemoryFolderReads(t)
	started, release := holdFirstMemoryRun()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = a.memorySnapshot()
	}()
	<-started
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := a.memorySnapshot(); err != nil || s == nil {
				t.Error("a reader during the shared run got no snapshot")
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	release()
	wg.Wait()
	if n := reads.Load(); n != 1 {
		t.Fatalf("11 concurrent readers read the folder %d times, want 1", n)
	}
}

// A read that arrives after a write never receives the run that started
// before it: the write is visible to it at once.
func TestAReadAfterAWriteNeverGetsAnOlderRun(t *testing.T) {
	rig := newHandoffTestRig(t)
	a := rig.app
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	writeMemoryFixture(t, "before-note", "Before note")
	settleMemoryFolder(t, dir)
	a.invalidateMemoryExternalReads()
	started, release := holdFirstMemoryRun()
	var firstWG sync.WaitGroup
	firstWG.Add(1)
	go func() {
		defer firstWG.Done()
		_, _ = a.memorySnapshot()
	}()
	<-started
	// The write lands during the run, then the app marks it (the forget and edit
	// paths do the same), then a reader arrives.
	writeMemoryFixture(t, "after-note", "After note")
	a.invalidateMemoryExternalReads()
	result := make(chan *memoryExternalSnapshot, 1)
	go func() {
		s, _ := a.memorySnapshot()
		result <- s
	}()
	time.Sleep(50 * time.Millisecond)
	release()
	firstWG.Wait()
	select {
	case s := <-result:
		found := false
		for _, e := range s.entries {
			if strings.Contains(e.raw.Content, "After note") {
				found = true
			}
		}
		if !found {
			t.Fatal("a read after a write was served the run from before it")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the read after the write did not finish")
	}
}
