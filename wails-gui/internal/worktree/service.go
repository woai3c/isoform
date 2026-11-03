package worktree

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wails-gui/storage"

	"wails-gui/internal/apphome"
)

// BaseDirName is the folder under the home directory that holds managed
// worktrees by default. The app sets it per build flavor.
var BaseDirName = ".yansu-agent"

// WorkspaceInfo represents a workspace backed by a git worktree.
type WorkspaceInfo struct {
	WorkspaceID string `json:"workspaceId"`
	Label       string `json:"label"`
	Branch      string `json:"branch"`
	BaseBranch  string `json:"baseBranch"`
	Path        string `json:"path"`
	CreatedAt   int64  `json:"createdAt"`
}

// RenameResult is returned by RenameWorkspaceBranch with the new branch and path.
type RenameResult struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
}

// Dependencies defines callbacks required by the worktree service.
type Dependencies struct {
	IsGitRepo              func(path string) bool
	WorktreeBaseDir        func() (string, error)
	DefaultWorktreeBaseDir func() (string, error)
	BranchPrefix           func() string
	// Translator converts a non-ASCII workspace label into a short English
	// phrase used for git branch naming. Optional: when nil, the original
	// label passes through and SanitizeBranchName does its best with the
	// unicode letters directly.
	Translator func(prompt string) (string, error)
}

// Service contains worktree read/write operations.
type Service struct {
	store                  *storage.Store
	isGitRepo              func(path string) bool
	worktreeBaseDir        func() (string, error)
	defaultWorktreeBaseDir func() (string, error)
	branchPrefix           func() string
	translator             func(prompt string) (string, error)
}

// NewService creates a worktree service.
func NewService(store *storage.Store, deps Dependencies) *Service {
	s := &Service{
		store:                  store,
		isGitRepo:              deps.IsGitRepo,
		worktreeBaseDir:        deps.WorktreeBaseDir,
		defaultWorktreeBaseDir: deps.DefaultWorktreeBaseDir,
		branchPrefix:           deps.BranchPrefix,
		translator:             deps.Translator,
	}
	if s.isGitRepo == nil {
		s.isGitRepo = func(path string) bool {
			if strings.TrimSpace(path) == "" {
				return false
			}
			_, err := RunGit(path, "rev-parse", "--show-toplevel")
			return err == nil
		}
	}
	if s.worktreeBaseDir == nil {
		s.worktreeBaseDir = func() (string, error) {
			dataDir, err := apphome.Dir(BaseDirName)
			if err != nil {
				return "", err
			}
			return filepath.Join(dataDir, "worktrees"), nil
		}
	}
	if s.defaultWorktreeBaseDir == nil {
		s.defaultWorktreeBaseDir = s.worktreeBaseDir
	}
	if s.branchPrefix == nil {
		s.branchPrefix = func() string { return "workspace" }
	}
	return s
}

// CreateWorkspace creates a new git worktree-backed workspace.
func (s *Service) CreateWorkspace(projectPath, projectID, workspaceID, label, baseBranch string) (*WorkspaceInfo, error) {
	if !s.isGitRepo(projectPath) {
		return nil, fmt.Errorf("not a git repository: %s", projectPath)
	}

	// Best-effort fetch so the new worktree starts from the latest remote code.
	if _, err := RunGit(projectPath, "fetch", "origin", baseBranch); err != nil {
		log.Printf("[workspace] fetch origin %s failed (continuing with local): %v", baseBranch, err)
	}

	resolvedBaseBranch, err := s.resolveWorktreeBaseBranch(projectPath, baseBranch)
	if err != nil {
		return nil, fmt.Errorf("resolve base branch: %w", err)
	}
	if resolvedBaseBranch != baseBranch {
		log.Printf("[workspace] requested base branch %q unavailable; using %q", baseBranch, resolvedBaseBranch)
	}

	branch := UniqueBranchName(projectPath, s.worktreeBranchName(label))
	// Directory name is derived from the branch suffix (part after prefix/).
	branchSuffix := label
	if idx := strings.LastIndex(branch, "/"); idx >= 0 {
		branchSuffix = branch[idx+1:]
	}
	wtPath, err := s.worktreeDirPath(filepath.Base(projectPath), branchSuffix)
	if err != nil {
		return nil, fmt.Errorf("compute worktree path: %w", err)
	}
	wtPath = UniqueDirPath(wtPath)

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o700); err != nil {
		return nil, fmt.Errorf("create worktree parent dir: %w", err)
	}

	// Create worktree with a new branch from baseBranch.
	if _, err := RunGit(projectPath, "worktree", "add", "-b", branch, wtPath, resolvedBaseBranch); err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	now := time.Now().UnixMilli()

	// Persist workspace row.
	if s.store != nil {
		if err := s.store.SaveWorkspace(context.Background(), storage.WorkspaceRow{
			ID:             workspaceID,
			ProjectID:      projectID,
			Label:          label,
			WorktreePath:   wtPath,
			WorktreeBranch: branch,
			BaseBranch:     resolvedBaseBranch,
			CreatedAt:      now,
			UpdatedAt:      now,
		}); err != nil {
			// Clean up worktree on DB failure.
			RunGit(projectPath, "worktree", "remove", "--force", wtPath) //nolint:errcheck
			return nil, fmt.Errorf("persist workspace: %w", err)
		}
	}

	return &WorkspaceInfo{
		WorkspaceID: workspaceID,
		Label:       label,
		Branch:      branch,
		BaseBranch:  resolvedBaseBranch,
		Path:        wtPath,
		CreatedAt:   now,
	}, nil
}

// RemoveWorkspace removes a git worktree and cleans up its branch and DB record.
func (s *Service) RemoveWorkspace(projectPath, workspaceID string) error {
	if s.store == nil {
		return fmt.Errorf("store not initialized")
	}

	ws, err := s.store.LoadWorkspace(context.Background(), workspaceID)
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}
	if ws.IsDefault {
		return fmt.Errorf("cannot remove the default workspace")
	}

	// Remove worktree (force in case of uncommitted changes).
	if ws.WorktreePath != "" {
		if _, err := RunGit(projectPath, "worktree", "remove", "--force", ws.WorktreePath); err != nil {
			log.Printf("[workspace] worktree remove failed: %v", err)
			// Fall through to cleanup directory manually.
			if rmErr := RemovePathSafe(ws.WorktreePath, s.managedWorktreeRoots()); rmErr != nil {
				log.Printf("[workspace] fallback remove skipped: %v", rmErr)
			}
		}
	}

	// Delete the branch.
	if ws.WorktreeBranch != "" {
		if _, err := RunGit(projectPath, "branch", "-D", ws.WorktreeBranch); err != nil {
			log.Printf("[workspace] branch delete failed (non-fatal): %v", err)
		}
	}

	// Remove DB record (cascade-deletes sessions and stream items).
	return s.store.DeleteWorkspace(context.Background(), workspaceID)
}

// ListWorkspaces returns all workspaces for a project.
func (s *Service) ListWorkspaces(projectID string) ([]WorkspaceInfo, error) {
	if s.store == nil {
		return nil, nil
	}

	rows, err := s.store.LoadWorkspaces(context.Background(), projectID)
	if err != nil {
		return nil, err
	}

	var workspaces []WorkspaceInfo
	for _, w := range rows {
		workspaces = append(workspaces, WorkspaceInfo{
			WorkspaceID: w.ID,
			Label:       w.Label,
			Branch:      w.WorktreeBranch,
			BaseBranch:  w.BaseBranch,
			Path:        w.WorktreePath,
			CreatedAt:   w.CreatedAt,
		})
	}
	return workspaces, nil
}

// RenameWorkspaceBranch renames the workspace's label, git branch, and worktree directory.
func (s *Service) RenameWorkspaceBranch(projectPath, workspaceID, newLabel string) (*RenameResult, error) {
	if s.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	ws, err := s.store.LoadWorkspace(context.Background(), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	if ws.IsDefault || ws.WorktreeBranch == "" {
		return nil, fmt.Errorf("cannot rename default workspace branch")
	}

	translated := TranslateWith(newLabel, s.translator)
	newSuffix := SanitizeBranchName(translated)
	baseBranch := s.branchPrefix() + "/" + newSuffix
	newBranch := UniqueBranchName(projectPath, baseBranch)

	// Avoid no-op.
	if newBranch == ws.WorktreeBranch {
		return &RenameResult{Branch: newBranch, Path: ws.WorktreePath}, nil
	}

	// Derive the actual suffix from the (possibly deduped) branch name for directory naming.
	if idx := strings.LastIndex(newBranch, "/"); idx >= 0 {
		newSuffix = newBranch[idx+1:]
	}

	oldBranch := ws.WorktreeBranch
	oldPath := ws.WorktreePath

	// Rename git branch.
	if _, err := RunGit(projectPath, "branch", "-m", oldBranch, newBranch); err != nil {
		return nil, fmt.Errorf("rename branch: %w", err)
	}

	// Move worktree directory to match the new branch suffix.
	newPath := oldPath // fallback: keep old path if move fails.
	if oldPath != "" {
		parentDir := filepath.Dir(oldPath)
		candidate := UniqueDirPath(filepath.Join(parentDir, newSuffix))
		if candidate != oldPath {
			if _, err := RunGit(projectPath, "worktree", "move", oldPath, candidate); err != nil {
				log.Printf("[workspace rename] worktree move failed (non-fatal): %v", err)
			} else {
				newPath = candidate
			}
		}
	}

	// Update DB.
	ws.Label = newLabel
	ws.WorktreeBranch = newBranch
	ws.WorktreePath = newPath
	if err := s.store.SaveWorkspace(context.Background(), *ws); err != nil {
		// Try to rollback branch rename and worktree move.
		RunGit(projectPath, "branch", "-m", newBranch, oldBranch) //nolint:errcheck
		if newPath != oldPath {
			RunGit(projectPath, "worktree", "move", newPath, oldPath) //nolint:errcheck
		}
		return nil, fmt.Errorf("persist rename: %w", err)
	}

	return &RenameResult{Branch: newBranch, Path: newPath}, nil
}

// MergeWorkspace merges a workspace branch into main worktree and cleans up.
func (s *Service) MergeWorkspace(projectPath, workspaceID, worktreePath, branch string) error {
	// 1. Stage and commit uncommitted changes in worktree.
	statusOut, _ := RunGit(worktreePath, "status", "--porcelain")
	if strings.TrimSpace(statusOut) != "" {
		if _, err := RunGit(worktreePath, "add", "-A"); err != nil {
			return fmt.Errorf("stage changes: %w", err)
		}
		if _, err := RunGit(worktreePath, "commit", "-m", "workspace: changes from "+workspaceID); err != nil {
			return fmt.Errorf("commit changes: %w", err)
		}
	}

	// 2. Merge workspace branch into main worktree's HEAD.
	if _, err := RunGit(projectPath, "merge", branch, "--no-ff", "-m", "Merge workspace "+workspaceID); err != nil {
		return fmt.Errorf("merge workspace branch %s: %w", branch, err)
	}

	// 3. Remove worktree.
	if _, err := RunGit(projectPath, "worktree", "remove", worktreePath); err != nil {
		log.Printf("[workspace] worktree remove after merge failed (non-fatal): %v", err)
		if rmErr := RemovePathSafe(worktreePath, s.managedWorktreeRoots()); rmErr != nil {
			log.Printf("[workspace] fallback remove skipped: %v", rmErr)
		}
	}

	// 4. Delete the workspace branch.
	if _, err := RunGit(projectPath, "branch", "-d", branch); err != nil {
		log.Printf("[workspace] branch delete after merge failed (non-fatal): %v", err)
	}

	// 5. Remove workspace from DB (cascade-deletes sessions and items).
	if s.store != nil {
		if err := s.store.DeleteWorkspace(context.Background(), workspaceID); err != nil {
			log.Printf("[workspace] delete workspace after merge failed: %v", err)
		}
	}

	return nil
}

func (s *Service) worktreeBranchName(workspaceID string) string {
	return s.branchPrefix() + "/" + workspaceID
}

func (s *Service) worktreeDirPath(projectName, branchSuffix string) (string, error) {
	base, err := s.worktreeBaseDir()
	if err != nil {
		return "", err
	}
	projDir := SanitizeBranchName(projectName)
	if projDir == "" {
		projDir = "project"
	}
	wsDir := SanitizeBranchName(branchSuffix)
	if wsDir == "" {
		wsDir = "workspace"
	}
	return filepath.Join(base, projDir, wsDir), nil
}

func (s *Service) managedWorktreeRoots() []string {
	seen := make(map[string]struct{}, 2)
	roots := make([]string, 0, 2)

	appendRoot := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return
		}
		absRoot = filepath.Clean(absRoot)
		if _, ok := seen[absRoot]; ok {
			return
		}
		seen[absRoot] = struct{}{}
		roots = append(roots, absRoot)
	}

	if configured, err := s.worktreeBaseDir(); err == nil {
		appendRoot(configured)
	}
	if fallback, err := s.defaultWorktreeBaseDir(); err == nil {
		appendRoot(fallback)
	}

	return roots
}

func appendUniqueRef(candidates []string, seen map[string]struct{}, ref string) []string {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return candidates
	}
	if _, ok := seen[trimmed]; ok {
		return candidates
	}
	seen[trimmed] = struct{}{}
	return append(candidates, trimmed)
}

func gitRefExists(projectPath, ref string) bool {
	if strings.TrimSpace(ref) == "" {
		return false
	}
	_, err := RunGit(projectPath, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

func (s *Service) resolveWorktreeBaseBranch(projectPath, requested string) (string, error) {
	seen := make(map[string]struct{}, 8)
	candidates := make([]string, 0, 8)

	// Prefer the remote tracking branch so the worktree starts from the
	// latest fetched code rather than a potentially stale local branch.
	if requested != "" && !strings.HasPrefix(requested, "origin/") {
		candidates = appendUniqueRef(candidates, seen, "origin/"+requested)
	}
	candidates = appendUniqueRef(candidates, seen, requested)

	if out, err := RunGit(projectPath, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		candidates = appendUniqueRef(candidates, seen, out)
	}
	if out, err := RunGit(projectPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		out = strings.TrimSpace(out)
		candidates = appendUniqueRef(candidates, seen, out)
		if local, ok := strings.CutPrefix(out, "origin/"); ok {
			candidates = appendUniqueRef(candidates, seen, local)
		}
	}

	candidates = appendUniqueRef(candidates, seen, "main")
	candidates = appendUniqueRef(candidates, seen, "master")
	candidates = appendUniqueRef(candidates, seen, "HEAD")

	for _, candidate := range candidates {
		if gitRefExists(projectPath, candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no valid base branch found (tried %s); does the repository have at least one commit?",
		strings.Join(candidates, ", "))
}
