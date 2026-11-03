package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// triageLogger writes structured triage decision events to a dedicated log file
// (~/.yansu-agent/triage.log) so operators can quickly verify that triage is
// working as expected, rather than having to sift through the main application log.
//
// Disk history contains counts for private text and readable fixed system
// reasons. Panel titles are resolved separately from current live entries.
type triageLogger struct {
	mu   sync.Mutex
	file *rotatingAppLog
	app  *App // set after app init for Wails event emission
}

var triage triageLogger

const (
	triageLogName    = "triage.log"
	triageMaxLogSize = 5 * 1024 * 1024 // three files of at most 5 MiB each
	triageMaxLines   = 500             // max lines returned by GetTriageLogs
)

// TriageLogEntry is a single triage decision exposed to the frontend.
type TriageLogEntry struct {
	Timestamp        string `json:"timestamp"`
	Source           string `json:"source"`
	ID               string `json:"id"`
	EntryID          string `json:"entry_id,omitempty"` // live artifact, distinct from the source session/conversation
	Decision         string `json:"decision"`
	Reason           string `json:"reason"`
	Title            string `json:"title,omitempty"`
	Description      string `json:"description,omitempty"`
	TitleBytes       *int   `json:"title_bytes,omitempty"`
	DescriptionBytes *int   `json:"description_bytes,omitempty"`
	ReasonBytes      *int   `json:"reason_bytes,omitempty"`
	WorthMemory      *bool  `json:"worthMemory,omitempty"`
	WorthKnowledge   *bool  `json:"worthKnowledge,omitempty"`
	WorthAutomation  *bool  `json:"worthAutomation,omitempty"`
	WorthHandoff     *bool  `json:"worthHandoff,omitempty"`
}

// initTriageLog opens (or creates) the triage log file with rotation.
// Call once at startup; safe to call multiple times.
func initTriageLog() {
	dir, err := flavorDataDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	logPath := filepath.Join(dir, triageLogName)

	triage.mu.Lock()
	defer triage.mu.Unlock()
	if triage.file != nil {
		_ = triage.file.Close()
		triage.file = nil
	}
	f, err := newRotatingAppLog(logPath, triageMaxLogSize)
	if err != nil {
		return
	}
	triage.file = f
}

func closeTriageLog() {
	triage.mu.Lock()
	defer triage.mu.Unlock()
	if triage.file != nil {
		_ = triage.file.Close()
		triage.file = nil
	}
}

// setTriageApp sets the App reference so logTriage can emit Wails events.
func setTriageApp(app *App) {
	triage.mu.Lock()
	defer triage.mu.Unlock()
	triage.app = app
}

// logTriage records a triage decision.
//
//	source   — subsystem that made the decision, e.g. "activity", "slack"
//	id       — identifier for the item being triaged (session ID, conversation ID, …)
//	decision — short label: "skip", "dismiss", "worth", "not_worth", etc.
//	reason   — human-readable explanation
func logTriage(source, id, decision, reason string) {
	logTriageEntry(TriageLogEntry{
		Source:   source,
		ID:       id,
		Decision: decision,
		Reason:   reason,
	})
}

// Only Isoform's fixed reasons may survive as text. In particular, a model
// reason is not made safe by calling it a system reason or by setting its source.
var triageDimensionReasonPattern = regexp.MustCompile(`^(connector conversation has zero \[SELF\] lines \(other=[0-9]+\): refused dimension="(user_insight|deep_user_model|agentic_memory)"|connector conversation dominated by \[OTHER\] \(self=[0-9]+ other=[0-9]+\): refused dimension="(user_insight|deep_user_model|agentic_memory)", coerced to topic_highlight|chat content dominated by non-self senders \(self=[0-9]+ non-self=[0-9]+\): refused dimension="(user_insight|deep_user_model|agentic_memory)", coerced to topic_highlight|(media playback app present in session|observed-dominant session|received-dominant session): refused dimension="(user_insight|deep_user_model|agentic_memory)", coerced to topic_highlight|legacy activity triage cooldown: [-0-9.hmsµnu]+ remaining)$`)

var triageSystemReasonPattern = regexp.MustCompile(`^(total=[0-9]+ eligible=[0-9]+ skippedLowMsg=[0-9]+ skippedAlreadyProcessed=[0-9]+ cachedTriage=[0-9]+|too few events: [0-9]+ < [0-9]+|too short duration: [-0-9.hmsµnu]+ < [-0-9.hmsµnu]+|too few apps: [0-9]+ < [0-9]+( \(events=[0-9]+ duration=[-0-9.hmsµnu]+, below high-activity exemption [0-9]+ / [-0-9.hmsµnu]+\))?)$`)

func triageSystemReason(reason string) bool {
	switch reason {
	case "", "all triage features disabled by settings", "AI returned no memory points", "AI returned no knowledge points", "ambient-dominant session: no extractable knowledge signal", triageSourceExcludedReason, "ambient-dominant session: no user-attributable signal", "received-dominant session: deep_user_model would attribute third-party content to user":
		return true
	default:
		return triageSystemReasonPattern.MatchString(reason) || triageDimensionReasonPattern.MatchString(reason)
	}
}

// triageLogMetadata is idempotent so retained counts describe the original
// fields, rather than the length of an earlier redaction token.
func triageLogMetadata(entry TriageLogEntry) TriageLogEntry {
	if entry.TitleBytes == nil || entry.Title != "" {
		n := len(entry.Title)
		entry.TitleBytes = &n
	}
	if entry.DescriptionBytes == nil || entry.Description != "" {
		n := len(entry.Description)
		entry.DescriptionBytes = &n
	}
	entry.Title, entry.Description = "", ""
	// Model triage and failures always use the private projection, even if
	// their text happens to resemble an internal reason.
	privateReason := entry.Decision == "worth" || entry.Decision == "not_worth" || entry.Decision == "error"
	if !privateReason && triageSystemReason(entry.Reason) {
		return entry
	}
	field := "reason"
	if entry.Decision == "error" {
		field = "error"
	}
	if entry.ReasonBytes != nil {
		expected := fmt.Sprintf("reason_bytes=%d", *entry.ReasonBytes)
		if field == "error" {
			expected = fmt.Sprintf("error_kind=%s error_bytes=%d", activityLogErrorKind(entry.Reason), *entry.ReasonBytes)
		}
		if entry.Reason == expected {
			return entry
		}
	}
	n := len(entry.Reason)
	entry.ReasonBytes = &n
	entry.Reason = activityLogFieldMetadata(field, entry.Reason)
	return entry
}

// Titles are a current view of the live entry, never retained diagnostic text.
// Missing entries, Forget markers and current source privacy all fail closed.
func (a *App) triagePanelEntry(entry TriageLogEntry) TriageLogEntry {
	entry = triageLogMetadata(entry)
	if a == nil || entry.EntryID == "" {
		return entry
	}
	allowSource, err := a.memoryReadFilter()
	if err != nil {
		return entry
	}
	allow := func(id, content string) bool {
		if parseMemoryFrontmatter(content).ReadProvenance != "" {
			provenance := memoryReadProvenanceFromContent(content)
			if provenance.Withheld || provenance.ReadBlocked {
				return false
			}
		}
		return allowSource(id, content)
	}
	release := acquireMemoryMutationRead()
	defer release()
	switch entry.Source {
	case "memory":
		unlock := memoryFileLocks.Acquire(entry.EntryID)
		defer unlock()
		live, err := a.GetMemoryEntry(entry.EntryID)
		if err != nil || !allow(live.ID, live.Content) {
			return entry
		}
		dir, err := memoryDir()
		if err != nil {
			return entry
		}
		forgotten, err := isMemoryForgotten(dir, live.ID, live.Content, time.Now())
		if err == nil && !forgotten {
			entry.Title = live.Title
		}
	case "knowledge":
		unlock := memoryFileLocks.Acquire("knowledge:" + entry.EntryID)
		defer unlock()
		live, err := a.GetKnowledgeEntry(entry.EntryID)
		if err == nil && allow(live.ID, live.Content) {
			entry.Title = live.Title
		}
	}
	return entry
}

// logTriageEntry records a full triage entry, including optional worth fields.
func logTriageEntry(entry TriageLogEntry) {
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	entry.Timestamp = ts

	// Build the log line with optional worth fields.
	var worthParts string
	if entry.WorthMemory != nil || entry.WorthKnowledge != nil || entry.WorthAutomation != nil || entry.WorthHandoff != nil {
		parts := make([]string, 0, 4)
		if entry.WorthMemory != nil {
			parts = append(parts, fmt.Sprintf("worth_memory=%v", *entry.WorthMemory))
		}
		if entry.WorthKnowledge != nil {
			parts = append(parts, fmt.Sprintf("worth_knowledge=%v", *entry.WorthKnowledge))
		}
		if entry.WorthAutomation != nil {
			parts = append(parts, fmt.Sprintf("worth_automation=%v", *entry.WorthAutomation))
		}
		if entry.WorthHandoff != nil {
			parts = append(parts, fmt.Sprintf("worth_handoff=%v", *entry.WorthHandoff))
		}
		worthParts = " " + strings.Join(parts, " ")
	}

	// Use the same source-free projection for ordinary application diagnostics.
	diskEntry := triageLogMetadata(entry)
	log.Printf("[triage] %s id=%s decision=%s%s title_bytes=%d description_bytes=%d reason=%s",
		entry.Source, entry.ID, entry.Decision, worthParts, *diskEntry.TitleBytes, *diskEntry.DescriptionBytes, diskEntry.Reason)

	// Redact before persistence regardless of the live-event debug setting.
	line, _ := json.Marshal(diskEntry)
	triage.mu.Lock()
	app := triage.app
	if triage.file != nil {
		fmt.Fprintln(triage.file, string(line))
	}
	triage.mu.Unlock()

	// Raw source text is transient and available only with exact debug opt-in.
	if app != nil && app.ctx != nil {
		eventEntry := diskEntry
		if os.Getenv(backgroundTranscriptDebugEnv) == "1" {
			eventEntry = entry
			eventEntry.TitleBytes = diskEntry.TitleBytes
			eventEntry.DescriptionBytes = diskEntry.DescriptionBytes
			eventEntry.ReasonBytes = diskEntry.ReasonBytes
		}
		// Knowledge and memory titles always come from the live artifact,
		// including in debug mode. A removed/private entry cannot be revived
		// by either an event payload or retained model text.
		switch entry.Source {
		case "knowledge", "memory":
			eventEntry = app.triagePanelEntry(diskEntry)
		}
		app.emitBrowserEvent("triage:log", eventEntry)
	}
}

// GetTriageLogs returns the most recent triage log entries (up to triageMaxLines).
// Exposed as a Wails binding.
func (a *App) GetTriageLogs() (perfValue []TriageLogEntry) {
	defer logPageBinding("GetTriageLogs", time.Now(), &perfValue)
	entries := readTriageLogs(scanTriageLogEntries)
	for i, entry := range entries {
		entries[i] = a.triagePanelEntry(entry)
	}
	return entries
}

func readTriageLogs(scan func(io.Reader, func(TriageLogEntry))) []TriageLogEntry {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return nil
	}
	logPath := filepath.Join(dataRoot, triageLogName)

	// Open retained files and snapshot their sizes while rotation is excluded.
	// Descriptors continue to name the same files after a rename, and the size
	// bound excludes later appends. Parsing can then proceed without delaying
	// triage decisions, even when Settings loads the entire retained history.
	type snapshot struct {
		file *os.File
		size int64
	}
	var files []snapshot
	triage.mu.Lock()
	for _, suffix := range []string{".old.2", ".old", ""} {
		f, err := openTriageLogSnapshot(logPath + suffix)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			continue
		}
		files = append(files, snapshot{file: f, size: info.Size()})
	}
	triage.mu.Unlock()
	defer func() {
		for _, retained := range files {
			_ = retained.file.Close()
		}
	}()

	// Keep a bounded ring so both memory use and displayed history stay bounded.
	var entries []TriageLogEntry
	next := 0
	for _, retained := range files {
		scan(io.LimitReader(retained.file, retained.size), func(entry TriageLogEntry) {
			if len(entries) < triageMaxLines {
				entries = append(entries, entry)
			} else {
				entries[next] = entry
				next = (next + 1) % triageMaxLines
			}
		})
	}
	if next != 0 {
		entries = append(entries[next:], entries[:next]...)
	}
	return entries
}

func scanTriageLogEntries(reader io.Reader, appendEntry func(TriageLogEntry)) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), triageMaxLogSize+1)
	for scanner.Scan() {
		entry, ok := parseTriageLogLine(scanner.Text())
		if ok {
			appendEntry(entry)
		}
	}
}

// parseTriageLogLine accepts structured JSON records and retained legacy lines:
//
//	2026-03-08 10:15:32.123 [activity] id=session-abc decision=skip reason=cooldown active
func parseTriageLogLine(line string) (TriageLogEntry, bool) {
	if strings.HasPrefix(line, "{") {
		var entry TriageLogEntry
		err := json.Unmarshal([]byte(line), &entry)
		return entry, err == nil && entry.Timestamp != "" && entry.Source != "" && entry.Decision != ""
	}

	// Timestamp is the first 23 characters: "2006-01-02 15:04:05.000"
	if len(line) < 26 {
		return TriageLogEntry{}, false
	}
	ts := line[:23]
	rest := line[24:] // skip space after timestamp

	// Extract [source]
	if !strings.HasPrefix(rest, "[") {
		return TriageLogEntry{}, false
	}
	closeBracket := strings.Index(rest, "]")
	if closeBracket < 0 {
		return TriageLogEntry{}, false
	}
	source := rest[1:closeBracket]
	rest = strings.TrimPrefix(rest[closeBracket+1:], " ")

	entry := TriageLogEntry{
		Timestamp: ts,
		Source:    source,
	}

	// Parse key=value pairs. The "reason" field consumes everything remaining.
	for rest != "" {
		eqIdx := strings.Index(rest, "=")
		if eqIdx < 0 {
			break
		}
		key := rest[:eqIdx]
		rest = rest[eqIdx+1:]

		var value string
		switch key {
		case "reason":
			// reason consumes the rest of the line
			value = rest
			rest = ""
		default:
			spIdx := strings.Index(rest, " ")
			if spIdx < 0 {
				value = rest
				rest = ""
			} else {
				value = rest[:spIdx]
				rest = rest[spIdx+1:]
			}
		}

		switch key {
		case "id":
			entry.ID = value
		case "decision":
			entry.Decision = value
		case "reason":
			entry.Reason = value
		case "worth_memory":
			b := value == "true"
			entry.WorthMemory = &b
		case "worth_knowledge":
			b := value == "true"
			entry.WorthKnowledge = &b
		case "worth_automation":
			b := value == "true"
			entry.WorthAutomation = &b
		}
	}

	return entry, true
}
