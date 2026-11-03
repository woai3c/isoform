//go:build !darwin

package main

import "context"

func (a *App) refreshMenuBarPresentation() {}

func (a *App) noteMenuBarEvent(_ string) {}

func menuBarPlaybackChanged(_ string) {}

func (a *App) preloadMenuBarView(_ context.Context) {}
