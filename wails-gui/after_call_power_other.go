//go:build !darwin

package main

// lowPowerModeEnabled has no equivalent signal here.
func lowPowerModeEnabled() bool { return false }

// memoryPressureHigh has no equivalent signal here.
func memoryPressureHigh() bool { return false }
