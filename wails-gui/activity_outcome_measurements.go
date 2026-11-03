package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"wails-gui/internal/pii"
)

func migrateActivityOutcomeMeasurementCache(tx *sql.Tx) error {
	for _, column := range []struct{ table, name, definition string }{
		{"activity_run_evidence_state", "measurement_evidence", "TEXT NOT NULL DEFAULT '[]'"},
		{"activity_outcome_observations", "event_rewrite_fingerprint", "TEXT NOT NULL DEFAULT ''"},
		{"activity_outcome_observations", "event_row_id", "INTEGER NOT NULL DEFAULT 0"},
		{"activity_outcome_observations", "message_context_fingerprint", "TEXT NOT NULL DEFAULT ''"},
	} {
		if _, err := tx.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.definition); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS activity_event_rewrite_revisions (
		session_id TEXT PRIMARY KEY, revision INTEGER NOT NULL DEFAULT 0
	);
	CREATE TRIGGER IF NOT EXISTS activity_event_rewrite_UPDATE
	AFTER UPDATE OF session_id,timestamp,kind,data ON activity_events BEGIN
		INSERT INTO activity_event_rewrite_revisions(session_id,revision) VALUES(NEW.session_id,1)
		ON CONFLICT(session_id) DO UPDATE SET revision=revision+1;
		INSERT INTO activity_event_rewrite_revisions(session_id,revision)
		SELECT OLD.session_id,1 WHERE OLD.session_id != NEW.session_id
		ON CONFLICT(session_id) DO UPDATE SET revision=revision+1;
	END;
	CREATE TRIGGER IF NOT EXISTS activity_event_rewrite_DELETE
	AFTER DELETE ON activity_events BEGIN
		INSERT INTO activity_event_rewrite_revisions(session_id,revision) VALUES(OLD.session_id,1)
		ON CONFLICT(session_id) DO UPDATE SET revision=revision+1;
	END;`)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE activity_event_rewrite_revisions ADD COLUMN snapshot_revision INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	// A late or corrected browser URL can make an older Send identifiable.
	// Such context changes need the same replay as edits to an old event.
	for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
		row, clause, previous := "NEW", operation, ""
		switch operation {
		case "UPDATE":
			clause += " OF session_id,timestamp,app,browser_url,parsed_json"
			previous = `INSERT INTO activity_event_rewrite_revisions(session_id,snapshot_revision)
				SELECT OLD.session_id,1 WHERE OLD.session_id != NEW.session_id
				ON CONFLICT(session_id) DO UPDATE SET snapshot_revision=snapshot_revision+1;`
		case "DELETE":
			row = "OLD"
		}
		if _, err := tx.Exec(fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS activity_event_rewrite_snapshot_%s
			AFTER %s ON activity_snapshots BEGIN
			INSERT INTO activity_event_rewrite_revisions(session_id,snapshot_revision) VALUES(%s.session_id,1)
			ON CONFLICT(session_id) DO UPDATE SET snapshot_revision=snapshot_revision+1;
			%s END`, operation, clause, row, previous)); err != nil {
			return err
		}
	}
	return nil
}

// Only AX line-paired identifiers are cached. Neither OCR nor surrounding
// prose enters this cache, which has its own bounded identifier budget.
func activityOutcomeLineMeasurements(runID, sourceID, text string, at int64) []ActivityOutcomeEvidence {
	var out []ActivityOutcomeEvidence
	text, _ = pii.RedactText(text)
	for _, line := range strings.Split(text, "\n") {
		var paired []ActivityEvidenceToken
		paths, figures := 0, 0
		for _, token := range extractActivityIdentifierTokens(line, at) {
			switch token.Kind {
			case "path":
				paths++
				paired = append(paired, token)
			case "figure":
				figures++
				paired = append(paired, token)
			}
		}
		if paths == 1 && figures == 1 && activityOutcomeHasMeasurementTokens(paired, nil) {
			out = mergeActivityOutcomeMeasurements(out, []ActivityOutcomeEvidence{{RunID: runID, SourceKind: "activity_snapshot", SourceID: sourceID, ObservedAt: at, Tokens: paired}})
		}
	}
	return out
}

// Repeated captures of an unchanged line retain its first and last source.
// Keep the newest pairs within the same 64-entry budget as run tokens, with
// both boundary sources together when a pair spans different captures.
func mergeActivityOutcomeMeasurements(existing, incoming []ActivityOutcomeEvidence) []ActivityOutcomeEvidence {
	type boundaries struct {
		key         string
		first, last ActivityOutcomeEvidence
	}
	pairs := make(map[string]boundaries)
	for _, items := range [][]ActivityOutcomeEvidence{existing, incoming} {
		for _, item := range items {
			var path, figure string
			for _, token := range item.Tokens {
				switch token.Kind {
				case "path":
					path = activityEvidenceKey(token)
				case "figure":
					figure = activityEvidenceKey(token)
				}
			}
			if path == "" || figure == "" {
				continue
			}
			key := path + "\x00" + figure
			pair, exists := pairs[key]
			if !exists {
				pairs[key] = boundaries{key: key, first: item, last: item}
				continue
			}
			if item.ObservedAt < pair.first.ObservedAt {
				pair.first = item
			}
			if item.ObservedAt >= pair.last.ObservedAt {
				pair.last = item
			}
			pairs[key] = pair
		}
	}
	ordered := make([]boundaries, 0, len(pairs))
	for _, pair := range pairs {
		ordered = append(ordered, pair)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].last.ObservedAt != ordered[j].last.ObservedAt {
			return ordered[i].last.ObservedAt > ordered[j].last.ObservedAt
		}
		return ordered[i].key < ordered[j].key
	})
	out := make([]ActivityOutcomeEvidence, 0, min(len(existing)+len(incoming), activityEvidenceTokenLimit))
	for _, pair := range ordered {
		count := 1
		if pair.first.SourceID != pair.last.SourceID {
			count++
		}
		if len(out)+count > activityEvidenceTokenLimit {
			continue
		}
		out = append(out, pair.first)
		if count == 2 {
			out = append(out, pair.last)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ObservedAt < out[j].ObservedAt })
	return out
}

func activityOutcomeHasStrongPath(tokens []ActivityEvidenceToken, common map[string]bool) bool {
	for _, token := range tokens {
		if token.Kind != "path" {
			continue
		}
		if _, strong := activityEvidenceWeight(token, common); strong {
			return true
		}
	}
	return false
}

func (s *ActivityStore) activityOutcomeMeasurements(run ActivityTimelineRunRow) ([]activityOutcomeObservation, error) {
	allowed, err := s.timelineRunEvidenceAllowed(run)
	if err != nil || !allowed {
		return nil, err
	}
	fingerprint, err := s.timelineRunEvidenceFingerprint(run)
	if err != nil {
		return nil, err
	}
	var raw string
	err = s.reader().QueryRow(`SELECT measurement_evidence FROM activity_run_evidence_state WHERE run_id=? AND source_fingerprint=?`, run.ID, fingerprint).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var evidence []ActivityOutcomeEvidence
	if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
		return nil, fmt.Errorf("decode activity measurement evidence: %w", err)
	}
	out := make([]activityOutcomeObservation, 0, len(evidence))
	for _, item := range evidence {
		if item.RunID != run.ID || item.SourceKind != "activity_snapshot" || item.SourceID == "" {
			continue
		}
		item.Tokens = sanitizeActivityEvidenceTokens(item.Tokens, false)
		if len(item.Tokens) == 2 && activityOutcomeHasMeasurementTokens(item.Tokens, nil) {
			out = append(out, activityOutcomeObservation{evidence: item})
		}
	}
	return out, nil
}

// Appended rows are bounded by the prior observation time. Rewrites can change
// old drafts or sends, so their small per-session revisions force a replay.
func (s *ActivityStore) activityOutcomeEventRewriteFingerprint(run ActivityTimelineRunRow) (string, error) {
	var revisions []string
	browser := taskLifecycleBrowserDestination(strings.ToLower(run.AppKey), normalizeTaskWorkflowText(run.AppName))
	for _, sessionID := range run.SessionIDs {
		var revision, snapshotRevision int64
		if err := s.reader().QueryRow(`SELECT COALESCE((SELECT revision FROM activity_event_rewrite_revisions WHERE session_id=?),0),COALESCE((SELECT snapshot_revision FROM activity_event_rewrite_revisions WHERE session_id=?),0)`, sessionID, sessionID).Scan(&revision, &snapshotRevision); err != nil {
			return "", err
		}
		if !browser {
			snapshotRevision = 0
		}
		if revision > 0 || snapshotRevision > 0 {
			revisions = append(revisions, fmt.Sprintf("%s:%d:%d", sessionID, revision, snapshotRevision))
		}
	}
	sort.Strings(revisions)
	return deterministicTaskEpisodeID("activity-event-rewrites", strings.Join(revisions, "\x00")), nil
}

func (s *ActivityStore) activityOutcomeEventReadStart(run ActivityTimelineRunRow, observedUntil, observedRowID int64) (int64, error) {
	start := observedUntil
	for _, sessionID := range run.SessionIDs {
		var earliest sql.NullInt64
		if err := s.reader().QueryRow(`SELECT MIN(timestamp) FROM activity_events
			WHERE session_id=? AND id>? AND id>=? AND id<=?`, sessionID, observedRowID, run.FirstEventRowID, run.LastEventRowID).Scan(&earliest); err != nil {
			return 0, err
		}
		if earliest.Valid && earliest.Int64 < start {
			start = earliest.Int64
		}
	}
	// A send after the cutoff may commit a draft immediately before it.
	return max(run.StartMs, start-activityOutcomeCommitWindowMs), nil
}

func activityOutcomeMessageContextFingerprint(task []ActivityEvidenceToken, runIDs string, common map[string]bool) string {
	// Seeing an existing token again changes its timestamp, not the set of
	// messages that can match it. Only changed identifiers need a replay.
	keys := make([]string, 0, len(task))
	for _, token := range task {
		keys = append(keys, activityEvidenceKey(token))
	}
	sort.Strings(keys)
	// Damping elsewhere cannot affect this run's messages. Keep matching
	// figure aliases too: incoming 60000 MB can match a task's 60 GB token.
	var commonKeys []string
	for key, isCommon := range common {
		if !isCommon {
			continue
		}
		kind, value, ok := strings.Cut(key, ":")
		if !ok {
			continue
		}
		candidate := ActivityEvidenceToken{Kind: kind, Value: value}
		for _, token := range task {
			if activityEvidenceMatches(candidate, token) {
				commonKeys = append(commonKeys, key)
				break
			}
		}
	}
	sort.Strings(commonKeys)
	payload, _ := json.Marshal(struct {
		Task   []string
		RunIDs string
		Common []string
	}{keys, runIDs, commonKeys})
	return deterministicTaskEpisodeID("activity-message-context", string(payload))
}
