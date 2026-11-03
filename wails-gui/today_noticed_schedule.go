package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"time"

	"wails-gui/storage"
)

var todayNoticedScheduleMu sync.Mutex
var errTodayNoticedUnavailable = errors.New("this suggestion is no longer available")

type todayNoticedScheduleSource struct {
	Target   MobileBriefActivityTarget `json:"target"`
	Proof    string                    `json:"proof"`
	Language string                    `json:"language"`
}

type TodayNoticedSchedule struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Proof    string `json:"proof"`
}

func todayNoticedScheduleKey(target MobileBriefActivityTarget, proof string) string {
	digest := sha256.Sum256([]byte(target.Date + "\x00" + target.SuggestionID + "\x00" + target.Action + "\x00" + proof))
	return "today_noticed_schedule:" + hex.EncodeToString(digest[:])
}

func todayNoticedSourceProof(item NoticedSuggestion) string {
	payload, _ := json.Marshal(item)
	return agencyPayloadHash(payload)
}

func todayNoticedScheduleRevision(job *storage.CronJobRow) string {
	if job == nil {
		return ""
	}
	payload, _ := json.Marshal(struct {
		Name, Description, ScheduleKind, ScheduleConfig, TargetKind, TargetConfig string
		Enabled                                                                   bool
		UpdatedAt, LastRunAt, RetryRunAt                                          int64
	}{job.Name, job.Description, job.ScheduleKind, job.ScheduleConfig, job.TargetKind, job.TargetConfig, job.Enabled, job.UpdatedAt, job.LastRunAt, job.RetryRunAt})
	return agencyPayloadHash(payload)
}

func (a *App) todayNoticedSuggestionInLanguage(target MobileBriefActivityTarget, language string) (NoticedSuggestion, error) {
	if a == nil || a.store == nil || a.activity == nil || a.activity.store == nil || a.cronScheduler == nil || a.IsClosedEyesModeEnabled() {
		return NoticedSuggestion{}, errTodayNoticedUnavailable
	}
	items, generation, err := a.activity.store.noticedStored(target.Date, language)
	if err != nil {
		return NoticedSuggestion{}, err
	}
	if len(items) > 0 && !a.noticedCurrent(target.Date, language, generation) {
		return NoticedSuggestion{}, errTodayNoticedUnavailable
	}
	rows, err := a.noticedCheck(target.Date, items)
	if err != nil {
		return NoticedSuggestion{}, err
	}
	for _, row := range rows {
		if row.ID == target.SuggestionID && row.Kind == target.Action {
			return row, nil
		}
	}
	return NoticedSuggestion{}, errTodayNoticedUnavailable
}

func todayNoticedInput(target MobileBriefActivityTarget, text string, draft *NoticedRoutineDraft) (CronJobInput, error) {
	var name, prompt, kind string
	var schedule json.RawMessage
	kind, schedule = "manual", json.RawMessage(`{}`)
	switch target.Action {
	case noticedKindFollowUp:
		name = userPurposeTitle(text)
		if name == "" {
			return CronJobInput{}, errActivityFollowUpEmpty
		}
		prompt = "Follow up on this request: " + name + "\nPrepare anything for another person as a draft and wait for the user to send it."
	case noticedKindRoutine:
		if draft == nil {
			return CronJobInput{}, errNoticedRoutineStep
		}
		clean := noticedCleanRoutine(draft, "")
		if clean.Name == "" {
			return CronJobInput{}, errNoticedRoutineName
		}
		if len(clean.Steps) == 0 {
			return CronJobInput{}, errNoticedRoutineStep
		}
		expr, ok := noticedCronExpr(draft.When)
		if !ok {
			return CronJobInput{}, errNoticedRoutineWhen
		}
		name, prompt = clean.Name, noticedRoutinePrompt(*clean)
		if expr != "" {
			kind = "cron"
			schedule, _ = json.Marshal(scheduleCron{Expr: expr, TZ: time.Local.String()})
			next, err := computeFirstRun(kind, schedule, time.Now())
			if err != nil || next <= time.Now().Add(5*time.Second).UnixMilli() {
				return CronJobInput{}, errTodayNoticedUnavailable
			}
		}
	default:
		return CronJobInput{}, errTodayNoticedUnavailable
	}
	cfg, err := json.Marshal(cronTargetConfig{Prompt: prompt, Model: ""})
	if err != nil {
		return CronJobInput{}, err
	}
	notify := false
	return CronJobInput{Name: name, Description: "Saved from Isoform noticed (" + target.Date + ")", ScheduleKind: kind, ScheduleConfig: schedule, TargetKind: "agent", TargetConfig: cfg, NotifyDM: &notify}, nil
}

// AddTodayNoticedSchedule saves the suggestion's current words and drafted
// steps as a schedule. An untimed item is manual, never an invented time.
func (a *App) AddTodayNoticedSchedule(target MobileBriefActivityTarget) (TodayNoticedSchedule, error) {
	todayNoticedScheduleMu.Lock()
	defer todayNoticedScheduleMu.Unlock()
	language := a.currentUILanguage()
	item, err := a.todayNoticedSuggestionInLanguage(target, language)
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	ctx := context.Background()
	proof := todayNoticedSourceProof(item)
	key := todayNoticedScheduleKey(target, proof)
	previous, err := a.store.GetUIState(ctx, key)
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	if previous != "" {
		var recorded TodayNoticedSchedule
		if json.Unmarshal([]byte(previous), &recorded) != nil || recorded.ID == "" || recorded.Proof != proof {
			return TodayNoticedSchedule{}, errTodayNoticedUnavailable
		}
		row, err := a.store.LoadCronJob(ctx, recorded.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return TodayNoticedSchedule{}, err
		}
		if row != nil {
			if todayNoticedScheduleRevision(row) != recorded.Revision {
				return TodayNoticedSchedule{}, errTodayNoticedUnavailable
			}
			return recorded, nil
		}
	}
	input, err := todayNoticedInput(target, item.Text, item.Routine)
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	input.TargetConfig, err = json.Marshal(cronTargetConfig{Prompt: extractPromptFromTargetConfig(input.TargetConfig), Model: a.chatTaskModel(), TodayNoticed: &todayNoticedScheduleSource{Target: target, Proof: proof, Language: language}})
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	intendedKind, intendedSchedule := input.ScheduleKind, input.ScheduleConfig
	// Keep it manual until the selected source has been rechecked. In
	// particular, a near-term routine must not run during that check.
	input.ScheduleKind, input.ScheduleConfig = "manual", json.RawMessage(`{}`)
	created, err := a.CreateCronJob(input)
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	// The source can be withdrawn while the scheduler writes. Refuse to
	// expose an accepted result if its current evidence has changed.
	current, err := a.todayNoticedSuggestionInLanguage(target, language)
	if err != nil || !reflect.DeepEqual(item, current) {
		_ = a.DeleteCronJob(created.ID)
		return TodayNoticedSchedule{}, errTodayNoticedUnavailable
	}
	if intendedKind != "manual" {
		next, checkErr := computeFirstRun(intendedKind, intendedSchedule, time.Now())
		if checkErr != nil || next <= time.Now().Add(5*time.Second).UnixMilli() {
			_ = a.DeleteCronJob(created.ID)
			return TodayNoticedSchedule{}, errTodayNoticedUnavailable
		}
		if _, err := a.UpdateCronJob(created.ID, CronJobPatch{ScheduleKind: &intendedKind, ScheduleConfig: &intendedSchedule}); err != nil {
			_ = a.DeleteCronJob(created.ID)
			return TodayNoticedSchedule{}, err
		}
	}
	row, err := a.store.LoadCronJob(ctx, created.ID)
	if err != nil || row == nil {
		_ = a.DeleteCronJob(created.ID)
		return TodayNoticedSchedule{}, errTodayNoticedUnavailable
	}
	result := TodayNoticedSchedule{ID: created.ID, Revision: todayNoticedScheduleRevision(row), Proof: proof}
	encoded, err := json.Marshal(result)
	if err != nil {
		_ = a.DeleteCronJob(created.ID)
		return TodayNoticedSchedule{}, err
	}
	if err := a.store.SetUIState(ctx, key, string(encoded)); err != nil {
		_ = a.DeleteCronJob(created.ID)
		return TodayNoticedSchedule{}, err
	}
	return result, nil
}

// UndoTodayNoticedSchedule refuses to remove a schedule the person has
// changed or run since adding it.
func (a *App) UndoTodayNoticedSchedule(target MobileBriefActivityTarget, saved TodayNoticedSchedule) error {
	todayNoticedScheduleMu.Lock()
	defer todayNoticedScheduleMu.Unlock()
	if a == nil || a.store == nil || a.cronScheduler == nil || saved.ID == "" || saved.Revision == "" || saved.Proof == "" {
		return errTodayNoticedUnavailable
	}
	ctx := context.Background()
	// An editor or dispatcher must not pass the version/run checks while
	// Undo removes the schedule. Dispatch also checks that the job remains.
	a.cronScheduler.changeMu.Lock()
	defer a.cronScheduler.changeMu.Unlock()
	key := todayNoticedScheduleKey(target, saved.Proof)
	stored, err := a.store.GetUIState(ctx, key)
	var recorded TodayNoticedSchedule
	if err != nil || json.Unmarshal([]byte(stored), &recorded) != nil || recorded != saved {
		return errTodayNoticedUnavailable
	}
	id := saved.ID
	a.cronScheduler.trackedLock()
	running := a.cronScheduler.running[id]
	a.cronScheduler.trackedUnlock()
	if running {
		return errTodayNoticedUnavailable
	}
	row, err := a.store.LoadCronJob(ctx, id)
	if err != nil || row == nil || todayNoticedScheduleRevision(row) != saved.Revision {
		return errTodayNoticedUnavailable
	}
	runs, err := a.store.ListCronRuns(ctx, id, 1)
	if err != nil || len(runs) != 0 {
		return errTodayNoticedUnavailable
	}
	if err := a.DeleteCronJob(id); err != nil {
		return err
	}
	return a.store.SetUIState(ctx, key, "")
}

// EditTodayNoticedSchedule changes only the schedule this suggestion created,
// and only while both the source and the displayed revision are current.
func (a *App) EditTodayNoticedSchedule(target MobileBriefActivityTarget, saved TodayNoticedSchedule, text string, draft *NoticedRoutineDraft) (TodayNoticedSchedule, error) {
	todayNoticedScheduleMu.Lock()
	defer todayNoticedScheduleMu.Unlock()
	language := a.currentUILanguage()
	item, err := a.todayNoticedSuggestionInLanguage(target, language)
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	if saved.Proof == "" || todayNoticedSourceProof(item) != saved.Proof {
		return TodayNoticedSchedule{}, errTodayNoticedUnavailable
	}
	ctx := context.Background()
	key := todayNoticedScheduleKey(target, saved.Proof)
	stored, err := a.store.GetUIState(ctx, key)
	var recorded TodayNoticedSchedule
	if err != nil || json.Unmarshal([]byte(stored), &recorded) != nil || recorded != saved {
		return TodayNoticedSchedule{}, errTodayNoticedUnavailable
	}
	id := saved.ID
	row, err := a.store.LoadCronJob(ctx, id)
	if err != nil || row == nil || todayNoticedScheduleRevision(row) != saved.Revision {
		return TodayNoticedSchedule{}, errTodayNoticedUnavailable
	}
	input, err := todayNoticedInput(target, text, draft)
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	input.TargetConfig, err = json.Marshal(cronTargetConfig{Prompt: extractPromptFromTargetConfig(input.TargetConfig), Model: a.chatTaskModel(), TodayNoticed: &todayNoticedScheduleSource{Target: target, Proof: saved.Proof, Language: language}})
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	patch := CronJobPatch{Name: &input.Name, ScheduleKind: &input.ScheduleKind, ScheduleConfig: &input.ScheduleConfig, TargetConfig: &input.TargetConfig}
	if _, err := a.UpdateCronJob(id, patch); err != nil {
		return TodayNoticedSchedule{}, err
	}
	row, err = a.store.LoadCronJob(ctx, id)
	if err != nil || row == nil {
		return TodayNoticedSchedule{}, errTodayNoticedUnavailable
	}
	updated := TodayNoticedSchedule{ID: id, Revision: todayNoticedScheduleRevision(row), Proof: saved.Proof}
	encoded, err := json.Marshal(updated)
	if err != nil {
		return TodayNoticedSchedule{}, err
	}
	if err := a.store.SetUIState(ctx, key, string(encoded)); err != nil {
		return TodayNoticedSchedule{}, err
	}
	return updated, nil
}

// A schedule derived from observation must not expose its saved prompt to
// an unattended run after its original source is withdrawn or changed.
func (a *App) todayNoticedScheduleReadable(source *todayNoticedScheduleSource) bool {
	if source == nil || source.Proof == "" || source.Language == "" {
		return false
	}
	item, err := a.todayNoticedSuggestionInLanguage(source.Target, source.Language)
	return err == nil && todayNoticedSourceProof(item) == source.Proof
}
