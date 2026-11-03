package main

import (
	"time"
)

// Normalize display prose at assembly and on reads, including retained briefs.
// Machine timestamps, source ids, refs, URLs and schedule specs stay verbatim.
// Copy slices and pointed-to text so a read never rewrites its stored input.
func mobileBriefWithDisplayTimes(brief MobileBriefV1, language string) MobileBriefV1 {
	text := func(value string) string {
		return normalizeBriefText(value, language, time.Time{}, false)
	}
	validAnswers := make([]bool, len(brief.Cards))
	for i, card := range brief.Cards {
		validAnswers[i] = mobileBriefAnswerMatches(brief, card)
	}
	for _, items := range []*[]MobileBriefItem{
		&brief.Sections.Noticed, &brief.Sections.Decide, &brief.Sections.Review,
		&brief.Sections.Plan, &brief.Sections.WaitingOnYou, &brief.Sections.Commitments,
		&brief.Sections.WorkingToward, &brief.Sections.DayAhead, &brief.Sections.Highlights,
		&brief.Sections.Moved, &brief.Sections.Doing, &brief.Sections.Watching,
		&brief.Sections.Meetings, &brief.Sections.People, &brief.Sections.OpenLoops,
		&brief.Sections.Learned, &brief.Sections.Deliverables, &brief.Sections.Schedules, &brief.Upcoming,
	} {
		*items = append((*items)[:0:0], (*items)...)
		for i := range *items {
			item := &(*items)[i]
			item.Title, item.Detail, item.NextStep = text(item.Title), text(item.Detail), text(item.NextStep)
			item.AttentionReason = text(item.AttentionReason)
			if item.Context != nil {
				extra := *item.Context
				extra.SourceLabel, extra.Preview = text(extra.SourceLabel), text(extra.Preview)
				extra.Schedule, extra.BlockedReason = text(extra.Schedule), text(extra.BlockedReason)
				item.Context = &extra
			}
			if item.Outcome != nil {
				outcome := *item.Outcome
				outcome.Summary, outcome.NextStep = text(outcome.Summary), text(outcome.NextStep)
				outcome.Action, outcome.Question, outcome.UserActed = text(outcome.Action), text(outcome.Question), text(outcome.UserActed)
				item.Outcome = &outcome
			}
		}
	}
	for _, paragraphs := range []*[]MobileBriefPara{&brief.Narrative, &brief.WeeklyRecap} {
		*paragraphs = append((*paragraphs)[:0:0], (*paragraphs)...)
		for i := range *paragraphs {
			para := &(*paragraphs)[i]
			para.Text = text(para.Text)
			if para.chipWords != nil {
				words := *para.chipWords
				words.Label, words.Message = text(words.Label), text(words.Message)
				para.chipWords = &words
			}
		}
	}
	brief.Evidence = append(brief.Evidence[:0:0], brief.Evidence...)
	for i := range brief.Evidence {
		brief.Evidence[i].Label = text(brief.Evidence[i].Label)
	}
	brief.Cards = append(brief.Cards[:0:0], brief.Cards...)
	for i := range brief.Cards {
		card := &brief.Cards[i]
		// Keep unsafe copy intact for hygiene to withhold; shortening first
		// could cut a privacy marker and hide it from that guard.
		if !mobileBriefCardCopySafe(*card) {
			continue
		}
		card.Label, card.Message = text(card.Label), text(card.Message)
		if card.Kind == mobileBriefCardQuestion || card.Kind == mobileBriefCardItem && card.Paragraph != nil {
			if label := mobileBriefQuestionChipLabel(card.Label, mobileBriefVoiceLang(language)); label != "" {
				card.Label = label
			}
		}
		// Only a previously valid answer may follow this deterministic display
		// change. A stale or unrelated answer never acquires a new binding.
		if validAnswers[i] {
			card.AnswerTo = mobileBriefClosingDigest(brief)
			card.Facts = card.AnswerTo
		}
	}
	brief.Offers = append(brief.Offers[:0:0], brief.Offers...)
	for i := range brief.Offers {
		offer := &brief.Offers[i]
		offer.Label, offer.Prompt, offer.Text = text(offer.Label), text(offer.Prompt), text(offer.Text)
	}
	brief.MeetingAttendance = append(brief.MeetingAttendance[:0:0], brief.MeetingAttendance...)
	for i := range brief.MeetingAttendance {
		item := &brief.MeetingAttendance[i]
		item.Title, item.Reason = text(item.Title), text(item.Reason)
	}
	if brief.Generation != nil {
		generation := *brief.Generation
		generation.Message = text(generation.Message)
		brief.Generation = &generation
	}
	return brief
}
