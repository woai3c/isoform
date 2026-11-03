package main

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// activitySessionsForExternalRead applies the model privacy gate before
// pagination. A protected row must not become a result or displace a safe row.
func (a *App) activitySessionsForExternalRead(limit, offset int, startMs, endMs int64, keyword string) ([]ActivitySessionSummary, error) {
	result := []ActivitySessionSummary{}
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() {
		return result, nil
	}
	store := a.activity.store
	const batchSize = 100
	for cursor, visible := 0, 0; ; cursor += batchSize {
		rows, err := store.GetFilteredSessions(batchSize, cursor, startMs, endMs, keyword)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if !a.activitySessionAllowsModelRead(store, row) {
				continue
			}
			visible++
			if visible <= offset {
				continue
			}
			result = append(result, activitySessionExternalSummary(row))
			if len(result) >= limit {
				return result, nil
			}
		}
		if len(rows) < batchSize {
			return result, nil
		}
	}
}

func (a *App) activitySessionForExternalRead(id string) (*ActivitySessionDetail, error) {
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() {
		return nil, sql.ErrNoRows
	}
	store := a.activity.store
	session, err := store.GetSessionByID(id)
	if err != nil {
		return nil, err
	}
	if !a.activitySessionAllowsModelRead(store, *session) {
		return nil, sql.ErrNoRows
	}
	events, err := store.GetSessionEvents(id)
	if err != nil {
		return nil, err
	}
	segments, err := store.GetSessionSegments(id)
	if err != nil {
		return nil, err
	}
	snapshots, err := store.GetSessionSnapshots(id)
	if err != nil {
		return nil, err
	}
	detail := &ActivitySessionDetail{
		ActivitySessionSummary: activitySessionExternalSummary(*session),
		Events:                 events,
		Segments:               segments,
		Snapshots:              snapshots,
	}
	// Individual key events can split a credential across many fields. The
	// CLI does not need raw keystrokes; leave originals in the local UI.
	for i := range detail.Events {
		detail.Events[i].KeyChar = ""
		detail.Events[i].KeyChars = nil
		detail.Events[i].KeyCode = 0
		detail.Events[i].TypedText = ""
	}
	// Traverse the serialized payload so speech, clipboard previews, AX/OCR,
	// URLs and future string fields all cross the same redaction boundary.
	raw, err := json.Marshal(detail)
	if err != nil {
		return nil, err
	}
	var payload any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	raw, err = json.Marshal(redactActivityExternalPayload(payload))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, detail); err != nil {
		return nil, err
	}
	return detail, nil
}

func redactActivityExternalPayload(value any) any {
	switch v := value.(type) {
	case string:
		// ParsedJSON is an encoded object inside the snapshot envelope.
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var nested any
			decoder := json.NewDecoder(strings.NewReader(v))
			decoder.UseNumber()
			if decoder.Decode(&nested) == nil {
				if raw, err := json.Marshal(redactActivityExternalPayload(nested)); err == nil {
					return string(raw)
				}
			}
		}
		return redactActivityModelText(v)
	case []any:
		for i := range v {
			v[i] = redactActivityExternalPayload(v[i])
		}
		return v
	case map[string]any:
		clean := make(map[string]any, len(v))
		for key, item := range v {
			clean[redactActivityModelText(key)] = redactActivityExternalPayload(item)
		}
		return clean
	default:
		return value
	}
}

func activitySessionExternalSummary(session ActivitySessionRow) ActivitySessionSummary {
	var apps []string
	_ = json.Unmarshal([]byte(session.AppNames), &apps)
	for i := range apps {
		apps[i] = redactActivityModelText(apps[i])
	}
	return ActivitySessionSummary{
		ID: session.ID, StartTime: session.StartTime, EndTime: session.EndTime,
		TriggerKind: session.TriggerKind, AppNames: apps,
		EventCount: session.EventCount, SnapshotCount: session.SnapshotCount,
		TriageStatus: session.TriageStatus, OCRStatus: session.OCRStatus,
	}
}
