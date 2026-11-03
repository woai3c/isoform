package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/platform/browserhistory"
	"wails-gui/internal/topics"
	"wails-gui/storage"
)

// What the interest model reads, from what the Mac already summarized.
// collectInterestInput reads the sources; interestItems turns them into
// evidence and applies every exclusion the topics apply (paused stretches,
// private windows, protected apps and browsers, messaging and mail apps,
// people's names), so the privacy rules are tested from fixtures. The text
// goes to the same model that wrote the summaries; nothing here leaves the
// Mac on its own.

const (
	interestNamedMax     = 120
	interestEntityRunes  = 60
	interestActivityRefs = 10
)

// interestRun is one per-app run summary: the things it named.
type interestRun struct {
	ID       string
	AppName  string
	StartMs  int64
	Type     string
	Entities []string
}

// interestGoal is one open Purpose Map item.
type interestGoal struct {
	ID    string
	Kind  string
	Title string
	At    time.Time
	Seen  int
}

// interestInput is the plain data behind one model pass.
type interestInput struct {
	Now  time.Time
	From time.Time
	// Pauses is the "Pause observing" log. PausesUnknown means it could not
	// be read: then nothing observed counts.
	Pauses        []observationPause
	PausesUnknown bool

	Activities []ActivityRow
	Semantic   []SemanticEventRow
	Runs       []interestRun
	Visits     []BrowserVisitRow
	Goals      []interestGoal
	Memories   []MemoryEntry
	Sessions   []storage.SessionRow
	Calendar   []storage.CalendarEventRow
	// People are names the user knows; they never become interests.
	// PeopleUnknown means the people list or the calendar could not be read:
	// then no pass runs, since the names it must refuse are missing.
	People        []string
	PeopleUnknown bool
}

// collectInterestInput reads every source from from to now. Each source is
// best effort.
func collectInterestInput(a *App, from, now time.Time) interestInput {
	in := interestInput{Now: now, From: from}
	if a == nil {
		return in
	}
	pauses, err := a.observationPauses()
	if err != nil {
		in.PausesUnknown = true
	}
	in.Pauses = pauses
	if a.activity != nil && a.activity.store != nil {
		store := a.activity.store
		if rows, err := store.GetActivitiesInRange(from.UnixMilli(), now.UnixMilli()); err == nil {
			in.Activities = a.filterComposerModelCandidates(store, rows)
		}
		if rows, err := store.GetSemanticEventsInRange(from.UnixMilli(), now.UnixMilli()); err == nil {
			for _, row := range rows {
				if a.interestSemanticSourceAllowsRead(row) {
					in.Semantic = append(in.Semantic, row)
				}
			}
		}
		if rows, err := store.ListBrowserVisits(from.UnixMilli(), now.UnixMilli()); err == nil {
			in.Visits = rows
		}
		for _, run := range collectInterestRuns(store, from.UnixMilli(), now.UnixMilli()) {
			source, err := store.GetTimelineRunByID(run.ID)
			if err == nil && source != nil && a.activityTimelineRunAllowsModelRead(store, *source) {
				in.Runs = append(in.Runs, run)
			}
		}
		// Today's collector resolves every contributing Purpose field and its
		// original evidence, rather than trusting the derivative title/time.
		in.Goals = interestGoalsFrom(a.collectTodayChatSources(context.Background(), now).Purposes)
	}
	if a.store != nil {
		ctx := context.Background()
		if rows, err := a.store.ListCalendarEvents(ctx, from.UnixMilli(), now.Add(topicUpcomingAhead).UnixMilli(), false); err == nil {
			in.Calendar = rows
		} else {
			in.PeopleUnknown = true
		}
		if all, err := a.store.LoadAllSessions(ctx); err == nil {
			for _, rows := range all {
				in.Sessions = append(in.Sessions, rows...)
			}
		}
	}
	if entries, err := a.GetMemoryEntries(); err == nil {
		for _, entry := range entries {
			if a.memorySourceAllowsRead(entry.Content) {
				in.Memories = append(in.Memories, entry)
			}
		}
	}
	if people, err := a.ListPrincipalsWithStats(); err == nil {
		for _, p := range people {
			in.People = append(in.People, p.Principal.DisplayName)
			in.People = append(in.People, p.Principal.Aliases...)
		}
	} else {
		in.PeopleUnknown = true
	}
	return in
}

// collectInterestRuns reads the run summaries that started in the window.
// Runs of protected apps have no summary.
func collectInterestRuns(store *ActivityStore, fromMs, toMs int64) []interestRun {
	if store == nil || store.db == nil {
		return nil
	}
	rows, err := store.db.Query(`
		SELECT group_id, app_name, start_ms, activity_type, entities
		FROM activity_timeline_group_summaries
		WHERE start_ms >= ? AND start_ms < ?
		ORDER BY start_ms DESC`, fromMs, toMs)
	if err != nil {
		log.Printf("[interests] run summaries unavailable: %v", err)
		return nil
	}
	defer rows.Close()
	var out []interestRun
	for rows.Next() {
		var run interestRun
		var entities string
		if rows.Scan(&run.ID, &run.AppName, &run.StartMs, &run.Type, &entities) != nil {
			continue
		}
		_ = json.Unmarshal([]byte(entities), &run.Entities)
		out = append(out, run)
	}
	return out
}

// interestGoalsFrom keeps the open goals, commitments, opportunities and
// responsibilities. Concerns are mostly bugs in what the user builds.
func interestGoalsFrom(details []PurposeOpenLoopDetail) []interestGoal {
	var out []interestGoal
	for _, detail := range details {
		kind, _ := normalizePurposeKind(detail.OpenLoop.Kind)
		switch kind {
		case PurposeKindGoal, PurposeKindCommitment, PurposeKindOpportunity, PurposeKindResponsibility:
		default:
			continue
		}
		if detail.Current.Title == "" || !purposeStateOpen(detail.Current.LifecycleState) {
			continue
		}
		out = append(out, interestGoal{
			ID:    detail.OpenLoop.ID,
			Kind:  kind,
			Title: detail.Current.Title,
			At:    time.UnixMilli(detail.Current.OccurredAt),
			Seen:  len(detail.Events),
		})
	}
	return out
}

// interestRunTypes are the runs whose named things say what the user is
// into: research, browsing, writing and coding, not settings or chat.
var interestRunTypes = map[string]bool{"research": true, "browsing": true, "writing": true, "coding": true}

// interestItems turns the input into evidence, in the order the prompt
// keeps it when it runs out of room, and returns the names no interest may
// carry: the people the user knows, the people on the calendar and the
// people the summaries named.
func interestItems(in interestInput) ([]interests.Item, []string) {
	observed := func(at time.Time) bool {
		if in.PausesUnknown {
			return false
		}
		return !pausedAt(in.Pauses, at)
	}
	inWindow := func(at time.Time) bool {
		return !at.Before(in.From) && !at.After(in.Now)
	}
	names := append([]string(nil), in.People...)
	for _, ev := range in.Calendar {
		names = append(names, ev.OrganizerName, ev.OrganizerEmail)
		for _, attendee := range ev.Attendees {
			names = append(names, attendee.Name, attendee.Email)
		}
	}
	// The people every summary in the window named, also the summaries left
	// out below (a chat in a messaging app names the most people). The model
	// adds the people it sees in the rest (interests.Answer.People): a
	// handle alone ("@github") may name a product.
	for _, raw := range interestEntityLists(in) {
		var entities []Entity
		_ = json.Unmarshal([]byte(raw), &entities)
		for _, e := range entities {
			if e.Type == entityPerson && strings.TrimSpace(e.Name) != "" {
				names = append(names, strings.TrimSpace(e.Name))
			}
		}
	}
	var goals, about, chats, searches, activities, named []interests.Item

	for _, g := range in.Goals {
		text := topicText(g.Title)
		if text == "" {
			continue
		}
		if g.Seen > 1 {
			text = fmt.Sprintf("%s (%s, seen %d times)", text, g.Kind, g.Seen)
		} else {
			text = fmt.Sprintf("%s (%s)", text, g.Kind)
		}
		goals = append(goals, interests.Item{Ref: "goal:" + g.ID, Source: interests.SourceGoal, Text: text, At: g.At})
	}

	for _, m := range in.Memories {
		// Who the user is and what they are into; not the instructions for
		// the assistant (agentic_memory).
		switch m.Dimension {
		case "user_insight", "topic_highlight", "deep_user_model", "custom":
		default:
			continue
		}
		if topicAnySensitive(m.Apps) || topicAnyPeopleApp(m.Apps) {
			continue
		}
		at := m.UpdatedAt
		if at == 0 {
			at = m.ModTime
		}
		points := parseMemoryPoints(m.Content)
		if len(points) == 0 {
			points = []string{m.Title}
		}
		for i, point := range points {
			if text := topicText(point); text != "" {
				about = append(about, interests.Item{Ref: fmt.Sprintf("about:%s#%d", m.ID, i), Source: interests.SourceAbout, Text: text, At: time.UnixMilli(at)})
			}
		}
	}

	for _, s := range in.Sessions {
		if s.Hidden || s.AutomationResult || s.MeetingID != "" || s.Kind == storage.SessionKindMain {
			continue
		}
		label := strings.TrimSpace(s.Label)
		at := time.UnixMilli(s.UpdatedAt)
		if label == "" || strings.HasPrefix(label, "Transcript:") || !inWindow(at) {
			continue
		}
		if text := topicText(label); text != "" {
			chats = append(chats, interests.Item{Ref: "chat:" + s.LocalID, Source: interests.SourceChat, Text: text, At: at})
		}
	}

	byBrowser := map[string][]browserhistory.Visit{}
	for _, row := range in.Visits {
		browser, _ := browserhistory.BrowserNamed(row.Browser)
		if browserVisitSensitive(browser.BundleID, row.Browser) {
			continue
		}
		at := time.UnixMilli(row.VisitedAt)
		if !observed(at) || activitymon.IsPrivateBrowsing(row.Title) {
			continue
		}
		byBrowser[row.Browser] = append(byBrowser[row.Browser], browserhistory.Visit{URL: row.URL, Title: row.Title, At: at})
	}
	browsers := make([]string, 0, len(byBrowser))
	for b := range byBrowser {
		browsers = append(browsers, b)
	}
	sort.Strings(browsers)
	for _, b := range browsers {
		for _, thread := range browserhistory.ResearchThreads(byBrowser[b], 0) {
			if text := topicText(thread.Query); text != "" {
				searches = append(searches, interests.Item{
					Ref: "search:" + interestHash(strings.ToLower(text)), Source: interests.SourceSearch,
					Text: text, At: thread.Last, Minutes: float64(thread.Pages * 2),
				})
			}
		}
	}

	addActivity := func(ref, title, description, entitiesRaw string, apps []string, start time.Time, dwell time.Duration) {
		if topicAnySensitive(apps) || topicAnyPeopleApp(apps) || !observed(start) {
			return
		}
		var entities []Entity
		_ = json.Unmarshal([]byte(entitiesRaw), &entities)
		var things []string
		for _, e := range entities {
			name := strings.TrimSpace(e.Name)
			switch {
			case name == "", e.Type == entityPerson:
			case len(things) < interestActivityRefs:
				things = append(things, name)
			}
		}
		text := strings.TrimSpace(title)
		if d := strings.TrimSpace(description); d != "" {
			text += ". " + d
		}
		if len(things) > 0 {
			text += " Named: " + strings.Join(things, "; ")
		}
		if text = topicText(text); text == "" {
			return
		}
		activities = append(activities, interests.Item{
			Ref: ref, Source: interests.SourceActivity, Text: text, At: start,
			Minutes: math.Round(dwell.Minutes()*10) / 10,
		})
	}
	for _, row := range in.Activities {
		apps, dwell := topicAppTime(row.ActiveMsByApp)
		addActivity("activity:"+row.ID, row.Title, row.Description, row.Entities, apps, time.UnixMilli(row.StartMs), dwell)
	}
	for _, row := range in.Semantic {
		var apps []string
		_ = json.Unmarshal([]byte(row.Apps), &apps)
		dwell := time.Duration(max(row.EndMs-row.StartMs, 0)) * time.Millisecond
		addActivity("semantic:"+row.ID, row.Title, row.Description, row.Entities, apps, time.UnixMilli(row.StartMs), dwell)
	}
	sort.SliceStable(activities, func(i, j int) bool { return activities[i].At.After(activities[j].At) })

	// Things named while researching, writing, coding or browsing, by how
	// many runs named them. People, links, hosts, paths and handles are
	// left out, and so are bare app and site names.
	type thing struct {
		name  string
		count int
		last  time.Time
	}
	things := map[string]*thing{}
	for _, run := range in.Runs {
		at := time.UnixMilli(run.StartMs)
		if !interestRunTypes[run.Type] || !observed(at) {
			continue
		}
		if topicPeopleApp(run.AppName, "") || topicAnySensitive([]string{run.AppName}) {
			continue
		}
		for _, raw := range run.Entities {
			name := strings.Join(strings.Fields(topicText(raw)), " ")
			if name == "" || len([]rune(name)) > interestEntityRunes || strings.EqualFold(name, run.AppName) {
				continue
			}
			switch topics.CheckLabel(name, names) {
			case topics.LabelPerson, topics.LabelLink, topics.LabelEmpty:
				continue
			default:
			}
			key := strings.ToLower(name)
			t := things[key]
			if t == nil {
				t = &thing{name: name}
				things[key] = t
			}
			t.count++
			if at.After(t.last) {
				t.last = at
			}
		}
	}
	keys := make([]string, 0, len(things))
	for key := range things {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := things[keys[i]], things[keys[j]]
		if a.count != b.count {
			return a.count > b.count
		}
		return keys[i] < keys[j]
	})
	if len(keys) > interestNamedMax {
		keys = keys[:interestNamedMax]
	}
	for _, key := range keys {
		t := things[key]
		named = append(named, interests.Item{
			Ref: "named:" + interestHash(key), Source: interests.SourceNamed,
			Text: fmt.Sprintf("%s (in %d runs)", t.name, t.count), At: t.last,
		})
	}

	var items []interests.Item
	for _, group := range [][]interests.Item{goals, about, chats, searches, activities, named} {
		items = append(items, group...)
	}
	return items, names
}

// interestEntityLists are the typed entity lists of every summary in the
// window.
func interestEntityLists(in interestInput) []string {
	var out []string
	for _, row := range in.Activities {
		out = append(out, row.Entities)
	}
	for _, row := range in.Semantic {
		out = append(out, row.Entities)
	}
	return out
}

// interestHash keeps typed searches and named things out of the stored
// evidence: only their hash is kept.
func interestHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
