//go:build !darwin

package main

// Keep-awake is a macOS power assertion. Elsewhere the computer reports its
// power as off, so no assertion is taken and the setting has no effect.

func newMobileKeepAwakeOSPower() mobileKeepAwakePower {
	return unsupportedKeepAwakePower{}
}

func newMobileKeepAwakeOSAssertion() mobileKeepAwakeAssertion {
	return unsupportedKeepAwakeAssertion{}
}

type unsupportedKeepAwakePower struct{}

func (unsupportedKeepAwakePower) State() mobileKeepAwakePowerState {
	return mobileKeepAwakePowerState{}
}

func (unsupportedKeepAwakePower) Watch(func()) error { return nil }

type unsupportedKeepAwakeAssertion struct{}

func (unsupportedKeepAwakeAssertion) Hold() error { return nil }

func (unsupportedKeepAwakeAssertion) Release() {}
