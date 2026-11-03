package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func peopleTestContact(t *testing.T, a *App, name string, aliases ...string) PrincipalDTO {
	t.Helper()
	p, err := a.CreateContact(name, "colleague", aliases, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func peopleTestMoment(t *testing.T, a *App, p PrincipalDTO, kind string, at time.Time) {
	t.Helper()
	params := `{"subject":"never expose this mail subject","name":"name"}`
	expires := at.Add(24 * time.Hour).UnixMilli()
	if kind == "waiting_on_reply" {
		expires = at.Add(mailWindowDays * 24 * time.Hour).UnixMilli()
	}
	_, err := a.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{Kind: kind, SubjectID: p.ID, DedupeKey: kind + p.ID + at.String(), State: storage.EngagementStatePending, Params: params, CreatedAt: at.Add(time.Hour).UnixMilli(), ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
}
func peopleTestPurpose(t *testing.T, a *App, key, counterparty, owner string, due time.Time) string {
	t.Helper()
	input, err := userPurposeCreateInput(userPurposeCreate{Key: key, Text: key, Kind: PurposeKindCommitment, Origin: "people-test"}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	input.Counterparty = counterparty
	input.Owner = owner
	if !due.IsZero() {
		input.DueKind = PurposeDueDate
		input.DueValue = due.Format(time.RFC3339)
	}
	store, err := a.purposeProductStore()
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	return result.OpenLoop.ID
}

func TestPeopleOverviewLoopsOrderingExactAliasAndCompleteDirectory(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true")
	now := time.Now().Truncate(time.Second)
	oldReply := peopleTestContact(t, a, "Old Reply")
	newReply := peopleTestContact(t, a, "New Reply")
	early := peopleTestContact(t, a, "Mei Chen", "  MEI  ")
	late := peopleTestContact(t, a, "Late")
	waiting := peopleTestContact(t, a, "Waiting")
	again := peopleTestContact(t, a, "Again")
	partial := peopleTestContact(t, a, "Mei Chen Jr")
	dormant := peopleTestContact(t, a, "No sightings")
	peopleTestMoment(t, a, oldReply, "waiting_on_reply", now.Add(-48*time.Hour))
	peopleTestMoment(t, a, newReply, "waiting_on_reply", now.Add(-26*time.Hour))
	peopleTestMoment(t, a, again, "person_reappeared", now.Add(-time.Hour))
	peopleTestPurpose(t, a, "early commitment", " mei ", PurposeOwnerUser, now.Add(time.Hour))
	peopleTestPurpose(t, a, "late commitment", late.DisplayName, PurposeOwnerUser, now.Add(2*time.Hour))
	peopleTestPurpose(t, a, "their commitment", waiting.DisplayName, PurposeOwnerOther, time.Time{})
	peopleTestPurpose(t, a, "partial must not match", "Mei C", PurposeOwnerUser, time.Time{})
	peopleTestPurpose(t, a, "shared owner is excluded", early.DisplayName, PurposeOwnerShared, time.Time{})
	if err := a.store.BumpPrincipalSighting(t.Context(), partial.ID, "wechat", "", now.UnixMilli(), "secret text"); err != nil {
		t.Fatal(err)
	}
	got, err := a.ListPeopleOverview()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range got.Open {
		ids = append(ids, p.ID)
	}
	want := []string{oldReply.ID, newReply.ID, early.ID, late.ID, waiting.ID, again.ID}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("open order=%v want=%v; %+v", ids, want, got)
	}
	if got.Headline != (PeopleHeadline{RepliesOwed: 2, ThingsOwed: 2, PeopleWaitingOnYou: 2}) {
		t.Fatal(got.Headline)
	}
	if got.Open[0].Loops[0].Since != now.Add(-48*time.Hour).UnixMilli() {
		t.Fatal("reply since is detection time instead of message time")
	}
	if got.Open[2].Loops[0].Title != "early commitment" || got.Open[2].Loops[0].DueAt != now.Add(time.Hour).UnixMilli() {
		t.Fatal(got.Open[2])
	}
	if len(got.Everyone) != 2 || got.Everyone[0].ID != partial.ID || got.Everyone[1].ID != dormant.ID {
		t.Fatal(got.Everyone)
	}
	page, err := a.ListPrincipalsWithStats()
	if err != nil {
		t.Fatal(err)
	}
	union := map[string]bool{}
	for _, p := range append(got.Open, got.Everyone...) {
		if union[p.ID] {
			t.Fatal("duplicate in union")
		}
		union[p.ID] = true
	}
	if len(union) != len(page) || got.KnownCount != len(page) {
		t.Fatal("directory set changed")
	}
	for _, p := range page {
		if !union[p.Principal.ID] {
			t.Fatal("person dropped", p)
		}
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "never expose") || strings.Contains(string(raw), "secret text") {
		t.Fatal(string(raw))
	}
}

func TestPeopleOverviewDuplicateHardSignalsAndUndo(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	var profiles []PrincipalDTO
	for i := 0; i < 6; i++ {
		p := peopleTestContact(t, a, fmt.Sprintf("Mei %d", i))
		profiles = append(profiles, p)
		if _, err := a.store.BindAppHandle(t.Context(), p.ID, "wechat", "wxid-real-account", fmt.Sprintf("display-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := a.store.BumpPrincipalSighting(t.Context(), profiles[3].ID, "wechat", "", int64(i+1), ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.store.SetUIState(t.Context(), "sensitive_apps", `{"private":"Private App"}`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := a.store.BumpPrincipalSighting(t.Context(), profiles[0].ID, "private", "", int64(i+1), ""); err != nil {
			t.Fatal(err)
		}
	}
	emailA := peopleTestContact(t, a, "Email A")
	emailB := peopleTestContact(t, a, "Email B")
	a.store.BindAppHandle(t.Context(), emailA.ID, "email", "MEI@example.test", "Mei")
	a.store.BindAppHandle(t.Context(), emailB.ID, "email", "mei@example.test", "M Chen")
	peopleTestContact(t, a, "Same Name")
	peopleTestContact(t, a, "Same Name")
	a.EnsureSelfProfile("Self", nil)
	got, err := a.ListPeopleOverview()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Duplicates) != 2 {
		t.Fatalf("groups=%+v", got.Duplicates)
	}
	var wx DuplicateGroup
	for _, group := range got.Duplicates {
		if group.Reason == "same_handle" {
			wx = group
		}
		if group.Reason == "same_email" && len(group.MemberIDs) != 2 {
			t.Fatal(group)
		}
	}
	if len(wx.MemberIDs) != 6 || wx.SuggestedTargetID != profiles[3].ID || wx.App != "wechat" {
		t.Fatal(wx)
	}
	// A contact-linked profile wins even over a higher message count.
	rows, err := a.ListPrincipalsWithStats()
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		if rows[i].Principal.ID == profiles[0].ID {
			rows[i].Principal.ContactLinked = true
		}
	}
	groups := a.peopleDuplicateGroups(t.Context(), rows, nil)
	for _, group := range groups {
		if group.Reason == "same_handle" && group.SuggestedTargetID != profiles[0].ID {
			t.Fatal(group)
		}
	}
	sources := []string{}
	for _, id := range wx.MemberIDs {
		if id != wx.SuggestedTargetID {
			sources = append(sources, id)
		}
	}
	merge, err := a.MergePeople(wx.SuggestedTargetID, sources)
	if err != nil {
		t.Fatal(err)
	}
	if merge.MergeID == "" || merge.Target.ID != wx.SuggestedTargetID || merge.Target.Name == "" {
		t.Fatal(merge)
	}
	got, err = a.ListPeopleOverview()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Duplicates) != 1 || got.KnownCount != 6 {
		t.Fatal(got)
	}
	if err := a.UndoMerge(merge.MergeID); err != nil {
		t.Fatal(err)
	}
	got, err = a.ListPeopleOverview()
	if err != nil || len(got.Duplicates) != 2 || got.KnownCount != 11 {
		t.Fatalf("undo directory=%+v %v", got, err)
	}
}

func TestPeopleMergeKeepsSourceLoopsOnTargetAndRestoresOwnership(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true")
	target := peopleTestContact(t, a, "Target")
	source := peopleTestContact(t, a, "Source")
	peopleTestMoment(t, a, source, "waiting_on_reply", time.Now().Add(-48*time.Hour))
	peopleTestPurpose(t, a, "source commitment", source.DisplayName, PurposeOwnerUser, time.Time{})
	result, err := a.MergePeople(target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := a.GetPersonDetail(target.ID)
	if err != nil || len(detail.Loops) != 2 {
		t.Fatalf("merged loops=%+v %v", detail, err)
	}
	if err := a.UndoMerge(result.MergeID); err != nil {
		t.Fatal(err)
	}
	detail, err = a.GetPersonDetail(source.ID)
	if err != nil || len(detail.Loops) != 2 {
		t.Fatalf("restored loops=%+v %v", detail, err)
	}
	detail, err = a.GetPersonDetail(target.ID)
	if err != nil || len(detail.Loops) != 0 {
		t.Fatalf("target loops=%+v %v", detail, err)
	}
}

func peopleTestActivity(t *testing.T, a *App, p PrincipalDTO, id, app, title, status string, at int64) {
	t.Helper()
	names, _ := json.Marshal([]string{app})
	session := ActivitySessionRow{ID: id, AppNames: string(names), StartTime: at, EndTime: at + 1000, TriageStatus: status}
	if err := a.activity.store.SaveSession(session, nil, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("run-"+id, app, at, at+1000, 1000, 1)
	run.FirstEventRowID, run.LastEventRowID = at, at
	run.SessionIDs = []string{id}
	insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
	appTimes, _ := json.Marshal(map[string]int64{app: 1000})
	if err := a.activity.store.InsertActivity(ActivityRow{ID: id, Title: title, RunIDs: fmt.Sprintf(`["%s"]`, run.ID), ActiveMsByApp: string(appTimes), StartMs: at, EndMs: at + 1000}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, app, id, at, "message text never returned"); err != nil {
		t.Fatal(err)
	}
}

func TestPersonDetailTogetherPrivacyAndClosedEyes(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	before := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(before) })
	pii.SetUserSensitiveApps(nil)
	p := peopleTestContact(t, a, "Mei", "M Chen")
	p.Notes = "stored user note"
	if _, err := a.UpdatePrincipal(p); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SavePrincipalResearch(t.Context(), storage.PrincipalResearchRow{PrincipalID: p.ID, Summary: "stored research summary"}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UnixMilli()
	peopleTestActivity(t, a, p, "allowed", "Code", "Readable work", "done", at)
	peopleTestActivity(t, a, p, "protected", "Code", "private protected title", "protected", at+2000)
	peopleTestActivity(t, a, p, "sensitive", "Secret App", "private sensitive title", "done", at+4000)
	peopleTestActivity(t, a, p, "mail", "Mail", "private mail subject", "done", at+6000)
	peopleTestActivity(t, a, p, "private-window", "Code", "Private - Incognito", "done", at+8000)
	pii.SetUserSensitiveApps(map[string]string{"com.example.secret": "Secret App"})
	got, err := a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Notes != p.Notes || got.ResearchSummary != "stored research summary" {
		t.Fatal(got)
	}
	if len(got.Together) != 1 || got.Together[0].Kind != "activity" || got.Together[0].Title != "Readable work" || got.Together[0].DurationMs != 1000 {
		t.Fatalf("together=%+v", got.Together)
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"private protected", "private sensitive", "private mail", "message text", "Incognito"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal(string(raw))
		}
	}
	a.closedEyes.enabled.Store(true)
	got, err = a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Together) != 0 || len(got.SharedTopics) != 0 || len(got.Loops) != 0 || got.Name != p.DisplayName || got.Notes != p.Notes || got.ResearchSummary == "" {
		t.Fatal(got)
	}
}

func TestPersonDetailLatestMessageMetadataMeetingsAndLimit(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	p := peopleTestContact(t, a, "Mei")
	at := time.Now().Add(-time.Hour).UnixMilli()
	a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true")
	for i := 0; i < 12; i++ {
		event := storage.CalendarEventRow{ID: fmt.Sprint(i), Source: "eventkit", Provider: "eventkit", Title: fmt.Sprintf("Meeting %02d", i), StartAt: at + int64(i*1000), EndAt: at + int64(i*1000) + 500, Attendees: []storage.CalendarAttendeeRow{{PrincipalID: p.ID, Email: "mei@example.test"}}}
		if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, at); err != nil {
			t.Fatal(err)
		}
	}
	for i, app := range []string{"wechat", "wechat", "telegram"} {
		if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, app, "", at+int64(i), "message-body-secret"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Together) != 10 || got.Together[0].Title != "Meeting 11" || got.MessageCount != 3 {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(got.SeenIn, []string{"telegram", "wechat"}) {
		t.Fatal(got.SeenIn)
	}
	a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "false")
	got, err = a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Together) != 2 {
		t.Fatal(got)
	}
	for _, row := range got.Together {
		if row.Kind != "message" || row.Title != "" {
			t.Fatal(row)
		}
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "message-body-secret") {
		t.Fatal(string(raw))
	}
}

type fakePeopleTopics []peopleWorkTopic

func (f fakePeopleTopics) ReadableWorkTopics(context.Context) ([]peopleWorkTopic, error) {
	return f, nil
}
func TestPersonSharedTopicsExactPersonKeysAndClosedEyes(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	dto := peopleTestContact(t, a, "Mei Chen", "M Chen")
	p, err := a.store.GetPrincipal(t.Context(), dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	registry := fakePeopleTopics{{ID: "one", Name: "Release", Keys: []string{"person:mei chen"}}, {ID: "two", Name: "Planning", Keys: []string{"person:m chen", "person:mei chen"}}, {ID: "no", Name: "Partial", Keys: []string{"person:mei", "repo:mei chen"}}}
	got, err := a.personSharedTopics(t.Context(), p, registry, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("topics=%+v %v", got, err)
	}
	a.closedEyes.enabled.Store(true)
	got, err = a.personSharedTopics(t.Context(), p, registry, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("paused topics=%+v %v", got, err)
	}
}

func TestPeopleLoopsPrivacySourceWithdrawalAndExpiry(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true")
	p := peopleTestContact(t, a, "Mei")
	now := time.Now()
	peopleTestMoment(t, a, p, "waiting_on_reply", now.Add(-48*time.Hour))
	peopleTestMoment(t, a, p, "waiting_on_reply", now.Add(-8*24*time.Hour))
	peopleTestPurpose(t, a, "readable explicit commitment", p.DisplayName, PurposeOwnerUser, time.Time{})
	got, err := a.ListPeopleOverview()
	if err != nil || got.Headline.RepliesOwed != 1 || got.Headline.ThingsOwed != 1 {
		t.Fatalf("before=%+v %v", got, err)
	}
	a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "false")
	got, err = a.ListPeopleOverview()
	if err != nil || got.Headline.RepliesOwed != 0 || got.Headline.ThingsOwed != 1 {
		t.Fatalf("mail off=%+v %v", got, err)
	}
	a.closedEyes.enabled.Store(true)
	got, err = a.ListPeopleOverview()
	if err != nil || len(got.Open) != 0 || len(got.Everyone) != 1 {
		t.Fatalf("closed eyes=%+v %v", got, err)
	}
	a.closedEyes.enabled.Store(false)
	a.saveObservationPauses([]observationPause{{Start: now.Add(-time.Hour).UnixMilli()}})
	got, err = a.ListPeopleOverview()
	if err != nil || len(got.Open) != 0 {
		t.Fatalf("paused=%+v %v", got, err)
	}
}

func TestPeopleOldMergeBindingShapeAndSenderCache(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	target := peopleTestContact(t, a, "Target")
	source := peopleTestContact(t, a, "Source")
	a.store.BindAppHandle(t.Context(), source.ID, "wechat", "account", "Source Handle")
	labeler := newSenderLabeler(a.store)
	labeler.Label(t.Context(), "wechat", "Source Handle", "", "", 100)
	result, err := a.MergePrincipals(source.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	baseline, _ := json.Marshal(principalToDTO(storage.PrincipalRow{}))
	var actualKeys, oldKeys map[string]any
	json.Unmarshal(raw, &actualKeys)
	json.Unmarshal(baseline, &oldKeys)
	if result.MergeID == "" {
		t.Fatal("legacy merge lost Undo receipt")
	}
	delete(actualKeys, "mergeId")
	delete(actualKeys, "appHandles")
	delete(oldKeys, "appHandles")
	actualNames := []string{}
	expectedNames := []string{}
	for key := range actualKeys {
		actualNames = append(actualNames, key)
	}
	for key := range oldKeys {
		expectedNames = append(expectedNames, key)
	}
	sort.Strings(actualNames)
	sort.Strings(expectedNames)
	if !reflect.DeepEqual(actualNames, expectedNames) || result.ID != target.ID || len(result.AppHandles) != 1 {
		t.Fatal(string(raw))
	}
	label := labeler.Label(t.Context(), "wechat", "Source Handle", "", "", 200)
	if strings.Contains(label, "Source") || !strings.Contains(label, "Target") {
		t.Fatalf("stale sender label=%s", label)
	}
}

// A producer regression would break reply ages even for old stored moments.
func TestPeopleWaitingMomentExpiryUsesMessageTime(t *testing.T) {
	at := time.Now().Add(-48 * time.Hour)
	now := time.Now()
	m := mailWaitingMoment(mailWaitingThread{PrincipalID: "p", At: at}, "Name", now)
	if m.Kind != engagement.KindWaitingOnReply || m.ExpiresAt.Add(-mailWindowDays*24*time.Hour) != at {
		t.Fatal(m)
	}
}

func TestPeopleLoopsUsePurposeEvidenceGates(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	p := peopleTestContact(t, a, "Mei")
	now := time.Now().Add(-time.Hour)
	for i, status := range []string{"done", "protected"} {
		id := "session-" + status
		if err := a.activity.store.SaveSession(ActivitySessionRow{ID: id, StartTime: now.UnixMilli(), EndTime: now.Add(time.Minute).UnixMilli(), AppNames: `["Code"]`, TriageStatus: status}, nil, nil); err != nil {
			t.Fatal(err)
		}
		input, err := userPurposeCreateInput(userPurposeCreate{Key: fmt.Sprint(i), Text: "commitment-" + status, Kind: PurposeKindCommitment, Origin: "people-gate"}, now)
		if err != nil {
			t.Fatal(err)
		}
		input.Counterparty = p.DisplayName
		input.Evidence[0].SourceKind = "activity_session"
		input.Evidence[0].SourceID = id
		store, err := a.purposeProductStore()
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Create(input); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.ListPeopleOverview()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Open) != 1 || len(got.Open[0].Loops) != 1 || got.Open[0].Loops[0].Title != "commitment-done" {
		t.Fatal(got)
	}
	if err := a.activity.store.UpdateTriageStatus("session-done", "protected"); err != nil {
		t.Fatal(err)
	}
	got, err = a.ListPeopleOverview()
	if err != nil || len(got.Open) != 0 {
		t.Fatalf("withdrawn Purpose=%+v %v", got, err)
	}
}

func TestPeoplePersistedSensitiveAppGates(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	p := peopleTestContact(t, a, "Mei")
	now := time.Now()
	before := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(before) })
	pii.SetUserSensitiveApps(nil)
	a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true")
	peopleTestMoment(t, a, p, "waiting_on_reply", now.Add(-48*time.Hour))
	a.store.BumpPrincipalSighting(t.Context(), p.ID, "email", "", now.UnixMilli(), "subject must stay private")
	peopleTestActivity(t, a, p, "observed", "Code", "private persisted code title", "done", now.Add(-time.Hour).UnixMilli())
	if err := a.store.SetUIState(t.Context(), "sensitive_apps", `{"com.apple.mail":"Mail","com.example.code":"Code"}`); err != nil {
		t.Fatal(err)
	}
	got, err := a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Loops) != 0 || len(got.Together) != 0 || len(got.SeenIn) != 0 || got.MessageCount != 0 {
		t.Fatal(got)
	}
}

func TestPeopleReadStampRejectsSourceAndPrivacyWithdrawal(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	for _, change := range []func(){
		func() { a.closedEyes.enabled.Store(true) },
		func() { a.store.SetUIState(t.Context(), "sensitive_apps", `{"com.apple.mail":"Mail"}`) },
		func() { peopleTestContact(t, a, "New principal") },
		func() { writeTodayContextMemory(t, "new-source", "source changed") },
	} {
		stamp, err := a.peopleReadStamp(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		change()
		if err := a.validatePeopleRead(t.Context(), stamp); err != storage.ErrReadRevisionChanged {
			t.Fatalf("publication accepted changed sources: %v", err)
		}
		a.closedEyes.enabled.Store(false)
	}
}

func peopleStatsTestRow(t *testing.T, rows []PrincipalWithStatsDTO, id string) PrincipalWithStatsDTO {
	t.Helper()
	for _, row := range rows {
		if row.Principal.ID == id {
			return row
		}
	}
	t.Fatalf("principal %s missing from stats list", id)
	return PrincipalWithStatsDTO{}
}

func TestPeopleStatsGatesReadableAppUnchanged(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	before := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(before) })
	pii.SetUserSensitiveApps(nil)
	p := peopleTestContact(t, a, "Mei")
	at := time.Now().Add(-time.Hour).UnixMilli()
	for i, app := range []string{"wechat", "wechat", "telegram"} {
		if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, app, "", at+int64(i), "preview-"+app); err != nil {
			t.Fatal(err)
		}
	}
	row := peopleStatsTestRow(t, mustListPrincipalsWithStats(t, a), p.ID)
	if row.Stats.TotalMessages != 3 || len(row.Stats.ByApp) != 2 || row.Stats.FirstSeenAt != at || row.Stats.LastSeenAt != at+2 {
		t.Fatalf("readable stats changed: %+v", row.Stats)
	}
	stats, err := a.GetPrincipalStats(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.RecentMessages) != 3 || stats.RecentMessages[0].Preview != "preview-telegram" {
		t.Fatalf("readable previews changed: %+v", stats.RecentMessages)
	}
}

func TestPeopleStatsGatesSensitiveAppDropsCountsAndPreviews(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	before := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(before) })
	pii.SetUserSensitiveApps(nil)
	p := peopleTestContact(t, a, "Mei")
	at := time.Now().Add(-time.Hour).UnixMilli()
	if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, "wechat", "", at, "readable preview"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, "com.example.secret", "", at+1, "secret preview text"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetUIState(t.Context(), "sensitive_apps", `{"com.example.secret":"Secret"}`); err != nil {
		t.Fatal(err)
	}
	row := peopleStatsTestRow(t, mustListPrincipalsWithStats(t, a), p.ID)
	if row.Stats.TotalMessages != 1 || len(row.Stats.ByApp) != 1 || row.Stats.ByApp[0].App != "wechat" {
		t.Fatalf("sensitive counts kept: %+v", row.Stats)
	}
	stats, err := a.GetPrincipalStats(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.RecentMessages) != 1 || stats.RecentMessages[0].Preview != "readable preview" {
		t.Fatalf("sensitive previews kept: %+v", stats.RecentMessages)
	}
	raw, _ := json.Marshal(stats)
	if strings.Contains(string(raw), "secret preview text") || strings.Contains(string(raw), "com.example.secret") {
		t.Fatalf("sensitive app leaked: %s", raw)
	}
}

func TestPeopleStatsGatesClosedEyesDropsCountsAndPreviews(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	p := peopleTestContact(t, a, "Mei")
	if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, "wechat", "", time.Now().UnixMilli(), "readable preview"); err != nil {
		t.Fatal(err)
	}
	a.closedEyes.enabled.Store(true)
	row := peopleStatsTestRow(t, mustListPrincipalsWithStats(t, a), p.ID)
	if row.Stats.TotalMessages != 0 || len(row.Stats.ByApp) != 0 || row.Stats.LastSeenAt != 0 {
		t.Fatalf("closed eyes stats kept: %+v", row.Stats)
	}
	stats, err := a.GetPrincipalStats(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalMessages != 0 || len(stats.RecentMessages) != 0 {
		t.Fatalf("closed eyes previews kept: %+v", stats)
	}
}

func TestPeopleStatsGatesProtectedSessionZeroesStats(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	p := peopleTestContact(t, a, "Mei")
	peopleTestActivity(t, a, p, "protected-stats", "Code", "Work", "protected", time.Now().Add(-time.Hour).UnixMilli())
	row := peopleStatsTestRow(t, mustListPrincipalsWithStats(t, a), p.ID)
	if row.Stats.TotalMessages != 0 || len(row.Stats.ByApp) != 0 {
		t.Fatalf("protected lineage stats kept: %+v", row.Stats)
	}
	stats, err := a.GetPrincipalStats(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalMessages != 0 || len(stats.RecentMessages) != 0 {
		t.Fatalf("protected lineage previews kept: %+v", stats)
	}
}

func mustListPrincipalsWithStats(t *testing.T, a *App) []PrincipalWithStatsDTO {
	t.Helper()
	rows, err := a.ListPrincipalsWithStats()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPeopleStatsGatesSharedCacheMatchesUncachedWalk(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	before := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(before) })
	pii.SetUserSensitiveApps(nil)
	readable := peopleTestContact(t, a, "Readable")
	protected := peopleTestContact(t, a, "Protected")
	sensitive := peopleTestContact(t, a, "Sensitive")
	at := time.Now().Add(-time.Hour).UnixMilli()
	// One session sighting shared by two people, one protected session, and a
	// sensitive app give the cache distinct verdicts to memoize.
	if err := a.activity.store.SaveSession(ActivitySessionRow{ID: "shared-session", AppNames: `["Code"]`, StartTime: at, EndTime: at + 1000, TriageStatus: "done"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BumpPrincipalSighting(t.Context(), readable.ID, "wechat", "shared-session", at, "readable preview"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BumpPrincipalSighting(t.Context(), protected.ID, "wechat", "shared-session", at, "protected person preview"); err != nil {
		t.Fatal(err)
	}
	peopleTestActivity(t, a, protected, "protected-session", "Code", "Work", "protected", at+1000)
	if err := a.store.BumpPrincipalSighting(t.Context(), sensitive.ID, "wechat", "", at, "readable preview"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BumpPrincipalSighting(t.Context(), sensitive.ID, "com.example.secret", "", at+1, "secret preview text"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetUIState(t.Context(), "sensitive_apps", `{"com.example.secret":"Secret"}`); err != nil {
		t.Fatal(err)
	}
	principals, err := a.store.ListPrincipals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	statsMap, err := a.store.GetAllPrincipalStats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cache := newPeopleReadGateCache()
	for _, p := range principals {
		stats := statsMap[p.ID]
		stats.PrincipalID = p.ID
		want := a.peopleStatsReadable(t.Context(), p, stats, nil)
		got := a.peopleStatsReadable(t.Context(), p, stats, cache)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("cached gate changed the outcome for %s:\nwalk=%+v\ncache=%+v", p.DisplayName, want, got)
		}
	}
	protectedRow, err := a.store.GetPrincipal(t.Context(), protected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.peopleStatsReadable(t.Context(), protectedRow, statsMap[protected.ID], cache); got.TotalMessages != 0 {
		t.Fatalf("cache leaked the protected session's stats: %+v", got)
	}
}

func TestPeopleReadGatesWalkLineageOncePerRead(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	before := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(before) })
	pii.SetUserSensitiveApps(nil)
	people := []PrincipalDTO{}
	for i := 0; i < 3; i++ {
		people = append(people, peopleTestContact(t, a, fmt.Sprintf("Person %d", i)))
	}
	at := time.Now().Add(-time.Hour).UnixMilli()
	for s := 0; s < 2; s++ {
		sessionID := fmt.Sprintf("shared-%d", s)
		if err := a.activity.store.SaveSession(ActivitySessionRow{ID: sessionID, AppNames: `["Code"]`, StartTime: at, EndTime: at + 1000, TriageStatus: "done"}, nil, nil); err != nil {
			t.Fatal(err)
		}
		for _, p := range people {
			if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, "wechat", sessionID, at, "preview"); err != nil {
				t.Fatal(err)
			}
		}
	}
	loads := 0
	a.peopleLineageProbe = func() { loads++ }
	if _, err := a.ListPeopleOverview(); err != nil {
		t.Fatal(err)
	}
	if loads != len(people) {
		t.Fatalf("overview loaded lineage %d times, want once per person (%d)", loads, len(people))
	}
	loads = 0
	if _, err := a.GetPersonDetail(people[0].ID); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("detail loaded lineage %d times, want 1", loads)
	}
	// A new read is a new generation: lineage is walked again, once per person.
	loads = 0
	if _, err := a.ListPeopleOverview(); err != nil {
		t.Fatal(err)
	}
	if loads != len(people) {
		t.Fatalf("second read reused a stale generation: %d", loads)
	}
}
