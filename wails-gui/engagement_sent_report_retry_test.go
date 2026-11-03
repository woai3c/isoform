package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func engagementRetryMeeting(t *testing.T, app *App, now, expiresAt time.Time, channels []engagement.Channel) engagement.Moment {
	t.Helper()
	event := proactiveVoiceTestEvent(t, app, now)
	moment := meetingPrepMoment(event, now, time.Local)
	moment.DueAt = now
	moment.ExpiresAt = expiresAt
	moment.Channels = channels
	return moment
}

// These failures occur after a real channel has accepted the moment. The
// channel delivery and its pacing remain committed while the chat write retries.
func TestEngagementSentMomentRetriesChatReportAfterRestart(t *testing.T) {
	for _, failure := range []string{"insert", "marker", "busy"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "sent-report.db")
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
			raw.SetMaxOpenConns(1)
			app := &App{store: store, eventBroadcaster: NewEventBroadcaster()}
			if _, err := app.ensureMainChat(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
			firstSent := now
			sends, attempts := 0, 0
			failing := true
			trigger := `CREATE TRIGGER refuse_sent_report BEFORE INSERT ON stream_items WHEN NEW.tool_name = 'main_chat_proactive' BEGIN SELECT RAISE(ABORT, 'chat write refused'); END`
			if failure == "marker" {
				trigger = `CREATE TRIGGER refuse_sent_report BEFORE UPDATE OF params ON engagement_moments WHEN json_extract(NEW.params, '$._chat_reported') = 'true' BEGIN SELECT RAISE(ABORT, 'report marker refused'); END`
			}
			if failure != "busy" {
				if _, err := raw.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			delivery := engagementDelivery{
				Desktop: func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
					sends++
					return nil
				},
				Report: func(m engagement.Moment, text engagementText) bool {
					attempts++
					if failure == "busy" && failing {
						if _, err := raw.Exec(`BEGIN IMMEDIATE`); err != nil {
							t.Fatal(err)
						}
						defer func() {
							if _, err := raw.Exec(`ROLLBACK`); err != nil {
								t.Fatal(err)
							}
						}()
					}
					return app.postEngagementReport(m, text)
				},
			}
			service := newEngagementService(store, nil, delivery)
			service.now = func() time.Time { return now }
			app.engagement = service
			moment := engagementRetryMeeting(t, app, now, now.Add(time.Minute), []engagement.Channel{engagement.ChannelDesktop})
			row, err := service.Submit(ctx, moment)
			if err != nil {
				t.Fatal(err)
			}
			service.Pass(ctx)
			stored, err := store.GetEngagementMoment(ctx, row.ID)
			if err != nil || stored == nil || stored.State != storage.EngagementStateSent || stored.Reason != "chat report" || stored.HoldUntil != now.Add(engagementRetryDelay).UnixMilli() || engagementReportedInChat(*stored) {
				t.Fatalf("sent moment lost its pending chat report: %+v, %v", stored, err)
			}
			if sends != 1 || attempts != 1 || stored.SentAt != firstSent.UnixMilli() || service.state.SentByKindToday[engagement.KindMeetingPrep] != 1 {
				t.Fatal("failed chat report changed delivery or pacing")
			}
			service.Pass(ctx)
			if attempts != 1 {
				t.Fatal("chat retry ignored its hold")
			}
			if failure != "busy" {
				if _, err := raw.Exec(`DROP TRIGGER refuse_sent_report`); err != nil {
					t.Fatal(err)
				}
			}
			failing = false
			now = now.Add(engagementRetryDelay)
			service = newEngagementService(store, nil, delivery)
			service.now = func() time.Time { return now }
			if err := service.load(ctx); err != nil {
				t.Fatal(err)
			}
			// Cooldown and expiry may stop another external notification;
			// presentation still retries while the kind remains enabled.
			app.engagement = service
			service.Pass(ctx)
			service.Pass(ctx)
			stored, err = store.GetEngagementMoment(ctx, row.ID)
			if err != nil || stored == nil || stored.State != storage.EngagementStateSent || stored.Reason == "chat report" || !engagementReportedInChat(*stored) || stored.SentAt != firstSent.UnixMilli() {
				t.Fatalf("chat retry did not complete: %+v, %v", stored, err)
			}
			if sends != 1 || attempts != 2 || service.state.SentByKindToday[engagement.KindMeetingPrep] != 1 || len(mainChatProactiveItems(t, app)) != 1 {
				t.Fatalf("chat retry duplicated delivery, pacing or card: sends=%d attempts=%d pacing=%d", sends, attempts, service.state.SentByKindToday[engagement.KindMeetingPrep])
			}
		})
	}
}

func TestEngagementPartialDeliveryRetriesChatBeforeRemainingChannels(t *testing.T) {
	for _, failure := range []string{"insert", "marker"} {
		for _, expired := range []bool{false, true} {
			name := failure + "/live"
			if expired {
				name = failure + "/expired"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "partial-report.db")
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
				now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
				desktop, push, attempts := 0, 0, 0
				pushReady := false
				delivery := engagementDelivery{
					Desktop: func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
						desktop++
						return nil
					},
					Push: func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
						if !pushReady {
							return errors.New("offline")
						}
						push++
						return nil
					},
					Report: func(m engagement.Moment, text engagementText) bool {
						attempts++
						return app.postEngagementReport(m, text)
					},
				}
				presence := func(time.Time) engagement.Presence { return engagement.Presence{PhoneRegistered: true} }
				service := newEngagementService(store, presence, delivery)
				service.now = func() time.Time { return now }
				app.engagement = service
				trigger := `CREATE TRIGGER refuse_partial_report BEFORE INSERT ON stream_items WHEN NEW.tool_name = 'main_chat_proactive' BEGIN SELECT RAISE(ABORT, 'chat write refused'); END`
				if failure == "marker" {
					trigger = `CREATE TRIGGER refuse_partial_report BEFORE UPDATE OF params ON engagement_moments WHEN json_extract(NEW.params, '$._chat_reported') = 'true' BEGIN SELECT RAISE(ABORT, 'report marker refused'); END`
				}
				if _, err := raw.Exec(trigger); err != nil {
					t.Fatal(err)
				}
				expiresAt := now.Add(time.Hour)
				if expired {
					expiresAt = now.Add(time.Minute)
				}
				moment := engagementRetryMeeting(t, app, now, expiresAt, []engagement.Channel{engagement.ChannelDesktop, engagement.ChannelPush})
				row, err := service.Submit(ctx, moment)
				if err != nil {
					t.Fatal(err)
				}
				service.Pass(ctx)
				stored, err := store.GetEngagementMoment(ctx, row.ID)
				if err != nil || stored == nil || stored.State != storage.EngagementStateScheduled || stored.Reason != "chat report" || stored.Channel != "desktop" || engagementReportedInChat(*stored) {
					t.Fatalf("partial delivery lost pending report: %+v, %v", stored, err)
				}
				pushReady = true
				now = now.Add(engagementRetryDelay)
				service = newEngagementService(store, presence, delivery)
				service.now = func() time.Time { return now }
				if err := service.load(ctx); err != nil {
					t.Fatal(err)
				}
				app.engagement = service
				service.Pass(ctx)
				if desktop != 1 || push != 0 || attempts != 2 {
					t.Fatalf("remaining channel overtook failed chat retry: desktop=%d push=%d reports=%d", desktop, push, attempts)
				}
				if _, err := raw.Exec(`DROP TRIGGER refuse_partial_report`); err != nil {
					t.Fatal(err)
				}
				now = now.Add(engagementRetryDelay)
				service.Pass(ctx)
				service.Pass(ctx)
				stored, err = store.GetEngagementMoment(ctx, row.ID)
				wantState, wantPush, wantPacing := storage.EngagementStateSent, 1, 1
				if expired {
					wantState, wantPush, wantPacing = storage.EngagementStateExpired, 0, 0
				}
				if err != nil || stored == nil || stored.State != wantState || !engagementReportedInChat(*stored) {
					t.Fatalf("partial report retry failed: %+v, %v", stored, err)
				}
				if desktop != 1 || push != wantPush || attempts != 3 || service.state.SentByKindToday[engagement.KindMeetingPrep] != wantPacing || len(mainChatProactiveItems(t, app)) != 1 {
					t.Fatalf("partial retry lost or duplicated delivery: desktop=%d push=%d reports=%d pacing=%d", desktop, push, attempts, service.state.SentByKindToday[engagement.KindMeetingPrep])
				}
			})
		}
	}
}

func TestEngagementForgetCancelsSentChatReportRetry(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	attempts := 0
	rig.svc.deliver.Report = func(engagement.Moment, engagementText) bool { attempts++; return false }
	row, err := rig.svc.Submit(ctx, engagement.Moment{Kind: engagement.KindMeetingPrep, SubjectID: "forgotten", Channels: []engagement.Channel{engagement.ChannelDesktop}, Params: map[string]string{"title": "Private meeting"}})
	if err != nil {
		t.Fatal(err)
	}
	rig.svc.Pass(ctx)
	// Forget also cancels generic reports whose fields were already blanked.
	if err := rig.store.SetEngagementMomentParams(ctx, row.ID, "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.store.ForgetEngagementMomentsOfKind(ctx, string(engagement.KindMeetingPrep), "source disabled"); err != nil {
		t.Fatal(err)
	}
	rig.clock = rig.clock.Add(engagementRetryDelay)
	rig.svc.Pass(ctx)
	stored, err := rig.store.GetEngagementMoment(ctx, row.ID)
	if err != nil || stored == nil || stored.State != storage.EngagementStateSent || stored.Reason != "source disabled" || stored.Params != "{}" || stored.SentAt == 0 {
		t.Fatalf("forget did not cancel pending report while preserving delivery: %+v, %v", stored, err)
	}
	if attempts != 1 || len(rig.desktop) != 1 || rig.svc.state.SentByKindToday[engagement.KindMeetingPrep] != 1 {
		t.Fatal("forgotten source was retried or delivery count changed")
	}
}

func TestEngagementChatReportRetryDoesNotEnterDigest(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	attempts, digests := 0, 0
	rig.svc.deliver.Report = func(engagement.Moment, engagementText) bool { attempts++; return false }
	rig.svc.deliver.Digest = func(context.Context, []engagementDigestItem) (engagement.Channel, error) {
		digests++
		return engagement.ChannelDesktop, nil
	}
	row, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
		Kind: string(engagement.KindCalendarConflict), DedupeKey: "calendar_conflict:delivered", State: storage.EngagementStateScheduled,
		Channel: "desktop", Reason: "chat report", HoldUntil: rig.clock.UnixMilli(), Params: `{"summary":"Two events overlap."}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.svc.Pass(ctx)
	stored, err := rig.store.GetEngagementMoment(ctx, row.ID)
	if err != nil || stored == nil || stored.Reason != "chat report" || stored.HoldUntil != rig.clock.Add(engagementRetryDelay).UnixMilli() {
		t.Fatalf("digestable chat retry lost: %+v, %v", stored, err)
	}
	if attempts != 1 || digests != 0 || len(rig.desktop)+len(rig.pushed) != 0 {
		t.Fatalf("delivered report became another notification: report=%d digest=%d", attempts, digests)
	}
}

func TestEngagementUnrecordedDeliveryRetriesBeforePacingAndExpiry(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "sent"
		if partial {
			name = "partial"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "unrecorded-report.db")
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
			now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
			firstSent := now
			desktop, push := 0, 0
			service := newEngagementService(store, func(time.Time) engagement.Presence { return engagement.Presence{PhoneRegistered: true} }, engagementDelivery{
				Desktop: func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
					desktop++
					return nil
				},
				Push: func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
					push++
					return errors.New("offline")
				},
				Report: app.postEngagementReport,
			})
			service.now = func() time.Time { return now }
			app.engagement = service
			for _, stmt := range []string{
				`CREATE TRIGGER refuse_unrecorded_report BEFORE INSERT ON stream_items WHEN NEW.tool_name = 'main_chat_proactive' BEGIN SELECT RAISE(ABORT, 'chat write refused'); END`,
				`CREATE TRIGGER refuse_sent_state BEFORE UPDATE OF state ON engagement_moments BEGIN SELECT RAISE(ABORT, 'state write refused'); END`,
			} {
				if _, err := raw.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
			channels := []engagement.Channel{engagement.ChannelDesktop}
			if partial {
				channels = append(channels, engagement.ChannelPush)
			}
			moment := engagementRetryMeeting(t, app, now, now.Add(time.Minute), channels)
			row, err := service.Submit(ctx, moment)
			if err != nil {
				t.Fatal(err)
			}
			service.Pass(ctx)
			if len(service.unrecorded) != 1 {
				t.Fatal("failed delivery state was not retained in memory")
			}
			now = now.Add(engagementRetryDelay)
			service.Pass(ctx)
			for _, stmt := range []string{`DROP TRIGGER refuse_unrecorded_report`, `DROP TRIGGER refuse_sent_state`} {
				if _, err := raw.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
			service.Pass(ctx)
			service.Pass(ctx)
			stored, err := store.GetEngagementMoment(ctx, row.ID)
			wantState, wantPacing, wantPush := storage.EngagementStateSent, 1, 0
			if partial {
				wantState, wantPacing, wantPush = storage.EngagementStateExpired, 0, 1
			}
			if err != nil || stored == nil || stored.State != wantState || !engagementReportedInChat(*stored) {
				t.Fatalf("unrecorded delivery lost chat after recovery: %+v, %v", stored, err)
			}
			if !partial && (stored.SentAt != firstSent.UnixMilli() || !service.state.LastSentByKey[row.DedupeKey].Equal(firstSent)) {
				t.Fatal("recovery moved the original delivery time")
			}
			if desktop != 1 || push != wantPush || len(service.unrecorded) != 0 || service.state.SentByKindToday[engagement.KindMeetingPrep] != wantPacing || len(mainChatProactiveItems(t, app)) != 1 {
				t.Fatalf("unrecorded retry duplicated or lost delivery: desktop=%d push=%d pending=%d pacing=%d", desktop, push, len(service.unrecorded), service.state.SentByKindToday[engagement.KindMeetingPrep])
			}
		})
	}
}

func TestEngagementDigestUnrecordedDeliveryKeepsOriginalPacing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "digest-report.db")
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
	if _, err := raw.Exec(`CREATE TRIGGER refuse_digest_state BEFORE UPDATE OF state ON engagement_moments WHEN NEW.state = 'sent' BEGIN SELECT RAISE(ABORT, 'state write refused'); END`); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, eventBroadcaster: NewEventBroadcaster()}
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	digests := 0
	delivery := engagementDelivery{Report: func(m engagement.Moment, _ engagementText) bool {
		return app.postEngagementReport(m, engagementText{PushBody: "A suggestion is ready.", Title: "A focused idea is ready."})
	}, Digest: func(ctx context.Context, items []engagementDigestItem) (engagement.Channel, error) {
		digests++
		return app.engagementDigest(ctx, items)
	}}
	service := newEngagementService(store, nil, delivery)
	service.now = func() time.Time { return now }
	app.engagement = service
	row, err := service.Submit(ctx, engagement.Moment{Kind: engagement.KindSuggestion, SubjectID: "suggestion", ExpiresAt: now.Add(3*time.Hour + time.Minute), Params: map[string]string{"title": "A focused idea"}})
	if err != nil {
		t.Fatal(err)
	}
	service.Pass(ctx)
	now = now.Add(3 * time.Hour)
	firstSent := now
	service.Pass(ctx)
	if digests != 1 || len(service.unrecorded) != 1 || service.state.SentByKindToday[engagement.KindSuggestion] != 1 {
		t.Fatal("delivered digest did not retain failed receipt")
	}
	if _, err := raw.Exec(`DROP TRIGGER refuse_digest_state`); err != nil {
		t.Fatal(err)
	}
	now = now.Add(engagementRetryDelay)
	service.Pass(ctx)
	stored, err := store.GetEngagementMoment(ctx, row.ID)
	if err != nil || stored == nil || stored.State != storage.EngagementStateSent || stored.SentAt != firstSent.UnixMilli() || stored.Level != "passive" || stored.Score == 0 {
		t.Fatalf("digest receipt lost its original delivery: %+v, %v", stored, err)
	}
	service = newEngagementService(store, nil, delivery)
	service.now = func() time.Time { return now }
	if err := service.load(ctx); err != nil {
		t.Fatal(err)
	}
	service.Pass(ctx)
	if digests != 1 || service.state.SentByKindToday[engagement.KindSuggestion] != 1 || !service.state.LastSentByKey[row.DedupeKey].Equal(firstSent) || len(mainChatProactiveItems(t, app)) != 1 {
		t.Fatal("digest recovery changed pacing or repeated its chat item")
	}
}
