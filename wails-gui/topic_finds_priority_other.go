//go:build !darwin

package main

// lowerCurrentThreadPriority is a no-op off macOS, where the app does not run.
func lowerCurrentThreadPriority() {}
