package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeatbeltPrivateConversationBriefAndLogs(t *testing.T) {
	home, project := sandboxFixture(t)
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		for _, relative := range privateConversationBriefLogPaths {
			t.Run(flavor+"/"+relative, func(t *testing.T) {
				path := filepath.Join(home, flavor, relative)
				sandboxWrite(t, path, sandboxSecret)
				assertPrivateArtifactKernelReadDenied(t, project, path)
			})
		}
	}
}

func TestSeatbeltPrivateArtifactDirectorySymlinks(t *testing.T) {
	home, project := sandboxFixture(t)
	for _, relative := range []string{"slack-scan/conversations", "mobile/briefs"} {
		t.Run(relative, func(t *testing.T) {
			destination := filepath.Join(project, strings.ReplaceAll(relative, "/", "-"))
			path := filepath.Join(destination, "private.json")
			sandboxWrite(t, path, sandboxSecret)
			link := filepath.Join(home, ".isoform-agent", relative)
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			if err := CheckProtectedPaths("Read", map[string]any{"path": path}, project); err == nil {
				t.Error("direct read of relocated private directory was allowed")
			}
			assertPrivateArtifactKernelReadDenied(t, project, path)
		})
	}
}

func assertPrivateArtifactKernelReadDenied(t *testing.T, project, path string) {
	t.Helper()
	// Opaque paths force the test through the kernel instead of the friendly
	// lexical refusal. The attempted read only contains fixture data.
	encoded := base64.StdEncoding.EncodeToString([]byte(path))
	command := `python3 -c 'import base64;print(open(base64.b64decode("` + encoded + `")).read())'`
	result, err := NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": command}})
	if err != nil || result.Metadata["exit_code"] == 0 || strings.Contains(result.Content, sandboxSecret) || !strings.Contains(result.Content, "Operation not permitted") {
		t.Fatalf("kernel allowed private artifact: err=%v result=%+v", err, result)
	}
}
