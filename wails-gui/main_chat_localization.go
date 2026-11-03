package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/pii"
)

// Localized copy is display-only, just like headline/body. It is generated
// from the Go catalog and never read by the model prompt builder.
type mainChatProactiveCopy struct {
	Headline     string            `json:"headline"`
	Body         string            `json:"body"`
	ActionLabels map[string]string `json:"actionLabels"`
	Helps        []string          `json:"helps,omitempty"`
}

var mainChatCopyParam = regexp.MustCompile(`\{([a-z_]+)\}`)

// recoverMainChatCopyParams matches only the complete known catalog template.
// Older rows have no parameters; recover from their existing display text when
// possible, otherwise use generic copy. This never reads another data source.
func recoverMainChatCopyParams(template, body string) map[string]string {
	matches := mainChatCopyParam.FindAllStringSubmatchIndex(template, -1)
	if len(matches) == 0 {
		return nil
	}
	pattern := "^"
	start := 0
	for _, match := range matches {
		pattern += regexp.QuoteMeta(template[start:match[0]]) + "(.*?)"
		start = match[1]
	}
	pattern += regexp.QuoteMeta(template[start:]) + "$"
	values := regexp.MustCompile(pattern).FindStringSubmatch(body)
	if values == nil {
		return nil
	}
	params := make(map[string]string, len(matches))
	for i, match := range matches {
		params[template[match[2]:match[3]]] = values[i+1]
	}
	return engagement.CanonicalChatParams(params)
}

// mainChatProactiveIsStatusEntry reports whether a card folds into a main-chat
// status entry instead of standing alone as a proactive card. A missed brief, a
// playbook result and finished work are always statuses; a generated capture
// outage is a status whose localized body carries the status line the delivery
// receipt hashes.
// mainChatCopyParams localizes catalog params and words a stored "15:04"
// clock in the computer's format (chat-review v1.0.2 item 4).
func mainChatCopyParams(params map[string]string, lang string) map[string]string {
	out := engagement.ChatParams(params, lang)
	if at, err := time.Parse("15:04", out["time"]); err == nil {
		out["time"] = computerClock(at, lang)
	}
	return out
}

func mainChatProactiveIsStatusEntry(item *mainChatProactive) bool {
	switch engagement.Kind(item.Kind) {
	case engagement.KindBriefMissed, engagement.KindPlaybookResult, engagement.KindWorkFinished:
		return true
	case engagement.KindCapturePaused:
		return item.VoiceVersion != ""
	default:
		return false
	}
}

func finalizeMainChatProactiveCopy(item *mainChatProactive) {
	if strings.TrimSpace(item.Headline) == "" || mainChatProactiveIsStatusEntry(item) {
		return
	}
	withoutRepeatedBody := func(headline, body string) string {
		normalize := func(value string) string {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "."))
		}
		if strings.EqualFold(normalize(body), normalize(headline)) {
			return ""
		}
		return body
	}
	item.Body = withoutRepeatedBody(item.Headline, item.Body)
	for lang, copy := range item.Localized {
		copy.Body = withoutRepeatedBody(copy.Headline, copy.Body)
		item.Localized[lang] = copy
	}
}

func populateMainChatProactiveCopy(item *mainChatProactive, params map[string]string) {
	populateMainChatProactiveCopyMode(item, params, true)
}

// populateMainChatProactiveCopyForRead hydrates a row already projected for
// delivery without re-applying the repeated-body rule. A withdrawn card is reset
// to generic copy whose body equals its headline; that body is the fallback, not
// a redundant line to drop.
func populateMainChatProactiveCopyForRead(item *mainChatProactive, params map[string]string) {
	populateMainChatProactiveCopyMode(item, params, false)
}

func populateMainChatProactiveCopyMode(item *mainChatProactive, params map[string]string, finalize bool) {
	if finalize {
		defer finalizeMainChatProactiveCopy(item)
	}
	repairCapturePausedHeadline(item)
	normalizeMainChatProactiveActions(item)
	for i := range item.Items {
		populateMainChatProactiveCopyMode(&item.Items[i], nil, finalize)
	}
	if item.Kind == "digest" {
		item.Localized = make(map[string]mainChatProactiveCopy, len(engagement.Languages))
		for _, lang := range engagement.Languages {
			item.Localized[lang] = mainChatProactiveCopy{Headline: engagement.DigestHeadline(lang, len(item.Items)), Body: engagement.DigestStatus(lang), ActionLabels: map[string]string{}}
		}
		return
	}
	kind := engagement.Kind(item.Kind)
	if _, known := engagement.Copy(kind, "en"); !known {
		return
	}
	if _, en := item.Localized["en"]; en && item.Localized["zh-CN"].Headline != "" {
		return
	}
	if params == nil && !engagementKindQuotesMail(kind) && !item.Draft {
		for _, lang := range engagement.Languages {
			set, _ := engagement.Copy(kind, lang)
			if recovered := recoverMainChatCopyParams(set.Sealed, item.Body); recovered != nil {
				params = recovered
				break
			}
		}
	}
	item.Localized = make(map[string]mainChatProactiveCopy, len(engagement.Languages))
	for _, lang := range engagement.Languages {
		set, _ := engagement.Copy(kind, lang, params)
		body := set.PushBody
		if !engagementKindQuotesMail(kind) && !item.Draft {
			if len(params) > 0 {
				body = engagement.Render(set.Sealed, mainChatCopyParams(params, lang))
			} else if item.Headline == set.PushBody {
				// Retain an older custom line in its original language. Other languages
				// get generic copy when there is no safe catalog-template match.
				body = item.Body
			}
		}
		body, _ = pii.RedactText(body)
		if strings.TrimSpace(body) == "" || !engagement.ValidBody(body) {
			body = set.PushBody
		}
		labels := make(map[string]string, len(item.ActionIDs))
		for _, id := range item.ActionIDs {
			if label := set.Actions[id]; label != "" {
				labels[id] = label
			}
		}
		item.Localized[lang] = mainChatProactiveCopy{Headline: set.PushBody, Body: body, ActionLabels: labels}
	}
}

func populateMainChatStatusCopy(entry *mainChatStatusEntry) {
	if entry.Localized == nil {
		entry.Localized = make(map[string]string, len(engagement.Languages))
	}
	if entry.Kind == string(engagement.KindCapturePaused) {
		for _, lang := range engagement.Languages {
			catalog, _ := engagement.Copy(engagement.KindCapturePaused, lang)
			if entry.Text == catalog.Actions["open_settings"] {
				entry.Text = catalog.PushBody
			}
			if entry.Localized[lang] == catalog.Actions["open_settings"] {
				entry.Localized[lang] = catalog.PushBody
			}
		}
	}
	if entry.Localized["en"] != "" && entry.Localized["zh-CN"] != "" {
		return
	}
	entry.Localized = make(map[string]string, len(engagement.Languages))
	if _, known := engagement.Copy(engagement.Kind(entry.Kind), "en"); known {
		item := mainChatProactive{Kind: entry.Kind, Body: entry.Text}
		// The actual template determines the original language, when recoverable.
		populateMainChatProactiveCopy(&item, nil)
		for lang, copy := range item.Localized {
			entry.Localized[lang] = copy.Body
		}
		return
	}
	title := ""
	for _, lang := range engagement.Languages {
		template := engagement.StatusReport(lang, entry.Kind, "{title}")
		if template != "" {
			if params := recoverMainChatCopyParams(template, entry.Text); params != nil {
				title = params["title"]
				break
			}
		}
	}
	switch title {
	case "A schedule", "一个定时任务", "Untitled work", "未命名工作":
		title = ""
	}
	for _, lang := range engagement.Languages {
		text := engagement.StatusReport(lang, entry.Kind, title)
		if entry.Kind == "digest_done" {
			text = engagement.DigestStatus(lang)
		}
		if text != "" {
			entry.Localized[lang] = text
		}
	}
}

// Hydrate old rows at the bounded presentation read, without rewriting history.
// New rows already carry both languages, so a UI language switch is immediate.
func localizedMainChatToolInput(tool, input string) string {
	var payload any
	switch tool {
	case mainChatProactiveTool:
		var item mainChatProactive
		if json.Unmarshal([]byte(input), &item) != nil || item.MomentID == "" {
			return input
		}
		populateMainChatProactiveCopyForRead(&item, nil)
		payload = item
	case mainChatStatusTool:
		var status mainChatStatus
		if json.Unmarshal([]byte(input), &status) != nil {
			return input
		}
		for i := range status.Entries {
			populateMainChatStatusCopy(&status.Entries[i])
		}
		payload = status
	default:
		return input
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return input
	}
	return string(encoded)
}

// Repair only the obsolete action-label fallback, preserving custom summaries.
func repairCapturePausedHeadline(item *mainChatProactive) {
	if item.Kind != string(engagement.KindCapturePaused) {
		return
	}
	for _, lang := range engagement.Languages {
		catalog, _ := engagement.Copy(engagement.KindCapturePaused, lang)
		if item.Headline == catalog.Actions["open_settings"] {
			item.Headline = catalog.PushBody
		}
		if item.Body == catalog.Actions["open_settings"] {
			item.Body = catalog.PushBody
		}
		copy, ok := item.Localized[lang]
		if !ok {
			continue
		}
		if copy.Headline == catalog.Actions["open_settings"] {
			copy.Headline = catalog.PushBody
		}
		if copy.Body == catalog.Actions["open_settings"] {
			copy.Body = catalog.PushBody
		}
		item.Localized[lang] = copy
	}
}
