package main

import (
	"strings"
	"time"
)

// mobileBriefWithoutStartedEventQuestion drops a brief's closing question,
// with its answers and offers, once the event it asks about has started
// (today-review v1.1.19 R41). The rest of the brief stays as written. An
// event that had started before the brief was made is the brief's own
// subject ("which is on now") and keeps its question.
func mobileBriefWithoutStartedEventQuestion(brief MobileBriefV1, now time.Time) MobileBriefV1 {
	last := len(brief.Narrative) - 1
	if last < 0 || brief.YourWeek != nil || now.IsZero() || !strings.ContainsAny(brief.Narrative[last].Text, "?？") {
		return brief
	}
	asOf := mobileBriefAsOfTime(brief)
	calendar := map[string]bool{}
	for _, ref := range brief.Evidence {
		if ref.SourceKind == mobileBriefCalendarSource {
			calendar[ref.Ref] = true
		}
	}
	started := map[string]bool{}
	for _, items := range [][]MobileBriefItem{brief.Sections.Meetings, brief.Sections.DayAhead, brief.Upcoming} {
		for _, item := range items {
			at := mobileBriefParseTime(item.At)
			if at.IsZero() || at.After(now) || !asOf.IsZero() && !at.After(asOf) {
				continue
			}
			for _, ref := range item.Refs {
				if calendar[ref] {
					started[ref] = true
				}
			}
		}
	}
	if !mobileBriefRefsAny(brief.Narrative[last].Refs, started) {
		return brief
	}
	brief.Narrative = append([]MobileBriefPara(nil), brief.Narrative[:last]...)
	var cards []MobileBriefCard
	for _, card := range brief.Cards {
		if card.AnswerTo != "" || card.Paragraph != nil && *card.Paragraph == last {
			continue
		}
		cards = append(cards, card)
	}
	brief.Cards = cards
	var offers []MobileBriefOffer
	for _, offer := range brief.Offers {
		if !mobileBriefRefsAny(offer.Refs, started) {
			offers = append(offers, offer)
		}
	}
	brief.Offers = offers
	return brief
}

func mobileBriefRefsAny(refs []string, set map[string]bool) bool {
	for _, ref := range refs {
		if set[ref] {
			return true
		}
	}
	return false
}
