package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/handoff"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// Independent privacy/transport tests still need real readable history sources.
func seedMobileBriefReadableHistory(t *testing.T, app *App, ids ...string) {
	t.Helper()
	if app.handoffStore == nil {
		var err error
		app.handoffStore, err = handoff.NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		if _, err := app.handoffStore.Save(handoff.Entry{ID: id, Title: "Readable work", Source: "manual", Status: handoff.StatusCompleted}); err != nil {
			t.Fatal(err)
		}
	}
}

const mobileBriefPrivateSubject = "Contract renewal for Acme"

func newMobileBriefPrivacyBridge(t *testing.T, app *App) *mobileBridge {
	t.Helper()
	dir := t.TempDir()
	if _, err := createMobileHubIdentity(dir); err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(app, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	return bridge
}

func seedMobileBriefSubject(t *testing.T, app *App, subject string) {
	t.Helper()
	now := time.Now()
	if _, err := app.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{{ID: "inbox:brief-mail", Mailbox: storage.MailboxInbox, SenderEmail: "sender@example.com", Subject: subject, ThreadKey: "brief-mail-thread", At: now.Add(-time.Hour).UnixMilli()}}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func assertMobileBriefNoSubject(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(mobileBriefPrivateSubject)) {
		t.Fatalf("mail subject reached brief JSON: %s", raw)
	}
}

func TestMobileBriefCommitmentKeepsNonAnswerChatMessageReference(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const messageID = "commitment-chat-message"
	if _, err := app.store.AppendStreamItem(t.Context(), session.LocalID, storage.StreamItemRow{
		ID: messageID, Kind: "assistant", Content: "I will follow up on the plan.",
	}); err != nil {
		t.Fatal(err)
	}
	brief := MobileBriefV1{
		NarrativeStatus: "ok",
		Evidence: []MobileBriefRef{{
			Ref: "commitment-source", SourceKind: "chat_message", SourceID: messageID,
			Label: "Follow up on the plan",
		}},
		Sections: MobileBriefSections{Commitments: []MobileBriefItem{{
			ID: "commitment", Title: "Follow up on the plan", Detail: "Due today",
			Status: "due_today", Kind: "commitment", Refs: []string{"commitment-source"},
		}}},
	}
	delivered, _ := app.mobileBriefDelivery(brief)
	if len(delivered.Sections.Commitments) != 1 || delivered.Sections.Commitments[0].ID != "commitment" {
		t.Fatalf("commitment with a non-answer chat reference was dropped: %+v", delivered.Sections.Commitments)
	}
}

func TestMobileBriefPurposeMailHoldbackAtCollection(t *testing.T) {
	for _, field := range []string{"title", "short_title", "short_embedded_title", "outcome", "waiting", "unavailable"} {
		t.Run(field, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			now := time.Now()
			detail := mobileBriefPurposeFixture("held", now)
			subject := mobileBriefPrivateSubject
			switch field {
			case "title":
				detail.Current.Title = "Answer " + subject
			case "short_title":
				subject = "Notes"
				detail.Current.Title = "Notes"
			case "short_embedded_title":
				subject = "HR"
				detail.Current.Title = "Review HR today"
			case "outcome":
				detail.Current.DesiredOutcome = subject
			case "waiting":
				detail.Current.WaitingCondition = subject
			}
			seedMobileBriefSubject(t, app, "Re: "+subject)
			if field == "unavailable" {
				app.store.Close()
			}
			in := mobileBriefInput{AsOf: now, Location: time.UTC}
			collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
			brief := buildMobileBriefV1(in)
			if !mobileBriefSectionsEmpty(brief.Sections) || len(brief.Evidence) != 0 {
				t.Fatalf("held goal entered the brief: %+v", brief)
			}
			if brief.Limits.ExcludedSensitiveItems != 1 {
				t.Fatalf("missing holdback count: %+v", brief.Limits)
			}
		})
	}
}

func TestMobileBriefMailHoldbackOnEveryWireField(t *testing.T) {
	for _, field := range []string{"title", "detail", "next_step", "context", "evidence_label", "source_id", "narrative", "generation", "upcoming", "chief_review", "chief_decide", "chief_plan", "chief_highlights", "chief_day_ahead"} {
		t.Run(field, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			seedMobileBriefReadableHistory(t, app, "held", "safe")
			seedMobileBriefSubject(t, app, mobileBriefPrivateSubject)
			brief := buildMobileBriefV1(mobileBriefInput{AsOf: time.Now(), OpenLoops: []mobileBriefCandidate{{SourceKind: "history", SourceID: "held", Title: "Review the draft"}, {SourceKind: "history", SourceID: "safe", Title: "Prepare the release"}}})
			item := &brief.Sections.OpenLoops[0]
			switch field {
			case "title":
				item.Title = mobileBriefPrivateSubject
			case "detail":
				item.Detail = "Follow up on " + mobileBriefPrivateSubject
			case "next_step":
				item.NextStep = mobileBriefPrivateSubject
			case "context":
				item.Context = &MobileBriefItemContext{Preview: mobileBriefPrivateSubject}
			case "evidence_label":
				brief.Evidence[0].Label = mobileBriefPrivateSubject
			case "source_id":
				brief.Evidence[0].SourceID = mobileBriefPrivateSubject
			case "narrative":
				brief.Narrative = []MobileBriefPara{{Text: mobileBriefPrivateSubject, Refs: item.Refs}}
				brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
			case "generation":
				brief.Generation = &MobileBriefGeneration{Message: mobileBriefPrivateSubject}
			case "upcoming":
				brief.Upcoming = append(brief.Upcoming, *item)
				brief.Upcoming[0].Detail = mobileBriefPrivateSubject
			case "chief_review", "chief_decide", "chief_plan", "chief_highlights", "chief_day_ahead":
				projected := *item
				projected.Detail = mobileBriefPrivateSubject
				switch field {
				case "chief_review":
					brief.Sections.Review = []MobileBriefItem{projected}
				case "chief_decide":
					brief.Sections.Decide = []MobileBriefItem{projected}
				case "chief_plan":
					brief.Sections.Plan = []MobileBriefItem{projected}
				case "chief_highlights":
					brief.Sections.Highlights = []MobileBriefItem{projected}
				case "chief_day_ahead":
					brief.Sections.DayAhead = []MobileBriefItem{projected}
				}
			}
			filtered := app.mobileBriefForDelivery(brief)
			assertMobileBriefNoSubject(t, filtered)
			found := false
			for _, item := range filtered.Sections.OpenLoops {
				found = found || item.Title == "Prepare the release"
			}
			if !found {
				t.Fatal("unrelated safe work was dropped")
			}
			if filtered.Audio != nil {
				t.Fatal("old spoken synthesis survived withdrawal")
			}
		})
	}
}

func TestMobileBriefMailReadFailureHoldsOpenLoopsAndSynthesis(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	seedMobileBriefReadableHistory(t, app, "safe", "legacy")
	brief := buildMobileBriefV1(mobileBriefInput{AsOf: time.Now(),
		Moved:     []mobileBriefCandidate{{SourceKind: "history", SourceID: "safe", Title: "Finished the draft"}},
		OpenLoops: []mobileBriefCandidate{{SourceKind: "history", SourceID: "legacy", Title: "Review a message"}},
	})
	brief.Narrative = []MobileBriefPara{{Text: "The message needs a reply.", Refs: brief.Sections.OpenLoops[0].Refs}}
	brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
	app.store.Close()
	filtered := app.mobileBriefForDelivery(brief)
	if len(filtered.Sections.OpenLoops) != 0 || len(filtered.Narrative) != 0 || filtered.Audio != nil {
		t.Fatal("unreadable mail made a legacy open loop or synthesis fail open")
	}
	if len(filtered.Sections.Moved) != 1 {
		t.Fatal("an unrelated safe section was withdrawn")
	}
}

func TestMobileBriefMailTextMatchesWordsAndPunctuation(t *testing.T) {
	for _, test := range []struct {
		text, subject string
		want          bool
	}{
		{"Review HR today", "hr", true},
		{"Ship the release", "hi", false},
		{"Review Contract-renewal for Acme", "contract renewal for acme", true},
		{"Review the release plan", "quarterly report", false},
	} {
		if got := mobileBriefTextEchoesMail(test.text, []string{test.subject}); got != test.want {
			t.Errorf("%q / %q: got %t, want %t", test.text, test.subject, got, test.want)
		}
	}
}

func TestMobileBriefStoredPurposeReadFilterAndPhoneJSON(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now()
	store := NewPurposeMapStore(app.activity.store.db)
	// A reminder's title keeps the mail hold; a goal typed for Isoform keeps
	// its card (item 66 C3).
	held := mobileTestPurpose(t, store, "held-brief", PurposeKindGoal, mobileBriefPrivateSubject, false, func(input *CreatePurposeOpenLoopInput) {
		input.Evidence[0].SourceKind = reminderPurposeSourceKind
	})
	safe := mobileTestPurpose(t, store, "safe-brief", PurposeKindGoal, "Prepare the release", false, nil)
	in := mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{held, safe}, nil)
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.WorkingToward) != 2 {
		t.Fatalf("fixture did not capture both goals: %+v", brief.Sections)
	}
	brief.Narrative = []MobileBriefPara{{Text: "Your work included " + mobileBriefPrivateSubject, Refs: brief.Sections.WorkingToward[0].Refs}}
	brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK, Bytes: 4, Mime: mobileBriefAudioMimeWAV}
	svc := newMobileBriefService(app)
	svc.location, svc.now = time.UTC, func() time.Time { return now }
	if err := svc.store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc.store.dir, brief.BriefID+".wav"), []byte("wave"), 0600); err != nil {
		t.Fatal(err)
	}
	seedMobileBriefSubject(t, app, "Re: "+mobileBriefPrivateSubject)

	// Use the transport boundary as well as the real service: an alternate
	// source or an old stored brief cannot skip the delivery filter.
	app.mobileBriefSvc = &mobileTestBriefSource{brief: &brief}
	bridge := newMobileBriefPrivacyBridge(t, app)
	device := mobileTestDevice(mobileScopeBriefRead)
	for _, path := range []string{"/brief/latest", "/briefs/" + brief.BriefID} {
		response := mobileTestChatCall(bridge, device, http.MethodGet, path, "")
		if response.Code != http.StatusOK || strings.Contains(strings.ToLower(response.Body.String()), strings.ToLower(mobileBriefPrivateSubject)) {
			t.Fatalf("%s leaked stored text: %d %s", path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), "Prepare the release") {
			t.Fatal("safe goal disappeared from phone JSON")
		}
	}
	for _, read := range []func() (MobileBriefV1, bool){func() (MobileBriefV1, bool) { return svc.GetMobileBrief(brief.BriefID) }, svc.LatestMobileBrief} {
		filtered, ok := read()
		if !ok {
			t.Fatal("stored brief missing")
		}
		assertMobileBriefNoSubject(t, filtered)
		if filtered.Audio != nil {
			t.Fatal("stored audio survived a held source")
		}
	}
	if _, ok := svc.Audio(brief.BriefID); ok {
		t.Fatal("audio endpoint returned the old spoken mail subject")
	}
	if len(mobileBriefFeedItemsFrom(svc.feed.briefs())) != 0 {
		t.Fatal("podcast feed advertised held audio")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	svc.feed.listener, svc.feed.secret = listener, "test-secret"
	recorder := httptest.NewRecorder()
	svc.feed.serve(recorder, httptest.NewRequest(http.MethodGet, "/feed/test-secret/audio/"+brief.BriefID+".wav", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("podcast audio returned %d", recorder.Code)
	}

	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	if err := bridge.publishBrief(brief, false); err != nil {
		t.Fatal(err)
	}
	frame := bridge.pendingBriefs[phone.deviceID]
	raw := phone.openFromHub(t, bridge.identity.Public, bridge.identity.HubID, frame.Env)
	if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(mobileBriefPrivateSubject)) {
		t.Fatal("encrypted push contained the held subject")
	}
}

func TestMobileBriefPurposeReadFilterTracksSensitiveSources(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now()
	at := now.Add(-time.Hour)
	session := seedMobileBriefPurposeRun(t, app, "sensitivity", 1, at, 1000, []string{"Editor"})
	detail := mobileTestPurpose(t, NewPurposeMapStore(app.activity.store.db), "sensitive-brief", PurposeKindGoal, "Review confidential plan", false, func(input *CreatePurposeOpenLoopInput) {
		evidence := input.Evidence[0]
		evidence.SourceKind, evidence.SourceID, evidence.SourceObservedAt = "activity_session", session, at.UnixMilli()
		evidence.EvidenceClass, evidence.IndependenceKey = PurposeEvidenceObservedUser, session
		input.Evidence = append(input.Evidence, evidence)
	})
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
	brief := buildMobileBriefV1(in)
	if len(app.mobileBriefForDelivery(brief).Sections.WorkingToward) != 1 {
		t.Fatal("safe source was unavailable before change")
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	filtered := app.mobileBriefForDelivery(brief)
	raw, _ := json.Marshal(filtered)
	if strings.Contains(string(raw), "Review confidential plan") || strings.Contains(string(raw), session) || len(filtered.Sections.OpenLoops) != 0 {
		t.Fatalf("protected source survived the read gate: %s", raw)
	}
}

func TestMobileBriefPurposeReadFilterReducesMissingEvidenceToFallback(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now().UTC()
	at := now.Add(-time.Hour)
	session := seedMobileBriefPurposeRun(t, app, "missing-read", 1, at, 1000, []string{"Editor"})
	const outcome = "UNRETAINED_OUTCOME"
	detail := mobileTestPurpose(t, NewPurposeMapStore(app.activity.store.db), "missing-read", PurposeKindCommitment, "Review the release", false, func(input *CreatePurposeOpenLoopInput) {
		evidence := input.Evidence[0]
		evidence.SourceKind, evidence.SourceID, evidence.SourceObservedAt = "activity_session", session, at.UnixMilli()
		evidence.EvidenceClass, evidence.IndependenceKey = PurposeEvidenceObservedUser, session
		input.Evidence = append(input.Evidence, evidence)
		input.DesiredOutcome = outcome
		input.DueKind, input.DueValue = PurposeDueDate, now.Format("2006-01-02")
	})
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
	stored := buildMobileBriefV1(in)
	if len(stored.Sections.OpenLoops) != 1 || stored.Sections.OpenLoops[0].Detail != outcome || len(stored.Sections.OpenLoops[0].Refs) != 1 {
		t.Fatal("fixture needs the legacy Purpose-only citation with a derived outcome")
	}
	stored.Sections.OpenLoops[0].NextStep = "UNRETAINED_NEXT_STEP"
	stored.Sections.OpenLoops[0].EndAt = formatMobileBriefTime(now)
	stored.Sections.OpenLoops[0].Context = &MobileBriefItemContext{Preview: "UNRETAINED_PREVIEW"}
	stored.Sections.WaitingOnYou = append(stored.Sections.WaitingOnYou, stored.Sections.WorkingToward[0])
	stored.Narrative = []MobileBriefPara{{Text: outcome, Refs: stored.Sections.OpenLoops[0].Refs}}
	stored.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
	if _, err := app.activity.store.db.Exec(`DELETE FROM activity_sessions WHERE id=?`, session); err != nil {
		t.Fatal(err)
	}
	filtered := app.mobileBriefForDelivery(stored)
	for _, items := range [][]MobileBriefItem{filtered.Sections.WorkingToward, filtered.Sections.OpenLoops} {
		if len(items) != 1 {
			t.Fatalf("missing evidence lost the allowed title/state fallback: %+v", filtered.Sections)
		}
		item := items[0]
		if item.Title != detail.Current.Title || item.Status != detail.Current.LifecycleState || item.PurposeID != detail.OpenLoop.ID || !item.EvidenceUnavailable || item.Detail != "Evidence no longer on this computer." {
			t.Fatalf("incorrect fallback: %+v", item)
		}
		if len(item.Refs) != 0 || item.At != "" || item.EndAt != "" || item.LastTouch != "" || item.NextStep != "" || item.DueAt != "" || item.MomentID != "" || item.ChatSessionID != "" || item.ChatMessageID != "" || item.URL != "" || item.Context != nil {
			t.Fatalf("fallback retained a claim without its evidence: %+v", item)
		}
	}
	if len(filtered.Sections.Commitments) != 0 || len(filtered.Sections.WaitingOnYou) != 0 || len(filtered.Evidence) != 0 || len(filtered.Narrative) != 0 || filtered.Audio != nil {
		t.Fatalf("missing evidence retained derived sections or synthesis: %+v", filtered)
	}
	raw, _ := json.Marshal(filtered)
	if strings.Contains(string(raw), "UNRETAINED_") || strings.Contains(string(raw), session) {
		t.Fatalf("old detail or provenance reached the phone: %s", raw)
	}

	// Fresh collection already emits source-free fallback rows. Keep their
	// stable feedback keys, localized detail and content-free wire contract.
	for _, language := range []string{"en", "zh-CN"} {
		if err := app.store.SetUIState(context.Background(), uiLanguageStateKey, language); err != nil {
			t.Fatal(err)
		}
		fresh := mobileBriefInput{AsOf: now, Location: time.UTC}
		collectMobileBriefPurposes(app, &fresh, []PurposeOpenLoopDetail{detail}, nil)
		fallback := buildMobileBriefV1(fresh)
		if got := app.mobileBriefForDelivery(fallback); !reflect.DeepEqual(got.Sections, fallback.Sections) || len(got.Evidence) != 0 {
			t.Fatalf("%s changed an existing safe fallback: %+v", language, got.Sections)
		}
	}
}

func TestMobileBriefStoredSubjectStaysHeldAfterRepairAndMailPrune(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now()
	store := NewPurposeMapStore(app.activity.store.db)
	// A reminder's title keeps the mail hold; a goal typed for Isoform keeps
	// its card (item 66 C3).
	detail := mobileTestPurpose(t, store, "repair-old-brief", PurposeKindGoal, mobileBriefPrivateSubject, false, func(input *CreatePurposeOpenLoopInput) {
		input.Evidence[0].SourceKind = reminderPurposeSourceKind
		input.DesiredOutcome = "A reviewed agreement"
	})
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
	stored := buildMobileBriefV1(in)
	if len(stored.Sections.WorkingToward) != 1 {
		t.Fatal("fixture did not retain the old title")
	}
	seedMobileBriefSubject(t, app, mobileBriefPrivateSubject)
	mailDB, err := sql.Open("sqlite", filepath.Join(os.Getenv("HOME"), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mailDB.Close()
	counts, err := backfillPurposeMailTitles(app.activity.store.db, mailDB, false, time.Now())
	if err != nil || counts.Retitled != 1 {
		t.Fatalf("title repair failed: %+v, %v", counts, err)
	}
	if _, err := app.store.PruneMailMessages(context.Background(), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetDetail(detail.OpenLoop.ID)
	if err != nil || current.Current.Title == mobileBriefPrivateSubject {
		t.Fatalf("current title was not repaired: %+v, %v", current, err)
	}
	filtered := app.mobileBriefForDelivery(stored)
	assertMobileBriefNoSubject(t, filtered)
	if len(filtered.Sections.WorkingToward) != 0 || len(filtered.Sections.OpenLoops) != 0 {
		t.Fatal("old stored title reappeared after its mail header was pruned")
	}
	fresh := mobileBriefInput{AsOf: time.Now(), Location: time.UTC}
	collectMobileBriefPurposes(app, &fresh, []PurposeOpenLoopDetail{current}, nil)
	if len(app.mobileBriefForDelivery(buildMobileBriefV1(fresh)).Sections.WorkingToward) != 1 {
		t.Fatal("the repaired neutral goal should remain eligible")
	}
}

func TestMobileBriefPurposeTodayProjectionPreservesSafetyMetadata(t *testing.T) {
	now := time.Now()
	item := mobileBriefCandidate{SourceKind: "purpose", SourceID: "safe", Title: "Prepare the release", Detail: "Reviewed the plan", At: now, LastTouch: now, NextStep: "Send the notes", DueAt: now.Add(time.Hour), MomentID: "moment", ChatSessionID: "chat", ChatMessageID: "message", Evidence: []mobileBriefCandidate{{SourceKind: "activity_run", SourceID: "run", Title: "Checked the plan"}}}
	fallback := mobileBriefCandidate{SourceKind: "purpose", SourceID: "missing", Title: "Remember the plan", Detail: "Evidence no longer on this computer.", EvidenceUnavailable: true}
	brief := buildMobileBriefV1(mobileBriefInput{AsOf: now, WorkingToward: []mobileBriefCandidate{item, fallback}, Commitments: []mobileBriefCandidate{item}, WaitingOnYou: []mobileBriefCandidate{item}, OpenLoops: []mobileBriefCandidate{item, fallback}})
	updated := projectMobileBriefToday(brief, mobileBriefInput{AsOf: now, Today: &mobileBriefTodayInput{Context: map[string]MobileBriefItemContext{}, Unavailable: map[string]bool{}}})
	if !reflect.DeepEqual(brief.Sections, updated.Sections) {
		t.Fatalf("Today discarded Purpose metadata or unavailable-evidence identity:\nbefore: %+v\nafter: %+v", brief.Sections, updated.Sections)
	}
	if !reflect.DeepEqual(brief.Evidence, updated.Evidence) {
		t.Fatal("Today discarded Purpose evidence")
	}
}

func TestMobileBriefPendingReplayRechecksMail(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	seedMobileBriefReadableHistory(t, app, "letter")
	bridge := newMobileBriefPrivacyBridge(t, app)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	brief := buildMobileBriefV1(mobileBriefInput{AsOf: time.Now(), OpenLoops: []mobileBriefCandidate{{SourceKind: "history", SourceID: "letter", Title: mobileBriefPrivateSubject}}})
	if err := bridge.publishBrief(brief, false); err != nil {
		t.Fatal(err)
	}
	queued := bridge.pendingBriefs[phone.deviceID]
	before := phone.openFromHub(t, bridge.identity.Public, bridge.identity.HubID, queued.Env)
	if !strings.Contains(string(before), mobileBriefPrivateSubject) {
		t.Fatal("fixture did not queue the pre-mail document")
	}
	seedMobileBriefSubject(t, app, mobileBriefPrivateSubject)
	bridge.briefDeliveryMu.Lock()
	refreshed, err := bridge.refreshPendingBrief(queued)
	bridge.briefDeliveryMu.Unlock()
	if err != nil || refreshed.Env.Seq <= queued.Env.Seq {
		t.Fatal("changed replay did not receive a new sequence")
	}
	after := phone.openFromHub(t, bridge.identity.Public, bridge.identity.HubID, refreshed.Env)
	if strings.Contains(string(after), mobileBriefPrivateSubject) {
		t.Fatal("offline replay leaked a newly held subject")
	}
}

func newStoredMobileBriefPrivacyFixture(t *testing.T) (*App, *mobileBriefService, MobileBriefV1) {
	t.Helper()
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(nil)
	insertTestSession(t, app.activity.store, "brief-delivery-source", "done", 100_000)
	content := "---\ntitle: Protected recall title\nsource: dream\nread_provenance: '{\"version\":2,\"kinds\":[\"activity\"],\"sessions\":[\"brief-delivery-source\"]}'\n---\n\nprivate-source-body\n"
	writePrivacyMemory(t, "protected-recall", content)
	writePrivacyMemory(t, "safe-recall", "# Safe recall title\n\nA manually written note.\n")
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "protected-knowledge.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	brief := buildMobileBriefV1(mobileBriefInput{
		AsOf: now, WindowStart: now.Add(-time.Hour), Slot: mobileBriefSlotMorning, Location: time.UTC,
		Learned: []mobileBriefCandidate{
			{SourceKind: "memory", SourceID: "protected-recall", Title: "Protected recall title", At: now.Add(-time.Minute)},
			{SourceKind: "knowledge", SourceID: "protected-knowledge", Title: "Protected knowledge title", At: now.Add(-time.Minute)},
			{SourceKind: "memory", SourceID: "safe-recall", Title: "Safe recall title", At: now.Add(-time.Minute)},
		},
		// Substantive activity evidence remains subject to retention/privacy. Raw
		// recording announcements have their own silence regression below.
		Meetings: []mobileBriefCandidate{{SourceKind: "activity_session", SourceID: "brief-delivery-source", Title: "Protected meeting title", At: now.Add(-time.Minute)}},
	})
	brief.NarrativeStatus = "ok"
	// Even an incorrectly cited paragraph has seen all the evidence. The
	// old narrative must be removed as a whole when any source is withdrawn.
	var safeRef string
	for _, ref := range brief.Evidence {
		if ref.SourceID == "safe-recall" {
			safeRef = ref.Ref
		}
	}
	brief.Narrative = []MobileBriefPara{{Text: "Protected narrative text", Refs: []string{safeRef}}}
	brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK, Attachment: &MobileBriefAudioAttachment{ID: "private-audio", Key: "private-audio-key"}}
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	svc := newMobileBriefService(app)
	svc.location = time.UTC
	svc.now = func() time.Time { return now }
	app.mobileBriefSvc = svc
	if err := svc.store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	return app, svc, brief
}

func assertMobileBriefDeliveryFiltered(t *testing.T, brief MobileBriefV1, closedEyes bool) {
	t.Helper()
	data, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Protected", "protected-recall", "protected-knowledge", "brief-delivery-source", "private-audio"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("delivered brief includes withdrawn source %q: %s", forbidden, data)
		}
	}
	if got := strings.Contains(string(data), "Safe recall title"); got == closedEyes {
		t.Fatalf("safe source presence = %v, closed eyes = %v", got, closedEyes)
	}
	if brief.Audio != nil || len(brief.Narrative) != 0 || brief.NarrativeStatus == "ok" {
		t.Fatal("withdrawn synthesis or recording remains deliverable")
	}
}

func TestMobileBriefDeliveryRechecksStoredSources(t *testing.T) {
	for _, reason := range []string{"sensitive app", "historical pause", "closed eyes", "missing source"} {
		t.Run(reason, func(t *testing.T) {
			app, svc, stored := newStoredMobileBriefPrivacyFixture(t)
			before, ok := svc.LatestMobileBrief()
			if !ok || len(before.Sections.Learned) != 3 || len(before.Sections.Meetings) != 1 || len(before.Narrative) != 1 || before.Audio == nil {
				t.Fatalf("fixture must remain readable before privacy changes: %+v", before)
			}
			switch reason {
			case "sensitive app":
				pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
			case "historical pause":
				app.saveObservationPauses([]observationPause{{Start: 100_000, End: 160_000}})
			case "closed eyes":
				app.closedEyes.enabled.Store(true)
			case "missing source":
				if err := app.activity.store.DeleteSession("brief-delivery-source"); err != nil {
					t.Fatal(err)
				}
			}
			bridge, err := newMobileBridge(app, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(bridge.Stop)
			for _, path := range []string{"/mobile/v1/brief/latest", "/mobile/v1/briefs/" + stored.BriefID} {
				recorder := httptest.NewRecorder()
				bridge.handleAuthorized(recorder, httptest.NewRequest(http.MethodGet, path, nil), mobileTestDevice(mobileScopeBriefRead))
				if recorder.Code != http.StatusOK {
					t.Fatalf("%s returned %d: %s", path, recorder.Code, recorder.Body.String())
				}
				var response struct {
					Data MobileBriefV1 `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				assertMobileBriefDeliveryFiltered(t, response.Data, reason == "closed eyes")
			}
			refreshed, ok := svc.store.load(stored.BriefID)
			if !ok || len(refreshed.Sections.Learned) != 3 {
				t.Fatal("a delivery projection modified the user's local source rows")
			}
			if reason == "closed eyes" {
				// Closed Eyes hides the text while it is on and withdraws
				// nothing for good: the stored text comes back with it off.
				if len(refreshed.Narrative) != 1 || refreshed.NarrativeStatus != "ok" || refreshed.Audio == nil {
					t.Fatal("Closed Eyes erased the stored brief's text")
				}
				app.closedEyes.enabled.Store(false)
				if after, ok := svc.LatestMobileBrief(); !ok || len(after.Narrative) != 1 || after.NarrativeStatus != "ok" {
					t.Fatalf("the text did not come back after Closed Eyes: %+v", after.Narrative)
				}
				return
			}
			if len(refreshed.Narrative) != 0 || refreshed.NarrativeStatus != "unavailable" || refreshed.Audio != nil || refreshed.DayShape != nil {
				t.Fatal("the Today refresh persisted synthesis from withdrawn sources")
			}
		})
	}
}

func TestMobileBriefDeliveryKeepsRetainedSources(t *testing.T) {
	app, svc, _ := newStoredMobileBriefPrivacyFixture(t)
	if err := app.activity.store.deleteRetainedSession("brief-delivery-source"); err != nil {
		t.Fatal(err)
	}
	before, ok := svc.LatestMobileBrief()
	if !ok || len(before.Sections.Learned) != 3 || len(before.Sections.Meetings) != 1 || len(before.Narrative) != 1 {
		t.Fatal("raw retention hid a brief with readable source receipts")
	}
	pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
	after, _ := svc.GetMobileBrief(before.BriefID)
	assertMobileBriefDeliveryFiltered(t, after, false)
}

func TestMobileBriefPushRechecksSourcesAtPublishAndReconnect(t *testing.T) {
	for _, when := range []string{"publish", "reconnect"} {
		t.Run(when, func(t *testing.T) {
			app, _, stored := newStoredMobileBriefPrivacyFixture(t)
			dir := t.TempDir()
			identity, err := createMobileHubIdentity(dir)
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := newMobileBridge(app, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(bridge.Stop)
			bridge.identity = &identity
			phone := newMobileTestPhone(t)
			mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
			if when == "publish" {
				pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
			}
			if err := bridge.publishBrief(stored, true); err != nil {
				t.Fatal(err)
			}
			frame := bridge.pendingBriefs[phone.deviceID]
			var queued MobileBriefV1
			if err := json.Unmarshal(phone.openFromHub(t, identity.Public, identity.HubID, frame.Env), &queued); err != nil {
				t.Fatal(err)
			}
			if when == "publish" {
				assertMobileBriefDeliveryFiltered(t, queued, false)
				return
			}
			if len(queued.Sections.Learned) != 3 || len(queued.Narrative) != 1 {
				t.Fatal("offline fixture did not contain the allowed source")
			}
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
			// Exercise the actual reconnect/flush boundary with the real relay
			// client and encryption, using the existing local protocol harness.
			relay := newMobileTestRelay(t, identity.Credential, identity.HubID)
			if err := bridge.enableRelay(relay.url(), false); err != nil {
				t.Fatal(err)
			}
			relay.acceptRegistration(t, mobileTestListed(phone))
			var delivered mobileBriefFrame
			if err := json.Unmarshal(relay.nextFrame(t, "brief").raw, &delivered); err != nil {
				t.Fatal(err)
			}
			if delivered.Env.Seq <= frame.Env.Seq {
				t.Fatal("a changed push payload reused its original encryption sequence")
			}
			var brief MobileBriefV1
			if err := json.Unmarshal(phone.openFromHub(t, identity.Public, identity.HubID, delivered.Env), &brief); err != nil {
				t.Fatal(err)
			}
			assertMobileBriefDeliveryFiltered(t, brief, false)
		})
	}
}

func TestMobileBriefPendingRecheckPreservesEnvelopeOrdering(t *testing.T) {
	for _, scenario := range []string{"unchanged", "newer publication"} {
		t.Run(scenario, func(t *testing.T) {
			app, _, stored := newStoredMobileBriefPrivacyFixture(t)
			dir := t.TempDir()
			identity, err := createMobileHubIdentity(dir)
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := newMobileBridge(app, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(bridge.Stop)
			bridge.identity = &identity
			phone := newMobileTestPhone(t)
			mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
			if err := bridge.publishBrief(stored, false); err != nil {
				t.Fatal(err)
			}
			// Registration takes the queued frame before privacy IO. A newer
			// publication can finish while that old frame is being checked.
			queued := bridge.pendingBriefs[phone.deviceID]
			delete(bridge.pendingBriefs, phone.deviceID)
			if scenario == "newer publication" {
				pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
				newer := stored
				newer.AsOf = formatMobileBriefTime(time.Now().Add(time.Minute))
				if err := bridge.publishBrief(newer, false); err != nil {
					t.Fatal(err)
				}
				delete(bridge.pendingBriefs, phone.deviceID)
			}
			sequence := bridge.devices[phone.deviceID].SendSeq
			// An offline client makes the flush deterministic: an accepted
			// frame is put back in the queue instead of sent to a socket.
			bridge.flushPendingBriefs(&mobileRelayClient{}, []mobileBriefFrame{queued})
			if bridge.devices[phone.deviceID].SendSeq != sequence {
				t.Fatal("unchanged or superseded brief consumed a new encryption sequence")
			}
			replayed, queuedAgain := bridge.pendingBriefs[phone.deviceID]
			if scenario == "newer publication" {
				if queuedAgain {
					t.Fatal("a superseded brief became deliverable after the newer publication")
				}
				return
			}
			if !queuedAgain || !jsonEqual(t, replayed, queued) {
				t.Fatal("an unchanged delivery recheck replaced the original envelope")
			}
		})
	}
}

// Today-only documents are outside scheduled-slot history. Both service paths
// must keep that fallback and still apply the same final delivery projection.
func TestMobileBriefTodayFallbackKeepsDeliveryPrivacy(t *testing.T) {
	for _, route := range []string{"latest", "by-id"} {
		t.Run(route, func(t *testing.T) {
			app, svc, stored := newStoredMobileBriefPrivacyFixture(t)
			if err := svc.store.saveCurrentToday(stored); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(svc.store.dir, stored.BriefID+".json")); err != nil {
				t.Fatal(err)
			}
			before, ok := svc.GetMobileBrief(stored.BriefID)
			if !ok || len(before.Sections.Learned) != 3 {
				t.Fatal("today-only fallback lost a readable document")
			}
			job := storage.CronJobRow{ID: "fresh-today-job", Name: "New scheduled work", Enabled: true,
				ScheduleKind: "cron", ScheduleConfig: `{"expr":"0 12 * * *"}`, TargetKind: "agent",
				TargetConfig: `{"prompt":"review"}`, NextRunAt: svc.now().Add(time.Hour).UnixMilli()}
			if err := app.store.SaveCronJob(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
			var got MobileBriefV1
			if route == "latest" {
				got, ok = svc.LatestMobileBrief()
				if len(got.Upcoming) != 1 || got.Upcoming[0].Title != job.Name {
					t.Fatalf("latest read skipped live Today refresh: %+v", got.Upcoming)
				}
			} else {
				got, ok = svc.GetMobileBrief(stored.BriefID)
			}
			if !ok {
				t.Fatal("today-only fallback disappeared")
			}
			assertMobileBriefDeliveryFiltered(t, got, false)
		})
	}
}

func TestMobileBriefTodayRefreshUsesMemoryReadGate(t *testing.T) {
	app, svc, _ := newStoredMobileBriefPrivacyFixture(t)
	now := svc.now().Add(time.Minute)
	provenance := memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"brief-delivery-source"}}
	memory := withMemoryReadProvenance(buildOKFMemoryMarkdown("protected-recall", "Protected recall title", now, "user_insight", "user_insight", []string{"Private source detail"}), provenance)
	writePrivacyMemory(t, "protected-recall", memory)
	knowledge := buildOKFKnowledgeMarkdown("protected-knowledge", "Protected knowledge title", "Private source detail", now, "activity", nil, []string{"brief-delivery-source"}, []string{"Private source detail"})
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "protected-knowledge.md"), []byte(knowledge), 0600); err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []bool{false, true} {
		if sensitive {
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
		}
		in := mobileBriefInput{AsOf: now.Add(time.Second), Location: time.UTC}
		collectMobileBriefToday(app, &in)
		seen := map[string]bool{}
		for _, candidate := range in.Learned {
			if candidate.SourceID == "protected-recall" || candidate.SourceID == "protected-knowledge" {
				seen[candidate.SourceID] = true
				if candidate.Protected != sensitive {
					t.Errorf("%s protected=%v, want %v", candidate.SourceID, candidate.Protected, sensitive)
				}
			}
		}
		if len(seen) != 2 {
			t.Fatalf("Today fixture lost sources: %+v", in.Learned)
		}
	}
}

// The delivery projection must treat every section of the brief: the Purpose
// Map from the Purpose brief and the upcoming rows from Today. Dropping the
// Purpose sections would erase the feature from the phone; leaving Upcoming out
// of the filter would send automation names and schedules under Closed Eyes.
func TestMobileBriefDeliveryFiltersPurposeSectionsAndUpcoming(t *testing.T) {
	for _, closedEyes := range []bool{false, true} {
		name := "open"
		if closedEyes {
			name = "closed eyes"
		}
		t.Run(name, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			// Current Purpose identity and provenance are checked at delivery.
			// A stored citation alone cannot authorize an unknown ledger row.
			store := NewPurposeMapStore(app.activity.store.db)
			goal := mobileTestPurpose(t, store, "goal-visible", PurposeKindGoal, "Visible goal", false, nil)
			purged := mobileTestPurpose(t, store, "goal-purged", PurposeKindGoal, "Purged goal", false, nil)
			commitment := mobileTestPurpose(t, store, "commitment-visible", PurposeKindCommitment, "Visible commitment", false, nil)
			now := time.Now().UTC()
			brief := buildMobileBriefV1(mobileBriefInput{
				AsOf: now, WindowStart: now.Add(-time.Hour), Slot: mobileBriefSlotMorning, Location: time.UTC,
				WorkingToward: []mobileBriefCandidate{
					{SourceKind: "purpose", SourceID: goal.OpenLoop.ID, Title: "Visible goal", Kind: "goal", Status: "active", EvidenceLabel: "Visible goal"},
					{SourceKind: "purpose", SourceID: purged.OpenLoop.ID, Title: "Purged goal", Kind: "goal", Status: "active", EvidenceUnavailable: true},
				},
				Commitments: []mobileBriefCandidate{{SourceKind: "purpose", SourceID: commitment.OpenLoop.ID, Title: "Visible commitment", Kind: "commitment", Status: "due_today", EvidenceLabel: "Visible commitment"}},
				Today:       &mobileBriefTodayInput{Upcoming: []mobileBriefCandidate{{SourceKind: "schedule", SourceID: "job-1", Title: "Nightly report job", Detail: "Runs at 22:00"}}},
			})
			if len(brief.Sections.WorkingToward) != 2 || len(brief.Sections.Commitments) != 1 || len(brief.Upcoming) != 1 {
				t.Fatalf("fixture must hold both Purpose sections and an upcoming row: %+v / %+v", brief.Sections, brief.Upcoming)
			}
			if closedEyes {
				app.closedEyes.enabled.Store(true)
			}
			delivered := app.mobileBriefForDelivery(brief)
			data, _ := json.Marshal(delivered)
			if closedEyes {
				if delivered.Sections.WorkingToward != nil || delivered.Sections.Commitments != nil || delivered.Sections.WaitingOnYou != nil {
					t.Fatal("empty optional Purpose sections must be nil")
				}
				if len(delivered.Sections.WorkingToward)+len(delivered.Sections.Commitments)+len(delivered.Sections.WaitingOnYou)+len(delivered.Upcoming) != 0 {
					t.Fatalf("Closed Eyes still delivers Purpose or upcoming rows: %s", data)
				}
				for _, secret := range []string{"Nightly report job", "Runs at 22:00", "Visible goal", "Purged goal", "Visible commitment"} {
					if strings.Contains(string(data), secret) {
						t.Fatalf("Closed Eyes delivered %q: %s", secret, data)
					}
				}
				return
			}
			if len(delivered.Sections.WorkingToward) != 2 || len(delivered.Sections.Commitments) != 1 || len(delivered.Upcoming) != 1 {
				t.Fatalf("delivery erased Purpose or upcoming rows: %s", data)
			}
		})
	}
}

// Purpose refs must resolve the full current memory envelope just like direct
// recall refs, including the source-kind aliases used by extraction.
func TestMobileBriefPurposeRechecksMemoryProvenance(t *testing.T) {
	for _, kind := range []string{"memory", "knowledge", taskContextSourceMemoryKind, taskContextSourceKnowledgeKind} {
		t.Run(kind, func(t *testing.T) {
			app, svc, _ := newStoredMobileBriefPrivacyFixture(t)
			id := "protected-recall"
			if kind == "knowledge" || kind == taskContextSourceKnowledgeKind {
				id = "protected-knowledge"
			}
			if kind == taskContextSourceMemoryKind || kind == taskContextSourceKnowledgeKind {
				id += ":live"
			}
			detail := mobileTestPurpose(t, NewPurposeMapStore(app.activity.store.db), "provenance", PurposeKindGoal, "Review the project plan", false, func(input *CreatePurposeOpenLoopInput) {
				evidence := input.Evidence[0]
				evidence.SourceKind, evidence.SourceID = kind, id
				evidence.EvidenceClass, evidence.IndependenceKey = PurposeEvidenceEvaluatedProjection, kind+":"+id
				input.Evidence = append(input.Evidence, evidence)
			})
			in := mobileBriefInput{AsOf: svc.now(), Location: time.UTC}
			collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
			stored := buildMobileBriefV1(in)
			if len(app.mobileBriefForDelivery(stored).Sections.WorkingToward) != 1 {
				t.Fatal("readable memory-backed Purpose was withheld before source protection")
			}
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
			filtered := app.mobileBriefForDelivery(stored)
			raw, _ := json.Marshal(filtered)
			if strings.Contains(string(raw), detail.Current.Title) || len(filtered.Sections.WorkingToward) != 0 || len(filtered.Sections.OpenLoops) != 0 {
				t.Fatalf("Purpose bypassed the current memory envelope policy: %s", raw)
			}
		})
	}
}

func TestMobileBriefHistoryRechecksLiveAndArchivedMemoryPolicy(t *testing.T) {
	for _, kind := range []string{taskContextSourceMemoryKind, taskContextSourceKnowledgeKind} {
		for _, blockedVersion := range []string{"live", "archive"} {
			t.Run(kind+"/"+blockedVersion, func(t *testing.T) {
				app := newMobileBriefPurposeTestApp(t)
				dir, err := memoryDir()
				if kind == taskContextSourceKnowledgeKind {
					dir, err = knowledgeDir()
				}
				if err != nil {
					t.Fatal(err)
				}
				const id = "historical-source"
				const sourceID = id + ":history:1000.md"
				const content = "---\ntitle: Historical finding\nsource: manual\n---\n\nA manually written note.\n"
				livePath := filepath.Join(dir, id+".md")
				historyDir, err := memoryHistoryDir(dir, id)
				if err != nil {
					t.Fatal(err)
				}
				archivePath := filepath.Join(historyDir, "1000.md")
				for _, path := range []string{livePath, archivePath} {
					if err := os.WriteFile(path, []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
				}
				detail := mobileTestPurpose(t, NewPurposeMapStore(app.activity.store.db), "history-policy", PurposeKindGoal, "Review source history", false, func(input *CreatePurposeOpenLoopInput) {
					evidence := input.Evidence[0]
					evidence.SourceKind, evidence.SourceID = kind, sourceID
					evidence.EvidenceClass, evidence.IndependenceKey = PurposeEvidenceEvaluatedProjection, kind+":"+sourceID
					input.Evidence = append(input.Evidence, evidence)
				})
				in := mobileBriefInput{AsOf: time.Now(), Location: time.UTC,
					Learned: []mobileBriefCandidate{{SourceKind: kind, SourceID: sourceID, Title: "Historical finding"}}}
				collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
				stored := buildMobileBriefV1(in)
				if len(stored.Sections.Learned) != 1 {
					t.Fatal("historical fixture lost its recall card")
				}
				stored.NarrativeStatus = "ok"
				stored.Narrative = []MobileBriefPara{{Text: "Historical source review", Refs: stored.Sections.Learned[0].Refs}}
				stored.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
				before := app.mobileBriefForDelivery(stored)
				if len(before.Sections.Learned) != 1 || len(before.Sections.WorkingToward) != 1 || len(before.Narrative) != 1 || before.Audio == nil {
					t.Fatalf("readable historical sources were withheld: %+v", before)
				}
				blockedPath := livePath
				if blockedVersion == "archive" {
					blockedPath = archivePath
				}
				blocked := withMemoryReadProvenance(content, memoryReadProvenance{Version: 2, ReadBlocked: true})
				if err := os.WriteFile(blockedPath, []byte(blocked), 0600); err != nil {
					t.Fatal(err)
				}
				filtered := app.mobileBriefForDelivery(stored)
				if len(filtered.Sections.Learned) != 0 || len(filtered.Sections.WorkingToward) != 0 || len(filtered.Sections.OpenLoops) != 0 || len(filtered.Evidence) != 0 || len(filtered.Narrative) != 0 || filtered.Audio != nil {
					t.Fatalf("historical source bypassed %s policy: %+v", blockedVersion, filtered)
				}
				for _, path := range []string{livePath, archivePath} {
					want := content
					if path == blockedPath {
						want = blocked
					}
					got, err := os.ReadFile(path)
					if err != nil || string(got) != want {
						t.Fatalf("delivery changed stored source %s: %v", path, err)
					}
				}
			})
		}
	}
}

func TestMobileBriefDeliveryCarriesTheChiefSections(t *testing.T) {
	for _, closedEyes := range []bool{false, true} {
		name := "open"
		if closedEyes {
			name = "closed eyes"
		}
		t.Run(name, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			seedMobileBriefReadableHistory(t, app, "run-1", "run-2", "run-3", "run-4")
			now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
			brief := buildMobileBriefV1(mobileBriefInput{
				AsOf: now, WindowStart: now.Add(-time.Hour), Slot: mobileBriefSectionSlotForTest(), Location: time.UTC,
				Moved: []mobileBriefCandidate{
					{SourceKind: "history", SourceID: "run-1", Title: "Pricing draft", Status: "needs_review", At: now.Add(-time.Minute)},
					{SourceKind: "history", SourceID: "run-2", Title: "Release options", Status: "blocked", At: now.Add(-time.Minute)},
					{SourceKind: "history", SourceID: "run-3", Title: "Next project task", Status: "active", At: now.Add(-time.Minute)},
					{SourceKind: "history", SourceID: "run-4", Title: "Sent the release", Status: "verified", At: now.Add(-time.Minute)},
				},
			})
			if len(brief.Sections.Review) != 1 || len(brief.Sections.Decide) != 1 || len(brief.Sections.Plan) != 1 {
				t.Fatalf("fixture must hold each chief section: %+v", brief.Sections)
			}
			if closedEyes {
				app.closedEyes.enabled.Store(true)
			}
			delivered := app.mobileBriefForDelivery(brief)
			for _, items := range [][]MobileBriefItem{delivered.Sections.Review, delivered.Sections.Decide, delivered.Sections.Plan, delivered.Sections.Highlights} {
				if closedEyes && items != nil {
					t.Fatalf("Closed Eyes left a chief section present: %+v", delivered.Sections)
				}
				if !closedEyes && len(items) != 1 {
					t.Fatalf("delivery dropped a chief section: %+v", delivered.Sections)
				}
			}
		})
	}
}

func mobileBriefSectionSlotForTest() string { return mobileBriefSlotMorning }

func TestMobileBriefAttendanceMailHoldbackOnEveryWireField(t *testing.T) {
	for _, field := range []string{"title", "event_id", "state", "reason", "scope", "refs"} {
		t.Run(field, func(t *testing.T) {
			app, in := newAttendancePrivacyFixture(t, "skipped")
			collectMobileBriefAttendance(app, &in)
			brief := buildMobileBriefV1(in)
			if got := app.mobileBriefForDelivery(brief); len(got.MeetingAttendance) != 1 {
				t.Fatal("readable attendance missing before mail holdback")
			}
			seedMobileBriefSubject(t, app, mobileBriefPrivateSubject)
			row := &brief.MeetingAttendance[0]
			switch field {
			case "title":
				row.Title = mobileBriefPrivateSubject
			case "event_id":
				row.EventID = mobileBriefPrivateSubject
			case "state":
				row.State = mobileBriefPrivateSubject
			case "reason":
				row.Reason = mobileBriefPrivateSubject
			case "scope":
				row.Scope = mobileBriefPrivateSubject
			case "refs":
				row.Refs = append(row.Refs, mobileBriefPrivateSubject)
			}
			brief.Narrative = []MobileBriefPara{{Text: "Review the meeting", Refs: row.Refs}}
			brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
			filtered := app.mobileBriefForDelivery(brief)
			assertMobileBriefNoSubject(t, filtered)
			if filtered.MeetingAttendance != nil || filtered.Sections.Review != nil || len(filtered.Evidence) != 0 || len(filtered.Narrative) != 0 || filtered.Audio != nil {
				t.Fatalf("held attendance retained derived rows or synthesis: %+v", filtered)
			}
		})
	}
}

func TestMobileBriefAttendanceKeepsUnprojectedEvidenceLocal(t *testing.T) {
	app, in := newAttendancePrivacyFixture(t, "unknown")
	collectMobileBriefAttendance(app, &in)
	brief := buildMobileBriefV1(in)
	if len(brief.MeetingAttendance) != 1 {
		t.Fatal("unknown attendance fixture missing")
	}
	// An unfinished slot has no chief review card, but its checked attendance
	// still owns the evidence citation and must survive repeated delivery.
	brief.MeetingAttendance[0].Reason = "slot_not_finished"
	brief.Sections = mobileBriefChiefSections(brief)
	if len(brief.Sections.Review) != 0 {
		t.Fatal("unfinished attendance unexpectedly has a review card")
	}
	filtered := app.mobileBriefForDelivery(app.mobileBriefForDelivery(brief))
	if len(filtered.MeetingAttendance) != 1 || len(filtered.Evidence) != 1 {
		t.Fatalf("unprojected attendance lost its citation: %+v", filtered)
	}
	raw, _ := json.Marshal(filtered)
	for _, forbidden := range []string{"fixture-session", "sourceSessionIDs", "sourceSessionIds", "attendanceSourceSessions"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("attendance exposed local provenance %q: %s", forbidden, raw)
		}
	}
}

func TestMobileBriefCachedRecordingAnnouncementsStaySilent(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	session := ActivitySessionRow{ID: "silent-call-session", StartTime: time.Now().UnixMilli(), EndTime: time.Now().UnixMilli()}
	id := writeLegacyMeetingEntry(t, app, session, "silent-call", "## Transcript\n\nA full call.\n")
	now := time.Now()
	storeEvent(t, app.store, calendar.Event{ID: "calendar-call", Title: "Design review", Start: now, End: now.Add(time.Hour), ConferenceURL: "https://example.com/meeting"})
	brief := buildMobileBriefV1(mobileBriefInput{AsOf: now, WindowStart: now.Add(-time.Hour), Location: time.UTC,
		Meetings: []mobileBriefCandidate{
			{SourceKind: mobileBriefMeetingAppSource, SourceID: session.ID, Title: "Recorded call", At: now},
			{SourceKind: mobileBriefCalendarSource, SourceID: "calendar-call", Title: "Design review", Detail: "Zoom; notes ready; 1 decisions, 1 follow-ups for you", At: now},
		},
		Learned: []mobileBriefCandidate{{SourceKind: "knowledge", SourceID: id, Title: "New call transcript", At: now}},
	})
	brief.Narrative = []MobileBriefPara{{Text: "Your meeting notes are ready."}}
	brief.NarrativeStatus = "ok"
	brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
	got := app.mobileBriefForDelivery(brief)
	if len(got.Sections.Learned) != 0 || len(got.Sections.Meetings) != 1 || got.Sections.Meetings[0].Title != "Design review" {
		t.Fatalf("cached call announced itself: %+v", got.Sections)
	}
	if strings.Contains(got.Sections.Meetings[0].Detail, "notes ready") || !strings.Contains(got.Sections.Meetings[0].Detail, "1 decisions") {
		t.Fatalf("calendar recording announcement survived: %+v", got.Sections.Meetings[0])
	}
	if len(got.Narrative) != 0 || got.Audio != nil {
		t.Fatal("old recording announcement survived in synthesis/audio")
	}
}
