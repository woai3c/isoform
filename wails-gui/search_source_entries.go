package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) searchEntries(ctx context.Context, words []string, memory bool) ([]searchCandidate, error) {
	// Unlike the page's directory helpers, this read never creates a folder.
	root, err := flavorDataDir()
	if err != nil {
		return nil, err
	}
	folder, place, kind := "knowledge", "know_how", "knowledge"
	if memory {
		folder, place, kind = "memory", "about_you", "memory"
	}
	dir := filepath.Join(root, folder)
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	suppressed := map[string]struct{}{}
	if memory && a.activity != nil && a.activity.store != nil {
		suppressed, err = a.activity.store.ListCorrectedAgencyLearningTargetIDsContext(ctx)
		if err != nil {
			return nil, err
		}
	}
	out := []searchCandidate{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") || isOKFReservedMarkdownFile(file.Name()) {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".md")
		if _, hidden := suppressed[id]; hidden {
			continue
		}
		// Bound individual file reads as well as matching candidates. Symlinks
		// are not page-owned entries and must not escape the local store.
		info, err := file.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			continue
		}
		f, err := os.Open(filepath.Join(dir, file.Name()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(data) > 1<<20 {
			continue
		}
		content := string(data)
		title := extractTitle(content)
		if title == "" {
			title = id
		}
		body := searchEntryBody(content)
		if !searchMatches(title+"\n"+body, words) {
			continue
		}
		at := info.ModTime().UnixMilli()
		if memory {
			forgotten, err := isMemoryForgotten(dir, id, content, time.Now())
			if err != nil {
				return nil, err
			}
			if forgotten {
				continue
			}
			if updated := parseMemoryFrontmatter(content).UpdatedAt; updated != 0 {
				at = updated
			}
		} else {
			entry := parseKnowledgeFile(file.Name(), content)
			title = entry.Title
			if date, err := time.Parse("2006-01-02", entry.Date); err == nil {
				at = date.UnixMilli()
			}
		}
		out = append(out, searchCandidate{result: SearchResult{sourcePath: filepath.Join(dir, file.Name()), sourceHash: sha256.Sum256(data), ID: id, Place: place, Title: title, At: at, Target: SearchTarget{Kind: kind, ID: id}}, body: body})
		if len(out) == searchCandidateLimit {
			break
		}
	}
	return out, ctx.Err()
}

// Match the page's known metadata fields, not arbitrary bold facts. The
// general token-extraction stripper treats every leading bold bullet as
// metadata; search must retain words a page actually displays.
func searchEntryBody(content string) string {
	content = legacyMemoryWithoutConfirmation(content)
	title := ""
	legacy := true
	if doc, ok := parseOKFDocument(content); ok {
		content = doc.Body
		title = okfString(doc.Frontmatter, "title")
		legacy = false
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "# ") && (legacy || strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "# ")) == title) {
		i++
	}
	metadata := false
	for ; i < len(lines); i++ {
		raw := lines[i]
		line := strings.TrimSpace(raw)
		if line == "" {
			metadata = false
			continue
		}
		if metadata && isIndentedMarkdownBullet(raw) {
			continue
		}
		if strings.HasPrefix(line, "- **") {
			if field := knowledgeMetaRe.FindStringSubmatch(line); len(field) > 1 {
				switch strings.ToLower(field[1]) {
				case "dimension", "source", "updated", "activity session", "activity sessions", "read provenance", "apps", "uuid", "id", "type", "date", "timestamp", "reassigned to", "mergedfrom", "merged from", "mergedfromrefs":
					metadata = true
					continue
				}
			}
		}
		return strings.Join(lines[i:], "\n")
	}
	return ""
}
