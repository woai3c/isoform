package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

var privateConversationBriefLogPaths = []string{
	"slack-scan/conversations/thread.json",
	"mobile/briefs/latest.json",
	"mobile/briefs/2026-10-16.json",
	"yansu-gui.log",
	"yansu-gui.log.old",
	"yansu-gui.log.old.2",
	"yansu-gui.stderr.log",
	"yansu-gui.stderr.log.old",
	"triage.log",
	"triage.log.old.2",
}

func TestPrivateConversationBriefAndLogsRefuseBash(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		for _, relative := range privateConversationBriefLogPaths {
			t.Run(flavor+"/"+relative, func(t *testing.T) {
				path := filepath.Join(home, flavor, relative)
				writeFile(t, path, "private-artifact-content")
				for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"} {
					if err := CheckProtectedPaths(tool, map[string]any{"path": path}, project); err == nil {
						t.Errorf("%s allowed private artifact %s", tool, relative)
					}
				}
				// No approval context is the attended prompt-off path.
				result, err := NewBashTool().Execute(context.Background(), ToolCall{
					WorkDir: project,
					Input:   map[string]any{"command": fmt.Sprintf("cat %q", path)},
				})
				if err == nil || strings.Contains(result.Content, "private-artifact-content") {
					t.Fatalf("attended Bash allowed private artifact: err=%v result=%+v", err, result)
				}
			})
		}
	}
}
