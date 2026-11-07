package main

import (
	"context"
	"testing"
	"time"
)

func round1TopicBrief(t *testing.T, now time.Time) (*App, string, *mobileBriefService, MobileBriefV1, time.Time) {
	t.Helper()
	a, tag, _ := registryChatTestApp(t)
	goal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "round1-goal", PurposeKindGoal, "Atlas checklist", false, nil)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	brief := buildMobileBriefV1(mobileBriefInput{Slot: "morning", AsOf: now, WindowStart: now.Add(-time.Hour), Location: time.Local, WorkingToward: []mobileBriefCandidate{{SourceKind: "purpose", SourceID: goal.OpenLoop.ID, Title: goal.Current.Title, Kind: "goal", Status: "active", DueAt: now.Add(time.Hour), At: now}}})
	brief.Narrative, brief.NarrativeStatus = mobileBriefNarrativeTemplate(brief, "en"), "unavailable"
	// The closing asks about the only item, which then gets no chip
	// (today-review v1.1.4 #4); keep the paragraph whose chip this covers.
	brief.Narrative = brief.Narrative[:1]
	brief = a.mobileBriefWithCards(a.mobileBriefForDelivery(brief))
	if len(brief.Narrative) == 0 || brief.Narrative[0].TopicID == "" {
		t.Fatal("missing readable topic control")
	}
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	svc.app, svc.store = a, newMobileBriefStore(dir)
	a.mobileBriefSvc = svc
	if err := svc.store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	return a, tag, svc, brief, now
}

func TestBriefConversationKeyIgnoresTopicNavigation(t *testing.T) {
	brief := MobileBriefV1{BriefID: "2026-10-22-morning", Narrative: []MobileBriefPara{{Text: "Atlas release is ready.", Refs: []string{"e1"}}}, WeeklyRecap: []MobileBriefPara{{Text: "Atlas moved.", Refs: []string{"e1"}}}}
	legacy := mobileBriefConversationKey(brief)
	brief.Narrative[0].TopicID, brief.Narrative[0].TopicName = "work:atlas", "Atlas"
	brief.WeeklyRecap[0].TopicID, brief.WeeklyRecap[0].TopicName = "work:atlas", "Atlas"
	if got := mobileBriefConversationKey(brief); got != legacy {
		t.Errorf("topic metadata changed legacy key: %s != %s", got, legacy)
	}
	if brief.Narrative[0].TopicName != "Atlas" || brief.WeeklyRecap[0].TopicName != "Atlas" {
		t.Fatal("hash mutated caller")
	}
	brief.Narrative[0].TopicName = "Renamed Atlas"
	if got := mobileBriefConversationKey(brief); got != legacy {
		t.Error("rename changed key")
	}
	brief.Narrative[0].Text = "Atlas release changed."
	if mobileBriefConversationKey(brief) == legacy {
		t.Error("prose change did not change key")
	}
}

func TestTodayBriefTalkSurvivesTopicRename(t *testing.T) {
	a, tag, _, brief, _ := round1TopicBrief(t, mobileBriefTestDay(8, 0))
	opened, err := a.OpenTodayBriefConversation(brief.BriefID)
	if err != nil {
		t.Fatal(err)
	}
	id := brief.Narrative[0].TopicID
	// Rename only the published topic's display metadata. Its source facts and
	// the written brief stay unchanged, as for a registry naming update.
	if _, err := a.topicsSvc().store.update(func(state *topicsStateFile) {
		for i := range state.ChatTopics {
			if state.ChatTopics[i].Topic.ID == id {
				state.ChatTopics[i].Topic.Name = "Renamed Atlas"
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	current, ok := a.todayChatDeliveredBrief(brief.BriefID)
	if !ok || current.Narrative[0].TopicName != "Renamed Atlas" {
		t.Fatal("rename did not reach read")
	}
	if current.ConversationKey != opened.Ref.SourceID {
		t.Error("rename changed the open conversation key")
	}
	runner := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = runner
	_, err = a.SendPromptWithTodayContext("Let's discuss the release.", "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "round1-talk-send", Kind: "user"}, opened.Ref)
	if err != nil {
		t.Fatalf("open Talk send after rename rejected: %v", err)
	}
	select {
	case <-runner.prompt:
	case <-time.After(5 * time.Second):
		t.Fatal("send did not reach runner")
	}
	waitForTagIdle(t, a, tag)
	row, err := a.store.LoadStreamItem(t.Context(), "round1-talk-send")
	if err != nil || row == nil || row.SessionLocalID != tag {
		t.Fatalf("send left the existing conversation: %+v %v", row, err)
	}
}

func TestBriefRound1SourceCollectionBudget(t *testing.T) {
	t.Run("feed", func(t *testing.T) {
		a, _, svc, brief, now := round1TopicBrief(t, mobileBriefTestDay(8, 0))
		for i := 1; i < 4; i++ {
			brief.BriefID = now.AddDate(0, 0, -i).Format("2006-01-02") + "-morning"
			if err := svc.store.save(brief, now); err != nil {
				t.Fatal(err)
			}
		}
		calls := 0
		a.todayChatCollectionObserver = func() { calls++ }
		if got := len(svc.feedBriefs()); got != 4 {
			t.Fatalf("feed briefs=%d", got)
		}
		if calls > 1 {
			t.Fatalf("feed collected sources %d times for 4 briefs", calls)
		}
		t.Logf("4-brief feed collections: %d", calls)
	})
	for _, writer := range []string{mobileBriefCardWriterPattern, mobileBriefCardWriterModel} {
		t.Run("card_"+writer, func(t *testing.T) {
			a, _, svc, brief, now := round1TopicBrief(t, mobileBriefTestDay(8, 0))
			for i := range brief.Sections.WorkingToward {
				brief.Sections.WorkingToward[i].AttentionReason = ""
			}
			brief = a.mobileBriefWithCards(brief)
			if err := svc.store.save(brief, now); err != nil {
				t.Fatal(err)
			}
			questions := cardsByKind(brief.Cards, mobileBriefCardQuestion)
			if len(questions) == 0 {
				t.Fatalf("fixture has no paragraph chip: narrative=%+v, workingToward=%+v, evidence=%+v, cards=%+v",
					brief.Narrative, brief.Sections.WorkingToward, brief.Evidence, brief.Cards)
			}
			card := questions[0]
			if writer == mobileBriefCardWriterModel {
				brief.NarrativeStatus = "ok"
				bindConversationBriefFixture(t, a, &brief)
				for i := range brief.Cards {
					brief.Cards[i].Writer = writer
				}
				if err := svc.store.save(brief, now); err != nil {
					t.Fatal(err)
				}
			}
			delivered, ok := a.todayChatDeliveredBrief(brief.BriefID)
			current, found := mobileBriefFindCard(delivered, card.ID)
			if !ok || !found || current.Writer != writer {
				t.Fatal("writer control was lost")
			}
			calls := 0
			a.todayChatCollectionObserver = func() { calls++ }
			ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: card.ID, BriefID: brief.BriefID}
			draft, err := a.PrepareTodayChatDraft(ref)
			if err != nil || draft.TopicID == "" || draft.Message != card.Message {
				t.Fatalf("draft: %+v %v", draft, err)
			}
			if calls != 1 {
				t.Fatalf("card resolve collected sources %d times", calls)
			}
			t.Logf("%s card collections: %d", writer, calls)
		})
	}
	t.Run("batch", func(t *testing.T) {
		a, _, svc, brief, now := round1TopicBrief(t, mobileBriefTestDay(8, 0))
		for i := range brief.Sections.WorkingToward {
			brief.Sections.WorkingToward[i].AttentionReason = ""
		}
		secondGoal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "round1-batch-goal", PurposeKindGoal, "Release notes", false, nil)
		if _, err := collectAndReadTopicViewsForTest(a); err != nil {
			t.Fatal(err)
		}
		brief.Evidence = append(brief.Evidence, MobileBriefRef{Ref: "e2", SourceKind: "purpose", SourceID: secondGoal.OpenLoop.ID, Label: secondGoal.Current.Title})
		brief.Sections.WorkingToward = append(brief.Sections.WorkingToward, MobileBriefItem{
			ID: "working-toward-batch", Key: "working-toward-batch", Title: secondGoal.Current.Title,
			Kind: "goal", Status: "active", Refs: []string{"e2"},
		})
		body := MobileBriefPara{Text: "The Release notes are ready.", Refs: []string{"e2"}}
		closingIndex := len(brief.Narrative) - 1
		brief.Narrative = append(brief.Narrative, MobileBriefPara{})
		copy(brief.Narrative[closingIndex+1:], brief.Narrative[closingIndex:])
		brief.Narrative[closingIndex] = body
		brief = a.mobileBriefWithCards(brief)
		if err := svc.store.save(brief, now); err != nil {
			t.Fatal(err)
		}
		var refs []TodayChatContextRef
		for _, card := range cardsByKind(brief.Cards, mobileBriefCardQuestion) {
			refs = append(refs, TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: card.ID, BriefID: brief.BriefID})
		}
		if len(refs) != 2 {
			t.Fatalf("questions=%d", len(refs))
		}
		calls := 0
		a.todayChatCollectionObserver = func() { calls++ }
		for _, result := range a.PrepareTodayChatDrafts(refs) {
			if result.Error != "" || result.Draft == nil || result.Draft.TopicID == "" {
				t.Fatalf("warm: %+v", result)
			}
		}
		if calls != 1 {
			t.Fatalf("batch collected sources %d times", calls)
		}
	})
	// Recent briefs suppress a repeated goal unless it is due today. The service
	// fixture uses UTC, so now+1h becomes tomorrow at 16:00 PDT. Pin both clocks
	// to the same whole second: the stored fixture is not an earlier brief after
	// RFC3339 persistence drops subsecond precision.
	pdt := time.FixedZone("PDT", -7*60*60)
	for _, tc := range []struct {
		name     string
		now      time.Time
		wantTier string
	}{
		{"before_utc_deadline_rollover", time.Date(2026, 10, 22, 14, 36, 0, 0, pdt), mobileBriefPickDue},
		{"afternoon_utc_deadline_rollover", time.Date(2026, 10, 22, 16, 55, 0, 0, pdt), mobileBriefPickDueSoon},
	} {
		t.Run("generation/"+tc.name, func(t *testing.T) {
			a, _, svc, brief, now := round1TopicBrief(t, tc.now)
			goal := brief.Sections.WorkingToward[0]
			sourceID := mobileBriefEvidenceByRef(brief)[goal.Refs[0]].SourceID
			svc.gather = func(_ context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
				return mobileBriefInput{Slot: slot, AsOf: asOf, WindowStart: start, Location: time.Local, WorkingToward: []mobileBriefCandidate{{SourceKind: "purpose", SourceID: sourceID, Title: goal.Title, Kind: "goal", Status: "active", DueAt: now.Add(time.Hour), At: now}}}, nil
			}
			locked := 0
			a.todayChatCollectionObserver = func() {
				if svc.patchMu.TryLock() {
					svc.patchMu.Unlock()
				} else {
					locked++
				}
			}
			generated, err := svc.generate(t.Context(), mobileBriefSlotOnRequest, mobileBriefReasonPhone)
			if err != nil || generated.modelBinding == nil {
				t.Fatalf("generation did not bind: %+v %v", generated.modelBinding, err)
			}
			if picks := mobileBriefPicks(generated); len(picks) != 1 || picks[0].Tier != tc.wantTier {
				t.Fatalf("generation picks: %+v, want %s", picks, tc.wantTier)
			}
			if locked != 0 {
				t.Fatalf("generation collected %d times under patchMu", locked)
			}
		})
	}
	t.Run("audio_patch", func(t *testing.T) {
		a, _, svc, brief, now := round1TopicBrief(t, mobileBriefTestDay(8, 0))
		// Only model text is read aloud (design R74): give the brief the model's reply.
		brief.Narrative, brief.NarrativeStatus, _ = testModelCompose(t.Context(), nil, brief, nil)
		if err := svc.store.save(brief, now); err != nil {
			t.Fatal(err)
		}
		svc.speaker = fakeSpeaker("en", nil, nil)
		svc.speaker.voiceReady = func(string) bool { return false }
		svc.speaker.downloadVoice = func(context.Context, string) bool { return true }
		locked := 0
		a.todayChatCollectionObserver = func() {
			if svc.patchMu.TryLock() {
				svc.patchMu.Unlock()
			} else {
				locked++
			}
		}
		svc.addAudio(t.Context(), brief, now)
		stored, ok := svc.store.load(brief.BriefID)
		if !ok || stored.Audio == nil {
			t.Fatal("audio patch did not run")
		}
		if locked != 0 {
			t.Fatalf("collected sources %d times while patchMu held", locked)
		}
	})
}
