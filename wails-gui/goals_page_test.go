package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

// Guards for item 66 C1 to C4: the computer's Goals page, edit, and goals the
// user typed never vanish for repeating a mail subject.

func goalsTestMail(t *testing.T, app *App, now time.Time) {
	t.Helper()
	if _, err := app.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{{
		ID: "inbox:m-1", Mailbox: storage.MailboxInbox, SenderEmail: "dana@example.com", Subject: "Re: Contract renewal for Acme",
		ThreadKey: "t-1", At: now.Add(-time.Hour).UnixMilli(),
	}}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func goalsTestFollowUp(t *testing.T, app *App, title string) PurposeOpenLoopDetail {
	t.Helper()
	detail, err := app.createUserPurpose(userPurposeCreate{
		Key: "activity:moment-1:k", Origin: "activity", Kind: PurposeKindCommitment, Text: title,
		EvidenceKind: activityFollowUpSourceKind, EvidenceSourceID: "moment-1",
	}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func goalsTestIDs(items []GoalItemView) []string {
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.Title)
	}
	return ids
}

func TestGoalsPageListsWhatThePhoneListsAndHoldsOnlyWhatIsoformNoticed(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	app := bridge.app
	goalsTestMail(t, app, now)
	mobileTestPurpose(t, store, "typed", PurposeKindCommitment, "Sign the contract renewal for Acme", false, nil)
	mobileTestPurpose(t, store, "noticed", PurposeKindCommitment, "Answer Dana about the contract renewal for Acme", true, nil)
	followUp := goalsTestFollowUp(t, app, "Send Dana the contract renewal for Acme")
	waiting := mobileTestPurpose(t, store, "waiting", PurposeKindGoal, "Renew passport", false, nil)
	if _, err := app.moveUserPurpose(userPurposeTransition{Key: "w", Origin: "test", OpenLoopID: waiting.OpenLoop.ID, Action: userPurposeWaiting, WaitingOn: "Ana"}, now); err != nil {
		t.Fatal(err)
	}
	done := mobileTestPurpose(t, store, "done", PurposeKindGoal, "Book the dentist", false, nil)
	goalTestMove(t, app, done.OpenLoop.ID, "", userPurposeResolve, "done")

	page, err := app.ListGoals()
	if err != nil {
		t.Fatal(err)
	}
	tracking := strings.Join(goalsTestIDs(page.Tracking), "|")
	if !strings.Contains(tracking, "Sign the contract renewal for Acme") || !strings.Contains(tracking, "Renew passport") || len(page.Tracking) != 2 {
		t.Fatalf("tracking = %v; a goal the user typed must never be held", goalsTestIDs(page.Tracking))
	}
	if got := goalsTestIDs(page.Done); len(got) != 1 || got[0] != "Book the dentist" {
		t.Fatalf("done = %v", got)
	}
	held := strings.Join(goalsTestIDs(page.Held), "|")
	if len(page.Held) != 2 || !strings.Contains(held, "Answer Dana") || !strings.Contains(held, "Send Dana") {
		t.Fatalf("held = %v; Isoform's finds and Activity follow-ups keep the mail hold", goalsTestIDs(page.Held))
	}
	for _, item := range append(append([]GoalItemView{}, page.Tracking...), page.Held...) {
		switch item.ID {
		case followUp.OpenLoop.ID:
			if item.Origin != goalOriginActivity {
				t.Fatalf("follow-up origin = %q", item.Origin)
			}
		case waiting.OpenLoop.ID:
			if item.State != PurposeStateWaiting || item.WaitingOn != "Ana" || item.Origin != goalOriginChat {
				t.Fatalf("waiting goal = %+v", item)
			}
		}
	}

	// The phone agrees: the typed goal is listed, the two held ones are counted.
	code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(), http.MethodGet, "/goals", "")
	phone := mobileTestDecodeGoalPage(t, body)
	if code != http.StatusOK || phone.Counts.OnMacOnly != 2 || !strings.Contains(body, "Sign the contract renewal for Acme") {
		t.Fatalf("phone list = %d %s", code, body)
	}
}

func TestEditingAGoalChangesItsTitleAndDateOnly(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	app := bridge.app
	goal := mobileTestPurpose(t, store, "edit", PurposeKindGoal, "Renew pasport", false, nil)
	if _, err := app.moveUserPurpose(userPurposeTransition{Key: "w", Origin: "test", OpenLoopID: goal.OpenLoop.ID, Action: userPurposeWaiting, WaitingOn: "Ana"}, now); err != nil {
		t.Fatal(err)
	}
	edited, err := app.MoveGoal(GoalMoveInput{GoalID: goal.OpenLoop.ID, Action: userPurposeRevise, Key: "e1", Title: "  Renew passport ", DueDate: "2026-11-02", SetDue: true})
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := store.GetDetail(goal.OpenLoop.ID)
	if edited.Title != "Renew passport" || detail.Current.DesiredOutcome != "Renew passport" || detail.Current.DueKind != PurposeDueDate ||
		detail.Current.DueValue != "2026-11-02" || detail.Current.LifecycleState != PurposeStateWaiting || detail.Current.WaitingCondition != "Ana" {
		t.Fatalf("edit = %+v", detail.Current)
	}
	page, _ := app.ListGoals()
	if len(page.Tracking) != 1 || page.Tracking[0].DueDate != "2026-11-02" || page.Tracking[0].Title != "Renew passport" {
		t.Fatalf("page after edit = %+v", page.Tracking)
	}
	if _, err := app.MoveGoal(GoalMoveInput{GoalID: goal.OpenLoop.ID, Action: userPurposeRevise, Key: "e2", Title: "Renew passport", SetDue: true}); err != nil {
		t.Fatal(err)
	}
	if detail, _ = store.GetDetail(goal.OpenLoop.ID); detail.Current.DueKind != PurposeDueNone || detail.Current.DueValue != "" {
		t.Fatalf("clearing the date = %+v", detail.Current)
	}
	for _, bad := range []GoalMoveInput{
		{GoalID: goal.OpenLoop.ID, Action: userPurposeRevise, Key: "e3", Title: "   "},
		{GoalID: goal.OpenLoop.ID, Action: userPurposeRevise, Key: "e4", Title: "Renew passport", DueDate: "next week", SetDue: true},
	} {
		if _, err := app.MoveGoal(bad); !errors.Is(err, errGoalMoveInvalid) {
			t.Fatalf("edit %+v = %v, want goal_invalid", bad, err)
		}
	}
}

func TestAGoalsStoryHasItsHistoryAndSources(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	app := bridge.app
	goal := mobileTestPurpose(t, store, "story", PurposeKindGoal, "Renew passport", false, nil)
	if _, err := app.moveUserPurpose(userPurposeTransition{Key: "w", Origin: "test", OpenLoopID: goal.OpenLoop.ID, Action: userPurposeWaiting, WaitingOn: "Ana"}, now); err != nil {
		t.Fatal(err)
	}
	story, err := app.GetGoal(goal.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(story.History) != 2 || story.History[0].Event != PurposeEventWaiting || story.History[1].Event != PurposeEventDiscovered {
		t.Fatalf("history = %+v, want newest first", story.History)
	}
	if len(story.Sources) != 1 || story.Sources[0].Kind != "you" {
		t.Fatalf("sources = %+v", story.Sources)
	}
	forgot := goalTestMove(t, app, goal.OpenLoop.ID, "", userPurposeForget, "f")
	if _, err := app.GetGoal(forgot.ID); !errors.Is(err, errGoalMoveInvalid) {
		t.Fatalf("a forgotten goal opened: %v", err)
	}
}

func TestAGoalTheUserTypedKeepsItsCardButNotTheWrittenText(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := NewPurposeMapStore(app.activity.store.db)
	now := time.Now()
	seedMobileBriefSubject(t, app, "Re: "+mobileBriefPrivateSubject)
	create := func(key string, noticed bool) PurposeOpenLoopDetail {
		input, err := userPurposeCreateInput(userPurposeCreate{Key: key, Origin: "chat", Kind: PurposeKindGoal, Text: "Sign the " + mobileBriefPrivateSubject}, now.Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if noticed {
			// Two independent observations make it Isoform's supported find.
			first := input.Evidence[0]
			first.EvidenceClass, first.IndependenceKey = PurposeEvidenceEvaluatedProjection, "memory:a"
			second := first
			second.SourceID, second.IndependenceKey = first.SourceID+":b", "memory:b"
			input.Evidence = []PurposeEvidenceInput{first, second}
		}
		created, _, err := store.Create(input)
		if err != nil {
			t.Fatal(err)
		}
		detail, err := store.GetDetail(created.OpenLoop.ID)
		if err != nil {
			t.Fatal(err)
		}
		return detail
	}
	typed := create("chat:typed", false)

	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{typed}, nil)
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.WorkingToward) != 1 || brief.Limits.ExcludedSensitiveItems != 0 {
		t.Fatalf("the typed goal was held from Today: %+v %+v", brief.Sections.WorkingToward, brief.Limits)
	}
	card := brief.Sections.WorkingToward[0]
	brief.NarrativeStatus = "ok"
	brief.Narrative = []MobileBriefPara{{Text: "You still need to sign the " + mobileBriefPrivateSubject + ".", Refs: card.Refs}}
	delivered := app.mobileBriefForDelivery(brief)
	if len(delivered.Sections.WorkingToward) != 1 || delivered.Sections.WorkingToward[0].Title != card.Title {
		t.Fatalf("the typed goal's card left the delivered brief: %+v", delivered.Sections.WorkingToward)
	}
	for _, paragraph := range delivered.Narrative {
		if strings.Contains(strings.ToLower(paragraph.Text), strings.ToLower(mobileBriefPrivateSubject)) {
			t.Fatalf("written text repeating a mail subject was delivered: %+v", delivered.Narrative)
		}
	}

	// A goal Isoform noticed in the same words is still held.
	noticed := create("chat:noticed", true)
	in = mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{noticed}, nil)
	if held := buildMobileBriefV1(in); len(held.Sections.WorkingToward) != 0 || held.Limits.ExcludedSensitiveItems != 1 {
		t.Fatalf("a noticed goal repeating a mail subject reached Today: %+v", held.Sections.WorkingToward)
	}
}

func TestOnlyTheUsersOwnWordsSkipTheMailHold(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	app := bridge.app
	goalsTestMail(t, app, now)
	held := func(detail PurposeOpenLoopDetail) bool {
		t.Helper()
		subjects, err := mobileGoalSubjects(app)
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := store.GetDetail(detail.OpenLoop.ID)
		if err != nil {
			t.Fatal(err)
		}
		return mobileGoalEchoesMail(fresh, mobileGoalView(fresh, now), subjects)
	}
	extraction := func(detail PurposeOpenLoopDetail, key, eventType string, edit func(*AppendPurposeOpenLoopEventInput)) {
		t.Helper()
		current := purposeCurrentLedgerEvent(detail)
		stance := map[string]string{PurposeEventRevised: PurposeStanceUpdate, PurposeEventWaiting: PurposeStanceWait}[eventType]
		// Separate clock reads can straddle a millisecond and postdate the event.
		at := time.Now().UnixMilli()
		input := AppendPurposeOpenLoopEventInput{
			OpenLoopID: detail.OpenLoop.ID, ExpectedEventID: current.ID, IdempotencyKey: "extraction:" + key, EventType: eventType,
			Title: current.Title, DesiredOutcome: current.DesiredOutcome, Owner: current.Owner, DueKind: current.DueKind,
			ReasoningStatus: PurposeReasoningUnknown, Confidence: 0.8, OccurredAt: at,
			Evidence: []PurposeEvidenceInput{{SourceKind: "memory", SourceID: "m-" + key, SourceVersion: "v1", SourceObservedAt: at,
				SourceHash: goalsTestHash(key), EvidenceClass: PurposeEvidenceObservedUser, Stance: stance, IndependenceKey: "memory:" + key, PrivacyDecision: TaskContextPrivacyRedact}},
		}
		edit(&input)
		if _, _, err := store.AppendEvent(input); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}

	// The user's own sentence repeating a subject: not held.
	typed := mobileTestPurpose(t, store, "own-words", PurposeKindGoal, "Sign the contract renewal for Acme", false, nil)
	if held(typed) {
		t.Fatal("the user's own words were held")
	}
	// A waiting condition Isoform's goal finder wrote from mail: held.
	waiting := mobileTestPurpose(t, store, "found-waiting", PurposeKindGoal, "Get the agreement signed", false, nil)
	extraction(waiting, "wait", PurposeEventWaiting, func(input *AppendPurposeOpenLoopEventInput) {
		input.WaitingCondition = "Re: Contract renewal for Acme"
	})
	if !held(waiting) {
		t.Fatal("a waiting condition from mail reached the phone on a typed goal")
	}
	// A title the goal finder revised from mail: held.
	revised := mobileTestPurpose(t, store, "found-title", PurposeKindGoal, "Get the agreement signed", false, nil)
	extraction(revised, "revise", PurposeEventRevised, func(input *AppendPurposeOpenLoopEventInput) {
		input.Title, input.DesiredOutcome = "Answer Dana about the contract renewal for Acme", "Answer Dana about the contract renewal for Acme"
	})
	if !held(revised) {
		t.Fatal("a title the goal finder revised from mail reached the phone")
	}
	// A counterparty from mail: held.
	person := mobileTestPurpose(t, store, "found-person", PurposeKindGoal, "Get the agreement signed", false, nil)
	extraction(person, "person", PurposeEventRevised, func(input *AppendPurposeOpenLoopEventInput) {
		input.Counterparty = "Contract renewal for Acme"
	})
	if !held(person) {
		t.Fatal("a counterparty from mail reached the phone")
	}
	// The user editing the title to words that repeat a subject: theirs, not held.
	edited := mobileTestPurpose(t, store, "user-edit", PurposeKindGoal, "Get the agreement signed", false, nil)
	if _, err := app.MoveGoal(GoalMoveInput{GoalID: edited.OpenLoop.ID, Action: userPurposeRevise, Key: "edit", Title: "Sign the contract renewal for Acme"}); err != nil {
		t.Fatal(err)
	}
	if held(edited) {
		t.Fatal("the user's own edit was held")
	}
	// A check-off carries the goal finder's title over; it does not make it the user's.
	latest, _ := store.GetDetail(edited.OpenLoop.ID)
	extraction(latest, "revise-after-edit", PurposeEventRevised, func(input *AppendPurposeOpenLoopEventInput) {
		input.Title, input.DesiredOutcome = "Answer Dana about the contract renewal for Acme", "Answer Dana about the contract renewal for Acme"
	})
	fresh, _ := store.GetDetail(edited.OpenLoop.ID)
	goalTestMove(t, app, edited.OpenLoop.ID, fresh.Current.ID, userPurposeResolve, "done-after-revise")
	if !held(edited) {
		t.Fatal("a check-off made the goal finder's title the user's own")
	}
}

func TestATypedGoalsWaitingCardStillChecksThePersonsName(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := NewPurposeMapStore(app.activity.store.db)
	now := time.Now()
	seedMobileBriefSubject(t, app, "Re: "+mobileBriefPrivateSubject)
	typed := mobileTestPurpose(t, store, "typed-person", PurposeKindCommitment, "Send the signed lease", false, nil)
	brief := MobileBriefV1{
		NarrativeStatus: "unavailable", Narrative: []MobileBriefPara{},
		Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "purpose", SourceID: typed.OpenLoop.ID, Label: typed.Current.Title, ObservedAt: formatMobileBriefTime(now)}},
		Sections: MobileBriefSections{
			WorkingToward: []MobileBriefItem{{ID: "w1", Title: typed.Current.Title, Kind: PurposeKindCommitment, PurposeID: typed.OpenLoop.ID, Refs: []string{"e1"}}},
			// A person card's title is the counterparty, not the user's words.
			WaitingOnYou: []MobileBriefItem{{ID: "p1", Title: mobileBriefPrivateSubject, Kind: PurposeKindCommitment, PurposeID: typed.OpenLoop.ID, Refs: []string{"e1"}}},
		},
	}
	delivered := app.mobileBriefForDelivery(brief)
	if len(delivered.Sections.WorkingToward) != 1 {
		t.Fatalf("the typed goal's own card left: %+v", delivered.Sections.WorkingToward)
	}
	if len(delivered.Sections.WaitingOnYou) != 0 {
		t.Fatalf("a person card titled with a mail subject was delivered: %+v", delivered.Sections.WaitingOnYou)
	}
}

func TestEditingATitleKeepsAWindowDue(t *testing.T) {
	bridge, store, _ := mobileTestGoalBridge(t)
	goal := mobileTestPurpose(t, store, "window", PurposeKindGoal, "Plan the offsite", false, func(input *CreatePurposeOpenLoopInput) {
		input.DueKind, input.DueValue = PurposeDueWindow, "next quarter"
	})
	if _, err := bridge.app.MoveGoal(GoalMoveInput{GoalID: goal.OpenLoop.ID, Action: userPurposeRevise, Key: "title-only", Title: "Plan the team offsite"}); err != nil {
		t.Fatal(err)
	}
	detail, _ := store.GetDetail(goal.OpenLoop.ID)
	if detail.Current.DueKind != PurposeDueWindow || detail.Current.DueValue != "next quarter" {
		t.Fatalf("a title edit changed the due: %q %q", detail.Current.DueKind, detail.Current.DueValue)
	}
}

func goalsTestHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
