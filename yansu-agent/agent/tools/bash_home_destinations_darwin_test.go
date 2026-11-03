package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashHomeDestinationsRequireWorkspaceBoundary(t *testing.T) {
	home, project := sandboxFixture(t)
	sandboxWrite(t, filepath.Join(project, "report.pdf"), "report")
	sandboxWrite(t, filepath.Join(home, "Downloads/a.pdf"), "download")
	sandboxWrite(t, filepath.Join(home, "ordinary.log"), "log")
	for _, command := range []string{"cp report.pdf ~/", "mv ~/Downloads/a.pdf ~/", "rm ~/*.log"} {
		result, err := NewBashTool().Execute(context.Background(), ToolCall{
			WorkDir: project, Input: map[string]any{"command": command},
			Authority: &AuthorityRuntime{Approved: true},
		})
		// These are not bundled-tool writes, so the lexical check permits
		// them. The HOME kernel boundary still refuses them after approval.
		if err != nil || result.Metadata["exit_code"] == 0 || !strings.Contains(result.Content, "Operation not permitted") {
			t.Fatalf("write outside workspace escaped HOME boundary: %s: %v %+v", command, err, result)
		}
	}
	for _, name := range []string{"report.pdf", "a.pdf"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Errorf("HOME destination was created: %s: %v", name, err)
		}
	}
	for name, want := range map[string]string{"Downloads/a.pdf": "download", "ordinary.log": "log"} {
		if body, err := os.ReadFile(filepath.Join(home, name)); err != nil || string(body) != want {
			t.Errorf("%s: %q %v", name, body, err)
		}
	}
}
