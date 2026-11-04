package main

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
	"yansu-agent/agent"
)

type threadOutcomeFixture struct {
	Name, Kind, State, Summary, NextStep, Action, Question, UserActed, Typed string
	Frames                                                                   []string
}

func threadOutcomeFixtures(t *testing.T) []threadOutcomeFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/brief-outcomes/threads.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []threadOutcomeFixture
	if json.Unmarshal(raw, &out) != nil {
		t.Fatal("invalid fixture")
	}
	return out
}

func threadFixtureReply(f threadOutcomeFixture, evidence []threadOutcomeEvidence) string {
	refs := []map[string]string{}
	// Cite the final screen and the typed conclusion/action, when present.
	for i, e := range evidence {
		if e.SourceKind == "activity_event" || i == len(evidence)-1 || (i == len(evidence)-2 && evidence[i+1].SourceKind == "activity_event") {
			refs = append(refs, map[string]string{"ref": e.Ref, "quote": e.Text})
		}
	}
	answerRef := ""
	if f.Kind == "assistant" {
		for _, e := range evidence {
			if e.SourceKind == "activity_snapshot" {
				answerRef = e.Ref
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{"answerRef": answerRef, "kind": f.Kind, "state": f.State, "summary": f.Summary, "nextStep": f.NextStep, "action": f.Action, "question": f.Question, "userActed": f.UserActed, "confidence": .92, "evidence": refs})
	return string(raw)
}

func seedThreadFixture(t *testing.T, app *App, id string, f threadOutcomeFixture, now time.Time) ActivityRow {
	t.Helper()
	store := app.activity.store
	start := now.Add(-time.Hour).UnixMilli()
	end := now.Add(-time.Minute).UnixMilli()
	events := []activitymon.ActivityEvent{{Kind: activitymon.EventFocus, Timestamp: start, AppName: "Notes", AppBundleID: "com.apple.Notes", WindowTitle: "RTK work"}}
	if f.Typed != "" {
		events = append(events, activitymon.ActivityEvent{Kind: activitymon.EventKeyboard, Timestamp: end, AppName: "Notes", AppBundleID: "com.apple.Notes", TypedText: f.Typed, TypedTextCaptureMethod: "ax-diff"})
	}
	sessionID := "session-" + id
	if err := store.SaveSession(ActivitySessionRow{ID: sessionID, StartTime: start, EndTime: end, AppNames: `["Notes"]`, TriageStatus: "done"}, events, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("run-"+id, "Notes", start, end, end-start, len(events))
	run.SessionIDs = []string{sessionID}
	if err := store.db.QueryRow(`SELECT MIN(id),MAX(id) FROM activity_events WHERE session_id=?`, sessionID).Scan(&run.FirstEventRowID, &run.LastEventRowID); err != nil {
		t.Fatal(err)
	}
	insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{Title: "RTK work"})
	for i, text := range f.Frames {
		result, err := store.db.Exec(`INSERT INTO activity_snapshots(session_id,timestamp,file_path,app,storage_tier) VALUES(?,?,'','Notes','hot')`, sessionID, start+int64(i+1)*1000)
		if err != nil {
			t.Fatal(err)
		}
		snapshotID, _ := result.LastInsertId()
		if err := store.SaveOCRFrame(OCRFrameRow{SessionID: sessionID, SegmentID: snapshotID, FrameTime: float64(i + 1), Text: text, Confidence: .99}); err != nil {
			t.Fatal(err)
		}
	}
	row := ActivityRow{ID: id, Title: "RTK work", Status: activityStatusIdle, RunIDs: composeJSON([]string{run.ID}), ActiveMsByApp: `{"Notes":60000}`, StartMs: start, EndMs: end, CreatedAt: start, UpdatedAt: end}
	if err := store.InsertActivity(row); err != nil {
		t.Fatal(err)
	}
	return row
}

func saveFixtureOutcome(t *testing.T, app *App, row ActivityRow, f threadOutcomeFixture) ActivityRow {
	t.Helper()
	evidence, ok := app.threadOutcomeEvidence(t.Context(), row)
	if !ok || len(evidence) == 0 {
		t.Fatalf("missing evidence: %+v", evidence)
	}
	outcome, err := parseThreadOutcome(threadFixtureReply(f, evidence), evidence)
	if err != nil || outcome == nil {
		t.Fatalf("parse outcome: %+v %v", outcome, err)
	}
	record := activityThreadOutcomeRecord{InputHash: threadOutcomeInputHash(row, evidence), AttemptedAt: row.EndMs, Outcome: outcome}
	if err := app.activity.store.saveThreadOutcome(t.Context(), row, record); err != nil {
		t.Fatal(err)
	}
	saved, err := app.activity.store.GetActivityByID(row.ID)
	if err != nil || saved == nil {
		t.Fatal(err)
	}
	return *saved
}

func TestThreadOutcomeOCRFixtures(t *testing.T) {
	for _, f := range threadOutcomeFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newPrivacyReadTestApp(t)
			now := mobileBriefTestDay(18, 0)
			row := seedThreadFixture(t, app, "thread", f, now)
			evidence, ok := app.threadOutcomeEvidence(t.Context(), row)
			if !ok {
				t.Fatal("safe source held")
			}
			prompt := buildThreadOutcomePrompt(evidence, "English")
			if strings.Contains(prompt, "SG-987654321") {
				t.Fatal("order number reached provider")
			}
			if !strings.Contains(prompt, threadOutcomeText(f.Frames[len(f.Frames)-1], threadOutcomeTextBytes)) {
				t.Fatal("final screen missing")
			}
			if f.Typed != "" && !strings.Contains(prompt, f.Typed) {
				t.Fatal("typed conclusion missing")
			}
			row = saveFixtureOutcome(t, app, row, f)
			outcome := app.readableThreadOutcome(t.Context(), row)
			if outcome == nil || outcome.Kind != f.Kind || outcome.State != f.State || len(outcome.Evidence) == 0 {
				t.Fatalf("outcome=%+v", outcome)
			}
			raw, _ := json.Marshal(outcome)
			if strings.Contains(string(raw), "SG-987654321") {
				t.Fatal("order number stored")
			}
			if f.State == "order_placed" && !strings.Contains(outcome.Summary, "[masked]") {
				t.Fatal("missing order mask")
			}
			in := mobileBriefInput{Slot: "evening", AsOf: now, WindowStart: now.Add(-12 * time.Hour), Location: time.UTC}
			collectMobileBriefRecap(app, &in)
			if len(in.Moved) != 1 || in.Moved[0].Outcome == nil {
				t.Fatalf("collector lost outcome: %+v", in.Moved)
			}
			brief := app.mobileBriefForDelivery(buildMobileBriefV1(in))
			languages := []string{"English", "Chinese"}
			if mobileBriefOutcomeAnchor(outcome) == "" {
				// An answer from an AI tool, or research, is what the user
				// worked on: it stays in Moved and never reaches the brief text
				// or its attention (mobile_brief_anchor.go).
				moved := brief.Sections.Moved
				if len(moved) != 1 || moved[0].Outcome == nil || moved[0].AttentionReason != "" || len(brief.Sections.Highlights) != 0 {
					t.Fatalf("activity-only outcome left What you worked on: %+v", brief.Sections)
				}
				for _, language := range []string{"English", "Chinese"} {
					for _, paragraph := range mobileBriefNarrativeTemplate(brief, language) {
						for _, ref := range paragraph.Refs {
							if slices.Contains(moved[0].Refs, ref) {
								t.Fatalf("brief text cites activity-only outcome: %+v", paragraph)
							}
						}
					}
				}
				languages = nil
			}
			for _, language := range languages {
				prompt := mobileBriefTestPrompt(brief, language)
				paragraphs := mobileBriefNarrativeTemplate(brief, language)
				if len(paragraphs) == 0 || !mobileBriefNarrativeWithinBudget(paragraphs) {
					t.Fatalf("empty/long template: %+v", paragraphs)
				}
				text := mobileBriefSpokenText(brief, language)
				if mobileBriefBannedOutcomePhrase(text) || strings.Contains(text, "I saw work on") {
					t.Fatalf("weak copy: %s", text)
				}
				if f.NextStep != "" && !strings.Contains(text, f.NextStep) {
					t.Fatalf("lost next step: %s", text)
				}
				// Since backlog item 65 an observed thread, anchored or not,
				// never reaches the written brief's prompt.
				if f.NextStep != "" && strings.Contains(prompt, f.NextStep) {
					t.Fatalf("an observed thread reached the prompt: %s", prompt)
				}
				if err := validateMobileBriefNarrativeVoice(brief, paragraphs, language); err != nil {
					t.Fatal(err)
				}
			}
			store := newMobileBriefStore(t.TempDir())
			if err := store.save(brief, now); err != nil {
				t.Fatal(err)
			}
			loaded, ok := store.load(brief.BriefID)
			if !ok || len(app.mobileBriefForDelivery(loaded).Sections.Moved) != 1 {
				t.Fatal("stored delivery lost outcome")
			}
		})
	}
}

func TestThreadOutcomeRejectsUnsupportedOrStaleEvidence(t *testing.T) {
	f := threadOutcomeFixtures(t)[0]
	evidence := []threadOutcomeEvidence{{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f1", SourceKind: "activity_snapshot"}, Text: f.Frames[1]}}
	good := threadFixtureReply(f, evidence)
	for _, raw := range []string{strings.Replace(good, `"f1"`, `"f99"`, 1), strings.Replace(good, f.Frames[1], "Order placed", 1), strings.Replace(good, `0.92`, `0.2`, 1), strings.Replace(good, `cart_left`, `unknown`, 1), strings.Replace(good, "Order the sailing gloves.", "Order the sailing gloves?", 1)} {
		if _, err := parseThreadOutcome(raw, evidence); err == nil {
			t.Fatalf("accepted unsupported reply %s", raw)
		}
	}
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	row := seedThreadFixture(t, app, "thread", f, mobileBriefTestDay(18, 0))
	row = saveFixtureOutcome(t, app, row, f)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_ocr_frames SET text='Your order was cancelled' WHERE session_id=?`, "session-thread"); err != nil {
		t.Fatal(err)
	}
	if app.readableThreadOutcome(t.Context(), row) != nil {
		t.Fatal("changed OCR retained old outcome")
	}
}

func TestThreadOutcomePrivacyAndWithdrawal(t *testing.T) {
	for _, state := range []string{"closed", "paused", "historical-pause", "sensitive", "protected", "private", "mail", "mail-failure", "mail-echo", "browser-mail", "excluded", "deleted", "membership", "purged-frame", "pii"} {
		t.Run(state, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newPrivacyReadTestApp(t)
			now := mobileBriefTestDay(18, 0)
			f := threadOutcomeFixtures(t)[0]
			row := seedThreadFixture(t, app, "thread", f, now)
			row = saveFixtureOutcome(t, app, row, f)
			in := mobileBriefInput{Slot: "evening", AsOf: now, WindowStart: now.Add(-12 * time.Hour), Location: time.UTC}
			collectMobileBriefRecap(app, &in)
			brief := buildMobileBriefV1(in)
			sourceID := threadOutcomeSourceID(row.ID, row.ThreadOutcome)
			switch state {
			case "closed":
				app.closedEyes.enabled.Store(true)
			case "paused":
				app.saveObservationPauses([]observationPause{{Start: now.Add(-2 * time.Hour).UnixMilli()}})
			case "historical-pause":
				app.saveObservationPauses([]observationPause{{Start: row.StartMs - 1, End: row.EndMs + 1}})
			case "sensitive":
				pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
			case "protected":
				_, _ = app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected'`)
			case "private":
				_, _ = app.activity.store.db.Exec(`UPDATE activity_events SET data=json_set(data,'$.privateWindow',json('true'))`)
			case "mail":
				_, _ = app.activity.store.db.Exec(`UPDATE activity_sessions SET app_names='["Mail"]'`)
			case "mail-failure":
				_ = app.store.Close()
			case "browser-mail":
				_, _ = app.activity.store.db.Exec(`UPDATE activity_snapshots SET browser_url='https://mail.google.com/mail/u/0'`)
			case "mail-echo":
				_, err := app.store.UpsertMailMessages(t.Context(), []storage.MailMessageRow{{ID: "mail", Mailbox: storage.MailboxInbox, Subject: "sailing gloves", At: now.UnixMilli()}}, now.UnixMilli())
				if err != nil {
					t.Fatal(err)
				}
			case "excluded":
				_, _ = app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=1`)
			case "deleted":
				_, _ = app.activity.store.db.Exec(`DELETE FROM activity_sessions`)
			case "membership":
				row.RunIDs = `[]`
				if err := app.activity.store.UpdateActivity(row); err != nil {
					t.Fatal(err)
				}
			case "purged-frame":
				_, _ = app.activity.store.db.Exec(`DELETE FROM activity_snapshots`)
			case "pii":
				_, _ = app.activity.store.db.Exec(`UPDATE activity_ocr_frames SET text=?`, "API key "+activityPrivacySecret)
			}
			if outcome := app.readableThreadOutcome(t.Context(), row); outcome != nil {
				t.Fatalf("%s retained outcome: %+v", state, outcome)
			}
			if app.threadOutcomeSourceReadable(t.Context(), sourceID) {
				t.Fatal("delivery retained held source")
			}
			filtered := app.mobileBriefForDelivery(brief)
			raw, _ := json.Marshal(filtered)
			if strings.Contains(string(raw), "sailing gloves") {
				t.Fatal("brief retained withdrawn outcome")
			}
			if state != "pii" {
				_, _ = app.activity.store.db.Exec(`UPDATE activities SET thread_outcome=''`)
				runner := &threadOutcomeTestRunner{reply: "null"}
				app.runner = runner
				app.fillThreadOutcomes(t.Context(), now, time.UTC)
				if runner.calls != 0 {
					t.Fatal("held source reached extractor")
				}
			}
		})
	}
}

type threadOutcomeTestRunner struct {
	activitySummaryI18nFakeRunner
	reply string
	check func(*agent.TaskInput)
	hook  func()
}

func (r *threadOutcomeTestRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.calls++
	if r.check != nil {
		r.check(input)
	}
	if r.hook != nil {
		r.hook()
	}
	return "outcome-test", callback(&agent.StreamEvent{Type: "assistant", Content: r.reply})
}

func TestThreadOutcomeWorkerBoundedAndRechecksPrivacy(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := mobileBriefTestDay(18, 0)
	f := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, app, "thread", f, now)
	evidence, _ := app.threadOutcomeEvidence(t.Context(), row)
	runner := &threadOutcomeTestRunner{reply: threadFixtureReply(f, evidence), check: func(input *agent.TaskInput) {
		if !input.NoTools || !input.NoSystem || !input.Ephemeral || input.AutoApproveTools || !input.SuppressTelemetry {
			t.Fatal("extractor gained tools/authority")
		}
	}}
	app.runner = runner
	app.fillThreadOutcomes(t.Context(), now, time.UTC)
	saved, _ := app.activity.store.GetActivityByID(row.ID)
	if saved.ThreadOutcome == nil {
		t.Fatal("worker did not persist outcome")
	}
	app.fillThreadOutcomes(t.Context(), now, time.UTC)
	if runner.calls != 1 {
		t.Fatal("unchanged input called model twice")
	}
	// Retry/changed-input path must discard a response if the source is held mid-call.
	_, _ = app.activity.store.db.Exec(`UPDATE activities SET thread_outcome=''`)
	runner.hook = func() { app.closedEyes.enabled.Store(true) }
	app.fillThreadOutcomes(t.Context(), now, time.UTC)
	saved, _ = app.activity.store.GetActivityByID(row.ID)
	if saved.ThreadOutcome != nil {
		t.Fatal("privacy changed during provider call")
	}
}

func TestThreadOutcomeProactiveCandidateDedupesAndWithdraws(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := mobileBriefTestDay(18, 0)
	f := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, app, "thread", f, now)
	row = saveFixtureOutcome(t, app, row, f)
	a, ok := threadOutcomeMoment(row, row.ThreadOutcome, now)
	b, _ := threadOutcomeMoment(row, row.ThreadOutcome, now.Add(time.Hour))
	if !ok || a.Kind != engagement.KindDecisionDue || a.DedupeKey != b.DedupeKey || a.HardDeadline {
		t.Fatalf("bad candidate %+v", a)
	}
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	app.engagement.now = func() time.Time { return now }
	app.engagement.sourceAllowsRead = app.threadOutcomeMomentAllowsRead
	app.engagement.threadOutcomeCopy = app.renderThreadOutcomeMoment
	app.produceThreadOutcome(t.Context(), row, row.ThreadOutcome, now)
	app.produceThreadOutcome(t.Context(), row, row.ThreadOutcome, now.Add(time.Hour))
	items, err := app.store.ListEngagementMoments(t.Context(), storage.EngagementStatePending, 10)
	if err != nil || len(items) != 1 || items[0].DedupeKey != a.DedupeKey {
		t.Fatalf("dedupe %+v %v", items, err)
	}
	if strings.Contains(items[0].Params, "sailing gloves") {
		t.Fatal("pending moment stored outcome text")
	}
	resolved, err := app.resolveTodayChatItem(t.Context(), TodayChatContextRef{SourceKind: mobileBriefRecapSource, SourceID: a.Params["_activity_outcome"]}, todayChatSources{}, now)
	if err != nil || !strings.Contains(resolved.Record.Text, f.NextStep) {
		t.Fatalf("chat lost follow-through: %+v %v", resolved, err)
	}
	spec, _ := engagement.Spec(a.Kind)
	copy := app.engagement.render(spec, a)
	if !strings.Contains(copy.Title, f.NextStep) || strings.Contains(copy.Title, "permission") {
		t.Fatalf("wrong follow-through %s", copy.Title)
	}
	item, ok := app.proactiveItem(a, copy)
	if !ok || item.ActivityOutcomeSource == "" || strings.Contains(strings.Join(item.ActionIDs, ","), "approve") {
		t.Fatalf("invalid card %+v", item)
	}
	// Open lands on the observed activity thread, never on Today (which is a
	// no-op while the Chat panel already sits beside Today).
	if item.Target.Kind != mainChatTargetThread || item.Target.ID != row.ID {
		t.Fatalf("thread outcome target = %+v, want thread %s", item.Target, row.ID)
	}
	for _, lang := range engagement.Languages {
		if copy := item.Localized[lang]; !strings.Contains(copy.Headline+" "+copy.Body, strings.TrimRight(f.NextStep, ".!?。")) {
			t.Fatalf("lost localized step %+v", item)
		}
	}
	app.closedEyes.enabled.Store(true)
	if app.threadOutcomeMomentAllowsRead(t.Context(), a) {
		t.Fatal("held candidate allowed delivery")
	}
	raw, _ := json.Marshal(item)
	content, input := app.projectProactiveVoiceDelivery(t.Context(), mainChatProactiveTool, item.Body, string(raw))
	if strings.Contains(content+input, "sailing gloves") || strings.Contains(content+input, "session-thread") {
		t.Fatal("stored chat leaked withdrawn source")
	}
}

func TestThreadOutcomeBannedPromptOutputAndTemplate(t *testing.T) {
	for _, phrase := range []string{"I can't tell", "I can’t see", "worth a quick look to see if it's settled", "I cannot tell", "I could not tell"} {
		in := mobileBriefGoldenInput()
		in.Moved = []mobileBriefCandidate{{SourceKind: "history", SourceID: "safe", Title: phrase, Status: "done"}}
		brief := buildMobileBriefV1(in)
		// The model's sentence goes, whatever else its reply says.
		var refs []string
		for _, ref := range mobileBriefNarrativeInputEvidence(brief) {
			refs = append(refs, ref.Ref)
		}
		raw, _ := json.Marshal(mobileBriefNarrativeReply{Paragraphs: []mobileBriefNarrativeReplyParagraph{{Text: phrase + " whether you reached a conclusion.", Refs: refs}}})
		if paragraphs, _, err := mobileBriefNarrativeFromReply(brief, mobileBriefPicks(brief), string(raw), "English"); err == nil {
			for _, p := range paragraphs {
				if mobileBriefBannedOutcomePhrase(p.Text) {
					t.Fatalf("model output accepted %q", phrase)
				}
			}
		}
		for _, p := range mobileBriefNarrativeTemplate(brief, "English") {
			if mobileBriefBannedOutcomePhrase(p.Text) {
				t.Fatalf("template accepted %s", p.Text)
			}
		}
	}
	paragraphs := []MobileBriefPara{{Text: strings.Repeat("word ", mobileBriefMaxWords+1), Refs: []string{"e1"}}}
	if mobileBriefNarrativeWithinBudget(paragraphs) {
		t.Fatal("overlong English brief accepted")
	}
	paragraphs[0].Text = strings.Repeat("字", 2*mobileBriefMaxWords+1)
	if mobileBriefNarrativeWithinBudget(paragraphs) {
		t.Fatal("overlong Chinese brief accepted")
	}
}

func TestThreadOutcomeTailBoundsAndNoSiblingOCR(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := mobileBriefTestDay(18, 0)
	f := threadOutcomeFixtures(t)[0]
	f.Frames = []string{strings.Repeat("earlier material ", 600) + "FINAL: gloves remain in the cart"}
	row := seedThreadFixture(t, app, "tail", f, now)
	evidence, ok := app.threadOutcomeEvidence(t.Context(), row)
	if !ok || len(evidence) != 1 || len(evidence[0].Text) > threadOutcomeTextBytes || !strings.HasSuffix(evidence[0].Text, "FINAL: gloves remain in the cart") {
		t.Fatalf("tail lost/budget broken %+v", evidence)
	}
	// An OCR-less later frame must not borrow the previous cart screen.
	_, err := app.activity.store.db.Exec(`INSERT INTO activity_snapshots(session_id,timestamp,file_path,app,storage_tier) VALUES(?,?,'','Notes','hot')`, "session-tail", row.EndMs)
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok = app.threadOutcomeEvidence(t.Context(), row)
	if !ok || len(evidence) != 2 || evidence[1].Text != threadOutcomeNoText {
		t.Fatalf("sibling OCR was copied: %+v", evidence)
	}
}

func TestThreadOutcomeWorkerCallCapAndFailedInputBackoff(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := mobileBriefTestDay(18, 0)
	f := threadOutcomeFixtures(t)[0]
	for i := 0; i < 8; i++ {
		seedThreadFixture(t, app, strconv.Itoa(i), f, now)
	}
	runner := &threadOutcomeTestRunner{reply: "null"}
	app.runner = runner
	app.fillThreadOutcomes(t.Context(), now, time.UTC)
	if runner.calls != 6 {
		t.Fatalf("calls=%d, wanted bounded six", runner.calls)
	}
	app.fillThreadOutcomes(t.Context(), now, time.UTC)
	if runner.calls != 8 {
		t.Fatalf("remaining page starved or null retried: %d", runner.calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	app.fillThreadOutcomes(ctx, now, time.UTC)
	if runner.calls != 8 {
		t.Fatal("cancelled pass called provider")
	}
}

func TestThreadOutcomeQuestionNeedsIncompleteEvidence(t *testing.T) {
	in := mobileBriefGoldenInput()
	in.Moved = []mobileBriefCandidate{{SourceKind: "summary", SourceID: "outcome", Title: "The gloves are in the cart", Kind: mobileBriefThreadOutcomeSource, Status: "needs_review", Outcome: &ActivityThreadOutcome{Kind: "purchase", State: "cart_left", NextStep: "Order the gloves at 08:15", Summary: "The gloves were in the cart at 08:00"}}}
	brief := buildMobileBriefV1(in)
	prompt := mobileBriefTestPrompt(brief, "English")
	if strings.Contains(prompt, "08:15") || strings.Contains(prompt, "08:00") {
		t.Fatal("past clocks reached outcome prompt")
	}
	item := brief.Sections.Review[0]
	if err := validateMobileBriefNarrativeVoice(brief, []MobileBriefPara{{Text: "Did you reach a conclusion?", Refs: item.Refs}}, "English"); err == nil {
		t.Fatal("known outcome prompted a generic question")
	}
}

func TestThreadOutcomeMasksOrderNumbersBeforeTruncation(t *testing.T) {
	for _, value := range []string{"Order number SG-987654321", "Order #SG-987654321", "订单编号：SG-987654321", "訂單號：SG-987654321"} {
		redacted := threadOutcomeText(value, 200)
		if strings.Contains(redacted, "SG-987654321") || !strings.Contains(redacted, "[masked]") {
			t.Fatalf("order ID escaped: %s", redacted)
		}
	}
}

func TestThreadOutcomeAssistantActionPrecedesFinalDocumentFrame(t *testing.T) {
	f := threadOutcomeFixtures(t)[4]
	evidence := []threadOutcomeEvidence{
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f1", SourceKind: "activity_snapshot", ObservedAt: 100}, Text: "ChatGPT: Include the RTK copyright notice when bundling."},
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f2", SourceKind: "activity_event", ObservedAt: 200}, Text: f.Typed},
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f3", SourceKind: "activity_snapshot", ObservedAt: 300}, Text: "THIRD_PARTY_LICENSES: RTK copyright notice. All changes saved."},
	}
	var reply map[string]any
	_ = json.Unmarshal([]byte(threadFixtureReply(f, evidence)), &reply)
	reply["answerRef"] = "f1"
	refs := []map[string]string{}
	for _, e := range evidence {
		refs = append(refs, map[string]string{"ref": e.Ref, "quote": e.Text})
	}
	reply["evidence"] = refs
	raw, _ := json.Marshal(reply)
	if _, err := parseThreadOutcome(string(raw), evidence); err != nil {
		t.Fatalf("later document hid observed follow-through: %v", err)
	}
	evidence[1].ObservedAt = 50
	if _, err := parseThreadOutcome(string(raw), evidence); err == nil {
		t.Fatal("pre-answer user text became follow-through")
	}
}

func TestThreadOutcomeProactiveKeepsContentHoldbacks(t *testing.T) {
	outcome := &ActivityThreadOutcome{State: "cart_left", Summary: "The sailing gloves are still in the cart.", NextStep: "Order the gloves.", Action: "order", Confidence: .95}
	for _, text := range []string{"Prescription refill", "Credit card payment pending", "诊断记录"} {
		if _, ok := threadOutcomeMoment(ActivityRow{ID: "thread", Title: text}, outcome, mobileBriefTestDay(18, 0)); ok {
			t.Fatalf("restricted topic became a proactive candidate: %s", text)
		}
	}
	if _, ok := threadOutcomeMoment(ActivityRow{ID: "thread", Title: "Sailing gloves"}, outcome, mobileBriefTestDay(18, 0)); !ok {
		t.Fatal("ordinary shopping follow-through was lost")
	}
}

func TestThreadOutcomeSameTimestampKeepsNewestFrame(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := mobileBriefTestDay(18, 0)
	f := threadOutcomeFixtures(t)[0]
	f.Frames = nil
	for i := 1; i <= 14; i++ {
		f.Frames = append(f.Frames, "Visible state "+strconv.Itoa(i))
	}
	row := seedThreadFixture(t, app, "same-time", f, now)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_snapshots SET timestamp=?`, row.EndMs); err != nil {
		t.Fatal(err)
	}
	evidence, ok := app.threadOutcomeEvidence(t.Context(), row)
	if !ok || len(evidence) != 6 || evidence[len(evidence)-1].Text != "Visible state 14" {
		t.Fatalf("same-time frames lost final state: %+v", evidence)
	}
}

// Backlog item 53: the 8am brief offered "I can draft the next steps for “You
// typed, "i used reset on codex, we can ask them to resume now."”". Typed
// input is evidence of what the user did, never the words of a loop title.
func TestThreadOutcomeNeverTurnsTypedInputIntoATitle(t *testing.T) {
	typed := "i used reset on codex, we can ask them to resume now."
	f := threadOutcomeFixture{Name: "typed into a terminal", Kind: "document", State: "draft", Typed: typed,
		Frames: []string{"Codex - session", "Codex: session paused after reset"}}
	evidence := []threadOutcomeEvidence{
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f1", SourceKind: "activity_snapshot"}, Text: f.Frames[1]},
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f2", SourceKind: "activity_event"}, Text: typed},
	}
	for _, summary := range []string{
		`You typed, "i used reset on codex, we can ask them to resume now."`,
		"I used reset on Codex; we can ask them to resume now.",
		"The note says “we can ask them to resume now”.",
		"你输入了一条关于 Codex 重置的消息。",
		"用户输入了一条关于 Codex 重置的消息。",
		"They typed a note about resuming Codex.",
		"Typed a reply about resuming Codex.",
		"A message was typed into Codex about the reset.",
		"You had typed a note about the Codex reset.",
		"You've typed a note about the Codex reset.",
		"You were typing a note about the Codex reset.",
		"你在搜索框里输入了一条关于 Codex 的消息。",
	} {
		f.Summary = summary
		if _, err := parseThreadOutcome(threadFixtureReply(f, evidence), evidence); err == nil {
			t.Errorf("typed input became the summary: %q", summary)
		}
	}
	for _, ordinary := range []string{"Plan saved. Typed notes for Q3 review are in Notes.", "你还需要输入收货地址才能下单。"} {
		if threadOutcomeTypingNarration.MatchString(ordinary) {
			t.Errorf("ordinary progress taken for typing narration: %q", ordinary)
		}
	}
	if !threadOutcomeTypingNarration.MatchString("你刚才输入的内容是关于 Codex 的。") {
		t.Error("zh narration without 了 was missed")
	}
	// Describing the work in Isoform's own words stays allowed, and a settled
	// conclusion may still restate what the user typed.
	f.Summary = "The Codex session is paused after a reset."
	if outcome, err := parseThreadOutcome(threadFixtureReply(f, evidence), evidence); err != nil || outcome == nil {
		t.Fatalf("paraphrase rejected: %v", err)
	}
	// Progress described in Isoform's words stays: entering an address is not
	// narrating typed text.
	checkout := threadOutcomeFixture{Name: "checkout", Kind: "purchase", State: "checkout_started", Typed: "12 Pine Street",
		Summary: "You stopped at checkout after you entered the address.", NextStep: "Finish the order.", Action: "order",
		Frames: []string{"Checkout", "Checkout: shipping address. Payment. Place order"}}
	checkoutEvidence := []threadOutcomeEvidence{
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f1", SourceKind: "activity_event"}, Text: checkout.Typed},
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f2", SourceKind: "activity_snapshot"}, Text: checkout.Frames[1]},
	}
	if _, err := parseThreadOutcome(threadFixtureReply(checkout, checkoutEvidence), checkoutEvidence); err != nil {
		t.Fatalf("ordinary progress rejected: %v", err)
	}
	// A long verbatim run is rejected even inside a longer summary.
	f.Summary = "Worked on the Codex session after a reset; I used reset on codex, we can ask them to resume now, and the note is still open."
	if _, err := parseThreadOutcome(threadFixtureReply(f, evidence), evidence); err == nil {
		t.Error("a typed sentence inside a long summary was accepted")
	}
	// A cart summary names the product the user typed into the store's search.
	product := "Anker USB C Charger 40W Nano Pro Foldable Fast Charging Block"
	cart := threadOutcomeFixture{Name: "typed product search", Kind: "purchase", State: "cart_left", Typed: product,
		Summary: "The " + product + " is still in the cart.", NextStep: "Order the hoody.", Action: "order",
		Frames: []string{"Search results", "Your cart: " + product + ". Proceed to checkout"}}
	cartEvidence := []threadOutcomeEvidence{
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f1", SourceKind: "activity_event"}, Text: product},
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f2", SourceKind: "activity_snapshot"}, Text: cart.Frames[1]},
	}
	if _, err := parseThreadOutcome(threadFixtureReply(cart, cartEvidence), cartEvidence); err != nil {
		t.Fatalf("cart summary naming a typed product rejected: %v", err)
	}
	conclusion := threadOutcomeFixtures(t)[8]
	conclusionEvidence := []threadOutcomeEvidence{
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f1", SourceKind: "activity_snapshot"}, Text: conclusion.Frames[0]},
		{ActivityThreadOutcomeRef: ActivityThreadOutcomeRef{Ref: "f2", SourceKind: "activity_event"}, Text: conclusion.Typed},
	}
	if _, err := parseThreadOutcome(threadFixtureReply(conclusion, conclusionEvidence), conclusionEvidence); err != nil {
		t.Fatalf("settled conclusion rejected: %v", err)
	}
	if !strings.Contains(buildThreadOutcomePrompt(evidence, "English"), `never quote, repeat or narrate what they typed`) {
		t.Fatal("prompt does not forbid quoting typed input")
	}

	// A projection saved before this check is withheld on read, so the loop
	// and its offer leave the brief without a re-extraction.
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := time.Now()
	f.Summary = "The Codex session is paused after a reset."
	row := seedThreadFixture(t, app, "typed", f, now.AddDate(0, 0, -3))
	row = saveFixtureOutcome(t, app, row, f)
	if app.readableThreadOutcome(t.Context(), row) == nil {
		t.Fatal("paraphrased outcome withheld")
	}
	// The collector does see this thread, so the check below is not vacuous.
	control := mobileBriefInput{Slot: "morning", AsOf: now, WindowStart: now.Add(-12 * time.Hour), Location: time.UTC}
	collectMobileBriefOlderThreads(t.Context(), app, &control)
	if len(control.OpenLoops) != 1 || control.OpenLoops[0].Title != f.Summary {
		t.Fatalf("collector did not see the paraphrased thread: %+v", control.OpenLoops)
	}
	stored, ok := app.threadOutcomeEvidence(t.Context(), row)
	if !ok {
		t.Fatal("missing evidence")
	}
	legacy := &ActivityThreadOutcome{Version: threadOutcomeVersion, Kind: "document", State: "draft", Confidence: .9,
		Summary: `You typed, "` + typed + `"`, Evidence: []ActivityThreadOutcomeRef{stored[len(stored)-1].ActivityThreadOutcomeRef}}
	if err := app.activity.store.saveThreadOutcome(t.Context(), row, activityThreadOutcomeRecord{InputHash: threadOutcomeInputHash(row, stored), AttemptedAt: row.EndMs, Outcome: legacy}); err != nil {
		t.Fatal(err)
	}
	saved, err := app.activity.store.GetActivityByID(row.ID)
	if err != nil || saved == nil {
		t.Fatal(err)
	}
	if app.readableThreadOutcome(t.Context(), *saved) != nil {
		t.Fatal("stored typed-input summary was replayed")
	}
	in := mobileBriefInput{Slot: "morning", AsOf: now, WindowStart: now.Add(-12 * time.Hour), Location: time.UTC}
	collectMobileBriefOlderThreads(t.Context(), app, &in)
	brief := buildMobileBriefV1(in)
	brief.Narrative = mobileBriefNarrativeTemplate(brief, "en")
	_, brief.Offers = mobileBriefClosing(brief, "en")
	raw, _ := json.Marshal(brief)
	if strings.Contains(string(raw), "You typed") || strings.Contains(string(raw), "resume now") {
		t.Fatalf("typed input reached the brief: %s", raw)
	}
}
