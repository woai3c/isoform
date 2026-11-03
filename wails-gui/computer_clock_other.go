//go:build !darwin

package main

func systemUses24HourClock() bool { return false }
