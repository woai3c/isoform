package main

import "testing"

func TestMobileBriefHistoryParagraphIsDeterministic(t *testing.T) {
	claudeView := AssistantHistoryView{Enabled: true, Projects: []HistoryProject{{
		Name: "Halcyon", Assistants: []string{"claude"}, SessionsThisWeek: 41,
	}}}
	got, ok := mobileBriefHistoryParagraph(claudeView, "en")
	if !ok || got.Text != "In Claude Code, Halcyon had your week, 41 sessions." {
		t.Fatalf("claude paragraph: %+v %v", got, ok)
	}

	withProblem := AssistantHistoryView{Enabled: true, Projects: []HistoryProject{{
		Name: "Halcyon", Assistants: []string{"claude", "codex"}, SessionsThisWeek: 41, OpenProblem: "the onboarding tests failing",
	}}}
	got, _ = mobileBriefHistoryParagraph(withProblem, "en")
	if got.Text != "In Claude Code, Halcyon had your week, 41 sessions, and the onboarding tests failing is still open." {
		t.Fatalf("open problem paragraph: %q", got.Text)
	}

	codexOnly := AssistantHistoryView{Enabled: true, Projects: []HistoryProject{{
		Name: "tool", Assistants: []string{"codex"}, SessionsThisWeek: 1,
	}}}
	got, _ = mobileBriefHistoryParagraph(codexOnly, "en")
	if got.Text != "In Codex, tool had your week, 1 session." {
		t.Fatalf("codex paragraph: %q", got.Text)
	}

	zh, _ := mobileBriefHistoryParagraph(withProblem, "zh-CN")
	if zh.Text != "在 Claude Code 中，Halcyon 这一周有 41 次会话，the onboarding tests failing还没有解决。" {
		t.Fatalf("zh paragraph: %q", zh.Text)
	}
}

func TestMobileBriefHistoryParagraphIsLeftOutWhenOffOrEmpty(t *testing.T) {
	if _, ok := mobileBriefHistoryParagraph(AssistantHistoryView{Enabled: false}, "en"); ok {
		t.Fatal("history off still wrote a paragraph")
	}
	if _, ok := mobileBriefHistoryParagraph(AssistantHistoryView{Enabled: true, Projects: []HistoryProject{}}, "en"); ok {
		t.Fatal("empty history wrote a paragraph")
	}
	// Projects with no session this week give no sentence about the week.
	if _, ok := mobileBriefHistoryParagraph(AssistantHistoryView{Enabled: true, Projects: []HistoryProject{{Name: "old", Sessions14d: 3}}}, "en"); ok {
		t.Fatal("a project with no session this week wrote a paragraph")
	}
}
