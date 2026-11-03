package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/proactivevoice"
	"wails-gui/storage"
	"yansu-agent/agent"
)

func voiceMailSource() proactiveVoiceSource {
	return proactiveVoiceSource{
		Items:        []proactivevoice.Item{{Ref: "storage-mail-id", Type: "mail_thread", FromName: proactivevoice.Text("Dana Ruiz"), WaitingSince: &proactivevoice.Display{Display: "Tuesday, Oct 13"}}},
		People:       []proactivevoice.Person{{Ref: "storage-person-id", Name: proactivevoice.Text("Dana Ruiz"), RoleLabel: "a colleague"}},
		ContactNames: []string{"Dana Ruiz"}, RawMailSubject: "Design review", RawMailBody: "Please review the color choices.",
		DraftRecipient: "dana@example.com", DraftSubject: "Re: Design review", DraftLanguage: "en", WritingSamples: []string{"Thanks, Dana."},
	}
}

func TestProactiveVoiceAssemblerConsentFlagsAndAddress(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 12, 0, 0, time.UTC)
	source := voiceMailSource()
	for _, consent := range []bool{false, true} {
		source.MailBodyConsent = consent
		job, addresses, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, now, source)
		if reason != "" {
			t.Fatal(reason)
		}
		encoded, _ := json.Marshal(job)
		text := string(encoded)
		for _, secret := range []string{"dana@example.com", "storage-mail-id", "storage-person-id"} {
			if strings.Contains(text, secret) {
				t.Fatalf("provider job leaked %s", secret)
			}
		}
		if !job.DraftAllowed || job.DraftTo != "addr_1" || addresses["addr_1"] != "dana@example.com" {
			t.Fatalf("opaque header-only draft mapping missing: consent=%v", consent)
		}
		if !consent && (strings.Contains(text, `"body_excerpt"`) || strings.Contains(text, `"writing_samples"`)) {
			t.Fatal("mail body content crossed header consent")
		}
		if consent && job.HeadlineSubject == nil {
			t.Fatal("body-consent headline subject missing")
		}
		if consent {
			item := mainChatProactive{}
			output := proactivevoice.Output{Headline: "Ready", Body: "Review", Actions: job.Actions, Draft: &proactivevoice.Draft{To: "addr_1", Subject: job.DraftSubject, Text: "Thank you for your patience."}}
			if !applyProactiveVoiceResult(&item, job, output, addresses) || item.DraftContent.To != "dana@example.com" {
				t.Fatal("address was not restored locally")
			}
			output.Draft.To = "addr_2"
			if applyProactiveVoiceResult(&item, job, output, addresses) {
				t.Fatal("unmapped token accepted")
			}
		}
	}
	for _, ask := range []struct{ text, flag string }{{"Please pay the invoice", "money"}, {"请把验证码给我", "code"}, {"Send your password", "password"}, {"点击这个链接", "link"}} {
		source.RawMailBody = ask.text
		job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, now, source)
		if reason != "" || job.MailFlag != ask.flag || job.DraftAllowed || job.Items[0].Subject != nil || job.Items[0].BodyExcerpt != nil || len(job.WritingSamples) != 0 {
			t.Fatalf("flag %s failed: %s %+v", ask.flag, reason, job)
		}
	}
}

func TestProactiveVoiceAssemblerMinimizesAndRejectsRestricted(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	source := voiceMailSource()
	source.MailBodyConsent = true
	source.RawMailBody = "Dana Ruiz says visit https://example.com or dana@example.com."
	job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", false, now, source)
	encoded, _ := json.Marshal(job)
	if reason != "no_source" || strings.Contains(string(encoded), "Dana Ruiz") || strings.Contains(string(encoded), "example.com") || job.People[0].Name != nil || job.User.Name != nil || job.Items[0].FromName != nil {
		t.Fatalf("names-off minimization: %s %s", reason, encoded)
	}
	source = proactiveVoiceSource{Items: []proactivevoice.Item{{Ref: "k", Type: "commitment", Title: proactivevoice.Text("Pay USD 500"), Progress: proactivevoice.Text("none recorded")}}}
	for _, title := range []string{"Pay USD 500", "Hospital appointment", "预约医疗检查"} {
		source.Items[0].Title = proactivevoice.Text(title)
		if _, _, reason := assembleProactiveVoiceJob("commitment_slipping", "en", true, now, source); reason != "unsafe_content" {
			t.Fatalf("restricted title passed: %s %s", title, reason)
		}
	}
	source = voiceMailSource()
	source.SourceApps = []string{"1Password"}
	if _, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, now, source); reason != "unsafe_content" {
		t.Fatalf("sensitive app = %s", reason)
	}
	source = voiceMailSource()
	source.MailBodyConsent = true
	source.RawMailBody = "Call me at +1 (415) 555-0123."
	job, _, reason = assembleProactiveVoiceJob("waiting_on_reply", "en", true, now, source)
	if reason != "" || strings.Contains(*job.Items[0].BodyExcerpt, "555-0123") {
		t.Fatal("PII not redacted")
	}
}

func TestProactiveVoiceUserWordsAndBounds(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	promised := now.AddDate(0, 0, -1)
	source := voiceMailSource()
	source.UserWords = []proactiveVoiceUserWordSource{{Source: "chat", Text: "I will answer Dana Ruiz tomorrow.", When: now.AddDate(0, 0, -2)}, {Source: "notes", Text: "I will answer Dana Ruiz Thursday.", When: now.AddDate(0, 0, -2), PromisedDay: &promised}, {Source: "mail_sent", Text: "Dana Ruiz has my answer.", When: now}}
	job, _, reason := assembleProactiveVoiceJob("waiting_on_reply", "zh-CN", true, now, source)
	if reason != "" || len(job.UserWords) != 1 || job.UserWords[0].Attribution != "你的笔记写道" || job.UserWords[0].PromisedDay.Status != "passed" || len(job.UserWords[0].About) == 0 {
		t.Fatalf("words = %s %+v", reason, job.UserWords)
	}
	if job.Limits.Headline.Max != 40 || job.Limits.Body.Max != 120 || job.Limits.Body.Unit != "characters" {
		t.Fatal("Chinese limits differ")
	}
	source = voiceMailSource()
	source.MailBodyConsent = true
	for range 20 {
		source.Items = append(source.Items, source.Items[0])
		source.People = append(source.People, source.People[0])
		source.WritingSamples = append(source.WritingSamples, strings.Repeat("x", 1000))
	}
	job, _, reason = assembleProactiveVoiceJob("waiting_on_reply", "en", true, now, source)
	encoded, _ := json.Marshal(job)
	if len(job.Items) > 6 || len(job.People) > 6 || len(job.WritingSamples) > 3 || (reason == "" && len(encoded) > proactiveVoiceJobLimit) {
		t.Fatal("job caps not enforced")
	}
	if _, _, reason := assembleProactiveVoiceJob("meeting_followup", "en", true, now, source); reason != "kind_not_supported" {
		t.Fatal("silent kind reached assembler")
	}
}

type proactiveVoiceTestRunner struct {
	agent.AgentRunner
	raw    string
	err    error
	input  *agent.TaskInput
	calls  int
	onCall func()
}

func (r *proactiveVoiceTestRunner) Init(context.Context) error { return nil }
func (r *proactiveVoiceTestRunner) IsReady() bool              { return true }

// Unit tests retain their in-memory backend while exercising the production
// router's before-disclosure route contract.
func proactiveVoiceTestRouter(backend agent.AgentRunner) *agent.RoutingAgentRunner {
	return agent.NewOrderedRoutingAgentRunner(&proactiveVoiceRoutingBackend{}, map[string]agent.AgentRunner{
		string(agent.BackendClaudeCode): backend,
	}, agent.RoutingPolicy{
		ManagedOrder: []string{string(agent.BackendClaudeCode)},
		Assistants: map[string]agent.AssistantState{
			string(agent.BackendClaudeCode): {SignedIn: true, Available: true, ControlProfile: true},
		},
	})
}

func (r *proactiveVoiceTestRunner) ExecuteTask(_ context.Context, input *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	r.calls++
	r.input = input
	if r.onCall != nil {
		r.onCall()
	}
	if r.err != nil {
		return "", r.err
	}
	return "voice-unit", emit(&agent.StreamEvent{Type: "assistant", Content: r.raw})
}

func TestProactiveVoiceRunnerContractAndFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: false}}
	}
	for _, tc := range []struct {
		name, raw string
		err       error
	}{
		{name: "invalid shape", raw: `{"body":"Injected model text"}`},
		{name: "validator rejection", raw: `{"headline":"Screen capture is urgent!","body":"Injected model text","draft":null,"actions":["open_settings"],"evidence":["m1"],"abstain":false,"reason":null}`},
		{name: "provider failure", err: errors.New("private provider content")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := mainChatTestApp(t)
			runner := &proactiveVoiceTestRunner{raw: tc.raw, err: tc.err}
			app.runner = proactiveVoiceTestRouter(runner)
			now := time.Now()
			moment := engagement.Moment{ID: "voice-capture", Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: now.Add(-2 * time.Hour), Params: map[string]string{"what": "Screen capture", "duration": "120 minutes"}}
			item, ok := app.proactiveItem(moment, engagementText{PushBody: "Capture is paused.", Title: "Screen capture has been off for 120 minutes."})
			if !ok || runner.calls != 1 || strings.Contains(item.Body, "Injected") || item.Draft {
				t.Fatalf("fallback = %+v calls=%d", item, runner.calls)
			}
			input := runner.input
			if input.SystemText != proactivevoice.SystemPrompt || input.NoSystem || !input.NoTools || !input.Ephemeral || input.Priority != agent.PriorityBackground || !input.DenyExternalWrites || input.History != "" || input.SessionID != "" {
				t.Fatalf("unsafe runner input %+v", input)
			}
			if !json.Valid([]byte(input.Prompt)) {
				t.Fatal("prompt is not only JSON")
			}
		})
	}
}

func TestProactiveVoiceFlaggedFallbackAndDraftHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	moment := engagement.Moment{ID: "flag", Kind: engagement.KindWaitingOnReply, SubjectID: "person", Params: map[string]string{"subject": "Please pay the invoice"}}
	item, ok := app.proactiveItem(moment, engagementText{})
	if !ok || !strings.HasSuffix(item.Body, proactivevoice.CautionLine("en")) || !strings.HasSuffix(item.Localized["zh-CN"].Body, proactivevoice.CautionLine("zh-CN")) {
		t.Fatalf("flagged fallback = %+v", item)
	}
	item.Draft = true
	item.DraftContent = &mainChatProactiveDraft{To: "dana@example.com", Subject: "Private topic", Text: "Private draft"}
	encoded, _ := json.Marshal(item)
	line := mainChatPromptReportLine(storage.StreamItemRow{ToolName: mainChatProactiveTool, ToolInput: string(encoded), Content: "Private draft"})
	if strings.Contains(line, "Private") || strings.Contains(line, "example.com") {
		t.Fatal("draft reached history")
	}
	other := mainChatProactive{}
	result := proactivevoice.Output{Headline: "A message", Body: "A safe message", Actions: []string{"open", "dismiss"}}
	job := proactivevoice.Job{Kind: "waiting_on_reply", Locale: "en"}
	if !applyProactiveVoiceResult(&other, job, result, nil) || !reflect.DeepEqual(other.ActionIDs, result.Actions) {
		t.Fatal("valid copy not applied")
	}
}

func TestProactiveVoiceAcceptedCopyStaysInMainChatOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: false}}
	}
	app := mainChatTestApp(t)
	runner := &proactiveVoiceTestRunner{raw: `{"headline":"Screen capture has been off for 120 minutes.","body":"Turn it back on from the menu bar.","draft":null,"actions":["open_settings"],"evidence":["m1"],"abstain":false,"reason":null}`}
	app.runner = proactiveVoiceTestRouter(runner)
	now := time.Now()
	moment := engagement.Moment{ID: "accepted-voice", Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: now.Add(-2 * time.Hour), Params: map[string]string{"what": "Screen capture", "duration": "120 minutes"}}
	doorbell := engagementText{PushBody: "Capture is paused.", Title: "Template copy."}
	if !app.postEngagementReport(moment, doorbell) || !app.postEngagementReport(moment, doorbell) {
		t.Fatal("chat append failed")
	}
	items := mainChatProactiveItems(t, app)
	if len(items) != 1 || runner.calls != 1 || items[0].VoiceVersion != proactivevoice.PromptVersion || items[0].Body != "Turn it back on from the menu bar." {
		t.Fatalf("accepted copy = %+v calls=%d", items, runner.calls)
	}
	if !strings.Contains(proactiveFallback(items[0]), "120 minutes") {
		t.Fatal("plain text lost headline fact")
	}
	if items[0].Localized["en"].Body != items[0].Body || doorbell.Title != "Template copy." {
		t.Fatal("model copy replaced doorbell template")
	}
	if got := proactivePromptLine(items[0]); strings.Contains(got, "120") {
		t.Fatal("voice prose entered history")
	}
}

func TestProactiveVoiceAttemptClaimSurvivesAppRestart(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	m := engagement.Moment{Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: now}
	if !app.claimProactiveVoiceAttempt(context.Background(), m, now) {
		t.Fatal("first attempt failed")
	}
	restarted := &App{store: app.store}
	if restarted.claimProactiveVoiceAttempt(context.Background(), m, now) {
		t.Fatal("restart retried provider")
	}
}

func TestProactiveVoiceRechecksSourceAfterGeneration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	off := true
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: !off, Running: !off}}
	}
	app := mainChatTestApp(t)
	runner := &proactiveVoiceTestRunner{raw: `{"headline":"Screen capture has been off for 120 minutes.","body":"Turn it back on from the menu bar.","draft":null,"actions":["open_settings"],"evidence":["m1"],"abstain":false,"reason":null}`, onCall: func() { off = false }}
	app.runner = proactiveVoiceTestRouter(runner)
	moment := engagement.Moment{ID: "restored-during-generation", Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: time.Now().Add(-2 * time.Hour)}
	item, ok := app.proactiveItem(moment, engagementText{})
	if !ok || runner.calls != 1 || item.VoiceVersion != "" || item.Body != item.Headline {
		t.Fatalf("stale model output survived: %+v", item)
	}
}

func TestProactiveVoiceStatusKeepsTheHeadlineFact(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: false}}
	}
	app := mainChatTestApp(t)
	app.runner = proactiveVoiceTestRouter(&proactiveVoiceTestRunner{raw: `{"headline":"Screen capture has been off for 20 minutes.","body":"Turn it back on from the menu bar.","draft":null,"actions":["open_settings"],"evidence":["m1"],"abstain":false,"reason":null}`})
	moment := engagement.Moment{ID: "status-voice", Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: time.Now().Add(-20 * time.Minute)}
	if !app.postEngagementReport(moment, engagementText{}) {
		t.Fatal("status was not stored")
	}
	entries := mainChatStatusEntries(t, app)
	if len(entries) != 1 || !strings.Contains(entries[0].Text, "20 minutes") || !strings.Contains(entries[0].Localized["en"], "20 minutes") {
		t.Fatalf("status lost core fact: %+v", entries)
	}
}

func proactiveVoiceFallbackTestHome(t *testing.T) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "pvf.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
}

func TestProactiveVoiceRejectedSourceUsesGenericFallback(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: true, Running: true}}
	}
	for _, kind := range []engagement.Kind{engagement.KindMeetingPrep, engagement.KindCapturePaused} {
		t.Run(string(kind), func(t *testing.T) {
			app := mainChatTestApp(t)
			runner := &proactiveVoiceTestRunner{err: errors.New("must not run")}
			app.runner = proactiveVoiceTestRouter(runner)
			now := time.Now()
			moment := engagement.Moment{ID: "rejected-source", Kind: kind, SubjectID: "screen", CreatedAt: now.Add(-2 * time.Hour), Params: map[string]string{"what": "Screen capture", "duration": "120 minutes"}}
			if kind == engagement.KindMeetingPrep {
				event := proactiveVoiceTestEvent(t, app, now)
				moment = meetingPrepMoment(event, now, time.Local)
				if err := app.store.SetUIState(context.Background(), permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
					t.Fatal(err)
				}
				if app.postEngagementReport(moment, engagementText{PushBody: "Old private headline", Title: "Old private body"}) {
					t.Fatal("meeting with withdrawn calendar access was posted")
				}
				if items := mainChatProactiveItems(t, app); len(items) != 0 || runner.calls != 0 {
					t.Fatalf("withdrawn meeting fallback=%+v provider calls=%d", items, runner.calls)
				}
				return
			}
			if !app.postEngagementReport(moment, engagementText{PushBody: "Old private headline", Title: "Old private body"}) {
				t.Fatal("generic fallback was not stored")
			}
			items := mainChatProactiveItems(t, app)
			if len(items) != 1 || runner.calls != 0 {
				t.Fatalf("fallback count=%d provider calls=%d", len(items), runner.calls)
			}
			item := items[0]
			if item.Body != "" || item.VoiceVersion != "" {
				t.Fatalf("detailed fallback survived: %+v", item)
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			for _, stale := range []string{"Old private", "Design review", "120 minutes", "has been off"} {
				if strings.Contains(string(encoded), stale) {
					t.Fatalf("source rejection retained %q: %s", stale, encoded)
				}
			}
			if kind == engagement.KindCapturePaused && (item.Headline != "Isoform stopped watching for a while." || item.Body != "" || item.Localized["zh-CN"].Headline != "Isoform 暂停观察了一会儿。" || item.Localized["zh-CN"].Body != "") {
				t.Fatalf("restored capture still claims an outage: %+v", item)
			}
		})
	}
}

func TestProactiveVoiceInitialExpiryUsesGenericFallback(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	reads := 0
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		reads++
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: false}}
	}
	app := mainChatTestApp(t)
	runner := &proactiveVoiceTestRunner{err: errors.New("must not run")}
	app.runner = proactiveVoiceTestRouter(runner)
	now := time.Now()
	moment := engagement.Moment{ID: "expired-capture", Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Minute), Params: map[string]string{"what": "Screen capture", "duration": "120 minutes"}}
	item, ok := app.proactiveItem(moment, engagementText{PushBody: "Screen capture is paused.", Title: "Screen capture has been off for 120 minutes."})
	if !ok || runner.calls != 0 || reads != 0 || item.Headline != "Isoform stopped watching for a while." || item.Body != item.Headline || item.Localized["zh-CN"].Body != "Isoform 暂停观察了一会儿。" {
		t.Fatalf("expired source retained details: %+v calls=%d reads=%d", item, runner.calls, reads)
	}
}

func TestMeetingPrepRechecksCalendarPermissionWithoutVoice(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	for _, revoked := range []bool{false, true} {
		name := "current event builds the card without voice"
		if revoked {
			name = "withdrawn source suppresses the card"
		}
		t.Run(name, func(t *testing.T) {
			app := mainChatTestApp(t)
			now := time.Now()
			event := proactiveVoiceTestEvent(t, app, now)
			moment := meetingPrepMoment(event, now, time.Local)
			runner := &proactiveVoiceTestRunner{err: errors.New("private provider failure")}
			app.runner = proactiveVoiceTestRouter(runner)
			if revoked {
				if err := app.store.SetUIState(context.Background(), permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
					t.Fatal(err)
				}
			}
			posted := app.postEngagementReport(moment, engagementText{Title: event.Title + " starts in 15 minutes."})
			items := mainChatProactiveItems(t, app)
			if runner.calls != 0 || posted == revoked || len(items) != map[bool]int{false: 1, true: 0}[revoked] {
				t.Fatalf("posted=%v cards=%+v calls=%d", posted, items, runner.calls)
			}
			if !revoked {
				encoded, err := json.Marshal(items[0])
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(encoded), event.Title) || strings.Contains(string(encoded), "private provider failure") {
					t.Fatalf("event copy = %s", encoded)
				}
			}
		})
	}
}

func TestMeetingPrepUsesCurrentCalendarEventWithoutVoice(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	now := time.Now()
	event := proactiveVoiceTestEvent(t, app, now)
	saveEvent := func() {
		t.Helper()
		row := calendarEventRow(event)
		for _, person := range event.Attendees {
			row.Attendees = append(row.Attendees, storage.CalendarAttendeeRow{Name: person.Name, Email: person.Email, IsMe: person.IsMe})
		}
		if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row}, now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	event.Title, event.Location, event.Attendees[0].Name = "Withdrawn project", "Withdrawn room", "Mara Oldname"
	event.ConferenceURL, event.ConferenceHost = "", ""
	saveEvent()
	moment := meetingPrepMoment(event, now, time.Local)
	catalog, _ := engagement.Copy(moment.Kind, "en")
	doorbell := engagementText{PushBody: catalog.PushBody, Title: engagement.Render(catalog.Sealed, moment.Params)}
	originalDoorbell := doorbell
	originalParams, err := json.Marshal(moment.Params)
	if err != nil {
		t.Fatal(err)
	}

	event.Title, event.Location, event.Attendees[0].Name = "Design review", "Studio", "Dana Ruiz"
	saveEvent()
	runner := &proactiveVoiceTestRunner{raw: `{"headline":"Design review starts in 15 minutes.","body":"Open the calendar for the details.","draft":null,"actions":["open"],"evidence":["m1"],"abstain":false,"reason":null}`}
	app.runner = proactiveVoiceTestRouter(runner)
	if !app.postEngagementReport(moment, doorbell) {
		t.Fatal("current calendar event was not posted")
	}
	rows := mainChatStructuredRows(t, app, mainChatProactiveTool)
	if len(rows) != 1 {
		t.Fatalf("persisted rows = %d", len(rows))
	}
	var item mainChatProactive
	if err := json.Unmarshal([]byte(rows[0].ToolInput), &item); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"Withdrawn project", "Withdrawn room", "Mara Oldname"} {
		if strings.Contains(rows[0].Content+rows[0].ToolInput, stale) {
			t.Fatalf("stale calendar detail %q reached durable copy", stale)
		}
	}
	if item.Event == nil || item.Event.Title != "Design review" || item.Event.Place != "Studio" || !strings.Contains(item.Event.People, "Dana Ruiz") {
		t.Fatalf("event details were not reloaded: %+v", item.Event)
	}
	for _, lang := range engagement.Languages {
		copy := item.Localized[lang]
		if !strings.Contains(copy.Headline+copy.Body, "Design review") {
			t.Fatalf("%s copy did not use current title: %+v", lang, copy)
		}
	}
	if runner.calls != 0 || item.VoiceVersion != "" {
		t.Fatalf("meeting prep entered voice path: calls=%d version=%q", runner.calls, item.VoiceVersion)
	}
	currentParams, err := json.Marshal(moment.Params)
	if err != nil || !reflect.DeepEqual(doorbell, originalDoorbell) || string(currentParams) != string(originalParams) {
		t.Fatal("main-chat copy changed the doorbell or producer params")
	}
}

func TestProactiveVoiceExpiryAfterProviderRechecksBeforeFallback(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	for _, providerError := range []bool{false, true} {
		name := "successful late result"
		if providerError {
			name = "failed late result"
		}
		t.Run(name, func(t *testing.T) {
			reads := 0
			proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
				reads++
				return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: false}}
			}
			app := mainChatTestApp(t)
			// A fixed pacing instant precedes the deadline; wall time after the
			// provider call is past it. No sleeping or real provider is needed.
			now := time.Now().Add(-time.Minute)
			app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
			app.engagement.now = func() time.Time { return now }
			runner := &proactiveVoiceTestRunner{raw: `{}`}
			if providerError {
				runner.err = errors.New("late provider failure")
			}
			app.runner = proactiveVoiceTestRouter(runner)
			moment := engagement.Moment{ID: "expired-after-provider", Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(30 * time.Second), Params: map[string]string{"what": "Screen capture", "duration": "120 minutes"}}
			item, ok := app.proactiveItem(moment, engagementText{Title: "Screen capture has been off for 120 minutes."})
			if !ok || runner.calls != 1 || reads != 2 || item.Headline != "Isoform stopped watching for a while." || item.Body != item.Headline || item.VoiceVersion != "" {
				t.Fatalf("late fallback=%+v calls=%d source reads=%d", item, runner.calls, reads)
			}
		})
	}
}
