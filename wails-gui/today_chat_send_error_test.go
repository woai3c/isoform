package main

import (
	"testing"

	"yansu-agent/agent"
)

func TestTodayChatSendDisappearedSourceReturnsStableErrorCode(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			app, tag, ref := todaySendTestApp(t)
			runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			app.runner = runner
			prepared, err := app.PrepareTodayChatTurn(tag, "Help me with this item.", ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := app.DeleteMemoryEntry(ref.SourceID); err != nil {
				t.Fatal(err)
			}
			_, err = app.SendPromptWithTodayContext(prepared, t.TempDir(), "m", "", tag, nil, nil, false, false, language, "", StreamItemData{ID: "today-user", Kind: "user"}, ref)
			if err == nil || err.Error() != "TODAY_CHAT_CONTEXT_UNAVAILABLE" {
				t.Fatalf("second lookup error = %v, want stable localization code", err)
			}
			select {
			case <-runner.inputs:
				t.Fatal("unavailable source started a model turn")
			default:
			}
		})
	}
}
