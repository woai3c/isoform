package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"yansu-agent/agent"
)

// portraitTestBeliefs is a person with enough to write: 24 beliefs over six
// days in four groups. The writer fixture below cites them in pairs from
// different days, so every sentence clears the bar.
var portraitTestBeliefs = []struct{ title, area string }{
	{"Outlines the whole plan before starting", "work"},
	{"Writes section headings before any text", "work"},
	{"Builds the first version from scratch within a day", "work"},
	{"Does deep work in long blocks before noon", "routines"},
	{"Says yes within about 20 minutes", "communication"},
	{"Sends replies that say no about two days after they arrive", "communication"},
	{"Books trips months ahead", "interests"},
	{"Keeps a list of trip plans", "interests"},
	{"Writes short replies, about two sentences", "communication"},
	{"Puts the answer in the first line", "communication"},
	{"Moves meetings after 2 PM", "routines"},
	{"Declines meetings before 11 AM", "routines"},
	{"Asks for the data before deciding", "work"},
	{"Opens the numbers before replying on pricing", "work"},
	{"Cooks from whatever is in the fridge", "interests"},
	{"Looks up quick recipes on weeknights", "interests"},
	{"Keeps one project open at a time", "work"},
	{"Plans the week on Sunday evening", "routines"},
	{"Rarely opens work apps after 7 PM", "routines"},
	{"Uses a calendar for every plan", "routines"},
	{"Skips greetings in short messages", "communication"},
	{"Reviews drafts once before sending", "communication"},
	{"Prototypes before writing a spec", "work"},
	{"Keeps notifications off while focusing", "routines"},
}

const portraitTestDraft = `{"sentences":[
 {"role":"connect","parts":[{"text":"You "},{"text":"plan the whole thing","ids":["belief-0","belief-1"]},{"text":", then "},{"text":"build the first version yourself","ids":["belief-2","belief-3"]},{"text":", and the rest of your week bends around that."}]},
 {"role":"insight","parts":[{"text":"You're quick on big calls and slow on awkward ones: "},{"text":"your yeses go out in about 20 minutes, your nos take about two days","ids":["belief-4","belief-5"]},{"text":"."}]},
 {"role":"life","parts":[{"text":"At home it's the same shape: "},{"text":"trips booked months ahead","ids":["belief-6","belief-7"]},{"text":"."}]},
 {"role":"action","parts":[{"text":"So Isoform "},{"text":"keeps drafts short","ids":["belief-8","belief-9"],"does":"drafts"},{"text":", and "},{"text":"keeps your mornings clear","ids":["belief-10","belief-11"],"does":"brief","optional":true},{"text":"."}]}
],"lately":null,"workWithMe":[
 {"text":"Send the number, not the opinion.","ids":["belief-12","belief-13"]},
 {"text":"Mornings are for building. Book me after 2 PM.","ids":["belief-10","belief-11"]},
 {"text":"If you need a no from me, ask for it directly.","ids":["belief-4","belief-5"]}
]}`

// portraitTestSideWords give every side of the shape five or more beliefs,
// both poles in turn, so the test person's shape is ready.
var portraitTestSideWords = []string{"Works in deep focus", "Starts from first principles", "Keeps it brief", "Plans ahead", "Juggles many threads", "Follows the checklist", "Writes detailed notes", "Goes ad hoc"}

// writePortraitTestBelief writes a belief brought from an assistant's
// history: imported at "at", but seen in two sessions on two earlier days.
func writePortraitTestBelief(t *testing.T, dir string, i int, title, area string, at time.Time, confirmed bool) {
	t.Helper()
	sessions := []string{okfTimestamp(at.Add(-24*time.Hour - time.Duration(i)*time.Minute)), okfTimestamp(at.Add(-48*time.Hour - time.Duration(i)*time.Minute))}
	fields := map[string]any{"type": "Isoform Memory", "title": title, "timestamp": okfTimestamp(at), "source": assistantHistorySource, "dimension": "preference", "area": area, "history_assistant": "claude", "history_session_at": sessions[0], "history_sessions_at": sessions}
	if confirmed {
		fields["confirmed_at"] = at.UTC().Format(time.RFC3339)
	}
	content := renderOKFDocument(fields, "## Memory Points\n\n- "+title+".\n- "+portraitTestSideWords[i%len(portraitTestSideWords)]+".")
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("belief-%d.md", i)), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// newPortraitTestRig writes the first count test beliefs, one day apart in
// turns of six, and an App whose runner answers with portraitTestDraft.
func newPortraitTestRig(t *testing.T, count int) (*App, *AboutYouPortraitService, *selfDreamFakeRunner, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(root, "data"))
	runner := &selfDreamFakeRunner{response: portraitTestDraft}
	app := &App{runner: runner}
	service := NewAboutYouPortraitService(app)
	app.aboutYouPortrait = service
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-10 * 24 * time.Hour)
	for i := 0; i < count; i++ {
		belief := portraitTestBeliefs[i%len(portraitTestBeliefs)]
		writePortraitTestBelief(t, dir, i, belief.title, belief.area, start.Add(time.Duration(i%6)*24*time.Hour), false)
	}
	path, err := portraitPath()
	if err != nil {
		t.Fatal(err)
	}
	return app, service, runner, path
}

func mustPortraitRead(t *testing.T, app *App, state string) AboutYouPortrait {
	t.Helper()
	result, err := app.GetAboutYouPortrait()
	if err != nil || result.State != state {
		t.Fatalf("portrait: %+v, %v; want %s", result, err, state)
	}
	if state != "ready" && len(result.Sentences) != 0 {
		t.Fatalf("text outside ready: %+v", result)
	}
	return result
}

func portraitParagraph(portrait AboutYouPortrait) string {
	var text strings.Builder
	for _, sentence := range portrait.Sentences {
		for _, part := range sentence.Parts {
			text.WriteString(part.Text)
		}
		text.WriteString(" ")
	}
	return strings.TrimSpace(text.String())
}

func TestPortraitPass1SidesNameAndHysteresis(t *testing.T) {
	sources := []portraitSource{
		{ID: "a", Title: "Plans the week ahead on Sunday", Area: "routines", Confirmed: true},
		{ID: "b", Title: "Builds a prototype from scratch", Area: "work", Confirmed: true},
		{ID: "c", Title: "Writes short replies", Area: "communication", Confirmed: true},
		{ID: "d", Title: "Does deep work in long blocks", Area: "routines", Confirmed: true},
	}
	sides, areas := portraitSides(sources)
	for side, value := range sides {
		if value >= 0.5 {
			t.Fatalf("side %d = %v, want the first pole", side, value)
		}
	}
	poles := portraitPoles(sides, nil)
	if first, second := portraitNameWords(poles); first != "blueprint" || second != "builder" {
		t.Fatalf("name = %s %s", first, second)
	}
	firstAreas, _ := portraitNameAreas(areas)
	if len(firstAreas) == 0 {
		t.Fatal("no groups behind the first word")
	}
	// A side flips only once it is ten points past the middle.
	if got := portraitPoles([4]float64{0.6, 0.2, 0.2, 0.2}, []int{0, 0, 0, 0}); got[0] != 0 {
		t.Fatal("flipped at 0.60")
	}
	if got := portraitPoles([4]float64{0.61, 0.2, 0.2, 0.2}, []int{0, 0, 0, 0}); got[0] != 1 {
		t.Fatal("did not flip at 0.61")
	}
	if got := portraitPoles([4]float64{0.45, 0.2, 0.2, 0.2}, []int{1, 0, 0, 0}); got[0] != 1 {
		t.Fatal("flipped back inside the margin")
	}
	// The same beliefs give the same name every time.
	again, _ := portraitSides(sources)
	if again != sides {
		t.Fatal("sides are not deterministic")
	}
}

func TestPortraitNamesAreTheFixedSixteen(t *testing.T) {
	names := map[string]bool{}
	for a := 0; a < 2; a++ {
		for b := 0; b < 2; b++ {
			for c := 0; c < 2; c++ {
				for d := 0; d < 2; d++ {
					first, second := portraitNameWords([]int{a, b, c, d})
					if _, ok := portraitNameWordText[first]; !ok {
						t.Fatalf("unknown word %s", first)
					}
					if _, ok := portraitNameWordText[second]; !ok {
						t.Fatalf("unknown word %s", second)
					}
					names[first+second] = true
				}
			}
		}
	}
	if len(names) != 16 {
		t.Fatalf("%d names", len(names))
	}
}

func TestPortraitReadiness(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	previousLocal := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = previousLocal })
	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{"current", time.Now()},
		{"DST starts", time.Date(2026, 3, 8, 23, 30, 0, 0, loc)},
		{"DST ends", time.Date(2026, 11, 1, 23, 30, 0, 0, loc)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testPortraitReadinessAt(t, tc.now)
		})
	}
}

func testPortraitReadinessAt(t *testing.T, now time.Time) {
	t.Helper()
	day := func(n int) int64 { return now.AddDate(0, 0, -n).UnixMilli() }
	var sources []portraitSource
	for i := 0; i < 14; i++ {
		sources = append(sources, portraitSource{ID: fmt.Sprint(i), Title: portraitTestSideWords[i%8], Area: []string{"work", "routines", "communication"}[i%3], FirstLearnedAt: day(i % 3)})
	}
	progress := portraitReadiness(sources)
	if progress.Met || progress.Beliefs != 14 || progress.Days != 3 || progress.MoreDays != 2 {
		t.Fatalf("progress = %+v", progress)
	}
	for i := 14; i < 21; i++ {
		sources = append(sources, portraitSource{ID: fmt.Sprint(i), Title: portraitTestSideWords[i%8], Area: "work", FirstLearnedAt: day(i % 6)})
	}
	if progress := portraitReadiness(sources); !progress.Met {
		t.Fatalf("bar not met: %+v", progress)
	}
	// Two groups only: not yet.
	for i := range sources {
		if sources[i].Area == "communication" {
			sources[i].Area = "work"
		}
	}
	if progress := portraitReadiness(sources); progress.Met || progress.Groups != 2 {
		t.Fatalf("met with two groups: %+v", progress)
	}
	// A side with fewer than five beliefs is reported, not required.
	for i := range sources {
		sources[i].Area = []string{"work", "routines", "communication"}[i%3]
		if i%8 == 2 || i%8 == 6 {
			sources[i].Title = "Keeps one list"
		}
	}
	if progress := portraitReadiness(sources); !progress.Met || progress.SidesReady {
		t.Fatalf("thin side: %+v", progress)
	}
}

// TestPortraitReadinessCountsSessionDays: beliefs imported on one day count
// the days their sessions happened.
func TestPortraitReadinessCountsSessionDays(t *testing.T) {
	importDay := time.Now().UnixMilli()
	var sources []portraitSource
	for i := 0; i < 24; i++ {
		day := portraitLocalDay(time.Now().Add(-time.Duration(1+i%6) * 24 * time.Hour).UnixMilli())
		sources = append(sources, portraitSource{ID: fmt.Sprint(i), Title: portraitTestSideWords[i%8], Area: []string{"work", "routines", "communication"}[i%3], FirstLearnedAt: importDay, Observations: []portraitObservation{{ID: fmt.Sprint("s", i), Kind: portraitObservedSession, Day: day}}})
	}
	if progress := portraitReadiness(sources); !progress.Met || progress.Days != 6 {
		t.Fatalf("progress = %+v", progress)
	}
}

func TestPortraitLatelyBar(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.Local)
	var usual []int64
	// Three a week for the 8 weeks before October, one a week this month.
	for week := 0; week < 8; week++ {
		for i := 0; i < 3; i++ {
			usual = append(usual, time.Date(2026, 9, 30, 9, 0, 0, 0, time.Local).Add(-time.Duration(week*7+i)*24*time.Hour).UnixMilli())
		}
	}
	month := []int64{time.Date(2026, 10, 2, 9, 0, 0, 0, time.Local).UnixMilli(), time.Date(2026, 10, 9, 9, 0, 0, 0, time.Local).UnixMilli(), time.Date(2026, 10, 16, 9, 0, 0, 0, time.Local).UnixMilli(), time.Date(2026, 10, 23, 9, 0, 0, 0, time.Local).UnixMilli()}
	routine := portraitSource{ID: "deep", Title: "Deep mornings", Area: "routines", SessionTimes: append(append([]int64{}, usual...), month...)}
	fact := portraitLately([]portraitSource{routine}, now)
	if fact == nil || fact.MemoryID != "deep" || fact.ThisMonth != 1 || fact.Usual != 3 {
		t.Fatalf("lately = %+v", fact)
	}
	if portraitLately([]portraitSource{routine}, time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)) != nil {
		t.Fatal("lately with under two weeks of this month")
	}
	steady := routine
	steady.SessionTimes = append(append([]int64{}, usual...), month...)
	for _, at := range month {
		steady.SessionTimes = append(steady.SessionTimes, at+3600_000, at+7200_000)
	}
	if fact := portraitLately([]portraitSource{steady}, now); fact != nil {
		t.Fatalf("lately inside the band: %+v", fact)
	}
	health := routine
	health.Title = "Goes to the doctor in the morning"
	if portraitLately([]portraitSource{health}, now) != nil {
		t.Fatal("lately about health")
	}
	work := routine
	work.Area = "work"
	if portraitLately([]portraitSource{work}, now) != nil {
		t.Fatal("lately from a non-routine")
	}
}

func TestPortraitFactsCarryNumbers(t *testing.T) {
	facts := portraitFacts([]portraitSource{
		{ID: "yes", Title: "Says yes within about 20 minutes"},
		{ID: "plain", Title: "Likes clear plans"},
		{ID: "sent", Title: "Writes short replies", SentMessageCount: 32},
		{ID: "money", Title: "Checks the bank balance 3 times a day"},
	})
	ids := map[string]bool{}
	for _, fact := range facts {
		ids[fact.ID] = true
	}
	if !ids["yes"] || !ids["sent"] || ids["plain"] || ids["money"] {
		t.Fatalf("facts = %+v", facts)
	}
}

func TestPortraitBar(t *testing.T) {
	seen := func(id string, days ...string) []portraitObservation {
		var out []portraitObservation
		for i, day := range days {
			out = append(out, portraitObservation{ID: fmt.Sprintf("%s-%d", id, i), Kind: portraitObservedSession, Day: day})
		}
		return out
	}
	live := map[string]portraitInput{
		// One strong belief: 12 sessions over 3 weeks.
		"strong": {MemoryID: "strong", Observations: seen("strong", "2026-10-01", "2026-10-03", "2026-10-08", "2026-10-09", "2026-10-10", "2026-10-14", "2026-10-15", "2026-10-16", "2026-10-17", "2026-10-20", "2026-10-21", "2026-10-22")},
		// Two weak beliefs from one afternoon.
		"weak1": {MemoryID: "weak1", Observations: seen("weak1", "2026-10-01")},
		"weak2": {MemoryID: "weak2", Observations: seen("weak2", "2026-10-01", "2026-10-01")},
		"other": {MemoryID: "other", Observations: seen("other", "2026-10-02")},
		"two":   {MemoryID: "two", Observations: seen("two", "2026-10-01", "2026-10-02")},
		// The same session behind two beliefs counts once.
		"same1": {MemoryID: "same1", Observations: []portraitObservation{{ID: "s1", Day: "2026-10-01"}, {ID: "s2", Day: "2026-10-02"}}},
		"same2": {MemoryID: "same2", Observations: []portraitObservation{{ID: "s1", Day: "2026-10-01"}, {ID: "s2", Day: "2026-10-02"}}},
		"k":     {MemoryID: "k", Confirmed: true, Observations: seen("k", "2026-10-01")},
		"nq":    {MemoryID: "nq", Confirmed: true, NotQuite: true, Observations: seen("nq", "2026-10-01", "2026-10-02", "2026-10-03")},
		// A portrait saved before v2.0.0 has days only.
		"old1": {MemoryID: "old1", Days: []string{"2026-10-01", "2026-10-02"}},
		"old2": {MemoryID: "old2", Days: []string{"2026-10-03"}},
	}
	for ids, want := range map[string]bool{
		"strong":            true,
		"weak1,weak2":       false,
		"weak1,weak2,other": true,
		"two":               false,
		"two,other":         true,
		"same1,same2":       false,
		"k":                 true,
		"nq":                false,
		"nq,weak1":          false,
		"old1,old2":         true,
		"weak1,gone":        false,
		"gone":              false,
	} {
		if got := portraitBelieved(strings.Split(ids, ","), live); got != want {
			t.Fatalf("%s: %t", ids, got)
		}
	}
}

func portraitTestCheck(t *testing.T) portraitCheck {
	t.Helper()
	check := portraitCheck{live: map[string]portraitInput{}, text: map[string]string{}, areas: map[string]string{}, facts: map[string]bool{"belief-4": true}}
	for i, belief := range portraitTestBeliefs {
		id := fmt.Sprintf("belief-%d", i)
		check.live[id] = portraitInput{MemoryID: id, Area: belief.area, Observations: []portraitObservation{{ID: id + "-a", Day: fmt.Sprintf("2026-10-%02d", 1+i%6)}, {ID: id + "-b", Day: fmt.Sprintf("2026-10-%02d", 10+i%6)}}}
		check.text[id] = belief.title
		check.areas[id] = belief.area
	}
	return check
}

func TestPortraitCheckDropsOnlyFailingSentences(t *testing.T) {
	draft, err := parsePortraitDraft("Here it is:\n```json\n" + portraitTestDraft + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	check := portraitTestCheck(t)
	checked := check.checkDraft(draft)
	sentences, lines := checked.Sentences, checked.Lines
	if len(sentences) != 4 || len(lines) != 3 || len(checked.Dropped) != 0 {
		t.Fatalf("clean draft: %d sentences, %d lines", len(sentences), len(lines))
	}
	cases := map[string]func(*portraitDraft){
		"unknown id":          func(d *portraitDraft) { d.Sentences[2].Parts[1].IDs = []string{"belief-6", "nobody"} },
		"no ids":              func(d *portraitDraft) { d.Sentences[2].Parts[1].IDs = nil },
		"pii":                 func(d *portraitDraft) { d.Sentences[2].Parts[1].Text = "trips booked by ada@example.com" },
		"hedge":               func(d *portraitDraft) { d.Sentences[2].Parts[0].Text = "At home you tend to keep the same shape: " },
		"you value":           func(d *portraitDraft) { d.Sentences[2].Parts[0].Text = "You value the same shape at home: " },
		"number from nowhere": func(d *portraitDraft) { d.Sentences[2].Parts[1].Text = "trips booked 7 months ahead" },
		"below the bar":       func(d *portraitDraft) { d.Sentences[2].Parts[1].IDs = []string{"belief-6"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			draft, _ := parsePortraitDraft(portraitTestDraft)
			mutate(&draft)
			sentences := check.checkDraft(draft).Sentences
			if len(sentences) != 3 {
				t.Fatalf("%d sentences kept", len(sentences))
			}
			for _, sentence := range sentences {
				if sentence.Role == "life" {
					t.Fatal("the failing sentence stayed")
				}
			}
		})
	}
	t.Run("insight without a fact", func(t *testing.T) {
		check := portraitTestCheck(t)
		check.facts = map[string]bool{}
		draft, _ := parsePortraitDraft(portraitTestDraft)
		sentences := check.checkDraft(draft).Sentences
		for _, sentence := range sentences {
			if sentence.Role == "insight" {
				t.Fatal("sentence 2 without a fact")
			}
		}
	})
	t.Run("optional clause goes alone", func(t *testing.T) {
		draft, _ := parsePortraitDraft(portraitTestDraft)
		draft.Sentences[3].Parts[3].Does = "books flights"
		sentences := check.checkDraft(draft).Sentences
		action := sentences[len(sentences)-1]
		if action.Role != "action" || action.text() != "So Isoform keeps drafts short." {
			t.Fatalf("action = %q", action.text())
		}
	})
	t.Run("work line about life outside work", func(t *testing.T) {
		draft, _ := parsePortraitDraft(portraitTestDraft)
		draft.WorkWithMe[0].IDs = []string{"belief-6", "belief-7"}
		lines := check.checkDraft(draft).Lines
		if len(lines) != 2 {
			t.Fatalf("%d lines", len(lines))
		}
	})
}

func TestPortraitSchedulerWritesOnceTheBarIsMet(t *testing.T) {
	app, service, runner, path := newPortraitTestRig(t, 19)
	now := time.Now()
	service.check(context.Background(), now)
	if len(runner.inputs) != 0 {
		t.Fatal("wrote below the bar")
	}
	learning := mustPortraitRead(t, app, "learning")
	if learning.Progress == nil || learning.Progress.Beliefs != 19 || learning.Progress.Met {
		t.Fatalf("progress = %+v", learning.Progress)
	}
	dir, _ := memoryDir()
	writePortraitTestBelief(t, dir, 19, portraitTestBeliefs[19].title, portraitTestBeliefs[19].area, now, false)
	service.check(context.Background(), now)
	if len(runner.inputs) != 1 {
		t.Fatal("did not write once the bar was met")
	}
	input := runner.inputs[0]
	if input.BackendHint != agent.BackendHintEphemeral || input.Model != app.chatTaskModel() || input.Priority != agent.PriorityBackground || !input.NoTools || !input.NoSystem || !input.Ephemeral {
		t.Fatalf("route: %+v", input)
	}
	ready := mustPortraitRead(t, app, "ready")
	if len(ready.Sentences) != 4 || len(ready.Name) != 2 || len(ready.WorkWithMe) != 3 || ready.BeliefCount != 20 {
		t.Fatalf("ready = %+v", ready)
	}
	if !strings.HasPrefix(portraitParagraph(ready), "You plan the whole thing") {
		t.Fatalf("paragraph = %q", portraitParagraph(ready))
	}
	doc, err := readPortrait(path)
	if err != nil || doc.Attempt == nil || doc.Attempt.Outcome != "written" || doc.Announced == "" {
		t.Fatalf("doc = %+v %v", doc, err)
	}
	// Reads never call the model.
	for i := 0; i < 3; i++ {
		mustPortraitRead(t, app, "ready")
	}
	if len(runner.inputs) != 1 {
		t.Fatal("a read called the model")
	}
}

func TestPortraitSchedulerThresholdAndWeekly(t *testing.T) {
	app, service, runner, _ := newPortraitTestRig(t, 20)
	now := time.Now()
	service.check(context.Background(), now)
	mustPortraitRead(t, app, "ready")
	dir, _ := memoryDir()
	for i := 20; i < 29; i++ {
		writePortraitTestBelief(t, dir, i, fmt.Sprintf("New habit %c", 'a'+i), "work", now.Add(time.Minute), false)
	}
	service.check(context.Background(), now.Add(time.Hour))
	if len(runner.inputs) != 1 {
		t.Fatal("rewrote after 9 changes")
	}
	writePortraitTestBelief(t, dir, 29, "New habit z", "work", now.Add(time.Minute), false)
	service.check(context.Background(), now.Add(time.Hour))
	if len(runner.inputs) != 2 {
		t.Fatal("no rewrite after 10 changes")
	}
	service.check(context.Background(), now.Add(2*time.Hour))
	if len(runner.inputs) != 2 {
		t.Fatal("rewrote with nothing changed")
	}
	service.check(context.Background(), now.Add(portraitCadence+time.Hour))
	if len(runner.inputs) != 3 {
		t.Fatal("no weekly rewrite")
	}
	// The previous paragraph goes to the writer so unchanged words stay.
	if !strings.Contains(runner.inputs[2].Prompt, "PREVIOUS (unchanged)") {
		t.Fatal("unchanged sentences not passed back")
	}
}

func TestPortraitBackoffAndTryNow(t *testing.T) {
	app, service, runner, path := newPortraitTestRig(t, 20)
	runner.err, runner.response = errors.New("assistant exited"), ""
	now := time.Now()
	service.check(context.Background(), now)
	failed := mustPortraitRead(t, app, "failed")
	if failed.Failure == nil || failed.Failure.Reason != portraitReasonNoAnswer || failed.Failure.RetryAt != now.Add(15*time.Minute).UnixMilli() {
		t.Fatalf("failure = %+v", failed.Failure)
	}
	service.check(context.Background(), now.Add(time.Minute))
	if len(runner.inputs) != 1 {
		t.Fatal("retried inside the backoff")
	}
	service.check(context.Background(), now.Add(16*time.Minute))
	doc, _ := readPortrait(path)
	if len(runner.inputs) != 2 || doc.Attempt.RetryAt != now.Add(16*time.Minute+time.Hour).UnixMilli() {
		t.Fatalf("second backoff: %d calls, %+v", len(runner.inputs), doc.Attempt)
	}
	// Try now runs at once, past the backoff.
	if err := app.RetryAboutYouPortrait(); err != nil {
		t.Fatal(err)
	}
	runner.err, runner.response = nil, portraitTestDraft
	service.check(context.Background(), now.Add(17*time.Minute))
	if len(runner.inputs) != 3 {
		t.Fatal("Try now waited for the backoff")
	}
	mustPortraitRead(t, app, "ready")
}

func TestPortraitLimitAndCouldntUpdate(t *testing.T) {
	app, service, runner, path := newPortraitTestRig(t, 20)
	now := time.Now()
	service.check(context.Background(), now)
	before, _ := os.ReadFile(path)
	runner.err, runner.response = &agent.AssistantUsageLimitError{Cause: errors.New("rate limited")}, ""
	service.check(context.Background(), now.Add(portraitCadence))
	ready := mustPortraitRead(t, app, "ready")
	if ready.Failure == nil || ready.Failure.Reason != portraitReasonLimit || len(ready.Sentences) != 4 {
		t.Fatalf("couldn't update = %+v", ready)
	}
	doc, _ := readPortrait(path)
	var old portraitDocument
	_ = json.Unmarshal(before, &old)
	if doc.GeneratedAt != old.GeneratedAt || portraitParagraph(ready) == "" {
		t.Fatal("a failed rewrite replaced the saved portrait")
	}
}

// An answer Isoform couldn't read retries quietly at 15 minutes and an hour;
// the third in a row shows, with a time, never naming the AI (section 3).
func TestPortraitUnreadableAnswersShowFromTheThird(t *testing.T) {
	app, service, runner, path := newPortraitTestRig(t, 20)
	now := time.Now()
	service.check(context.Background(), now)
	first := portraitParagraph(mustPortraitRead(t, app, "ready"))
	runner.response = "I can't do that."
	at := now.Add(portraitCadence)
	for try, wait := range []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour} {
		service.check(context.Background(), at)
		got := mustPortraitRead(t, app, "ready")
		if portraitParagraph(got) != first {
			t.Fatal("an unreadable answer replaced the portrait")
		}
		if (got.Failure != nil) != (try == 2) {
			t.Fatalf("try %d: failure shown = %+v", try+1, got.Failure)
		}
		doc, _ := readPortrait(path)
		if doc.Attempt.Reason != portraitReasonUnusable || doc.Attempt.RetryAt != at.Add(wait).UnixMilli() {
			t.Fatalf("try %d: attempt = %+v", try+1, doc.Attempt)
		}
		at = at.Add(wait)
	}
}

// When the AI answered and the checks left too little, the card shows what
// survived, never "Couldn't" (sections 2 and 3).
func TestPortraitPublishesWhatSurvived(t *testing.T) {
	thin := func(draft string, roles ...string) string {
		var parsed portraitDraft
		_ = json.Unmarshal([]byte(draft), &parsed)
		for i := range parsed.Sentences {
			if slices.Contains(roles, parsed.Sentences[i].Role) {
				for j := range parsed.Sentences[i].Parts {
					if len(parsed.Sentences[i].Parts[j].IDs) > 0 {
						parsed.Sentences[i].Parts[j].IDs = parsed.Sentences[i].Parts[j].IDs[:1]
					}
				}
			}
		}
		raw, _ := json.Marshal(parsed)
		return string(raw)
	}
	for name, tc := range map[string]struct {
		response  string
		state     string
		sentences []string
	}{
		"one sentence is enough":   {thin(portraitTestDraft, "insight", "life", "action"), "ready", []string{"connect"}},
		"action never shows alone": {thin(portraitTestDraft, "connect", "insight", "life"), "shape", nil},
		"only the shape":           {thin(portraitTestDraft, "connect", "insight", "life", "action"), "shape", nil},
		"three of four, in order":  {thin(portraitTestDraft, "insight"), "ready", []string{"connect", "life", "action"}},
	} {
		t.Run(name, func(t *testing.T) {
			app, service, runner, path := newPortraitTestRig(t, 20)
			runner.response = tc.response
			now := time.Now()
			service.check(context.Background(), now)
			got := mustPortraitRead(t, app, tc.state)
			var roles []string
			for _, sentence := range got.Sentences {
				roles = append(roles, sentence.Role)
			}
			if !slices.Equal(roles, tc.sentences) || got.Failure != nil || len(got.Name) != 2 || len(got.Sides) != 4 {
				t.Fatalf("got %+v", got)
			}
			doc, _ := readPortrait(path)
			support := false
			for _, dropped := range doc.Dropped {
				// The thinned insight also loses the number its second
				// belief carried.
				if dropped.Reason != portraitDropSupport && !(dropped.Role == "insight" && dropped.Reason == portraitDropNumber) {
					t.Fatalf("dropped = %+v", dropped)
				}
				support = support || dropped.Reason == portraitDropSupport
			}
			if support != (len(got.Candidates) > 0) || len(got.Candidates) > 3 {
				t.Fatalf("confirm rows = %+v for %+v", got.Candidates, doc.Dropped)
			}
			// Thin evidence is not a failure: no timed retry. (A write with
			// nothing passing tried twice, with the reasons.)
			calls := len(runner.inputs)
			service.check(context.Background(), now.Add(6*time.Hour))
			if len(runner.inputs) != calls {
				t.Fatalf("%d writes after thin evidence", len(runner.inputs))
			}
		})
	}
}

// A rewrite with fewer passing sentences never replaces a better one unless
// what the shown sentences rest on changed (section 2).
func TestPortraitRewriteNeverMakesItWorse(t *testing.T) {
	app, service, runner, _ := newPortraitTestRig(t, 20)
	now := time.Now()
	service.check(context.Background(), now)
	first := portraitParagraph(mustPortraitRead(t, app, "ready"))
	runner.response = `{"sentences":[{"role":"connect","parts":[{"text":"You "},{"text":"plan the whole thing","ids":["belief-0","belief-1"]},{"text":"."}]}],"lately":null,"workWithMe":[]}`
	service.check(context.Background(), now.Add(portraitCadence))
	// The thin answer is retried once, asking for the missing sentences.
	if len(runner.inputs) != 3 || portraitParagraph(mustPortraitRead(t, app, "ready")) != first {
		t.Fatalf("a thinner rewrite replaced the portrait (%d calls)", len(runner.inputs))
	}
	// It does not rewrite again at once: the kept answer counts as checked.
	service.check(context.Background(), now.Add(portraitCadence+time.Hour))
	if len(runner.inputs) != 3 {
		t.Fatal("rewrote again after keeping the portrait")
	}
	// A belief under a shown sentence changed: the new answer replaces it.
	if _, err := app.ConfirmMemoryEntry("belief-6"); err != nil {
		t.Fatal(err)
	}
	_ = app.RetryAboutYouPortrait()
	service.check(context.Background(), now.Add(portraitCadence+2*time.Hour))
	if got := mustPortraitRead(t, app, "ready"); len(got.Sentences) != 1 {
		t.Fatalf("got %q", portraitParagraph(got))
	}
}

// Each failure has its own cadence: 15 minutes, an hour, then every 6 hours
// when the AI didn't answer; nothing timed when no AI is signed in.
func TestPortraitRetryCadenceByCause(t *testing.T) {
	app, service, runner, path := newPortraitTestRig(t, 20)
	runner.err, runner.response = errors.New("assistant exited"), ""
	at := time.Now()
	for _, wait := range []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 6 * time.Hour} {
		service.check(context.Background(), at)
		doc, _ := readPortrait(path)
		if doc.Attempt.Reason != portraitReasonNoAnswer || doc.Attempt.RetryAt != at.Add(wait).UnixMilli() {
			t.Fatalf("attempt = %+v, want retry in %s", doc.Attempt, wait)
		}
		if got := mustPortraitRead(t, app, "failed"); got.Failure == nil || got.Failure.RetryAt != at.Add(wait).UnixMilli() {
			t.Fatalf("failure = %+v", got.Failure)
		}
		at = at.Add(wait)
	}
	// No AI signed in: no timed retry, ever.
	doc, _ := readPortrait(path)
	doc.Attempt = &portraitAttempt{Outcome: "failed", Reason: portraitReasonNoAI, At: at.UnixMilli(), Failures: 1}
	if err := writePortrait(path, doc); err != nil {
		t.Fatal(err)
	}
	calls := len(runner.inputs)
	service.check(context.Background(), at.Add(24*time.Hour))
	if len(runner.inputs) != calls {
		t.Fatal("retried with no AI signed in")
	}
	if got := mustPortraitRead(t, app, "failed"); got.Failure.Reason != portraitReasonNoAI || got.Failure.RetryAt != 0 {
		t.Fatalf("failure = %+v", got.Failure)
	}
}

// At most 6 writes a day, never two within 10 minutes except Try now.
func TestPortraitWriteCap(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	previousLocal := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = previousLocal })
	for _, tc := range []struct {
		name string
		day  time.Time
	}{
		{"current day", time.Now()},
		{"DST starts", time.Date(2026, 3, 8, 0, 0, 0, 0, loc)},
		{"DST ends", time.Date(2026, 11, 1, 0, 0, 0, 0, loc)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, service, runner, path := newPortraitTestRig(t, 20)
			runner.err, runner.response = errors.New("assistant exited"), ""
			at := time.Date(tc.day.Year(), tc.day.Month(), tc.day.Day(), 0, 1, 0, 0, loc)
			service.check(context.Background(), at)
			// Try now inside 10 minutes runs.
			service.requestWrite(true)
			service.check(context.Background(), at.Add(time.Minute))
			if len(runner.inputs) != 2 {
				t.Fatalf("Try now waited: %d", len(runner.inputs))
			}
			// A due retry inside 10 minutes of the last write waits.
			doc, _ := readPortrait(path)
			doc.Attempt.RetryAt = at.Add(2 * time.Minute).UnixMilli()
			_ = writePortrait(path, doc)
			service.check(context.Background(), at.Add(5*time.Minute))
			if len(runner.inputs) != 2 {
				t.Fatal("two writes within 10 minutes")
			}
			for i := 0; i < 10; i++ {
				service.requestWrite(true)
				service.check(context.Background(), at.Add(time.Duration(20+i)*time.Minute))
			}
			if len(runner.inputs) != portraitWritesPerDay {
				t.Fatalf("%d writes in a day", len(runner.inputs))
			}
			service.requestWrite(true)
			service.check(context.Background(), at.AddDate(0, 0, 1))
			if len(runner.inputs) != portraitWritesPerDay+1 {
				t.Fatal("the cap did not reset the next day")
			}
		})
	}
}

// That's me on a row brings back the sentences it lets through, tinted,
// after 30 seconds without another answer and without another write. Not
// quite marks the belief, and it stops supporting claims (section 6).
func TestPortraitConfirmRows(t *testing.T) {
	app, service, runner, path := newPortraitTestRig(t, 20)
	runner.response = strings.Replace(portraitTestDraft, `"ids":["belief-6","belief-7"]`, `"ids":["belief-6"]`, 1)
	now := time.Now()
	service.now = func() time.Time { return now }
	service.check(context.Background(), now)
	got := mustPortraitRead(t, app, "ready")
	if len(got.Sentences) != 3 || len(got.Candidates) != 1 || got.Candidates[0].MemoryID != "belief-6" || got.Candidates[0].Answer != "" || got.Candidates[0].Text == "" {
		t.Fatalf("got %+v", got)
	}
	calls := len(runner.inputs)
	if err := app.AnswerAboutYouPortraitBelief("belief-6", true); err != nil {
		t.Fatal(err)
	}
	if got := mustPortraitRead(t, app, "ready"); !got.Confirming || got.Candidates[0].Answer != "confirmed" || len(got.Sentences) != 3 {
		t.Fatalf("confirming = %+v", got)
	}
	service.check(context.Background(), now.Add(10*time.Second))
	if len(mustPortraitRead(t, app, "ready").Sentences) != 3 {
		t.Fatal("rewrote before waiting for more")
	}
	service.check(context.Background(), now.Add(31*time.Second))
	got = mustPortraitRead(t, app, "ready")
	if len(got.Sentences) != 4 || got.Sentences[2].Role != "life" || !got.Sentences[2].Changed || got.Confirming || len(got.Candidates) != 0 {
		t.Fatalf("after confirming = %+v", got)
	}
	if len(runner.inputs) != calls {
		t.Fatal("a confirmation called the model")
	}

	// Not quite: the row reads Noted and the belief supports nothing.
	if err := app.AnswerAboutYouPortraitBelief("belief-0", false); err != nil {
		t.Fatal(err)
	}
	entry, _ := app.GetMemoryEntry("belief-0")
	if !portraitNotQuite(entry) || portraitConfirmed(entry) {
		t.Fatal("Not quite did not mark the belief")
	}
	doc, _ := readPortrait(path)
	live := liveInputs(doc.Inputs, mustPortraitEntries(t, app))
	if portraitBelieved([]string{"belief-0"}, live) || portraitBelieved([]string{"belief-0", "belief-1"}, live) {
		t.Fatal("a Not quite belief still supports a claim")
	}
	// Confirming again outranks Not quite.
	if _, err := app.ConfirmMemoryEntry("belief-0"); err != nil {
		t.Fatal(err)
	}
	entry, _ = app.GetMemoryEntry("belief-0")
	if portraitNotQuite(entry) || !portraitConfirmed(entry) {
		t.Fatal("a new confirmation did not outrank Not quite")
	}
}

func mustPortraitEntries(t *testing.T, app *App) []MemoryEntry {
	t.Helper()
	entries, err := app.GetMemoryEntries()
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// Imported history beliefs are dated by their sessions, never the import.
func TestPortraitImportedBeliefsCountTheirSessions(t *testing.T) {
	app, service, _, _ := newPortraitTestRig(t, 1)
	sources := service.portraitSources(mustPortraitEntries(t, app))
	if len(sources) != 1 || len(sources[0].observations()) != 2 || len(sources[0].days()) != 2 {
		t.Fatalf("sources = %+v", sources)
	}
	for _, observation := range sources[0].observations() {
		if observation.Kind != portraitObservedSession || observation.Day == portraitLocalDay(time.Now().UnixMilli()) {
			t.Fatalf("observation = %+v", observation)
		}
	}
}

// The checks after the writer (section 5): each new ban drops only its
// sentence, frequency words pass when the beliefs support them, and numbers
// match within rounding.
func TestPortraitChecksV2(t *testing.T) {
	check := portraitTestCheck(t)
	check.names = []string{"Priya"}
	check.text["belief-4"] = "Says yes within about 20 minutes, usually by 9:30 AM, in 85% of threads"
	check.text["belief-5"] = "Sends replies that say no 48 hours after they arrive, often"
	insight := func(text string) portraitSentence {
		return portraitSentence{Role: "insight", Parts: []portraitPart{{Text: text, IDs: []string{"belief-4", "belief-5"}}}}
	}
	for text, want := range map[string]string{
		"Your yeses go out in about 20 minutes, your nos take about two days.": "",
		"Your yeses go out in about 25 minutes.":                               "number",
		"Your yeses go out by 9:35 AM.":                                        "",
		"Your yeses go out by 9:45 AM.":                                        "number",
		"Your yeses go out by about 10 AM.":                                    "",
		"You say yes in 82% of threads.":                                       "",
		"You say yes in 70% of threads.":                                       "number",
		"Your nos take three days.":                                            "number",
		"You usually say yes fast.":                                            "",
		"Your nos often wait.":                                                 "",
		"You sometimes say yes fast.":                                          "",
		"You always say yes fast.":                                             "banned",
		"Your yeses go out fast; Priya gets the nos.":                          "banned",
		"You replied “let me think about it later” twice.":                     "banned",
		"You procrastinate on the nos.":                                        "banned",
		"You answer faster than most people.":                                  "banned",
		"You will say yes faster next month.":                                  "banned",
		"You love saying yes.":                                                 "banned",
		"Isoform noticed your yeses go fast.":                                  "banned",
		"You say yes fast, so your nos may wait.":                              "banned",
		"Sometimes you say yes fast, other times you wait.":                    "banned",
		"Your yeses come fast, and your salary talks wait.":                    "sensitive",
	} {
		_, reason, code, _ := check.judgeSentence(insight(text))
		if reason != want {
			t.Errorf("%q: reason %q (%s), want %q", text, reason, code, want)
		}
	}
	// Feelings pass when every cited belief is confirmed.
	confirmed := check.live["belief-4"]
	confirmed.Confirmed = true
	check.live["belief-4"] = confirmed
	if _, reason, _, _ := check.judgeSentence(portraitSentence{Role: "connect", Parts: []portraitPart{{Text: "You love saying yes.", IDs: []string{"belief-4"}}}}); reason != "" {
		t.Errorf("a confirmed feeling dropped: %s", reason)
	}
	// The life sentence never rests on People.
	check.areas["belief-6"] = "people"
	if _, reason, _, _ := check.judgeSentence(portraitSentence{Role: "life", Parts: []portraitPart{{Text: "At home, trips come first.", IDs: []string{"belief-6", "belief-7"}}}}); reason != "banned" {
		t.Errorf("life from people: %q", reason)
	}
}

func TestPortraitForgetRemovesOnlyWhatRestedOnIt(t *testing.T) {
	_, service, runner, path := newPortraitTestRig(t, 20)
	app, _ := newForgetTestApp(t)
	app.runner, app.aboutYouPortrait, service.app = runner, service, app
	service.check(context.Background(), time.Now())
	mustPortraitRead(t, app, "ready")
	preview, err := app.GetMemoryForgetPreview("belief-5")
	if err != nil {
		t.Fatal(err)
	}
	// Drain any wake from setup so the forget's own wake is observed.
	select {
	case <-service.wake:
	default:
	}
	if err := app.ForgetMemoryEntry("belief-5", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	got := mustPortraitRead(t, app, "ready")
	if len(got.Sentences) != 3 || !got.Forgotten {
		t.Fatalf("after forget: %+v", got)
	}
	for _, sentence := range got.Sentences {
		if sentence.Role == "insight" {
			t.Fatal("the sentence resting on the forgotten belief stayed")
		}
	}
	if len(got.WorkWithMe) != 2 {
		t.Fatalf("lines = %+v", got.WorkWithMe)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"belief-5"`) || strings.Contains(string(raw), "your nos take") {
		t.Fatal("the forgotten belief or its text is still stored")
	}
	select {
	case <-service.wake:
	default:
		t.Fatal("no rewrite queued")
	}
}

func TestPortraitForgetStorageFailureDoesNotDeleteSource(t *testing.T) {
	_, service, runner, path := newPortraitTestRig(t, 20)
	app, dir := newForgetTestApp(t)
	app.runner, app.aboutYouPortrait, service.app = runner, service, app
	service.check(context.Background(), time.Now())
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview("belief-5")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("belief-5", preview.ConfirmationToken); err == nil {
		t.Fatal("acknowledged Forget without durable portrait cleanup")
	}
	if _, err := os.ReadFile(filepath.Join(dir, "belief-5.md")); err != nil {
		t.Fatal("failed erasure already deleted the belief")
	}
	_ = os.Remove(path)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("belief-5", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
}

func TestPortraitEditAndPrivacyRemoveAtRead(t *testing.T) {
	for _, change := range []string{"edit", "delete", "sensitive", "closed-eyes-sensitive"} {
		t.Run(change, func(t *testing.T) {
			app, service, _, path := newPortraitTestRig(t, 20)
			service.check(context.Background(), time.Now())
			mustPortraitRead(t, app, "ready")
			dir, _ := memoryDir()
			file := filepath.Join(dir, "belief-6.md")
			content, _ := os.ReadFile(file)
			switch change {
			case "edit":
				if _, err := app.SaveMemoryEntry("belief-6", strings.ReplaceAll(string(content), "months ahead", "a week ahead")); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := app.DeleteMemoryEntry("belief-6"); err != nil {
					t.Fatal(err)
				}
			case "sensitive", "closed-eyes-sensitive":
				old := pii.GetUserSensitiveApps()
				t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
				if err := os.WriteFile(file, []byte(strings.Replace(string(content), "---\n", "---\napps: [1Password]\n", 1)), 0600); err != nil {
					t.Fatal(err)
				}
				app.closedEyes.enabled.Store(change == "closed-eyes-sensitive")
			}
			got := mustPortraitRead(t, app, "ready")
			if strings.Contains(portraitParagraph(got), "trips booked") || len(got.Sentences) != 3 {
				t.Fatalf("after %s: %q", change, portraitParagraph(got))
			}
			raw, _ := os.ReadFile(path)
			if strings.Contains(string(raw), "trips booked") {
				t.Fatal("removed text still stored")
			}
		})
	}
}

func TestPortraitClosedEyesNeverWrites(t *testing.T) {
	app, service, runner, _ := newPortraitTestRig(t, 20)
	app.closedEyes.enabled.Store(true)
	service.check(context.Background(), time.Now())
	if len(runner.inputs) != 0 {
		t.Fatal("Closed Eyes allowed a write")
	}
	app.closedEyes.enabled.Store(false)
	service.check(context.Background(), time.Now())
	app.closedEyes.enabled.Store(true)
	mustPortraitRead(t, app, "ready")
	_ = app.RetryAboutYouPortrait()
	service.check(context.Background(), time.Now().Add(portraitCadence))
	if len(runner.inputs) != 1 {
		t.Fatal("Try now wrote during Closed Eyes")
	}
}

func TestPortraitRedactsModelInput(t *testing.T) {
	_, service, runner, _ := newPortraitTestRig(t, 20)
	dir, _ := memoryDir()
	secret := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"
	if err := os.WriteFile(filepath.Join(dir, "belief-0.md"), []byte(strings.Replace(renderOKFDocument(map[string]any{"title": "Plans", "area": "work"}, "## Memory Points\n\n- Uses token "+secret+" for plans.\n\n## Evidence\n\n- RAW_SCREEN_TEXT_NEVER_INPUT"), "", "", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private.md"), []byte("# PRIVATE_TITLE_NEVER_INPUT\n\n- **Apps**: 1Password\n\n## Memory Points\n\n- PRIVATE_POINT_NEVER_INPUT"), 0600); err != nil {
		t.Fatal(err)
	}
	service.check(context.Background(), time.Now())
	if len(runner.inputs) != 1 {
		t.Fatal("no write")
	}
	prompt := runner.inputs[0].Prompt
	for _, forbidden := range []string{secret, "PRIVATE_TITLE_NEVER_INPUT", "PRIVATE_POINT_NEVER_INPUT", "RAW_SCREEN_TEXT_NEVER_INPUT"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("writer received %q", forbidden)
		}
	}
}

func TestPortraitReadNeverWaitsForAWrite(t *testing.T) {
	app, service, runner, _ := newPortraitTestRig(t, 20)
	entered, unblock := make(chan struct{}, 1), make(chan struct{})
	runner.entered, runner.block = entered, unblock
	done := make(chan struct{})
	go func() { defer close(done); service.check(context.Background(), time.Now()) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("writer not reached")
	}
	start := time.Now()
	got := mustPortraitRead(t, app, "writing")
	if !got.Writing || time.Since(start) > time.Second {
		t.Fatalf("read waited %s: %+v", time.Since(start), got)
	}
	close(unblock)
	<-done
	mustPortraitRead(t, app, "ready")
}

func TestPortraitNotQuiteSavesTheNoteAndRewrites(t *testing.T) {
	app, service, runner, _ := newPortraitTestRig(t, 20)
	service.check(context.Background(), time.Now())
	if err := app.NotQuiteAboutYouPortrait("I'm not slow on nos, I save them for Friday"); err != nil {
		t.Fatal(err)
	}
	got := mustPortraitRead(t, app, "ready")
	if !got.Rewriting {
		t.Fatal("not marked rewriting")
	}
	entries, _ := app.GetMemoryEntries()
	var note MemoryEntry
	for _, entry := range entries {
		if strings.Contains(entry.Content, "save them for Friday") {
			note = entry
		}
	}
	if note.ID == "" || !portraitConfirmed(note) {
		t.Fatalf("note not saved as a confirmed belief: %+v", note)
	}
	service.check(context.Background(), time.Now().Add(time.Minute))
	if len(runner.inputs) != 2 || !strings.Contains(runner.inputs[1].Prompt, "save them for Friday") || !regexp.MustCompile(`is belief b\d+ and outranks`).MatchString(runner.inputs[1].Prompt) {
		t.Fatal("the rewrite did not carry the note")
	}
	if got := mustPortraitRead(t, app, "ready"); got.Rewriting {
		t.Fatal("still rewriting after the rewrite landed")
	}
}

func TestPortraitThatsMeAndUndo(t *testing.T) {
	app, service, _, _ := newPortraitTestRig(t, 20)
	service.check(context.Background(), time.Now())
	confirmed, err := app.ConfirmAboutYouPortrait()
	if err != nil || len(confirmed) != 12 {
		t.Fatalf("confirmed %v %v", confirmed, err)
	}
	got := mustPortraitRead(t, app, "ready")
	if !got.Sentences[0].Parts[1].Confirmed {
		t.Fatal("phrase not solid after That's me")
	}
	if err := app.UndoConfirmAboutYouPortrait(confirmed); err != nil {
		t.Fatal(err)
	}
	got = mustPortraitRead(t, app, "ready")
	if got.Sentences[0].Parts[1].Confirmed {
		t.Fatal("Undo kept the confirmation")
	}
}

func TestPortraitUnreadableFileIsRewritten(t *testing.T) {
	app, service, runner, path := newPortraitTestRig(t, 20)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	// A portrait saved by the previous version.
	if err := os.WriteFile(path, []byte(`{"version":1,"generatedAt":1,"segments":[],"inputs":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	got := mustPortraitRead(t, app, "writing")
	if !got.Writing {
		t.Fatal("not shown as writing")
	}
	service.check(context.Background(), time.Now())
	if len(runner.inputs) != 1 {
		t.Fatal("unreadable file not rewritten")
	}
	mustPortraitRead(t, app, "ready")
}

func TestPortraitAtomicInterruptedWrite(t *testing.T) {
	app, service, _, path := newPortraitTestRig(t, 20)
	service.check(context.Background(), time.Now())
	before, _ := os.ReadFile(path)
	temp := filepath.Join(filepath.Dir(path), ".portrait-interrupted")
	if err := os.WriteFile(temp, []byte(`{"version":2,"sentences":[`), 0600); err != nil {
		t.Fatal(err)
	}
	app.aboutYouPortrait = NewAboutYouPortraitService(app)
	mustPortraitRead(t, app, "ready")
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("interrupted write damaged the live file")
	}
}

func TestPortraitSingleFlightAndShutdown(t *testing.T) {
	app, service, runner, _ := newPortraitTestRig(t, 20)
	entered, unblock := make(chan struct{}, 1), make(chan struct{})
	runner.entered, runner.block = entered, unblock
	ctx, cancel := context.WithCancel(context.Background())
	service.Start(ctx)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("start did not write")
	}
	service.check(context.Background(), time.Now())
	if len(runner.inputs) != 1 {
		t.Fatal("two writes at once")
	}
	cancel()
	service.Stop()
	close(unblock)
	_ = app
}

func TestPortraitTodayCardOnceAndForANewName(t *testing.T) {
	app, service, _, path := newPortraitTestRig(t, 20)
	if card, err := app.GetPortraitTodayCard(); err != nil || card != nil {
		t.Fatalf("card before a portrait: %+v %v", card, err)
	}
	service.check(context.Background(), time.Now())
	card, err := app.GetPortraitTodayCard()
	if err != nil || card == nil || card.Renamed || len(card.Name) != 2 || len(card.Sentence) == 0 {
		t.Fatalf("first card: %+v %v", card, err)
	}
	if err := app.DismissPortraitTodayCard(); err != nil {
		t.Fatal(err)
	}
	if card, _ := app.GetPortraitTodayCard(); card != nil {
		t.Fatal("card came back after Not now")
	}
	// A rewrite with the same name keeps it away; a new name brings it back.
	service.check(context.Background(), time.Now().Add(portraitCadence+time.Hour))
	if card, _ := app.GetPortraitTodayCard(); card != nil {
		t.Fatal("card came back without a new name")
	}
	doc, _ := readPortrait(path)
	doc.TodaySeen = "steady-author"
	if err := writePortrait(path, doc); err != nil {
		t.Fatal(err)
	}
	if card, _ := app.GetPortraitTodayCard(); card == nil || !card.Renamed {
		t.Fatalf("renamed card: %+v", card)
	}
}

// portraitSequenceRunner answers each call with the next response.
type portraitSequenceRunner struct {
	responses []string
	inputs    []*agent.TaskInput
}

func (r *portraitSequenceRunner) Init(context.Context) error { return nil }
func (r *portraitSequenceRunner) IsReady() bool              { return true }
func (r *portraitSequenceRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.inputs = append(r.inputs, input)
	answer := r.responses[min(len(r.inputs), len(r.responses))-1]
	return "portrait-sequence", callback(&agent.StreamEvent{Type: "assistant", Content: answer})
}
func (r *portraitSequenceRunner) CancelRun(string) error                   { return nil }
func (r *portraitSequenceRunner) RespondToPermission(string, string) error { return nil }
func (r *portraitSequenceRunner) CancelPermission(string)                  {}
func (r *portraitSequenceRunner) Shutdown() error                          { return nil }

// A draft whose sentences fail the bar gets one second try with the reasons,
// and the writer sees the dates each belief was seen on.
func TestPortraitRetriesOnceWithTheReasons(t *testing.T) {
	app, service, _, _ := newPortraitTestRig(t, 20)
	oneDay := regexp.MustCompile(`"ids":\[[^\]]*\]`).ReplaceAllString(portraitTestDraft, `"ids":["belief-0"]`)
	runner := &portraitSequenceRunner{responses: []string{oneDay, portraitTestDraft}}
	app.runner = runner
	service.check(context.Background(), time.Now())
	if len(runner.inputs) != 2 {
		t.Fatalf("calls = %d, want a first try and one retry", len(runner.inputs))
	}
	first, retry := runner.inputs[0].Prompt, runner.inputs[1].Prompt
	if !regexp.MustCompile(`"seen":"\d+ times, on \d{4}-\d{2}-\d{2}`).MatchString(first) {
		t.Fatal("the writer does not see the dates beliefs were seen on")
	}
	if !strings.Contains(retry, "PREVIOUS ANSWER:") || !strings.Contains(retry, "fewer than 3 observations") {
		t.Fatalf("retry prompt lacks the reasons: %s", retry[len(first):])
	}
	if got := mustPortraitRead(t, app, "ready"); len(got.Sentences) < 2 {
		t.Fatalf("got %+v", got)
	}
}

// An answer with nothing in it is unreadable: it gets its own retry at once,
// and the log shows only its shape. Two empty answers record unreadable.
func TestPortraitEmptyAnswerRetriesAtOnce(t *testing.T) {
	empty := `{"sentences":[],"lately":null,"workWithMe":[]}`
	app, service, _, _ := newPortraitTestRig(t, 20)
	runner := &portraitSequenceRunner{responses: []string{empty, portraitTestDraft}}
	app.runner = runner
	service.check(context.Background(), time.Now())
	if len(runner.inputs) != 2 || !strings.Contains(runner.inputs[1].Prompt, "had no sentences and no lines") {
		t.Fatalf("calls = %d", len(runner.inputs))
	}
	if got := mustPortraitRead(t, app, "ready"); len(got.Sentences) != 4 {
		t.Fatalf("got %+v", got)
	}

	app2, service2, _, path2 := newPortraitTestRig(t, 20)
	runner2 := &portraitSequenceRunner{responses: []string{empty}}
	app2.runner = runner2
	service2.check(context.Background(), time.Now())
	doc, _ := readPortrait(path2)
	if len(runner2.inputs) != 2 || doc.Attempt == nil || doc.Attempt.Reason != portraitReasonUnusable {
		t.Fatalf("calls = %d, attempt = %+v", len(runner2.inputs), doc.Attempt)
	}
	if shape := portraitAnswerShape(`{"sentences":[], "note":"private words"}`); strings.Contains(shape, "private") || !strings.Contains(shape, "starts with { true, has sentences true") {
		t.Fatalf("shape = %q", shape)
	}
}

// The product's old name in a belief becomes Isoform in the prose, and the
// checks never drop a claim for it.
func TestPortraitProductRename(t *testing.T) {
	_, service, runner, _ := newPortraitTestRig(t, 20)
	service.check(context.Background(), time.Now())
	if !strings.Contains(runner.inputs[0].Prompt, "Where a belief says Yansu (its old name), write Isoform.") {
		t.Fatal("the writer is not told about the rename")
	}
	check := portraitTestCheck(t)
	check.text["belief-0"] = "Runs Yansu on two computers"
	check.text["belief-1"] = "Keeps Yansu open all day"
	for _, text := range []string{"You run Isoform on both of your computers.", "You keep Yansu open all day."} {
		sentence := portraitSentence{Role: "connect", Parts: []portraitPart{{Text: text, IDs: []string{"belief-0", "belief-1"}}}}
		if _, reason, code, _ := check.judgeSentence(sentence); reason != "" {
			t.Errorf("%q dropped: %s %s", text, reason, code)
		}
	}
}

// A banned phrase is named in the log code and the retry note (the phrase is
// ours), and the retry asks for the sentences the beliefs could carry.
func TestPortraitRetryNamesThePhraseAndMissingRoles(t *testing.T) {
	check := portraitTestCheck(t)
	draft, _ := parsePortraitDraft(portraitTestDraft)
	draft.Sentences = []portraitSentence{draft.Sentences[0], draft.Sentences[3]}
	draft.Sentences[1].Parts[0].Text = "So Isoform, based on your habits, "
	checked := check.checkDraft(draft)
	if len(checked.Sentences) != 1 || len(checked.Dropped) != 1 || checked.Dropped[0].Code != "banned_phrase:based on your" {
		t.Fatalf("checked = %+v", checked)
	}
	missing := portraitMissingRoles(checked.Sentences, []string{"connect", "insight", "life", "action"})
	note := check.retryNote(checked, missing, "{}")
	for _, want := range []string{`uses "based on your"`, `add the "insight" sentence`, `add the "life" sentence`, "keeping what worked"} {
		if !strings.Contains(note, want) {
			t.Errorf("retry note lacks %q:\n%s", want, note)
		}
	}
	// Only roles the beliefs could carry are asked for.
	sources := []portraitSource{{ID: "a", Area: "work"}, {ID: "b", Area: "routines"}}
	if roles := portraitPossibleRoles(sources, nil); slices.Contains(roles, "insight") || slices.Contains(roles, "life") {
		t.Fatalf("roles = %v", roles)
	}
}
