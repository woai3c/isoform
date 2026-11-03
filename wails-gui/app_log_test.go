package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAppLogRetainsThirtyMinuteWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yansu-gui.log")
	w, err := newRotatingAppLog(path, maxLogSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	// Deliberately exceed two files: 30 simulated minutes at 12 KiB/s of
	// aggregate diagnostics, far above the state-only capture messages.
	var want bytes.Buffer
	for second := 0; second < 30*60; second++ {
		line := fmt.Sprintf("session second=%04d ", second) + strings.Repeat("x", 12*1024) + "\n"
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
		want.WriteString(line)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	for _, suffix := range []string{".old.2", ".old", ""} {
		data, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > maxLogSize {
			t.Fatalf("%s exceeded cap: %d", suffix, len(data))
		}
		got.Write(data)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatalf("30-minute diagnostic window was not preserved: got %d, want %d bytes", got.Len(), want.Len())
	}
}

func TestAppLogRotatesDuringWritesAndAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yansu-gui.log")
	const capBytes = 64
	w, err := newRotatingAppLog(path, capBytes)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := w.Write(bytes.Repeat([]byte{byte('a' + i)}, capBytes)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for i, suffix := range []string{".old.2", ".old", ""} {
		data, err := os.ReadFile(path + suffix)
		if err != nil || !bytes.Equal(data, bytes.Repeat([]byte{byte('c' + i)}, capBytes)) {
			t.Fatalf("unexpected rotation %s: %q, %v", suffix, data, err)
		}
	}
	w, err = newRotatingAppLog(path, capBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write([]byte("reopened\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path + ".old")
	if err != nil || !bytes.Equal(data, bytes.Repeat([]byte("e"), capBytes)) {
		t.Fatalf("startup did not rotate full file: %q, %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 3 {
		t.Fatalf("expected exactly three log files: %d, %v", len(entries), err)
	}
}

func TestAppLogConcurrentWritesStayWholeAndBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yansu-gui.log")
	w, err := newRotatingAppLog(path, 2048)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := fmt.Fprintf(w, "%040d\n", i*10+j); err != nil {
					t.Errorf("write: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	seen := make(map[string]bool)
	for _, suffix := range []string{".old.2", ".old", ""} {
		data, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 2048 {
			t.Fatalf("log exceeds cap: %d", len(data))
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if len(line) != 40 || seen[line] {
				t.Fatalf("split or duplicate entry: %q", line)
			}
			seen[line] = true
		}
	}
	if len(seen) != 100 {
		t.Fatalf("lost entries: got %d", len(seen))
	}
}
