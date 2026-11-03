//go:build !darwin

package main

// The setup window and the menu bar mark are macOS features. These stubs keep the
// shared code compiling elsewhere; the App methods return nil or zero values.

func nativeSetupWindowApply(_ int, _ setupWindowRect) bool { return false }

func nativeSetupWindowFrame() (setupWindowRect, bool) { return setupWindowRect{}, false }

func nativeSetupMarkSetVisible(_, _ bool) bool { return false }

func nativeSetupWindowLandingInput() setupWindowLandingInput { return setupWindowLandingInput{} }

func nativeSetupFinishPlay(_ setupFinishPlay) <-chan struct{} { return nil }

func nativeSetupReveal(_ float64) {}

func nativeSetupMarkStartsVisible(_ bool) {}

func readNativeSetupLaunchState() setupLaunchState { return setupLaunchState{} }
