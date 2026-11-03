//go:build !darwin || !cgo || ios

package models

import "context"

// Other platforms currently have no path-cost provider.
func WatchNetwork(ctx context.Context, update func(NetworkState)) func() NetworkState {
	state := NetworkState{Known: true}
	update(state)
	return func() NetworkState { return state }
}
