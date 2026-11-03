package main

import (
	"context"
	"strings"
	"testing"
	"time"
	"yansu-agent/agent"
)

func TestTodayChatRedactsGenericCodesAtOutputBoundaries(t *testing.T) {
	for _, secret := range []string{"482913", "4 8 2 9 1 3", "验证码：482913", "PIN 4-8-2-9-1-3"} {
		t.Run(secret, func(t *testing.T) {
			packed, _ := packTodayChatSection([]todayChatRecord{{Kind: secret, ID: secret, Title: secret, Text: secret}}, 4000)
			if strings.ContainsAny(packed, "489") {
				t.Fatalf("packed context retained code: %s", packed)
			}
			a, tag, ref := todaySendTestApp(t)
			prompt, err := a.PrepareTodayChatTurn(tag, "Help with "+secret, ref)
			if err != nil {
				t.Fatal(err)
			}
			if strings.ContainsAny(prompt, "489") {
				t.Fatalf("prepared prompt retained code: %s", prompt)
			}
			runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			a.runner = runner
			if _, err := a.SendPromptWithTodayContext("Help with "+secret, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "codes", Kind: "user"}, ref); err != nil {
				t.Fatal(err)
			}
			select {
			case input := <-runner.inputs:
				modelPrompt, history := input.PromptFunc()
				if strings.Contains(modelPrompt+history, "482913") || strings.Contains(modelPrompt+history, "4 8 2 9 1 3") || strings.Contains(modelPrompt+history, "4-8-2-9-1-3") {
					t.Errorf("model prompt retained code: %s", modelPrompt)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("model did not receive prompt")
			}
			waitForTagIdle(t, a, tag)
			page, err := a.store.LoadStreamItemsLatest(context.Background(), tag, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range page.Items {
				if row.Kind == "user" && strings.ContainsAny(row.Content, "489") {
					t.Errorf("history retained code: %s", row.Content)
				}
			}
		})
	}
}
