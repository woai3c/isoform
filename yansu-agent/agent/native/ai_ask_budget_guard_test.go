package native

import (
	"encoding/json"
	"testing"
)

func TestIsAIAskToolCall(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		command string
		want    bool
	}{
		{"plain ask", "Bash", `opencli grok ask "who won?" -f yaml`, true},
		{"chat verb", "Bash", `opencli gemini chat "hello" --site-session ephemeral`, true},
		{"ask with search flag", "Bash", `opencli doubao ask "世界杯门将天蝎座是谁" --search true -f yaml`, true},
		{"absolute path prelude", "Bash", `cd /tmp && opencli kimi ask "question"`, true},
		{"help probe short", "Bash", `opencli doubao ask -h`, false},
		{"help probe long", "Bash", `opencli doubao ask --help`, false},
		{"web read", "Bash", `opencli web read --url "https://example.com" --wait-until networkidle`, false},
		{"list preflight", "Bash", `opencli list -f yaml`, false},
		{"vertical search", "Bash", `opencli zhihu search "天蝎座" --limit 10`, false},
		{"ordinary bash", "Bash", `go test ./...`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := mustJSONForAIAskBudgetTest(t, map[string]any{"command": tc.command})
			if got := isAIAskToolCall(tc.tool, input); got != tc.want {
				t.Fatalf("isAIAskToolCall(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}

	t.Run("non-bash tool never counts", func(t *testing.T) {
		input := mustJSONForAIAskBudgetTest(t, map[string]any{"command": `opencli grok ask "q"`})
		if isAIAskToolCall("Read", input) {
			t.Fatal("non-Bash tool was classified as an AI ask")
		}
	})
}

func TestCheckAIAskBudgetThresholds(t *testing.T) {
	input := mustJSONForAIAskBudgetTest(t, map[string]any{
		"command": `opencli grok ask "who is the Scorpio goalkeeper?" -f yaml`,
	})

	got := CheckAIAskBudget(0, 0, "Bash", input)
	if got.Kind != "ok" || got.Count != 1 || !got.Counted {
		t.Fatalf("first ask result = %+v", got)
	}

	got = CheckAIAskBudget(AIAskNudgeThreshold-1, 0, "Bash", input)
	if got.Kind != "nudge" || got.Count != AIAskNudgeThreshold || !got.Counted {
		t.Fatalf("nudge threshold result = %+v", got)
	}

	got = CheckAIAskBudget(AIAskHardThreshold-1, 0, "Bash", input)
	if got.Kind != "hard" || got.Count != AIAskHardThreshold || !got.Counted {
		t.Fatalf("hard threshold result = %+v", got)
	}

	ordinary := mustJSONForAIAskBudgetTest(t, map[string]any{"command": "go test ./..."})
	got = CheckAIAskBudget(AIAskHardThreshold, 0, "Bash", ordinary)
	if got.Kind != "ok" || got.Count != AIAskHardThreshold || got.Counted {
		t.Fatalf("ordinary command should not count against AI ask budget: %+v", got)
	}

	// Allowance raises both thresholds: with 3 named sources, the 3rd ask
	// that would normally hard-block is plain ok.
	got = CheckAIAskBudget(AIAskHardThreshold-1, 3, "Bash", input)
	if got.Kind != "ok" || !got.Counted {
		t.Fatalf("allowance should lift hard threshold: %+v", got)
	}
	got = CheckAIAskBudget(AIAskHardThreshold-1+3, 3, "Bash", input)
	if got.Kind != "hard" {
		t.Fatalf("raised hard threshold should still trip eventually: %+v", got)
	}
}

func TestAIAskBudgetAllowance(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   int
	}{
		{"explicit two latin", `帮我把这个问题问一遍 grok 和 gemini，对比下答案`, 2},
		{"explicit three mixed", `分别问问 grok、豆包和 ChatGPT 这个问题`, 3},
		{"chinese alias dedup", `用 qwen 和 千问 查一下`, 0}, // same canonical source → 1 distinct → no allowance
		{"single mention", `用 gemini 帮我查下天气`, 0},
		{"no mention", `今天深圳天气怎么样`, 0},
		{"incidental product talk", `Gemini 模型的上下文窗口多大`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AIAskBudgetAllowance(tc.prompt); got != tc.want {
				t.Fatalf("AIAskBudgetAllowance(%q) = %d, want %d", tc.prompt, got, tc.want)
			}
		})
	}
}

func mustJSONForAIAskBudgetTest(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
