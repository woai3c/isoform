package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

func TestTodayChatRejectedDraftNeverAdvancesPhoneCursor(t *testing.T) {
	app, tag, ref := todaySendTestApp(t)
	ctx := context.Background()
	cursor, err := app.store.AppendStreamItem(ctx, tag, storage.StreamItemRow{ID: "previous-user", Kind: "user", Content: "Previous request"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := app.PrepareTodayChatDraft(ref)
	if err != nil {
		t.Fatal(err)
	}
	ref.GeneratedTitle, ref.GeneratedFingerprint = draft.Prefill.Title, draft.Fingerprint
	prompt := "Help with \"" + draft.Prefill.Title + "\"."
	if _, err := app.SaveMemoryEntry(ref.SourceID, "# Public current title\nCurrent source."); err != nil {
		t.Fatal(err)
	}
	app.runner = &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	if order, err := app.SendPromptWithTodayContext(prompt, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "rejected-user", Kind: "user"}, ref); order != -1 || !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("stale draft admission = %d, %v", order, err)
	}
	page, err := app.store.LoadStreamItemsAfter(ctx, tag, cursor, 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("rejected draft reached the phone cursor: %+v, %v", page, err)
	}
	draft, err = app.PrepareTodayChatDraft(ref)
	if err != nil {
		t.Fatal(err)
	}
	ref.GeneratedTitle, ref.GeneratedFingerprint = draft.Prefill.Title, draft.Fingerprint
	prompt = "Help with \"" + draft.Prefill.Title + "\"."
	userItem := StreamItemData{ID: "accepted-user", Kind: "user", Content: "Untrusted renderer content", Images: `[{"mediaType":"image/png","dataBase64":"aW1hZ2U="}]`, Files: `[{"fileName":"notes.txt","mediaType":"text/plain","fileSize":5,"category":"text"}]`}
	order, err := app.SendPromptWithTodayContext(prompt, "", "m", "", tag, nil, nil, false, false, "en", "", userItem, ref)
	if err != nil || order <= cursor {
		t.Fatalf("accepted draft admission = %d after %d, %v", order, cursor, err)
	}
	waitForTagIdle(t, app, tag)
	page, err = app.store.LoadStreamItemsAfter(ctx, tag, cursor, 100)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("accepted draft missing from phone page: %+v, %v", page, err)
	}
	row := page.Items[0]
	if row.ID != userItem.ID || row.Content != prompt || row.SortOrder != order || row.Images != userItem.Images || row.Files != userItem.Files {
		t.Fatalf("admitted row lost canonical content, attachments or order: %+v", row)
	}
	if _, visible := mobileChatItemFromRow(row); !visible {
		t.Fatal("admitted user row is not visible on the phone")
	}
}

type todayChatStopRunner struct {
	upcomingCaptureRunner
}

func (r *todayChatStopRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, _ agent.StreamCallback) (string, error) {
	r.inputs <- input
	<-ctx.Done()
	return "", ctx.Err()
}

func TestTodayChatStopAfterAdmissionKeepsUserHistory(t *testing.T) {
	app, tag, ref := todaySendTestApp(t)
	runner := &todayChatStopRunner{upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}}
	app.runner = runner
	const prompt = "Help with the launch plan."
	order, err := app.SendPromptWithTodayContext(prompt, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "admitted-user", Kind: "user"}, ref)
	if err != nil || order < 0 {
		t.Fatalf("send admission = %d, %v", order, err)
	}
	select {
	case <-runner.inputs:
	case <-time.After(5 * time.Second):
		t.Fatal("admitted turn did not start")
	}
	app.CancelByTag(tag)
	waitForTagIdle(t, app, tag)
	row, err := app.store.LoadStreamItem(context.Background(), "admitted-user")
	if err != nil || row == nil || row.Content != prompt || row.SortOrder != order {
		t.Fatalf("Stop removed admitted user history: %+v, %v", row, err)
	}
}
