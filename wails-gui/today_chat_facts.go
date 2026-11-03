package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/pii"
)

// todayChatFact is one structured fact Isoform keeps about the user's own
// Today item: its kind, state, due date, when it was noted. Values come from
// typed fields of Isoform's own records, never from captured text, so they
// are not run through the code redaction that turned a due date into
// "[REDACTED:code]". Time facts are rendered with their age when packed.
type todayChatFact struct {
	Name  string
	Value string
	// At is Unix milliseconds. A non-zero At renders as a local date or time
	// with its age relative to the moment the context is built.
	At int64
	// DateOnly marks a calendar day without a time of day, such as a due date.
	DateOnly bool
	// Settled marks a due date of an item that is no longer open (resolved,
	// dismissed, superseded): it is never called overdue.
	Settled bool
}

type todayChatRenderedFact struct {
	Name  string
	Value string
}

// todayChatRenderedFacts marshals as one JSON object that keeps fact order.
type todayChatRenderedFacts []todayChatRenderedFact

func (facts todayChatRenderedFacts) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, fact := range facts {
		if i > 0 {
			out.WriteByte(',')
		}
		name, err := json.Marshal(fact.Name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(fact.Value)
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func (facts *todayChatRenderedFacts) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var out todayChatRenderedFacts
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, _ := token.(string)
		var value string
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		out = append(out, todayChatRenderedFact{Name: name, Value: value})
	}
	*facts = out
	return nil
}

func renderTodayChatFacts(facts []todayChatFact, now time.Time) todayChatRenderedFacts {
	var out todayChatRenderedFacts
	for _, fact := range facts {
		value := fact.Value
		if fact.At > 0 {
			at := time.UnixMilli(fact.At)
			value = todayChatWhen(at, fact.DateOnly, now)
			if fact.Name == "due" && !fact.Settled && todayChatPastDue(at, fact.DateOnly, now) {
				value = strings.TrimSuffix(value, ")") + ", overdue)"
			}
		}
		if strings.TrimSpace(value) != "" {
			out = append(out, todayChatRenderedFact{Name: fact.Name, Value: value})
		}
	}
	return out
}

func todayChatPastDue(due time.Time, dateOnly bool, now time.Time) bool {
	if dateOnly {
		return todayChatDayOffset(due, now) < 0
	}
	return due.Before(now)
}

// todayChatWhen states a local date or time with its age, for example
// "2026-10-20 Tue (tomorrow)" or "2026-10-16 Fri 14:05 (3 days ago)".
func todayChatWhen(at time.Time, dateOnly bool, now time.Time) string {
	at = at.In(now.Location())
	layout := "2006-01-02 Mon 15:04"
	if dateOnly {
		layout = "2006-01-02 Mon"
	}
	return at.Format(layout) + " (" + todayChatAge(at, dateOnly, now) + ")"
}

func todayChatDayOffset(at time.Time, now time.Time) int {
	loc := now.Location()
	day := func(t time.Time) time.Time {
		y, m, d := t.In(loc).Date()
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	}
	return int(math.Round(day(at).Sub(day(now)).Hours() / 24))
}

func todayChatAge(at time.Time, dateOnly bool, now time.Time) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	days := todayChatDayOffset(at, now)
	switch {
	case days == 0 && dateOnly:
		return "today"
	case days == 0:
		diff := now.Sub(at)
		ahead := diff < 0
		if ahead {
			diff = -diff
		}
		var span string
		switch {
		case diff < time.Minute:
			return "now"
		case diff < time.Hour:
			span = plural(int(diff/time.Minute), "minute")
		default:
			span = plural(int(diff/time.Hour), "hour")
		}
		if ahead {
			return "today, in " + span
		}
		return "today, " + span + " ago"
	case days == -1:
		return "yesterday"
	case days == 1:
		return "tomorrow"
	case days < 0:
		return plural(-days, "day") + " ago"
	default:
		return "in " + plural(days, "day")
	}
}

// Calendar dates are not one-time codes. Isoform's own titles and the user's
// visible request keep them readable; every other digit run, a bare year
// included ("Invoice 2045", "PIN is 2024"), still goes through
// pii.RedactCodes, and RedactText applies to the whole text.
var (
	// Whole numeric dates, kept as they are: 2026-10-20 (with an optional
	// time), 2026/10/20 and 10/20/2026.
	todayChatNumericDate = regexp.MustCompile(`\b(?:` +
		`(?:19|20)\d{2}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])(?:[T ](?:[01]\d|2[0-3]):[0-5]\d(?::[0-5]\d(?:\.\d+)?)?(?:Z|[+-](?:[01]\d|2[0-3]):?[0-5]\d)?)?` +
		`|(?:19|20)\d{2}/(?:0?[1-9]|1[0-2])/(?:0?[1-9]|[12]\d|3[01])` +
		`|(?:0?[1-9]|1[0-2])/(?:0?[1-9]|[12]\d|3[01])/(?:19|20)\d{2}` +
		`)\b`)
	// A year that a month, a quarter or 年 marks as one: "October 20, 2026",
	// "20 Oct 2026", "Q4 2026", "2026年". Group 1 is the year.
	todayChatMarkedYear = regexp.MustCompile(`(?i)(?:\b(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?(?:\s+\d{1,2}(?:st|nd|rd|th)?)?,?\s+|\bQ[1-4]\s+)((?:19|20)\d{2})\b|((?:19|20)\d{2})年`)
)

func todayChatRedactKeepingDates(text string) string {
	text, _ = pii.RedactText(text)
	var spans [][2]int
	for _, loc := range todayChatNumericDate.FindAllStringIndex(text, -1) {
		spans = append(spans, [2]int{loc[0], loc[1]})
	}
	for _, loc := range todayChatMarkedYear.FindAllStringSubmatchIndex(text, -1) {
		for group := 1; group <= 2; group++ {
			if start, end := loc[2*group], loc[2*group+1]; start >= 0 {
				spans = append(spans, [2]int{start, end})
			}
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	var kept [][2]int
	last := 0
	for _, span := range spans {
		if span[0] < last || !todayChatDateStandsAlone(text, span[0], span[1]) {
			continue
		}
		kept = append(kept, span)
		last = span[1]
	}
	// A code keyword on the far side of a kept date still marks the code.
	return pii.RedactCodesKeeping(text, kept)
}

// Digit groups joined to a date make it part of a longer number, such as the
// last groups of a phone number. "Q4 2026" is not one: Q4 is a word.
var (
	todayChatNumberBefore = regexp.MustCompile(`(?:^|[^0-9A-Za-z])[0-9]+[ .,\-/()]$`)
	todayChatNumberAfter  = regexp.MustCompile(`^[ .,\-/()][0-9]+(?:$|[^0-9A-Za-z])`)
)

func todayChatDateStandsAlone(text string, start, end int) bool {
	before, after := text[:start], text[end:]
	return !todayChatNumberBefore.MatchString(before) && !todayChatNumberAfter.MatchString(after)
}

// todayChatOwnedText is the redaction for Isoform's own titles and free-text
// facts about the user's item: other people's details such as emails and
// phone numbers stay redacted, codes stay redacted, dates and years remain.
func todayChatOwnedText(text string) string {
	return todayChatRedactKeepingDates(text)
}

// todayChatPurposeFacts reads the already privacy-filtered Purpose: a field
// cleared because its source became private stays absent here as well.
func todayChatPurposeFacts(purpose PurposeOpenLoopDetail, now time.Time) []todayChatFact {
	current := purpose.Current
	var facts []todayChatFact
	add := func(fact todayChatFact) {
		if strings.TrimSpace(fact.Value) != "" || fact.At > 0 {
			facts = append(facts, fact)
		}
	}
	add(todayChatFact{Name: "type", Value: purpose.OpenLoop.Kind})
	add(todayChatFact{Name: "state", Value: current.LifecycleState})
	switch current.DueKind {
	case PurposeDueDate:
		if due, ok := parseEngagementDue(current.DueValue, now.Location()); ok {
			_, notTimestamp := time.Parse(time.RFC3339, strings.TrimSpace(current.DueValue))
			add(todayChatFact{Name: "due", At: due.UnixMilli(), DateOnly: notTimestamp != nil, Settled: todayChatPurposeSettled(current.LifecycleState)})
		}
	case PurposeDueWindow, PurposeDueCondition:
		add(todayChatFact{Name: "due", Value: todayChatOwnedText(current.DueValue)})
	case PurposeDueNone:
		add(todayChatFact{Name: "due", Value: "no due date"})
	}
	add(todayChatFact{Name: "noted", At: purpose.OpenLoop.CreatedAt})
	if current.OccurredAt > purpose.OpenLoop.CreatedAt {
		add(todayChatFact{Name: "lastChange", At: current.OccurredAt})
	}
	if since := todayChatPurposeWaitingSince(purpose); since > 0 {
		add(todayChatFact{Name: "waitingSince", At: since})
	}
	if last := todayChatPurposeLastActivity(purpose); last > 0 {
		add(todayChatFact{Name: "lastLinkedActivity", At: last})
	} else {
		add(todayChatFact{Name: "lastLinkedActivity", Value: "none linked"})
	}
	return facts
}

func todayChatPurposeSettled(state string) bool {
	switch state {
	case PurposeStateResolved, PurposeStateDismissed, PurposeStateSuperseded, PurposeStateContradicted:
		return true
	default:
		return false
	}
}

// Only evidence of events that passed the read-time filter counts.
func todayChatPurposeLastActivity(purpose PurposeOpenLoopDetail) int64 {
	readable := map[string]bool{purpose.Current.ID: true}
	for _, event := range purpose.Events {
		readable[event.ID] = true
	}
	var last int64
	for _, evidence := range purpose.Evidence {
		if !readable[evidence.EventID] {
			continue
		}
		switch evidence.SourceKind {
		case "activity", "activity_session", "activity_summary", "activity_run", "activity_outcome", "meeting":
			last = max(last, evidence.SourceObservedAt)
		}
	}
	return last
}

func todayChatStatusFacts(status string, updatedAt int64) []todayChatFact {
	var facts []todayChatFact
	if strings.TrimSpace(status) != "" {
		facts = append(facts, todayChatFact{Name: "status", Value: status})
	}
	if updatedAt > 0 {
		facts = append(facts, todayChatFact{Name: "updated", At: updatedAt})
	}
	return facts
}

func todayChatScheduleFacts(job CronJobState) []todayChatFact {
	facts := []todayChatFact{{Name: "enabled", Value: fmt.Sprintf("%t", job.Enabled)}}
	if job.NextRunAt > 0 {
		facts = append(facts, todayChatFact{Name: "nextRun", At: job.NextRunAt})
	}
	if job.LastRunAt > 0 {
		facts = append(facts, todayChatFact{Name: "lastRun", At: job.LastRunAt})
	}
	if job.LastRunStatus != "" {
		facts = append(facts, todayChatFact{Name: "lastStatus", Value: job.LastRunStatus})
	}
	return facts
}
