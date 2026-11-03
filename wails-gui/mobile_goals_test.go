package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

// mobileTestGoalBridge is a bridge over a real receipt store and a real Purpose
// Map in an activity store, with the goal routes' clock fixed at now.
func mobileTestGoalBridge(t *testing.T) (*mobileBridge, *PurposeMapStore, time.Time) {
	t.Helper()
	bridge, _ := mobileTestChatBridge(t)
	activity, err := NewActivityStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activity.Close() })
	bridge.app.activity = &activityMonitorState{store: activity}
	now := time.Now()
	previous := mobileGoalsNow
	mobileGoalsNow = func() time.Time { return now }
	t.Cleanup(func() { mobileGoalsNow = previous })
	return bridge, NewPurposeMapStore(activity.db), now
}

func mobileTestGoalDevice(scopes ...string) mobileDeviceRecord {
	if scopes == nil {
		scopes = []string{mobileScopeGoalsRead, mobileScopeGoalsWrite}
	}
	return mobileTestDevice(scopes...)
}

type mobileTestGoalAnswer struct {
	Goal MobileGoalView `json:"goal"`
}

func mobileTestGoalCall(t *testing.T, bridge *mobileBridge, device mobileDeviceRecord, method, path, body string) (int, string) {
	t.Helper()
	recorder := mobileTestChatCall(bridge, device, method, path, body)
	return recorder.Code, recorder.Body.String()
}

func mobileTestDecodeGoal(t *testing.T, body string) MobileGoalView {
	t.Helper()
	var reply struct {
		Data mobileTestGoalAnswer `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return reply.Data.Goal
}

func mobileTestDecodeGoalPage(t *testing.T, body string) MobileGoalPage {
	t.Helper()
	var reply struct {
		OK   bool           `json:"ok"`
		Data MobileGoalPage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil || !reply.OK {
		t.Fatalf("decode page %s: %v", body, err)
	}
	return reply.Data
}

// mobileTestPurpose adds one Purpose straight to the ledger. noticed makes it
// Isoform's (two independent observations), otherwise the user's.
func mobileTestPurpose(t *testing.T, store *PurposeMapStore, key, kind, title string, noticed bool, edit func(*CreatePurposeOpenLoopInput)) PurposeOpenLoopDetail {
	t.Helper()
	at := time.Now().Add(-time.Minute).UnixMilli()
	input, err := userPurposeCreateInput(userPurposeCreate{Key: "test:" + key, Origin: "test", Kind: kind, Text: title}, time.UnixMilli(at))
	if err != nil {
		t.Fatal(err)
	}
	if noticed {
		first := input.Evidence[0]
		first.EvidenceClass, first.IndependenceKey = PurposeEvidenceEvaluatedProjection, "memory:a"
		second := first
		second.SourceID, second.IndependenceKey = first.SourceID+":b", "memory:b"
		input.Evidence = []PurposeEvidenceInput{first, second}
	}
	if edit != nil {
		edit(&input)
	}
	result, _, err := store.Create(input)
	if err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
	detail, err := store.GetDetail(result.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestMobileGoalRoutesNeedTheirScopes(t *testing.T) {
	bridge, _, _ := mobileTestGoalBridge(t)
	readOnly := mobileTestGoalDevice(mobileScopeGoalsRead)
	cases := []struct {
		name   string
		device mobileDeviceRecord
		method string
		path   string
		body   string
		want   string
	}{
		{"listing needs goals:read", mobileTestGoalDevice(mobileScopeBriefRead), http.MethodGet, "/goals", "", "goals:read"},
		{"a goal needs goals:read", mobileTestGoalDevice(mobileScopeBriefRead), http.MethodGet, "/goals/g-1", "", "goals:read"},
		{"adding needs goals:write", readOnly, http.MethodPost, "/goals", `{"idempotencyKey":"k1","kind":"goal","text":"Ship it"}`, "goals:write"},
		{"checking off needs goals:write", readOnly, http.MethodPost, "/goals/g-1/events", `{"idempotencyKey":"k2","action":"resolve"}`, "goals:write"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := mobileTestGoalCall(t, bridge, tc.device, tc.method, tc.path, tc.body)
			if code != http.StatusForbidden || !strings.Contains(body, tc.want) {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
}

func TestMobileGoalsAddCheckOffAndReopenReplayOnARealStore(t *testing.T) {
	bridge, store, _ := mobileTestGoalBridge(t)
	device := mobileTestGoalDevice()

	for _, bad := range []string{
		`{"idempotencyKey":"b1","kind":"wish","text":"Ship it"}`,
		`{"idempotencyKey":"b2","kind":"goal","text":"   "}`,
		`{"kind":"goal","text":"Ship it"}`,
	} {
		if code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals", bad); code != http.StatusBadRequest {
			t.Fatalf("%s answered %d %s", bad, code, body)
		}
	}

	create := `{"idempotencyKey":"add-1","kind":"commitment","text":"  Renew the passport   before November  "}`
	code, first := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals", create)
	_, again := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals", create)
	if code != http.StatusCreated || again != first {
		t.Fatalf("add replay: %d %s / %s", code, first, again)
	}
	goal := mobileTestDecodeGoal(t, first)
	if goal.Title != "Renew the passport before November" || goal.Kind != PurposeKindCommitment ||
		goal.State != PurposeStateActive || goal.Group != mobileGoalGroupTracking || goal.Status != "You added it today." || !goal.AddedByYou {
		t.Fatalf("added goal = %+v", goal)
	}
	if details, _ := store.ListDetailsAll(); len(details) != 1 {
		t.Fatalf("the replay added %d goals", len(details))
	}

	path := "/goals/" + goal.ID + "/events"
	stale := `{"idempotencyKey":"stale-1","action":"resolve","expectedEventId":"evt-old"}`
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, stale); code != http.StatusConflict || !strings.Contains(body, "this goal changed on your computer; refresh and try again") {
		t.Fatalf("a stale check-off answered %d %s", code, body)
	}
	resolve := `{"idempotencyKey":"done-1","action":"resolve","expectedEventId":"` + goal.EventID + `"}`
	code, done := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, resolve)
	_, doneAgain := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, resolve)
	if code != http.StatusOK || doneAgain != done {
		t.Fatalf("check-off replay: %d %s / %s", code, done, doneAgain)
	}
	checked := mobileTestDecodeGoal(t, done)
	if checked.State != PurposeStateResolved || checked.Group != mobileGoalGroupDone || checked.Status != "Done today." {
		t.Fatalf("checked goal = %+v", checked)
	}
	detail, err := store.GetDetail(goal.ID)
	if err != nil || len(detail.Events) != 2 {
		t.Fatalf("the replay wrote %d events: %v", len(detail.Events), err)
	}

	reopen := `{"idempotencyKey":"open-1","action":"reopen","expectedEventId":"` + checked.EventID + `"}`
	code, opened := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, reopen)
	if reopened := mobileTestDecodeGoal(t, opened); code != http.StatusOK || reopened.State != PurposeStateActive || reopened.Status != "Reopened today." {
		t.Fatalf("reopen: %d %s", code, opened)
	}
	reopenedGoal := mobileTestDecodeGoal(t, opened)
	waiting := `{"idempotencyKey":"wait-1","action":"waiting","expectedEventId":"` + reopenedGoal.EventID + `","waitingOn":"Ana's signature"}`
	code, parked := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, waiting)
	if goal := mobileTestDecodeGoal(t, parked); code != http.StatusOK || goal.State != PurposeStateWaiting || goal.Status != "Waiting on Ana's signature since today." {
		t.Fatalf("waiting: %d %s", code, parked)
	}

	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals/missing/events", `{"idempotencyKey":"x-1","action":"resolve"}`); code != http.StatusNotFound || !strings.Contains(body, "this goal is no longer on your computer") {
		t.Fatalf("an unknown goal answered %d %s", code, body)
	}
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, `{"idempotencyKey":"x-2","action":"snooze"}`); code != http.StatusBadRequest {
		t.Fatalf("snooze answered %d %s", code, body)
	}
}

func TestMobileGoalsListOrdersPagesAndWritesStatusLines(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	today := now.Format("2006-01-02")
	later := now.AddDate(0, 0, 5).Format("2006-01-02")
	slipping := mobileTestPurpose(t, store, "slip", PurposeKindCommitment, "Send the contract draft", false, func(in *CreatePurposeOpenLoopInput) {
		in.DueKind, in.DueValue = PurposeDueDate, today
	})
	dated := mobileTestPurpose(t, store, "dated", PurposeKindGoal, "Book the venue", false, func(in *CreatePurposeOpenLoopInput) {
		in.DueKind, in.DueValue = PurposeDueDate, later
	})
	waitingBase := mobileTestPurpose(t, store, "wait", PurposeKindCommitment, "Get the lease signed", false, func(in *CreatePurposeOpenLoopInput) {
		in.Counterparty = "Ana"
	})
	if _, err := bridge.app.moveUserPurpose(userPurposeTransition{Key: "test:wait-event", Origin: "test", OpenLoopID: waitingBase.OpenLoop.ID,
		ExpectedEventID: waitingBase.Current.ID, Action: userPurposeWaiting, WaitingOn: "her signature"}, now); err != nil {
		t.Fatal(err)
	}
	noticed := mobileTestPurpose(t, store, "noticed", PurposeKindResponsibility, "Keep the release notes current", true, nil)
	candidate := mobileTestPurpose(t, store, "candidate", PurposeKindConcern, "Maybe the budget is short", true, func(in *CreatePurposeOpenLoopInput) {
		in.Evidence = in.Evidence[:1]
	})
	doneBase := mobileTestPurpose(t, store, "done", PurposeKindGoal, "Book the dentist", false, nil)
	if _, err := bridge.app.moveUserPurpose(userPurposeTransition{Key: "test:done-event", Origin: "test", OpenLoopID: doneBase.OpenLoop.ID,
		ExpectedEventID: doneBase.Current.ID, Action: userPurposeResolve}, now); err != nil {
		t.Fatal(err)
	}
	if candidate.Current.LifecycleState != PurposeStateCandidate {
		t.Fatalf("the candidate fixture is %s", candidate.Current.LifecycleState)
	}

	device := mobileTestGoalDevice(mobileScopeGoalsRead)
	var all []MobileGoalView
	cursor := ""
	pages := 0
	for {
		path := "/goals?limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		code, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, path, "")
		if code != http.StatusOK {
			t.Fatalf("page %d: %d %s", pages, code, body)
		}
		page := mobileTestDecodeGoalPage(t, body)
		if page.Counts.Tracking != 4 || page.Counts.Done != 1 {
			t.Fatalf("counts = %+v", page.Counts)
		}
		all = append(all, page.Goals...)
		pages++
		if !page.HasMore {
			break
		}
		cursor = page.Cursor
	}
	if pages != 3 {
		t.Fatalf("five goals in pages of two took %d pages", pages)
	}
	got := []string{}
	for _, goal := range all {
		got = append(got, goal.Title+" | "+goal.Status)
	}
	want := []string{
		"Send the contract draft | Due today, and nothing has moved on it yet. You added it today.",
		"Book the venue | Due on " + now.AddDate(0, 0, 5).Format("Jan 2") + ". You added it today.",
		"Get the lease signed | Waiting on Ana since today: her signature.",
		"Keep the release notes current | Isoform noticed it today.",
		"Book the dentist | Done today.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("goals:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !all[0].Slipping || all[0].ID != slipping.OpenLoop.ID || all[1].ID != dated.OpenLoop.ID || all[1].DueAt == "" || all[3].ID != noticed.OpenLoop.ID || all[3].AddedByYou {
		t.Fatalf("goal fields = %+v", all)
	}

	code, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals/"+noticed.OpenLoop.ID, "")
	if code != http.StatusOK || !strings.Contains(body, `"what":"Added"`) {
		t.Fatalf("detail: %d %s", code, body)
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals/"+candidate.OpenLoop.ID, ""); code != http.StatusNotFound {
		t.Fatalf("a candidate reached the phone: %d", code)
	}
	for _, bad := range []string{"/goals?limit=0", "/goals?limit=101", "/goals?cursor=not-a-cursor"} {
		if code, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, bad, ""); code != http.StatusBadRequest {
			t.Fatalf("%s answered %d %s", bad, code, body)
		}
	}
}

func TestMobileGoalsHoldBackGoalsThatRepeatAMailSubject(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	if _, err := bridge.app.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{{
		ID: "inbox:m-1", Mailbox: storage.MailboxInbox, SenderEmail: "dana@example.com", Subject: "Re: Contract renewal for Acme",
		ThreadKey: "t-1", At: now.Add(-time.Hour).UnixMilli(),
	}}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	echo := mobileTestPurpose(t, store, "echo", PurposeKindCommitment, "Answer Dana about the contract renewal for Acme", true, nil)
	mobileTestPurpose(t, store, "plain", PurposeKindGoal, "Run three times a week", true, nil)

	device := mobileTestGoalDevice()
	code, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals", "")
	page := mobileTestDecodeGoalPage(t, body)
	if code != http.StatusOK || len(page.Goals) != 1 || page.Goals[0].Title != "Run three times a week" || page.Counts.OnMacOnly != 1 {
		t.Fatalf("a goal repeating a mail subject reached the phone: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "contract renewal") {
		t.Fatalf("the subject left the Mac: %s", body)
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals/"+echo.OpenLoop.ID, ""); code != http.StatusNotFound {
		t.Fatalf("the held goal opened: %d", code)
	}

	// The user's own sentence from the phone is theirs to see, whatever it says.
	code, typed := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals",
		`{"idempotencyKey":"mine","kind":"commitment","text":"Sign the contract renewal for Acme"}`)
	if code != http.StatusCreated {
		t.Fatalf("add: %d %s", code, typed)
	}
	_, body = mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals", "")
	if page := mobileTestDecodeGoalPage(t, body); len(page.Goals) != 2 || page.Counts.OnMacOnly != 1 {
		t.Fatalf("the goal typed on the phone was held: %s", body)
	}
}

func TestMobileGoalEventsHoldBackGoalsThatRepeatAMailSubject(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	if _, err := bridge.app.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{{
		ID: "inbox:m-1", Mailbox: storage.MailboxInbox, SenderEmail: "dana@example.com", Subject: "Re: Contract renewal for Acme",
		ThreadKey: "t-1", At: now.Add(-time.Hour).UnixMilli(),
	}}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	echo := mobileTestPurpose(t, store, "echo", PurposeKindCommitment, "Answer Dana about the contract renewal for Acme", true, nil)
	device := mobileTestGoalDevice()
	before := mobileGoalView(echo, now)

	// A held goal cannot be moved from the phone, and its text never comes back.
	path := "/goals/" + echo.OpenLoop.ID + "/events"
	for _, action := range []string{"resolve", "waiting", "reopen"} {
		code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, `{"idempotencyKey":"held-`+action+`","action":"`+action+`"}`)
		if code != http.StatusNotFound {
			t.Fatalf("%s on a held goal answered %d %s", action, code, body)
		}
		if strings.Contains(strings.ToLower(body), "contract renewal") {
			t.Fatalf("the subject left the Mac: %s", body)
		}
	}
	after, err := bridge.app.userPurposeDetail(echo.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view := mobileGoalView(after, now); view.EventID != before.EventID || view.State != before.State || view.Group != before.Group {
		t.Fatalf("the held goal moved: %+v -> %+v", before, view)
	}
}

func TestMobileGoalsFailClosedWhenMailCannotBeRead(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	plain := mobileTestPurpose(t, store, "plain", PurposeKindGoal, "Run three times a week", true, nil)
	device := mobileTestGoalDevice()

	// The user's own sentence is never held, so it still moves without mail.
	code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals",
		`{"idempotencyKey":"mine","kind":"commitment","text":"Sign the contract renewal for Acme"}`)
	if code != http.StatusCreated {
		t.Fatalf("add: %d %s", code, body)
	}
	mine := mobileTestDecodeGoal(t, body)

	previous := mobileGoalMailRows
	mobileGoalMailRows = func(*App) ([]storage.MailMessageRow, error) { return nil, errors.New("database is locked") }
	t.Cleanup(func() { mobileGoalMailRows = previous })

	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("the list was served without the mail check: %d %s", code, body)
	}
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals/"+plain.OpenLoop.ID, ""); code != http.StatusServiceUnavailable {
		t.Fatalf("the goal was served without the mail check: %d %s", code, body)
	}
	before := mobileGoalView(plain, now)
	code, body = mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals/"+plain.OpenLoop.ID+"/events", `{"idempotencyKey":"nomail-1","action":"resolve"}`)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("the goal moved without the mail check: %d %s", code, body)
	}
	after, err := bridge.app.userPurposeDetail(plain.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view := mobileGoalView(after, now); view.EventID != before.EventID || view.Group != before.Group {
		t.Fatalf("the goal moved: %+v -> %+v", before, view)
	}
	code, body = mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals/"+mine.ID+"/events", `{"idempotencyKey":"mine-1","action":"resolve","expectedEventId":"`+mine.EventID+`"}`)
	if code != http.StatusOK || mobileTestDecodeGoal(t, body).Group != mobileGoalGroupDone {
		t.Fatalf("the phone-typed goal did not move: %d %s", code, body)
	}

	// Once the mail reads again the same key is still fresh: the 503 kept it.
	mobileGoalMailRows = previous
	code, body = mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals/"+plain.OpenLoop.ID+"/events", `{"idempotencyKey":"nomail-1","action":"resolve"}`)
	if code != http.StatusOK || mobileTestDecodeGoal(t, body).Group != mobileGoalGroupDone {
		t.Fatalf("the retry under the same key did not move the goal: %d %s", code, body)
	}
}

func TestMobileGoalWriteFailedKeepsThePhoneKeyUnlessItIsAConflict(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{errUserPurposeChanged, http.StatusConflict},
		{errUserPurposeState, http.StatusConflict},
		{errUserPurposeStore, http.StatusServiceUnavailable},
		{errors.New("database is locked"), http.StatusServiceUnavailable},
	} {
		recorder := httptest.NewRecorder()
		mobileGoalWriteFailed(recorder, tc.err)
		if recorder.Code != tc.want {
			t.Fatalf("%v answered %d, want %d", tc.err, recorder.Code, tc.want)
		}
	}
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, http.StatusNotFound},
		{errPurposeOpenLoopNotFound, http.StatusNotFound},
		{errUserPurposeStore, http.StatusServiceUnavailable},
		{errors.New("database is locked"), http.StatusServiceUnavailable},
	} {
		recorder := httptest.NewRecorder()
		mobileGoalMissing(recorder, tc.err)
		if recorder.Code != tc.want {
			t.Fatalf("missing with %v answered %d, want %d", tc.err, recorder.Code, tc.want)
		}
	}
}

func TestMobileGoalEventsAnswer503OnATransientReadError(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	plain := mobileTestPurpose(t, store, "plain", PurposeKindGoal, "Run three times a week", true, nil)
	device := mobileTestGoalDevice()
	before := mobileGoalView(plain, now)

	previous := mobileGoalDetail
	mobileGoalDetail = func(*App, string) (PurposeOpenLoopDetail, error) {
		return PurposeOpenLoopDetail{}, errors.New("database is locked")
	}
	t.Cleanup(func() { mobileGoalDetail = previous })
	path := "/goals/" + plain.OpenLoop.ID + "/events"
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, `{"idempotencyKey":"read-1","action":"resolve"}`); code != http.StatusServiceUnavailable {
		t.Fatalf("a failed read answered %d %s, want 503 so the phone keeps its key", code, body)
	}
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals/"+plain.OpenLoop.ID, ""); code != http.StatusServiceUnavailable {
		t.Fatalf("a failed read answered %d %s on the detail route", code, body)
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals/"+plain.OpenLoop.ID+"x", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("the seam did not apply: %d", code)
	}
	mobileGoalDetail = previous
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/goals/missing-id-0000", ""); code != http.StatusNotFound {
		t.Fatalf("an unknown goal answered %d %s, want 404", code, body)
	}
	after, err := bridge.app.userPurposeDetail(plain.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view := mobileGoalView(after, now); view.EventID != before.EventID {
		t.Fatalf("the goal moved during the failed read: %+v", view)
	}
	// The same key is still fresh once the Mac reads again.
	code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, `{"idempotencyKey":"read-1","action":"resolve"}`)
	if code != http.StatusOK || mobileTestDecodeGoal(t, body).Group != mobileGoalGroupDone {
		t.Fatalf("the retry did not move the goal: %d %s", code, body)
	}
}

func TestMobileGoalRetryOfACommittedMoveAnswersTheMove(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	plain := mobileTestPurpose(t, store, "plain", PurposeKindGoal, "Run three times a week", true, nil)
	device := mobileTestGoalDevice()
	stale := plain.Current.ID

	// The Mac committed the move but its answer was lost after the write
	// (the read-back failed, so no receipt was stored): the phone retries
	// under the same key with the event id it last saw.
	key, valid := mobileKey(device.DeviceID, "retry-1")
	if !valid {
		t.Fatal("key")
	}
	appendInput, err := userPurposeAppendInput(plain, userPurposeTransition{
		Key: key, Origin: "mobile:" + device.DeviceID, OpenLoopID: plain.OpenLoop.ID, Action: userPurposeResolve,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AppendEvent(appendInput); err != nil {
		t.Fatal(err)
	}
	moved, err := store.GetDetail(plain.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}

	code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals/"+plain.OpenLoop.ID+"/events",
		`{"idempotencyKey":"retry-1","action":"resolve","expectedEventId":"`+stale+`"}`)
	if code != http.StatusOK {
		t.Fatalf("the retry of a committed move answered %d %s, want 200", code, body)
	}
	if goal := mobileTestDecodeGoal(t, body); goal.Group != mobileGoalGroupDone || goal.EventID != moved.Current.ID {
		t.Fatalf("retry answered %+v, want the committed move %s", goal, moved.Current.ID)
	}
	again, err := store.GetDetail(plain.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Events) != len(moved.Events) {
		t.Fatalf("the retry appended a second event: %d -> %d", len(moved.Events), len(again.Events))
	}
	// A different key with the stale event id is still a conflict.
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/goals/"+plain.OpenLoop.ID+"/events",
		`{"idempotencyKey":"other-1","action":"reopen","expectedEventId":"`+stale+`"}`); code != http.StatusConflict {
		t.Fatalf("a stale move under a new key answered %d %s, want 409", code, body)
	}
}

func TestMobileGoalStatusLines(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{now, "today"},
		{now.AddDate(0, 0, -1), "yesterday"},
		{now.AddDate(0, 0, -3), "3 days ago"},
		{time.Date(2026, 10, 2, 9, 0, 0, 0, time.Local), "on Oct 2"},
	} {
		if got := mobileGoalDay(tc.at, now); got != tc.want {
			t.Fatalf("%v: %q, want %q", tc.at, got, tc.want)
		}
	}
	if got := mobileGoalDueText(now.AddDate(0, 0, -2), false, now); got != "Was due 2 days ago." {
		t.Fatalf("overdue: %q", got)
	}
	if got := mobileGoalDueText(now.Add(3*time.Hour), true, now); got != "Due today, and nothing has moved on it yet." {
		t.Fatalf("slipping: %q", got)
	}
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{now, "since today"},
		{now.AddDate(0, 0, -1), "since yesterday"},
		{time.Date(2026, 10, 13, 9, 0, 0, 0, time.Local), "since Tuesday"},
		{time.Date(2026, 10, 2, 9, 0, 0, 0, time.Local), "since Oct 2"},
	} {
		if got := mobileGoalSince(tc.at, now); got != tc.want {
			t.Fatalf("%v: %q, want %q", tc.at, got, tc.want)
		}
	}
	for _, tc := range []struct {
		event PurposeOpenLoopEventRecord
		want  string
	}{
		{PurposeOpenLoopEventRecord{WaitingCondition: "Fidelity to send the forms."}, "Waiting on Fidelity to send the forms since today."},
		{PurposeOpenLoopEventRecord{Counterparty: "Ana", WaitingCondition: "Ana"}, "Waiting on Ana since today."},
		{PurposeOpenLoopEventRecord{}, "Waiting since today."},
	} {
		if got := mobileGoalWaitingText(tc.event, now, now); got != tc.want {
			t.Fatalf("waiting %+v: %q, want %q", tc.event, got, tc.want)
		}
	}
	outcome := PurposeOpenLoopDetail{Current: PurposeOpenLoopEventRecord{
		LifecycleState: PurposeStateActive, EventType: PurposeEventRevised, Title: "Run a half marathon",
		DesiredOutcome: "Finish the city half in under two hours", OccurredAt: now.AddDate(0, 0, -3).UnixMilli(),
	}}
	if got := mobileGoalStatus(outcome, MobileGoalView{}, now); got != "Last updated 3 days ago. The aim: Finish the city half in under two hours." {
		t.Fatalf("outcome: %q", got)
	}
}
