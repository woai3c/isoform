package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

// "Talk about this brief" did nothing (2026-10-21): once the user marked a goal
// the brief cites Done or Forgotten, every read left the goal out but kept the
// brief's old conversation key, so the click's own check of that key failed
// and Isoform never spoke.
func TestTalkAboutThisBriefAfterAGoalInItIsClosed(t *testing.T) {
	rig, svc, brief := cardRig(t)
	app := rig.app
	app.activeTags, app.eventBroadcaster = map[string]bool{}, NewEventBroadcaster()
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 4)}
	app.runner, app.tagCancels, app.sessionTempDirs = runner, map[string]context.CancelFunc{}, map[string]string{}
	app.streams, app.runIDByTag, app.pendingTraceIDs = map[string]context.CancelFunc{}, map[string]string{}, map[string]string{}
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := NewPurposeMapStore(rig.activity.db)
	goal := mobileTestPurpose(t, store, "beta", PurposeKindGoal, "Launch the beta", false, nil)
	// The brief speaks of the goal in its own paragraph.
	stored, ok := svc.store.load(brief.BriefID)
	if !ok || len(stored.Narrative) == 0 {
		t.Fatalf("the fixture brief has no text: %+v", stored.Narrative)
	}
	stored.Evidence = append(stored.Evidence, MobileBriefRef{Ref: "goal-ref", SourceKind: "purpose", SourceID: goal.OpenLoop.ID, Label: "Launch the beta"})
	stored.Narrative = append(stored.Narrative, MobileBriefPara{Text: "The beta launch has not moved this week.", Refs: []string{"goal-ref"}})
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.MoveGoal(GoalMoveInput{GoalID: goal.OpenLoop.ID, Action: userPurposeResolve, Key: "done-beta"}); err != nil {
		t.Fatal(err)
	}

	shown, ok := app.todayChatDeliveredBrief(brief.BriefID)
	if !ok || shown.ConversationKey != mobileBriefConversationKey(shown) {
		t.Fatalf("the brief as read carries a stale conversation key: %q, want %q", shown.ConversationKey, mobileBriefConversationKey(shown))
	}
	for _, paragraph := range shown.Narrative {
		if strings.Contains(paragraph.Text, "beta") {
			t.Fatalf("the brief still speaks of the closed goal: %+v", shown.Narrative)
		}
	}
	opener, ok := mobileBriefFindCard(shown, mobileBriefOpenerCardID)
	if !ok || opener.Message == "" {
		t.Fatalf("the brief offers no opener: %+v", shown.Cards)
	}

	opened, err := app.OpenTodayBriefConversation(brief.BriefID)
	if err != nil || opened.Ref.SourceKind != "brief" || opened.Ref.SourceID != shown.ConversationKey {
		t.Fatalf("Talk about this brief = %+v, %v", opened, err)
	}
	page, err := app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 50)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("main chat = %+v, %v", page, err)
	}
	if last := page.Items[len(page.Items)-1]; last.Kind != "assistant" || last.Content != opener.Message {
		t.Fatalf("Isoform did not speak first: %+v", last)
	}

	// The user's answer goes out with the brief pinned to it.
	ref := opened.Ref
	ref.GeneratedTitle, ref.GeneratedFingerprint = opened.Draft.Prefill.Title, opened.Draft.Fingerprint
	answer := "Yes, start there."
	if checked, err := app.PrepareTodayChatTurn(main.LocalID, answer, ref); err != nil || checked != answer {
		t.Fatalf("the answer with the brief = %q, %v", checked, err)
	}
	if _, err := app.SendPromptWithTodayContext(answer, "", "m", "", main.LocalID, nil, nil, false, false, "en", "", StreamItemData{ID: "user-answer", Kind: "user"}, ref); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case input := <-runner.inputs:
		prompt, _ := input.PromptFunc()
		if !strings.HasSuffix(prompt, answer) || strings.Count(prompt, "<today-item-context>") != 1 ||
			!strings.Contains(prompt, `"kind":"brief"`) || !strings.Contains(prompt, "Release memo") {
			t.Fatalf("the answer does not carry the brief: %q", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answer did not start")
	}
	waitForTagIdle(t, app, main.LocalID)
}

func TestAskAboutTodayBriefParagraphQuotesBodyOnce(t *testing.T) {
	rig, svc, brief := cardRig(t)
	app := rig.app
	stored, ok := svc.store.load(brief.BriefID)
	if !ok || len(stored.Narrative) == 0 {
		t.Fatalf("brief has no narrative: %+v", stored.Narrative)
	}
	paragraph := -1
	for index := range stored.Narrative {
		if index < len(stored.Narrative)-1 || !strings.ContainsAny(stored.Narrative[index].Text, "?？") {
			paragraph = index
			break
		}
	}
	if paragraph < 0 {
		t.Fatal("brief has no body paragraph")
	}
	stored.Narrative[paragraph].Text = "The release is ready.\nThe review is next."
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	view, err := app.GetLatestBrief()
	if err != nil || view.Brief == nil {
		t.Fatalf("view = %+v, %v", view, err)
	}
	opened, err := app.OpenTodayBriefParagraph(brief.BriefID, paragraph, stored.Narrative[paragraph].Text)
	if err != nil || opened.Ref.SourceKind != "brief" || opened.Ref.BriefID != brief.BriefID {
		t.Fatalf("opened = %+v, %v", opened, err)
	}
	if _, err := app.OpenTodayBriefParagraph(brief.BriefID, paragraph, stored.Narrative[paragraph].Text); err != nil {
		t.Fatal(err)
	}
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	page, err := app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := "About this part of your brief:\n\n> The release is ready.\n> The review is next."
	said := 0
	for _, row := range page.Items {
		if row.Kind == "assistant" && row.Content == want {
			said++
		}
	}
	if said != 1 {
		t.Fatalf("quoted paragraph was posted %d times: %+v", said, page.Items)
	}
	if _, err := app.OpenTodayBriefParagraph(brief.BriefID, len(view.Brief.Narrative), "missing paragraph"); err != errTodayChatItemUnavailable {
		t.Fatalf("out-of-range paragraph error = %v", err)
	}
	stored.Narrative[len(stored.Narrative)-1].Text = "Would you like to discuss this?"
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenTodayBriefParagraph(brief.BriefID, len(stored.Narrative)-1, stored.Narrative[len(stored.Narrative)-1].Text); err != errTodayChatItemUnavailable {
		t.Fatalf("closing paragraph error = %v", err)
	}
	stored.Narrative = []MobileBriefPara{
		{Text: "The first body paragraph is ready.", Refs: append([]string(nil), stored.Narrative[paragraph].Refs...)},
		{Text: "Should this middle paragraph be discussed?", Refs: append([]string(nil), stored.Narrative[paragraph].Refs...)},
		{Text: "The final body paragraph has no question.", Refs: append([]string(nil), stored.Narrative[paragraph].Refs...)},
	}
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenTodayBriefParagraph(brief.BriefID, 1, stored.Narrative[1].Text); err != nil {
		t.Fatalf("middle body paragraph with a question mark was rejected: %v", err)
	}
	page, err = app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 100)
	if err != nil {
		t.Fatal(err)
	}
	want = "About this part of your brief:\n\n> Should this middle paragraph be discussed?"
	said = 0
	for _, row := range page.Items {
		if row.Kind == "assistant" && row.Content == want {
			said++
		}
	}
	if said != 1 {
		t.Fatalf("middle body paragraph was quoted %d times: %+v", said, page.Items)
	}
	app.closedEyes.enabled.Store(true)
	if _, err := app.OpenTodayBriefParagraph(brief.BriefID, paragraph, stored.Narrative[paragraph].Text); err != errTodayChatObservationPaused {
		t.Fatalf("closed-eyes paragraph error = %v", err)
	}
}

func TestPrepareTodayBriefParagraphDoesNotPostAndRejectsChangedText(t *testing.T) {
	rig, svc, brief := cardRig(t)
	stored, ok := svc.store.load(brief.BriefID)
	if !ok || len(stored.Narrative) == 0 {
		t.Fatal("missing brief narrative")
	}
	stored.Narrative = []MobileBriefPara{{Text: "The release is ready.", Refs: stored.Narrative[0].Refs}}
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	main, err := rig.app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before, err := rig.app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 100)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := rig.app.PrepareTodayBriefParagraph(brief.BriefID, 0, stored.Narrative[0].Text)
	if err != nil || ref.SourceKind != "brief" || ref.BriefID != brief.BriefID || ref.SourceID == "" || ref.ParagraphDigest != mobileBriefModelDigest("The release is ready.") {
		t.Fatalf("paragraph reference = %+v, %v", ref, err)
	}
	after, err := rig.app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 100)
	if err != nil || len(before.Items) != len(after.Items) {
		t.Fatalf("preparing posted a message: before=%d after=%d err=%v", len(before.Items), len(after.Items), err)
	}
	if _, err := rig.app.PrepareTodayBriefParagraph(brief.BriefID, 0, "A changed paragraph"); err != errTodayChatItemUnavailable {
		t.Fatalf("changed text accepted: %v", err)
	}
}

func TestPreparedParagraphQuestionCarriesItsSelectedTextAndRechecksSource(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	refs := brief.Narrative[0].Refs
	brief.Narrative = []MobileBriefPara{{Text: "The first part of the launch.", Refs: refs}, {Text: "The second part needs review.", Refs: refs}}
	bindConversationBriefFixture(t, app, &brief)
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, time.Now()); err != nil {
		t.Fatal(err)
	}
	ref, err := app.PrepareTodayBriefParagraph(brief.BriefID, 1, "The second part needs review.")
	if err != nil {
		t.Fatal(err)
	}
	context, err := app.buildTodayChatContext(t.Context(), ref)
	if err != nil || !strings.Contains(context.Text, "The selected paragraph is: The second part needs review.") {
		t.Fatalf("selected paragraph missing: %q %v", context.Text, err)
	}
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitTodayChatTurn(t.Context(), main.LocalID, storage.StreamItemRow{ID: "paragraph-user", Kind: "user", Content: "What needs review?"}, ref, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := app.store.BindAnswerChipUserRun(t.Context(), main.LocalID, "paragraph-user", "paragraph-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "paragraph-answer", Kind: "assistant", RunID: "paragraph-run", Content: "Review the second part."}); err != nil {
		t.Fatal(err)
	}
	app.savePanelAskAnswer(main.LocalID, "paragraph-user", "paragraph-run", "paragraph-answer", "Review the second part.")
	answer, err := app.GetTodayPanelAnswer(ref)
	if err != nil || answer == nil || answer.Answer != "Review the second part." {
		t.Fatalf("saved paragraph answer: %+v %v", answer, err)
	}
	if err := app.DeleteMemoryEntry("launch"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.buildTodayChatContext(t.Context(), ref); err == nil {
		t.Fatal("withdrawn paragraph remained usable")
	}
	answer, err = app.GetTodayPanelAnswer(ref)
	if err != nil || answer != nil {
		t.Fatalf("withdrawn paragraph exposed saved answer: %+v %v", answer, err)
	}
}

func TestAskAboutTodayBriefParagraphFindsTextAfterDisplayedIndexShift(t *testing.T) {
	rig, svc, brief := cardRig(t)
	app := rig.app
	stored, ok := svc.store.load(brief.BriefID)
	if !ok || len(stored.Narrative) == 0 {
		t.Fatalf("brief has no narrative: %+v", stored.Narrative)
	}
	refs := append([]string(nil), stored.Narrative[0].Refs...)
	if len(refs) == 0 {
		t.Fatal("brief has no paragraph refs")
	}
	goText := "The visible paragraph is ready.\nThe second line remains."
	stored.Narrative = []MobileBriefPara{
		{Text: "A paragraph removed from the display.", Refs: refs},
		{Text: goText, Refs: refs},
		{Text: "Would you like to discuss this?", Refs: refs},
	}
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenTodayBriefParagraph(brief.BriefID, 0, "  "+goText+"  "); err != nil {
		t.Fatalf("shifted displayed index could not open matching text: %v", err)
	}
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	page, err := app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := "About this part of your brief:\n\n> The visible paragraph is ready.\n> The second line remains."
	said := 0
	for _, row := range page.Items {
		if row.Kind == "assistant" && row.Content == want {
			said++
		}
	}
	if said != 1 {
		t.Fatalf("the matching Go paragraph was quoted %d times: %+v", said, page.Items)
	}
	if _, err := app.OpenTodayBriefParagraph(brief.BriefID, 0, "This text exists nowhere."); err != errTodayChatItemUnavailable {
		t.Fatalf("unmatched paragraph text error = %v", err)
	}
}

// The conversation needs the brief's own sources, not the topic navigation
// links on its paragraphs. It collects those sources once for opener and draft.
func TestBriefConversationSkipsTopicNavigationAndReusesSources(t *testing.T) {
	rig, _, brief := cardRig(t)
	app := rig.app
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	calls := 0
	app.todayChatCollectionObserver = func() { calls++ }
	app.topicSource = &chatTopicFake{topic: ChatTopic{ID: "unrelated"}, onKeys: func() { t.Fatal("validated unrelated topic") }}
	if _, err := app.topicsSvc().store.update(func(state *topicsStateFile) {
		state.ChatTopics = []topicChatSnapshot{{Topic: ChatTopic{ID: "unrelated"}}}
	}); err != nil {
		t.Fatal(err)
	}
	opened, err := app.OpenTodayBriefConversation(brief.BriefID)
	if err != nil || opened.Ref.SourceID == "" {
		t.Fatalf("open = %+v, %v", opened, err)
	}
	if calls != 1 {
		t.Fatalf("collected sources %d times, want one", calls)
	}
}

func TestBriefConversationCancelledBeforePosting(t *testing.T) {
	rig, _, brief := cardRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rig.app.openTodayBriefConversation(ctx, brief.BriefID); err == nil {
		t.Fatal("cancelled open succeeded")
	}
	main, err := rig.app.store.LoadMainSession(context.Background())
	if err == nil {
		page, err := rig.app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 10)
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("cancelled open posted: %+v, %v", page.Items, err)
		}
	}
}

func TestBriefOpenerDoesNotPostAfterDeadlineWhileWaitingForLock(t *testing.T) {
	rig, _, _ := cardRig(t)
	main, err := rig.app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	mainChatPostMu.Lock()
	done := make(chan error, 1)
	go func() { done <- rig.app.postTodayBriefOpener(ctx, main.LocalID, "late opener", "test") }()
	cancel()
	mainChatPostMu.Unlock()
	if err := <-done; err == nil {
		t.Fatal("cancelled opener succeeded")
	}
	page, err := rig.app.store.LoadStreamItemsLatest(context.Background(), main.LocalID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Content == "late opener" {
			t.Fatal("opener posted after cancellation")
		}
	}
}
