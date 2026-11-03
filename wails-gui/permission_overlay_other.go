//go:build !darwin

package main

func presentPermissionOverlay(panelKey string, srcX, srcY, srcW, srcH float64) {}

func dismissPermissionOverlay() {}
