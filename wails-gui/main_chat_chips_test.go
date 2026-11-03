package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

type answerChipRunner struct {
	upcomingCaptureRunner
	output string
	err    error
	calls  atomic.Int32
	check  func(context.Context, *agent.TaskInput)
}

func (r *answerChipRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	r.calls.Add(1)
	if r.check != nil {
		r.check(ctx, input)
	}
	if r.err != nil {
		return "", r.err
	}
	if input.BeforeDispatch != nil {
		if err := input.BeforeDispatch(ctx); err != nil {
			return "", err
		}
	}
	return "chip-run", emit(&agent.StreamEvent{Type: "assistant", Content: r.output})
}

func answerChipTestApp(t *testing.T) (*App, string, *answerChipRunner) {
	t.Helper()
	a := newTodayContextTestApp(t, true)
	main, err := a.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	a.eventBroadcaster = NewEventBroadcaster()
	r := &answerChipRunner{output: `{"chips":["Open the plan","Review the draft","Another request"]}`}
	a.runner = r
	for _, row := range []storage.StreamItemRow{
		{ID: "user", Kind: "user", Content: "Help with the plan", RunID: "run"},
		{ID: "answer", Kind: "assistant", Content: "Want me to open the plan?", RunID: "run"},
	} {
		if _, err := a.store.AppendStreamItem(t.Context(), main.LocalID, row); err != nil {
			t.Fatal(err)
		}
	}
	return a, main.LocalID, r
}

func TestAttendedMainChatRunBindingPrefersAdmissionUserID(t *testing.T) {
	for _, tc := range []struct {
		admissionID string
		chipID      string
		want        string
	}{
		{admissionID: "persisted-user", want: "persisted-user"},
		{admissionID: "persisted-user", chipID: "chip-user", want: "persisted-user"},
		{chipID: "chip-user", want: "chip-user"},
	} {
		if got := attendedMainChatRunBindingUserID(tc.admissionID, tc.chipID); got != tc.want {
			t.Errorf("binding user = %q, want %q", got, tc.want)
		}
	}
}

func TestChipGenerationTextOnlyRedactedBoundedAndEmits(t *testing.T) {
	a, tag, runner := answerChipTestApp(t)
	const secret = "sk-ant-api03-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	answer := "API key: " + secret + " " + strings.Repeat("文", 4100)
	if _, err := a.store.GrowStreamItemContent(t.Context(), tag, "answer", answer); err != nil {
		t.Fatal(err)
	}
	runner.check = func(ctx context.Context, input *agent.TaskInput) {
		if strings.Contains(input.Prompt, secret) || !strings.Contains(input.Prompt, "REDACTED") {
			t.Fatal("secret reached runner or redaction missing")
		}
		if !input.NoSystem || !input.NoTools || !input.Ephemeral || !input.SuppressTelemetry || !input.RequireNativeDispatch || input.Priority != agent.PriorityBackground || input.Model != a.lightTaskModel() || input.HistoryKey != "" || input.SessionID != "" || input.WorkDir != "" || input.PromptFunc != nil || len(input.ExtraTools) != 0 {
			t.Fatalf("unexpected execution scope: %+v", input)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Fatal("generation lacks 20-second deadline")
		}
		start := strings.LastIndex(input.Prompt, "\n{")
		var data answerChipInput
		if start < 0 || json.Unmarshal([]byte(input.Prompt[start+1:]), &data) != nil || len([]rune(data.Answer)) != 4000 || data.Message != "Help with the plan" {
			t.Fatal("input contract not preserved")
		}
	}
	client := a.eventBroadcaster.AddClient("chips")
	defer a.eventBroadcaster.RemoveClient("chips")
	a.generateAnswerChips(t.Context(), tag, "user", "run", "answer")
	got, err := a.ListMainChatAnswerChips([]string{"answer"})
	if err != nil || !reflect.DeepEqual(got["answer"], []string{"Open the plan", "Review the draft"}) {
		t.Fatalf("chips=%+v err=%v", got, err)
	}
	select {
	case payload := <-client:
		var event struct {
			Event string                `json:"event"`
			Args  []mainChatChipsNotice `json:"args"`
		}
		if json.Unmarshal(payload, &event) != nil || event.Event != mainChatChipsEvent || len(event.Args) != 1 || event.Args[0].SessionID != tag || event.Args[0].ItemID != "answer" {
			t.Fatalf("event=%s", payload)
		}
	default:
		t.Fatal("missing chips event")
	}
	a.generateAnswerChips(t.Context(), tag, "user", "run", "answer")
	if runner.calls.Load() != 1 {
		t.Fatal("retried the answer")
	}
}

func TestChipOutputStrictAndFiltered(t *testing.T) {
	for _, raw := range []string{`{`, "```json\n{\"chips\":[]}\n```", `{"chips":null}`, `{"chips":[],"extra":1}`, `{"chips":[],"chips":["Open"]}`, `{"chips":[]} {}`, `{"chips":[1]}`, `[]`} {
		if got := parseAnswerChips(raw, "message"); len(got) != 0 {
			t.Errorf("accepted malformed %q: %+v", raw, got)
		}
	}
	raw, _ := json.Marshal(struct {
		Chips []string `json:"chips"`
	}{[]string{" ", strings.Repeat("文", 61), "https://example.com", "Open www.example.com", "Open example.com/plan", "HELP WITH THE PLAN", "Open the plan", "open the plan", "Review the draft", "Third request"}})
	if got := parseAnswerChips(string(raw), "Help with the plan"); !reflect.DeepEqual(got, []string{"Open the plan", "Review the draft"}) {
		t.Fatalf("filtered=%+v", got)
	}
	if got := parseAnswerChips(`{"chips":["Review password: correct-horse-battery-staple"]}`, "message"); len(got) != 1 || strings.Contains(got[0], "correct-horse-battery-staple") {
		t.Fatalf("output not redacted: %+v", got)
	}
	if got := parseAnswerChips(`{"chips":["Sent the message","Booked the hold","Send Alice a reply","Pay the invoice","Delete the file","已发送消息","删除文件"]}`, "message"); len(got) != 0 {
		t.Fatalf("unsafe chip=%+v", got)
	}
}

func TestChipsClosedEyesAtStartAndPublication(t *testing.T) {
	for _, stage := range []string{"start", "publication"} {
		t.Run(stage, func(t *testing.T) {
			a, tag, runner := answerChipTestApp(t)
			if stage == "start" {
				a.closedEyes.enabled.Store(true)
			} else {
				runner.check = func(context.Context, *agent.TaskInput) {
					a.closedEyes.mu.Lock()
					a.closedEyes.enabled.Store(true)
					a.closedEyes.mu.Unlock()
				}
			}
			a.generateAnswerChips(t.Context(), tag, "user", "run", "answer")
			if got, err := a.ListMainChatAnswerChips([]string{"answer"}); err != nil || len(got) != 0 {
				t.Fatalf("paused read=%+v %v", got, err)
			}
			a.closedEyes.enabled.Store(false)
			got, err := a.ListMainChatAnswerChips([]string{"answer"})
			if err != nil || len(got["answer"]) != 0 || (stage == "start" && runner.calls.Load() != 0) {
				t.Fatalf("paused generation=%+v calls=%d %v", got, runner.calls.Load(), err)
			}
		})
	}
}

func TestChipEmptyFailedAndMalformedAttemptsAreNotRetried(t *testing.T) {
	for _, output := range []string{`{"chips":[]}`, `invalid`, `{"chips":["https://example.com"]}`, "failure"} {
		t.Run(output, func(t *testing.T) {
			a, tag, r := answerChipTestApp(t)
			r.output = output
			if output == "failure" {
				r.err = errors.New("provider failed")
			}
			a.generateAnswerChips(t.Context(), tag, "user", "run", "answer")
			a.generateAnswerChips(t.Context(), tag, "user", "run", "answer")
			got, err := a.ListMainChatAnswerChips([]string{"answer"})
			if err != nil || r.calls.Load() != 1 || got["answer"] == nil || len(got["answer"]) != 0 {
				t.Fatalf("attempt=%+v calls=%d %v", got, r.calls.Load(), err)
			}
		})
	}
}

func TestChipSingleFlightAndDeletionDuringCall(t *testing.T) {
	a, tag, r := answerChipTestApp(t)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.check = func(context.Context, *agent.TaskInput) { close(entered); <-release }
	go func() {
		defer close(finished)
		a.generateAnswerChips(t.Context(), tag, "user", "run", "answer")
	}()
	<-entered
	a.generateAnswerChips(t.Context(), tag, "user", "run", "answer")
	if err := a.store.DeleteStreamItemsFrom(t.Context(), tag, 1); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-finished
	got, err := a.ListMainChatAnswerChips([]string{"answer"})
	if err != nil || len(got) != 0 || r.calls.Load() != 1 {
		t.Fatalf("late result=%+v calls=%d %v", got, r.calls.Load(), err)
	}
}

func TestChipEligibilityRequiresAttendedMainUserTurn(t *testing.T) {
	a, tag, r := answerChipTestApp(t)
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	user := rows[0]
	user.RunID = ""
	if got := a.answerChipUser(t.Context(), tag, []storage.StreamItemRow{user}, promptOriginMac, "agentloop.user.send_clicked", user.Content); got != "user" {
		t.Fatalf("ordinary send excluded: %s", got)
	}
	if got := a.answerChipUser(t.Context(), tag, []storage.StreamItemRow{user}, promptOriginMac, "agentloop.user.send_clicked", "A different message"); got != "" {
		t.Fatalf("borrowed an old user bubble: %s", got)
	}
	if got := a.answerChipUser(t.Context(), tag, []storage.StreamItemRow{user}, promptOriginMac, "agentloop.user.send_clicked", "[The user attached 1 image(s) as visual input above.]\n\n"+user.Content); got != user.ID {
		t.Fatalf("attachment words not matched: %s", got)
	}
	for _, tc := range []struct {
		name, session, kind, tool, kickoff string
		origin                             promptOrigin
	}{
		{"background", tag, "user", "", "", promptOriginResume},
		{"proactive", tag, "tool_use", "main_chat_proactive", "", promptOriginMac},
		{"report", tag, "tool_use", "main_chat_report", "", promptOriginMac},
		{"status", tag, "tool_use", "main_chat_status", "", promptOriginMac},
		{"onboarding", tag, "system", "", "agentloop.user.send_clicked", promptOriginMac},
		{"scheduled", "cron", "user", "", "", promptOriginMac},
		{"non-main", "side", "user", "", "agentloop.user.send_clicked", promptOriginMac},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := user
			row.Kind, row.ToolName = tc.kind, tc.tool
			if got := a.answerChipUser(t.Context(), tc.session, []storage.StreamItemRow{row}, tc.origin, tc.kickoff, user.Content); got != "" {
				t.Fatalf("ineligible seed=%s", got)
			}
			if tc.session == tag && tc.kind != "user" {
				id := "excluded-" + tc.name
				if _, err := a.store.AppendStreamItem(t.Context(), tag, storage.StreamItemRow{ID: id, Kind: tc.kind, ToolName: tc.tool, Content: "Want to open the plan?", RunID: "run"}); err != nil {
					t.Fatal(err)
				}
				a.generateAnswerChips(t.Context(), tag, "user", "run", id)
			} else if tc.session != tag {
				main, err := a.store.LoadSession(t.Context(), tag)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.store.SaveSession(t.Context(), storage.SessionRow{LocalID: tc.session, ProjectID: main.ProjectID, Kind: storage.SessionKindSide, Hidden: tc.name == "scheduled"}); err != nil {
					t.Fatal(err)
				}
				for _, kind := range []string{"user", "assistant"} {
					if _, err := a.store.AppendStreamItem(t.Context(), tc.session, storage.StreamItemRow{ID: tc.name + kind, Kind: kind, Content: "Open the plan", RunID: "run"}); err != nil {
						t.Fatal(err)
					}
				}
				a.generateAnswerChips(t.Context(), tc.session, tc.name+"user", "run", tc.name+"assistant")
			}
		})
	}
	if r.calls.Load() != 0 {
		t.Fatal("ineligible turn called model")
	}
}

type chipChatRunner struct {
	answerChipRunner
}

func (r *chipChatRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	if input.NoTools {
		return r.answerChipRunner.ExecuteTask(ctx, input, emit)
	}
	for _, event := range []*agent.StreamEvent{
		{Type: "run_created", RunID: "chat-run"},
		{Type: "assistant", RunID: "chat-run", Content: "Want me to open "},
		{Type: "assistant", RunID: "chat-run", Content: "the plan?"},
		{Type: "done", RunID: "chat-run"},
		{Type: "close", RunID: "chat-run"},
	} {
		if err := emit(event); err != nil {
			return "chat-run", err
		}
	}
	return "chat-run", nil
}

func TestMainChatFinishedUserTurnGeneratesChips(t *testing.T) {
	for _, topic := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "topic"}[topic], func(t *testing.T) {
			a, tag := topicChatTestApp(t)
			a.eventBroadcaster = NewEventBroadcaster()
			r := &chipChatRunner{answerChipRunner: answerChipRunner{output: `{"chips":["Open the plan"]}`}}
			a.runner = r
			a.onAgentEvent(func(event AgentEvent) {
				if event.Type == "assistant" {
					_, err := a.store.AppendStreamItem(context.Background(), tag, storage.StreamItemRow{ID: event.ItemID, Kind: "assistant", RunID: event.RunID, Content: event.Content})
					if err != nil {
						t.Error(err)
					}
				}
			})
			client := a.eventBroadcaster.AddClient("chip-completion")
			defer a.eventBroadcaster.RemoveClient("chip-completion")
			if topic {
				if _, err := a.SendMainChatWithTopic("Help with the plan", "atlas"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := a.store.AppendStreamItem(t.Context(), tag, storage.StreamItemRow{ID: "ordinary-user", Kind: "user", Content: "Help with the plan"}); err != nil {
					t.Fatal(err)
				}
				if err := a.SendPrompt("Help with the plan", "", "m", "", tag, false, false, "", "", "", "en", ""); err != nil {
					t.Fatal(err)
				}
			}
			deadline := time.After(10 * time.Second)
			for {
				select {
				case payload := <-client:
					var notice struct {
						Event string                `json:"event"`
						Args  []mainChatChipsNotice `json:"args"`
					}
					if json.Unmarshal(payload, &notice) != nil || notice.Event != mainChatChipsEvent {
						continue
					}
					got, err := a.ListMainChatAnswerChips([]string{notice.Args[0].ItemID})
					if err != nil || !reflect.DeepEqual(got[notice.Args[0].ItemID], []string{"Open the plan"}) || r.calls.Load() != 1 {
						t.Fatalf("completion=%+v calls=%d %v", got, r.calls.Load(), err)
					}
					a.flushAgentSegmentWrites(true)
					return
				case <-deadline:
					t.Fatal("finished user turn did not publish chips")
				}
			}
		})
	}
}

func TestMainChatExclusionQuestionAnswerChips(t *testing.T) {
	terms := mobileBriefStatedExclusions([]MemoryEntry{{Content: "Keep PT out of my morning brief."}})
	question := "PT is due today.\n\nYou said earlier to keep PT out of your briefs. Should I leave this one out as well, even when it's due today?"
	if got := exclusionAnswerChips(question, terms, "en"); !reflect.DeepEqual(got, []string{"Keep it out", "Include it when it's due"}) {
		t.Fatalf("en chips=%v", got)
	}
	if got := exclusionAnswerChips(question, terms, "zh-CN"); !reflect.DeepEqual(got, []string{"不放进简报", "到期时放进去"}) {
		t.Fatalf("zh chips=%v", got)
	}
	for _, answer := range []string{
		"You said earlier to keep PT out of your briefs. I left it out.",
		"Should I add the build to your briefs?",
		"Should I leave PT out of the calendar?",
		"PT stays out of your briefs.\n\nShould I call Alice?",
	} {
		if got := exclusionAnswerChips(answer, terms, "en"); got != nil {
			t.Fatalf("%q chips=%v", answer, got)
		}
	}
	if got := exclusionAnswerChips(question, nil, "en"); !reflect.DeepEqual(got, []string{"Keep it out", "Include it when it's due"}) {
		t.Fatalf("question repeats the preference: chips=%v", got)
	}
}

func TestChatHistoricalQuestionChipsAndBriefExclusion(t *testing.T) {
	a, tag, runner := answerChipTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brief-preference.md"), []byte("# Brief preference\n\nKeep PT out of my morning brief.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	question := "You said earlier to keep PT out of your briefs.\n\nShould I leave this one out as well, even when it's due today?"
	for _, row := range []storage.StreamItemRow{
		{ID: "old-pt", Kind: "assistant", Content: question},
		{ID: "old-question", Kind: "assistant", Content: "Should I open the plan?"},
		{ID: "later", Kind: "user", Content: "Something else"},
	} {
		if _, err := a.store.AppendStreamItem(t.Context(), tag, row); err != nil {
			t.Fatal(err)
		}
	}
	// An old empty attempt must not hide the deterministic PT answers.
	pt, err := a.store.LoadStreamItem(t.Context(), "old-pt")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := a.store.ReserveAnswerChips(t.Context(), *pt, "en", time.Now().UnixMilli()); err != nil || !ok {
		t.Fatalf("reserve: %v %v", ok, err)
	}
	got, err := a.ListMainChatAnswerChips([]string{"old-pt", "old-question"})
	if err != nil || !reflect.DeepEqual(got["old-pt"], []string{"Keep it out", "Include it when it's due"}) {
		t.Fatalf("PT chips=%v err=%v", got, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err = a.store.ListMainChatAnswerChips(t.Context(), []string{"old-question"})
		if err == nil && len(got["old-question"]) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reflect.DeepEqual(got["old-question"], []string{"Open the plan", "Review the draft"}) || runner.calls.Load() != 1 {
		t.Fatalf("historical chips=%v calls=%d err=%v", got, runner.calls.Load(), err)
	}
	terms := a.mobileBriefExclusions()
	in := mobileBriefInput{Commitments: []mobileBriefCandidate{{Title: "PT due today"}, {Title: "Atlas"}}}
	if filtered := mobileBriefWithoutExcluded(in, terms); len(filtered.Commitments) != 1 || filtered.Commitments[0].Title != "Atlas" {
		t.Fatalf("unanswered exclusion lost: %+v", filtered.Commitments)
	}
}

func TestChatQuestionDetection(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"A question?", true}, {"**可以吗？**", true}, {"A question?\n\nMore text.", false}, {"", false},
	} {
		if got := answerEndsWithQuestion(tc.text); got != tc.want {
			t.Errorf("%q: got %v", tc.text, got)
		}
	}
}

func TestChatQuestionAtEndOfLongAnswerReachesChipModel(t *testing.T) {
	a, tag, runner := answerChipTestApp(t)
	answer := "Beginning. " + strings.Repeat("long text ", 500) + " Should I keep it out?"
	if _, err := a.store.GrowStreamItemContent(t.Context(), tag, "answer", answer); err != nil {
		t.Fatal(err)
	}
	runner.check = func(_ context.Context, input *agent.TaskInput) {
		start := strings.LastIndex(input.Prompt, "\n{")
		var data answerChipInput
		if start < 0 || json.Unmarshal([]byte(input.Prompt[start+1:]), &data) != nil || len([]rune(data.Answer)) != 4000 || !strings.HasSuffix(data.Answer, "Should I keep it out?") || !strings.HasPrefix(data.Answer, "Beginning.") {
			t.Fatalf("long answer lost question or context: %+v", data)
		}
	}
	a.generateAnswerChips(t.Context(), tag, "user", "run", "answer")
	if runner.calls.Load() != 1 {
		t.Fatal("question was not sent to the chip model")
	}
}
