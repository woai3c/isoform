// Package projectcontext supplies project guidance only for an Isoform run.
package projectcontext

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

//go:embed instructions.md
var instructions string

// WithPrompt carries the guidance previously appended to agent instruction
// files. It only locates project-local context; it never reads that context
// or writes instructions into the project. Ordinary repositories and runs with
// no project directory receive no additional guidance.
func WithPrompt(prompt, workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return prompt
	}
	contextDir := findContextDir(workDir)
	if contextDir == "" {
		return prompt
	}
	// Runs in a subdirectory or linked worktree must read the original local
	// context without copying it into the execution directory.
	guidance := strings.ReplaceAll(instructions, ".something/", filepath.ToSlash(contextDir)+"/")
	return "# Isoform Project Context\n\n" + guidance + "\n\n" + prompt
}

func findContextDir(workDir string) string {
	dir, err := filepath.Abs(workDir)
	if err != nil {
		return ""
	}
	for {
		contextDir := filepath.Join(dir, ".something")
		if info, err := os.Stat(contextDir); err == nil && info.IsDir() {
			return contextDir
		}
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil {
			if !info.IsDir() {
				return linkedContextDir(dir, gitPath)
			}
			// Never borrow context from a containing, unrelated repository.
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Linked worktrees point to <main>/.git/worktrees/<name>. Its commondir
// identifies the main repository, where clone/init leave local context.
func linkedContextDir(worktreeRoot, gitPath string) string {
	data, err := os.ReadFile(gitPath)
	if err != nil || !strings.HasPrefix(string(data), "gitdir: ") {
		return ""
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreeRoot, gitDir)
	}
	common, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil || strings.TrimSpace(string(common)) == "" {
		return ""
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) != ".git" {
		return ""
	}
	contextDir := filepath.Join(filepath.Dir(commonDir), ".something")
	if info, err := os.Stat(contextDir); err == nil && info.IsDir() {
		return contextDir
	}
	return ""
}
