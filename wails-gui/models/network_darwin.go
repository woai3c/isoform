//go:build darwin && cgo && !ios

package models

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Network
#include <Network/Network.h>
#include <dispatch/dispatch.h>
#include <stdatomic.h>
#include <stdlib.h>

typedef struct {
    nw_path_monitor_t monitor;
    dispatch_queue_t queue;
    dispatch_semaphore_t stopped;
    atomic_int flags;
} setup_network_monitor;

static setup_network_monitor *setup_network_start(void) {
    setup_network_monitor *state = calloc(1, sizeof(*state));
    state->monitor = nw_path_monitor_create();
    state->queue = dispatch_queue_create("app.isoform.model-network", DISPATCH_QUEUE_SERIAL);
    state->stopped = dispatch_semaphore_create(0);
    nw_path_monitor_set_update_handler(state->monitor, ^(nw_path_t path) {
        int flags = 1;
        if (nw_path_get_status(path) != nw_path_status_satisfied) flags |= 8;
        if (nw_path_is_expensive(path)) flags |= 2;
        if (nw_path_is_constrained(path)) flags |= 4;
        atomic_store(&state->flags, flags);
    });
    nw_path_monitor_set_cancel_handler(state->monitor, ^{ dispatch_semaphore_signal(state->stopped); });
    nw_path_monitor_set_queue(state->monitor, state->queue);
    nw_path_monitor_start(state->monitor);
    return state;
}
static int setup_network_flags(setup_network_monitor *state) { return atomic_load(&state->flags); }
static void setup_network_stop(setup_network_monitor *state) {
    nw_path_monitor_cancel(state->monitor);
    dispatch_semaphore_wait(state->stopped, DISPATCH_TIME_FOREVER);
    nw_release(state->monitor);
    dispatch_release(state->queue);
    dispatch_release(state->stopped);
    free(state);
}
*/
import "C"

import (
	"context"
	"sync"
	"time"
)

// WatchNetwork keeps the initial unknown path closed. NWPathMonitor owns the
// native path events; the bridge reads its atomic snapshot without a UI loop.
func WatchNetwork(ctx context.Context, update func(NetworkState)) func() NetworkState {
	monitor := C.setup_network_start()
	var mu sync.Mutex
	read := func() NetworkState {
		mu.Lock()
		defer mu.Unlock()
		if monitor == nil {
			return NetworkState{}
		}
		flags := int(C.setup_network_flags(monitor))
		return NetworkState{Known: flags&1 != 0, Offline: flags&8 != 0, Expensive: flags&2 != 0, Constrained: flags&4 != 0}
	}
	update(NetworkState{})
	go func() {
		defer func() {
			mu.Lock()
			defer mu.Unlock()
			C.setup_network_stop(monitor)
			monitor = nil
		}()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		previous := NetworkState{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state := read()
				if state != previous {
					previous = state
					update(state)
				}
			}
		}
	}()
	return read
}
