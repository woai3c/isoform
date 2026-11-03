package main

import (
	"strconv"
	"strings"
	"time"

	"wails-gui/internal/engagement"
)

func mainChatProactiveEvents(params map[string]string) (*mainChatProactiveEvent, []mainChatProactiveEvent) {
	event := mainChatProactiveEventFromParams(params, "event_")
	if params["event_type"] != "overlap" {
		if event == nil {
			return nil, nil
		}
		return event, nil
	}
	events := make([]mainChatProactiveEvent, 0, 2)
	for _, prefix := range []string{"event_first_", "event_second_"} {
		if next := mainChatProactiveEventFromParams(params, prefix); next != nil {
			events = append(events, *next)
		}
	}
	return nil, events
}

func mainChatProactiveEventFromParams(params map[string]string, prefix string) *mainChatProactiveEvent {
	title, start, end := engagementTitle(params[prefix+"title"]), params[prefix+"start"], params[prefix+"end"]
	if title == "" && start == "" && end == "" {
		return nil
	}
	minutes, _ := strconv.Atoi(params[prefix+"minutes"])
	private := params[prefix+"private"] == "true" || title == "" || proactiveVoiceRestrictedText(title)
	if private {
		title = ""
	}
	return &mainChatProactiveEvent{
		Title:          title,
		Start:          start,
		End:            end,
		Place:          engagementParamWhenNotPrivate(params, prefix+"place", private),
		People:         engagementParamWhenNotPrivate(params, prefix+"people", private),
		ConferenceHost: engagementParamWhenNotPrivate(params, prefix+"conference", private),
		Private:        private,
		Minutes:        minutes,
	}
}

func engagementParamWhenNotPrivate(params map[string]string, key string, private bool) string {
	if private {
		return ""
	}
	return params[key]
}

func populateMainChatEventCopy(item *mainChatProactive, params map[string]string) {
	switch item.Kind {
	case "meeting_prep":
		event := mainChatProactiveEventFromParams(params, "event_")
		if event == nil || event.Private || event.Title == "" {
			return
		}
		for lang, copy := range item.Localized {
			copy.Headline, copy.Body = mainChatMeetingSentence(*event, lang), ""
			item.Localized[lang] = copy
		}
	case "calendar_conflict":
		if params["event_type"] != "overlap" || len(item.Events) != 2 {
			return
		}
		first, second := item.Events[0], item.Events[1]
		for lang, copy := range item.Localized {
			clock := mainChatEventClock(params["event_overlap_at"], lang)
			switch {
			case first.Private && second.Private:
				if lang == "zh-CN" {
					copy.Headline = clock + " 有两个会议冲突。"
					copy.Body = "两个私人日程。"
				} else {
					copy.Headline = "Two meetings overlap at " + clock + "."
					copy.Body = "Two private events."
				}
			case first.Private:
				if lang == "zh-CN" {
					copy.Headline = clock + " 有两个会议冲突。"
					copy.Body = "“" + second.Title + "”和一个私人日程。"
				} else {
					copy.Headline = "Two meetings overlap at " + clock + "."
					copy.Body = second.Title + " and a private event."
				}
			case second.Private:
				if lang == "zh-CN" {
					copy.Headline = clock + " 有两个会议冲突。"
					copy.Body = "“" + first.Title + "”和一个私人日程。"
				} else {
					copy.Headline = "Two meetings overlap at " + clock + "."
					copy.Body = first.Title + " and a private event."
				}
			case lang == "zh-CN":
				copy.Headline = clock + " 有两个会议冲突。"
				copy.Body = "“" + first.Title + "”和“" + second.Title + "”。"
			default:
				copy.Headline = "Two meetings overlap at " + clock + "."
				copy.Body = first.Title + " and " + second.Title + "."
			}
			item.Localized[lang] = copy
		}
	case "decision_due":
		if params["next_step"] == "" {
			return
		}
		// The writer's two sentences: what the person was doing, then the
		// next step. Where and when they left off is the card's source line.
		title := mainChatSentenceFragment(params["title"])
		nextStep := mainChatSentenceFragment(params["next_step"])
		for lang, copy := range item.Localized {
			if lang == "zh-CN" {
				copy.Headline = title + "。"
				copy.Body = "接下来：" + nextStep + "。"
			} else {
				copy.Headline = title + "."
				copy.Body = "Next: " + nextStep + "."
			}
			item.Localized[lang] = copy
		}
	}
}

// mainChatMeetingSentence names the meeting and its time (chat-review
// v1.0.2 item 1): "{title} with {name} starts at {time}."
func mainChatMeetingSentence(event mainChatProactiveEvent, lang string) string {
	clock := mainChatEventClock(event.Start, lang)
	if lang == "zh-CN" {
		if event.People != "" {
			return "和 " + event.People + " 的" + event.Title + clock + "开始。"
		}
		return event.Title + clock + "开始。"
	}
	if event.People != "" {
		return event.Title + " with " + event.People + " starts at " + clock + "."
	}
	return event.Title + " starts at " + clock + "."
}

func mainChatSentenceFragment(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), ".!?。")
}

func mainChatEventClock(value string, lang string) string {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	return computerClock(at.In(time.Local), lang)
}

func mainChatProactiveNeedsSentence(item mainChatProactive) bool {
	switch item.Kind {
	case "calendar_conflict", "commitment_slipping", "waiting_on_reply", "person_reappeared", "reminder":
		return true
	case "decision_due":
		return item.Target.Kind == mainChatTargetThread
	default:
		return false
	}
}

func mainChatProactiveHasSentence(item mainChatProactive) bool {
	for lang, copy := range item.Localized {
		if strings.TrimSpace(copy.Body) != "" && strings.TrimSpace(copy.Body) != strings.TrimSpace(copy.Headline) {
			return true
		}
		catalog, _ := engagement.Copy(engagement.Kind(item.Kind), lang)
		if strings.TrimSpace(copy.Headline) != "" && strings.TrimSpace(copy.Headline) != strings.TrimSpace(catalog.PushBody) {
			return true
		}
	}
	if strings.TrimSpace(item.Body) != "" && strings.TrimSpace(item.Body) != strings.TrimSpace(item.Headline) {
		return true
	}
	catalog, _ := engagement.Copy(engagement.Kind(item.Kind), "en")
	return strings.TrimSpace(item.Headline) != "" && strings.TrimSpace(item.Headline) != strings.TrimSpace(catalog.PushBody)
}
