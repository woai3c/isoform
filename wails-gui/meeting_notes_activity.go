package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The Activity screen shows a meeting's notes with that meeting's activity.
// Every read passes the same gates as the meeting's transcript: Closed Eyes,
// a protected or deleted source, a sensitive app or a pause on any activity
// session of the call hides the notes, the transcript and the recording.

// MeetingNotesView is one meeting's notes as the Activity screen shows them.
type MeetingNotesView struct {
	MeetingKey string                 `json:"meetingKey"`
	MeetingApp string                 `json:"meetingApp"`
	Title      string                 `json:"title"`
	StartTime  int64                  `json:"startTime"`
	EndTime    int64                  `json:"endTime"`
	Summary    string                 `json:"summary"`
	Decisions  []string               `json:"decisions"`
	Followups  []MeetingNotesFollowup `json:"followups"`
	UserNotes  string                 `json:"userNotes"`
	Parts      []MeetingNotesPart     `json:"parts"`
}

// MeetingNotesFollowup is one thing someone has to do after the meeting.
// Owner is "me", "other" or "unknown".
type MeetingNotesFollowup struct {
	Text   string `json:"text"`
	Owner  string `json:"owner"`
	Person string `json:"person,omitempty"`
	Due    string `json:"due,omitempty"`
}

// MeetingNotesPart is one activity session of the meeting, with what the
// notes link to.
type MeetingNotesPart struct {
	SessionID     string `json:"sessionId"`
	StartTime     int64  `json:"startTime"`
	EndTime       int64  `json:"endTime"`
	WordCount     int    `json:"wordCount"`
	HasTranscript bool   `json:"hasTranscript"`
	HasRecording  bool   `json:"hasRecording"`
}

// meetingSessionReadable reports whether one slice of a meeting may be read
// now, with the transcript's gates. A slice whose raw capture retention
// removed is judged by its retained receipt, over the slice's own time span;
// an explicitly deleted one has a private receipt and is never readable.
// It returns the slice's current session row (nil when raw capture is gone).
func (a *App) meetingSessionReadable(store *ActivityStore, slice meetingNoteSlice, meetingApp string, policy activityReadPrivacyPolicy) (*ActivitySessionRow, bool) {
	if a == nil || store == nil || a.IsClosedEyesModeEnabled() {
		return nil, false
	}
	row, err := store.GetSessionByID(slice.SessionID)
	if err == nil && row != nil {
		allowed, err := a.activitySessionAllowsWithPolicy(store, *row, policy)
		if err != nil || !allowed {
			return nil, false
		}
		// The transcript's own dependencies: every activity session the
		// recording spans and the recording's capture provenance.
		if row.TranscriptPath != "" || row.AudioPath != "" {
			if _, err := a.transcriptSourcesWithPolicy(store, *row, "", policy); err != nil {
				return nil, false
			}
		}
		return row, true
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false
	}
	var apps []string
	if app := strings.TrimSpace(meetingApp); app != "" {
		apps = []string{app}
	}
	source, err := store.activityDependencyProvenance(slice.SessionID, apps)
	if err != nil {
		return nil, false
	}
	if source.StartTime == 0 && source.EndTime == 0 {
		// A legacy source without a receipt carries no time: pauses are
		// checked against the slice's own span.
		source.StartTime, source.EndTime = slice.StartTime, slice.EndTime
	}
	if !policy.allows(source) {
		return nil, false
	}
	return nil, true
}

// meetingNotesReadable checks every slice of a meeting: one protected slice
// hides the whole meeting, the way a protected source hides its transcript.
// It returns the current session rows of the slices (nil where raw capture
// is gone).
func (a *App) meetingNotesReadable(store *ActivityStore, r meetingNotesRecord, policy activityReadPrivacyPolicy) (map[string]*ActivitySessionRow, bool) {
	if len(r.Slices) == 0 {
		return nil, false
	}
	rows := make(map[string]*ActivitySessionRow, len(r.Slices))
	for _, sl := range r.Slices {
		row, ok := a.meetingSessionReadable(store, sl, r.MeetingApp, policy)
		if !ok {
			return nil, false
		}
		rows[sl.SessionID] = row
	}
	return rows, true
}

// readableMeetingSlice returns the current row of one slice when its whole
// meeting may be read now.
func (a *App) readableMeetingSlice(sessionID string) (*ActivitySessionRow, error) {
	if a.activity == nil || a.activity.store == nil {
		return nil, fmt.Errorf("activity store not initialized")
	}
	store := a.activity.store
	key, ok, err := store.meetingNotesKeyForSession(sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errMeetingNotesNotReadable
	}
	r, found, err := store.meetingNotesByKey(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errMeetingNotesNotReadable
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return nil, err
	}
	rows, readable := a.meetingNotesReadable(store, r, policy)
	if !readable || rows[sessionID] == nil {
		return nil, errMeetingNotesNotReadable
	}
	return rows[sessionID], nil
}

var errMeetingNotesNotReadable = errors.New("meeting notes are not readable")

// meetingNotesView renders a stored meeting for Activity.
func meetingNotesView(r meetingNotesRecord, rows map[string]*ActivitySessionRow) MeetingNotesView {
	view := MeetingNotesView{
		MeetingKey: r.Key,
		MeetingApp: r.MeetingApp,
		Title:      r.Title,
		StartTime:  r.StartTime,
		EndTime:    r.EndTime,
		Decisions:  []string{},
		Followups:  []MeetingNotesFollowup{},
		UserNotes:  r.UserNotes,
		Parts:      []MeetingNotesPart{},
	}
	var summaries []string
	seenDecision, seenFollowup := map[string]bool{}, map[string]bool{}
	for _, sl := range r.Slices {
		if s := strings.TrimSpace(sl.Summary); s != "" {
			summaries = append(summaries, s)
		}
		for _, d := range sl.Decisions {
			if key := strings.ToLower(d); d != "" && !seenDecision[key] {
				seenDecision[key] = true
				view.Decisions = append(view.Decisions, d)
			}
		}
		for _, f := range sl.Followups {
			if key := strings.ToLower(f.Text); f.Text != "" && !seenFollowup[key] {
				seenFollowup[key] = true
				view.Followups = append(view.Followups, MeetingNotesFollowup{Text: f.Text, Owner: f.Owner, Person: f.Person, Due: f.Due})
			}
		}
		part := MeetingNotesPart{SessionID: sl.SessionID, StartTime: sl.StartTime, EndTime: sl.EndTime, WordCount: sl.WordCount}
		if row := rows[sl.SessionID]; row != nil {
			part.HasTranscript = row.TranscriptPath != ""
			part.HasRecording = row.AudioPath != ""
		}
		view.Parts = append(view.Parts, part)
	}
	view.Summary = strings.Join(summaries, "\n\n")
	return view
}

// GetMeetingNotesForSessions returns the notes of every meeting that has an
// activity session among sessionIDs (the sessions behind the Activity row the
// user opened). Meetings that may not be read now are left out.
func (a *App) GetMeetingNotesForSessions(sessionIDs []string) ([]MeetingNotesView, error) {
	if a.activity == nil || a.activity.store == nil {
		return nil, fmt.Errorf("activity store not initialized")
	}
	store := a.activity.store
	records, err := store.meetingNotesForSessions(sessionIDs)
	if err != nil {
		return nil, err
	}
	out := []MeetingNotesView{}
	if len(records) == 0 {
		return out, nil
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		rows, ok := a.meetingNotesReadable(store, r, policy)
		if !ok {
			continue
		}
		out = append(out, meetingNotesView(r, rows))
	}
	return out, nil
}

// GetRecentMeetingNotes returns the readable notes of meetings that ended in
// the last days, newest first.
func (a *App) GetRecentMeetingNotes(days int) (perfValue []MeetingNotesView, perfErr error) {
	defer logPageBinding("GetRecentMeetingNotes", time.Now(), &perfValue)
	if a.activity == nil || a.activity.store == nil {
		return nil, fmt.Errorf("activity store not initialized")
	}
	if days <= 0 {
		days = 1
	}
	store := a.activity.store
	keys, err := store.recentMeetingNotes(time.Now().AddDate(0, 0, -days).UnixMilli())
	if err != nil {
		return nil, err
	}
	out := []MeetingNotesView{}
	if len(keys) == 0 {
		return out, nil
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return nil, err
	}
	for i := len(keys) - 1; i >= 0; i-- {
		r, ok, err := store.meetingNotesByKey(keys[i])
		if err != nil || !ok {
			continue
		}
		if rows, readable := a.meetingNotesReadable(store, r, policy); readable {
			out = append(out, meetingNotesView(r, rows))
		}
	}
	return out, nil
}

// meetingRecordingPath returns the recording of one slice of a meeting
// when the whole meeting may be read now.
func (a *App) meetingRecordingPath(sessionID string) (string, error) {
	row, err := a.readableMeetingSlice(sessionID)
	if err != nil {
		return "", err
	}
	if row.AudioPath == "" || !filepath.IsLocal(row.AudioPath) {
		return "", errMeetingNotesNotReadable
	}
	path := filepath.Join(a.activity.store.AudioDir(), row.AudioPath)
	if _, err := os.Stat(path); err != nil {
		return "", errMeetingNotesNotReadable
	}
	return path, nil
}

// GetMeetingNotesTranscript returns the full transcript of one part of a
// meeting when the whole meeting may be read now.
func (a *App) GetMeetingNotesTranscript(sessionID string) (string, error) {
	row, err := a.readableMeetingSlice(sessionID)
	if err != nil {
		return "", err
	}
	if row.TranscriptPath == "" {
		return "", errMeetingNotesNotReadable
	}
	text, _, err := a.readMeetingTranscript(*row)
	if err != nil {
		return "", errMeetingNotesNotReadable
	}
	return text, nil
}

// handleActivityMeetingAudio serves a call recording to the Activity
// screen's player. GET /api/activity/audio?session=<activity session id>
func (a *App) handleActivityMeetingAudio(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionID == "" {
		a.bridge.writeError(w, http.StatusBadRequest, "session parameter required")
		return
	}
	path, err := a.meetingRecordingPath(sessionID)
	if err != nil {
		a.bridge.writeError(w, http.StatusNotFound, "recording not found")
		return
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case meetingAudioOpusExt:
		w.Header().Set("Content-Type", "audio/ogg; codecs=opus")
	case ".wav":
		w.Header().Set("Content-Type", "audio/wav")
	}
	// Privacy can change between plays: never cache.
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

// RevealMeetingRecording shows one activity session's call recording in the
// computer's file manager.
func (a *App) RevealMeetingRecording(sessionID string) error {
	path, err := a.meetingRecordingPath(sessionID)
	if err != nil {
		return err
	}
	return revealInFileManager(path)
}
