package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skratchdot/open-golang/open"
)

// openableDeliverableExtensions is the whitelist of file extensions that a
// one-click on a deliverable chip is allowed to hand off to the system
// default application. Executables (.sh, .command, .bat, .exe, ...) are
// intentionally excluded so an LLM-generated automation cannot tempt the
// user into one-click code execution by surfacing a script as a
// "deliverable".
var openableDeliverableExtensions = map[string]struct{}{
	".pdf":  {},
	".csv":  {},
	".json": {},
	".png":  {},
	".jpg":  {},
	".jpeg": {},
	".gif":  {},
	".pptx": {},
	".xlsx": {},
	".docx": {},
	".zip":  {},
	".md":   {},
	".txt":  {},
	".html": {},
	".htm":  {},
}

func isOpenableDeliverableExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := openableDeliverableExtensions[ext]
	return ok
}

// peekSessionTempDir returns the cached temp dir for a session tag without
// creating one. Used by validators that must be side-effect-free.
func (a *App) peekSessionTempDir(tag string) string {
	if tag == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionTempDirs[tag]
}

// defaultAutomationWorkDir returns (and lazily creates) the stable per-job
// work directory used by a cron job whose target config does not pin a
// projectDir. Outputs live under ~/.isoform-workspaces/<flavor>/automations/<jobID>/ so
// they survive app restart, stay addressable from the UI, and give the user a
// single well-known place to browse historical artifacts.
func (a *App) defaultAutomationWorkDir(jobID string) (string, error) {
	return prepareAgentWorkspace("automations", jobID)
}

// OpenAutomationDeliverable opens a file produced by an automation (cron)
// run in the system's default application.
//
// Safety model:
//   - Path must be contained within the job's workDir, the session's temp
//     dir, or the user's home directory (same allow-list used by
//     validateDeliverablePath for DM attachments).
//   - Extension must be on the openable whitelist so a stray shell script
//     or Windows executable cannot be one-click executed.
func (a *App) OpenAutomationDeliverable(jobID string, path string) error {
	absPath, err := a.resolveAutomationDeliverablePath(jobID, path)
	if err != nil {
		return err
	}
	return open.Start(absPath)
}

// resolveAutomationDeliverablePath performs the same validation as opening a
// deliverable, without handing the file to another application.
func (a *App) resolveAutomationDeliverablePath(jobID string, path string) (string, error) {
	if jobID == "" || path == "" {
		return "", fmt.Errorf("job ID and path are required")
	}
	if a.store == nil {
		return "", fmt.Errorf("store not initialized")
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	job, err := a.store.LoadCronJob(ctx, jobID)
	if err != nil {
		return "", fmt.Errorf("job not found: %w", err)
	}

	var cfg cronTargetConfig
	if err := json.Unmarshal([]byte(job.TargetConfig), &cfg); err != nil {
		return "", fmt.Errorf("parse target config: %w", err)
	}
	workDir := cfg.ProjectDir
	if workDir == "" {
		// Mirror executeACPJob: unpinned jobs default to the stable
		// per-job directory outside the private app data roots. Resolving the same
		// path here keeps the validator's allow-list aligned with where
		// the agent actually writes. If we can't resolve it we must
		// abort — otherwise workDir stays empty, relative paths fail to
		// resolve, and validateDeliverablePath reports a misleading
		// "outside allowed directories" error.
		dir, derr := a.defaultAutomationWorkDir(jobID)
		if derr != nil {
			return "", fmt.Errorf("resolve default work dir: %w", derr)
		}
		workDir = dir
	}
	sessionTempDir := a.peekSessionTempDir(cfg.SessionTag)

	// Mirror readDeliverableFiles: resolve relative paths against workDir.
	absPath := path
	if !filepath.IsAbs(path) && workDir != "" {
		absPath = filepath.Join(workDir, path)
	}

	// Order matters: validateDeliverablePath calls filepath.EvalSymlinks,
	// which fails for nonexistent files. If we checked containment first
	// the user would see a misleading "outside allowed directories" error
	// for a simple missing file. Check existence first so each error
	// message reflects the real problem.
	if _, err := os.Stat(absPath); err != nil {
		return "", fmt.Errorf("file does not exist: %w", err)
	}

	if !validateDeliverablePath(absPath, workDir, sessionTempDir) {
		return "", fmt.Errorf("path is outside the allowed directories for this automation")
	}

	if !isOpenableDeliverableExt(absPath) {
		return "", fmt.Errorf("file type is not openable from this UI")
	}

	return absPath, nil
}
