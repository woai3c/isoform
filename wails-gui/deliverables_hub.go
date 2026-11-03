package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/skratchdot/open-golang/open"
	"wails-gui/storage"
)

// Deliverable sources. The unified hub aggregates outputs that today live in
// four separate, source-specific surfaces (chat stream, Automation grid,
// Handoffs page, Timeline calendar) into one read-time view. Each entry's
// composite ID is "<source>:<sourceID>" so file operations can route back to
// the owning subsystem to resolve a path-validation context.
const (
	deliverableSourceChat       = "chat"
	deliverableSourceAutomation = "automation"
	deliverableSourceHandoff    = "handoff"
)

// automationDeliverableLookbackDays bounds the cron-run scan so the hub query
// stays cheap; runs older than this simply don't surface (their files usually
// no longer matter and remain reachable from the Automation grid).
const automationDeliverableLookbackDays = 30

// DeliverableFileRef is one openable file attached to a deliverable entry.
// Description is the agent-written one-sentence explanation of what the file
// is (empty for sources/rows that predate the "path | description" protocol).
type DeliverableFileRef struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// DeliverableEntry is the unified, read-only projection of a produced artifact
// across all sources, shown as a card in the Deliverables hub.
type DeliverableEntry struct {
	ID        string               `json:"id"`     // "<source>:<sourceID>"
	Source    string               `json:"source"` // chat | automation | handoff | extractor
	SourceID  string               `json:"sourceId"`
	Title     string               `json:"title"`
	Summary   string               `json:"summary"`
	Status    string               `json:"status"` // completed | in_progress | failed | active | stale | aborted | skipped
	Files     []DeliverableFileRef `json:"files"`
	CreatedAt int64                `json:"createdAt"` // unix ms
	UpdatedAt int64                `json:"updatedAt"` // unix ms
}

// ListDeliverables aggregates produced artifacts across chat, automation,
// handoff, and extractor sources into a single status-sorted list. This is a
// read-time projection: each source remains the single source of truth for its
// own data, so there is no write-path duplication or migration to keep in sync.
// A failure collecting one source is logged and skipped rather than failing the
// whole list, so a single bad row can't blank the hub.
func (a *App) ListDeliverables() ([]DeliverableEntry, error) {
	return a.listDeliverablesContext(a.deliverableCtx())
}

func (a *App) listDeliverablesContext(ctx context.Context) ([]DeliverableEntry, error) {
	var entries []DeliverableEntry
	entries = append(entries, a.collectChatDeliverables(ctx)...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries = append(entries, a.collectAutomationDeliverables(ctx)...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries = append(entries, a.collectHandoffDeliverables()...)

	sortDeliverables(entries)
	if entries == nil {
		entries = []DeliverableEntry{}
	}
	return entries, nil
}

func (a *App) collectChatDeliverables(ctx context.Context) []DeliverableEntry {
	if a.store == nil {
		return nil
	}
	rows, err := a.store.ListChatDeliverables(ctx, 200)
	if err != nil {
		log.Printf("[deliverables] list chat: %v", err)
		return nil
	}
	out := make([]DeliverableEntry, 0, len(rows))
	for _, r := range rows {
		files := decodeChatDeliverableFiles(r.Files)
		if len(files) == 0 {
			continue
		}
		out = append(out, DeliverableEntry{
			ID:        deliverableSourceChat + ":" + r.ID,
			Source:    deliverableSourceChat,
			SourceID:  r.ID,
			Title:     r.Title,
			Summary:   deliverableSummaryLine(r.Summary, 200),
			Status:    "completed",
			Files:     files,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return out
}

// decodeChatDeliverableFiles reads a chat deliverable row's files JSON in
// either the current []DeliverableFileRef form or the legacy []string
// (paths-only) form written before per-file descriptions existed. Names are
// re-derived from paths so stale stored values can't drift from the file.
func decodeChatDeliverableFiles(raw string) []DeliverableFileRef {
	var refs []DeliverableFileRef
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		var paths []string
		if err := json.Unmarshal([]byte(raw), &paths); err != nil {
			return nil
		}
		return toDeliverableFileRefs(paths)
	}
	out := make([]DeliverableFileRef, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Path) == "" {
			continue
		}
		ref.Name = filepath.Base(ref.Path)
		ref.Description = sanitizeDeliverableDescription(ref.Description)
		out = append(out, ref)
	}
	return out
}

func (a *App) collectAutomationDeliverables(ctx context.Context) []DeliverableEntry {
	if a.store == nil {
		return nil
	}
	now := time.Now()
	startMs := now.AddDate(0, 0, -automationDeliverableLookbackDays).UnixMilli()
	endMs := now.Add(24 * time.Hour).UnixMilli()
	runs, err := a.store.ListCronRunsByDateRange(ctx, startMs, endMs, 200)
	if err != nil {
		log.Printf("[deliverables] list automation runs: %v", err)
		return nil
	}

	jobNames := map[string]string{}
	out := make([]DeliverableEntry, 0)
	for _, run := range runs {
		if ctx.Err() != nil {
			return nil
		}
		// The scheduler strips the <deliverables> block from result_summary
		// before persisting it, so the produced files survive only in the run's
		// stream log (the same source the run-detail view reads). Re-derive them
		// there rather than from the already-cleaned summary.
		files := a.cronRunDeliverableRefs(ctx, run)
		if len(files) == 0 {
			continue // only runs that actually produced files are "deliverables"
		}

		name, ok := jobNames[run.JobID]
		if !ok {
			if job, jerr := a.store.LoadCronJob(ctx, run.JobID); jerr == nil {
				name = job.Name
			}
			jobNames[run.JobID] = name
		}
		title := strings.TrimSpace(name)
		if title == "" {
			title = "Automation"
		}

		updated := run.FinishedAt
		if updated == 0 {
			updated = run.StartedAt
		}
		out = append(out, DeliverableEntry{
			ID:        deliverableSourceAutomation + ":" + run.JobID + ":" + run.ID,
			Source:    deliverableSourceAutomation,
			SourceID:  run.JobID + ":" + run.ID,
			Title:     title,
			Summary:   deliverableSummaryLine(run.ResultSummary, 200),
			Status:    mapRunStatus(run.Status),
			Files:     files,
			CreatedAt: run.StartedAt,
			UpdatedAt: updated,
		})
	}
	return out
}

// cronRunDeliverableRefs re-derives the files a cron run produced from its
// stream log. result_summary can't be used — the scheduler strips the
// <deliverables> block before persisting it — so the run's assistant/result
// stream items are the only surviving source (the run-detail view reads the
// same slice). Returns nil for runs without logs. Files are de-duplicated by
// path, preserving first-seen order; when the same path is declared more than
// once, the first non-empty description wins.
func (a *App) cronRunDeliverableRefs(ctx context.Context, run storage.CronRunRow) []DeliverableFileRef {
	if run.SessionTag == "" || run.ItemSortStart < 0 {
		return nil
	}
	sortEnd, err := a.store.GetNextCronRunSortStart(ctx, run.JobID, run.ID, run.StartedAt)
	if err != nil {
		log.Printf("[deliverables] next sort start for run %s: %v", run.ID, err)
		return nil
	}
	items, err := a.store.LoadStreamItemsForCronRun(ctx, run.SessionTag, run.ItemSortStart, sortEnd)
	if err != nil {
		log.Printf("[deliverables] load stream items for run %s: %v", run.ID, err)
		return nil
	}

	indexByPath := map[string]int{}
	var files []DeliverableFileRef
	for _, it := range items {
		switch it.Kind {
		case "assistant", "result":
			_, refs := parseDeliverableRefs(it.Content)
			for _, ref := range refs {
				if i, ok := indexByPath[ref.Path]; ok {
					if files[i].Description == "" {
						files[i].Description = ref.Description
					}
					continue
				}
				indexByPath[ref.Path] = len(files)
				files = append(files, DeliverableFileRef{
					Path:        ref.Path,
					Name:        filepath.Base(ref.Path),
					Description: ref.Description,
				})
			}
		}
	}
	return files
}

func (a *App) collectHandoffDeliverables() []DeliverableEntry {
	if a.handoffStore == nil {
		return nil
	}
	entries, err := a.handoffStore.List()
	if err != nil {
		log.Printf("[deliverables] list handoffs: %v", err)
		return nil
	}
	out := make([]DeliverableEntry, 0, len(entries))
	for _, e := range entries {
		if len(e.KeyFiles) == 0 {
			continue // no produced files to surface
		}
		out = append(out, DeliverableEntry{
			ID:        deliverableSourceHandoff + ":" + e.ID,
			Source:    deliverableSourceHandoff,
			SourceID:  e.ID,
			Title:     e.Title,
			Summary:   deliverableSummaryLine(e.Body, 200),
			Status:    string(e.Status),
			Files:     toDeliverableFileRefs(e.KeyFiles),
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
		})
	}
	return out
}

// recordFinishedChatDeliverable records a completed turn without relying on a
// renderer having observed its streaming transition. The frontend recorder
// remains an idempotent fallback for rows assembled by older backends.
func (a *App) recordFinishedChatDeliverable(segment finishedSegment) {
	summary, refs := parseDeliverableRefs(segment.content)
	if len(refs) == 0 {
		return
	}
	title := "Chat"
	if a.store != nil {
		if session, err := a.store.LoadSession(a.deliverableCtx(), segment.tag); err == nil && session != nil {
			if label := strings.TrimSpace(session.Label); label != "" {
				title = label
			}
		}
	}
	files := make([]DeliverableFileRef, 0, len(refs))
	for _, ref := range refs {
		files = append(files, DeliverableFileRef{
			Path:        ref.Path,
			Name:        filepath.Base(ref.Path),
			Description: ref.Description,
		})
	}
	if err := a.RecordChatDeliverables(segment.tag, segment.tag, title, deliverableSummaryLine(summary, 200), files); err != nil {
		log.Printf("[deliverables] record finished chat %s: %v", segment.tag, err)
	}
}

// RecordChatDeliverables persists the files an interactive chat agent declared
// via its <deliverables> block so they surface in the hub. The backend records
// every completed turn; the frontend may record the same turn again after
// parsing the block for inline chips. Paths are validated (existence + containment) before storing;
// invalid entries are dropped. Re-recording the same set for a session upserts
// in place via the content-hash ID (keyed on paths only, so a re-record can
// refresh descriptions). workDir/sessionTempDir are captured now so the files
// stay openable after the in-memory temp-dir cache is lost on restart.
func (a *App) RecordChatDeliverables(tag, sessionID, title, summary string, files []DeliverableFileRef) error {
	if a.store == nil {
		return fmt.Errorf("store not initialized")
	}

	var valid []DeliverableFileRef
	for _, f := range files {
		abs, err := a.resolveDeliverablePath(tag, f.Path)
		if err != nil {
			continue
		}
		// Defensive normalization at the persistence boundary: the frontend
		// parser doesn't cap or strip descriptions, and this keeps chat rows
		// under the same guarantees as automation's parseDeliverableRefs.
		valid = append(valid, DeliverableFileRef{
			Path:        abs,
			Name:        filepath.Base(abs),
			Description: sanitizeDeliverableDescription(f.Description),
		})
	}
	if len(valid) == 0 {
		return nil
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Path < valid[j].Path })
	sortedPaths := make([]string, 0, len(valid))
	for _, f := range valid {
		sortedPaths = append(sortedPaths, f.Path)
	}

	filesJSON, err := json.Marshal(valid)
	if err != nil {
		return fmt.Errorf("marshal deliverable files: %w", err)
	}

	workDir, _ := a.chatSessionWorkDir(tag)
	row := storage.ChatDeliverableRow{
		ID:             chatDeliverableID(sessionID, sortedPaths),
		SessionID:      sessionID,
		Title:          strings.TrimSpace(title),
		Summary:        strings.TrimSpace(summary),
		Files:          string(filesJSON),
		WorkDir:        workDir,
		SessionTempDir: a.peekSessionTempDir(tag),
	}
	if err := a.store.SaveChatDeliverable(a.deliverableCtx(), row); err != nil {
		return fmt.Errorf("save chat deliverable: %w", err)
	}
	a.emitBrowserEvent("deliverables:updated", nil)
	return nil
}

// OpenDeliverableFile opens a hub deliverable file in the system default app.
func (a *App) OpenDeliverableFile(source, sourceID, path string) error {
	abs, err := a.resolveHubDeliverablePath(source, sourceID, path)
	if err != nil {
		return err
	}
	if !isOpenableDeliverableExt(abs) {
		return fmt.Errorf("file type is not openable from this UI")
	}
	return open.Start(abs)
}

// RevealDeliverableFile selects the file in the system file manager.
func (a *App) RevealDeliverableFile(source, sourceID, path string) error {
	abs, err := a.resolveHubDeliverablePath(source, sourceID, path)
	if err != nil {
		return err
	}
	return revealInFileManager(abs)
}

// DeliverableFileInfo returns the type + size metadata for a hub deliverable chip.
func (a *App) DeliverableFileInfo(source, sourceID, path string) (*DeliverableInfo, error) {
	abs, err := a.resolveHubDeliverablePath(source, sourceID, path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat deliverable: %w", err)
	}
	ext := strings.TrimPrefix(strings.ToUpper(filepath.Ext(abs)), ".")
	return &DeliverableInfo{Name: filepath.Base(abs), Ext: ext, SizeBytes: fi.Size()}, nil
}

// ListDeliverableFileOpenWithApps returns apps that can open the file (macOS).
func (a *App) ListDeliverableFileOpenWithApps(source, sourceID, path string) ([]DeliverableOpenWithApp, error) {
	abs, err := a.resolveHubDeliverablePath(source, sourceID, path)
	if err != nil {
		return nil, err
	}
	if !isOpenableDeliverableExt(abs) {
		return nil, fmt.Errorf("file type is not openable from this UI")
	}
	return listOpenWithApps(abs), nil
}

// OpenDeliverableFileWithApp opens the file using a specific application bundle.
func (a *App) OpenDeliverableFileWithApp(source, sourceID, path, appPath string) error {
	abs, err := a.resolveHubDeliverablePath(source, sourceID, path)
	if err != nil {
		return err
	}
	if !isOpenableDeliverableExt(abs) {
		return fmt.Errorf("file type is not openable from this UI")
	}
	return openWithApp(abs, appPath)
}

// resolveHubDeliverablePath validates a deliverable file path against the
// path-validation context (workDir/sessionTempDir) owned by its source, so the
// hub never widens the allow-list beyond what each source-specific opener
// already permits. The user home directory is always allowed (see
// validateDeliverablePath), which is why automation outputs under
// ~/.isoform-workspaces resolve without a per-job workDir lookup miss.
func (a *App) resolveHubDeliverablePath(source, sourceID, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	workDir, sessionTempDir, err := a.hubDeliverableContext(source, sourceID)
	if err != nil {
		return "", err
	}

	absPath := path
	if !filepath.IsAbs(path) && workDir != "" {
		absPath = filepath.Join(workDir, path)
	}

	// Existence first so a missing file reports "does not exist" rather than the
	// misleading "outside allowed directories" EvalSymlinks would trigger.
	if _, err := os.Stat(absPath); err != nil {
		return "", fmt.Errorf("file does not exist: %w", err)
	}
	if !validateDeliverablePath(absPath, workDir, sessionTempDir) {
		return "", fmt.Errorf("path is outside the allowed directories for this deliverable")
	}
	return absPath, nil
}

// hubDeliverableContext resolves the (workDir, sessionTempDir) pair used for
// path validation, by routing back to the source that owns the deliverable.
func (a *App) hubDeliverableContext(source, sourceID string) (workDir, sessionTempDir string, err error) {
	ctx := a.deliverableCtx()
	switch source {
	case deliverableSourceChat:
		if a.store == nil {
			return "", "", fmt.Errorf("store not initialized")
		}
		row, derr := a.store.GetChatDeliverable(ctx, sourceID)
		if derr != nil {
			return "", "", fmt.Errorf("load chat deliverable: %w", derr)
		}
		if row == nil {
			return "", "", fmt.Errorf("chat deliverable not found")
		}
		return row.WorkDir, row.SessionTempDir, nil
	case deliverableSourceAutomation:
		// sourceID is "<jobID>:<runID>"; the job config gives the work dir.
		jobID := sourceID
		if i := strings.Index(sourceID, ":"); i >= 0 {
			jobID = sourceID[:i]
		}
		return a.automationDeliverableContext(jobID)
	case deliverableSourceHandoff:
		if a.handoffStore == nil {
			return "", "", fmt.Errorf("handoff store not initialized")
		}
		e, derr := a.handoffStore.Get(sourceID)
		if derr != nil {
			return "", "", fmt.Errorf("load handoff: %w", derr)
		}
		return e.Workdir, "", nil
	default:
		return "", "", fmt.Errorf("unknown deliverable source %q", source)
	}
}

// automationDeliverableContext mirrors OpenAutomationDeliverable's work-dir
// resolution: pinned jobs use their projectDir, unpinned jobs fall back to the
// stable per-job dir under ~/.isoform-workspaces.
func (a *App) automationDeliverableContext(jobID string) (string, string, error) {
	if a.store == nil {
		return "", "", fmt.Errorf("store not initialized")
	}
	job, err := a.store.LoadCronJob(a.deliverableCtx(), jobID)
	if err != nil {
		return "", "", fmt.Errorf("job not found: %w", err)
	}
	var cfg cronTargetConfig
	if err := json.Unmarshal([]byte(job.TargetConfig), &cfg); err != nil {
		return "", "", fmt.Errorf("parse target config: %w", err)
	}
	workDir := cfg.ProjectDir
	if workDir == "" {
		dir, derr := a.defaultAutomationWorkDir(jobID)
		if derr != nil {
			return "", "", fmt.Errorf("resolve default work dir: %w", derr)
		}
		workDir = dir
	}
	return workDir, a.peekSessionTempDir(cronScratchTag(jobID)), nil
}

func (a *App) deliverableCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// chatDeliverableID is a stable content hash (session + sorted paths) so
// re-recording an identical deliverable set upserts in place.
func chatDeliverableID(sessionID string, sortedPaths []string) string {
	h := sha256.Sum256([]byte(sessionID + "\x00" + strings.Join(sortedPaths, "\x00")))
	return hex.EncodeToString(h[:])[:24]
}

func toDeliverableFileRefs(paths []string) []DeliverableFileRef {
	refs := make([]DeliverableFileRef, 0, len(paths))
	for _, p := range paths {
		refs = append(refs, DeliverableFileRef{Path: p, Name: filepath.Base(p)})
	}
	return refs
}

// Markdown strippers for card summaries. Sources feed agent-authored markdown
// (chat replies, automation result summaries, handoff objective docs) straight
// into a one-line plain-text subtitle, so raw tokens like "**done**" or
// "# Objective" would otherwise leak into the UI verbatim.
var (
	mdImageRe      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLinkRe       = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	mdBoldRe       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdBoldAltRe    = regexp.MustCompile(`__([^_]+)__`)
	mdItalicRe     = regexp.MustCompile(`\*([^*]+)\*`)
	mdCodeRe       = regexp.MustCompile("`([^`]+)`")
	mdStrikeRe     = regexp.MustCompile(`~~([^~]+)~~`)
	mdHeadingRe    = regexp.MustCompile(`^#{1,6}\s+`)
	mdLinePrefixRe = regexp.MustCompile(`^(?:>\s?|[-*+]\s+|\d{1,3}[.)]\s+)`)
	mdHrRe         = regexp.MustCompile(`^(?:-\s*){3,}$|^(?:\*\s*){3,}$|^(?:_\s*){3,}$`)
)

// stripInlineMarkdown reduces a single markdown line to its plain text:
// images/links keep their label, paired emphasis/code/strike delimiters are
// unwrapped, and leading blockquote/list markers are removed. Single
// underscores are deliberately left alone — agents emit snake_case identifiers
// far more often than `_italics_`, and unwrapping those would corrupt names.
//
// Mirrors stripInlineMarkdown in frontend/src/lib/agent-deliverables.ts (the
// frontend cleans chat descriptions before they reach the backend) — keep the
// two in sync. The parity tests in deliverables_test.go and
// agent-deliverables.test.ts share the same input/expectation pair.
func stripInlineMarkdown(line string) string {
	line = mdImageRe.ReplaceAllString(line, "$1")
	line = mdLinkRe.ReplaceAllString(line, "$1")
	line = mdBoldRe.ReplaceAllString(line, "$1")
	line = mdBoldAltRe.ReplaceAllString(line, "$1")
	line = mdItalicRe.ReplaceAllString(line, "$1")
	line = mdCodeRe.ReplaceAllString(line, "$1")
	line = mdStrikeRe.ReplaceAllString(line, "$1")
	for {
		stripped := mdLinePrefixRe.ReplaceAllString(line, "")
		if stripped == line {
			break
		}
		line = stripped
	}
	return strings.TrimSpace(line)
}

// deliverableSummaryLine returns the first content line of s as plain text,
// truncated to max runes, for a compact card subtitle. Markdown heading lines
// are skipped when body text follows: a structural title like "# Objective"
// carries no information on a card, while the paragraph under it does. If the
// text consists only of headings, the first one (sans markers) is used so the
// summary never comes back empty for non-empty input. Each candidate line is
// stripped of inline markdown BEFORE being accepted — a line that is pure
// markdown chrome (an alt-less image, a horizontal rule) must be skipped in
// favor of later body text, not picked and then emptied by the cleanup.
func deliverableSummaryLine(s string, max int) string {
	headingFallback := ""
	pick := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || mdHrRe.MatchString(line) {
			continue
		}
		if mdHeadingRe.MatchString(line) {
			if headingFallback == "" {
				headingFallback = stripInlineMarkdown(mdHeadingRe.ReplaceAllString(line, ""))
			}
			continue
		}
		if stripped := stripInlineMarkdown(line); stripped != "" {
			pick = stripped
			break
		}
	}
	if pick == "" {
		pick = headingFallback
	}
	r := []rune(pick)
	if max > 0 && len(r) > max {
		return string(r[:max]) + "…"
	}
	return pick
}

// mapRunStatus normalizes cron run status into the hub's status vocabulary.
func mapRunStatus(status string) string {
	switch status {
	case "ok":
		return "completed"
	case "error":
		return "failed"
	case "running":
		return "in_progress"
	case "skipped":
		return "skipped"
	default:
		return status
	}
}

// sortDeliverables orders entries by recency: the most recently updated
// deliverable floats to the top, regardless of status.
func sortDeliverables(entries []DeliverableEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].UpdatedAt > entries[j].UpdatedAt
	})
}
