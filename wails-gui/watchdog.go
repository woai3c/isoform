package main

import (
	"context"
	"log"
	"runtime"
	"time"
)

// watchdogInterval is the cadence at which the watchdog goroutine emits a
// heartbeat. Picked at 30s so a hang of even a minute leaves a visible
// "last heartbeat at X" gap, but a healthy day fills the log with only
// ~2880 lines (~ kB-class).
const watchdogInterval = 30 * time.Second

// runWatchdog emits a periodic heartbeat so we can disambiguate process
// hangs from subsystem deadlocks. Useful when investigating "Yansu was
// running but the window wouldn't open" reports.
//
// Reading the log after a hang:
//   - Heartbeat continues but background-audio / OCR / etc. all stop →
//     a subsystem (likely a CGO call) is blocked but the Go runtime is alive.
//   - Heartbeat also stops abruptly → the entire process is hung or was
//     killed; the last heartbeat timestamp pinpoints the moment.
//
// The watchdog is started from startup() and shuts down when ctx is
// cancelled by Wails on app exit.
func runWatchdog(ctx context.Context, startedAtMs int64) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		uptime := time.Duration(time.Now().UnixMilli()-startedAtMs) * time.Millisecond
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		log.Printf(
			"[watchdog] alive uptime=%s goroutines=%d cgo_calls=%d heap_mb=%.0f sys_mb=%.0f num_gc=%d",
			uptime.Truncate(time.Second),
			runtime.NumGoroutine(),
			runtime.NumCgoCall(),
			float64(m.HeapAlloc)/(1024*1024),
			float64(m.Sys)/(1024*1024),
			m.NumGC,
		)
	}
}
