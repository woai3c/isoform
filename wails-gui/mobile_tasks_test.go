package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestMobileTasksGroupsOrdersAndLimitsEntries(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	entries := []handoff.Entry{
		{ID: "active", Title: "Active", Status: handoff.StatusActive, CreatedAt: now.UnixMilli(), UpdatedAt: now.Add(-time.Hour).UnixMilli()},
		{ID: "blocked", Title: "Blocked", Status: handoff.StatusBlocked, BlockedReason: handoff.BlockedInterrupted, UpdatedAt: now.Add(-2 * time.Hour).UnixMilli()},
		{ID: "review", Title: "Review", Status: handoff.StatusNeedsReview, UpdatedAt: now.Add(-3 * time.Hour).UnixMilli()},
		{ID: "running", Title: "Running", Status: handoff.StatusInProgress, UpdatedAt: now.Add(-time.Minute).UnixMilli()},
		{ID: "done-boundary", Title: "Done", Status: handoff.StatusCompleted, UpdatedAt: now.Add(-mobileTasksDoneWindow).UnixMilli()},
		{ID: "done-old", Title: "Old", Status: handoff.StatusCompleted, UpdatedAt: now.Add(-mobileTasksDoneWindow - time.Millisecond).UnixMilli()},
		{ID: "done-future", Title: "Future", Status: handoff.StatusCompleted, UpdatedAt: now.Add(time.Second).UnixMilli()},
		{ID: "stale", Title: "Stale", Status: handoff.StatusStale},
		{ID: "aborted", Title: "Aborted", Status: handoff.StatusAborted},
	}
	page := mobileTaskPage(entries, nil, now)
	if page.Counts != (MobileTaskCounts{NeedsYou: 3, Running: 1, DoneThisWeek: 1}) {
		t.Fatalf("counts = %+v", page.Counts)
	}
	want := []string{"review", "blocked", "active", "running", "done-boundary"}
	got := make([]string, len(page.Tasks))
	for i, task := range page.Tasks {
		got[i] = task.ID
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("task order = %v, want %v", got, want)
	}
	if page.Tasks[1].BlockedReason != handoff.BlockedInterrupted {
		t.Fatalf("blocked reason = %q", page.Tasks[1].BlockedReason)
	}
	if page.Tasks[4].UpdatedAt != now.Add(-mobileTasksDoneWindow).Format(time.RFC3339) {
		t.Fatalf("boundary timestamp = %q", page.Tasks[4].UpdatedAt)
	}
}

func TestMobileTasksCountsAreUncappedAndMailHeldTasksAreDropped(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	entries := make([]handoff.Entry, 0, 61)
	for i := 0; i < 21; i++ {
		entries = append(entries, handoff.Entry{
			ID: "active-" + string(rune('a'+i)), Title: "Ordinary task", Status: handoff.StatusActive,
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute).UnixMilli(),
		})
	}
	for i := 0; i < 21; i++ {
		entries = append(entries, handoff.Entry{
			ID: "running-" + string(rune('a'+i)), Title: "Running task", Status: handoff.StatusInProgress,
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute).UnixMilli(),
		})
	}
	for i := 0; i < 21; i++ {
		entries = append(entries, handoff.Entry{
			ID: "done-" + string(rune('a'+i)), Title: "Completed task", Status: handoff.StatusCompleted,
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute).UnixMilli(),
		})
	}
	entries = append(entries, handoff.Entry{ID: "held", Title: "Quarterly planning notes", Status: handoff.StatusActive})
	page := mobileTaskPage(entries, []string{"quarterly planning notes"}, now)
	if page.Counts != (MobileTaskCounts{NeedsYou: 21, Running: 21, DoneThisWeek: 21}) {
		t.Fatalf("held task was counted or counts capped: %+v", page.Counts)
	}
	if len(page.Tasks) != 60 {
		t.Fatalf("tasks returned = %d, want 60", len(page.Tasks))
	}
	for _, task := range page.Tasks {
		if task.ID == "held" {
			t.Fatal("mail-held task was returned")
		}
	}
}

func TestMobileTasksJSONContainsOnlyThePhoneFields(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	entry := handoff.Entry{
		ID: "task-private", Title: "Prepare the report", Status: handoff.StatusNeedsReview,
		Workdir: "/Users/private/work", KeyFiles: []string{"secret.txt"}, SourceLink: "https://private.invalid",
		ChatSessionID: "private-chat", Body: "private body", BlockedReason: "verification details",
		Verification: &handoff.Verification{Met: 1, Total: 2, Note: "private verification note"},
		CreatedAt:    now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	}
	page := mobileTaskPage([]handoff.Entry{entry}, nil, now)
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)
	for _, private := range []string{entry.Workdir, "secret.txt", entry.SourceLink, entry.ChatSessionID,
		entry.Body, entry.Verification.Note, "verification details"} {
		if strings.Contains(encoded, private) {
			t.Fatalf("private value %q in %s", private, encoded)
		}
	}
	if !strings.Contains(encoded, `"progress":{"done":1,"total":2}`) ||
		strings.Contains(encoded, `"runner"`) || strings.Contains(encoded, `"blockedReason"`) {
		t.Fatalf("unexpected optional fields in %s", encoded)
	}
}

func TestMobileTasksRouteUsesBriefReadAndReturnsUnavailableWithoutStore(t *testing.T) {
	bridge, _, _ := mobileTestGoalBridge(t)
	if code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(), http.MethodGet, "/tasks", ""); code != http.StatusForbidden ||
		!strings.Contains(body, mobileScopeBriefRead) {
		t.Fatalf("route without brief:read answered %d %s", code, body)
	}
	bridge.app.handoffStore = nil
	if code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead), http.MethodGet, "/tasks", ""); code != http.StatusServiceUnavailable ||
		!strings.Contains(body, errMobileStoreUnavailable.Error()) {
		t.Fatalf("route without handoff store answered %d %s", code, body)
	}
}

func TestMobileTaskDetailBuildsBoundedStepsAndProgress(t *testing.T) {
	createdAt := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	entry := handoff.Entry{
		ID: "detail", Title: "Prepare a release", Status: handoff.StatusInProgress,
		CreatedAt: createdAt.UnixMilli(), UpdatedAt: createdAt.UnixMilli(),
		AttemptStartedAt: createdAt.Add(5 * time.Hour).UnixMilli(),
		Criteria:         []string{"First step", "Second step", strings.Repeat("x", 210)},
		Verification:     &handoff.Verification{Met: 1, Total: 3},
	}
	detail, ok := mobileTaskDetail(entry, nil)
	if !ok {
		t.Fatal("expected task detail")
	}
	if detail.StartedAt != "2026-10-26T14:00:00Z" || len(detail.Steps) != 3 {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Steps[0].State != "done" || detail.Steps[1].State != "current" ||
		detail.Steps[2].State != "pending" || len(detail.Steps[2].Text) > 200 {
		t.Fatalf("steps = %+v", detail.Steps)
	}

	entry.Status = handoff.StatusNeedsReview
	entry.Result = &handoff.Result{Criteria: []handoff.ResultCriterion{
		{Met: false}, {Met: true}, {Met: false},
	}}
	detail, ok = mobileTaskDetail(entry, nil)
	if !ok || detail.Steps[0].State != "pending" || detail.Steps[1].State != "done" {
		t.Fatalf("result criteria were not used: %+v", detail.Steps)
	}
}

func TestMobileTaskDetailOmitsStartedAtForAnUnstartedTask(t *testing.T) {
	entry := handoff.Entry{ID: "unstarted", Title: "A safe task", Status: handoff.StatusActive}
	detail, ok := mobileTaskDetail(entry, nil)
	if !ok {
		t.Fatal("expected task detail")
	}
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"startedAt"`) {
		t.Fatalf("unstarted detail leaked startedAt: %s", data)
	}
}

func TestMobileTaskDetailFiltersHeldCriteriaWithoutShiftingResultIndexes(t *testing.T) {
	entry := handoff.Entry{
		ID: "held-criterion", Title: "Prepare a public status update", Status: handoff.StatusNeedsReview,
		Criteria: []string{
			"Confirm the timeline",
			"Discuss the confidential launch proposal",
			"List next steps",
		},
		Result: &handoff.Result{Criteria: []handoff.ResultCriterion{
			{Met: true}, {Met: true}, {Met: false},
		}},
	}
	detail, ok := mobileTaskDetail(entry, []string{"confidential launch proposal"})
	if !ok {
		t.Fatal("expected task detail")
	}
	if len(detail.Steps) != 2 ||
		detail.Steps[0].Text != "Confirm the timeline" || detail.Steps[0].State != "done" ||
		detail.Steps[1].Text != "List next steps" || detail.Steps[1].State != "pending" {
		t.Fatalf("steps = %+v", detail.Steps)
	}
}

func TestMobileTaskDetailCapsCriteriaBeforeFilteringHeldText(t *testing.T) {
	criteria := make([]string, 13)
	for i := range criteria {
		criteria[i] = "A safe criterion"
	}
	criteria[1] = "Confidential launch proposal"
	criteria[12] = "Beyond the twelve-step limit"
	entry := handoff.Entry{ID: "bounded-held-criterion", Title: "A safe task",
		Status: handoff.StatusActive, Criteria: criteria}

	detail, ok := mobileTaskDetail(entry, []string{"confidential launch proposal"})
	if !ok {
		t.Fatal("expected task detail")
	}
	if len(detail.Steps) != 11 {
		t.Fatalf("steps = %d, want 11 after the held criterion is omitted", len(detail.Steps))
	}
	for _, step := range detail.Steps {
		if strings.Contains(step.Text, "Beyond the twelve-step limit") {
			t.Fatalf("criterion outside the original cap was returned: %+v", detail.Steps)
		}
	}
}

func TestMobileTaskDetailUsesPrivacyHoldAndHidesUnavailableTasks(t *testing.T) {
	entry := handoff.Entry{ID: "held-task", Title: "Discuss the private subject",
		Status: handoff.StatusActive, Criteria: []string{"Read the notes"}}
	if _, ok := mobileTaskDetail(entry, []string{"private subject"}); ok {
		t.Fatal("mail-held task was visible")
	}
	for _, status := range []handoff.Status{handoff.StatusStale, handoff.StatusAborted} {
		entry.Status = status
		if _, ok := mobileTaskDetail(entry, nil); ok {
			t.Fatalf("%s task was visible", status)
		}
	}
}

func TestMobileTaskDetailRouteRequiresBriefReadAndReturnsDetail(t *testing.T) {
	bridge, _, _ := mobileTestGoalBridge(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.handoffStore = store
	entry, err := store.Save(handoff.Entry{ID: "detail-route", Title: "Prepare a plan",
		Status: handoff.StatusInProgress, AttemptStartedAt: time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC).UnixMilli(),
		Criteria: []string{"Read the brief"}})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(), http.MethodGet, "/tasks/detail-route", ""); code != http.StatusForbidden ||
		!strings.Contains(body, mobileScopeBriefRead) {
		t.Fatalf("detail without scope answered %d %s", code, body)
	}
	code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead),
		http.MethodGet, "/tasks/detail-route", "")
	if code != http.StatusOK {
		t.Fatalf("detail answered %d %s", code, body)
	}
	var reply struct {
		Data MobileTaskDetail `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Data.ID != entry.ID || reply.Data.StartedAt == "" ||
		len(reply.Data.Steps) != 1 || reply.Data.Steps[0].Text != "Read the brief" {
		t.Fatalf("detail response = %+v", reply.Data)
	}
	if code, _ := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead),
		http.MethodGet, "/tasks/missing", ""); code != http.StatusNotFound {
		t.Fatalf("missing task answered %d", code)
	}
}

func TestMobileTaskDetailRouteHoldsMailSubjectCriteria(t *testing.T) {
	bridge, _, _ := mobileTestGoalBridge(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.handoffStore = store
	const subject = "Confidential launch proposal"
	if _, err := store.Save(handoff.Entry{
		ID: "held-criterion-route", Title: "Prepare a public status update",
		Status:   handoff.StatusActive,
		Criteria: []string{"Confirm the timeline", "Review the CONFIDENTIAL LAUNCH PROPOSAL", "List next steps"},
	}); err != nil {
		t.Fatal(err)
	}
	previous := mobileGoalMailRows
	mobileGoalMailRows = func(*App) ([]storage.MailMessageRow, error) {
		return []storage.MailMessageRow{{Subject: subject}}, nil
	}
	t.Cleanup(func() { mobileGoalMailRows = previous })

	code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead),
		http.MethodGet, "/tasks/held-criterion-route", "")
	if code != http.StatusOK {
		t.Fatalf("detail answered %d %s", code, body)
	}
	if strings.Contains(strings.ToLower(body), strings.ToLower(subject)) {
		t.Fatalf("mail subject was returned in %s", body)
	}
}

func TestMobileTaskDetailAndStopRoutesHideMailHeldTasks(t *testing.T) {
	bridge, _, _ := mobileTestGoalBridge(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.handoffStore = store
	if _, err := store.Save(handoff.Entry{ID: "held-route", Title: "Discuss the confidential launch proposal",
		Status: handoff.StatusActive, Criteria: []string{"Read the brief"}}); err != nil {
		t.Fatal(err)
	}
	previous := mobileGoalMailRows
	mobileGoalMailRows = func(*App) ([]storage.MailMessageRow, error) {
		return []storage.MailMessageRow{{Subject: "Confidential launch proposal"}}, nil
	}
	t.Cleanup(func() { mobileGoalMailRows = previous })

	device := mobileTestGoalDevice(mobileScopeBriefRead, mobileScopeHandoff)
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodGet, "/tasks/held-route", ""); code != http.StatusNotFound {
		t.Fatalf("mail-held detail answered %d", code)
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/held-route/stop",
		`{"idempotencyKey":"held-route-stop"}`); code != http.StatusNotFound {
		t.Fatalf("mail-held Stop answered %d", code)
	}
}

func TestMobileTaskStopRequiresScopeIsIdempotentAndChecksStatus(t *testing.T) {
	if got := mobileRouteFamily(http.MethodPost, "/tasks/stop-active/stop"); got != "POST /tasks/*/stop" {
		t.Fatalf("stop route family = %q", got)
	}
	bridge, _, _ := mobileTestGoalBridge(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.handoffStore = store
	if _, err := store.Save(handoff.Entry{ID: "stop-active", Title: "Prepare a plan",
		Status: handoff.StatusActive, Criteria: []string{"Read the brief"}}); err != nil {
		t.Fatal(err)
	}
	body := `{"idempotencyKey":"stop-active-key"}`
	if code, response := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(mobileScopeBriefRead),
		http.MethodPost, "/tasks/stop-active/stop", body); code != http.StatusForbidden ||
		!strings.Contains(response, mobileScopeHandoff) {
		t.Fatalf("stop without scope answered %d %s", code, response)
	}
	device := mobileTestGoalDevice(mobileScopeHandoff, mobileScopeBriefRead)
	code, response := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/stop-active/stop", body)
	if code != http.StatusOK {
		t.Fatalf("stop answered %d %s", code, response)
	}
	replayCode, replay := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/stop-active/stop", body)
	if replayCode != code || replay != response {
		t.Fatalf("idempotent replay = %d %s, want %d %s", replayCode, replay, code, response)
	}
	stopped, err := store.Get("stop-active")
	if err != nil || stopped.Status != handoff.StatusAborted {
		t.Fatalf("stopped task = %+v, err %v", stopped, err)
	}
	for _, status := range []handoff.Status{
		handoff.StatusInProgress, handoff.StatusBlocked, handoff.StatusNeedsReview,
	} {
		id := "stop-" + string(status)
		if _, err := store.Save(handoff.Entry{ID: id, Title: "Work to stop",
			Status: status, Criteria: []string{"Read the brief"}}); err != nil {
			t.Fatal(err)
		}
		requestBody := `{"idempotencyKey":"` + id + `"}`
		if code, response := mobileTestGoalCall(t, bridge, device, http.MethodPost,
			"/tasks/"+id+"/stop", requestBody); code != http.StatusOK {
			t.Fatalf("stop %s answered %d %s", status, code, response)
		}
	}
	if _, err := store.Save(handoff.Entry{ID: "stop-done", Title: "Completed work",
		Status: handoff.StatusCompleted, Criteria: []string{"Read the brief"}}); err != nil {
		t.Fatal(err)
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/tasks/stop-done/stop",
		`{"idempotencyKey":"stop-done-key"}`); code != http.StatusConflict {
		t.Fatalf("completed task stop answered %d", code)
	}
}
