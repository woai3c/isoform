package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/sop"
)

func newKnowHowCandidateTestApp(t *testing.T) (*App, *ActivityStore) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := mainChatTestApp(t)
	store, err := NewActivityStore(filepath.Join(home, "activity"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	app.activity = &activityMonitorState{store: store}
	candidates, err := sop.NewStore(filepath.Join(home, "know-how-candidates"))
	if err != nil {
		t.Fatal(err)
	}
	app.knowHowCandidates = candidates
	return app, store
}

// knowHowTestSession writes one observed session whose focus steps are the
// app's own QA walk: Terminal then Isoform, twice.
func knowHowTestSession(t *testing.T, store *ActivityStore, id string, start time.Time) {
	t.Helper()
	events := []activitymon.ActivityEvent{}
	step := int64(0)
	for _, stepSpec := range [][2]string{
		{"Terminal", "release checklist"},
		{"Isoform", "Isoform — Settings"},
		{"Terminal", "release checklist"},
		{"Isoform", "Isoform — onboarding"},
	} {
		events = append(events, activitymon.ActivityEvent{
			Timestamp:   start.Add(time.Duration(step) * time.Minute).UnixMilli(),
			Kind:        activitymon.EventFocus,
			AppBundleID: "com.example." + strings.ToLower(stepSpec[0]),
			AppName:     stepSpec[0],
			WindowTitle: stepSpec[1],
		})
		step++
	}
	session := ActivitySessionRow{
		ID:        id,
		StartTime: start.UnixMilli(),
		EndTime:   start.Add(40 * time.Minute).UnixMilli(),
		AppNames:  `["Terminal","Isoform"]`,
	}
	if err := store.SaveSession(session, events, nil); err != nil {
		t.Fatal(err)
	}
}

func saveKnowHowCandidateFixture(t *testing.T, app *App, id string, sessionIDs []string) sop.Entry {
	t.Helper()
	entry, err := app.knowHowCandidates.Save(sop.Entry{
		ID:          id,
		Title:       "Fresh-install QA",
		Description: "The same 4 steps, 3 times.",
		Trigger:     "When you open release checklist again.",
		Status:      sop.StatusDraft,
		Source:      sop.SourceObserved,
		Scope:       "mine",
		SourceIDs:   sessionIDs,
		Body:        knowHowCandidateBody([]string{"release checklist", "Isoform — Settings", "release checklist", "Isoform — onboarding"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestKnowHowCandidateProposedAfterThreeSameWaySessions(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -10)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil {
		t.Fatal(err)
	}
	if candidate == nil {
		t.Fatal("three same-way sessions produced no proposal")
	}
	if candidate.Times != 3 || len(candidate.Steps) != 4 || len(candidate.SessionIDs) != 3 || len(candidate.OccurredAtMs) != 3 {
		t.Fatalf("candidate = %+v", candidate)
	}
	if candidate.Scope != "mine" || candidate.AvgDurationMs != 40*60*1000 {
		t.Fatalf("candidate = %+v", candidate)
	}
	if !strings.HasPrefix(candidate.ID, knowHowCandidateIDPrefix) {
		t.Fatalf("candidate id %q leaks source content", candidate.ID)
	}
	// A second read serves the same persisted draft, not a new detection.
	again, err := app.GetKnowHowCandidate()
	if err != nil {
		t.Fatal(err)
	}
	if again == nil || again.ID != candidate.ID {
		t.Fatalf("proposal not stable: %+v then %+v", candidate, again)
	}
	entry, err := app.knowHowCandidates.Get(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != sop.StatusDraft || entry.Source != sop.SourceObserved || entry.Scope != "mine" {
		t.Fatalf("draft = %+v", entry)
	}
}

func TestKnowHowCandidateNeedsThreeSessions(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -5)
	for i := 0; i < 2; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil {
		t.Fatal(err)
	}
	if candidate != nil {
		t.Fatalf("two sessions proposed %+v", candidate)
	}
}

func TestKnowHowCandidateSkipsProtectedAndSensitiveSources(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -6)
	// Two ordinary occurrences, one protected with the same steps: never
	// three readable ones.
	knowHowTestSession(t, store, "a", day)
	knowHowTestSession(t, store, "b", day.AddDate(0, 0, 1))
	protectedStart := day.AddDate(0, 0, 2)
	protectedEvents := []activitymon.ActivityEvent{}
	for i, stepSpec := range [][2]string{
		{"Terminal", "release checklist"},
		{"Isoform", "Isoform — Settings"},
		{"Terminal", "release checklist"},
		{"Isoform", "Isoform — onboarding"},
	} {
		protectedEvents = append(protectedEvents, activitymon.ActivityEvent{
			Timestamp:   protectedStart.Add(time.Duration(i) * time.Minute).UnixMilli(),
			Kind:        activitymon.EventFocus,
			AppBundleID: "com.example." + strings.ToLower(stepSpec[0]),
			AppName:     stepSpec[0],
			WindowTitle: stepSpec[1],
		})
	}
	if err := store.SaveSession(ActivitySessionRow{
		ID: "c", StartTime: protectedStart.UnixMilli(), EndTime: protectedStart.Add(40 * time.Minute).UnixMilli(),
		AppNames: `["Terminal","Isoform"]`, TriageStatus: "protected",
	}, protectedEvents, nil); err != nil {
		t.Fatal(err)
	}
	if candidate, err := app.GetKnowHowCandidate(); err != nil || candidate != nil {
		t.Fatalf("protected session counted: %+v %v", candidate, err)
	}

	// A third ordinary occurrence proposes; protecting an app afterwards
	// withdraws the draft at once.
	knowHowTestSession(t, store, "d", day.AddDate(0, 0, 3))
	app.invalidateKnowHowCandidateCache()
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	previous := pii.GetUserSensitiveApps()
	pii.AddUserSensitiveApp("com.example.terminal", "Terminal")
	defer pii.SetUserSensitiveApps(previous)
	app.withdrawKnowHowCandidatesForProtection()
	if candidate, err := app.GetKnowHowCandidate(); err != nil || candidate != nil {
		t.Fatalf("protected-app draft survived: %+v %v", candidate, err)
	}
	if entries, err := app.knowHowCandidates.List(); err != nil || len(entries) != 0 {
		t.Fatalf("draft bundle kept after protect: %+v %v", entries, err)
	}
}

func TestKnowHowCandidateWithdrawsWhenEvidenceIsForgotten(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -4)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	// Forgetting one of the source sessions withdraws the open proposal.
	if err := store.DeleteSession("s0"); err != nil {
		t.Fatal(err)
	}
	app.invalidateKnowHowCandidateCache()
	if candidate, err := app.GetKnowHowCandidate(); err != nil || candidate != nil {
		t.Fatalf("forgotten-evidence draft survived: %+v %v", candidate, err)
	}
	if _, err := app.knowHowCandidates.Get(candidate.ID); err == nil {
		t.Fatal("withdrawn draft stayed in the store")
	}
}

func TestKnowHowCandidateConfirmWritesKnowledgeWithEvidenceAndScope(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -3)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	result, err := app.ConfirmKnowHowCandidate(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := app.GetKnowledgeEntry(result.KnowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Title != candidate.Title || len(entry.SessionIDs) != 3 {
		t.Fatalf("knowledge entry = %+v", entry)
	}
	doc, ok := parseOKFDocument(entry.Content)
	if !ok {
		t.Fatal("confirmed entry is not OKF")
	}
	if got := okfString(doc.Frontmatter, "source"); got != "observed" {
		t.Fatalf("source = %q", got)
	}
	if got := okfString(doc.Frontmatter, "scope"); got != "mine" {
		t.Fatalf("scope = %q", got)
	}
	if got := okfString(doc.Frontmatter, "trigger"); got == "" {
		t.Fatal("confirmed entry lost when-to-use")
	}
	if strings.Contains(entry.Content, "release checklist") == false {
		t.Fatal("confirmed entry lost the steps")
	}
	// The card grid sees it as a how-to with its sessions and scope.
	cards, err := app.ListKnowHow()
	if err != nil {
		t.Fatal(err)
	}
	var card *KnowHowCard
	for i := range cards {
		if cards[i].ID == result.KnowledgeID {
			card = &cards[i]
		}
	}
	if card == nil || card.Kind != "how_to" || card.Scope != "mine" || card.Source != "observed" || card.SessionCount != 3 || card.Trigger == "" {
		t.Fatalf("card = %+v", card)
	}
	// The lifecycle record is confirmed; the proposal is done.
	draft, err := app.knowHowCandidates.Get(candidate.ID)
	if err != nil || draft.Status != sop.StatusConfirmed {
		t.Fatalf("candidate after confirm = %+v %v", draft, err)
	}
	if next, err := app.GetKnowHowCandidate(); err != nil || next != nil {
		t.Fatalf("confirmed repetition re-proposed: %+v %v", next, err)
	}
}

func TestKnowHowCandidateDeclineNeverReproposes(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -3)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	if err := app.DeclineKnowHowCandidate(candidate.ID); err != nil {
		t.Fatal(err)
	}
	if next, err := app.GetKnowHowCandidate(); err != nil || next != nil {
		t.Fatalf("declined repetition re-proposed: %+v %v", next, err)
	}
	// Undo while it is on screen brings the same proposal back.
	if err := app.UndoDeclineKnowHowCandidate(candidate.ID); err != nil {
		t.Fatal(err)
	}
	back, err := app.GetKnowHowCandidate()
	if err != nil || back == nil || back.ID != candidate.ID {
		t.Fatalf("undo decline = %+v %v", back, err)
	}
}

func TestKnowHowCandidateUndoConfirmRestoresTheDraft(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -3)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	if _, err := app.ConfirmKnowHowCandidate(candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.UndoConfirmKnowHowCandidate(candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetKnowledgeEntry(candidate.ID); err == nil {
		t.Fatal("undo confirm kept the knowledge entry")
	}
	back, err := app.GetKnowHowCandidate()
	if err != nil || back == nil || back.ID != candidate.ID {
		t.Fatalf("undo confirm = %+v %v", back, err)
	}
	entry, err := app.knowHowCandidates.Get(candidate.ID)
	if err != nil || entry.Status != sop.StatusDraft {
		t.Fatalf("candidate after undo = %+v %v", entry, err)
	}
}

func TestKnowHowCandidateEditKeepsTheDraft(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -3)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	saved, err := app.SaveKnowHowCandidate(candidate.ID, "  Fresh-install QA before a release  ", "When a signed build exists.", []string{"Remove the app.", "", "Install the signed build."})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Title != "Fresh-install QA before a release" || len(saved.Steps) != 2 || saved.Steps[1] != "Install the signed build." {
		t.Fatalf("saved = %+v", saved)
	}
	entry, err := app.knowHowCandidates.Get(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != sop.StatusDraft || entry.Trigger != "When a signed build exists." {
		t.Fatalf("edited entry = %+v", entry)
	}
	if _, err := app.SaveKnowHowCandidate(candidate.ID, " ", "", []string{"a step"}); err == nil {
		t.Fatal("empty title saved")
	}
	if _, err := app.SaveKnowHowCandidate(candidate.ID, "title", "", nil); err == nil {
		t.Fatal("empty steps saved")
	}
	// The edit flows through to the confirmed entry.
	if _, err := app.ConfirmKnowHowCandidate(candidate.ID); err != nil {
		t.Fatal(err)
	}
	knowledge, err := app.GetKnowledgeEntry(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if knowledge.Title != "Fresh-install QA before a release" || !strings.Contains(knowledge.Content, "Install the signed build.") || strings.Contains(knowledge.Content, "release checklist") {
		t.Fatalf("confirmed edit = %q", knowledge.Content)
	}
}

func TestKnowHowCandidateBindingsRejectUnknownIDs(t *testing.T) {
	app, _ := newKnowHowCandidateTestApp(t)
	if _, err := app.ConfirmKnowHowCandidate("knowhow-missing"); err == nil {
		t.Fatal("confirmed a missing candidate")
	}
	if err := app.DeclineKnowHowCandidate("knowhow-missing"); err == nil {
		t.Fatal("declined a missing candidate")
	}
	if err := app.UndoConfirmKnowHowCandidate("knowhow-missing"); err == nil {
		t.Fatal("undo-confirm on a missing candidate")
	}
	if _, err := app.SaveKnowHowCandidate("knowhow-missing", "t", "", []string{"s"}); err == nil {
		t.Fatal("edited a missing candidate")
	}
}

func TestKnowHowSessionStepsCollapseRepeatsAndMaskDigits(t *testing.T) {
	events := []activitymon.ActivityEvent{
		{Kind: activitymon.EventFocus, AppName: "Terminal", WindowTitle: "build 12"},
		{Kind: activitymon.EventKeyboard, AppName: "Terminal", WindowTitle: "build 12"},
		{Kind: activitymon.EventFocus, AppName: "Terminal", WindowTitle: "build 12"},
		{Kind: activitymon.EventAppSwitch, AppName: "Isoform", WindowTitle: ""},
		{Kind: activitymon.EventFocus, AppName: "Isoform", WindowTitle: "Isoform"},
		{Kind: activitymon.EventFocus, AppName: "", WindowTitle: "no app"},
	}
	labels, keys, titled := knowHowSessionSteps(events)
	if len(labels) != 2 {
		t.Fatalf("labels = %v", labels)
	}
	if labels[0] != "build 12" || labels[1] != "Isoform" {
		t.Fatalf("labels = %v", labels)
	}
	if !titled[0] || titled[1] {
		t.Fatalf("titled = %v", titled)
	}
	// Digit runs normalize away, so "build 12" and "build 13" share a key.
	if keys[0] != knowHowStepKey("Terminal", "build 99") {
		t.Fatalf("key = %q", keys[0])
	}
	if knowHowSignature(keys) == knowHowSignature(append([]string{}, keys[1:]...)) {
		t.Fatal("signature ignores step order")
	}
}

func TestKnowHowCandidateHidesUnderClosedEyesWithoutWithdrawing(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -3)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	// Closed Eyes hides the proposal but keeps the draft: the sessions are
	// intact, only unreadable right now.
	app.closedEyes.enabled.Store(true)
	app.invalidateKnowHowCandidateCache()
	if hidden, err := app.GetKnowHowCandidate(); err != nil || hidden != nil {
		t.Fatalf("Closed Eyes still shows a proposal: %+v %v", hidden, err)
	}
	if _, err := app.knowHowCandidates.Get(candidate.ID); err != nil {
		t.Fatalf("Closed Eyes withdrew the draft: %v", err)
	}
	if _, err := app.ConfirmKnowHowCandidate(candidate.ID); err == nil {
		t.Fatal("confirm succeeded under Closed Eyes")
	}
	if _, err := app.knowHowCandidates.Get(candidate.ID); err != nil {
		t.Fatalf("a refused confirm withdrew the draft: %v", err)
	}
	app.closedEyes.enabled.Store(false)
	app.invalidateKnowHowCandidateCache()
	back, err := app.GetKnowHowCandidate()
	if err != nil || back == nil || back.ID != candidate.ID {
		t.Fatalf("proposal did not return after Closed Eyes: %+v %v", back, err)
	}
}

func TestKnowHowCandidateStoreStaysOutOfPlaybooks(t *testing.T) {
	app, store := newKnowHowCandidateTestApp(t)
	day := time.Now().AddDate(0, 0, -3)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("s%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	// The playbooks store lists nothing for this proposal.
	sops, err := sop.NewStore(filepath.Join(t.TempDir(), "sops"))
	if err != nil {
		t.Fatal(err)
	}
	app.sopStore = sops
	entries, err := app.sopStore.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.ID == candidate.ID {
			t.Fatal("know-how candidate leaked into Playbooks")
		}
	}
}
