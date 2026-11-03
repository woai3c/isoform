package main

import (
	"regexp"
	"strings"
	"time"

	"wails-gui/internal/interests"
)

// topicAddActivityDay adds at's local day to days when it falls in the 14
// days up to now.
func topicAddActivityDay(days []string, at int64, now time.Time) []string {
	if at <= 0 {
		return days
	}
	when := time.UnixMilli(at).In(time.Local)
	if when.After(now.Add(time.Minute)) || now.Sub(when) > 14*24*time.Hour {
		return days
	}
	day := when.Format("2006-01-02")
	for _, existing := range days {
		if existing == day {
			return days
		}
	}
	return append(days, day)
}

// topicCardPrivateDetail is payment, bank and tax detail, which never shows
// on a topic card (today-review v1.1.4 #9), on top of the brief's sensitive
// filter (interests.Sensitive).
var topicCardPrivateDetail = regexp.MustCompile(`(?i)(?:^|[^\pL])(?:banks?|banking|tax(?:es)?|ftb|irs|web\s*pay|payments?|card\s+number|routing\s+number|ssn|social\s+security)(?:$|[^\pL])|银行|税|付款|支付`)

func topicCardPrivate(text string) bool {
	return strings.TrimSpace(text) != "" && (interests.Sensitive(text) || topicCardPrivateDetail.MatchString(text))
}

// topicCardsWithoutPrivate applies the brief's private filter to Today's
// topics and Chat's pills: a private name hides the topic, and a private
// line is left off its card.
func topicCardsWithoutPrivate(views []TopicView) []TopicView {
	out := make([]TopicView, 0, len(views))
	for _, view := range views {
		if topicCardPrivate(view.Name) {
			continue
		}
		for _, moment := range []**TopicMoment{&view.Latest, &view.LatestFind, &view.Next} {
			if *moment != nil && topicCardPrivate((*moment).Text) {
				*moment = nil
			}
		}
		out = append(out, view)
	}
	return out
}
