package tools

import (
	"path/filepath"
	"testing"
)

func TestCuaDaemonSocketUsesSelectedAgentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YANSU_APP_FLAVOR", "isoform")
	t.Setenv("YANSU_AGENT_HOME", "")
	if got, want := cuaDaemonSocketPath(), filepath.Join(home, ".isoform-agent", "run", "cua-driver.sock"); got != want {
		t.Fatalf("flavor socket = %q, want %q", got, want)
	}
	custom := filepath.Join(home, "custom")
	t.Setenv("YANSU_AGENT_HOME", custom)
	if got, want := cuaDaemonSocketPath(), filepath.Join(custom, "run", "cua-driver.sock"); got != want {
		t.Fatalf("override socket = %q, want %q", got, want)
	}
	t.Setenv("YANSU_AGENT_HOME", "relative")
	if got := cuaDaemonSocketPath(); got != "" {
		t.Fatalf("invalid home produced socket %q", got)
	}
}
