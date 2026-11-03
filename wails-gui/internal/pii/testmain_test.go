package pii

import (
	"os"
	"testing"
)

// TestMain points HOME at a fresh temporary directory so a test that resolves
// a home-relative folder (the detector's config dir fallback) lands there
// instead of the user's real data folder. The integration test keeps its
// explicit YANSU_CONFIG_DIR escape hatch.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("/tmp", "pii-test-home-")
	if err != nil {
		home, err = os.MkdirTemp("", "pii-test-home-")
	}
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	_ = os.Unsetenv("YANSU_AGENT_HOME")
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
