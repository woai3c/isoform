package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestEngagementRoutineResultRetriesFailedChatPersistence(t *testing.T) {
	for _, operation := range []string{"insert", "update", "marker"} {
		for _, explicit := range []bool{false, true} {
			name := operation + "/automatic"
			if explicit {
				name = operation + "/chosen desktop"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "report.db")
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
				now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
				app := &App{store: store, eventBroadcaster: NewEventBroadcaster()}
				sent := 0
				delivery := engagementDelivery{
					Report: app.postEngagementReport,
					Desktop: func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
						sent++
						return nil
					},
				}
				service := newEngagementService(store, nil, delivery)
				service.now = func() time.Time { return now }
				app.engagement = service
				existing := 0
				trigger := `CREATE TRIGGER refuse_chat_report BEFORE INSERT ON stream_items WHEN NEW.tool_name = 'main_chat_status' BEGIN SELECT RAISE(ABORT, 'chat write refused'); END`
				switch operation {
				case "update":
					if app.postMainChatStatus(mainChatStatusEntry{ID: "earlier", Kind: "digest_done", Text: "Your digest is ready."}, now) == "" {
						t.Fatal("seed folded status row")
					}
					existing = 1
					trigger = `CREATE TRIGGER refuse_chat_report BEFORE UPDATE OF tool_input ON stream_items WHEN NEW.tool_name = 'main_chat_status' BEGIN SELECT RAISE(ABORT, 'chat write refused'); END`
				case "marker":
					trigger = `CREATE TRIGGER refuse_chat_report BEFORE UPDATE OF params ON engagement_moments WHEN json_extract(NEW.params, '$._chat_reported') = 'true' BEGIN SELECT RAISE(ABORT, 'report marker refused'); END`
				}
				if _, err := raw.Exec(trigger); err != nil {
					t.Fatal(err)
				}
				moment := engagement.Moment{Kind: engagement.KindPlaybookResult, SubjectID: "durable-result", ExpiresAt: now.Add(24 * time.Hour), Params: map[string]string{"title": "Weekly review", "summary": "Your review is ready."}}
				if explicit {
					moment.Channels = []engagement.Channel{engagement.ChannelDesktop}
				}
				row, err := service.Submit(ctx, moment)
				if err != nil {
					t.Fatal(err)
				}
				service.Pass(ctx)
				stored, err := store.GetEngagementMoment(ctx, row.ID)
				firstSentAt, wantMeasuredSends := int64(0), 0
				if operation == "marker" {
					// The card is durable even though its retry marker failed.
					firstSentAt, wantMeasuredSends = now.UnixMilli(), 1
				}
				if err != nil || stored == nil || stored.State != storage.EngagementStateScheduled || stored.Reason != "chat report" || stored.Channel != "" || stored.SentAt != firstSentAt {
					t.Fatalf("failed chat write was acknowledged as delivered: %+v, %v", stored, err)
				}
				stats, err := store.EngagementStatsSince(ctx, now.Add(-time.Hour))
				if err != nil || stats[string(engagement.KindPlaybookResult)].Sent != wantMeasuredSends {
					t.Fatalf("durable chat send count = %+v, %v; want %d", stats, err, wantMeasuredSends)
				}
				if engagementReportedInChat(*stored) || sent != 0 || service.state.SentByKindToday[engagement.KindPlaybookResult] != 0 {
					t.Fatal("failed chat write recorded a report, sent a chosen channel or consumed pacing")
				}
				wantExisting := existing
				if operation == "marker" {
					wantExisting++
				}
				if entries := mainChatStatusEntries(t, app); len(entries) != wantExisting {
					t.Fatalf("failed write changed the status row: %+v", entries)
				}
				beforeRetry := mainChatStructuredRows(t, app, mainChatStatusTool)

				// A restart must retry the unacknowledged chat write before any
				// requested delivery, preserving the result and its pacing count.
				if _, err := raw.Exec(`DROP TRIGGER refuse_chat_report`); err != nil {
					t.Fatal(err)
				}
				now = now.Add(engagementRetryDelay)
				if operation == "marker" {
					// The content was committed but its service receipt was not.
					// A later period must find the original entry without adding
					// another row or moving the original to the tail again.
					now = now.Add(4 * time.Hour)
				}
				service = newEngagementService(store, nil, delivery)
				service.now = func() time.Time { return now }
				if err := service.load(ctx); err != nil {
					t.Fatal(err)
				}
				app.engagement = service
				if firstSentAt == 0 {
					firstSentAt = now.UnixMilli()
				}
				service.Pass(ctx)
				service.Pass(ctx)
				stored, err = store.GetEngagementMoment(ctx, row.ID)
				if err != nil || stored == nil || stored.State != storage.EngagementStateSent || !engagementReportedInChat(*stored) || stored.SentAt != firstSentAt {
					t.Fatalf("recovered chat write did not finish delivery: %+v, %v", stored, err)
				}
				stats, err = store.EngagementStatsSince(ctx, time.UnixMilli(firstSentAt).Add(-time.Hour))
				if err != nil || stats[string(engagement.KindPlaybookResult)].Sent != 1 {
					t.Fatalf("retry must preserve one measured send: %+v, %v", stats, err)
				}
				wantSent := 0
				if explicit {
					wantSent = 1
				}
				if sent != wantSent || service.state.SentByKindToday[engagement.KindPlaybookResult] != 1 || len(mainChatStatusEntries(t, app)) != existing+1 {
					t.Fatalf("retry duplicated or lost result: sent=%d pacing=%d entries=%+v", sent, service.state.SentByKindToday[engagement.KindPlaybookResult], mainChatStatusEntries(t, app))
				}
				if operation == "marker" {
					afterRetry := mainChatStructuredRows(t, app, mainChatStatusTool)
					if len(beforeRetry) != 1 || len(afterRetry) != 1 || afterRetry[0].ID != beforeRetry[0].ID || afterRetry[0].SortOrder != beforeRetry[0].SortOrder {
						t.Fatalf("receipt retry repeated committed chat content: before=%+v after=%+v", beforeRetry, afterRetry)
					}
				}
			})
		}
	}
}
