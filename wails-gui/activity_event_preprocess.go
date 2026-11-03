package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"wails-gui/internal/platform/activitymon/appparser"
)

// Observation is a preprocessed, de-noised unit of desktop activity — the real
// input to the event-extraction LLM. Its 0-based Index in an ObservationSequence
// is what extracted events reference via source_indices. One Observation may
// fold several raw snapshots (scrolling collapse) or carry a whole meeting's
// transcript. See docs/activity-event-model-p1-design.md (local) §3.
type Observation struct {
	Index            int      `json:"index"`
	StartMs          int64    `json:"startMs"`
	EndMs            int64    `json:"endMs"`
	RealApp          string   `json:"realApp"` // resolved app, NOT raw snapshot.app
	Title            string   `json:"title"`   // de-noised conversation / window title
	Body             string   `json:"body"`    // clean body (union / transcript)
	URL              string   `json:"url,omitempty"`
	Entities         []Entity `json:"entities,omitempty"` // filled by the entity-linking layer (§4)
	SnapshotIDs      []int64  `json:"snapshotIds"`        // raw snapshots folded into this observation
	SourceSessionIDs []string `json:"sourceSessionIds,omitempty"`
	SessionID        string   `json:"sessionId"`
	Source           string   `json:"source"` // "screen" | "meeting_transcript"
}

// ObservationSequence is the chronological, de-noised observation list for one
// preprocessing pass (typically one session).
type ObservationSequence struct {
	StartMs      int64         `json:"startMs"`
	EndMs        int64         `json:"endMs"`
	Observations []Observation `json:"observations"`
}

const (
	// scrollCollapseGapMs: consecutive snapshots of the SAME conversation within
	// this gap are folded into one observation (scrolling the same thread).
	scrollCollapseGapMs = 90 * 1000
	// minBodyLen: bodies at or below window-title length are treated as degraded
	// (metadata_only terminals etc.) and replaced by OCR text when available.
	minBodyLen = 24
)

// unknownAppName is the placeholder returned when real-app inference fails. It
// must never be counted as a real app for cross-app aggregation/stats, or a
// single terminal whose source could not be resolved looks like a second app.
const unknownAppName = "?"

// knownBundleMap maps a parser-reported bundle ID to a stable display app name.
// bundleId is the STRONGEST real-app signal: raw snapshot.app is frequently
// wrong during fast window switches (validation #6/#11/#25), but the parser's
// metadata.bundleId reflects the window the content was actually parsed from.
var knownBundleMap = map[string]string{
	"com.tinyspeck.slackmacgap": "Slack",
	"com.tencent.xinWeChat":     "WeChat",
	"com.hnc.Discord":           "Discord",
	"com.google.Chrome":         "Chrome",
	"com.googlecode.iterm2":     "iTerm2",
	"com.cmuxterm.app":          "cmux",
	"md.obsidian":               "Obsidian",
	"com.airjelly.app":          "AirJelly",
	"com.apple.finder":          "Finder",
}

// titleAppHints are weaker, body/title-derived fallbacks (a window title like
// "... - Slack" strongly implies Slack). Order matters only for determinism.
var titleAppHints = []struct {
	needle string
	app    string
}{
	{" - Slack", "Slack"},
	{" - Google Chrome", "Chrome"},
	{" - Discord", "Discord"},
	{"Obsidian", "Obsidian"},
}

var zoomURLRe = regexp.MustCompile(`zoom\.us/(j|wc)/`)

// unreadNoiseRe strips Slack/Discord unread badges and "N new item(s)" suffixes
// so the same conversation produces a stable obsConversationKey across scrolling.
var unreadNoiseRe = regexp.MustCompile(`(?i)\s*[-—|]\s*\d+\s+new\s+items?\b|\s*\(\d+\)\s*$|^\s*!?\s*`)

// parseSnapshotContent unmarshals a snapshot's parsed_json into ParsedContent.
// Returns nil when absent or unparsable (caller falls back to ax_text / OCR).
func parseSnapshotContent(sn ActivitySnapshotRow) *appparser.ParsedContent {
	if strings.TrimSpace(sn.ParsedJSON) == "" {
		return nil
	}
	var pc appparser.ParsedContent
	if err := json.Unmarshal([]byte(sn.ParsedJSON), &pc); err != nil {
		return nil
	}
	return &pc
}

// appAliases maps parser app keys to one canonical display name, so the same app
// never appears under two labels. The WeChat/Slack/Discord parsers emit
// "tencent_chat"/"slack"/"discord" while the bundle map yields "WeChat"/"Slack"/
// "Discord": frames with a bundle id take the bundle name, frames without (parser
// matched but no bundleId) fall back to the parser key. Canonicalizing collapses
// both onto a single label.
var appAliases = map[string]string{
	"tencent_chat": "WeChat",
	"slack":        "Slack",
	"discord":      "Discord",
}

func canonicalAppName(app string) string {
	if c, ok := appAliases[app]; ok {
		return c
	}
	return app
}

// resolveRealApp reverse-infers the true source app for a snapshot, distrusting
// the raw snapshot.app field. Priority: bundleId > title suffix > raw app (§3a),
// then canonicalized so parser-key and bundle-name variants collapse to one label.
func resolveRealApp(parsed *appparser.ParsedContent, rawApp string) string {
	return canonicalAppName(resolveRealAppRaw(parsed, rawApp))
}

func resolveRealAppRaw(parsed *appparser.ParsedContent, rawApp string) string {
	if parsed != nil {
		if bid := strings.TrimSpace(parsed.Metadata["bundleId"]); bid != "" {
			if name, ok := knownBundleMap[bid]; ok {
				return name
			}
			// Unknown bundle: prefer the window owner name (carried in rawApp) over
			// the bundle's last segment, which is often a generic word that collides
			// across apps (com.termius-dmg.mac → "mac"). Fall back to the derived
			// last segment only when no owner name is available.
			if r := strings.TrimSpace(rawApp); r != "" {
				return r
			}
			return appNameFromBundleID(bid)
		}
		title := parsed.Title
		for _, hint := range titleAppHints {
			if strings.Contains(title, hint.needle) {
				return hint.app
			}
		}
	}
	if rawApp != "" {
		return rawApp
	}
	return unknownAppName
}

// appNameFromBundleID derives a display name from a bundle id's last segment,
// e.g. "com.west2online.ClashX" -> "ClashX", "com.hnc.Discord" -> "Discord".
func appNameFromBundleID(bid string) string {
	if i := strings.LastIndex(bid, "."); i >= 0 && i+1 < len(bid) {
		return bid[i+1:]
	}
	return bid
}

// obsConversationKey produces a stable identity for scrolling-collapse: same key +
// same app + small time gap => same thread being scrolled (§3b).
func obsConversationKey(parsed *appparser.ParsedContent, realApp, axText string) string {
	base := ""
	if parsed != nil {
		switch {
		case parsed.Channel != "":
			base = parsed.Channel
		case parsed.Title != "":
			base = parsed.Title
		}
	}
	if base == "" {
		base = firstLine(axText)
	}
	return realApp + "|" + normalizeConversationKey(base)
}

func normalizeConversationKey(s string) string {
	s = unreadNoiseRe.ReplaceAllString(s, "")
	return strings.ToLower(strings.TrimSpace(s))
}

// cleanBody returns the best clean body text for a snapshot, falling back to OCR
// when the parsed body is degraded (metadata_only terminals, validation #1/#9).
func cleanBody(parsed *appparser.ParsedContent, sn ActivitySnapshotRow, ocr string) string {
	body := ""
	if parsed != nil {
		body = strings.TrimSpace(parsed.Body)
		// A body that merely echoes the title carries no information.
		if body == strings.TrimSpace(parsed.Title) {
			body = ""
		}
	}
	if len(body) < minBodyLen {
		if ax := strings.TrimSpace(sn.AXText); len(ax) >= minBodyLen && ax != body {
			body = ax
		}
	}
	if len(body) < minBodyLen && strings.TrimSpace(ocr) != "" {
		body = strings.TrimSpace(ocr)
	}
	return body
}

type scrollBucket struct {
	key         string
	app         string
	title       string
	body        string
	url         string
	startMs     int64
	endMs       int64
	lastMs      int64
	snapshotIDs []int64
	focusIDs    []int64 // subset with a usable focus rect (preferred for thumbnails)
	sessionID   string
}

func (b *scrollBucket) toObservation() Observation {
	return Observation{
		StartMs: b.startMs,
		EndMs:   b.endMs,
		RealApp: b.app,
		Title:   b.title,
		Body:    b.body,
		URL:     b.url,
		// Focus-valid frames first so topThumbnails (which takes the leading N)
		// prefers frames that crop to the focused window over full-screen ones.
		SnapshotIDs: orderFocusFirst(b.snapshotIDs, b.focusIDs),
		SessionID:   b.sessionID,
		Source:      "screen",
	}
}

// thumbFocusMinFraction mirrors the front-end's hasFocus threshold: a focus rect
// smaller than this on either axis is treated as "unknown" (full-frame).
const thumbFocusMinFraction = 0.02

func snapshotHasFocus(sn ActivitySnapshotRow) bool {
	return sn.FocusRectW > thumbFocusMinFraction && sn.FocusRectH > thumbFocusMinFraction
}

// orderFocusFirst returns all ids with the focus-valid ones moved to the front,
// each group keeping its original (chronological) order.
func orderFocusFirst(all, focus []int64) []int64 {
	if len(focus) == 0 || len(focus) == len(all) {
		return all
	}
	focusSet := make(map[int64]bool, len(focus))
	for _, id := range focus {
		focusSet[id] = true
	}
	out := make([]int64, 0, len(all))
	out = append(out, focus...)
	for _, id := range all {
		if !focusSet[id] {
			out = append(out, id)
		}
	}
	return out
}

// ownActivitySurfaceMarkers are strings unique to OUR OWN Activity timeline UI
// (the on-device redaction notice + the view chrome). When a snapshot's captured
// text contains one, the user is looking at our activity view — meta, not real
// activity. Such snapshots are dropped so the event pipeline never re-ingests its
// own output: a feedback loop that is the root cause of meta-noise events and
// pollutes real cross-window content (validated: our Activity page occupied ~87%
// of a 52KB ax_text, burying a real WeChat conversation past the body cutoff).
var ownActivitySurfaceMarkers = []string{
	"auto-redacted on device",
	"All recorded data is auto-redacted",
}

func isOwnActivitySurface(texts ...string) bool {
	for _, t := range texts {
		for _, m := range ownActivitySurfaceMarkers {
			if strings.Contains(t, m) {
				return true
			}
		}
		// Backup signal: the Activity page's Events/Timeline/Summary tabs stay in
		// the AX tree even after the redaction notice scrolls out of view.
		if strings.Contains(t, "Events") && strings.Contains(t, "Timeline") && strings.Contains(t, "Summary") {
			return true
		}
	}
	return false
}

// collapseScrolling folds consecutive same-conversation snapshots into one
// observation, taking the longest body as the union (§3b). contentHash dedup
// fails here because every scroll frame differs; this is identity-based.
func collapseScrolling(snapshots []ActivitySnapshotRow, ocrBySnapshot map[int64]string) []Observation {
	sorted := make([]ActivitySnapshotRow, len(snapshots))
	copy(sorted, snapshots)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Timestamp < sorted[j].Timestamp })

	var out []Observation
	var bucket *scrollBucket
	flush := func() {
		if bucket != nil {
			out = append(out, bucket.toObservation())
			bucket = nil
		}
	}
	for _, sn := range sorted {
		// Skip snapshots of our own Activity UI (self-pollution / meta-noise root).
		if isOwnActivitySurface(sn.AXText, ocrBySnapshot[sn.ID]) {
			continue
		}
		parsed := parseSnapshotContent(sn)
		app := resolveRealApp(parsed, sn.App)
		key := obsConversationKey(parsed, app, sn.AXText)
		body := cleanBody(parsed, sn, ocrBySnapshot[sn.ID])
		title := ""
		url := sn.BrowserURL
		if parsed != nil {
			title = strings.TrimSpace(parsed.Title)
			if parsed.URL != "" {
				url = parsed.URL
			}
		}

		if bucket != nil && bucket.key == key && bucket.app == app &&
			sn.Timestamp-bucket.lastMs < scrollCollapseGapMs {
			bucket.snapshotIDs = append(bucket.snapshotIDs, sn.ID)
			if snapshotHasFocus(sn) {
				bucket.focusIDs = append(bucket.focusIDs, sn.ID)
			}
			if len(body) > len(bucket.body) {
				bucket.body = body // longest body = fullest union of the scroll
			}
			if bucket.title == "" {
				bucket.title = title
			}
			if bucket.url == "" {
				bucket.url = url
			}
			bucket.endMs = sn.Timestamp
			bucket.lastMs = sn.Timestamp
			continue
		}
		flush()
		bucket = &scrollBucket{
			key: key, app: app, title: title, body: body, url: url,
			startMs: sn.Timestamp, endMs: sn.Timestamp, lastMs: sn.Timestamp,
			snapshotIDs: []int64{sn.ID}, sessionID: sn.SessionID,
		}
		if snapshotHasFocus(sn) {
			bucket.focusIDs = []int64{sn.ID}
		}
	}
	flush()
	return out
}

// isMeetingSession decides whether a session should be treated as a meeting and
// have its screen body replaced by the ASR transcript (§3c). Zoom screen bodies
// are bookmark-bar noise; the transcript is the only real signal.
func isMeetingSession(session ActivitySessionRow, snapshots []ActivitySnapshotRow) bool {
	if session.IsMeeting || strings.TrimSpace(session.MeetingApp) != "" {
		return true
	}
	for _, sn := range snapshots {
		if zoomURLRe.MatchString(sn.BrowserURL) {
			return true
		}
	}
	return false
}

// meetingObservation builds a single transcript-backed observation for a meeting
// session, or nil when it is not a meeting / has no transcript text.
func meetingObservation(session ActivitySessionRow, snapshots []ActivitySnapshotRow, transcript string) *Observation {
	if !isMeetingSession(session, snapshots) {
		return nil
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return nil
	}
	app := session.MeetingApp
	if app == "" {
		app = "Meeting"
	}
	ids := make([]int64, 0, len(snapshots))
	for _, sn := range snapshots {
		ids = append(ids, sn.ID)
	}
	return &Observation{
		StartMs:     session.StartTime,
		EndMs:       session.EndTime,
		RealApp:     app,
		Title:       firstLine(transcript),
		Body:        transcript,
		SnapshotIDs: ids,
		SessionID:   session.ID,
		Source:      "meeting_transcript",
	}
}

// buildObservationSequence is the pure preprocessing entry point: meeting →
// transcript observation; otherwise scrolling-collapsed screen observations.
// ocrBySnapshot supplies OCR fallback for degraded bodies; transcript is the
// session's ASR text (empty when none).
func buildObservationSequence(
	session ActivitySessionRow,
	snapshots []ActivitySnapshotRow,
	ocrBySnapshot map[int64]string,
	transcript string,
) ObservationSequence {
	var obs []Observation
	if m := meetingObservation(session, snapshots, transcript); m != nil {
		obs = []Observation{*m}
	} else {
		obs = collapseScrolling(snapshots, ocrBySnapshot)
	}

	// Drop observations with no usable signal (no body and no title).
	filtered := obs[:0]
	for _, o := range obs {
		if strings.TrimSpace(o.Body) == "" && strings.TrimSpace(o.Title) == "" {
			continue
		}
		filtered = append(filtered, o)
	}
	obs = filtered

	seq := ObservationSequence{Observations: obs}
	for i := range obs {
		obs[i].Index = i
		if i == 0 || obs[i].StartMs < seq.StartMs {
			seq.StartMs = obs[i].StartMs
		}
		if obs[i].EndMs > seq.EndMs {
			seq.EndMs = obs[i].EndMs
		}
	}
	return seq
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return strings.TrimSpace(s)
}
