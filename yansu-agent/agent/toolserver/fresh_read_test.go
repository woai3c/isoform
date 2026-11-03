package toolserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/agent/tools"
)

type freshSourceRead struct{ tools.Tool }

func (freshSourceRead) RequiresFreshRead() bool { return true }

// The real Read handler and real request-ID MCP transport verify that a
// privacy-sensitive reader cannot replay text after a file is changed/deleted.
func TestToolServerFreshReadDoesNotReplayWithdrawnSource(t *testing.T) {
	server, work, _ := newReplayCacheServer(t)
	server.gate.Config.Registry = tools.NewRegistry()
	server.gate.Config.Registry.Register(freshSourceRead{tools.NewReadTool()})
	c := newRequestIDClient(t, server)
	path := filepath.Join(work, "source.txt")
	if err := os.WriteFile(path, []byte("original source marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := c.call(t, "first", "Read", map[string]any{"path": path}, false); !strings.Contains(string(result), "original source marker") {
		t.Fatalf("missing real source: %s", result)
	}
	if err := os.WriteFile(path, []byte("updated source marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := c.call(t, "first", "Read", map[string]any{"path": path}, true); strings.Contains(string(result), "original source marker") || !strings.Contains(string(result), "fresh call ID") {
		t.Fatalf("stale replay: %s", result)
	}
	if result := c.call(t, "second", "Read", map[string]any{"path": path}, false); !strings.Contains(string(result), "updated source marker") {
		t.Fatalf("retry lost current source: %s", result)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	c.call(t, "third", "Read", map[string]any{"path": path}, true)
	assertReplayCacheSize(t, server, 3, 0)
}
