//go:build !darwin

package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func readWindowState(ctx context.Context) (WindowState, bool) {
	if runtime.WindowIsMinimised(ctx) {
		return WindowState{}, false
	}
	maximised := runtime.WindowIsMaximised(ctx)
	w, h := runtime.WindowGetSize(ctx)
	x, y := runtime.WindowGetPosition(ctx)
	return WindowState{X: x, Y: y, Width: w, Height: h, Maximised: maximised}, true
}
