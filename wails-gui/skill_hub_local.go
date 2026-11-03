package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	skillHubSourceLocal = "local"

	maxLocalSkillBytes = 64 << 20 // 64 MiB; matches the GitHub/ClawHub limit.
)

var localSlugSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

type localSkillOrigin struct {
	Version     int    `json:"version"`
	SourcePath  string `json:"sourcePath"`
	InstalledAt int64  `json:"installedAt"`
}

// SelectLocalSkillDir opens a native directory picker so the user can choose
// the folder that contains their SKILL.md. Returns "" if the user cancels.
func (a *App) SelectLocalSkillDir() (string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose a skill folder (must contain SKILL.md)",
	})
	if err != nil {
		return "", fmt.Errorf("open directory dialog: %w", err)
	}
	return dir, nil
}

// InstallSkillFromLocal copies a local directory containing SKILL.md into the
// skill hub directory and registers it under source="local".
//
// The caller (frontend) typically obtains the path from SelectLocalSkillDir.
// We intentionally copy rather than symlink so the skill hub stays
// self-contained even if the original folder moves or is deleted.
func (a *App) InstallSkillFromLocal(srcDir string) (*SkillHubInstalledSkill, error) {
	return a.installSkillFromLocal(srcDir, skillHubSourceLocal)
}

func (a *App) installSkillFromLocal(srcDir string, source string) (*SkillHubInstalledSkill, error) {
	srcDir = strings.TrimSpace(srcDir)
	if srcDir == "" {
		return nil, fmt.Errorf("choose a folder to install")
	}
	absSrc, err := filepath.Abs(srcDir)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}
	info, err := os.Stat(absSrc)
	if err != nil {
		return nil, fmt.Errorf("source folder unavailable: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("expected a directory, got a file: %s", absSrc)
	}
	skillFile := filepath.Join(absSrc, "SKILL.md")
	// Bound the SKILL.md read before pulling it into memory — a malicious or
	// accidental multi-GB file would otherwise blow up the process before
	// assertLocalSkillSize had a chance to reject it.
	skillInfo, err := os.Lstat(skillFile)
	if err != nil {
		return nil, fmt.Errorf("folder does not contain SKILL.md: %w", err)
	}
	if !skillInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("SKILL.md must be a regular file (no symlinks): %s", skillFile)
	}
	if skillInfo.Size() > maxLocalSkillBytes {
		return nil, fmt.Errorf("SKILL.md is larger than %d bytes", maxLocalSkillBytes)
	}
	if err := assertLocalSkillSize(absSrc); err != nil {
		return nil, err
	}
	skillBody, err := os.ReadFile(skillFile)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	// Reject installing from inside the skill hub itself — that would create
	// nested copies of the same content on every install. We use
	// filepath.IsLocal here instead of a HasPrefix(rel, "..") check because
	// the latter false-positives on legitimate sibling names like "..evil"
	// (Rel("/a/b", "/a/b/..evil") returns "..evil") and lets the caller
	// bypass the guard with a directory whose name happens to start with
	// dots. IsLocal returns true exactly when `rel` is contained inside
	// absBase (no .., no abs path), which is the property we actually want.
	base, err := skillHubBaseDir()
	if err != nil {
		return nil, err
	}
	absBase, _ := filepath.Abs(base)
	if absBase != "" {
		rel, err := filepath.Rel(absBase, absSrc)
		if err == nil && filepath.IsLocal(rel) {
			return nil, fmt.Errorf("cannot install a skill from inside the skill hub directory")
		}
	}

	if source == "" {
		source = skillHubSourceLocal
	}
	slugPrefix := "local"
	if source == skillHubSourceAgentDirs {
		slugPrefix = "agent"
	}
	slug, err := localSkillSlugWithPrefix(absSrc, slugPrefix)
	if err != nil {
		return nil, err
	}
	targetDir, err := skillHubSkillDir(slug)
	if err != nil {
		return nil, err
	}

	// Stage the copy in a system temp directory, then promote it atomically
	// onto targetDir via replaceDir — same pattern the ClawHub/GitHub paths
	// use, so a partial copy never appears under skillHubBaseDir.
	tempDir, err := os.MkdirTemp("", "yansu-skillhub-local-*")
	if err != nil {
		return nil, fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	stage := filepath.Join(tempDir, "staged")
	if err := copyDir(absSrc, stage); err != nil {
		return nil, fmt.Errorf("copy local skill: %w", err)
	}

	// Per-slug install lock: serialise the file-replace + origin-write +
	// state-write triple so two parallel installs of the same slug can't
	// leave on-disk content and state.json describing different versions.
	releaseInstallLock := acquireSkillHubInstallLock(slug)
	defer releaseInstallLock()

	if err := replaceDir(stage, targetDir); err != nil {
		return nil, fmt.Errorf("install skill files: %w", err)
	}

	now := time.Now().UnixMilli()
	if err := writeLocalSkillOrigin(targetDir, localSkillOrigin{
		Version:     1,
		SourcePath:  absSrc,
		InstalledAt: now,
	}); err != nil {
		return nil, err
	}

	displayName := skillNameFromSkillBody(skillBody, filepath.Base(absSrc))
	var skill SkillHubInstalledSkill
	if err := withSkillHubState(func(state *skillHubState) error {
		existing := state.Skills[slug]
		installedAt := existing.InstalledAt
		if installedAt == 0 {
			installedAt = now
		}
		skill = SkillHubInstalledSkill{
			ID:          slug,
			Slug:        slug,
			Name:        displayName,
			Description: skillSummaryFromBody(skillBody),
			Source:      source,
			SourceURL:   "file://" + absSrc,
			Version:     fmt.Sprintf("%s-%d", source, now/1000),
			Path:        targetDir,
			Enabled:     true,
			Domains:     defaultSkillHubDomains(),
			InstalledAt: installedAt,
			UpdatedAt:   now,
		}
		if existing.Domains != nil {
			skill.Domains = normalizeSkillHubDomains(existing.Domains)
		}
		state.Skills[slug] = skill
		return nil
	}); err != nil {
		return nil, err
	}
	return &skill, nil
}

// localSkillSlug builds a deterministic slug for a local folder so that
// reinstalling the same path replaces the existing entry rather than creating
// a duplicate. Format: local-<sanitised-basename>-<short-hash-of-abs-path>.
func localSkillSlug(absSrc string) (string, error) {
	return localSkillSlugWithPrefix(absSrc, "local")
}

func localSkillSlugWithPrefix(absSrc string, prefix string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "local"
	}
	base := strings.ToLower(filepath.Base(absSrc))
	sanitised := localSlugSanitizer.ReplaceAllString(base, "-")
	sanitised = strings.Trim(sanitised, "-")
	if sanitised == "" {
		sanitised = "skill"
	}
	if len(sanitised) > 64 {
		sanitised = sanitised[:64]
	}
	sum := sha1.Sum([]byte(absSrc))
	slug := prefix + "-" + sanitised + "-" + hex.EncodeToString(sum[:4])
	if !clawHubSlugPattern.MatchString(slug) {
		return "", fmt.Errorf("derived slug %q is not a valid skill identifier", slug)
	}
	return slug, nil
}

// skillNameFromSkillBody pulls a friendly display name out of SKILL.md by
// looking at YAML frontmatter `name:` first, then the first `# heading`,
// and finally falling back to the folder name.
func skillNameFromSkillBody(body []byte, fallback string) string {
	if name := extractFrontmatterField(body, "name"); name != "" {
		return name
	}
	if heading := extractFirstHeading(body); heading != "" {
		return heading
	}
	if fallback != "" {
		return fallback
	}
	return "Local skill"
}

func skillSummaryFromBody(body []byte) string {
	if desc := extractFrontmatterField(body, "description"); desc != "" {
		return desc
	}
	if desc := extractFrontmatterField(body, "summary"); desc != "" {
		return desc
	}
	return ""
}

func extractFrontmatterField(body []byte, field string) string {
	text := string(body)
	if !strings.HasPrefix(text, "---") {
		return ""
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return ""
	}
	header := text[3 : 3+end]
	prefix := field + ":"
	for line := range strings.SplitSeq(header, "\n") {
		trimmed := strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(strings.ToLower(trimmed), prefix)
		if !ok {
			continue
		}
		value := strings.TrimSpace(trimmed[len(trimmed)-len(rest):])
		return strings.Trim(value, `"'`)
	}
	return ""
}

func extractFirstHeading(body []byte) string {
	for line := range strings.SplitSeq(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, "# "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func assertLocalSkillSize(srcDir string) error {
	var total int64
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > maxLocalSkillBytes {
			return fmt.Errorf("skill folder is larger than %d bytes", maxLocalSkillBytes)
		}
		return nil
	})
	return err
}

func writeLocalSkillOrigin(skillDir string, origin localSkillOrigin) error {
	data, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(skillDir, ".yansu-skillhub", "local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
