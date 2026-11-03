package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"wails-gui/internal/engagement"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

// The actions in an opened row of the Activity page's "What you worked on"
// (backlog item 62, step 3): Add follow-up, Remind me and Not right. Go keeps
// every rule: the prepared follow-up line, the follow-up as a user commitment
// in the Purpose Map, the reminder as a held engagement moment, and the
// user's corrections to a title or summary. The page only shows the fields.
const (
	// activityFollowUpSourceKind marks a commitment the user added from a
	// moment in Activity; its evidence names the moment's activity id.
	activityFollowUpSourceKind = "activity_followup"
	// activityFollowUpMaxRunes bounds the prepared line, so a long AI title
	// becomes a short sentence the user can read and edit.
	activityFollowUpMaxRunes = 80
	// activityReminderTitleMaxRunes bounds the line a reminder says.
	activityReminderTitleMaxRunes = 80
	// activityReminderMinLead and activityReminderMaxLead bound when a
	// reminder may be set for.
	activityReminderMinLead = 30 * time.Second
	activityReminderMaxLead = 366 * 24 * time.Hour
	// activityReminderGrace is how late a reminder is still worth saying,
	// for a computer that slept through its time.
	activityReminderGrace = 24 * time.Hour
	// activityCorrectionTitleMaxRunes and activityCorrectionDescriptionMaxRunes
	// bound what the user types into Not right.
	activityCorrectionTitleMaxRunes       = 200
	activityCorrectionDescriptionMaxRunes = 600
	// activityNamingCorrectionsInPrompt is how many recent title corrections
	// the composer sees as the user's naming style.
	activityNamingCorrectionsInPrompt = 10

	// Why a row offers no follow-up, reminder or chat.
	activityActionsPrivate = "private"
	activityActionsPaused  = "paused"
)

var (
	errActivityNotFound        = errors.New("this moment is no longer in your history")
	errActivityActionsWithheld = errors.New("Isoform keeps this moment out of follow-ups and reminders")
	errActivityFollowUpEmpty   = errors.New("a follow-up needs a sentence")
	errActivityReminderTime    = errors.New("pick a time in the future")
	errActivityRemindersOff    = errors.New("reminders are not available right now")
	errActivityTitleEmpty      = errors.New("a title cannot be empty")
)

// ActivityFollowUp is the open follow-up a moment has under "What needs your
// attention". EventID is the event the user saw, for Undo.
type ActivityFollowUp struct {
	OpenLoopID string `json:"openLoopId"`
	EventID    string `json:"eventId"`
	Text       string `json:"text"`
}

// ── Migration ──

// migrateActivityUserCorrections adds the user's own title and summary to an
// activity (they win over the composer's), the log of those corrections the
// composer reads as the user's naming style, and the link from a follow-up
// commitment back to the moment it was added from.
func migrateActivityUserCorrections(tx *sql.Tx) error {
	for _, stmt := range []string{
		`ALTER TABLE activities ADD COLUMN user_title TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE activities ADD COLUMN user_description TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := tx.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS activity_user_corrections (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			activity_id TEXT NOT NULL,
			field       TEXT NOT NULL,
			before_text TEXT NOT NULL,
			after_text  TEXT NOT NULL,
			created_at  INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_user_corrections_created ON activity_user_corrections(created_at)`,
		`CREATE TABLE IF NOT EXISTS activity_followups (
			open_loop_id TEXT PRIMARY KEY,
			activity_id  TEXT NOT NULL,
			created_at   INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_followups_activity ON activity_followups(activity_id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *ActivityStore) activityUserCorrectionsPresent() (bool, error) {
	a, e1 := s.hasColumn("activities", "user_title")
	b, e2 := s.hasColumn("activities", "user_description")
	c, e3 := s.hasTable("activity_user_corrections")
	d, e4 := s.hasTable("activity_followups")
	return a && b && c && d, firstErr(e1, e2, e3, e4)
}

// ── The prepared follow-up line ──

// activityFollowUpClauseBreak ends a title's first clause: a comma,
// semicolon or colon (either width), a full stop, or a spaced dash.
var activityFollowUpClauseBreak = regexp.MustCompile(`\s*(?:[,;:，；：。]|\s[-–—]\s)\s*`)

// activityFollowUpClause is a title's first clause, so "Review and merge
// isoform PRs, direct Kimi workers, and plan the release" reads "Review and
// merge isoform PRs".
func activityFollowUpClause(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if loc := activityFollowUpClauseBreak.FindStringIndex(title); loc != nil && loc[0] > 0 {
		title = title[:loc[0]]
	}
	return strings.TrimSpace(title)
}

// activityLowerFirst lowercases a leading capital so the clause reads inside
// a sentence, but keeps a word with other capitals ("PRs", "API") as it is.
func activityLowerFirst(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	if first == utf8.RuneError || !unicode.IsUpper(first) {
		return text
	}
	word := text[size:]
	if end := strings.IndexFunc(word, unicode.IsSpace); end >= 0 {
		word = word[:end]
	}
	for _, r := range word {
		if unicode.IsUpper(r) {
			return text
		}
	}
	return string(unicode.ToLower(first)) + text[size:]
}

// activityFitRunes shortens text to at most limit runes, at a word boundary
// when the text has spaces.
func activityFitRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := string(runes[:limit])
	if space := strings.LastIndexFunc(cut, unicode.IsSpace); space > 0 && strings.ContainsFunc(text, unicode.IsSpace) {
		cut = cut[:space]
	}
	return strings.TrimSpace(cut)
}

// activityFollowUpText is the line Add follow-up starts from: "Follow up
// with Priya about the Halcyon Arc lamp", or "Follow up on <the title's first
// clause>" with no person, at most activityFollowUpMaxRunes. The user edits
// it before it is added.
func activityFollowUpText(title, person, lang string) string {
	clause := activityFollowUpClause(title)
	if clause == "" {
		return ""
	}
	person = strings.Join(strings.Fields(person), " ")
	var prefix string
	switch lang {
	case "zh-CN":
		prefix = "跟进："
		if person != "" {
			prefix = "与" + person + "跟进："
		}
	default:
		clause = activityLowerFirst(clause)
		prefix = "Follow up on "
		if person != "" {
			prefix = "Follow up with " + person + " about "
		}
	}
	room := activityFollowUpMaxRunes - utf8.RuneCountInString(prefix)
	if room < 12 {
		// A very long name: leave it out rather than squeeze the subject.
		return activityFollowUpText(title, "", lang)
	}
	return prefix + activityFitRunes(clause, room)
}

// activityFirstPerson is the first person a moment names.
func activityFirstPerson(entities []Entity) string {
	for _, e := range entities {
		if e.Type == "person" && strings.TrimSpace(e.Name) != "" {
			return strings.TrimSpace(e.Name)
		}
	}
	return ""
}

// activityActionsWithheld says why a moment offers no follow-up, reminder or
// chat: a private window or a sensitive app, or a time observation was paused.
func activityActionsWithheld(row ActivityRow, pauses []observationPause) string {
	if activitymon.IsPrivateBrowsing(row.Title) || composerBucketContainsSensitiveApp(row) {
		return activityActionsPrivate
	}
	for _, pause := range pauses {
		if (row.StartMs < pause.End || pause.End == 0) && row.EndMs >= pause.Start {
			return activityActionsPaused
		}
	}
	return ""
}

// ── Row state for the Day view ──

// fillActivityRowActions adds what the row actions need to each view: the
// prepared follow-up line, the open follow-up, the pending reminder, whether
// the user corrected it, and why actions are withheld. A failed lookup is
// logged and leaves its field empty; the list itself still loads.
func (a *App) fillActivityRowActions(views []ActivityView, rows []ActivityRow) {
	if len(views) == 0 || a.activity == nil || a.activity.store == nil {
		return
	}
	store := a.activity.store
	ids := make([]string, len(rows))
	byID := make(map[string]ActivityRow, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		byID[r.ID] = r
	}
	pauses, pauseErr := a.observationPauses()
	lang := a.currentUILanguage()
	followUps, err := a.activityOpenFollowUps(ids)
	if err != nil {
		log.Printf("[activity] reading follow-ups: %v", err)
	}
	reminders, err := a.activityPendingReminders(context.Background())
	if err != nil {
		log.Printf("[activity] reading reminders: %v", err)
	}
	corrected, err := store.activityCorrectedIDs(ids)
	if err != nil {
		log.Printf("[activity] reading corrections: %v", err)
	}
	for i := range views {
		row, ok := byID[views[i].ID]
		if !ok {
			continue
		}
		views[i].Corrected = corrected[row.ID]
		views[i].FollowUp = followUps[row.ID]
		views[i].ReminderAt = reminders[row.ID]
		if pauseErr != nil {
			// Unknown pauses: say nothing that could repeat paused work.
			views[i].ActionsWithheld = activityActionsPaused
			continue
		}
		views[i].ActionsWithheld = activityActionsWithheld(row, pauses)
		if views[i].ActionsWithheld == "" {
			views[i].FollowUpText = activityFollowUpText(row.Title, activityFirstPerson(views[i].Entities), lang)
		}
	}
}

func (s *ActivityStore) activityCorrectedIDs(ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	query := `SELECT id FROM activities WHERE (user_title != '' OR user_description != '') AND id IN (` + sqlPlaceholders(len(ids)) + `)`
	rows, err := s.reader().Query(query, stringsToAny(ids)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// ── Add follow-up ──

// AddActivityFollowUp adds the user's line as a commitment linked to the
// moment, so it shows under Today's "What needs your attention". key is the
// caller's idempotency key: a retry with the same key returns the same
// follow-up instead of adding a second.
func (a *App) AddActivityFollowUp(activityID, text, key string) (ActivityFollowUp, error) {
	return a.addActivityFollowUp(activityID, text, key, time.Now())
}

func (a *App) addActivityFollowUp(activityID, text, key string, now time.Time) (ActivityFollowUp, error) {
	row, err := a.activityForAction(activityID)
	if err != nil {
		return ActivityFollowUp{}, err
	}
	text = userPurposeTitle(text)
	if text == "" {
		return ActivityFollowUp{}, errActivityFollowUpEmpty
	}
	key = strings.TrimSpace(key)
	if key == "" {
		sum := sha256.Sum256([]byte(row.ID + "\x00" + text + "\x00" + strconv.FormatInt(now.UnixNano(), 10)))
		key = hex.EncodeToString(sum[:8])
	}
	detail, err := a.createUserPurpose(userPurposeCreate{
		Key:              "activity:" + row.ID + ":" + key,
		Origin:           "activity",
		Kind:             PurposeKindCommitment,
		Text:             text,
		EvidenceKind:     activityFollowUpSourceKind,
		EvidenceSourceID: row.ID,
	}, now)
	if err != nil {
		return ActivityFollowUp{}, err
	}
	if _, err := a.activity.store.db.Exec(`INSERT OR IGNORE INTO activity_followups (open_loop_id, activity_id, created_at) VALUES (?, ?, ?)`,
		detail.OpenLoop.ID, row.ID, now.UnixMilli()); err != nil {
		return ActivityFollowUp{}, err
	}
	a.emitBrowserEvent("agency:updated")
	current := purposeCurrentLedgerEvent(detail)
	return ActivityFollowUp{OpenLoopID: detail.OpenLoop.ID, EventID: current.ID, Text: current.Title}, nil
}

// UndoActivityFollowUp takes back a follow-up just added: it is dismissed in
// the Purpose Map. expectedEventID is the event the user saw; a follow-up
// changed since then is left alone. Undoing one already closed is a no-op.
func (a *App) UndoActivityFollowUp(openLoopID, expectedEventID string) error {
	return a.undoActivityFollowUp(openLoopID, expectedEventID, time.Now())
}

func (a *App) undoActivityFollowUp(openLoopID, expectedEventID string, now time.Time) error {
	store, err := a.userPurposeStore()
	if err != nil {
		return err
	}
	detail, err := store.GetDetail(strings.TrimSpace(openLoopID))
	if err != nil {
		return err
	}
	if !activityFollowUpPurpose(detail) {
		return errUserPurposeState
	}
	current := purposeCurrentLedgerEvent(detail)
	switch current.LifecycleState {
	case PurposeStateActive, PurposeStateWaiting, PurposeStateCandidate:
	default:
		return nil
	}
	if strings.TrimSpace(expectedEventID) != "" && expectedEventID != current.ID {
		return errUserPurposeChanged
	}
	occurredAt := now.UnixMilli()
	if occurredAt < current.OccurredAt {
		occurredAt = current.OccurredAt
	}
	if wall := time.Now().UnixMilli(); occurredAt > wall {
		occurredAt = wall
	}
	key := "user-goal-event:activity-undo:" + detail.OpenLoop.ID + ":" + current.ID
	if _, _, err := store.AppendEvent(AppendPurposeOpenLoopEventInput{
		OpenLoopID:       detail.OpenLoop.ID,
		ExpectedEventID:  current.ID,
		IdempotencyKey:   key,
		EventType:        PurposeEventDismissed,
		Title:            current.Title,
		DesiredOutcome:   current.DesiredOutcome,
		Owner:            current.Owner,
		Counterparty:     current.Counterparty,
		Scope:            current.Scope,
		DueKind:          current.DueKind,
		DueValue:         current.DueValue,
		WaitingCondition: current.WaitingCondition,
		ReasoningStatus:  PurposeReasoningUnknown,
		Confidence:       userPurposeConfidence,
		OccurredAt:       occurredAt,
		Evidence:         []PurposeEvidenceInput{userPurposeEvidence(key, "activity", PurposeStanceDismiss, current.Title, occurredAt)},
	}); err != nil {
		return err
	}
	a.emitBrowserEvent("agency:updated")
	return nil
}

// activityFollowUpPurpose reports whether a Purpose is a follow-up the user
// added from a moment in Activity.
func activityFollowUpPurpose(detail PurposeOpenLoopDetail) bool {
	for _, evidence := range detail.Evidence {
		if evidence.SourceKind == activityFollowUpSourceKind {
			return true
		}
	}
	return false
}

// activityOpenFollowUps maps each activity id to its newest open follow-up.
func (a *App) activityOpenFollowUps(ids []string) (map[string]*ActivityFollowUp, error) {
	out := map[string]*ActivityFollowUp{}
	if len(ids) == 0 {
		return out, nil
	}
	if a.activity == nil || a.activity.store == nil || a.activity.store.db == nil {
		return out, errUserPurposeStore
	}
	purposes := NewPurposeMapStore(a.activity.store.reader())
	rows, err := a.activity.store.reader().Query(`SELECT open_loop_id, activity_id FROM activity_followups WHERE activity_id IN (`+sqlPlaceholders(len(ids))+`) ORDER BY created_at`, stringsToAny(ids)...)
	if err != nil {
		return out, err
	}
	type link struct{ openLoopID, activityID string }
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.openLoopID, &l.activityID); err != nil {
			rows.Close()
			return out, err
		}
		links = append(links, l)
	}
	rows.Close()
	for _, l := range links {
		detail, err := purposes.GetDetail(l.openLoopID)
		if err != nil {
			continue
		}
		current := purposeCurrentLedgerEvent(detail)
		if current.LifecycleState != PurposeStateActive && current.LifecycleState != PurposeStateWaiting {
			continue
		}
		out[l.activityID] = &ActivityFollowUp{OpenLoopID: detail.OpenLoop.ID, EventID: current.ID, Text: current.Title}
	}
	return out, nil
}

// ── Remind me ──

const (
	activityReminderLaterToday      = "later_today"
	activityReminderTomorrowMorning = "tomorrow_morning"
	// activityReminderLaterLead is how far "Later today" looks ahead; it is
	// offered only before activityReminderLaterCutoffHour.
	activityReminderLaterLead       = 3 * time.Hour
	activityReminderLaterCutoffHour = 20
	activityReminderMorningHour     = 9
)

// ActivityReminderChoice is one quick time Remind me offers.
type ActivityReminderChoice struct {
	ID   string `json:"id"`
	AtMs int64  `json:"atMs"`
}

// GetActivityReminderChoices returns the quick times: "Later today" (three
// hours from now, on the quarter hour, until 8 PM) and "Tomorrow morning"
// (9:00). The user can also pick any time.
func (a *App) GetActivityReminderChoices() []ActivityReminderChoice {
	return activityReminderChoices(time.Now())
}

func activityReminderChoices(now time.Time) []ActivityReminderChoice {
	var out []ActivityReminderChoice
	if now.Hour() < activityReminderLaterCutoffHour {
		later := now.Add(activityReminderLaterLead).Truncate(time.Minute)
		if extra := later.Minute() % 15; extra != 0 {
			later = later.Add(time.Duration(15-extra) * time.Minute)
		}
		out = append(out, ActivityReminderChoice{ID: activityReminderLaterToday, AtMs: later.UnixMilli()})
	}
	tomorrow := now.AddDate(0, 0, 1)
	morning := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), activityReminderMorningHour, 0, 0, 0, now.Location())
	return append(out, ActivityReminderChoice{ID: activityReminderTomorrowMorning, AtMs: morning.UnixMilli()})
}

// SetActivityReminder sets (or moves) the moment's reminder to atMs. It is a
// reminder engagement moment held until that time, then said on the computer
// and the phone. It returns the time it was set for.
func (a *App) SetActivityReminder(activityID string, atMs int64) (int64, error) {
	return a.setActivityReminder(context.Background(), activityID, time.UnixMilli(atMs), time.Now())
}

func (a *App) setActivityReminder(ctx context.Context, activityID string, at, now time.Time) (int64, error) {
	row, err := a.activityForAction(activityID)
	if err != nil {
		return 0, err
	}
	if !at.After(now.Add(activityReminderMinLead)) || at.After(now.Add(activityReminderMaxLead)) {
		return 0, errActivityReminderTime
	}
	if !a.engagementActive() {
		return 0, errActivityRemindersOff
	}
	if err := a.cancelActivityReminder(ctx, row.ID); err != nil {
		return 0, err
	}
	title := activityFitRunes(activityFollowUpClause(row.Title), activityReminderTitleMaxRunes)
	if title == "" {
		title = activityFitRunes(strings.TrimSpace(row.Title), activityReminderTitleMaxRunes)
	}
	stored, err := a.engagement.Submit(ctx, engagement.Moment{
		Kind:      engagement.KindReminder,
		SubjectID: row.ID,
		DedupeKey: engagement.DedupeKey(engagement.KindReminder, row.ID, strconv.FormatInt(at.UnixMilli(), 10)),
		NotBefore: at,
		ExpiresAt: at.Add(activityReminderGrace),
		Stakes:    0.9,
		Params:    map[string]string{"title": title, "day": time.UnixMilli(row.StartMs).Format("2006-01-02")},
		Channels:  []engagement.Channel{engagement.ChannelDesktop, engagement.ChannelPush},
	})
	if err != nil {
		return 0, err
	}
	if stored.State == storage.EngagementStateSuppressed {
		// The line named something Isoform never sends; nothing was set.
		return 0, errActivityActionsWithheld
	}
	return at.UnixMilli(), nil
}

// CancelActivityReminder takes back the moment's pending reminder.
func (a *App) CancelActivityReminder(activityID string) error {
	return a.cancelActivityReminder(context.Background(), strings.TrimSpace(activityID))
}

func (a *App) cancelActivityReminder(ctx context.Context, activityID string) error {
	if !a.engagementActive() || a.store == nil {
		return errActivityRemindersOff
	}
	open, err := a.store.OpenEngagementMomentsOfKind(ctx, string(engagement.KindReminder))
	if err != nil {
		return err
	}
	for _, row := range open {
		if row.SubjectID != activityID || row.State != storage.EngagementStateScheduled || row.SentAt != 0 {
			continue
		}
		if err := a.engagement.Retire(ctx, row.DedupeKey, "reminder cancelled"); err != nil {
			return err
		}
	}
	return nil
}

// activityPendingReminders maps each activity id to its pending reminder's
// time.
func (a *App) activityPendingReminders(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	if a.store == nil {
		return out, nil
	}
	open, err := a.store.OpenEngagementMomentsOfKind(ctx, string(engagement.KindReminder))
	if err != nil {
		return out, err
	}
	for _, row := range open {
		if row.State != storage.EngagementStateScheduled || row.SentAt != 0 || row.HoldUntil <= 0 {
			continue
		}
		if row.HoldUntil > out[row.SubjectID] {
			out[row.SubjectID] = row.HoldUntil
		}
	}
	return out, nil
}

// ── Not right ──

// CorrectActivity replaces a moment's title and summary with the user's.
// The user's text wins over the composer from then on, and each change is
// logged so the composer names later moments the user's way.
func (a *App) CorrectActivity(activityID, title, description string) error {
	if a.activity == nil || a.activity.store == nil {
		return errActivityNotFound
	}
	changed, err := a.activity.store.correctActivityText(strings.TrimSpace(activityID), title, description, time.Now())
	if err != nil {
		return err
	}
	if changed {
		a.emitBrowserEvent("activity:activities-updated")
	}
	return nil
}

func activityCorrectionText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.ToValidUTF8(text, "")), " ")
	if utf8.RuneCountInString(text) > limit {
		text = string([]rune(text)[:limit])
	}
	return text
}

func (s *ActivityStore) correctActivityText(activityID, title, description string, now time.Time) (bool, error) {
	title = activityCorrectionText(title, activityCorrectionTitleMaxRunes)
	description = activityCorrectionText(description, activityCorrectionDescriptionMaxRunes)
	if title == "" {
		return false, errActivityTitleEmpty
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentTitle, currentDescription string
	err = tx.QueryRow(`SELECT title, description FROM activities WHERE id = ?`, activityID).Scan(&currentTitle, &currentDescription)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errActivityNotFound
	}
	if err != nil {
		return false, err
	}
	changed := false
	for _, field := range []struct{ name, before, after, column, userColumn string }{
		{"title", currentTitle, title, "title", "user_title"},
		{"description", currentDescription, description, "description", "user_description"},
	} {
		if field.after == field.before {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO activity_user_corrections (activity_id, field, before_text, after_text, created_at) VALUES (?, ?, ?, ?, ?)`,
			activityID, field.name, field.before, field.after, now.UnixMilli()); err != nil {
			return false, err
		}
		// An emptied summary stays empty: the lock is the user's text, and
		// "" would release it, so a single space marks a cleared one.
		lock := field.after
		if lock == "" {
			lock = " "
		}
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE activities SET %s = ?, %s = ?, updated_at = ? WHERE id = ?`, field.column, field.userColumn),
			field.after, lock, now.UnixMilli(), activityID); err != nil {
			return false, err
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, tx.Commit()
}

// activityNameCorrection is one title the user corrected.
type activityNameCorrection struct {
	Before string
	After  string
}

func (s *ActivityStore) recentActivityNameCorrections(limit int) ([]activityNameCorrection, error) {
	rows, err := s.reader().Query(`SELECT before_text, after_text FROM activity_user_corrections WHERE field = 'title' ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activityNameCorrection
	for rows.Next() {
		var c activityNameCorrection
		if err := rows.Scan(&c.Before, &c.After); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// composerNamingCorrections is the prompt block that tells the composer how
// the user renamed earlier moments. Each line is redacted like every other
// activity text the composer sees.
func composerNamingCorrections(corrections []activityNameCorrection) string {
	var b strings.Builder
	for _, c := range corrections {
		before := truncateRunes(redactActivityModelText(strings.TrimSpace(c.Before)), 160)
		after := truncateRunes(redactActivityModelText(strings.TrimSpace(c.After)), 160)
		if before == "" || after == "" || before == after {
			continue
		}
		fmt.Fprintf(&b, "- %q became %q\n", before, after)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\nHow the user renamed earlier activities (follow their naming style for new titles; never copy these names onto unrelated work):\n" + b.String()
}

// ── Shared ──

// activityForAction reads the moment an action is for and refuses one whose
// actions are withheld.
func (a *App) activityForAction(activityID string) (ActivityRow, error) {
	if a.activity == nil || a.activity.store == nil {
		return ActivityRow{}, errActivityNotFound
	}
	row, err := a.activity.store.GetActivityByID(strings.TrimSpace(activityID))
	if err != nil {
		return ActivityRow{}, err
	}
	if row == nil {
		return ActivityRow{}, errActivityNotFound
	}
	pauses, err := a.observationPauses()
	if err != nil || activityActionsWithheld(*row, pauses) != "" {
		return ActivityRow{}, errActivityActionsWithheld
	}
	return *row, nil
}
