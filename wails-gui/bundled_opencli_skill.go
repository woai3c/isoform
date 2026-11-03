package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed all:bundled-opencli-skills
var bundledOpenCLISkillsFS embed.FS

const (
	bundledOpenCLISkillSourceTag = "bundled-opencli"
	bundledOpenCLISkillSourceURL = "https://github.com/jackwener/OpenCLI"
	// Bumped whenever the embedded SKILL.md set is refreshed from upstream.
	// Mirrors the bundled-cua pattern: the version string is what the skill
	// hub UI surfaces and what we compare against to know whether a managed
	// copy needs to be rewritten on app upgrade.
	bundledOpenCLISkillVersion = "0.1.0-yansu2"
	bundledOpenCLISkillManaged = "<!-- managed-by-yansu-wails:opencli -->"
	bundledOpenCLIEmbedRoot    = "bundled-opencli-skills"
)

type bundledOpenCLISkillSpec struct {
	Slug        string
	Name        string
	EmbedSubdir string
	// PatchSkillMD optionally rewrites SKILL.md before it's written to disk.
	// Use sparingly — only when upstream content actively misleads the
	// agent inside Yansu (e.g. install instructions that conflict with
	// Yansu's bundled OpenCLI).
	PatchSkillMD func([]byte) []byte
}

// The 5 skills that ship with @jackwener/opencli upstream. opencli-usage is
// the orientation entry point; the others are loaded on demand via its links.
var bundledOpenCLISkillSpecs = []bundledOpenCLISkillSpec{
	{Slug: "opencli-usage", Name: "OpenCLI Usage", EmbedSubdir: "opencli-usage", PatchSkillMD: patchOpenCLIUsageInstallSection},
	{Slug: "opencli-browser", Name: "OpenCLI Browser", EmbedSubdir: "opencli-browser"},
	{Slug: "opencli-autofix", Name: "OpenCLI AutoFix", EmbedSubdir: "opencli-autofix"},
	{Slug: "opencli-adapter-author", Name: "OpenCLI Adapter Author", EmbedSubdir: "opencli-adapter-author"},
	{Slug: "smart-search", Name: "OpenCLI Smart Search", EmbedSubdir: "smart-search"},
}

// yansuOpenCLIInstallSection replaces the upstream "## Install" block.
// Upstream tells the agent to `npm install -g @jackwener/opencli`, but Yansu
// already auto-installs OpenCLI via bun into ~/.yansu-agent/opencli and
// prepends its bin dir to PATH (see bundled_opencli.go). Following the
// upstream instructions inside Yansu would either fail (no Node) or
// duplicate the install with a possibly-different version.
const yansuOpenCLIInstallSection = "## Install (Isoform)\n\n" +
	"OpenCLI is **already installed and on PATH** — Isoform auto-installs " +
	"the package on first launch via the bundled bun runtime (see " +
	"`$YANSU_AGENT_HOME/opencli/`). Just run `opencli ...` directly. Do not " +
	"try to install OpenCLI yourself with npm, brew, or git clone — " +
	"there is no system Node here, and any parallel install would shadow " +
	"Isoform's managed copy with a possibly-different version.\n\n" +
	"The **Chrome extension is the only piece Isoform cannot install for " +
	"the user** — it has to be added from the Chrome Web Store by hand. " +
	"It is required for the `COOKIE` / `INTERCEPT` / `UI` strategies " +
	"(see the next section's Prerequisites table). If `opencli doctor` " +
	"reports the extension as missing and the user's task needs one of " +
	"those strategies, point them at the Chrome Web Store install link " +
	"in `opencli-browser`'s troubleshooting table — do not try to " +
	"install it yourself.\n\n" +
	"```bash\n" +
	"opencli doctor   # required before COOKIE / INTERCEPT / UI adapters\n" +
	"```\n"

// patchOpenCLIUsageInstallSection rewrites the upstream `## Install` block
// in opencli-usage/SKILL.md with Yansu-specific guidance. The match is
// anchored on the section header and stops at the next H2 so we don't
// accidentally swallow downstream content if upstream rearranges sections.
func patchOpenCLIUsageInstallSection(data []byte) []byte {
	text := string(data)
	const header = "## Install"
	start := strings.Index(text, header)
	if start == -1 {
		return data
	}
	rest := text[start+len(header):]
	nextHeader := strings.Index(rest, "\n## ")
	if nextHeader == -1 {
		return data
	}
	end := start + len(header) + nextHeader + 1
	return []byte(text[:start] + yansuOpenCLIInstallSection + "\n" + text[end:])
}

// ensureBundledOpenCLISkills extracts every bundled OpenCLI skill into the
// skill-hub directory and registers it in state.json. Idempotent: managed
// files are rewritten only when their content differs; user-edited copies
// (those without the managed marker) are left alone.
func ensureBundledOpenCLISkills() {
	for _, spec := range bundledOpenCLISkillSpecs {
		if err := installBundledOpenCLISkill(spec); err != nil {
			log.Printf("[opencli-skill] %s: %v", spec.Slug, err)
		}
	}
}

func installBundledOpenCLISkill(spec bundledOpenCLISkillSpec) error {
	embedDir := bundledOpenCLIEmbedRoot + "/" + spec.EmbedSubdir
	skillBytes, err := bundledOpenCLISkillsFS.ReadFile(embedDir + "/SKILL.md")
	if err != nil {
		return fmt.Errorf("read embedded SKILL.md: %w", err)
	}

	// Description is parsed from the unpatched bytes so it always reflects
	// upstream's frontmatter, never our local rewrites.
	description := parseSkillDescription(skillBytes)
	if description == "" {
		return fmt.Errorf("SKILL.md missing description in frontmatter")
	}

	targetDir, err := skillHubSkillDir(spec.Slug)
	if err != nil {
		return fmt.Errorf("resolve skill dir: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}

	if err := fs.WalkDir(bundledOpenCLISkillsFS, embedDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(path, embedDir)
		rel = strings.TrimPrefix(rel, "/")
		dst := targetDir
		if rel != "" {
			dst = filepath.Join(targetDir, rel)
		}
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, readErr := bundledOpenCLISkillsFS.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		if rel == "SKILL.md" && spec.PatchSkillMD != nil {
			data = spec.PatchSkillMD(data)
		}
		writeManagedOpenCLIFile(dst, withOpenCLIManagedMarker(data))
		return nil
	}); err != nil {
		return fmt.Errorf("walk embedded skill: %w", err)
	}

	now := time.Now().UnixMilli()
	if err := withSkillHubState(func(state *skillHubState) error {
		existing, ok := state.Skills[spec.Slug]
		installedAt := existing.InstalledAt
		if installedAt == 0 {
			installedAt = now
		}
		domains := existing.Domains
		if domains == nil {
			domains = defaultSkillHubDomains()
		}
		enabled := existing.Enabled
		if !ok {
			enabled = true
		}
		state.Skills[spec.Slug] = SkillHubInstalledSkill{
			ID:          spec.Slug,
			Slug:        spec.Slug,
			Name:        spec.Name,
			Description: description,
			Source:      bundledOpenCLISkillSourceTag,
			SourceURL:   bundledOpenCLISkillSourceURL,
			Version:     bundledOpenCLISkillVersion,
			Path:        targetDir,
			Enabled:     enabled,
			Domains:     normalizeSkillHubDomains(domains),
			InstalledAt: installedAt,
			UpdatedAt:   now,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("update state.json: %w", err)
	}
	return nil
}

// writeManagedOpenCLIFile writes desired bytes when the on-disk file is
// missing or carries the managed marker; otherwise it preserves the
// user-edited copy. Mirrors writeManagedSkillFile but operates on bytes so
// it can handle non-UTF8-clean reference files safely.
func writeManagedOpenCLIFile(path string, desired []byte) {
	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if writeErr := writeAtomicBytes(path, desired); writeErr != nil {
			log.Printf("[opencli-skill] write %s: %v", filepath.Base(path), writeErr)
		}
	case err != nil:
		log.Printf("[opencli-skill] read existing %s: %v", filepath.Base(path), err)
	case !strings.Contains(string(existing), bundledOpenCLISkillManaged):
		// User edited this file; leave it alone.
	default:
		if string(existing) == string(desired) {
			return
		}
		if writeErr := writeAtomicBytes(path, desired); writeErr != nil {
			log.Printf("[opencli-skill] update %s: %v", filepath.Base(path), writeErr)
		}
	}
}

func writeAtomicBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// withOpenCLIManagedMarker appends the managed-by sentinel to file content.
// The marker is an HTML comment so it renders to nothing in markdown and
// does not interfere with the YAML frontmatter parsers used by skill
// loaders.
func withOpenCLIManagedMarker(data []byte) []byte {
	body := strings.TrimRight(string(data), "\n")
	return []byte(body + "\n\n" + bundledOpenCLISkillManaged + "\n")
}

// parseSkillDescription extracts the description field from the YAML
// frontmatter at the top of a SKILL.md. Multi-line values are joined into a
// single line. Returns "" if no frontmatter or no description is present.
func parseSkillDescription(data []byte) string {
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return ""
	}
	rest := text[3:]
	rest = strings.TrimLeft(rest, " \t")
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return ""
	}
	frontmatter := rest[:end]

	var (
		desc      strings.Builder
		capturing bool
	)
	for _, raw := range strings.Split(frontmatter, "\n") {
		line := strings.TrimRight(raw, "\r")
		if capturing {
			if line == "" {
				break
			}
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				if strings.Contains(line, ":") {
					break
				}
			}
			if desc.Len() > 0 {
				desc.WriteString(" ")
			}
			desc.WriteString(strings.TrimSpace(line))
			continue
		}
		if strings.HasPrefix(line, "description:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			if value != "" {
				return value
			}
			capturing = true
		}
	}
	return strings.TrimSpace(desc.String())
}
