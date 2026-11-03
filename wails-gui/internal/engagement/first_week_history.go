package engagement

import "strings"

// FirstWeekText is the copy for a first-week moment. The day-0 welcome becomes
// the history welcome when the first brief was made from the person's assistant
// history (history is "claude", "codex" or "both"); every other case keeps
// FirstWeekCopy.
func FirstWeekText(stage, firstBrief, history, name, lang string) string {
	if stage == "welcome" && history != "" {
		return FirstWeekHistoryWelcome(history, name, lang)
	}
	return FirstWeekCopy(stage, firstBrief, lang)
}

// FirstWeekHistoryWelcome is the day-0 welcome with the history line:
// "Hi {name}. Here's your first brief, made from today's calendar and your
// Claude Code history." Without a name it reads "Hi there." in English and
// starts at the sentence in Chinese.
func FirstWeekHistoryWelcome(history, name, lang string) string {
	name = strings.TrimSpace(name)
	if normalizeLang(lang) == "zh-CN" {
		label := map[string]string{
			"claude": "Claude Code 记录",
			"codex":  "Codex 记录",
			"both":   "Claude Code 和 Codex 记录",
		}[history]
		if label == "" {
			return FirstWeekCopy("welcome", "", lang)
		}
		greeting := "你好"
		if name != "" {
			greeting += " " + name
		}
		return greeting + "。这是根据今天的日历和你的 " + label + "生成的第一份简报。"
	}
	label := map[string]string{
		"claude": "Claude Code history",
		"codex":  "Codex history",
		"both":   "Claude Code and Codex history",
	}[history]
	if label == "" {
		return FirstWeekCopy("welcome", "", lang)
	}
	greeting := "Hi there."
	if name != "" {
		greeting = "Hi " + name + "."
	}
	return greeting + " Here's your first brief, made from today's calendar and your " + label + "."
}
