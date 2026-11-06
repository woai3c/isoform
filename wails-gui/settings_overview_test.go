package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/pii"
	"wails-gui/internal/remoteaccess"
	"wails-gui/storage"
)

// overviewTestBriefSource is a brief service fixture with a fixed schedule.
type overviewTestBriefSource struct {
	mobileTestBriefSource
	schedule MobileBriefSchedule
}

func (s *overviewTestBriefSource) Schedule() (MobileBriefSchedule, error) {
	return s.schedule, nil
}

// seedOverviewEngagementSent records count sent moments of a kind at the rig's
// clock, each with one opened receipt when opened is set.
func seedOverviewEngagementSent(t *testing.T, rig *engagementTestRig, kind engagement.Kind, count int, opened bool) {
	t.Helper()
	ctx := context.Background()
	batch := time.Now().UnixNano()
	for i := 0; i < count; i++ {
		row, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
			Kind:      string(kind),
			DedupeKey: fmt.Sprintf("overview-%s-%d-%d", kind, batch, i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := rig.store.SetEngagementMomentState(ctx, row.ID, storage.EngagementStateSent, "ok", storage.EngagementMomentRow{
			Channel: "push",
			SentAt:  rig.clock.UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
		if opened {
			if err := rig.store.RecordEngagementReceipt(ctx, storage.EngagementReceiptRow{
				MomentID: row.ID,
				Channel:  "push",
				State:    storage.EngagementReceiptOpened,
				At:       rig.clock.UnixMilli(),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// seedOverviewSelfDreamPending stores qualified proposals and one duplicate
// using the real run, attempt and operation schema.
func seedOverviewSelfDreamPending(t *testing.T, store *ActivityStore, count int) {
	t.Helper()
	run, _, err := store.AddSelfDreamRun(SelfDreamRunRecord{
		IdempotencyKey: "overview-run",
		TriggerKind:    "scheduled",
		PolicyVersion:  "policy",
		SourceCutoffAt: time.Now().UnixMilli(),
		Manifest:       []TaskContextManifestEntry{},
		InputHash:      "input",
		PromptVersion:  "prompt",
		Prompt:         "prompt",
		PromptHash:     "prompt-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.AddSelfDreamAttempt(SelfDreamAttemptRecord{
		RunID:         run.ID,
		Status:        "completed",
		Model:         "fixture",
		PromptHash:    "prompt-hash",
		RawOutputHash: "raw-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	ops := []SelfDreamOperationRecord{}
	for i := 0; i < count; i++ {
		ops = append(ops, SelfDreamOperationRecord{
			RunID:            run.ID,
			AttemptID:        attempt.ID,
			Operation:        SelfDreamOperationContextualize,
			TargetEntryIDs:   []string{},
			SelfKind:         selfModelKindPreference,
			Scope:            SelfDreamScope{Kind: "project", Key: "isoform"},
			Statement:        fmt.Sprintf("Preference number %d.", i),
			ReasoningStatus:  "unknown",
			Confidence:       0.7,
			EvidenceRefs:     []string{},
			Source:           "model",
			ValidationStatus: SelfDreamValidationQualified,
			OperationHash:    fmt.Sprintf("overview-%d", i),
			CreatedAt:        time.Now().UnixMilli(),
		})
	}
	// An identical proposal from another operation is still one review item.
	repeat := ops[0]
	repeat.OperationHash = "overview-repeat"
	ops = append(ops, repeat)
	_, err = store.AddSelfDreamOperations(ops)
	if err != nil {
		t.Fatal(err)
	}
}

// overviewLoopbackBridge returns a bridge whose relay is a loopback address
// with one paired physical phone, so the same-network warning is active.
func overviewLoopbackBridge(t *testing.T, app *App) *mobileBridge {
	t.Helper()
	bridge, err := newMobileBridge(app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	device := mobileTestDevice(mobileScopePush)
	device.DeviceName = "Ada iPhone"
	device.LastSeenAt = mobileTimestamp(time.Now())
	bridge.mu.Lock()
	bridge.relay = mobileRelayConfig{Enabled: true, URL: "http://127.0.0.1:8791"}
	bridge.devices[device.DeviceID] = &device
	bridge.mu.Unlock()
	return bridge
}

func TestSettingsOverviewMatchesItsSources(t *testing.T) {
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	rig := newEngagementTestRig(t)
	ctx := context.Background()

	// Speaks up: a normal level with one kind enabled and noisy, one muted.
	if err := rig.svc.SetKindEnabled(ctx, engagement.KindWorkFinished, true); err != nil {
		t.Fatal(err)
	}
	seedOverviewEngagementSent(t, rig, engagement.KindWorkFinished, 25, false)
	seedOverviewEngagementSent(t, rig, engagement.KindWorkBlocked, 22, true)

	// Keeps: 14 days, 7 GiB, one session's audio bytes on disk.
	home, err := flavorDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "activity-config.json"), []byte(`{"retention_days":14,"max_storage_gb":7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	activityStore := newTestStore(t)
	insertSession(t, activityStore, "session-1", rig.clock, "done", 4096)

	// Remembers: one learned thing waiting for confirmation.
	seedOverviewSelfDreamPending(t, activityStore, 1)

	// Thinks with: claude at 74 percent left of its 5-hour window, codex
	// without numbers.
	resetAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	app := &App{store: rig.store, engagement: rig.svc}
	app.activity = &activityMonitorState{store: activityStore}
	app.magpieModels.applyQuotas([]magpieQuotaRow{
		{Provider: "codex", Name: "Codex", Kind: "subscription"},
		{
			Provider: "claude", Name: "Claude Code", Kind: "subscription",
			Windows: []magpieQuotaWindowRow{{Name: "5 hours", Used: 26, Remaining: 74, ResetsAt: resetAt}},
		},
	}, nil, time.Now())

	// Briefs: a fixed schedule with adaptive timing; the phone is paired and
	// reachable only on the same network.
	adaptive := true
	app.mobileBriefSvc = &overviewTestBriefSource{schedule: MobileBriefSchedule{
		Morning: "07:30", Evening: "18:45", AdaptiveTiming: &adaptive,
	}}
	app.mobileBridge = overviewLoopbackBridge(t, app)

	// Ignores: one app the user marked, beside the hidden password managers.
	pii.SetUserSensitiveApps(map[string]string{"com.apple.journal": "Journal"})
	t.Cleanup(func() { pii.SetUserSensitiveApps(map[string]string{}) })

	view, err := app.GetSettingsOverview()
	if err != nil {
		t.Fatal(err)
	}

	if !view.Sees.Watching || view.Sees.PausedUntil != nil {
		t.Fatalf("sees while watching: %+v", view.Sees)
	}
	// Count the named ignored list; password managers remain hidden.
	if view.Sees.IgnoredAppsCount != 1 || view.Sees.IgnoredAppsCount != len(view.Ignores.Apps) || !view.Sees.PasswordManagersHidden {
		t.Fatalf("sees counts: %+v", view.Sees)
	}

	if view.SpeaksUp.Level != "normal" || view.SpeaksUp.DailyCap != 6 ||
		view.SpeaksUp.QuietStart != "22:00" || view.SpeaksUp.QuietEnd != "08:00" {
		t.Fatalf("speaksUp: %+v", view.SpeaksUp)
	}
	// The rig's presence has a registered phone: desktop and push.
	if len(view.SpeaksUp.Channels) != 2 || view.SpeaksUp.Channels[0] != "desktop" || view.SpeaksUp.Channels[1] != "push" {
		t.Fatalf("channels: %+v", view.SpeaksUp.Channels)
	}

	if len(view.ThinksWith.Order) != 2 {
		t.Fatalf("thinksWith order: %+v", view.ThinksWith.Order)
	}
	claude := view.ThinksWith.Order[0]
	if claude.Assistant != "claude" || claude.PercentLeft == nil || *claude.PercentLeft != 74 ||
		claude.WindowLabel != "5 hours" || claude.ResetsAt != resetAt {
		t.Fatalf("claude entry: %+v", claude)
	}
	codex := view.ThinksWith.Order[1]
	if codex.Assistant != "codex" || codex.PercentLeft != nil || codex.WindowLabel != "" || codex.ResetsAt != "" {
		t.Fatalf("codex entry must report unknown quota: %+v", codex)
	}

	if view.Remembers.AwaitingConfirmCount != 1 {
		t.Fatalf("remembers: %+v", view.Remembers)
	}

	if view.Keeps.RetentionDays != 14 || view.Keeps.LimitBytes != 7*1024*1024*1024 || view.Keeps.UsedBytes != 4096 {
		t.Fatalf("keeps: %+v", view.Keeps)
	}

	if view.Briefs.MorningAt != "07:30" || view.Briefs.EveningAt != "18:45" || !view.Briefs.Adaptive {
		t.Fatalf("briefs: %+v", view.Briefs)
	}
	phoneSource, err := app.GetMobileConnectionStatus()
	if err != nil || len(phoneSource.Devices) != 1 {
		t.Fatalf("phone fixture: %+v %v", phoneSource, err)
	}
	if view.Briefs.Phone == nil || view.Briefs.Phone.Name != phoneSource.Devices[0].DeviceName || view.Briefs.Phone.LastSeenAt != phoneSource.Devices[0].LastSeenAt {
		t.Fatalf("briefs phone: %+v", view.Briefs.Phone)
	}
	if view.Briefs.Connection == nil || view.Briefs.Connection.Enabled != phoneSource.Enabled || view.Briefs.Connection.RelayState != phoneSource.RelayState || view.Briefs.Connection.RelayURL != phoneSource.RelayURL || view.Briefs.Connection.WarningCode != phoneSource.WarningCode {
		t.Fatalf("briefs connection differs from Phone: %+v vs %+v", view.Briefs.Connection, phoneSource)
	}

	if len(view.Ignores.Apps) != 1 || view.Ignores.Apps[0].Name != "Journal" || view.Ignores.Apps[0].BundleID != "com.apple.journal" {
		t.Fatalf("ignores: %+v", view.Ignores.Apps)
	}

	// Worth a look: the phone warning and the noisy kind, in that order.
	// work_blocked was opened, so it does not qualify.
	if len(view.WorthALook) != 2 {
		t.Fatalf("worthALook: %+v", view.WorthALook)
	}
	phone := view.WorthALook[0]
	if phone.ID != overviewIssuePhoneSameNetwork || phone.Kind != overviewIssuePhoneSameNetwork ||
		phone.Params.Host != "127.0.0.1" || phone.Params.RemoteAccessOff != "saved_relay" ||
		phone.Signature != "127.0.0.1" {
		t.Fatalf("phone issue: %+v", phone)
	}
	noisy := view.WorthALook[1]
	if noisy.ID != "noisy_kind:work_finished" || noisy.Kind != overviewIssueNoisyKind ||
		noisy.Params.Kind != "work_finished" || noisy.Params.Sent != 25 ||
		noisy.Signature != "work_finished|20" {
		t.Fatalf("noisy issue: %+v", noisy)
	}
}

func TestSettingsOverviewPhoneRemoteAccessIssues(t *testing.T) {
	loopback := computeOverviewIssues(overviewSources{mobile: MobileConnectionStatus{
		WarningCode: "relay_loopback", WarningHost: "127.0.0.1",
		RemoteAccessOffReason: "saved_relay",
	}})
	if len(loopback) != 1 || loopback[0].Kind != overviewIssuePhoneSameNetwork ||
		loopback[0].Params.RemoteAccessOff != "saved_relay" {
		t.Fatalf("loopback phone issue: %+v", loopback)
	}

	failed := computeOverviewIssues(overviewSources{mobile: MobileConnectionStatus{
		Enabled: true, Devices: []MobileDeviceView{{DeviceID: "phone"}},
		RemoteAccess: &MobileRemoteAccessView{State: remoteaccess.StateFailed},
	}})
	if len(failed) != 1 || failed[0].ID != overviewIssuePhoneRemoteUnavailable ||
		failed[0].Kind != overviewIssuePhoneRemoteUnavailable || failed[0].Signature != "failed" {
		t.Fatalf("failed remote-access issue: %+v", failed)
	}

	ready := computeOverviewIssues(overviewSources{mobile: MobileConnectionStatus{
		Enabled: true, Devices: []MobileDeviceView{{DeviceID: "phone"}},
		RemoteAccess: &MobileRemoteAccessView{State: remoteaccess.StateReady},
	}})
	if len(ready) != 0 {
		t.Fatalf("ready remote access produced a phone issue: %+v", ready)
	}
}

func TestSettingsOverviewEmptyWhenNothingQualifies(t *testing.T) {
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc}

	view, err := app.GetSettingsOverview()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.WorthALook) != 0 {
		t.Fatalf("nothing qualifies, so worthALook must be empty: %+v", view.WorthALook)
	}
	// The quotas were never read: both assistants report unknown, not zero.
	for _, entry := range view.ThinksWith.Order {
		if entry.PercentLeft != nil {
			t.Fatalf("an unread assistant must report unknown quota: %+v", entry)
		}
	}
	if view.Briefs.Phone != nil || view.Sees.Watching != true {
		t.Fatalf("no phone, watching: %+v", view)
	}

	// A bare App without any store still answers with empty sections.
	bare := &App{}
	if view, err := bare.GetSettingsOverview(); err != nil {
		t.Fatalf("bare app: %v", err)
	} else if len(view.WorthALook) != 0 || view.SpeaksUp.Level != "" || len(view.SpeaksUp.Channels) != 0 {
		t.Fatalf("bare app must degrade to zero values: %+v", view)
	}
}

func TestDismissOverviewIssueHidesUntilSignatureChanges(t *testing.T) {
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc}
	seedOverviewEngagementSent(t, rig, engagement.KindWorkFinished, 25, false)

	if err := app.DismissOverviewIssue("noisy_kind:work_blocked"); err == nil {
		t.Fatal("dismissing an issue that is not current must fail")
	}

	current := func() []SettingsOverviewIssue {
		t.Helper()
		view, err := app.GetSettingsOverview()
		if err != nil {
			t.Fatal(err)
		}
		return view.WorthALook
	}
	if issues := current(); len(issues) != 1 || issues[0].ID != "noisy_kind:work_finished" {
		t.Fatalf("before dismissal: %+v", issues)
	}
	if err := app.DismissOverviewIssue("noisy_kind:work_finished"); err != nil {
		t.Fatal(err)
	}
	if issues := current(); len(issues) != 0 {
		t.Fatalf("a dismissed issue hides: %+v", issues)
	}
	// A fresh App over the same store keeps the dismissal: it is persisted.
	restarted := &App{store: rig.store, engagement: rig.svc}
	if view, err := restarted.GetSettingsOverview(); err != nil || len(view.WorthALook) != 0 {
		t.Fatalf("the dismissal survives a restart: %+v %v", view.WorthALook, err)
	}

	// When the noise doubles, the signature changes and the issue returns.
	seedOverviewEngagementSent(t, rig, engagement.KindWorkFinished, 15, false)
	issues := current()
	if len(issues) != 1 || issues[0].Signature != "work_finished|40" || issues[0].Params.Sent != 40 {
		t.Fatalf("a doubled count returns the issue: %+v", issues)
	}
}

func TestSettingsOverviewCapsWorthALookAtThree(t *testing.T) {
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	app := &App{store: rig.store, engagement: rig.svc}

	for _, kind := range []engagement.Kind{
		engagement.KindWorkFinished,
		engagement.KindBriefReady,
		engagement.KindCapturePaused,
		engagement.KindMeetingPrep,
	} {
		if err := rig.svc.SetKindEnabled(ctx, kind, true); err != nil {
			t.Fatal(err)
		}
	}
	seedOverviewEngagementSent(t, rig, engagement.KindWorkFinished, 40, false)
	seedOverviewEngagementSent(t, rig, engagement.KindBriefReady, 30, false)
	seedOverviewEngagementSent(t, rig, engagement.KindCapturePaused, 25, false)
	seedOverviewEngagementSent(t, rig, engagement.KindMeetingPrep, 20, false)

	view, err := app.GetSettingsOverview()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.WorthALook) != settingsOverviewMaxIssues {
		t.Fatalf("worthALook holds at most %d: %+v", settingsOverviewMaxIssues, view.WorthALook)
	}
	// Capture pauses are retired; the next eligible kind takes its place.
	want := []string{"noisy_kind:work_finished", "noisy_kind:brief_ready", "noisy_kind:meeting_prep"}
	for i, issue := range view.WorthALook {
		if issue.ID != want[i] {
			t.Fatalf("issue %d is %s, want %s (%+v)", i, issue.ID, want[i], view.WorthALook)
		}
	}
}

func TestSettingsOverviewAssistantPercentIsMagpieWindowRemaining(t *testing.T) {
	// The quota unit: PercentLeft passes through magpie's remaining, the
	// percent of the window's allowance left (QuotaWindow documents 0-100).
	models := ModelAccountsStatus{
		AccountOrder: []string{"claude", "codex"},
		Accounts: []ModelAccount{
			{
				Provider: "claude",
				Windows: []QuotaWindow{
					{Name: "5 hours", Used: 40, Remaining: 60, ResetsAt: "2026-10-12T15:00:00Z"},
					{Name: "weekly", Used: 95, Remaining: 5, ResetsAt: "2026-10-19T00:00:00Z"},
				},
			},
			{Provider: "codex", Windows: []QuotaWindow{{Name: "weekly", Used: 10, Remaining: 90, ResetsAt: "2026-10-19T00:00:00Z"}}},
		},
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	thinksWith, _ := overviewThinksWithAt(models, now)
	if len(thinksWith.Order) != 2 {
		t.Fatalf("order: %+v", thinksWith.Order)
	}
	// The binding window is the lowest remaining, not the first listed.
	if got := thinksWith.Order[0]; got.PercentLeft == nil || *got.PercentLeft != 5 || got.WindowLabel != "weekly" {
		t.Fatalf("the lowest remaining window binds: %+v", got)
	}
	if got := thinksWith.Order[1]; got.PercentLeft == nil || *got.PercentLeft != 90 {
		t.Fatalf("codex: %+v", got)
	}
	// Zero percent left is a number, not unknown, and raises the issue.
	issues := computeOverviewIssues(overviewSources{now: now, models: ModelAccountsStatus{
		AccountOrder: []string{"claude"},
		Accounts:     []ModelAccount{{Provider: "claude", Windows: []QuotaWindow{{Name: "5 hours", Used: 100, Remaining: 0, ResetsAt: "2026-10-12T15:00:00Z"}}}},
	}})
	if len(issues) != 1 || issues[0].ID != "assistant_out_of_usage:claude" ||
		issues[0].Params.Assistant != "claude" || issues[0].Params.ResetsAt != "2026-10-12T15:00:00Z" ||
		issues[0].Signature != "claude|2026-10-12T15:00:00Z" {
		t.Fatalf("assistant issue: %+v", issues)
	}
}

func TestSettingsOverviewShowsTimedPauseDeadline(t *testing.T) {
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	store, err := storage.New(filepath.Join(t.TempDir(), "overview.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	app := &App{store: store}

	before := time.Now()
	if _, err := app.setClosedEyesModeTimed(true, time.Hour); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = app.setClosedEyesModeEnabled(false) })

	view, err := app.GetSettingsOverview()
	if err != nil {
		t.Fatal(err)
	}
	if view.Sees.Watching || view.Sees.PausedUntil == nil {
		t.Fatalf("a timed pause shows not watching with a deadline: %+v", view.Sees)
	}
	deadline, err := time.Parse(time.RFC3339, *view.Sees.PausedUntil)
	if err != nil {
		t.Fatalf("pausedUntil parses: %v", err)
	}
	if deadline.Before(before.Add(59*time.Minute)) || deadline.After(time.Now().Add(61*time.Minute)) {
		t.Fatalf("pausedUntil is about an hour out: %s", deadline)
	}
}

func TestSettingsOverviewMagpieFractionalQuotaWireContract(t *testing.T) {
	// Pinned magpie v0.1.1172 QuotaSpan: percent, float64, not a 0-1 ratio,
	// credit count or integer. Decode the gateway shape before projecting it.
	var payload struct {
		Data []magpieQuotaRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(`{"data":[{"provider":"claude","windows":[{"name":"5 hours","used":26.5,"remaining":73.5,"resetsAt":"2026-10-22T01:00:00Z"}]}]}`), &payload); err != nil {
		t.Fatal(err)
	}
	accounts := buildModelAccounts(payload.Data, nil)
	view, _ := overviewThinksWithAt(ModelAccountsStatus{AccountOrder: []string{"claude"}, Accounts: accounts}, time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC))
	if got := view.Order[0]; got.PercentLeft == nil || *got.PercentLeft != 73.5 || accounts[0].Windows[0].Used != 26.5 {
		t.Fatalf("fractional percentage lost: %+v", got)
	}
	unknown := overviewThinksWithFrom(ModelAccountsStatus{AccountOrder: []string{"claude"}, Accounts: []ModelAccount{{Provider: "claude", Windows: []QuotaWindow{{Unlimited: true}, {Remaining: -1}, {Remaining: 101}}}}})
	if unknown.Order[0].PercentLeft != nil {
		t.Fatal("invalid or unlimited quota became zero remaining")
	}
	window, ok := overviewBindingWindowAt([]QuotaWindow{
		{Name: "later", Remaining: 5, ResetsAt: "2026-10-22T01:00:00-07:00"},
		{Name: "earlier", Remaining: 5, ResetsAt: "2026-10-22T02:00:00Z"},
	}, time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC))
	if !ok || window.Name != "earlier" {
		t.Fatalf("reset tie must compare instants: %+v", window)
	}
}

func TestSettingsOverviewIssueThresholdsAndAssistantDismissal(t *testing.T) {
	for _, tc := range []struct {
		sent, opened, acted int
		want                bool
	}{
		{19, 0, 0, false}, {20, 0, 0, true}, {100, 1, 0, false}, {100, 0, 1, false},
	} {
		issues := noisyKindIssues(&EngagementSettingsView{Kinds: []EngagementKindView{{Kind: "brief_ready", QuietSuggestionAllowed: true, Sent: tc.sent, Opened: tc.opened, Acted: tc.acted,
			NotificationSent: tc.sent, NotificationOpenedOrActed: tc.opened + tc.acted}}})
		if (len(issues) == 1) != tc.want {
			t.Fatalf("threshold %+v: %+v", tc, issues)
		}
	}
	resetAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	newResetAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	app := newTimedPauseFixture(t)
	app.magpieModels.applyQuotas([]magpieQuotaRow{{Provider: "claude", Windows: []magpieQuotaWindowRow{{Name: "weekly", Remaining: 0, ResetsAt: resetAt}}}}, nil, time.Now())
	id := "assistant_out_of_usage:claude"
	if err := app.DismissOverviewIssue(id); err != nil {
		t.Fatal(err)
	}
	issues := app.visibleOverviewIssues(computeOverviewIssues(app.readOverviewSources()))
	if len(issues) != 0 {
		t.Fatalf("dismissal did not hide quota: %+v", issues)
	}
	app.magpieModels.applyQuotas([]magpieQuotaRow{
		{Provider: "claude", Windows: []magpieQuotaWindowRow{{Remaining: 0, ResetsAt: newResetAt}}},
		{Provider: "codex", Windows: []magpieQuotaWindowRow{{Remaining: 0}}},
	}, nil, time.Now())
	issues = app.visibleOverviewIssues(computeOverviewIssues(app.readOverviewSources()))
	if len(issues) != 2 {
		t.Fatalf("new window and different assistant must return: %+v", issues)
	}
	// An old/corrupt JSON null must not panic or hide issues.
	if err := app.store.SetUIState(context.Background(), settingsOverviewDismissalsKey, "null"); err != nil {
		t.Fatal(err)
	}
	if err := app.DismissOverviewIssue(id); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsOverviewPartialFailureAndJSONShape(t *testing.T) {
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	app := &App{}
	view, err := app.GetSettingsOverview()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Unavailable, []string{"speaksUp", "remembers", "briefs"}) {
		t.Fatalf("missing sections are unavailable: %+v", view.Unavailable)
	}
	if !view.Sees.Watching || !view.Sees.PasswordManagersHidden || view.Keeps.LimitBytes == 0 {
		t.Fatal("one missing source erased healthy settings")
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["briefs"].(map[string]any)["phone"] != nil || wire["sees"].(map[string]any)["pausedUntil"] != nil {
		t.Fatalf("optional fields must encode null: %s", raw)
	}
	for _, key := range []string{"worthALook", "unavailable"} {
		if _, ok := wire[key].([]any); !ok {
			t.Fatalf("%s is not an array: %s", key, raw)
		}
	}
}

func TestSettingsOverviewRemembersCountsPastDisplayLimit(t *testing.T) {
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	store := newTestStore(t)
	seedOverviewSelfDreamPending(t, store, 56)
	app := &App{activity: &activityMonitorState{store: store}}
	count, err := app.settingsOverviewAwaitingConfirmCount()
	if err != nil || count != 56 {
		t.Fatalf("count=%d, err=%v", count, err)
	}
	listed, err := app.ListSelfDreamUpdates()
	if err != nil || len(listed) != 50 {
		t.Fatalf("display list changed: %d, %v", len(listed), err)
	}
	if _, err := app.ReviewSelfDreamOperation(ReviewSelfDreamOperationInput{
		OperationID: listed[0].ID, IdempotencyKey: "overview-keep", Decision: SelfDreamDecisionKeepSeparate,
	}); err != nil {
		t.Fatal(err)
	}
	count, err = app.settingsOverviewAwaitingConfirmCount()
	if err != nil || count != 55 {
		t.Fatalf("answered count=%d, err=%v", count, err)
	}
}

func TestSettingsOverviewQuotaFailureAndStalenessAreUnknown(t *testing.T) {
	for _, state := range []string{"failed poll", "stale poll", "expired window", "account error", "unhealthy sidecar"} {
		t.Run(state, func(t *testing.T) {
			app := newTimedPauseFixture(t)
			now := time.Now().UTC()
			rows := []magpieQuotaRow{{Provider: "claude", Windows: []magpieQuotaWindowRow{{Name: "hour", Remaining: 0, ResetsAt: now.Add(time.Hour).Format(time.RFC3339Nano)}}}}
			app.magpieModels.applyQuotas(rows, nil, now)
			initial, err := app.GetSettingsOverview()
			if err != nil || len(initial.WorthALook) != 1 || initial.ThinksWith.Order[0].PercentLeft == nil {
				t.Fatalf("valid exhausted control: %+v %v", initial, err)
			}
			switch state {
			case "unhealthy sidecar":
				app.magpie = newMagpieSidecar(t.TempDir())
			case "failed poll":
				app.magpieModels.applyQuotas(nil, errors.New("fixture_gateway_unavailable"), now)
			case "stale poll":
				app.magpieModels.applyQuotas(rows, nil, now.Add(-overviewQuotaMaxAge-time.Second))
			case "expired window":
				rows[0].Windows[0].ResetsAt = now.Add(-time.Second).Format(time.RFC3339Nano)
				app.magpieModels.applyQuotas(rows, nil, now)
			case "account error":
				rows[0].Error = "fixture_vendor_unavailable"
				app.magpieModels.applyQuotas(rows, nil, now)
			}
			view, err := app.GetSettingsOverview()
			if err != nil {
				t.Fatal(err)
			}
			if view.ThinksWith.Order[0].PercentLeft != nil || view.ThinksWith.Order[0].WindowLabel != "" || view.ThinksWith.Order[0].ResetsAt != "" || len(view.WorthALook) != 0 {
				t.Fatalf("failed/stale quota remained known: %+v %+v", view.ThinksWith, view.WorthALook)
			}
			found := false
			for _, section := range view.Unavailable {
				found = found || section == "thinksWith"
			}
			if !found || !view.Sees.Watching || view.Keeps.LimitBytes == 0 {
				t.Fatalf("unavailability crossed sections: %+v", view)
			}
			app.magpie = nil
			rows[0].Error = ""
			rows[0].Windows[0].ResetsAt = now.Add(time.Hour).Format(time.RFC3339Nano)
			rows[0].Windows[0].Remaining = 75
			app.magpieModels.applyQuotas(rows, nil, time.Now())
			recovered, err := app.GetSettingsOverview()
			if err != nil || recovered.ThinksWith.Order[0].PercentLeft == nil || *recovered.ThinksWith.Order[0].PercentLeft != 75 {
				t.Fatalf("fresh poll did not recover: %+v %v", recovered.ThinksWith, err)
			}
		})
	}
}

func TestDismissOverviewIssueDoesNotOverwriteUnreadableDismissals(t *testing.T) {
	app := newTimedPauseFixture(t)
	app.magpieModels.applyQuotas([]magpieQuotaRow{{Provider: "claude", Windows: []magpieQuotaWindowRow{{Remaining: 0}}}}, nil, time.Now())
	// Corrupt persisted data is a read/decode failure; a new Leave it must
	// fail rather than publish an empty map over earlier dismissal data.
	prior := `{"older_issue":{"signature":"older"}`
	if err := app.store.SetUIState(context.Background(), settingsOverviewDismissalsKey, prior); err != nil {
		t.Fatal(err)
	}
	if err := app.DismissOverviewIssue("assistant_out_of_usage:claude"); err == nil {
		t.Fatal("dismissal succeeded after read failure")
	}
	after, err := app.store.GetUIState(context.Background(), settingsOverviewDismissalsKey)
	if err != nil || after != prior {
		t.Fatalf("prior dismissals overwritten: %q %v", after, err)
	}
}

func TestSettingsOverviewUnlimitedQuotaIsNotASourceFailure(t *testing.T) {
	view, unavailable := overviewThinksWithAt(ModelAccountsStatus{
		AccountOrder: []string{"claude"},
		Accounts:     []ModelAccount{{Provider: "claude", Windows: []QuotaWindow{{Unlimited: true}}}},
	}, time.Now())
	if unavailable || view.Order[0].PercentLeft != nil {
		t.Fatalf("unlimited reading reported a source failure: %+v %v", view, unavailable)
	}
}

func TestSettingsOverviewNoisyKindsExcludeRetiredMeetingKinds(t *testing.T) {
	view := &EngagementSettingsView{Kinds: []EngagementKindView{
		{Kind: "meeting_ended", Sent: 100},
		{Kind: "meeting_followup", Sent: 100},
		{Kind: "brief_ready", QuietSuggestionAllowed: true, Sent: 20, NotificationSent: 20},
	}}
	issues := noisyKindIssues(view)
	if len(issues) != 1 || issues[0].Params.Kind != "brief_ready" {
		t.Fatalf("only an active kind may offer the quiet fix: %+v", issues)
	}
	if len(view.Kinds) != 3 {
		t.Fatal("ranked notifications must retain all historical rows")
	}
}
