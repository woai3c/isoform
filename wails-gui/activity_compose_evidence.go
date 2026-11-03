package main

import (
	"database/sql"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	activityCandidateLimit          = 6
	activityCandidateWindowMs int64 = 2 * 60 * 60 * 1000
	activityStrongLinkMs      int64 = 24 * 60 * 60 * 1000
	activityWeakLinkMs        int64 = 30 * 60 * 1000
)

func activityEvidenceKey(token ActivityEvidenceToken) string {
	if token.Kind == "path" {
		return token.Kind + ":" + canonicalActivityEvidencePath(token.Value)
	}
	return token.Kind + ":" + strings.ToLower(strings.TrimSpace(token.Value))
}

// Count distinct tasks, not their runs. Returning repeatedly to the same task
// must not turn its own specific identifier into a generic anchor.
func commonActivityEvidence(buckets []ActivityRow, now int64) map[string]bool {
	counts := map[string]int{}
	total := 0
	seenBuckets := map[string]bool{}
	for _, bucket := range buckets {
		if bucket.ID == "" || seenBuckets[bucket.ID] {
			continue
		}
		seenBuckets[bucket.ID] = true
		total++
		seen := map[string]bool{}
		for _, token := range bucket.EvidenceTokens {
			if token.LastSeenMs > now || now-token.LastSeenMs > activityStrongLinkMs {
				continue
			}
			key := activityEvidenceKey(token)
			if !seen[key] {
				counts[key]++
				seen[key] = true
			}
		}
	}
	common := map[string]bool{}
	for key, count := range counts {
		if count >= 4 && count*2 >= total {
			common[key] = true
		}
	}
	return common
}

// timelineRunEvidenceAllowed rechecks privacy without re-reading capture text
// or OCR. Persisted tokens already came through the extraction boundary.
func (s *ActivityStore) timelineRunEvidenceAllowed(run ActivityTimelineRunRow) (bool, error) {
	return timelineRunEvidenceAllowed(s.reader(), run)
}

func timelineRunEvidenceAllowed(q sqlQueryer, run ActivityTimelineRunRow) (bool, error) {
	if run.EvidenceExcluded || activityEvidenceAppExcluded(run.AppKey, run.AppName) {
		return false, nil
	}
	var excluded bool
	err := q.QueryRow(`SELECT evidence_excluded FROM activity_timeline_groups WHERE id = ?`, run.ID).Scan(&excluded)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if excluded {
		return false, nil
	}
	for _, id := range run.SessionIDs {
		var status string
		err := q.QueryRow(`SELECT triage_status FROM activity_sessions WHERE id = ?`, id).Scan(&status)
		if err != nil && err != sql.ErrNoRows {
			return false, err
		}
		if status == "protected" {
			return false, nil
		}
	}
	return true, nil
}

func (s *ActivityStore) commonComposerEvidence(now int64) (map[string]bool, error) {
	return commonComposerEvidence(s.reader(), now)
}

func commonComposerEvidence(q sqlQueryer, now int64) (map[string]bool, error) {
	rows, err := q.Query(`SELECT l.activity_id, g.id, g.app_key, g.app_name, g.session_ids, g.evidence_tokens, g.evidence_excluded
		FROM activity_run_links l JOIN activity_timeline_groups g ON g.id = l.run_id
		WHERE l.activity_id != '' AND g.end_ms >= ? AND g.start_ms <= ?`, max(int64(0), now-activityStrongLinkMs), now)
	if err != nil {
		return nil, err
	}
	type member struct {
		activity string
		run      ActivityTimelineRunRow
	}
	var members []member
	for rows.Next() {
		var item member
		var sessions, tokens string
		if err := rows.Scan(&item.activity, &item.run.ID, &item.run.AppKey, &item.run.AppName, &sessions, &tokens, &item.run.EvidenceExcluded); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(sessions), &item.run.SessionIDs); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(tokens), &item.run.EvidenceTokens); err != nil {
			rows.Close()
			return nil, err
		}
		item.run.EvidenceTokens = sanitizeActivityEvidenceTokens(item.run.EvidenceTokens, true)
		members = append(members, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	byBucket := map[string][]ActivityEvidenceToken{}
	for _, item := range members {
		allowed, err := timelineRunEvidenceAllowed(q, item.run)
		if err != nil {
			return nil, err
		}
		if allowed {
			byBucket[item.activity] = append(byBucket[item.activity], item.run.EvidenceTokens...)
		}
	}
	buckets := make([]ActivityRow, 0, len(byBucket))
	for id, tokens := range byBucket {
		buckets = append(buckets, ActivityRow{ID: id, EvidenceTokens: tokens})
	}
	return commonActivityEvidence(buckets, now), nil
}

func activityPathMatches(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	a = canonicalActivityEvidencePath(a)
	b = canonicalActivityEvidencePath(b)
	if a == "" || b == "" {
		return false
	}
	// Capture tokens do not establish whether a path names a file or a repo.
	// An arbitrary directory prefix is therefore not the same target.
	return a == b
}

var activityFigurePattern = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)[ \t]*([a-z%]+)$`)

// Figures compare in base units, with a 2% rounding tolerance. Unit dimensions
// remain separate: storage, time and percentages must never match each other.
func activityFigureValue(s string) (float64, string, bool) {
	m := activityFigurePattern.FindStringSubmatch(strings.ReplaceAll(strings.TrimSpace(s), ",", ""))
	if m == nil {
		return 0, "", false
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, "", false
	}
	unit := strings.ToLower(m[2])
	multipliers := map[string]float64{"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "kib": 1024, "mib": 1048576, "gib": 1073741824, "tib": 1099511627776}
	if scale, ok := multipliers[unit]; ok {
		return value * scale, "bytes", true
	}
	times := map[string]float64{"ms": 0.001, "s": 1, "sec": 1, "secs": 1, "second": 1, "seconds": 1, "min": 60, "mins": 60, "minute": 60, "minutes": 60, "h": 3600, "hr": 3600, "hours": 3600}
	if scale, ok := times[unit]; ok {
		return value * scale, "seconds", true
	}
	return value, unit, true
}

func activityFigureMatches(a, b string) bool {
	av, au, aok := activityFigureValue(a)
	bv, bu, bok := activityFigureValue(b)
	if !aok || !bok || au != bu {
		return false
	}
	if au == "%" {
		return av == bv
	}
	return math.Abs(av-bv) <= math.Max(math.Abs(av), math.Abs(bv))*0.02
}

func activityEvidenceMatches(a, b ActivityEvidenceToken) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case "path":
		return activityPathMatches(a.Value, b.Value)
	case "figure":
		return activityFigureMatches(a.Value, b.Value)
	default:
		return strings.EqualFold(strings.TrimSpace(a.Value), strings.TrimSpace(b.Value)) && strings.TrimSpace(a.Value) != ""
	}
}

func activityEvidenceWeight(token ActivityEvidenceToken, common map[string]bool) (int, bool) {
	if common[activityEvidenceKey(token)] {
		return 10, false
	}
	switch token.Kind {
	case "path":
		if strings.TrimSpace(token.Value) == "" {
			return 0, false
		}
		path := canonicalActivityEvidencePath(token.Value)
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if path == "." || path == "/" || (strings.HasPrefix(path, "/") && len(parts) < 2) {
			return 0, false
		}
		if path == "~" {
			return 10, false
		}
		if strings.HasPrefix(path, "~/") {
			parts = parts[1:]
			// App-owned data is concrete after the app and child directory;
			// source collections and bare repository roots remain weak.
			if len(parts) < 3 && !(len(parts) >= 2 && strings.HasPrefix(parts[0], ".")) {
				return 10, false
			}
		} else if strings.HasPrefix(path, "/Users/") || strings.HasPrefix(path, "/home/") {
			if len(parts) < 5 && !(len(parts) >= 4 && strings.HasPrefix(parts[2], ".")) {
				return 10, false
			}
		} else if !strings.HasPrefix(path, "/") || len(parts) < 3 {
			return 10, false
		}
		return 120, true
	case "pr":
		return 120, true
	case "error":
		// Extraction retains only codes, never clauses. Require a code marker
		// that survives that privacy boundary before treating it as strong.
		code := strings.TrimSpace(token.Value)
		if strings.ContainsAny(code, "0123456789_") {
			return 110, true
		}
		return 10, false
	case "figure":
		value, unit, ok := activityFigureValue(token.Value)
		if !ok || unit == "%" {
			return 10, false
		}
		match := activityFigurePattern.FindStringSubmatch(strings.ReplaceAll(strings.TrimSpace(token.Value), ",", ""))
		if len(match) != 3 || (len(match[2]) == 1 && math.Abs(value) < 100) {
			return 10, false
		}
		return 100, true
	case "repo":
		return 80, true
	default:
		return 10, false
	}
}

// Each current token contributes once using its best historical match. The
// clock is the matched observation, never the bucket's unrelated latest run.
func activityEvidenceScore(current, previous []ActivityEvidenceToken, common map[string]bool, now int64) (int, bool) {
	score := 0
	hasStrong := false
	seen := map[string]bool{}
	for _, token := range current {
		key := activityEvidenceKey(token)
		if seen[key] {
			continue
		}
		seen[key] = true
		best := 0
		referenceMs := max(now, token.LastSeenMs)
		for _, older := range previous {
			if !activityEvidenceMatches(token, older) || older.LastSeenMs > referenceMs {
				continue
			}
			weight, strong := activityEvidenceWeight(token, common)
			otherWeight, otherStrong := activityEvidenceWeight(older, common)
			weight = min(weight, otherWeight)
			strong = strong && otherStrong
			lease := activityWeakLinkMs
			if strong {
				lease = activityStrongLinkMs
			}
			gap := max(referenceMs-older.LastSeenMs, referenceMs-token.LastSeenMs)
			if gap > lease {
				continue
			}
			// Retain half the weight at lease expiry, then expire completely.
			decayed := weight - int(int64(weight)*gap/(2*lease))
			if decayed > best {
				best = decayed
			}
			hasStrong = hasStrong || strong
		}
		score += best
	}
	return score, hasStrong
}

func matchedActivityEvidence(current, previous []ActivityEvidenceToken, now int64, commonMaps ...map[string]bool) []ActivityEvidenceToken {
	var common map[string]bool
	if len(commonMaps) > 0 {
		common = commonMaps[0]
	}
	matched := []ActivityEvidenceToken{}
	for _, token := range current {
		if _, strong := activityEvidenceWeight(token, common); !strong {
			continue
		}
		if now-token.LastSeenMs > activityWeakLinkMs {
			continue
		}
		for _, older := range previous {
			if _, strong := activityEvidenceWeight(older, common); !strong {
				continue
			}
			if older.LastSeenMs <= now && max(now, token.LastSeenMs)-older.LastSeenMs <= activityWeakLinkMs && activityEvidenceMatches(token, older) {
				matched = append(matched, token)
				break
			}
		}
		if len(matched) >= 8 {
			break
		}
	}
	return matched
}

func activityAppFamily(app string) string {
	app = strings.ToLower(app)
	for _, family := range []struct {
		name string
		apps []string
	}{
		{"terminal", []string{"iterm", "wezterm", "terminal", "warp"}},
		{"browser", []string{"chrome", "safari", "firefox", "edge", "arc"}},
		{"editor", []string{"code", "cursor", "xcode", "zed"}},
	} {
		for _, name := range family.apps {
			if strings.Contains(app, name) {
				return family.name
			}
		}
	}
	return app
}

func activityEvidenceChatApp(key, name string) bool {
	for _, app := range []string{key, name} {
		app = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(app)), "app:")
		switch app {
		case "claude", "chatgpt", "codex", "copilot", "gemini", "grok", "perplexity", "yansu", "isoform",
			"slack", "wechat", "weixin", "discord", "telegram", "whatsapp", "messages", "signal", "teams", "microsoft teams",
			"com.anthropic.claudefordesktop", "com.anthropic.claude", "com.openai.chat", "com.openai.codex",
			"com.wails.yansu", "com.wails.isoform", "com.yansu",
			"com.tinyspeck.slackmacgap", "com.tencent.xinwechat", "com.hnc.discord", "ru.keepcoder.telegram", "org.telegram.desktop",
			"net.whatsapp.whatsapp", "com.apple.ichat", "org.whispersystems.signal-desktop", "com.microsoft.teams", "com.microsoft.teams2":
			return true
		}
	}
	return false
}

// Matching observations lead the bounded prompt, followed by the newest other
// evidence. Keep all tokens for scoring; only rendering imposes the text cap.
func orderActivityPromptEvidence(tokens, current []ActivityEvidenceToken) []ActivityEvidenceToken {
	byKey := map[string]ActivityEvidenceToken{}
	for _, token := range tokens {
		key := activityEvidenceKey(token)
		if older, ok := byKey[key]; !ok || token.LastSeenMs > older.LastSeenMs {
			byKey[key] = token
		}
	}
	ordered := make([]ActivityEvidenceToken, 0, len(byKey))
	matched := map[string]bool{}
	for _, token := range byKey {
		ordered = append(ordered, token)
		for _, incoming := range current {
			if activityEvidenceMatches(token, incoming) {
				matched[activityEvidenceKey(token)] = true
				break
			}
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := matched[activityEvidenceKey(ordered[i])], matched[activityEvidenceKey(ordered[j])]
		if a != b {
			return a
		}
		if ordered[i].LastSeenMs != ordered[j].LastSeenMs {
			return ordered[i].LastSeenMs > ordered[j].LastSeenMs
		}
		return activityEvidenceKey(ordered[i]) < activityEvidenceKey(ordered[j])
	})
	return ordered
}

func eligibleActivityPromptEvidence(tokens, current []ActivityEvidenceToken, common map[string]bool, now int64) []ActivityEvidenceToken {
	eligible := make([]ActivityEvidenceToken, 0, len(tokens))
	for _, token := range tokens {
		_, strong := activityEvidenceWeight(token, common)
		matched, strongMatch := false, false
		for _, incoming := range current {
			if activityEvidenceMatches(token, incoming) {
				matched = true
				_, incomingStrong := activityEvidenceWeight(incoming, common)
				strongMatch = strongMatch || incomingStrong
			}
		}
		strong = strong && (!matched || strongMatch)
		lease := activityWeakLinkMs
		if strong {
			lease = activityStrongLinkMs
		}
		if token.LastSeenMs <= now && now-token.LastSeenMs <= lease {
			eligible = append(eligible, token)
		}
	}
	return eligible
}
