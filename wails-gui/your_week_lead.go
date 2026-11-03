package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func yourWeekModelNames(week *MobileBriefWeek) []string {
	if week == nil {
		return nil
	}
	var names []string
	for _, row := range week.Time {
		if !row.Other && strings.TrimSpace(row.Name) != "" {
			names = append(names, strings.TrimSpace(row.Name))
		}
	}
	for _, row := range week.Done {
		if strings.TrimSpace(row.Title) != "" {
			names = append(names, strings.TrimSpace(row.Title))
		}
	}
	for _, row := range week.Open {
		if strings.TrimSpace(row.Title) != "" {
			names = append(names, strings.TrimSpace(row.Title))
		}
	}
	return names
}

func yourWeekNamesDigest(week *MobileBriefWeek) string {
	return mobileBriefModelDigest(yourWeekModelNames(week))
}

func yourWeekHeadline(brief MobileBriefV1, week *MobileBriefWeek, quiet bool) string {
	if !quiet && week != nil && brief.WeekLead != "" && brief.WeekLeadFacts != "" &&
		brief.WeekLeadFacts == yourWeekNamesDigest(week) && !mobileBriefUncertainLead(brief.WeekLead) {
		return brief.WeekLead
	}
	if week == nil {
		return ""
	}
	return week.Headline
}

func yourWeekModelPromptBlock(week *MobileBriefWeek, lang string) string {
	var b strings.Builder
	b.WriteString(pick(lang, "Your week:\n", "你这周的情况：\n"))
	for _, row := range week.Time {
		if !row.Other && row.Name != "" {
			fmt.Fprintf(&b, "- %s: %d %s\n", row.Name, row.Hours, yourWeekHourWord(row.Hours))
		}
	}
	for _, row := range week.Done {
		if row.Title != "" {
			b.WriteString(pick(lang, "- Done: ", "- 已完成：") + row.Title + "\n")
		}
	}
	for _, row := range week.Open {
		if row.Title != "" {
			b.WriteString(pick(lang, "- Still open: ", "- 尚未完成：") + row.Title + "\n")
		}
	}
	b.WriteString(pick(lang,
		"Optionally add top-level \"weekLead\": one sentence, at most 25 words, using at least one name above. No digits. Leave it out if no grounded sentence fits.\n\n",
		"可选地添加顶层字段 \"weekLead\"：一句话，最多60个字，必须使用上面至少一个名称，不要包含数字。无法根据事实写出时省略。\n\n"))
	return b.String()
}

func validateYourWeekModelLead(text string, week *MobileBriefWeek, lang string) (string, string) {
	text = strings.TrimSpace(text)
	if text == "" || week == nil || !yourWeekCopyAllowed(text) || mobileBriefUncertainLead(text) {
		return "", ""
	}
	for _, r := range text {
		if unicode.IsDigit(r) {
			return "", ""
		}
	}
	if lang == "zh" {
		if utf8.RuneCountInString(text) > 60 {
			return "", ""
		}
	} else if mobileBriefWordCount(text) > 25 {
		return "", ""
	}
	terminators := 0
	for _, r := range text {
		switch r {
		case '.', '?', '!', '。', '？', '！':
			terminators++
		}
	}
	if terminators != 1 {
		return "", ""
	}
	matched := false
	for _, name := range yourWeekModelNames(week) {
		if strings.Contains(strings.ToLower(text), strings.ToLower(name)) {
			matched = true
			break
		}
	}
	if !matched {
		return "", ""
	}
	return text, yourWeekNamesDigest(week)
}
