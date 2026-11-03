package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type agentSkillRoot struct {
	AgentID   string
	AgentName string
	RelPath   string
}

var agentSkillRoots = []agentSkillRoot{
	{AgentID: "claude", AgentName: "Claude Code", RelPath: ".claude/skills"},
	{AgentID: "codex", AgentName: "Codex", RelPath: ".codex/skills"},
	{AgentID: "cursor", AgentName: "Cursor", RelPath: ".cursor/skills"},
	{AgentID: "agents", AgentName: "Agents", RelPath: ".agents/skills"},
	{AgentID: "opencode", AgentName: "OpenCode", RelPath: ".opencode/skill"},
	{AgentID: "kilocode", AgentName: "Kilo Code", RelPath: ".kilocode/skills"},
	{AgentID: "roo", AgentName: "Roo", RelPath: ".roo/skills"},
	{AgentID: "goose", AgentName: "Goose", RelPath: ".goose/skills"},
	{AgentID: "gemini", AgentName: "Gemini", RelPath: ".gemini/skills"},
	{AgentID: "agent", AgentName: "Agent", RelPath: ".agent/skills"},
	{AgentID: "github", AgentName: "GitHub Copilot", RelPath: ".github/skills"},
	{AgentID: "skills", AgentName: "Skills", RelPath: "skills"},
	{AgentID: "factory", AgentName: "Factory", RelPath: ".factory/skills"},
	{AgentID: "windsurf", AgentName: "Windsurf", RelPath: ".windsurf/skills"},
}

const maxAgentSkillDiscoverDepth = 4

func (a *App) DiscoverAgentSkills() ([]SkillHubDiscoveredSkill, error) {
	return a.searchAgentSkills("")
}

func (a *App) SearchAgentSkills(query string) ([]SkillHubDiscoveredSkill, error) {
	return a.searchAgentSkills(query)
}

func (a *App) InstallSkillFromAgentDir(id string) (*SkillHubInstalledSkill, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("discovered skill ID is required")
	}
	skills, err := a.searchAgentSkills("")
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		if skill.ID == id {
			return a.installSkillFromLocal(skill.Path, skillHubSourceAgentDirs)
		}
	}
	return nil, fmt.Errorf("discovered skill not found: %s", id)
}

func (a *App) searchAgentSkills(query string) ([]SkillHubDiscoveredSkill, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	installed, err := a.ListSkillHubInstalledSkills()
	if err != nil {
		return nil, err
	}
	installedBySourcePath := map[string]SkillHubInstalledSkill{}
	for _, skill := range installed {
		if sourcePath := filePathFromSourceURL(skill.SourceURL); sourcePath != "" {
			if absPath, err := filepath.Abs(sourcePath); err == nil {
				installedBySourcePath[filepath.Clean(absPath)] = skill
			}
		}
	}

	query = strings.ToLower(strings.TrimSpace(query))
	baseDir, _ := skillHubBaseDir()
	absBaseDir, _ := filepath.Abs(baseDir)

	var out []SkillHubDiscoveredSkill
	seen := map[string]bool{}
	for _, root := range agentSkillRoots {
		rootDir := filepath.Join(home, filepath.FromSlash(root.RelPath))
		candidates, err := discoverSkillDirsInRoot(rootDir)
		if err != nil {
			continue
		}
		for _, candidate := range candidates {
			absCandidate, err := filepath.Abs(candidate)
			if err != nil {
				continue
			}
			absCandidate = filepath.Clean(absCandidate)
			if isPathInside(absBaseDir, absCandidate) {
				continue
			}
			if seen[absCandidate] {
				continue
			}
			seen[absCandidate] = true

			skillPath := filepath.Join(absCandidate, "SKILL.md")
			// Bound the SKILL.md read so a malicious or accidental multi-GB
			// file in a scanned agent directory cannot OOM the discovery
			// walker. Mirrors the InstallSkillFromLocal guard.
			skillInfo, err := os.Lstat(skillPath)
			if err != nil || !skillInfo.Mode().IsRegular() || skillInfo.Size() > maxLocalSkillBytes {
				continue
			}
			body, err := os.ReadFile(skillPath)
			if err != nil {
				continue
			}
			id := discoveredAgentSkillID(root.AgentID, absCandidate)
			discovered := SkillHubDiscoveredSkill{
				ID:          id,
				AgentID:     root.AgentID,
				AgentName:   root.AgentName,
				Slug:        filepath.Base(absCandidate),
				Name:        skillNameFromSkillBody(body, filepath.Base(absCandidate)),
				Description: skillSummaryFromBody(body),
				Path:        absCandidate,
				SourceURL:   "file://" + absCandidate,
			}
			if installedSkill, ok := installedBySourcePath[absCandidate]; ok {
				discovered.Installed = true
				discovered.InstalledID = installedSkill.ID
			}
			if query != "" && !discoveredAgentSkillMatches(discovered, query) {
				continue
			}
			out = append(out, discovered)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].AgentName != out[j].AgentName {
			return out[i].AgentName < out[j].AgentName
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func discoverSkillDirsInRoot(rootDir string) ([]string, error) {
	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return nil, err
	}
	var dirs []string
	err = filepath.WalkDir(rootDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip the offending node — typically a permission-denied
			// subdirectory under another agent's skills root — but keep
			// walking sibling entries. Aborting on the first error would
			// blank out discovery for the whole agent root.
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == rootDir {
			return nil
		}
		depth := pathDepth(rootDir, path)
		if depth > maxAgentSkillDiscoverDepth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Legacy: older wails-gui versions auto-injected a "yansu-agent-cli"
		// SKILL.md into every agent directory. Skip leftover copies so they
		// don't reappear as "discovered" skills.
		if entry.IsDir() && entry.Name() == "yansu-agent-cli" {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		dirs = append(dirs, filepath.Dir(path))
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	return dirs, nil
}

func pathDepth(rootDir string, path string) int {
	rel, err := filepath.Rel(rootDir, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

func discoveredAgentSkillID(agentID string, absPath string) string {
	sum := sha1.Sum([]byte(absPath))
	slug := localSlugSanitizer.ReplaceAllString(strings.ToLower(filepath.Base(absPath)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "skill"
	}
	return agentID + ":" + slug + ":" + hex.EncodeToString(sum[:4])
}

func discoveredAgentSkillMatches(skill SkillHubDiscoveredSkill, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		skill.ID,
		skill.AgentID,
		skill.AgentName,
		skill.Slug,
		skill.Name,
		skill.Description,
		skill.Path,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func filePathFromSourceURL(sourceURL string) string {
	sourceURL = strings.TrimSpace(sourceURL)
	if !strings.HasPrefix(sourceURL, "file://") {
		return ""
	}
	return strings.TrimPrefix(sourceURL, "file://")
}

func isPathInside(parent string, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}
