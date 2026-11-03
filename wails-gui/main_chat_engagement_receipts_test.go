package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func engagementReceiptStates(t *testing.T, app *App, momentID, channel string) map[string]bool {
	t.Helper()
	receiptID, err := app.mainChatReceiptMomentID(t.Context(), momentID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := app.store.ListEngagementReceipts(t.Context(), receiptID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]bool{}
	for _, row := range rows {
		if channel == "" || row.Channel == channel {
			if states[row.State] {
				t.Fatalf("duplicate receipt %s/%s: %+v", momentID, channel, rows)
			}
			states[row.State] = true
		}
	}
	return states
}

func TestMainChatEngagementReceiptsObserveWithoutClaimingCompletion(t *testing.T) {
	app := mainChatTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	now := time.Now()
	event := proactiveVoiceTestEvent(t, app, now)
	moment := meetingPrepMoment(event, now, time.Local)
	moment.ID = "receipt-card"
	item, ok := app.proactiveItem(moment, engagementText{})
	if !ok || app.postProactiveItem(item, item.MomentID) == "" {
		t.Fatal("card was not saved")
	}
	for _, state := range []string{"delivered", "opened", "delivered", "opened"} {
		if err := app.RecordMainChatProactiveReceipt(item.MomentID, state); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"created", "sent", "acted", "muted", "dismissed"} {
		if err := app.RecordMainChatProactiveReceipt(item.MomentID, state); err == nil {
			t.Fatalf("client supplied %s", state)
		}
	}
	if err := app.RecordMainChatProactiveReceipt("absent", "opened"); err == nil {
		t.Fatal("unknown card accepted")
	}
	if _, err := app.ResolveMainChatProactiveAction(item.MomentID, "open"); err != nil {
		t.Fatal(err)
	}
	states := engagementReceiptStates(t, app, item.MomentID, "")
	for _, state := range []string{"created", "sent", "delivered", "opened"} {
		if !states[state] {
			t.Fatalf("missing %s: %v", state, states)
		}
	}
	if states["acted"] {
		t.Fatal("opening a card was counted as completion")
	}
}

func TestMainChatMuteLegacyDigestChildPersistsKindPreference(t *testing.T) {
	app := mainChatTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a row created before mute existed, including saved translations.
	child := mainChatProactive{MomentID: "legacy-child", Kind: string(engagement.KindBriefReady), State: "pending", ActionIDs: []string{"open"},
		Localized: map[string]mainChatProactiveCopy{
			"en":    {Headline: "Your brief is ready.", ActionLabels: map[string]string{"open": "Open brief"}},
			"zh-CN": {Headline: "你的简报已经准备好了。", ActionLabels: map[string]string{"open": "打开简报"}},
		}}
	item := mainChatProactive{MomentID: "legacy-digest", Kind: "digest", State: "pending", Items: []mainChatProactive{child}}
	payload, _ := json.Marshal(item)
	if _, err := app.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "old-digest", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(payload)}); err != nil {
		t.Fatal(err)
	}
	var presented mainChatProactive
	if err := json.Unmarshal([]byte(localizedMainChatToolInput(mainChatProactiveTool, string(payload))), &presented); err != nil {
		t.Fatal(err)
	}
	for _, lang := range engagement.Languages {
		if got := presented.Items[0].Localized[lang].ActionLabels[engagement.ActionMuteKind]; got != engagement.MuteKindLabel(lang) {
			t.Fatalf("%s mute label = %q", lang, got)
		}
	}
	if err := app.RecordMainChatProactiveReceipt(item.MomentID, "opened"); err == nil {
		t.Fatal("digest wrapper counted as a message")
	}
	for range 2 {
		state, err := app.ResolveMainChatProactiveAction(child.MomentID, engagement.ActionMuteKind)
		if err != nil || state != "pending" {
			t.Fatalf("mute = %s, %v", state, err)
		}
	}
	stored := mainChatProactiveItems(t, app)[0]
	if stored.State != "pending" || stored.Items[0].State != "pending" || !stored.Items[0].KindMuted {
		t.Fatalf("mute changed wrong card: %+v", stored)
	}
	prefs, err := app.store.ListEngagementPrefs(t.Context())
	if err != nil || len(prefs) != 1 || prefs[0].Kind != child.Kind || prefs[0].Enabled != 0 {
		t.Fatalf("mute not durable: %+v, %v", prefs, err)
	}
	if !engagementReceiptStates(t, app, child.MomentID, string(engagement.ChannelDesktop))["muted"] {
		t.Fatal("mute receipt missing")
	}
}

func TestMainChatDirectDeliveryRegistersOnceAndPreservesExistingMoment(t *testing.T) {
	app := mainChatTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	item := mainChatProactive{MomentID: "direct-permission", Kind: string(engagement.KindDecisionDue), State: "pending", Permission: &mainChatWorkPermission{HandoffID: "work-1"}}
	for range 2 {
		if app.postProactiveItem(item, item.MomentID) == "" {
			t.Fatal("direct report not saved")
		}
	}
	row, err := app.store.GetEngagementMoment(t.Context(), mainChatEngagementMomentID(item.MomentID))
	if err != nil || row == nil || row.SubjectID != "work-1" || row.State != storage.EngagementStateSent {
		t.Fatalf("direct moment = %+v, %v", row, err)
	}
	states := engagementReceiptStates(t, app, item.MomentID, "")
	if len(states) != 2 || !states["created"] || !states["sent"] {
		t.Fatalf("direct lifecycle = %v", states)
	}
	_, err = app.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{ID: "paced", Kind: string(engagement.KindBriefReady), DedupeKey: "paced", State: storage.EngagementStateScheduled})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.recordMainChatDelivery(mainChatProactive{MomentID: "paced", Kind: string(engagement.KindBriefReady)}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	row, _ = app.store.GetEngagementMoment(t.Context(), "paced")
	if row.State != storage.EngagementStateScheduled || row.SentAt == 0 {
		t.Fatalf("chat changed pacing state: %+v", row)
	}
}

func TestEngagementLegacyPhoneActionIsNotCompletion(t *testing.T) {
	app := mainChatTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	item := mainChatProactive{MomentID: "phone-notice", Kind: string(engagement.KindMeetingPrep)}
	if err := app.recordMainChatDelivery(item, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	app.engagementReceipt("phone-1", mainChatEngagementMomentID(item.MomentID), "acted", "open", time.Now())
	app.engagementReceipt("phone-1", mainChatEngagementMomentID(item.MomentID), "acted", "dismiss", time.Now())
	states := engagementReceiptStates(t, app, item.MomentID, string(engagement.ChannelPush))
	if !states["opened"] || !states["dismissed"] || states["acted"] {
		t.Fatalf("legacy phone action states = %v", states)
	}
}

func TestMainChatWorkCardAndNoticeShareOneMoment(t *testing.T) {
	app := mainChatTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	entry := handoff.Entry{ID: "finished-work", Title: "Prepare the report", Status: handoff.StatusCompleted, UpdatedAt: time.Now().UnixMilli()}
	app.produceWorkFinished(entry)
	app.postHandoffResult(entry, "The report is ready.")
	var rows []storage.EngagementMomentRow
	for _, state := range []string{storage.EngagementStatePending, storage.EngagementStateScheduled, storage.EngagementStateSent} {
		batch, err := app.store.ListEngagementMoments(t.Context(), state, 0)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, batch...)
	}
	if len(rows) != 1 {
		t.Fatalf("work was measured twice: %+v", rows)
	}
	card := mainChatProactiveItems(t, app)[0]
	storedCard := mainChatStructuredRows(t, app, mainChatProactiveTool)[0]
	if rows[0].ID != mainChatEngagementMomentID(card.MomentID) || !mobileValidUUID(rows[0].ID) || rows[0].SentAt != storedCard.CreatedAt {
		t.Fatalf("card and notice do not share a sent UUID: %+v / %+v", card, rows[0])
	}
	if err := app.RecordMainChatProactiveReceipt(card.MomentID, "opened"); err != nil {
		t.Fatal(err)
	}
	if !engagementReceiptStates(t, app, rows[0].ID, string(engagement.ChannelDesktop))["opened"] {
		t.Fatal("card receipt did not reach the notice moment")
	}
}

func TestMainChatEngagementMomentIDConformsToPhoneContract(t *testing.T) {
	for _, cardID := range []string{"handoff:work:completed::1792267534142", "work-permission:request", "9e5f940b-c3ae-4ae5-9296-362412a45240"} {
		id := mainChatEngagementMomentID(cardID)
		if !mobileValidUUID(id) || mainChatEngagementMomentID(cardID) != id {
			t.Fatalf("card %q does not map to a stable phone UUID: %q", cardID, id)
		}
		if mobileValidUUID(cardID) && id != cardID {
			t.Fatalf("existing notice identity changed: %q -> %q", cardID, id)
		}
	}
}

func TestMainChatMuteKeepsPermissionAnswerAvailableAndResumeRefreshes(t *testing.T) {
	app, runner := newMainChatPermissionTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	response := awaitWorkPermission(t, runner, "work-agent")
	app.emitAgentEvent(workPermissionEvent("mute-keeps-permission"))
	card := mainChatProactiveItems(t, app)[0]
	state, err := app.ResolveMainChatProactiveAction(card.MomentID, engagement.ActionMuteKind)
	if err != nil || state != "pending" || runner.calls.Load() != 0 {
		t.Fatalf("mute changed the pending permission: %s, %v, calls %d", state, err, runner.calls.Load())
	}
	if card := mainChatProactiveItems(t, app)[0]; !card.KindMuted || card.State != "pending" {
		t.Fatalf("mute lost pending controls: %+v", card)
	}
	if err := app.engagement.SetKindEnabled(t.Context(), engagement.KindDecisionDue, true); err != nil {
		t.Fatal(err)
	}
	row := mainChatStructuredRows(t, app, mainChatProactiveTool)[0]
	var refreshed mainChatProactive
	if err := json.Unmarshal([]byte(app.projectMainChatEngagementPrefs(row.ToolName, row.ToolInput)), &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.KindMuted {
		t.Fatal("resumed kind still appears muted on the card")
	}
	if state, err := app.ResolveMainChatProactiveAction(card.MomentID, "approve"); err != nil || state != "acted" || runner.calls.Load() != 1 {
		t.Fatalf("permission unavailable after mute: %s, %v", state, err)
	}
	select {
	case <-response:
	case <-time.After(time.Second):
		t.Fatal("permission answer did not reach its runner")
	}
	if engagementReceiptStates(t, app, card.MomentID, string(engagement.ChannelDesktop))["acted"] {
		t.Fatal("permission approval was measured as completed work")
	}
}

func TestMainChatRoutineStatusReceiptsUseTheirMoment(t *testing.T) {
	app := mainChatTestApp(t)
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	moment, err := app.engagement.Submit(t.Context(), engagement.Moment{Kind: engagement.KindBriefMissed, SubjectID: "morning-brief"})
	if err != nil {
		t.Fatal(err)
	}
	if !app.postEngagementReport(engagement.Moment{ID: moment.ID, Kind: engagement.KindBriefMissed, SubjectID: moment.SubjectID}, engagementText{}) {
		t.Fatal("status report not saved")
	}
	entry := mainChatStatusEntries(t, app)[0]
	if entry.MomentID != moment.ID || entry.ID == moment.ID {
		t.Fatalf("status identity link = %+v", entry)
	}
	for _, state := range []string{"delivered", "opened"} {
		if err := app.RecordMainChatProactiveReceipt(entry.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	states := engagementReceiptStates(t, app, moment.ID, string(engagement.ChannelDesktop))
	if !states["sent"] || !states["delivered"] || !states["opened"] || states["acted"] {
		t.Fatalf("routine status receipts = %v", states)
	}
	app.postMainChatStatus(mainChatStatusEntry{ID: "synthetic", Kind: "digest_done", Text: "Your updates are ready."}, time.Now())
	if err := app.RecordMainChatProactiveReceipt("synthetic", "opened"); err == nil {
		t.Fatal("synthetic status counted as another message")
	}
}

func TestMainChatLegacyWorkCardReusesOriginalNoticeMoment(t *testing.T) {
	for _, status := range []handoff.Status{handoff.StatusCompleted, handoff.StatusBlocked} {
		t.Run(string(status), func(t *testing.T) {
			app := mainChatTestApp(t)
			at := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
			entry := handoff.Entry{ID: "legacy-work", Status: status, UpdatedAt: at.UnixMilli()}
			kind := engagement.KindWorkFinished
			key := engagement.DedupeKey(kind, entry.ID)
			if status == handoff.StatusBlocked {
				kind = engagement.KindWorkBlocked
				entry.BlockedReason = handoff.BlockedTimeout
				key = engagement.DedupeKey(kind, entry.ID, engagement.HashPart(entry.BlockedReason))
			}
			originalID := uuid.NewString()
			for _, candidate := range []struct {
				id string
				at time.Time
			}{{uuid.NewString(), at.Add(-time.Hour)}, {originalID, at.Add(10 * time.Millisecond)}, {uuid.NewString(), at.Add(time.Second * 5)}} {
				if _, err := app.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{
					ID: candidate.id, Kind: string(kind), SubjectID: entry.ID, DedupeKey: key,
					State: storage.EngagementStateSent, Channel: string(engagement.ChannelPush),
					CreatedAt: candidate.at.UnixMilli(), SentAt: candidate.at.UnixMilli(),
				}); err != nil {
					t.Fatal(err)
				}
			}
			card := mainChatProactive{MomentID: mainChatHandoffMomentID(entry), Kind: string(kind), State: "pending",
				Target: mainChatTarget{Kind: mainChatTargetHandoff, ID: entry.ID}, Work: &mainChatHandoffWork{HandoffID: entry.ID, Status: status}}
			payload, _ := json.Marshal(card)
			main, err := app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := app.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{
				ID: "legacy-work-card", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(payload), CreatedAt: at.Add(20 * time.Millisecond).UnixMilli(),
			}); err != nil {
				t.Fatal(err)
			}
			app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
			before, _ := app.store.EngagementStatsSince(t.Context(), at.Add(-time.Hour*2))
			if err := app.RecordMainChatProactiveReceipt(card.MomentID, "opened"); err != nil {
				t.Fatal(err)
			}
			after, _ := app.store.EngagementStatsSince(t.Context(), at.Add(-time.Hour*2))
			if before[string(kind)].Sent != after[string(kind)].Sent {
				t.Fatalf("upgrade double counted existing work: before=%+v after=%+v", before, after)
			}
			if row, err := app.store.GetEngagementMoment(t.Context(), mainChatEngagementMomentID(card.MomentID)); err != nil || row != nil {
				t.Fatalf("upgrade created another measurement identity: %+v, %v", row, err)
			}
			if !engagementReceiptStates(t, app, originalID, string(engagement.ChannelDesktop))["opened"] {
				t.Fatal("legacy card receipt did not reach its original phone notice")
			}
		})
	}
}

func TestMainChatChangedDigestRetryRepairsOriginalChildSend(t *testing.T) {
	for _, retry := range []string{"same key", "smaller batch", "mixed batch", "single child"} {
		t.Run(retry, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "digest.db")
			store, err := storage.New(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = raw.Close() })
			app := &App{store: store, eventBroadcaster: NewEventBroadcaster()}
			app.engagement = newEngagementService(store, nil, engagementDelivery{})
			firstAt := time.Now().Truncate(time.Millisecond)
			now := firstAt
			app.engagement.now = func() time.Time { return now }
			var children []mainChatProactive
			for _, id := range []string{"first", "second", "third"} {
				if _, err := store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{ID: id, Kind: string(engagement.KindWaitingOnReply), DedupeKey: id, CreatedAt: firstAt.UnixMilli()}); err != nil {
					t.Fatal(err)
				}
				children = append(children, mainChatProactive{MomentID: id, Kind: string(engagement.KindWaitingOnReply), State: "pending"})
			}
			if _, err := raw.Exec(`CREATE TRIGGER refuse_child_send BEFORE INSERT ON engagement_receipts WHEN NEW.moment_id = 'second' AND NEW.state = 'sent' BEGIN SELECT RAISE(ABORT, 'child receipt refused'); END`); err != nil {
				t.Fatal(err)
			}
			original := mainChatProactive{MomentID: "original-digest", Kind: "digest", Items: children[:2]}
			if app.postProactiveItem(original, "original") != "" {
				t.Fatal("failed child receipt reported success")
			}
			if len(mainChatStructuredRows(t, app, mainChatProactiveTool)) != 1 {
				t.Fatal("failed child receipt lost the committed digest")
			}
			if _, err := raw.Exec(`DROP TRIGGER refuse_child_send`); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Hour)
			item, key := original, "original"
			switch retry {
			case "smaller batch":
				item, key = mainChatProactive{MomentID: "smaller", Kind: "digest", Items: children[1:2]}, "smaller"
			case "mixed batch":
				item, key = mainChatProactive{MomentID: "mixed", Kind: "digest", Items: children[1:]}, "mixed"
			case "single child":
				item, key = children[1], "standalone"
			}
			if app.postProactiveItem(item, key) == "" {
				t.Fatal("retry did not recover")
			}
			wantCards := 1
			if retry == "mixed batch" {
				wantCards = 2
			}
			if got := len(mainChatStructuredRows(t, app, mainChatProactiveTool)); got != wantCards {
				t.Fatalf("retry duplicated digest cards: %d, want %d", got, wantCards)
			}
			for _, child := range children[:2] {
				row, err := store.GetEngagementMoment(t.Context(), child.MomentID)
				if err != nil || row == nil || row.SentAt != firstAt.UnixMilli() {
					t.Fatalf("retry shifted %s first send: %+v, %v", child.MomentID, row, err)
				}
				receipts, _ := store.ListEngagementReceipts(t.Context(), child.MomentID)
				sentCount := 0
				for _, receipt := range receipts {
					if receipt.State == storage.EngagementReceiptSent {
						sentCount++
						if receipt.At != firstAt.UnixMilli() {
							t.Fatalf("retry shifted %s sent receipt: %+v", child.MomentID, receipt)
						}
					}
				}
				if sentCount != 1 {
					t.Fatalf("retry lost or duplicated %s sent receipt: %+v", child.MomentID, receipts)
				}
			}
		})
	}
}

func TestMainChatFoldedStatusRetryKeepsOriginalEntrySend(t *testing.T) {
	for _, scenario := range []string{"new row", "existing row", "later folded update"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "status.db")
			store, err := storage.New(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = raw.Close() })
			app := &App{store: store, eventBroadcaster: NewEventBroadcaster()}
			app.engagement = newEngagementService(store, nil, engagementDelivery{})
			firstAt := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
			now := firstAt
			app.engagement.now = func() time.Time { return now }
			if scenario == "existing row" {
				if app.postMainChatStatus(mainChatStatusEntry{ID: "earlier", Kind: "digest_done", Text: "Your updates are ready."}, firstAt.Add(-time.Minute)) == "" {
					t.Fatal("could not seed the status row")
				}
			}
			row, err := app.engagement.Submit(t.Context(), engagement.Moment{Kind: engagement.KindPlaybookResult, SubjectID: "status-run"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(`CREATE TRIGGER refuse_status_send BEFORE INSERT ON engagement_receipts WHEN NEW.state = 'sent' BEGIN SELECT RAISE(ABORT, 'status receipt refused'); END`); err != nil {
				t.Fatal(err)
			}
			moment := momentFromRow(row)
			if app.postEngagementReport(moment, engagementText{}) {
				t.Fatal("failed send evidence reported success")
			}
			before := mainChatStructuredRows(t, app, mainChatStatusTool)
			if len(before) != 1 {
				t.Fatal("receipt failure lost the durable status")
			}
			if scenario == "later folded update" {
				if app.postMainChatStatus(mainChatStatusEntry{ID: "later", Kind: "digest_done", Text: "Your updates are ready."}, firstAt.Add(10*time.Minute)) == "" {
					t.Fatal("could not add a later folded update")
				}
				moved := mainChatStructuredRows(t, app, mainChatStatusTool)[0]
				if moved.CreatedAt <= firstAt.UnixMilli() {
					t.Fatal("fixture did not move the folded row timestamp")
				}
			}
			if _, err := raw.Exec(`DROP TRIGGER refuse_status_send`); err != nil {
				t.Fatal(err)
			}
			now = firstAt.Add(4 * time.Hour)
			app.engagement = newEngagementService(store, nil, engagementDelivery{})
			app.engagement.now = func() time.Time { return now }
			if !app.postEngagementReport(moment, engagementText{}) {
				t.Fatal("status retry did not recover")
			}
			stored, err := store.GetEngagementMoment(t.Context(), row.ID)
			if err != nil || stored == nil || stored.SentAt != firstAt.UnixMilli() {
				t.Fatalf("status retry shifted first send: %+v, %v", stored, err)
			}
			receipts, err := store.ListEngagementReceipts(t.Context(), row.ID)
			if err != nil {
				t.Fatal(err)
			}
			sends := 0
			for _, receipt := range receipts {
				if receipt.State == storage.EngagementReceiptSent {
					sends++
					if receipt.At != firstAt.UnixMilli() {
						t.Fatalf("status retry shifted sent receipt: %+v", receipt)
					}
				}
			}
			if sends != 1 || len(mainChatStructuredRows(t, app, mainChatStatusTool)) != 1 {
				t.Fatal("status retry duplicated sends or status rows")
			}
		})
	}
}
