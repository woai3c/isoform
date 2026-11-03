package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxProtectedSymlinkReplacementIsDenied(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		for _, folder := range append([]string{""}, bundledToolFolders...) {
			t.Run(flavor+"/"+folder, func(t *testing.T) {
				home, project := sandboxFixture(t)
				entry := filepath.Join(home, flavor, folder)
				target := filepath.Join(project, "relocated")
				filename := "fixture-tool"
				if folder == "" {
					filename = "bin/fixture-tool"
					// Replace only this test's fake app-data root with a link.
					if err := os.RemoveAll(entry); err != nil {
						t.Fatal(err)
					}
				}
				sandboxWrite(t, filepath.Join(target, filename), "original")
				if err := os.Symlink(target, entry); err != nil {
					t.Fatal(err)
				}
				encoded := base64.StdEncoding.EncodeToString([]byte(entry))
				for _, operation := range []string{`os.unlink(p)`, `os.rename(p,p+b".moved")`} {
					command := `python3 -c 'import os,base64;p=base64.b64decode("` + encoded + `");` + operation + `;os.mkdir(p);f=os.path.join(p,b"` + filename + `");os.makedirs(os.path.dirname(f),exist_ok=True);open(f,"w").write("changed")'`
					if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
						t.Fatalf("opaque replacement unexpectedly stopped at lexical check: %v", err)
					}
					result, err := NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": command}})
					if err != nil || result.Metadata["exit_code"] == 0 || !strings.Contains(result.Content, "Operation not permitted") {
						t.Fatalf("kernel permitted %s of protected symlink: %v %+v", operation, err, result)
					}
					info, err := os.Lstat(entry)
					if err != nil || info.Mode()&os.ModeSymlink == 0 {
						t.Fatalf("protected symlink was replaced: %v", err)
					}
					if body, err := os.ReadFile(filepath.Join(entry, filename)); err != nil || string(body) != "original" {
						t.Fatalf("protected bundled file changed: %q %v", body, err)
					}
				}
			})
		}
	}
}
