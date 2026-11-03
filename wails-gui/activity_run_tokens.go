package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/platform/activitymon/appparser"
)

// ActivityEvidenceToken is an observed identifier, never an inferred summary
// fact or permission to act. LastSeenMs is the source event/snapshot time.
type ActivityEvidenceToken struct {
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	LastSeenMs int64  `json:"lastSeenMs"`
}

const activityEvidenceTokenLimit = 64

var (
	activityEvidencePathRE         = regexp.MustCompile("(?:^|[\\s\"'`(\\[=:])((?:~|\\.{1,2})?/[^\\s\"'`<>\\[\\]{}(),;]+)")
	activityEvidenceRelativePathRE = regexp.MustCompile("(?:^|[\\s\"'`(\\[=:])([\\w.-]+(?:/[\\w.-]+)+\\.[A-Za-z0-9]{1,12})")
	activityEvidenceFigureRE       = regexp.MustCompile(`(?i)\b(\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+(?:\.\d+)?)[ \t]*((?:tib|gib|mib|kib|tb|gb|mb|kb|bytes?|ms|seconds?|secs?|minutes?|mins?|hours?|hrs?|s|b)\b|%)`)
	activityEvidenceErrorRE        = regexp.MustCompile(`(?m)^((?i:error|fatal|panic)|[A-Za-z_]*(?:Error|Exception)|E[A-Z0-9_]{2,})(?::([^\r\n]*)|$)`)
	activityEvidenceRepoTokenRE    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	activityEvidencePRTokenRE      = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[0-9]+$`)
)

// Terminal OCR can include AI chat, so keep error codes only, never clauses.
// Bare words remain weak evidence; only unambiguous codes can support a join.
func extractActivityEvidenceTokens(text string, timestamp int64) []ActivityEvidenceToken {
	tokens := extractActivityIdentifierTokens(text, timestamp)
	text, _ = pii.RedactText(text)
	for _, match := range activityEvidenceErrorRE.FindAllStringSubmatch(text, 8) {
		if strings.Contains(strings.ToLower(match[0]), "redacted") {
			continue
		}
		tokens = append(tokens, ActivityEvidenceToken{Kind: "error", Value: match[1], LastSeenMs: timestamp})
	}
	return mergeActivityEvidenceTokens(nil, tokens)
}

func canonicalActivityEvidencePath(value string) string {
	value = path.Clean(strings.ReplaceAll(value, "\\", "/"))
	if home, err := os.UserHomeDir(); err == nil && home != "/" {
		if value == home {
			return "~"
		}
		if strings.HasPrefix(value, home+"/") {
			return "~" + strings.TrimPrefix(value, home)
		}
	}
	return value
}

// Identifiers are safe for message/AX sources: no free-word or error prose.
func extractActivityIdentifierTokens(text string, timestamp int64) []ActivityEvidenceToken {
	text, _ = pii.RedactText(text)
	var tokens []ActivityEvidenceToken
	add := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value == "" || strings.Contains(strings.ToLower(value), "redacted") {
			return
		}
		tokens = append(tokens, ActivityEvidenceToken{Kind: kind, Value: value, LastSeenMs: timestamp})
	}
	for _, re := range []*regexp.Regexp{activityEvidencePathRE, activityEvidenceRelativePathRE} {
		for _, indices := range re.FindAllStringSubmatchIndex(text, activityEvidenceTokenLimit) {
			// A redacted path component must not leave a joinable parent behind.
			if strings.HasPrefix(strings.ToLower(text[indices[3]:]), "[redacted") {
				continue
			}
			value := strings.TrimRight(text[indices[2]:indices[3]], ".:!?")
			if strings.HasPrefix(value, "//") || strings.Contains(value, "://") {
				continue
			}
			value = canonicalActivityEvidencePath(value)
			if len(strings.Split(strings.Trim(value, "~/.."), "/")) >= 2 {
				add("path", value)
			}
		}
	}
	for _, match := range activityEvidenceFigureRE.FindAllStringSubmatch(text, activityEvidenceTokenLimit) {
		unit := strings.ToLower(match[2])
		switch unit {
		case "byte", "bytes":
			unit = "b"
		case "second", "seconds", "sec", "secs":
			unit = "s"
		case "minute", "minutes", "mins":
			unit = "min"
		case "hour", "hours", "hr", "hrs":
			unit = "h"
		}
		add("figure", strings.ReplaceAll(match[1], ",", "")+" "+unit)
	}
	for _, match := range githubPRRe.FindAllStringSubmatch(text, activityEvidenceTokenLimit) {
		add("pr", strings.ToLower(match[1]+"/"+match[2]+"#"+match[3]))
	}
	for _, indices := range githubRepoRe.FindAllStringSubmatchIndex(text, activityEvidenceTokenLimit) {
		if goModDepSuffixRe.MatchString(text[indices[1]:]) {
			continue
		}
		add("repo", strings.ToLower(text[indices[2]:indices[3]]+"/"+strings.TrimSuffix(text[indices[4]:indices[5]], ".git")))
	}
	return mergeActivityEvidenceTokens(nil, tokens)
}

func mergeActivityEvidenceTokens(left, right []ActivityEvidenceToken) []ActivityEvidenceToken {
	byKey := make(map[string]ActivityEvidenceToken, len(left)+len(right))
	for _, batch := range [][]ActivityEvidenceToken{left, right} {
		for _, token := range batch {
			key := token.Kind + "\x00" + token.Value
			if old, exists := byKey[key]; !exists || token.LastSeenMs > old.LastSeenMs {
				byKey[key] = token
			}
		}
	}
	tokens := make([]ActivityEvidenceToken, 0, len(byKey))
	for _, token := range byKey {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].LastSeenMs != tokens[j].LastSeenMs {
			return tokens[i].LastSeenMs > tokens[j].LastSeenMs
		}
		if tokens[i].Kind != tokens[j].Kind {
			return tokens[i].Kind < tokens[j].Kind
		}
		return tokens[i].Value < tokens[j].Value
	})
	if len(tokens) > activityEvidenceTokenLimit {
		tokens = tokens[:activityEvidenceTokenLimit]
	}
	return tokens
}

// Validate stored identifiers too: legacy rows and callers must not bypass
// the extraction policy. Legacy errors lack source provenance and are dropped
// during migration; terminal signatures retain only their code.
func sanitizeActivityEvidenceTokens(tokens []ActivityEvidenceToken, allowErrors bool) []ActivityEvidenceToken {
	clean := []ActivityEvidenceToken{}
	for _, token := range tokens {
		value := strings.TrimSpace(token.Value)
		switch token.Kind {
		case "repo", "pr":
			re := activityEvidenceRepoTokenRE
			if token.Kind == "pr" {
				re = activityEvidencePRTokenRE
			}
			if re.MatchString(value) {
				token.Value = strings.ToLower(value)
				clean = append(clean, token)
			}
		case "path", "figure":
			if token.Kind == "path" {
				value = canonicalActivityEvidencePath(value)
			}
			for _, parsed := range extractActivityIdentifierTokens(value, token.LastSeenMs) {
				if parsed.Kind == token.Kind && strings.EqualFold(parsed.Value, value) {
					clean = append(clean, parsed)
					break
				}
			}
		case "error":
			if allowErrors {
				for _, parsed := range extractActivityEvidenceTokens(value, token.LastSeenMs) {
					if parsed.Kind == "error" {
						clean = append(clean, parsed)
						break
					}
				}
			}
		}
	}
	return mergeActivityEvidenceTokens(nil, clean)
}

func activityEvidenceTokensEqual(left, right []ActivityEvidenceToken) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// activityEvidenceAppExcluded applies both Settings privacy lists at read time,
// including names for legacy app-only runs and built-in password managers.
func activityEvidenceAppExcluded(appKey, appName string) bool {
	bundleID := strings.TrimPrefix(appKey, "app:")
	if _, protected := pii.IsSensitiveApp(bundleID); protected || pii.IsSensitiveAppName(appName) {
		return true
	}
	if isVoiceInputApp(bundleID) {
		return true
	}
	for _, entry := range AllVoiceInputAppEntries() {
		if strings.EqualFold(strings.TrimSpace(appName), entry.AppName) {
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(appName)) {
	case "1password", "bitwarden", "lastpass", "dashlane", "keepassxc", "enpass", "nordpass", "roboform", "keychain access", "passwords":
		return true
	}
	return false
}

func activityEvidenceSecureField(role string) bool {
	role = strings.ToLower(role)
	return strings.Contains(role, "securetextfield") || strings.Contains(role, "password")
}

func activityEvidenceSecureSnapshot(parsed *appparser.ParsedContent) bool {
	if parsed != nil {
		for key, value := range parsed.Metadata {
			switch strings.ToLower(key) {
			case "focusrole", "focusedrole", "focused-element-role", "inputtype", "fieldtype":
				if activityEvidenceSecureField(value) {
					return true
				}
			}
		}
	}
	return false
}

func activityEventEvidenceTokens(evt activitymon.ActivityEvent) []ActivityEvidenceToken {
	if evt.PrivateWindow || activitymon.IsPrivateBrowsing(evt.WindowTitle) || activityEvidenceAppExcluded(evt.AppBundleID, evt.AppName) || activityEvidenceSecureField(evt.FocusRole) || activityEvidenceSecureField(evt.ClickRole) {
		return nil
	}
	// Raw keystrokes are excluded: composed TypedText is the committed value.
	text := strings.Join([]string{evt.WindowTitle, evt.ClickLabel, evt.FocusLabel, evt.TypedText, evt.SpeechText}, "\n")
	return extractActivityIdentifierTokens(text, evt.Timestamp)
}

func persistTimelineRunEvidence(q sqlQueryer, run ActivityTimelineRunRow) error {
	tokens := sanitizeActivityEvidenceTokens(run.EvidenceTokens, true)
	if tokens == nil {
		tokens = []ActivityEvidenceToken{}
	}
	encoded, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("encode timeline run evidence: %w", err)
	}
	_, err = q.Exec(`UPDATE activity_timeline_groups SET
		evidence_tokens = CASE WHEN ? THEN '[]' ELSE ? END,
		evidence_excluded = ? WHERE id = ?`, run.EvidenceExcluded, string(encoded), run.EvidenceExcluded, run.ID)
	return err
}

// RefreshTimelineRunEvidence runs in the composer/lifecycle worker, not the
// session-save path. Source revisions detect late AX/OCR without reading raw
// captures again. Privacy changes also invalidate the fingerprint.
func (s *ActivityStore) RefreshTimelineRunEvidence(run *ActivityTimelineRunRow) (bool, error) {
	s.timelineRunsMu.Lock()
	defer s.timelineRunsMu.Unlock()
	stored, err := s.GetTimelineRunByID(run.ID)
	if err != nil {
		return false, err
	}
	if stored != nil {
		*run = *stored
	}
	fingerprint, err := s.timelineRunEvidenceFingerprint(*run)
	if err != nil {
		return false, err
	}
	var previous string
	err = s.reader().QueryRow(`SELECT source_fingerprint FROM activity_run_evidence_state WHERE run_id=?`, run.ID).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if previous == fingerprint {
		return !run.EvidenceExcluded, nil
	}
	var measurements []ActivityOutcomeEvidence
	allowed, err := s.collectTimelineRunEvidence(run, &measurements)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := persistTimelineRunEvidence(tx, *run); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO activity_run_evidence_state(run_id,source_fingerprint,measurement_evidence) VALUES(?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET source_fingerprint=excluded.source_fingerprint,measurement_evidence=excluded.measurement_evidence`, run.ID, fingerprint, composeJSON(measurements)); err != nil {
		return false, err
	}
	return allowed, tx.Commit()
}

func (s *ActivityStore) collectTimelineRunEvidence(run *ActivityTimelineRunRow, measurements *[]ActivityOutcomeEvidence) (bool, error) {
	exclude := func() (bool, error) {
		run.EvidenceTokens = []ActivityEvidenceToken{}
		run.EvidenceExcluded = true
		*measurements = nil
		return false, nil
	}
	if activityEvidenceAppExcluded(run.AppKey, run.AppName) {
		return exclude()
	}
	for _, sessionID := range run.SessionIDs {
		if s.isSessionProtected(sessionID) {
			return exclude()
		}
	}
	events, err := s.LoadTimelineRunEvents(*run)
	if err != nil {
		return false, err
	}
	// Preserve exclusion after complete raw retention, but re-evaluate it
	// whenever current source events are available (including corrected titles).
	if len(events) == 0 && run.EvidenceExcluded {
		return exclude()
	}
	run.EvidenceExcluded = false
	tokens := sanitizeActivityEvidenceTokens(run.EvidenceTokens, true)
	terminalOutput := activityAppFamily(run.AppName) == "terminal"
	measurementsAllowed := !activityEvidenceChatApp(run.AppKey, run.AppName)
	for _, evt := range events {
		if evt.PrivateWindow || activitymon.IsPrivateBrowsing(evt.WindowTitle) || activityEvidenceAppExcluded(evt.AppBundleID, evt.AppName) {
			return exclude()
		}
		tokens = mergeActivityEvidenceTokens(tokens, activityEventEvidenceTokens(evt.ActivityEvent))
		if activityEvidenceChatApp(evt.AppBundleID, evt.AppName) {
			terminalOutput = false
		}
		if activityOutcomeChat(evt.ActivityEvent) {
			measurementsAllowed = false
		}
	}
	if !terminalOutput {
		tokens = sanitizeActivityEvidenceTokens(tokens, false)
	}
	group := timelineRunToGroup(*run, nil)
	for _, sessionID := range run.SessionIDs {
		snapshots, err := s.GetSessionSnapshots(sessionID)
		if err != nil {
			return false, err
		}
		for _, snap := range snapshots {
			if !timelineGroupSnapshotMatches(group, snap) {
				continue
			}
			parsed := parseSnapshotContent(snap)
			if activityEvidenceAppExcluded(parsedSnapshotBundleID(snap.ParsedJSON), snap.App) {
				return exclude()
			}
			// Only actual focused-field metadata can suppress a snapshot. A
			// password mention in surrounding prose is not a secure input role.
			if activityEvidenceSecureSnapshot(parsed) {
				continue
			}
			// Pair measurement identifiers before the general token cap, using
			// the same events and snapshots already loaded for evidence.
			if measurementsAllowed && activityOutcomeWebDestination(snap.BrowserURL) == "" && (parsed == nil || parsed.ContentType != "conversation") {
				*measurements = mergeActivityOutcomeMeasurements(*measurements, activityOutcomeLineMeasurements(run.ID, fmt.Sprint(snap.ID), snap.AXText, snap.Timestamp))
				if parsed != nil {
					*measurements = mergeActivityOutcomeMeasurements(*measurements, activityOutcomeLineMeasurements(run.ID, fmt.Sprint(snap.ID), parsed.Body, snap.Timestamp))
				}
			}
			texts := []string{snap.AXText, snap.BrowserURL}
			allowErrors := terminalOutput
			if parsed != nil {
				allowErrors = allowErrors && parsed.ContentType != "conversation" && !activityEvidenceChatApp("", parsed.Title)
				redactParsedContentPII(parsed)
				texts = append(texts, parsed.Title, parsed.Body, parsed.URL)
				for _, message := range parsed.Messages {
					texts = append(texts, message.Content)
				}
			}
			// No sibling fallback: neighboring snapshots may belong to another
			// app, so only OCR for this exact matched snapshot can join a run.
			rows, err := s.reader().Query(`SELECT text FROM activity_ocr_frames WHERE session_id = ? AND segment_id = ?`, sessionID, snap.ID)
			if err != nil {
				return false, err
			}
			for rows.Next() {
				var text string
				if err := rows.Scan(&text); err != nil {
					rows.Close()
					return false, err
				}
				texts = append(texts, text)
				if allowErrors {
					tokens = mergeActivityEvidenceTokens(tokens, extractActivityEvidenceTokens(text, snap.Timestamp))
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return false, err
			}
			tokens = mergeActivityEvidenceTokens(tokens, extractActivityIdentifierTokens(strings.Join(texts, "\n"), snap.Timestamp))
		}
	}
	run.EvidenceTokens = tokens
	return true, nil
}
