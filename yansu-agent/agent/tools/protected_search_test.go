package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProtectedDirectToolsResolveAliases(t *testing.T) {
	home, _ := hardBlockHome(t)
	key := filepath.Join(home, ".ssh", "id_rsa")
	aliases := []string{key}
	link := filepath.Join(home, "key-alias")
	if err := os.Symlink(filepath.Dir(key), link); err != nil {
		t.Fatal(err)
	}
	aliases = append(aliases, filepath.Join(link, filepath.Base(key)))
	if runtime.GOOS == "darwin" {
		aliases = append(aliases, strings.ToLower(firmlinkPrefix)+key, strings.ToUpper(key))
	}
	tools := []Tool{NewReadTool(), NewWriteTool(nil), NewEditTool(nil), NewNotebookEditTool(nil), NewGlobTool(), NewGrepTool()}
	for _, tool := range tools {
		for _, alias := range aliases {
			_, err := tool.Execute(context.Background(), ToolCall{WorkDir: home, Input: map[string]any{
				"path": alias, "pattern": "KEY", "content": "changed", "old_string": "KEY", "new_string": "changed",
			}})
			if _, ok := err.(*HardBlockedPathError); !ok {
				t.Errorf("%s %s: got %v, want hard refusal", tool.Name(), alias, err)
			}
		}
	}
	if data, err := os.ReadFile(key); err != nil || string(data) != "KEY" {
		t.Fatalf("private key changed: %q, %v", data, err)
	}
}

func TestProtectedCredentialRootsResolveSymlinks(t *testing.T) {
	home, _ := hardBlockHome(t)
	ssh := filepath.Join(home, ".ssh")
	target := filepath.Join(home, "moved-keys")
	if err := os.Rename(ssh, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ssh); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(ssh, "id_rsa"), filepath.Join(target, "id_rsa"), filepath.Join(target, "new-key")} {
		if err := CheckProtectedPaths("Write", map[string]any{"path": path}, home); err == nil {
			t.Errorf("credential root alias was allowed: %s", path)
		}
	}
	for _, name := range []string{"id_rsa.pub", "known_hosts", "config"} {
		if err := CheckProtectedPaths("Read", map[string]any{"path": filepath.Join(target, name)}, home); err != nil {
			t.Errorf("public SSH file refused: %s: %v", name, err)
		}
	}
}

func TestProtectedDirectDataTools(t *testing.T) {
	home, project := hardBlockHome(t)
	data := filepath.Join(home, ".isoform-agent")
	link := filepath.Join(project, "data-link")
	if err := os.Symlink(data, link); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(data, "cron-secret"), filepath.Join(link, "cron-secret"), filepath.Join(link, "new", "private.key")}
	if runtime.GOOS == "darwin" {
		paths = append(paths, strings.ToLower(firmlinkPrefix)+filepath.Join(data, "cron-secret"))
	}
	for _, tool := range []Tool{NewReadTool(), NewWriteTool(nil), NewEditTool(nil), NewNotebookEditTool(nil), NewGlobTool(), NewGrepTool()} {
		for _, path := range paths {
			_, err := tool.Execute(context.Background(), ToolCall{WorkDir: home, Input: map[string]any{
				"path": path, "pattern": "s", "content": "changed", "old_string": "s", "new_string": "changed",
			}})
			if _, ok := err.(*ProtectedPathError); !ok {
				t.Errorf("%s %s: got %v, want protected-data refusal", tool.Name(), path, err)
			}
		}
	}
}

func TestProtectedSearchSkipsSecrets(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, path := range []string{
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, "Library", "Keychains", "login.keychain-db"),
		filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Default", "Cookies"),
		filepath.Join(home, ".isoform-agent", "cron-secret"),
	} {
		writeFile(t, path, "PRIVATE_SENTINEL")
	}
	writeFile(t, filepath.Join(project, "safe.go"), "cron-secret is a protected filename\n")
	writeFile(t, filepath.Join(project, "logins.json"), "project fixture\n")
	if err := os.Symlink(filepath.Join(home, ".isoform-agent"), filepath.Join(project, "data-alias")); err != nil {
		t.Fatal(err)
	}
	glob, err := NewGlobTool().Execute(context.Background(), ToolCall{WorkDir: home, Input: map[string]any{"path": home, "pattern": "**/*"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []string{"id_rsa\n", "login.keychain-db", "Default/Cookies", "cron-secret", "data-alias"} {
		if strings.Contains(glob.Content, blocked) {
			t.Errorf("Glob returned protected entry %q: %s", blocked, glob.Content)
		}
	}
	for _, allowed := range []string{"safe.go", "logins.json", "id_rsa.pub", "known_hosts", ".ssh/config"} {
		if !strings.Contains(glob.Content, allowed) {
			t.Errorf("Glob omitted safe entry %q: %s", allowed, glob.Content)
		}
	}
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) is not on PATH: real Grep execution cannot run; exclusion-builder regressions still run")
	}
	for _, pattern := range []string{"PRIVATE_SENTINEL", "cron-secret"} {
		result, err := NewGrepTool().Execute(context.Background(), ToolCall{WorkDir: home, Input: map[string]any{
			"path": home, "pattern": pattern, "glob": "**/*", "output_mode": "content",
		}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Content, "PRIVATE_SENTINEL") {
			t.Fatalf("Grep leaked credential contents: %s", result.Content)
		}
		if pattern == "cron-secret" && !strings.Contains(result.Content, "protected filename") {
			t.Errorf("Grep could not search source text cron-secret: %s", result.Content)
		}
	}
}

func TestProtectedGrepEscapesExcludePatterns(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) is not on PATH: real Grep execution cannot run; exclusion-builder regressions still run")
	}
	home, _ := hardBlockHome(t)
	data := filepath.Join(home, "data[1]{x}?")
	t.Setenv("YANSU_AGENT_HOME", data)
	writeFile(t, filepath.Join(data, "cron-secret"), "PRIVATE_SENTINEL")
	writeFile(t, filepath.Join(home, "safe.txt"), "safe line")
	result, err := NewGrepTool().Execute(context.Background(), ToolCall{WorkDir: home, Input: map[string]any{
		"path": home, "pattern": "PRIVATE_SENTINEL|safe line", "glob": "**/*", "output_mode": "content",
	}})
	if err != nil || strings.Contains(result.Content, "PRIVATE_SENTINEL") || !strings.Contains(result.Content, "safe line") {
		t.Fatalf("Grep exclusions: %q, %v", result.Content, err)
	}
}

func TestProtectedGrepFiltersSymlinkRoot(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) is not on PATH: real Grep execution cannot run; exclusion-builder regressions still run")
	}
	home, project := hardBlockHome(t)
	writeFile(t, filepath.Join(home, "Library", "Keychains", "login.keychain-db"), "PRIVATE_SENTINEL")
	writeFile(t, filepath.Join(project, "safe.txt"), "safe line")
	alias := filepath.Join(project, "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	result, err := NewGrepTool().Execute(context.Background(), ToolCall{WorkDir: home, Input: map[string]any{
		"path": alias, "pattern": "PRIVATE_SENTINEL|safe line", "glob": "**/*", "output_mode": "content",
	}})
	if err != nil || strings.Contains(result.Content, "PRIVATE_SENTINEL") || !strings.Contains(result.Content, "safe line") {
		t.Fatalf("Grep symlink root filtering: %q, %v", result.Content, err)
	}
}

func TestProtectedNamesAndCodeSearchRemainAllowed(t *testing.T) {
	_, project := hardBlockHome(t)
	for _, command := range []string{"ls ~/.ssh", "ls -la ~/.ssh", "rg cron-secret .", "grep -R 'cron-secret' ."} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
			t.Errorf("%s: %v", command, err)
		}
	}
	for _, command := range []string{"cp -r ~/.ssh ./keys", "ls ~/.ssh && cat ~/.ssh/id_rsa", "ls -R ~/.ssh", "./ls ~/.ssh"} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
			t.Errorf("unsafe SSH read allowed: %s", command)
		}
	}
}

func TestProtectedPathsFailClosedWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("YANSU_AGENT_HOME", "")
	for _, name := range []string{"Bash", "Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"} {
		if err := CheckProtectedPaths(name, map[string]any{}, ""); err == nil {
			t.Errorf("%s allowed without HOME", name)
		}
	}
	if err := CheckProtectedPaths("WebSearch", map[string]any{}, ""); err != nil {
		t.Fatalf("unrelated tool refused: %v", err)
	}
}

func TestProtectedGrepCredentialRoots(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) is not on PATH: real Grep execution cannot run; exclusion-builder regressions still run")
	}
	home, _ := hardBlockHome(t)
	for _, relative := range []string{
		".config/gh/hosts.yml", ".aws/credentials", ".mozilla/firefox/p/logins.json",
		".docker/config.json", ".kube/config",
	} {
		writeFile(t, filepath.Join(home, filepath.FromSlash(relative)), "PRIVATE_SENTINEL")
	}
	for _, tc := range []struct{ root, glob string }{
		{".config", "**/*"}, {".aws", "**/*"}, {".mozilla/firefox/p", "logins.json"},
		{".docker", "**/*"}, {".kube", "**/*"},
	} {
		t.Run(tc.root, func(t *testing.T) {
			root := filepath.Join(home, filepath.FromSlash(tc.root))
			result, err := NewGrepTool().Execute(context.Background(), ToolCall{WorkDir: home, Input: map[string]any{
				"path": root, "pattern": "PRIVATE_SENTINEL", "glob": tc.glob, "output_mode": "content",
			}})
			if err != nil || strings.Contains(result.Content, "PRIVATE_SENTINEL") {
				t.Fatalf("Grep credential subtree: %q, %v", result.Content, err)
			}
		})
	}
}
