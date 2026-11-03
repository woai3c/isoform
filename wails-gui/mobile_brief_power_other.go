//go:build !darwin

package main

// holdMobileBriefPowerAssertion is a no-op where no idle-sleep assertion is
// available. The returned release function is always safe to call.
func holdMobileBriefPowerAssertion(string) (func(), error) {
	return func() {}, nil
}
