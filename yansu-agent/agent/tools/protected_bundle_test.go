package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent/authority"
)

func TestBundledToolsAreReadableButImmutable(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		root := filepath.Join(home, flavor)
		for _, folder := range bundledToolFolders {
			path := filepath.Join(root, folder, "tool.ipynb")
			writeFile(t, path, `{"cells":[{"cell_type":"code","source":["original"]}],"nbformat":4}`)
			for _, tool := range []string{"Read", "Glob", "Grep"} {
				if err := CheckProtectedPaths(tool, map[string]any{"path": path}, project); err != nil {
					t.Errorf("%s cannot read bundled %s: %v", tool, folder, err)
				}
			}
			paths := []string{path, filepath.Join(root, folder, "new-file")}
			if runtime.GOOS == "darwin" {
				paths = append(paths, "/system/volumes/data"+path, strings.ToUpper(path))
			}
			for _, candidate := range paths {
				for _, tool := range []Tool{NewWriteTool(nil), NewEditTool(nil), NewNotebookEditTool(nil)} {
					_, err := tool.Execute(context.Background(), ToolCall{
						WorkDir:   root,
						Authority: &AuthorityRuntime{Approved: true, Context: authority.Context{}},
						Input:     map[string]any{"path": candidate, "content": "changed", "old_string": "original", "new_string": "changed", "cell_index": 0, "new_source": "changed"},
					})
					var blocked *BundledToolWriteError
					if !errors.As(err, &blocked) {
						t.Errorf("%s %s: %v, want bundled tool refusal even when approved", tool.Name(), candidate, err)
					}
				}
			}
			quoted := strconv.Quote(path)
			for _, command := range []string{
				"printf changed > " + quoted, "printf changed >> " + quoted,
				"cp source " + quoted, "mv " + quoted + " moved", "rm " + quoted,
				"chmod +x " + quoted, "touch " + quoted, "tee " + quoted,
				"dd if=/dev/null of=" + quoted, "sed -i '' s/original/changed/ " + quoted,
				"ln source " + quoted, "ln " + quoted + " alias",
				"cd " + strconv.Quote(filepath.Dir(path)) + "; printf changed > tool.ipynb",
			} {
				var blocked *BundledToolWriteError
				if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); !errors.As(err, &blocked) {
					t.Errorf("mutation %s: %v, want bundled tool refusal", command, err)
				}
			}
			if err := CheckProtectedPaths("Bash", map[string]any{"command": "cat " + quoted}, project); err != nil {
				t.Errorf("read bundled tool: %v", err)
			}
		}
		if err := CheckProtectedPaths("Bash", map[string]any{"command": "rm -rf " + strconv.Quote(root)}, project); err == nil {
			t.Error("removing a bundle ancestor was allowed")
		}
	}
}

func TestBundledToolRootSymlinkIsImmutable(t *testing.T) {
	home, project := hardBlockHome(t)
	root := filepath.Join(home, ".isoform-agent")
	target := filepath.Join(home, "relocated-tools")
	writeFile(t, filepath.Join(target, "tool"), "original")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(target, "tool"), filepath.Join(root, "bin", "tool")} {
		if err := CheckProtectedPaths("Write", map[string]any{"path": path}, project); err == nil {
			t.Errorf("symlinked bundle writable: %s", path)
		}
	}
}

func TestActivityAndSQLitePathsAreProtected(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		for _, relative := range []string{"activity", "activity/custom/new-format.bin", "activity/key-frames/x.jpg", "cache.sqlite", "cache.sqlite3", "cache.sqlite-journal", "cache.sqlite3-journal", "cache.sqlite-wal", "cache.sqlite3-shm", "cache.db-journal"} {
			path := filepath.Join(home, flavor, relative)
			for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"} {
				if err := CheckProtectedPaths(tool, map[string]any{"path": path}, project); err == nil {
					t.Errorf("%s allowed %s", tool, path)
				}
			}
			if err := CheckProtectedPaths("Bash", map[string]any{"command": "cat " + strconv.Quote(path)}, project); err == nil {
				t.Errorf("Bash allowed %s", path)
			}
		}
	}
}

func TestActivityDirectorySymlinkTargetsStayProtected(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		root := filepath.Join(home, flavor)
		target := filepath.Join(home, flavor+"-media-target")
		path := filepath.Join(target, "private-frame.bin")
		writeFile(t, path, "PRIVATE_MEDIA_SENTINEL")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "activity")); err != nil {
			t.Fatal(err)
		}
		for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"} {
			if err := CheckProtectedPaths(tool, map[string]any{"path": path}, project); err == nil {
				t.Errorf("%s accepted direct media target %s", tool, path)
			}
		}
	}
	for _, item := range []struct {
		tool  Tool
		input map[string]any
	}{
		{NewGlobTool(), map[string]any{"path": home, "pattern": "**/*"}},
		{NewGrepTool(), map[string]any{"path": home, "pattern": "PRIVATE_MEDIA_SENTINEL", "glob": "**/*", "output_mode": "content"}},
	} {
		t.Run(item.tool.Name(), func(t *testing.T) {
			if item.tool.Name() == "Grep" {
				if _, err := exec.LookPath("rg"); err != nil {
					t.Skip("ripgrep unavailable; other protected-path checks still run")
				}
			}
			result, err := item.tool.Execute(context.Background(), ToolCall{WorkDir: home, Input: item.input})
			if err != nil || strings.Contains(result.Content, "private-frame.bin") || strings.Contains(result.Content, "PRIVATE_MEDIA_SENTINEL") {
				t.Errorf("%s exposed media through an ancestor: %v %+v", item.tool.Name(), err, result)
			}
		})
	}
}

func TestProtectedToolsLargeLibraryBudget(t *testing.T) {
	if os.Getenv("YANSU_TEST_LARGE_LIBRARY") != "1" {
		t.Skip("set YANSU_TEST_LARGE_LIBRARY=1 to exercise the 300,000-entry inventory regression")
	}
	// Exceed the former 100,000-entry inventory cap. Keep this fixture opt-in
	// because creating and removing its entries is expensive on developer Macs.
	testProtectedToolsLibraryBudget(t, 300)
}

func TestProtectedToolsLibraryBudget(t *testing.T) {
	testProtectedToolsLibraryBudget(t, 10)
}

func testProtectedToolsLibraryBudget(t *testing.T, directories int) {
	t.Helper()
	home, project := hardBlockHome(t)
	// Real directory entries reproduce the former per-call inventory cost.
	// Hard links avoid unnecessary file data; setup is outside the tool budget.
	const entriesPerDirectory = 1000
	setup := time.Now()
	for directory := 0; directory < directories; directory++ {
		dir := filepath.Join(home, "Library", "Application Support", "large-app", fmt.Sprintf("d%03d", directory))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		seed := filepath.Join(dir, "f0000")
		if err := os.WriteFile(seed, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		for entry := 1; entry < entriesPerDirectory; entry++ {
			if err := os.Link(seed, filepath.Join(dir, fmt.Sprintf("f%04d", entry))); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("created %d Library files in %s", directories*entriesPerDirectory, time.Since(setup))
	path := filepath.Join(project, "safe.txt")
	writeFile(t, path, "large-tree-ok")
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep", "Bash"} {
		if err := CheckProtectedPaths(tool, map[string]any{"path": path, "command": "echo large-tree-ok"}, project); err != nil {
			t.Fatalf("%s refused a project with a large unrelated Library: %v", tool, err)
		}
	}
	for _, item := range []struct {
		tool  Tool
		input map[string]any
	}{
		{NewReadTool(), map[string]any{"path": path}},
		{NewWriteTool(nil), map[string]any{"path": filepath.Join(project, "written.txt"), "content": "ok"}},
		{NewGlobTool(), map[string]any{"path": project, "pattern": "**/*.txt"}},
		{NewGrepTool(), map[string]any{"path": project, "pattern": "large-tree-ok"}},
		{NewBashTool(), map[string]any{"command": "echo large-tree-ok"}},
	} {
		t.Run(item.tool.Name(), func(t *testing.T) {
			if item.tool.Name() == "Grep" {
				if _, err := exec.LookPath("rg"); err != nil {
					t.Skip("ripgrep unavailable; other protected-path checks still run")
				}
			}
			result, err := item.tool.Execute(ctx, ToolCall{WorkDir: project, Input: item.input})
			if err != nil || result.IsError {
				t.Fatalf("%s with large Library: %v %+v", item.tool.Name(), err, result)
			}
		})
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("unrelated tools took %s with a large Library; budget is 5s", elapsed)
	} else {
		t.Logf("all tool checks and executions completed in %s", elapsed)
	}
}
