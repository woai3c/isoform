package main

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
)

type activityThreadOutcomeSnapshot struct {
	id                     int64
	session                string
	at                     int64
	text, app, parsed, ocr string
}

type activityThreadOutcomeEvent struct {
	id            int64
	session, data string
}

type activityThreadDayOutcomes struct {
	evidence  *activityThreadDayEvidence
	snapshots map[string][]activityThreadOutcomeSnapshot
	events    map[string][]activityThreadOutcomeEvent
	subjects  []string
}

// An open pause is the current observation state even if its timestamp is
// ahead of this clock. Never keep a derived outcome visible during that state.
func activityThreadOutcomesPaused(pauses []observationPause, now time.Time) bool {
	for _, pause := range pauses {
		if pause.End == 0 {
			return true
		}
	}
	return pausedAt(pauses, now)
}

// Reconstruct all recorded outcome tails with two day-wide queries. The bounds,
// exact OCR match, six-row caps and final evidence ordering match the ordinary
// outcome reader. Capture text is loaded only for runs with a recorded outcome.
func (a *App) activityThreadDayOutcomes(ctx context.Context, rows []ActivityRow, evidence *activityThreadDayEvidence) *activityThreadDayOutcomes {
	hasOutcome := false
	for _, row := range rows {
		hasOutcome = hasOutcome || row.threadOutcomeRecord != ""
	}
	if !hasOutcome || a.IsClosedEyesModeEnabled() {
		return nil
	}
	pauses, err := a.observationPausesContext(ctx)
	if err != nil || activityThreadOutcomesPaused(pauses, time.Now()) {
		return nil
	}
	subjects, err := mobileBriefMailSubjects(a)
	if err != nil {
		return nil
	}
	result := &activityThreadDayOutcomes{evidence: evidence, snapshots: map[string][]activityThreadOutcomeSnapshot{}, events: map[string][]activityThreadOutcomeEvent{}, subjects: subjects}
	ids := map[string]bool{}
	for _, row := range rows {
		if result.rowAllowed(row) {
			for _, id := range composeParseStrings(row.RunIDs) {
				ids[id] = true
			}
		}
	}
	if len(ids) == 0 {
		return result
	}
	type envelope struct {
		ID       string   `json:"id"`
		Sessions []string `json:"sessions"`
		Start    int64    `json:"start"`
		End      int64    `json:"end"`
	}
	runs := make([]envelope, 0, len(ids))
	for id := range ids {
		run := evidence.runs[id]
		runs = append(runs, envelope{run.ID, run.SessionIDs, run.StartMs, run.EndMs})
	}
	// Select tails from the captured authorized envelopes, never a later live
	// run whose bounds or sessions may have changed after the privacy read.
	const capturedRuns = `WITH runs AS (SELECT json_extract(value,'$.id') AS id,
        json_extract(value,'$.sessions') AS session_ids,json_extract(value,'$.start') AS start_ms,
        json_extract(value,'$.end') AS end_ms FROM json_each(?)), candidates AS (`
	frames, err := a.activity.store.reader().QueryContext(ctx, capturedRuns+`
        SELECT g.id AS run_id,s.id,s.session_id,s.timestamp,COALESCE(s.ax_text,'') AS text,COALESCE(s.app,'') AS app,COALESCE(s.parsed_json,'') AS parsed,
            ROW_NUMBER() OVER (PARTITION BY g.id ORDER BY s.timestamp DESC,s.id DESC) AS rank
        FROM runs g JOIN json_each(g.session_ids) j JOIN activity_snapshots s ON s.session_id=j.value
        WHERE s.timestamp BETWEEN g.start_ms AND g.end_ms
    ) SELECT run_id,id,session_id,timestamp,text,app,parsed,COALESCE((SELECT text FROM activity_ocr_frames o
        WHERE o.session_id=c.session_id AND o.segment_id=c.id AND TRIM(o.text) != '' ORDER BY o.id DESC LIMIT 1),'')
      FROM candidates c WHERE rank<=6 ORDER BY run_id,timestamp DESC,id DESC`, composeJSON(runs))
	if err != nil {
		return nil
	}
	for frames.Next() {
		var id string
		var item activityThreadOutcomeSnapshot
		if frames.Scan(&id, &item.id, &item.session, &item.at, &item.text, &item.app, &item.parsed, &item.ocr) != nil {
			frames.Close()
			return nil
		}
		result.snapshots[id] = append(result.snapshots[id], item)
	}
	if firstErr(frames.Err(), frames.Close()) != nil {
		return nil
	}
	events, err := a.activity.store.reader().QueryContext(ctx, capturedRuns+`
        SELECT g.id AS run_id,e.id,e.session_id,e.data,
            ROW_NUMBER() OVER (PARTITION BY g.id ORDER BY json_extract(e.data,'$.timestamp') DESC,e.sort_order DESC) AS rank
        FROM runs g JOIN json_each(g.session_ids) j JOIN activity_events e ON e.session_id=j.value
        WHERE json_extract(e.data,'$.timestamp') BETWEEN g.start_ms AND g.end_ms
          AND COALESCE(json_extract(e.data,'$.typedText'),'') != ''
    ) SELECT run_id,id,session_id,data FROM candidates WHERE rank<=6 ORDER BY run_id,rank`, composeJSON(runs))
	if err != nil {
		return nil
	}
	for events.Next() {
		var id string
		var item activityThreadOutcomeEvent
		if events.Scan(&id, &item.id, &item.session, &item.data) != nil {
			events.Close()
			return nil
		}
		result.events[id] = append(result.events[id], item)
	}
	if firstErr(events.Err(), events.Close()) != nil {
		return nil
	}
	return result
}

func (d *activityThreadDayOutcomes) rowAllowed(row ActivityRow) bool {
	if d == nil || row.threadOutcomeRecord == "" || activitymon.IsPrivateBrowsing(row.Title) || composerBucketContainsSensitiveApp(row) {
		return false
	}
	ids := composeParseStrings(row.RunIDs)
	if len(ids) == 0 || len(ids) > 64 {
		return false
	}
	apps, _ := topicAppTime(row.ActiveMsByApp)
	if len(apps) == 0 || topicAnySensitive(apps) || topicAnyPeopleApp(apps) || mobileBriefValueEchoesMail([]string{row.Title, row.Description}, d.subjects) {
		return false
	}
	for _, id := range ids {
		run, ok := d.evidence.runs[id]
		if !ok || !d.evidence.allowed[id] || len(run.SessionIDs) > 128 || threadOutcomeHeldApps([]string{run.AppName, run.AppKey}) {
			return false
		}
		for _, session := range run.SessionIDs {
			source, ok := d.evidence.sources[session]
			if !ok || threadOutcomeHeldApps(source.Apps) {
				return false
			}
			if source.MCPSnapshots != nil {
				for _, snapshot := range *source.MCPSnapshots {
					if threadOutcomeHeldApps(snapshot.Apps) {
						return false
					}
					for _, host := range snapshot.Hosts {
						if topicPeopleHost("https://" + host) {
							return false
						}
					}
				}
			}
		}
	}
	return true
}

func (d *activityThreadDayOutcomes) readable(row ActivityRow) *ActivityThreadOutcome {
	if !d.rowAllowed(row) {
		return nil
	}
	var evidence []threadOutcomeEvidence
	var runs []ActivityTimelineRunRow
	for _, id := range composeParseStrings(row.RunIDs) {
		runs = append(runs, d.evidence.runs[id])
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].EndMs > runs[j].EndMs })
	for _, run := range runs {
		id := run.ID
		for _, frame := range d.snapshots[id] {
			if !timelineGroupSnapshotMatches(ActivityTimelineGroup{AppKey: run.AppKey, AppName: run.AppName, StartTime: run.StartMs, EndTime: run.EndMs}, ActivitySnapshotRow{Timestamp: frame.at, App: frame.app, ParsedJSON: frame.parsed}) {
				continue
			}
			text := frame.text
			if frame.ocr != "" {
				text = strings.TrimSpace(frame.ocr)
			}
			if mobileBriefTextEchoesMail(text, d.subjects) {
				return nil
			}
			text = threadOutcomeObservationText(text)
			if text == "" {
				text = threadOutcomeNoText
			}
			evidence = append(evidence, threadOutcomeEvidence{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{
				RunID: id, SessionID: frame.session, SourceKind: "activity_snapshot", SourceID: strconv.FormatInt(frame.id, 10), ObservedAt: frame.at}, Text: text})
		}
		for _, item := range d.events[id] {
			var event activitymon.ActivityEvent
			if json.Unmarshal([]byte(item.data), &event) != nil || event.PrivateWindow || activitySourceAppIsSensitive(event.AppBundleID, event.AppName) || strings.Contains(strings.ToLower(event.FocusRole), "secure") || mobileBriefTextEchoesMail(event.TypedText, d.subjects) {
				return nil
			}
			if text := threadOutcomeObservationText(event.TypedText); text != "" {
				evidence = append(evidence, threadOutcomeEvidence{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{
					RunID: id, SessionID: item.session, SourceKind: "activity_event", SourceID: strconv.FormatInt(item.id, 10), ObservedAt: event.Timestamp}, Text: text})
			}
		}
	}
	evidence = finishThreadOutcomeEvidence(evidence)
	return readableThreadOutcomeEvidence(row, evidence, true, d.subjects)
}
