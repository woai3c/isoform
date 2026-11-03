package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"wails-gui/internal/platform/cmdutil"
)

// GitInfo represents git repository information for a project directory.
type GitInfo struct {
	IsGitRepo     bool   `json:"isGitRepo"`
	CurrentBranch string `json:"currentBranch"`
	RemoteURL     string `json:"remoteUrl,omitempty"`
	RootPath      string `json:"rootPath"`
}

// IsGitRepo checks if the given path is the root of a git repository.
// Returns false for subdirectories that happen to be inside a parent git repo.
func (a *App) IsGitRepo(path string) bool {
	if path == "" {
		return false
	}
	cmd := cmdutil.GitCommand("rev-parse", "--show-toplevel")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	rootPath := filepath.Clean(strings.TrimSpace(string(out)))
	return rootPath == filepath.Clean(path)
}

// GetGitInfo returns git information for the given directory.
// Returns a GitInfo with IsGitRepo=false if the path is not a git repo.
func (a *App) GetGitInfo(path string) GitInfo {
	info := GitInfo{IsGitRepo: false}

	if path == "" {
		return info
	}

	// Get repo root path and verify this directory is the actual repo root
	// (not just a subdirectory inside a parent git repo).
	cmd := cmdutil.GitCommand("rev-parse", "--show-toplevel")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return info
	}
	info.RootPath = strings.TrimSpace(string(out))
	if filepath.Clean(info.RootPath) != filepath.Clean(path) {
		return info
	}
	info.IsGitRepo = true

	// Get current branch.
	cmd = cmdutil.GitCommand("rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = path
	if out, err := cmd.Output(); err == nil {
		info.CurrentBranch = strings.TrimSpace(string(out))
	}

	// Get remote URL (best-effort).
	cmd = cmdutil.GitCommand("remote", "get-url", "origin")
	cmd.Dir = path
	if out, err := cmd.Output(); err == nil {
		info.RemoteURL = strings.TrimSpace(string(out))
	}

	return info
}

// ParseGitRemoteURL parses a git remote URL into its host, owner, and repo components.
// Supported formats:
//   - git@github.com:org/repo.git          (SCP-style)
//   - https://github.com/org/repo.git      (HTTPS with .git)
//   - https://github.com/org/repo          (HTTPS without .git)
//   - ssh://git@github.com:22/org/repo.git (SSH with port)
func ParseGitRemoteURL(remoteURL string) (host, owner, repo string, err error) {
	raw := strings.TrimSpace(remoteURL)
	if raw == "" {
		return "", "", "", errors.New("empty remote URL")
	}

	var pathPart string

	switch {
	// SCP-style: git@host:owner/repo.git
	case strings.Contains(raw, "://"):
		// Standard URL (https://, ssh://, git://).
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "", "", "", fmt.Errorf("parse URL: %w", parseErr)
		}
		host = u.Hostname()
		pathPart = strings.TrimPrefix(u.Path, "/")

	default:
		// SCP-style: user@host:path
		at := strings.Index(raw, "@")
		colon := strings.Index(raw, ":")
		if at < 0 || colon < 0 || colon <= at {
			return "", "", "", fmt.Errorf("unsupported remote URL format: %s", raw)
		}
		host = raw[at+1 : colon]
		pathPart = raw[colon+1:]
	}

	// Strip trailing .git suffix.
	pathPart = strings.TrimSuffix(pathPart, ".git")

	parts := strings.Split(pathPart, "/")
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("could not extract owner/repo from path: %s", pathPart)
	}

	// Take the last two segments as owner and repo to handle paths like /org/repo
	// as well as SSH port-prefixed paths that may have extra segments.
	owner = parts[len(parts)-2]
	repo = parts[len(parts)-1]

	if host == "" || owner == "" || repo == "" {
		return "", "", "", fmt.Errorf("incomplete remote URL: host=%q owner=%q repo=%q", host, owner, repo)
	}

	return host, owner, repo, nil
}

// BuildRepoURL converts any git remote URL into a browser-friendly HTTPS URL.
// For example, git@github.com:org/repo.git becomes https://github.com/org/repo.
func BuildRepoURL(remoteURL string) (string, error) {
	host, owner, repo, err := ParseGitRemoteURL(remoteURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s/%s/%s", host, owner, repo), nil
}

// BuildPRCreateURL constructs a "Create Pull/Merge Request" URL for the given remote.
// GitHub remotes produce a compare URL; GitLab remotes produce a merge-request URL.
func (a *App) BuildPRCreateURL(remoteURL, head, base string) (string, error) {
	return buildPRCreateURL(remoteURL, head, base)
}

func buildPRCreateURL(remoteURL, head, base string) (string, error) {
	host, owner, repo, err := ParseGitRemoteURL(remoteURL)
	if err != nil {
		return "", err
	}

	switch {
	case strings.Contains(host, "gitlab"):
		return fmt.Sprintf(
			"https://%s/%s/%s/-/merge_requests/new?merge_request[source_branch]=%s&merge_request[target_branch]=%s",
			host, owner, repo,
			url.QueryEscape(head),
			url.QueryEscape(base),
		), nil
	default:
		// GitHub-style compare URL.
		return fmt.Sprintf(
			"https://%s/%s/%s/compare/%s...%s?expand=1",
			host, owner, repo,
			url.PathEscape(base),
			url.PathEscape(head),
		), nil
	}
}

// PRInfo represents the status of a pull/merge request for a branch.
type PRInfo struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // "open" | "merged" | "closed"
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
}

// GetPRInfo queries pull request status for the given branch using the gh CLI.
// Returns nil, nil when no PR exists for the branch or gh is not installed.
func (a *App) GetPRInfo(projectPath, branch string) (*PRInfo, error) {
	cmd := cmdutil.Command("gh", "pr", "list",
		"--head", branch,
		"--json", "number,title,state,url,isDraft",
		"--limit", "1",
	)
	cmd.Dir = projectPath

	out, err := cmd.Output()
	if err != nil {
		// gh CLI not installed — not an error.
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return nil, nil
		}
		// gh returned a non-zero exit code (e.g. not in a repo, auth issue).
		return nil, fmt.Errorf("gh pr list: %w", err)
	}

	// gh returns a JSON array; parse it.
	type ghPR struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		State   string `json:"state"`
		URL     string `json:"url"`
		IsDraft bool   `json:"isDraft"`
	}

	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}
	if len(prs) == 0 {
		return nil, nil
	}

	pr := prs[0]
	return &PRInfo{
		Number: pr.Number,
		Title:  pr.Title,
		State:  strings.ToLower(pr.State),
		URL:    pr.URL,
		Draft:  pr.IsDraft,
	}, nil
}
