package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"wails-gui/internal/engagement"

	"wails-gui/storage"
)

func TestOpenAnswerBindingsAndProjection(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	session, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := storage.OpenAnswerRow{ID: "a", ChatSessionID: session.LocalID, AssistantMessageID: "promise", Question: "Did Priya reply?", Topic: "replies", Settles: "reply", Source: "Mail", WatchedEvent: `{"who":"Priya","label":"reply"}`, Delivery: "around", ExpiresAt: now.Add(7 * 24 * time.Hour).UnixMilli(), State: "open", CreatedAt: now.UnixMilli(), LearnedAt: now.UnixMilli()}
	if _, err := app.store.AppendStreamItem(ctx, session.LocalID, storage.StreamItemRow{ID: "promise", Kind: "assistant", Content: "Not yet."}); err != nil {
		t.Fatal(err)
	}
	if err := app.store.CreateOpenAnswer(ctx, row); err != nil {
		t.Fatal(err)
	}
	events := app.eventBroadcaster.AddClient("answers")
	defer app.eventBroadcaster.RemoveClient("answers")

	list, err := app.ListOpenAnswers()
	if err != nil || len(list) != 1 || list[0].SessionID != session.LocalID || list[0].MessageID != "promise" || list[0].Kind != "replies" || list[0].WatchedEvent == nil || list[0].WatchedEvent.Who != "Priya" || !list[0].Learned {
		t.Fatalf("list = %+v, %v", list, err)
	}
	raw, err := json.Marshal(list[0])
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded["learned"] != true {
		t.Fatalf("learned projection = %v", encoded["learned"])
	}
	if _, err := time.Parse(time.RFC3339Nano, encoded["createdAt"].(string)); err != nil {
		t.Fatal(err)
	}

	changed, err := app.ChangeOpenAnswer("a", "brief")
	if err != nil || changed.Delivery != "brief" {
		t.Fatalf("change = %+v, %v", changed, err)
	}
	awaitBrowserEvent(t, events, openAnswersChangedEvent)
	if _, err := app.ChangeOpenAnswer("a", "invalid"); err == nil {
		t.Fatal("invalid delivery accepted")
	}
	if err := app.store.CommitOpenAnswerTransition(ctx, "a", "settled", now.UnixMilli(), &storage.StreamItemRow{ID: "post", Kind: "assistant", Content: "Priya replied."}); err != nil {
		t.Fatal(err)
	}
	page, err := app.LoadStreamItemsPage(session.LocalID, -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]StreamItemData{}
	for _, item := range page.Items {
		seen[item.ID] = item
	}
	if seen["promise"].OpenAnswer == nil || seen["promise"].CardType != "" || seen["post"].OpenAnswer == nil || seen["post"].CardType != "answer_ready" || seen["post"].CreatedAt == 0 {
		t.Fatalf("projection = %+v", seen)
	}
	if err := app.NotForMeAnswer("promise"); err == nil {
		t.Fatal("accepted a promise instead of a post")
	}
	if err := app.NotForMeAnswer("post"); err != nil {
		t.Fatal(err)
	}
	awaitBrowserEvent(t, events, openAnswersChangedEvent)
	muted, err := app.store.OpenAnswerKindMuted(ctx, "replies")
	if err != nil || !muted {
		t.Fatalf("muted = %v %v", muted, err)
	}
	if err := app.UndoNotForMeAnswer("replies"); err != nil {
		t.Fatal(err)
	}
	awaitBrowserEvent(t, events, openAnswersChangedEvent)
	muted, err = app.store.OpenAnswerKindMuted(ctx, "replies")
	if err != nil || muted {
		t.Fatalf("muted after undo = %v %v", muted, err)
	}
	row, err = app.store.GetOpenAnswer(ctx, "a")
	if err != nil || row.LearnedAt >= 0 || openAnswerView(row).Learned {
		t.Fatalf("learned_at after undo = %+v, %v", row, err)
	}

	row.ID, row.AssistantMessageID = "b", "promise-b"
	row.State, row.Posts, row.SawAt = "open", 0, 0
	row.CreatedAt, row.ExpiresAt = now.UnixMilli(), now.Add(7*24*time.Hour).UnixMilli()
	if err := app.store.CreateOpenAnswer(ctx, row); err != nil {
		t.Fatal(err)
	}
	cancelled, err := app.CancelOpenAnswer("b")
	if err != nil || cancelled.State != "cancelled" {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	awaitBrowserEvent(t, events, openAnswersChangedEvent)
}

func createAppAnswerPost(t *testing.T, app *App, sessionID, id, topic string, createdAt time.Time, withMoment bool) string {
	t.Helper()
	row := storage.OpenAnswerRow{
		ID: id, ChatSessionID: sessionID, AssistantMessageID: "promise-" + id,
		Question: "When will the result be final?", Topic: topic, Settles: "result", Source: "Official results",
		WindowStart: createdAt.Add(-time.Hour).UnixMilli(), WindowEnd: createdAt.Add(time.Hour).UnixMilli(),
		Delivery: "around", ExpiresAt: createdAt.Add(7 * 24 * time.Hour).UnixMilli(),
		State: "open", CreatedAt: createdAt.Add(-2 * time.Hour).UnixMilli(),
	}
	if err := app.store.CreateOpenAnswer(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	messageID := "post-" + id
	if err := app.store.CommitOpenAnswerTransition(t.Context(), id, "settled", createdAt.UnixMilli(),
		&storage.StreamItemRow{ID: messageID, Kind: "assistant", Content: "The result is final.", CreatedAt: createdAt.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	momentID := "moment-" + id
	if withMoment {
		params, err := json.Marshal(map[string]string{"message_id": messageID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{
			ID: momentID, Kind: "answer_ready", SubjectID: id, DedupeKey: "answer_ready:" + id,
			CreatedAt: createdAt.UnixMilli(), Params: string(params),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return momentID
}

func TestOpenAnswerPostAndRecoveryLearnUnopenedKinds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		afterPost bool
		wantMute  bool
	}{
		{name: "after recent post", afterPost: true},
		{name: "startup recovery", wantMute: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := mainChatTestApp(t)
			ctx := context.Background()
			session, err := app.ensureMainChat(ctx)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			createAppAnswerPost(t, app, session.LocalID, "old", "Results", now.Add(-72*time.Hour), true)
			createAppAnswerPost(t, app, session.LocalID, "newer", "results", now.Add(-48*time.Hour), true)
			if tc.afterPost {
				current := storage.OpenAnswerRow{
					ID: "current", ChatSessionID: session.LocalID, AssistantMessageID: "promise-current",
					Question: "When will the result be final?", Topic: "results", Settles: "result", Source: "Official results",
					WindowStart: now.Add(-time.Hour).UnixMilli(), WindowEnd: now.Add(time.Hour).UnixMilli(),
					Delivery: "brief", ExpiresAt: now.Add(7 * 24 * time.Hour).UnixMilli(),
					State: "open", CreatedAt: now.UnixMilli(), HeldTransition: "settled", HeldText: "A new result is ready.",
				}
				if err := app.store.CreateOpenAnswer(ctx, current); err != nil {
					t.Fatal(err)
				}
				if err := app.postOpenAnswerTransition(ctx, current.ID, "settled", current.HeldText, now, true); err != nil {
					t.Fatal(err)
				}
			} else {
				app.recoverOpenAnswerChecks()
			}
			muted, err := app.store.OpenAnswerKindMuted(ctx, "RESULTS")
			if err != nil || muted != tc.wantMute {
				t.Fatalf("kind muted = %v, %v; want %v", muted, err, tc.wantMute)
			}
			newer, err := app.store.GetOpenAnswer(ctx, "newer")
			if err != nil || (newer.LearnedAt != 0) != tc.wantMute {
				t.Fatalf("newer record learned = %+v, %v", newer, err)
			}
			if tc.afterPost {
				page, err := app.LoadStreamItemsPage(session.LocalID, -1, 100)
				if err != nil {
					t.Fatal(err)
				}
				var projectedLearned bool
				for _, item := range page.Items {
					if item.ID == "post-newer" && item.OpenAnswer != nil && item.OpenAnswer.Learned {
						projectedLearned = true
					}
				}
				if projectedLearned {
					t.Fatal("a recent post let older records mute the kind")
				}
			}
		})
	}
}

func TestOpenAnswerPostSchedulesMaturityEvaluation(t *testing.T) {
	// No quiet hours: the default 22:00-08:00 window would hold the post at night.
	app, _, _ := openAnswerQuietTestApp(t, engagement.QuietWindow{})
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	previousAfterFunc := openAnswerAfterFunc
	var callbacks []func()
	openAnswerAfterFunc = func(_ time.Duration, callback func()) *time.Timer {
		callbacks = append(callbacks, callback)
		return nil
	}
	t.Cleanup(func() { openAnswerAfterFunc = previousAfterFunc })

	now := time.Now().UTC().Truncate(time.Millisecond)
	for index, age := range []time.Duration{48 * time.Hour, 36 * time.Hour} {
		createdAt := now.Add(-age)
		id := []string{"mature-old", "mature-newer"}[index]
		answer := storage.OpenAnswerRow{
			ID: id, ChatSessionID: session.LocalID, AssistantMessageID: "promise-" + id,
			Question: "When will the Metro runoff result be final?", Topic: "Metro runoff",
			Settles: "result", Source: "Official results",
			WindowStart: createdAt.Add(-time.Hour).UnixMilli(), WindowEnd: createdAt.Add(time.Hour).UnixMilli(),
			Delivery: "around", ExpiresAt: now.Add(7 * 24 * time.Hour).UnixMilli(),
			State: "open", CreatedAt: createdAt.UnixMilli(),
		}
		if err := app.store.CreateOpenAnswer(t.Context(), answer); err != nil {
			t.Fatal(err)
		}
		if err := app.postOpenAnswerTransition(t.Context(), id, "settled", "The result is final.", createdAt, false); err != nil {
			t.Fatal(err)
		}
		items, err := app.store.LoadStreamItems(t.Context(), session.LocalID)
		if err != nil {
			t.Fatal(err)
		}
		var messageID string
		for _, item := range items {
			if item.RunID == "open-answer:"+id {
				messageID = item.ID
				break
			}
		}
		if messageID == "" {
			t.Fatalf("post message for %s not found", id)
		}
		params, err := json.Marshal(map[string]string{"message_id": messageID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{
			ID: "maturity-moment-" + id, Kind: "answer_ready", SubjectID: id,
			DedupeKey: "answer_ready:" + id, CreatedAt: createdAt.UnixMilli(), Params: string(params),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(callbacks) != 2 {
		t.Fatalf("scheduled callbacks = %d, want 2", len(callbacks))
	}

	gaveUp := storage.OpenAnswerRow{
		ID: "mature-gave-up", ChatSessionID: session.LocalID, AssistantMessageID: "promise-mature-gave-up",
		Question: "When will the other result be final?", Topic: "Other result",
		Settles: "result", Source: "Official results",
		WindowStart: now.Add(-time.Hour).UnixMilli(), WindowEnd: now.Add(time.Hour).UnixMilli(),
		Delivery: "around", ExpiresAt: now.Add(7 * 24 * time.Hour).UnixMilli(),
		State: "open", CreatedAt: now.UnixMilli(),
	}
	if err := app.store.CreateOpenAnswer(t.Context(), gaveUp); err != nil {
		t.Fatal(err)
	}
	if err := app.postOpenAnswerTransition(t.Context(), gaveUp.ID, "gave_up", "The result is unavailable.", now, false); err != nil {
		t.Fatal(err)
	}
	if len(callbacks) != 2 {
		t.Fatalf("gave_up scheduled a maturity callback: %d", len(callbacks))
	}

	callbacks[0]()
	muted, err := app.store.OpenAnswerKindMuted(t.Context(), "metro runoff")
	if err != nil || !muted {
		t.Fatalf("mature kind muted = %v, %v", muted, err)
	}
	learned, err := app.store.GetOpenAnswer(t.Context(), "mature-newer")
	if err != nil || learned.LearnedAt <= 0 || !openAnswerView(learned).Learned {
		t.Fatalf("learned line = %+v, %v", learned, err)
	}
}
