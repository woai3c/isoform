package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashBundledToolsByNameAndOpaqueWriteRefusal(t *testing.T) {
	home, project := sandboxFixture(t)
	const original = "#!/bin/sh\nprintf bundled-tool-ok\n"
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		root := filepath.Join(home, flavor)
		for _, folder := range bundledToolFolders {
			path := filepath.Join(root, folder, "isoform-fixture-tool")
			sandboxWrite(t, path, original)
			if err := os.Chmod(path, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Dir(path)+string(os.PathListSeparator)+os.Getenv("PATH"))
			result, err := NewBashTool().Execute(context.Background(), ToolCall{
				WorkDir: project, Input: map[string]any{"command": "isoform-fixture-tool"},
			})
			if err != nil || result.IsError || result.Metadata["exit_code"] != 0 || result.Content != "bundled-tool-ok" {
				t.Fatalf("bundled tool by name %s: %v %+v", path, err, result)
			}
			encoded := base64.StdEncoding.EncodeToString([]byte(path))
			for _, operation := range []string{`open(p,"w").write("changed")`, `os.unlink(p)`, `os.chmod(p,0o777)`, `os.rename(p,p+b".moved")`} {
				command := `python3 -c 'import os,base64;p=base64.b64decode("` + encoded + `");` + operation + `'`
				if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
					t.Fatalf("opaque probe unexpectedly stopped at lexical check: %v", err)
				}
				result, err = NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": command}})
				if err != nil || result.Metadata["exit_code"] == 0 || !strings.Contains(result.Content, "Operation not permitted") {
					t.Errorf("kernel permitted %s on %s: %v %+v", operation, path, err, result)
				}
				contents, err := os.ReadFile(path)
				if err != nil || string(contents) != original {
					t.Fatalf("bundled tool changed: %s: %v %q", path, err, contents)
				}
			}
			newPath := filepath.Join(root, folder, "new-tool")
			command := `python3 -c 'import base64;open(base64.b64decode("` + base64.StdEncoding.EncodeToString([]byte(newPath)) + `"),"w").write("changed")'`
			result, err = NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": command}})
			if err != nil || result.Metadata["exit_code"] == 0 || !strings.Contains(result.Content, "Operation not permitted") {
				t.Errorf("kernel permitted creating bundled file %s: %v %+v", newPath, err, result)
			}
			if _, err := os.Stat(newPath); !os.IsNotExist(err) {
				t.Fatalf("bundled file was created: %s (%v)", newPath, err)
			}
		}
	}
}

func TestBashActivityAndSQLiteOpaqueReadRefusal(t *testing.T) {
	home, project := sandboxFixture(t)
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		for _, relative := range []string{"activity/key-frames/private.jpg", "activity/new-format/private.bin", "probe.sqlite", "probe.sqlite3", "probe.sqlite-journal", "probe.sqlite3-journal", "probe.sqlite-wal", "probe.sqlite3-shm"} {
			path := filepath.Join(home, flavor, relative)
			sandboxWrite(t, path, sandboxSecret)
			command := `python3 -c 'import base64;print(open(base64.b64decode("` + base64.StdEncoding.EncodeToString([]byte(path)) + `")).read())'`
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
				t.Fatalf("opaque read unexpectedly stopped at lexical check: %v", err)
			}
			result, err := NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": command}})
			if err != nil || result.Metadata["exit_code"] == 0 || strings.Contains(result.Content, sandboxSecret) || !strings.Contains(result.Content, "Operation not permitted") {
				t.Errorf("kernel permitted reading %s: %v %+v", path, err, result)
			}
		}
	}
}
