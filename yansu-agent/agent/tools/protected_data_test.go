package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataFoldersExposeToolsAndWorkspaces(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		root := filepath.Join(home, flavor)
		for _, rel := range []string{"skills/research/SKILL.md", "skill-hub/skills/research/SKILL.md", "scratch/report.md", "research/result.md", "workspaces/app/source.go", "chats/c1/report.md", "automations/a1/deliverable.md", "handoffs/result.md", "run/service.sock", "settings.json"} {
			path := filepath.Join(root, filepath.FromSlash(rel))
			for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"} {
				if err := CheckProtectedPaths(tool, map[string]any{"path": path}, project); err != nil {
					t.Errorf("%s %s: %v", tool, rel, err)
				}
			}
			if err := CheckProtectedPaths("Bash", map[string]any{"command": "cat " + path}, project); err != nil {
				t.Errorf("Bash %s: %v", rel, err)
			}
		}
		port := filepath.Join(root, "cron-port")
		if err := CheckProtectedPaths("Read", map[string]any{"path": port}, project); err != nil {
			t.Errorf("read port: %v", err)
		}
		for _, tool := range []string{"Write", "Edit", "NotebookEdit"} {
			if err := CheckProtectedPaths(tool, map[string]any{"path": port}, project); err == nil {
				t.Errorf("%s allowed port rewrite", tool)
			}
		}
		for _, rel := range []string{"sessions.db", "workspaces/app/cache.db", "x-wal", "x-shm", "cron-secret", "ai-provider.json", "memory.json", "mcp-hub/state.json", "mcp-hub/state.json.tmp.1.1", "connector-key.json", "external-mcp.json", "daemon.json", "composio.json", "token", "browser-bridge-token", "mcp/oauth-token.json", "slack/credentials.json", "feishu/credentials.archived.json", "mobile/hub-identity.json", "mobile/relay.json", "mobile/devices.json", "keys/private.pem", "keys/signing.key", "screenshots/x.png", "activity/snapshots/x.png", "activity/audio/x.wav", "activity/segments/x.mp4", "transcripts/x.txt"} {
			path := filepath.Join(root, filepath.FromSlash(rel))
			for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"} {
				if err := CheckProtectedPaths(tool, map[string]any{"path": path}, project); err == nil {
					t.Errorf("%s allowed %s", tool, rel)
				}
			}
			if err := CheckProtectedPaths("Bash", map[string]any{"command": "cat " + path}, project); err == nil {
				t.Errorf("Bash allowed %s", rel)
			}
		}
		for _, rel := range []string{"memory/fact.md", "knowledge/note.md", "SOUL.md"} {
			path := filepath.Join(root, rel)
			if err := CheckProtectedPaths("Bash", map[string]any{"command": "cat " + path}, project); err == nil {
				t.Errorf("Bash allowed personal context %s", rel)
			}
			if err := CheckProtectedPaths("Read", map[string]any{"path": path}, project); err != nil {
				t.Errorf("in-process context read: %v", err)
			}
		}
		if err := CheckProtectedPaths("Bash", map[string]any{"command": "s=research; cat " + root + "/skills/$s/SKILL.md"}, project); err != nil {
			t.Errorf("dynamic skill path: %v", err)
		}
	}
}

func TestBrowserAccountCredentialStores(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, name := range []string{"Login Data For Account", "Web Data", "Account Web Data", "Local State", "Extension Cookies", "Token Service"} {
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			path := filepath.Join(home, "Library/Application Support/Google/Chrome/Default", name+suffix)
			for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"} {
				if err := CheckProtectedPaths(tool, map[string]any{"path": path}, project); err == nil {
					t.Errorf("%s allowed %s", tool, path)
				}
			}
			if err := CheckProtectedPaths("Bash", map[string]any{"command": fmt.Sprintf("cat %q", path)}, project); err == nil {
				t.Errorf("Bash allowed %s", path)
			}
		}
	}
}

func TestProtectedToolsDoNotInventoryCredentialTrees(t *testing.T) {
	home, project := hardBlockHome(t)
	// A loop anywhere in an unrelated browser tree used to refuse every tool.
	// There is deliberately no budget or cardinality-dependent authorization.
	root := filepath.Join(home, "Library/Application Support/large-app")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		if err := os.Symlink(fmt.Sprintf("alias-%d", (i+1)%128), filepath.Join(root, fmt.Sprintf("alias-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(project, "safe.txt")
	writeFile(t, path, "safe")
	for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep", "Bash"} {
		if err := CheckProtectedPaths(tool, map[string]any{"path": path, "command": "cat safe.txt"}, project); err != nil {
			t.Errorf("%s refused unrelated project: %v", tool, err)
		}
	}
	result, err := NewReadTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"path": path}})
	if err != nil || !strings.Contains(result.Content, "safe") {
		t.Fatalf("read after cyclic inventory: %v %v", result, err)
	}
	// Exercise profile construction too: the former Seatbelt inventory
	// followed these unrelated aliases and refused every shell on ELOOP.
	result, err = NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": "cat safe.txt"}})
	if err != nil || result.IsError || !strings.Contains(result.Content, "safe") {
		t.Fatalf("Bash after cyclic inventory: %v %v", result, err)
	}
}
