package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/speakerid"
)

// Invite order is never speaker order. With multiple people, only a strong
// voice match supplies a name. A sole non-declined invitee may name a sole
// remote voice; contacts supply the display name by an exact, unique email.
func callInviteName(event *calendar.Event, contacts []systemContact, remoteSpeakers int) string {
	if event == nil || remoteSpeakers != 1 {
		return ""
	}
	var others []calendar.Attendee
	for _, att := range event.Attendees {
		if !att.IsMe && att.Status != calendar.StatusDeclined {
			others = append(others, att)
		}
	}
	if len(others) != 1 {
		return ""
	}
	att := others[0]
	name := strings.TrimSpace(att.Name)
	if att.Email != "" {
		var matches []string
		for _, c := range contacts {
			for _, email := range c.Emails {
				if calendar.NormalizeEmail(email) == calendar.NormalizeEmail(att.Email) {
					matches = append(matches, contactDisplayName(c))
					break
				}
			}
		}
		if len(matches) == 1 && matches[0] != "" {
			name = matches[0]
		}
	}
	return name
}

func (a *App) callContacts() []systemContact {
	im, err := a.newContactsImporter()
	if err != nil || !hasCurrentContactsConsent(im.consentPath) {
		return nil
	}
	cards, err := im.store.ListImportedContactCards(context.Background())
	if err != nil {
		return nil
	}
	var out []systemContact
	for _, card := range cards {
		var c systemContact
		if json.Unmarshal([]byte(card.Payload), &c) == nil {
			out = append(out, c)
		}
	}
	return out
}

func matchCallVoice(probe []float32, refs []callVoiceSignature) (callVoiceSignature, bool) {
	// Multiple samples of the same name are evidence for one candidate,
	// rather than artificial competing runner-ups.
	bestByName := map[string]float64{}
	refByName := map[string]callVoiceSignature{}
	for _, ref := range refs {
		score := speakerid.CosineSimilarity(probe, ref.Embedding)
		if score > bestByName[ref.Name] {
			bestByName[ref.Name] = score
			refByName[ref.Name] = ref
		}
	}
	best, second := 0.0, 0.0
	name := ""
	for n, score := range bestByName {
		if score > best {
			second = best
			best = score
			name = n
		} else if score > second {
			second = score
		}
	}
	return refByName[name], best >= 0.85 && best-second >= 0.10
}

func (a *App) readableCallSignatures() []callVoiceSignature {
	store := a.activity.store
	refs, err := store.callSignatures()
	if err != nil {
		return nil
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return nil
	}
	var out []callVoiceSignature
	for _, ref := range refs {
		r, ok, err := store.meetingNotesByKey(ref.Key)
		if err != nil || !ok {
			continue
		}
		if _, ok := a.meetingNotesReadable(store, r, policy); ok {
			out = append(out, ref)
		}
	}
	return out
}

func buildCallTurns(utterances []meetingUtterance, sessionID string) []MeetingCallTurn {
	sorted := append([]meetingUtterance(nil), utterances...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	out := []MeetingCallTurn{}
	for _, u := range sorted {
		text := strings.TrimSpace(u.Text)
		if text == "" {
			continue
		}
		id := u.SpeakerID
		if u.Source == "microphone" {
			id = "you"
		} else if id == "" {
			id = "unknown"
		}
		if id != "you" {
			id = sessionID + ":" + id
		}
		if u.Source != "microphone" && u.CallSpeakerID != "" {
			id = u.CallSpeakerID
		}
		turn := MeetingCallTurn{SpeakerID: id, Source: u.Source, StartTime: u.At.UnixMilli(), EndTime: max(u.At.UnixMilli(), u.End.UnixMilli()), Text: text, LegacyText: u.LegacyText}
		if len(out) > 0 {
			last := &out[len(out)-1]
			// Real pauses, a voice change and overlapping speech all keep
			// separate turns; short adjacent ASR fragments may be joined.
			if last.SpeakerID == id && last.LegacyText == turn.LegacyText && turn.StartTime >= last.EndTime && turn.StartTime-last.EndTime < 700 && turn.EndTime-last.StartTime < 30000 {
				last.EndTime = turn.EndTime
				last.Text = joinTranscriptText(last.Text, text)
				continue
			}
		}
		out = append(out, turn)
	}
	return out
}

// Persistence receives text and call-local speaker ids only. Candidate
// embeddings expire with this function; explicit Rename extracts a fresh
// clean sample from retained audio before saving a named reference.
func (a *App) saveMeetingCallTurns(key string, row ActivitySessionRow, utterances []meetingUtterance, event *calendar.Event, automaticEpoch *uint64) error {
	if !a.meetingTranscriptAllowed(row, automaticEpoch) {
		return errMeetingNotesNotReadable
	}
	store := a.activity.store
	// Rejoined recordings share acoustic identities without persisting
	// unnamed embeddings: extract references from their retained audio.
	utterances = append([]meetingUtterance(nil), utterances...)
	if old, ok, err := store.meetingNotesByKey(key); err == nil && ok {
		seen := map[string]bool{}
		var localRefs []callVoiceSignature
		for _, sl := range old.Slices {
			if sl.SessionID == row.ID {
				continue
			}
			prior, _ := store.callTurns(sl.SessionID)
			for _, t := range prior {
				if t.SpeakerID == "you" || seen[t.SpeakerID] {
					continue
				}
				seen[t.SpeakerID] = true
				if emb := a.callSpeakerSignature(old, t.SpeakerID); len(emb) > 0 {
					localRefs = append(localRefs, callVoiceSignature{Key: key, Speaker: t.SpeakerID, Name: t.SpeakerID, Embedding: emb})
				}
			}
		}
		alignCallSpeakerIdentities(utterances, localRefs)
	}
	turns := buildCallTurns(utterances, row.ID)
	refs := a.readableCallSignatures()
	remote := map[string]bool{}
	for _, t := range turns {
		if t.SpeakerID != "you" {
			remote[t.SpeakerID] = true
		}
	}
	inviteName := callInviteName(event, a.callContacts(), len(remote))
	names := map[string]callVoiceSignature{}
	embeddings := map[string][][]float32{}
	for _, u := range utterances {
		if u.Source != "system_audio" || len(u.Embedding) == 0 {
			continue
		}
		id := row.ID + ":" + u.SpeakerID
		if u.CallSpeakerID != "" {
			id = u.CallSpeakerID
		}
		embeddings[id] = append(embeddings[id], u.Embedding)
	}
	for id, embs := range embeddings {
		probe, err := speakerid.AverageEmbeddings(embs)
		if err == nil {
			if ref, ok := matchCallVoice(probe, refs); ok {
				names[id] = ref
			}
		}
	}
	for i := range turns {
		if turns[i].SpeakerID != "you" {
			ref := names[turns[i].SpeakerID]
			turns[i].Name = ref.Name
			if ref.Name != "" {
				turns[i].NameSource = "voice:" + ref.Key
			}
			if turns[i].Name == "" && !callTurnUnidentified(turns[i].SpeakerID) {
				turns[i].Name = inviteName
				turns[i].NameSource = "invite"
			}
		}
	}
	// Recheck the entire call too, including earlier rejoined parts.
	r, ok, err := store.meetingNotesByKey(key)
	if err != nil {
		return err
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return err
	}
	if _, readable := a.meetingNotesReadable(store, r, policy); !ok || !readable {
		return errMeetingNotesNotReadable
	}
	if automaticEpoch != nil {
		a.meetingRecordingMu.Lock()
		defer a.meetingRecordingMu.Unlock()
	}
	if !a.meetingTranscriptAllowed(row, automaticEpoch) {
		return errMeetingNotesNotReadable
	}
	if err := store.saveCallTurns(key, row.ID, turns); err != nil {
		return err
	}
	policy, err = a.activityReadPrivacyPolicy()
	if err != nil {
		return err
	}
	if _, readable := a.meetingNotesReadable(store, r, policy); !readable {
		_, _ = store.db.Exec(`DELETE FROM meeting_call_turns WHERE meeting_key=?`, key)
		return errMeetingNotesNotReadable
	}
	return nil
}

// GetMeetingCallTurns is a window-only binding. Existing recordings are rebuilt
// on demand with installed models; the old transcript remains readable when
// a model/engine or the recording has gone. No network setup runs here.
func (a *App) GetMeetingCallTurns(sessionID string) (MeetingCallTurnsView, error) {
	return a.meetingCallTurns(sessionID, a.rebuildCallTurnsFromRecording)
}

// rebuildCallTurnsFromRecording upgrades a call's legacy text turns from its
// recording. A recording whose rebuild failed is not decoded again until the
// file changes or a day passes (or, for one without a sidecar, until Isoform
// starts again), so a legacy call whose audio cannot be decoded is not
// retried — and its failure not logged again — on every load.
func (a *App) rebuildCallTurnsFromRecording(row ActivitySessionRow) []meetingUtterance {
	if row.AudioPath == "" || a.speechTranscriber() == nil {
		return nil
	}
	if _, err := newCallModelsReady(); err != nil {
		return nil
	}
	return a.decodeCallAudioOncePerFile(row, a.meetingAudioUtterances)
}

// decodeCallAudioOncePerFile runs the turns view's decode attempt, recording
// a failure on the recording's sidecar with the file's size and modification
// time. The same unchanged recording is not decoded again until a day
// passes; the attempt itself logs the failure, once per attempt.
func (a *App) decodeCallAudioOncePerFile(row ActivitySessionRow, attempt func(ActivitySessionRow) ([]meetingUtterance, bool)) []meetingUtterance {
	if a.activity == nil || a.activity.store == nil {
		return nil
	}
	audioDir := a.activity.store.AudioDir()
	if meetingAudioDecodeFailedRecently(audioDir, row.AudioPath, time.Now()) {
		return nil
	}
	utterances, ok := attempt(row)
	if !ok {
		noteMeetingAudioDecodeFailed(audioDir, row.AudioPath, time.Now())
	} else {
		clearMeetingAudioDecodeFailed(audioDir, row.AudioPath)
	}
	return utterances
}

// Keep the audio boundary separate from the read/upgrade policy. Unassigned
// speakers are a valid completed result, never evidence of legacy text.
func (a *App) meetingCallTurns(sessionID string, rebuild func(ActivitySessionRow) []meetingUtterance) (view MeetingCallTurnsView, err error) {
	view = MeetingCallTurnsView{Turns: []MeetingCallTurn{}, Status: "ready"}
	defer func() {
		if err != nil {
			view = MeetingCallTurnsView{Turns: []MeetingCallTurn{}, Status: "unavailable"}
		}
	}()
	unlock := memoryFileLocks.Acquire("meeting-call-turns:" + sessionID)
	defer unlock()
	row, err := a.readableMeetingSlice(sessionID)
	if err != nil {
		return view, err
	}
	store := a.activity.store
	view.Turns, err = store.callTurns(sessionID)
	if err != nil {
		return view, err
	}
	needsUpgrade := false
	for _, turn := range view.Turns {
		if turn.LegacyText {
			needsUpgrade = true
			break
		}
	}
	if len(view.Turns) == 0 || needsUpgrade {
		key, _, err := store.meetingNotesKeyForSession(sessionID)
		if err != nil {
			return view, err
		}
		utterances := rebuild(*row)
		// Keep an existing legacy view when models/audio are unavailable. A
		// read must not repeatedly rewrite the same fallback rows either.
		if len(utterances) > 0 || len(view.Turns) == 0 {
			if len(utterances) == 0 {
				text, err := a.GetMeetingNotesTranscript(sessionID)
				if err != nil {
					return view, err
				}
				utterances = legacyCallUtterances(text, *row)
				view.Status = "unavailable"
			}
			if err := a.saveMeetingCallTurns(key, *row, utterances, a.calendarEventForSession(*row, row.MeetingApp), nil); err != nil {
				return view, err
			}
			view.Turns, err = store.callTurns(sessionID)
			if err != nil {
				return view, err
			}
		}
	}
	// Stable numbering is across the entire call, starting after You.
	key, _, err := store.meetingNotesKeyForSession(sessionID)
	if err != nil {
		return view, err
	}
	rows, err := store.db.Query(`SELECT speaker_id FROM meeting_call_turns WHERE meeting_key=? AND speaker_id<>'you' GROUP BY speaker_id ORDER BY MIN(start_time),speaker_id`, key)
	if err != nil {
		return view, err
	}
	numbers := map[string]int{}
	n := 2
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return view, err
		}
		numbers[id] = n
		n++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return view, err
	}
	for i := range view.Turns {
		t := &view.Turns[i]
		t.Number = numbers[t.SpeakerID]
		if t.LegacyText || callTurnUnidentified(t.SpeakerID) {
			view.Status = "unavailable"
		}
		if strings.HasPrefix(t.NameSource, "voice:") {
			r, ok, err := store.meetingNotesByKey(strings.TrimPrefix(t.NameSource, "voice:"))
			policy, policyErr := a.activityReadPrivacyPolicy()
			if err != nil || policyErr != nil || !ok {
				t.Name = ""
			} else if _, ok := a.meetingNotesReadable(store, r, policy); !ok {
				t.Name = ""
			}
		}
	}
	if _, err := a.readableMeetingSlice(sessionID); err != nil {
		return MeetingCallTurnsView{Turns: []MeetingCallTurn{}}, err
	}
	return view, nil
}

func callTurnUnidentified(id string) bool {
	return strings.HasSuffix(id, ":unknown") || strings.Contains(id, ":unidentified-")
}

// A clean long turn identifies the entire detector cluster, including its
// short replies which cannot independently provide a reliable embedding.
func alignCallSpeakerIdentities(utterances []meetingUtterance, refs []callVoiceSignature) {
	bySpeaker := map[string][][]float32{}
	for _, u := range utterances {
		if u.Source == "system_audio" && len(u.Embedding) > 0 {
			bySpeaker[u.SpeakerID] = append(bySpeaker[u.SpeakerID], u.Embedding)
		}
	}
	aliases := map[string]string{}
	for id, embs := range bySpeaker {
		probe, err := speakerid.AverageEmbeddings(embs)
		if err == nil {
			if ref, ok := matchCallVoice(probe, refs); ok {
				aliases[id] = ref.Speaker
			}
		}
	}
	for i, u := range utterances {
		if u.Source == "system_audio" {
			utterances[i].CallSpeakerID = aliases[u.SpeakerID]
		}
	}
}

func newCallModelsReady() (sherpaPaths, error) {
	p := callSpeakerModelPaths()
	if !verifiedCallModel(p.segmentation, sha256SpeakerSegmentation) || !verifiedCallModel(p.speakerEmbd, sha256SpeakerEmbedding) {
		return p, errCallModelsUnavailable
	}
	return p, nil
}

var legacyCallLine = regexp.MustCompile(`^\[(\d\d:\d\d:\d\d)\]\s*(.*)$`)

func legacyCallUtterances(text string, row ActivitySessionRow) []meetingUtterance {
	var out []meetingUtterance
	day := time.UnixMilli(row.StartTime).In(time.Local)
	body := strings.SplitN(text, "## Transcript", 2)
	if len(body) == 2 {
		text = body[1]
	}
	for _, line := range strings.Split(text, "\n") {
		m := legacyCallLine.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) == 0 {
			continue
		}
		clock, err := time.Parse("15:04:05", m[1])
		if err != nil {
			continue
		}
		at := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), clock.Second(), 0, day.Location())
		if len(out) > 0 && at.Before(out[len(out)-1].At.Add(-12*time.Hour)) {
			day = day.AddDate(0, 0, 1)
			at = at.AddDate(0, 0, 1)
		}
		source := ""
		words := m[2]
		for _, label := range []string{"Me: ", "You: ", "我: "} {
			if strings.HasPrefix(words, label) {
				source = "microphone"
				words = strings.TrimPrefix(words, label)
				break
			}
		}
		for _, label := range []string{"Other: ", "Them: ", "对方: "} {
			if strings.HasPrefix(words, label) {
				source = "system_audio"
				words = strings.TrimPrefix(words, label)
				break
			}
		}
		out = append(out, meetingUtterance{At: at, End: at, Source: source, Text: words, LegacyText: true})
	}
	return out
}

// Only clean, non-overlapping system speech from this exact call can teach a
// signature. Too-short/unavailable audio still allows a call-wide text rename.
func (a *App) callSpeakerSignature(record meetingNotesRecord, id string) []float32 {
	p, err := newCallModelsReady()
	if err != nil {
		return nil
	}
	ext, err := speakerid.New(speakerid.Config{Model: p.speakerEmbd, NumThreads: 1})
	if err != nil {
		return nil
	}
	defer ext.Close()
	for _, sl := range record.Slices {
		row, err := a.readableMeetingSlice(sl.SessionID)
		if err != nil || row.AudioPath == "" {
			continue
		}
		turns, err := a.activity.store.callTurns(sl.SessionID)
		if err != nil {
			continue
		}
		path, cleanup, err := decodeMeetingAudioToWAV(installedFFmpegPath(), filepath.Join(a.activity.store.AudioDir(), row.AudioPath))
		if err != nil {
			continue
		}
		monoSource := readMeetingWAVMeta(a.activity.store.AudioDir(), row.AudioPath).MonoSource
		emb := extractCallSpeakerSignature(path, turns, id, ext.ExtractFromPCM, monoSource)
		cleanup()
		if len(emb) > 0 {
			return emb
		}
	}
	return nil
}

func extractCallSpeakerSignature(path string, turns []MeetingCallTurn, id string, extract func([]float32, int) ([]float32, error), monoSource ...string) []float32 {
	start, ok := meetingAudioStartFromPath(path)
	if !ok {
		return nil
	}
	info, err := readMeetingWAVInfo(path)
	if err != nil {
		return nil
	}
	channel := 1
	if info.channels == 1 {
		if len(monoSource) == 0 || monoSource[0] != "system_audio" {
			return nil
		}
		channel = 0
	}
	for _, turn := range turns {
		if turn.SpeakerID != id || turn.Source != "system_audio" || turn.EndTime-turn.StartTime < 1500 {
			continue
		}
		clean := true
		for _, other := range turns {
			if other.Source == "system_audio" && other.SpeakerID != id && other.StartTime < turn.EndTime && other.EndTime > turn.StartTime {
				clean = false
			}
		}
		if !clean {
			continue
		}
		reader, err := openMeetingWAVChannel(path, info, channel)
		if err != nil {
			continue
		}
		from := max(int64(0), (turn.StartTime-start.UnixMilli())*16)
		to := min((turn.EndTime-start.UnixMilli())*16, from+160000)
		var samples []float32
		pos := int64(0)
		for pos < to {
			block, err := reader.ReadSamples()
			if len(block) > 0 {
				lo, hi := max(int64(0), from-pos), min(int64(len(block)), to-pos)
				if hi > lo {
					samples = append(samples, block[lo:hi]...)
				}
				pos += int64(len(block))
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				samples = nil
				break
			}
		}
		reader.Close()
		if len(samples) >= 24000 {
			if emb, err := extract(samples, 16000); err == nil {
				return emb
			}
		}
	}
	return nil
}

func (a *App) RenameMeetingSpeaker(meetingKey, speakerID, name string) error {
	if a.activity == nil || a.activity.store == nil {
		return errMeetingNotesNotReadable
	}
	store := a.activity.store
	generation, err := store.callVoiceGeneration()
	if err != nil {
		return err
	}
	r, ok, err := store.meetingNotesByKey(meetingKey)
	if err != nil {
		return err
	}
	if !ok {
		return errMeetingNotesNotReadable
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return err
	}
	if _, ok := a.meetingNotesReadable(store, r, policy); !ok {
		return errMeetingNotesNotReadable
	}
	emb := a.callSpeakerSignature(r, speakerID)
	policy, err = a.activityReadPrivacyPolicy()
	if err != nil {
		return err
	}
	if _, ok := a.meetingNotesReadable(store, r, policy); !ok {
		return errMeetingNotesNotReadable
	}
	if err := store.renameCallSpeaker(meetingKey, speakerID, name, emb, generation); err != nil {
		return err
	}
	policy, err = a.activityReadPrivacyPolicy()
	if _, readable := a.meetingNotesReadable(store, r, policy); err != nil || !readable {
		_, _ = store.db.Exec(`DELETE FROM meeting_voice_signatures WHERE meeting_key=? AND speaker_id=?`, meetingKey, speakerID)
		return errMeetingNotesNotReadable
	}
	if a.ctx != nil {
		a.emitBrowserEvent(meetingNotesSavedEvent, map[string]string{"meetingKey": meetingKey})
	}
	return nil
}

// Forget retains the names typed in existing notes, but removes every learned
// call signature. New calls cannot regain a voice through a retained name.
func (a *App) ForgetVoices() error {
	a.voiceMemoryMu.Lock()
	defer a.voiceMemoryMu.Unlock()
	a.voiceMemoryGeneration++
	if a.activity == nil || a.activity.store == nil {
		return errors.New("activity store unavailable")
	}
	if err := a.activity.store.forgetCallVoices(); err != nil {
		return err
	}
	if a.store != nil {
		if err := a.store.DeleteAllVoiceEmbeddings(context.Background()); err != nil {
			return fmt.Errorf("forget enrolled voices: %w", err)
		}
		a.mu.Lock()
		_ = a.refreshSelfEmbeddingCacheLocked()
		a.mu.Unlock()
	}
	return nil
}
