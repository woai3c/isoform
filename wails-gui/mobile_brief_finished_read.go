package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync/atomic"
	"time"
)

// today-review v1.1.15 D13: a brief is saved as finished text, plus its
// audio, the moment it is made, and opening it reads that. The finished read
// is the brief as Today shows it (delivery gates and cards applied), saved
// beside the brief with the key of everything those gates read: the stored
// brief, the files and privacy state the brief's sources live in, the read
// policy, Closed Eyes, the language, the day and the run record. A read whose
// key still matches returns the saved text without working anything out
// again; any change makes the next read work it out once and save it.
const mobileBriefFinishedReadExt = ".read"

var errMobileBriefFinishedReadUnreadable = errors.New("the saved brief could not be read")

// mobileBriefReadEpoch counts changes to what a brief read depends on that no
// file stamp shows (privacy withdrawals, mail, calendar, goals). A finished
// read from another process never matches (mobileBriefProcessToken).
var (
	mobileBriefReadEpoch    atomic.Uint64
	mobileBriefProcessToken = strconv.FormatInt(time.Now().UnixNano(), 36)
)

func noteMobileBriefReadInputsChanged() { mobileBriefReadEpoch.Add(1) }

// mobileBriefReadInputEvents are the app events after which a saved read is
// worked out again. brief:stored is not one: the stored brief is in the key.
var mobileBriefReadInputEvents = map[string]bool{
	"cron:state": true, "handoff:created": true, "handoff:updated": true, "insight:saved": true,
	"memory:updated": true, "activity:summary-ready": true, "deliverables:updated": true, "agency:updated": true,
	"sensitive-apps:updated": true, closedEyesModeChangedEvent: true, "background-listening:changed": true,
	"topics:withdrawn": true, "topics:updated": true, "calendar:synced": true, "mail:synced": true, "people:updated": true,
}

type mobileBriefFinishedRead struct {
	Schema int             `json:"schema"`
	Key    string          `json:"key"`
	Brief  json.RawMessage `json:"brief"`
}

func (s *mobileBriefStore) finishedReadPath(briefID string) (string, bool) {
	if s.dir == "" || !mobileBriefIDPattern.MatchString(briefID) {
		return "", false
	}
	return filepath.Join(s.dir, briefID+mobileBriefFinishedReadExt), true
}

func (s *mobileBriefStore) saveFinishedRead(briefID string, read mobileBriefFinishedRead) error {
	if _, ok := s.finishedReadPath(briefID); !ok {
		return errMobileBriefStateUnavailable
	}
	data, err := json.Marshal(read)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	return writeMobileBriefFileAtomic(s.dir, briefID+mobileBriefFinishedReadExt, data)
}

// loadFinishedRead returns the saved read, found=false when there is none,
// and an error when one is there but cannot be read; that one is removed so
// the next try works the read out again.
func (s *mobileBriefStore) loadFinishedRead(briefID string) (mobileBriefFinishedRead, bool, error) {
	path, ok := s.finishedReadPath(briefID)
	if !ok {
		return mobileBriefFinishedRead{}, false, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return mobileBriefFinishedRead{}, false, nil
	}
	var read mobileBriefFinishedRead
	if err == nil && json.Unmarshal(data, &read) == nil && read.Schema == 1 {
		return read, true, nil
	}
	_ = os.Remove(path)
	return mobileBriefFinishedRead{}, false, errMobileBriefFinishedReadUnreadable
}

// finishedReadKey names everything a read of source depends on, or "" when
// any of it cannot be read (then nothing is saved).
// mobileBriefFinishedReadKeys counts finishedReadKey computations, for tests.
var mobileBriefFinishedReadKeys atomic.Uint64

func (s *mobileBriefService) finishedReadKey(ctx context.Context, source MobileBriefV1) string {
	mobileBriefFinishedReadKeys.Add(1)
	if s == nil || s.app == nil || s.app.store == nil {
		return ""
	}
	raw, err := marshalStoredMobileBrief(source)
	if err != nil {
		return ""
	}
	files, err := s.app.readMobileBriefGatherFiles(ctx)
	if err != nil {
		return ""
	}
	stamps := make([]string, 0, len(files))
	for path, info := range files {
		stamps = append(stamps, path+"|"+strconv.FormatInt(info.Size(), 10)+"|"+strconv.FormatInt(info.ModTime().UnixNano(), 10))
	}
	sort.Strings(stamps)
	activity, err := s.app.mobileBriefActivityPrivacyStamp(ctx, time.Now().UnixMilli())
	if err != nil {
		return ""
	}
	pauses, err := s.app.observationPauses()
	if err != nil {
		return ""
	}
	// The mail holdback: a store that cannot be read saves nothing.
	subjects, err := mobileBriefMailSubjects(s.app)
	if err != nil {
		return ""
	}
	loc := s.location
	if loc == nil {
		loc = time.Local
	}
	var run BriefScheduleRun
	if recorded := todayBriefRecordedRun(s, source.BriefID); recorded != nil {
		run = *recorded
	}
	return mobileBriefModelDigest(struct {
		Source, Process, Policy, Language, Day string
		Epoch                                  uint64
		Files, Mail                            []string
		Activity                               [2]int64
		ClosedEyes                             bool
		Run                                    BriefScheduleRun
		Pauses                                 []observationPause
	}{mobileBriefPackageVersion(raw), mobileBriefProcessToken, s.app.mobileBriefGatherPolicy(ctx), s.app.modelDisplayLanguage(), s.now().In(loc).Format("2006-01-02"),
		mobileBriefReadEpoch.Load(), stamps, subjects, [2]int64{activity.sessions, activity.protected}, s.app.IsClosedEyesModeEnabled(), run, pauses})
}

// finishedRead is source as Today opens it: the saved finished read when its
// key still matches, or else worked out once and saved.
func (s *mobileBriefService) finishedRead(source MobileBriefV1) (MobileBriefV1, error) {
	if !s.beginWork() {
		return MobileBriefV1{}, errMobileBriefUnavailable
	}
	defer s.wg.Done()
	ctx := context.Background()
	key := s.finishedReadKey(ctx, source)
	saved, found, err := s.store.loadFinishedRead(source.BriefID)
	if err != nil {
		return MobileBriefV1{}, err
	}
	if found && key != "" && saved.Key == key {
		var brief MobileBriefV1
		if unmarshalStoredMobileBrief(saved.Brief, &brief) == nil && brief.BriefID == source.BriefID {
			return brief, nil
		}
	}
	return s.writeFinishedRead(ctx, source, key), nil
}

// saveFinishedRead saves the finished read of a brief the moment it is made
// or changed (generation, its audio, a patch, Today's refresh).
func (s *mobileBriefService) saveFinishedRead(source MobileBriefV1) {
	if s == nil || s.app == nil || source.BriefID == "" {
		return
	}
	ctx := context.Background()
	s.writeFinishedRead(ctx, source, s.finishedReadKey(ctx, source))
}

func (s *mobileBriefService) writeFinishedRead(ctx context.Context, source MobileBriefV1, key string) MobileBriefV1 {
	brief := s.app.mobileBriefWithCards(s.app.mobileBriefForDelivery(source))
	brief.Audio = s.finishedReadAudio(ctx, source, brief)
	// A change while the read was worked out leaves it unsaved.
	if key == "" || key != s.finishedReadKey(ctx, source) {
		return brief
	}
	if raw, err := marshalStoredMobileBrief(brief); err == nil {
		_ = s.store.saveFinishedRead(source.BriefID, mobileBriefFinishedRead{Schema: 1, Key: key, Brief: raw})
	}
	return brief
}

// storedSource is the stored brief Today shows under briefID: a stored brief,
// or today's snapshot before any.
func (s *mobileBriefService) storedSource(briefID string) (MobileBriefV1, bool) {
	if brief, ok := s.store.load(briefID); ok {
		return brief, true
	}
	brief, ok := s.store.currentToday()
	loc := s.location
	if loc == nil {
		loc = time.Local
	}
	return brief, ok && brief.BriefID == briefID && brief.LocalDate == s.now().In(loc).Format("2006-01-02")
}

// finishedBrief is GetMobileBrief from the finished read.
func (s *mobileBriefService) finishedBrief(briefID string) (MobileBriefV1, bool, error) {
	source, ok := s.storedSource(briefID)
	if !ok {
		return MobileBriefV1{}, false, nil
	}
	brief, err := s.finishedRead(source)
	return brief, err == nil, err
}

// A live row leaving a template brief hides its recording on this read only
// until Today's refresh stores the brief without that row (see
// mobileBriefTodayReadableSynthesis). Read the brief as that refresh stores
// it: the recording stays when what it speaks is still exactly what is shown.
func (s *mobileBriefService) finishedReadAudio(ctx context.Context, source, delivered MobileBriefV1) *MobileBriefAudio {
	if delivered.Audio != nil || s.app == nil || source.Audio == nil || source.Audio.binding == "" {
		return delivered.Audio
	}
	stored, withheld := s.app.mobileBriefDeliveryContext(ctx, source)
	if withheld.Reason != "" {
		return nil
	}
	stored.Audio = source.Audio
	again := s.app.mobileBriefForDeliveryContext(ctx, stored)
	if again.Audio == nil || !mobileBriefParagraphsSay(again.Narrative, delivered.Narrative) || !mobileBriefParagraphsSay(again.WeeklyRecap, delivered.WeeklyRecap) {
		return nil
	}
	return again.Audio
}

func mobileBriefParagraphsSay(left, right []MobileBriefPara) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Text != right[i].Text {
			return false
		}
	}
	return true
}

// deliveredWithAudio is a stored brief as every reader opens it: Today, the
// menu bar's player and the phone keep the same recording.
func (s *mobileBriefService) deliveredWithAudio(source MobileBriefV1) MobileBriefV1 {
	brief := s.app.mobileBriefWithCards(s.app.mobileBriefForDelivery(source))
	brief.Audio = s.finishedReadAudio(context.Background(), source, brief)
	return brief
}
