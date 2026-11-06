package main

import (
	"sync"
	"testing"
	"time"

	"wails-gui/internal/interests"
)

// Two filters built under the same key share the remembered decisions; they
// must not read and write those maps without the shared lock (R65: the app
// quit with "concurrent map read and map write").
func TestInterestsReadDecisionsAreGuardedAcrossFilters(t *testing.T) {
	var c interestsReadCache
	now := time.Now()
	d := c.decisions("k", now)
	if d == nil || c.decisions("k", now) != d {
		t.Fatal("the same key should return the shared decisions")
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				ref := interests.Ref{Key: "memory:" + string(rune('a'+(i+w)%26))}
				d.mu.Lock()
				_, seen := d.checked[ref]
				if !seen {
					d.checked[ref], d.allowed[ref] = true, i%2 == 0
				}
				d.mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
}
