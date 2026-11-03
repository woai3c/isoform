//go:build !updateqa

package main

func startUpdateQAHook(*App) bool { return false }
