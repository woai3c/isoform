//go:build windows

package main

// Windows helpers are already bound to a kill-on-close Job Object.
func cleanupOrphanedSpotlightHelpers(string) {}
