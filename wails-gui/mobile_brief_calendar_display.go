package main

import (
	"context"
	"regexp"
	"strings"
)

var briefAllDayTime = regexp.MustCompile(`(?i)\s+at\s+(?:\d{1,2}(?::\d{2})?\s*(?:a\.?m|p\.?m)|\d{1,2}:\d{2}|midnight|noon)|(?:凌晨|上午|中午|下午|晚上)\s*\d{1,2}(?::\d{2}|点(?:\d{1,2}分?)?)|\s+\d{1,2}:\d{2}`)

// mobileBriefSayMeetingName puts a cleaned title where its raw title was, as
// people say it: "The 1:1 with Jenny is at 9:00 AM", "prepare for the 1:1
// with Jenny" (today-review v1.1.6 R12, v1.1.10 G). A title already led by
// an article or possessive keeps it.
func mobileBriefSayMeetingName(text, raw, friendly string) string {
	if !strings.HasPrefix(friendly, "1:1 ") {
		return strings.ReplaceAll(text, raw, friendly)
	}
	var out strings.Builder
	for {
		before, after, found := strings.Cut(text, raw)
		out.WriteString(before)
		if !found {
			return out.String()
		}
		lead := strings.TrimRight(before, " ")
		words := strings.Fields(lead)
		last := ""
		if len(words) > 0 {
			last = strings.ToLower(strings.Trim(words[len(words)-1], "\"“("))
		}
		switch {
		case lead == "" || strings.HasSuffix(lead, ".") || strings.HasSuffix(lead, "!") || strings.HasSuffix(lead, "?") || strings.HasSuffix(lead, "\n"):
			out.WriteString("The " + friendly)
		case last == "the" || last == "your" || last == "my" || last == "a" || last == "this" || last == "today's" || last == "tomorrow's" || strings.HasSuffix(last, "'s"):
			out.WriteString(friendly)
		case before != "" && !strings.HasSuffix(before, " "):
			out.WriteString(friendly)
		default:
			out.WriteString("the " + friendly)
		}
		text = after
	}
}

// mobileBriefCalendarDisplay reopens the exact readable occurrence before
// replacing its raw title. It never supplies a person absent from that event.
// The source label remains intact, and stale audio and prepared actions leave.
func (a *App) mobileBriefCalendarDisplay(ctx context.Context, brief MobileBriefV1) MobileBriefV1 {
	if a == nil || a.store == nil {
		return brief
	}
	type names struct {
		raw, friendly string
		allDay        bool
	}
	byRef := map[string]names{}
	for _, ref := range brief.Evidence {
		if ref.SourceKind != mobileBriefCalendarSource {
			continue
		}
		row, found, err := a.store.GetCalendarEvent(ctx, ref.SourceID)
		if err != nil || !found || row.DeletedAt != 0 {
			continue
		}
		event := calendarEventFromRow(row)
		friendly := mobileBriefCalendarName(event)
		if (friendly == "" || friendly == event.Title) && !event.AllDay {
			continue
		}
		byRef[ref.Ref] = names{event.Title, friendly, event.AllDay}
	}
	if len(byRef) == 0 {
		return brief
	}
	changed := false
	// Titles stay bare ("1:1 with Jenny"); sentences say it as people do.
	rename := func(text string, refs []string, sentence bool) string {
		for _, ref := range refs {
			if name, ok := byRef[ref]; ok && name.raw != "" && name.friendly != "" && name.friendly != name.raw && strings.Contains(text, name.raw) {
				if sentence {
					text = mobileBriefSayMeetingName(text, name.raw, name.friendly)
				} else {
					text = strings.ReplaceAll(text, name.raw, name.friendly)
				}
				changed = true
			}
		}
		return text
	}
	replace := func(text string, refs []string) string { return rename(text, refs, false) }
	say := func(text string, refs []string) string { return rename(text, refs, true) }
	for _, items := range []*[]MobileBriefItem{
		&brief.Sections.Noticed, &brief.Sections.Decide, &brief.Sections.Review,
		&brief.Sections.Plan, &brief.Sections.WaitingOnYou, &brief.Sections.Commitments,
		&brief.Sections.WorkingToward, &brief.Sections.DayAhead, &brief.Sections.Highlights,
		&brief.Sections.Moved, &brief.Sections.Doing, &brief.Sections.Watching,
		&brief.Sections.Meetings, &brief.Sections.People, &brief.Sections.OpenLoops,
		&brief.Sections.Learned, &brief.Sections.Deliverables, &brief.Sections.Schedules, &brief.Upcoming,
	} {
		if *items == nil {
			continue
		}
		*items = append((*items)[:0:0], (*items)...)
		for i := range *items {
			item := &(*items)[i]
			item.Title = replace(item.Title, item.Refs)
			item.Detail = replace(item.Detail, item.Refs)
		}
	}
	brief.Narrative = append(brief.Narrative[:0:0], brief.Narrative...)
	for i := range brief.Narrative {
		para := &brief.Narrative[i]
		para.Text = say(para.Text, para.Refs)
		if len(para.Refs) == 1 && byRef[para.Refs[0]].allDay {
			withoutTime := briefAllDayTime.ReplaceAllString(para.Text, "")
			changed = changed || withoutTime != para.Text
			para.Text = withoutTime
		}
	}
	if changed {
		// The model's card words name the old title and leave. Offers keep
		// their subject, renamed, so the closing question keeps its chips.
		brief.Audio, brief.Cards = mobileBriefWithoutStaleAudio(brief.Audio), nil
		brief.Offers = append([]MobileBriefOffer(nil), brief.Offers...)
		for i := range brief.Offers {
			offer := &brief.Offers[i]
			offer.Label, offer.Prompt, offer.Text = replace(offer.Label, offer.Refs), say(offer.Prompt, offer.Refs), say(offer.Text, offer.Refs)
		}
	}
	return brief
}
