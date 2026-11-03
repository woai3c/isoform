package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

// portraitCLIRunner sends the writer prompt to the real Claude Code CLI under
// the person's own sign-in, the way the routed runner would.
type portraitCLIRunner struct {
	home    string
	inputs  []*agent.TaskInput
	answers []string
}

func (r *portraitCLIRunner) Init(context.Context) error { return nil }
func (r *portraitCLIRunner) IsReady() bool              { return true }
func (r *portraitCLIRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.inputs = append(r.inputs, input)
	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "text")
	cmd.Stdin = strings.NewReader(input.Prompt)
	cmd.Env = append(os.Environ(), "HOME="+r.home)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s %s", err, stderr.String(), out)
	}
	r.answers = append(r.answers, string(out))
	return "portrait-cli-run", callback(&agent.StreamEvent{Type: "assistant", Content: string(out)})
}
func (r *portraitCLIRunner) CancelRun(string) error                   { return nil }
func (r *portraitCLIRunner) RespondToPermission(string, string) error { return nil }
func (r *portraitCLIRunner) CancelPermission(string)                  {}
func (r *portraitCLIRunner) Shutdown() error                          { return nil }

// portraitFixture is one person, about 60 beliefs over two weeks.
var portraitFixture = []struct{ title, area string }{
	{"Outlines the whole plan in a doc before writing any code", "work"},
	{"Writes section headings first, then fills them in", "work"},
	{"Builds the first prototype alone before showing anyone", "work"},
	{"Prototypes in Cursor within a day of deciding", "tools"},
	{"Keeps one project open at a time and closes the rest", "work"},
	{"Asks for the numbers before deciding on pricing", "work"},
	{"Opens the spreadsheet before replying about budgets", "work"},
	{"Reads the source code instead of the docs when stuck", "work"},
	{"Rewrites a plan from scratch rather than patching it", "work"},
	{"Reviews pull requests in one batch late in the afternoon", "work"},
	{"Says yes to meeting requests within about 20 minutes", "communication"},
	{"Leaves replies that decline something for about two days", "communication"},
	{"Writes replies of about two sentences", "communication"},
	{"Puts the answer in the first line of a message", "communication"},
	{"Skips greetings in short messages", "communication"},
	{"Writes longer, careful messages to investors", "communication"},
	{"Answers in Chinese to Chinese speakers and in English to others", "communication"},
	{"Uses bullet lists for status updates", "communication"},
	{"Rarely uses emoji in work messages", "communication"},
	{"Sends a summary after every planning meeting", "communication"},
	{"Does deep work in blocks of 90 minutes or more before noon", "routines"},
	{"Moves meetings to after 2 PM", "routines"},
	{"Declines meetings before 11 AM", "routines"},
	{"Plans the week on Sunday evening", "routines"},
	{"Rarely opens work apps after 7 PM", "routines"},
	{"Checks email twice a day, at 11 AM and 5 PM", "routines"},
	{"Walks for 30 minutes after lunch", "routines"},
	{"Keeps notifications off while focusing", "routines"},
	{"Clears the inbox on Friday afternoon", "routines"},
	{"Starts work at about 8 AM", "routines"},
	{"Books trips months ahead", "interests"},
	{"Keeps a running list of places to visit", "interests"},
	{"Cooks dinner from whatever is in the fridge", "interests"},
	{"Looks up quick recipes on weeknights", "interests"},
	{"Reads long articles about city design on weekends", "interests"},
	{"Plays tennis on Saturday mornings", "interests"},
	{"Listens to podcasts about company history", "interests"},
	{"Plans weekend hikes a week ahead", "interests"},
	{"Buys books after reading a single review", "interests"},
	{"Watches one film a week, usually on Friday", "interests"},
	{"Uses Cursor for most coding", "tools"},
	{"Uses Keynote for every presentation", "tools"},
	{"Keeps plans in Notion", "tools"},
	{"Uses Linear to track work", "tools"},
	{"Uses Figma to sketch screens before building them", "tools"},
	{"Keeps a calendar block for every plan", "tools"},
	{"Uses the terminal more than the Finder", "tools"},
	{"Drafts long messages in Notes first", "tools"},
	{"Uses Claude Code for refactors", "tools"},
	{"Uses Safari for research and Chrome for work", "tools"},
	{"Meets the design lead every Tuesday", "people"},
	{"Talks with investors once a month", "people"},
	{"Has a weekly one-on-one with each engineer", "people"},
	{"Calls family on Sunday", "people"},
	{"Prefers written updates to status meetings", "work"},
	{"Ships small changes several times a day", "work"},
	{"Writes a decision record for every big choice", "work"},
	{"Tests the product as a new user every Friday", "work"},
	{"Keeps a short list of the three most important things", "work"},
	{"Turns ideas into a written proposal before discussing them", "work"},
}

// writeBoShapedBeliefs writes the fixture the way Bo's beliefs arrived: all
// imported from assistant history on two days, each dated by the sessions
// behind it over nine days (most one session, some two or three), and one
// confirmed. Under v1.0.0's rule the days were the import days, and nothing
// passed.
func writeBoShapedBeliefs(t *testing.T, dir string) {
	t.Helper()
	today := time.Now()
	for i, belief := range portraitFixture {
		imported := today.Add(-time.Duration(i%2) * 24 * time.Hour)
		var sessions []string
		for _, back := range []int{i % 9, (i + 4) % 9, (i + 7) % 9}[:1+min(2, boShapedExtra(i))] {
			sessions = append(sessions, okfTimestamp(today.Add(-time.Duration(2+back)*24*time.Hour-time.Duration(i)*time.Minute)))
		}
		fields := map[string]any{"type": "Isoform Memory", "title": belief.title, "timestamp": okfTimestamp(imported), "source": assistantHistorySource, "dimension": "preference", "area": belief.area, "history_assistant": "claude", "history_session_at": sessions[0], "history_sessions_at": sessions}
		if i == 0 {
			fields["confirmed_at"] = imported.UTC().Format(time.RFC3339)
		}
		content := renderOKFDocument(fields, "## Memory Points\n\n- "+belief.title+".")
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("bo-%d.md", i)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// boShapedExtra gives every fourth belief a second session and every
// seventh a third.
func boShapedExtra(i int) int {
	extra := 0
	if i%4 == 0 {
		extra++
	}
	if i%7 == 0 {
		extra++
	}
	return extra
}

// TestPortraitBoShapedIsReady: Bo's shape of data meets the readiness bar
// and the support bar can pass, with no model.
func TestPortraitBoShapedIsReady(t *testing.T) {
	app, service, _, _ := newPortraitTestRig(t, 0)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	writeBoShapedBeliefs(t, dir)
	entries, err := app.GetMemoryEntries()
	if err != nil {
		t.Fatal(err)
	}
	sources := service.portraitSources(entries)
	progress := portraitReadiness(sources)
	if !progress.Met || progress.Days < 5 {
		t.Fatalf("progress = %+v", progress)
	}
	live := map[string]portraitInput{}
	strong := 0
	for _, source := range sources {
		live[source.ID] = portraitInputOf(source)
		if portraitBelieved([]string{source.ID}, live) {
			strong++
		}
	}
	// One belief with 3 observations on 2 or more days carries a claim alone;
	// three single-session beliefs from different days carry one together.
	if strong == 0 || !portraitBelieved([]string{"bo-1", "bo-2", "bo-3"}, live) || portraitBelieved([]string{"bo-1", "bo-2"}, live) {
		t.Fatalf("strong = %d", strong)
	}
}

// TestPortraitWithRealModel writes a portrait from about 60 beliefs through
// the real Claude Code CLI. Opt-in: YANSU_CLAUDE_INTEGRATION=1. It spends
// from your own sign-in and lets the CLI update ~/.claude.json. The bo_shaped
// run proves v2.0.0's first Done-when: with Bo's shape of data the card shows
// at least the name, the shape and what passed, never "Couldn't write it yet"
// while the AI answers.
func TestPortraitWithRealModel(t *testing.T) {
	t.Run("varied", func(t *testing.T) { testPortraitWithRealModel(t, false) })
	t.Run("bo_shaped", func(t *testing.T) { testPortraitWithRealModel(t, true) })
}

func testPortraitWithRealModel(t *testing.T, boShaped bool) {
	if os.Getenv("YANSU_CLAUDE_INTEGRATION") != "1" {
		t.Skip("set YANSU_CLAUDE_INTEGRATION=1 to run against the real Claude Code CLI")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not installed")
	}
	// The CLI keeps the real home folder, where its sign-in is; TestMain
	// moved HOME aside, so the real home is what it captured first.
	realHome := testRealHomeDir
	if realHome == "" {
		t.Fatal("the real home directory is unknown; cannot sign the CLI in")
	}
	app, service, _, _ := newPortraitTestRig(t, 0)
	runner := &portraitCLIRunner{home: realHome}
	app.runner = runner
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-14 * 24 * time.Hour)
	if boShaped {
		writeBoShapedBeliefs(t, dir)
	} else {
		for i, belief := range portraitFixture {
			writePortraitTestBelief(t, dir, i, belief.title, belief.area, start.Add(time.Duration(i%12)*24*time.Hour), i%9 == 0)
		}
	}
	service.check(context.Background(), time.Now())
	if len(runner.inputs) == 0 {
		t.Fatal("the writer was not called")
	}
	portrait, err := app.GetAboutYouPortrait()
	if err != nil {
		t.Fatal(err)
	}
	if (portrait.State != "ready" && portrait.State != "shape") || portrait.Failure != nil || len(portrait.Name) != 2 || len(portrait.Sides) != 4 {
		t.Fatalf("portrait: %+v", portrait)
	}
	path, _ := portraitPath()
	if doc, _ := readPortrait(path); doc != nil {
		reasons := map[string]int{}
		for _, dropped := range doc.Dropped {
			reasons[dropped.Reason+"/"+dropped.Code]++
		}
		t.Logf("STATE: %s, %d writer calls, dropped %v, %d confirm rows", portrait.State, len(runner.inputs), reasons, len(portrait.Candidates))
	}
	for _, row := range portrait.Candidates {
		t.Logf("CONFIRM: %s", row.Text)
	}
	t.Logf("NAME: %s %s", portraitNameWordText[portrait.Name[0]][0], portraitNameWordText[portrait.Name[1]][0])
	t.Logf("SIDES: %v", portrait.Sides)
	for _, sentence := range portrait.Sentences {
		var text strings.Builder
		for _, part := range sentence.Parts {
			if len(part.MemoryIDs) > 0 {
				text.WriteString("[" + part.Text + "]")
				continue
			}
			text.WriteString(part.Text)
		}
		t.Logf("%s: %s", strings.ToUpper(sentence.Role), text.String())
	}
	if portrait.Lately != nil {
		t.Logf("LATELY: %+v", portrait.Lately)
	}
	for _, line := range portrait.WorkWithMe {
		t.Logf("WORK WITH ME: %s (%s)", line.Text, line.Area)
	}
	if len(portrait.Sentences) < len(portraitRoles) {
		t.Logf("RAW DRAFT: %s", runner.answers[0])
	}
	if !boShaped && len(portrait.Sentences) < 2 {
		t.Fatalf("fewer than two sentences survived: %+v", portrait.Sentences)
	}
}
