package main

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Git-remote bridge: a terminal observation usually names a LOCAL directory
// (e.g. /Users/me/isoform/sth-airjelly) whose basename differs from the GitHub
// slug (Isoform/something). Pure text matching can never align those, so the
// terminal's repo work fails to stitch with the browser PR / chat mention. This
// bridge reads the repo's git remote to recover the canonical slug, closing the
// acceptance gap surfaced in docs/activity-event-model-airjelly-analysis.md.

// localPathRe matches absolute / home-relative paths in observation text. It is
// intentionally permissive: non-git paths cost only a stat and yield nothing.
var localPathRe = regexp.MustCompile(`(?:~)?/[\w.@~/-]{3,}`)

// urlHostRe matches domain-like prefixes immediately before a matched path,
// so we can reject URL-derived false positives (e.g. "console.aws.amazon.com/console/home").
var urlHostRe = regexp.MustCompile(`[a-zA-Z0-9][-a-zA-Z0-9]*\.[a-zA-Z]{2,}$`)

// gitRemoteSlugRe pulls owner/repo out of a GitHub remote URL (ssh or https).
var gitRemoteSlugRe = regexp.MustCompile(`github\.com[/:]([\w.-]+)/([\w.-]+?)(?:\.git)?\s*$`)

// repoSlugCache memoizes gitRoot -> slug (negative results cached as "").
var repoSlugCache sync.Map

func extractLocalPaths(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, idx := range localPathRe.FindAllStringIndex(text, 8) {
		m := text[idx[0]:idx[1]]
		p := strings.TrimRight(m, `/.,):;"'`)
		if len(p) < 3 || seen[p] {
			continue
		}
		// Reject URL-derived paths: if the text immediately before the match
		// ends with a domain name (e.g. "amazon.com"), this is a URL path
		// component, not a local filesystem path.
		if idx[0] > 0 {
			prefix := text[:idx[0]]
			if urlHostRe.MatchString(prefix) {
				continue
			}
			// Also reject paths preceded by "://" (explicit URL scheme).
			if strings.HasSuffix(prefix, "://") || strings.HasSuffix(prefix, ":/") {
				continue
			}
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// findGitRoot walks up from path to the nearest ancestor containing a .git dir.
func findGitRoot(path string) string {
	start := time.Now()
	dir := expandHome(path)
	// Skip UNC / network paths — os.Stat on unreachable hosts blocks for
	// the full SMB timeout (~40s), which stalls the semantic loop.
	abs, _ := filepath.Abs(dir)
	if strings.HasPrefix(abs, `\\`) {
		return ""
	}
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	for i := 0; i < 40; i++ {
		target := filepath.Join(dir, ".git")
		t0 := time.Now()
		fi, err := os.Stat(target)
		if d := time.Since(t0); d > 2*time.Second {
			log.Printf("[semantic-events] findGitRoot: os.Stat(%s) took %v err=%v", target, d, err)
		}
		if err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if d := time.Since(start); d > 5*time.Second {
		log.Printf("[semantic-events] findGitRoot(%s) total %v", path, d)
	}
	return ""
}

func parseRemoteSlug(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "url") {
			continue
		}
		if m := gitRemoteSlugRe.FindStringSubmatch(line); m != nil {
			return m[1] + "/" + m[2]
		}
	}
	return ""
}

// resolveRepoSlugFromPath returns the GitHub slug for the repo containing path,
// or "" when path is not in a GitHub-remote git repo. Cached per git root.
func resolveRepoSlugFromPath(path string) string {
	root := findGitRoot(path)
	if root == "" {
		return ""
	}
	if v, ok := repoSlugCache.Load(root); ok {
		return v.(string)
	}
	slug := parseRemoteSlug(filepath.Join(root, ".git", "config"))
	repoSlugCache.Store(root, slug)
	return slug
}

func repoBasename(slug string) string {
	if i := strings.LastIndexByte(slug, '/'); i >= 0 {
		return slug[i+1:]
	}
	return slug
}

// repoLinkEntities bridges local repo paths in an observation to their GitHub
// slug + basename project, so terminal work aligns with browser/chat repo
// entities even when the directory name differs from the slug.
func repoLinkEntities(o Observation) []Entity {
	var out []Entity
	seen := map[string]bool{}
	for _, p := range extractLocalPaths(o.Title + "\n" + o.Body) {
		slug := resolveRepoSlugFromPath(p)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, Entity{Type: entityRepo, Name: slug})
		out = append(out, Entity{Type: entityProject, Name: repoBasename(slug)})
	}
	return out
}
