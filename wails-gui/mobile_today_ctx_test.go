package main

import (
	"testing"
	"time"
)

// A bridge built without a lifecycle context must not start the Today loops,
// which select on bridge.ctx.Done() and would panic on a nil context.
func TestMobileTodayCacheWithoutContextStartsNoLoops(t *testing.T) {
	bridge := &mobileBridge{}
	bridge.startTodayCache()
	time.Sleep(50 * time.Millisecond)
	if bridge.today.updates == nil || bridge.today.wake == nil {
		t.Fatal("the Today channels should still be created")
	}
}
