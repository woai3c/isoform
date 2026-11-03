package main

import (
	"fmt"
	"strings"
)

// mobile_brief_history.go writes the one history line a first brief carries.
// It is deterministic: no model call, built from the history view alone, and
// absent when history is off or empty.

// mobileBriefHistoryParagraph is the line for the project with the most work
// this week. It names the assistant (Codex only when Claude Code did not run
// the project), the count, and an open problem when pass 2 found one.
func mobileBriefHistoryParagraph(view AssistantHistoryView, language string) (MobileBriefPara, bool) {
	if !view.Enabled {
		return MobileBriefPara{}, false
	}
	var project *HistoryProject
	for i := range view.Projects {
		if view.Projects[i].SessionsThisWeek > 0 {
			project = &view.Projects[i]
			break
		}
	}
	if project == nil {
		return MobileBriefPara{}, false
	}
	sessions := project.SessionsThisWeek
	if normalizeUILanguage(language) == "zh-CN" {
		label := "Claude Code"
		if !containsString(project.Assistants, "claude") && containsString(project.Assistants, "codex") {
			label = "Codex"
		}
		text := fmt.Sprintf("在 %s 中，%s 这一周有 %d 次会话", label, project.Name, sessions)
		if project.OpenProblem != "" {
			text += "，" + project.OpenProblem + "还没有解决"
		}
		return MobileBriefPara{Text: text + "。", Refs: []string{}}, true
	}
	label := "Claude Code"
	if !containsString(project.Assistants, "claude") && containsString(project.Assistants, "codex") {
		label = "Codex"
	}
	noun := "sessions"
	if sessions == 1 {
		noun = "session"
	}
	text := fmt.Sprintf("In %s, %s had your week, %d %s", label, project.Name, sessions, noun)
	if open := strings.TrimSpace(project.OpenProblem); open != "" {
		text += ", and " + open + " is still open"
	}
	return MobileBriefPara{Text: text + ".", Refs: []string{}}, true
}
