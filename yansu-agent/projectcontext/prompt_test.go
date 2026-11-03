package projectcontext

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectPromptIsPerRun(t *testing.T) {
	root := t.TempDir()
	prompt := "Implement the requested fix."
	if got := WithPrompt(prompt, ""); got != prompt {
		t.Fatal("a run without a project acquired project instructions")
	}
	if got := WithPrompt(prompt, root); got != prompt {
		t.Fatal("an ordinary repository acquired project instructions")
	}
	contextDir := filepath.Join(root, ".something")
	if err := os.Mkdir(contextDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "context.md"), []byte("private project content"), 0600); err != nil {
		t.Fatal(err)
	}
	got := WithPrompt(prompt, root)
	for _, want := range []string{"# Isoform Project Context", ".something/context.md", ".something/prd.md", ".something/scenarios/", ".something/knowledge/_index.md", ".something/new-knowledge/", prompt} {
		if !strings.Contains(got, want) {
			t.Errorf("run prompt lost %q", want)
		}
	}
	if strings.Contains(got, "private project content") {
		t.Fatal("project documents were copied into the prompt")
	}
	if err := os.RemoveAll(contextDir); err != nil {
		t.Fatal(err)
	}
	if got := WithPrompt(prompt, root); got != prompt {
		t.Fatal("project context was retained across runs")
	}
	if err := os.WriteFile(contextDir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := WithPrompt(prompt, root); got != prompt {
		t.Fatal("a file named .something enabled project context")
	}
}

func TestProjectPromptFindsWorktreeAndSubdirectoryContext(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", root}, {"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	contextDir := filepath.Join(root, ".something")
	if err := os.Mkdir(contextDir, 0755); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "workspace")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", worktree).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v: %s", err, out)
	}
	for _, workDir := range []string{filepath.Join(root, "nested"), worktree, filepath.Join(worktree, "nested")} {
		if err := os.MkdirAll(workDir, 0755); err != nil {
			t.Fatal(err)
		}
		prompt := WithPrompt("Fix the project", workDir)
		if !strings.Contains(prompt, filepath.ToSlash(filepath.Join(contextDir, "context.md"))) || !strings.Contains(prompt, "Fix the project") {
			t.Errorf("run in %s lost original context location: %q", workDir, prompt)
		}
	}
	if _, err := os.Stat(filepath.Join(worktree, ".something")); !os.IsNotExist(err) {
		t.Fatalf("context was copied into the execution worktree: %v", err)
	}
	// A nested repository owns its own context boundary, even when the parent
	// project has context. Broken worktree metadata fails closed as well.
	for _, state := range []string{"repository", "broken-worktree"} {
		dir := filepath.Join(root, state)
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if state == "repository" {
			if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: missing"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := WithPrompt("Fix the project", dir); got != "Fix the project" {
			t.Errorf("%s borrowed unrelated context", state)
		}
	}
}
