package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/interests"
)

// storeTestInterests writes a stored interest list for the app.
func storeTestInterests(t *testing.T, app *App, list ...interests.Interest) {
	t.Helper()
	if _, err := app.interestsSvc().store.update(func(st *interestsStateFile) { st.Interests = list }); err != nil {
		t.Fatal(err)
	}
	app.topicsSvc().invalidate()
}

func testInterest(id, name, why string, queries ...string) interests.Interest {
	return interests.Interest{
		ID: id, Key: interests.KeyFor(name), Name: name, Why: why, Kind: interests.KindProject,
		Confidence: 0.8, Queries: queries,
		Evidence: []interests.Ref{{Key: "activity:" + id, Source: interests.SourceActivity, At: topicHoursAgo(2).UnixMilli(), Minutes: 10}},
	}
}

func TestYourTopicsListsInterestsAndFallsBackToPhrases(t *testing.T) {
	app := newTopicFindsTestApp(t)
	app.interestsSvc().now = func() time.Time { return topicTestNow }
	// Claude Code is signed in: research can run, so the status says what
	// the topics allow.
	previousReady, previousKnown := claudeResearchReady, claudeResearchKnown
	t.Cleanup(func() { claudeResearchReady, claudeResearchKnown = previousReady, previousKnown })
	claudeResearchReady = func() bool { return true }
	claudeResearchKnown = func() (bool, bool) { return true, true }
	svc := app.topicsSvc()

	// Nothing at all: no topics.
	if finds := app.topicFindsView(); finds.Status != "no_topics" || len(finds.Topics) != 0 {
		t.Fatalf("with nothing observed: %+v", finds)
	}

	// No interests yet: the phrases stand in, a search can still run, and
	// the line under New on your topics names none of them.
	svc.collect = func(now time.Time) topicInput {
		return topicInput{Now: now, Sessions: topicChatsOnThreeDays("Fix wails drag regions")}
	}
	svc.invalidate()
	view := svc.view()
	if view.Interests || len(view.Topics) == 0 || view.Topics[0].Why != "" {
		t.Fatalf("fallback view = %+v", view)
	}
	finds := app.topicFindsView()
	if finds.Status != "none" || len(finds.Topics) != 0 {
		t.Fatalf("with phrases only: %+v", finds)
	}

	storeTestInterests(t, app,
		testInterest("i-hat", "wearable AI camera hats", "You are prototyping a smart cap.", "smart hat camera", "XIAO ESP32-S3 Sense camera"),
		testInterest("i-asr", "on-device speech recognition", "You fix recording quality.", "speaker diarization on-device"),
	)
	view = svc.view()
	if !view.Interests || len(view.Topics) != 2 {
		t.Fatalf("interest view = %+v", view)
	}
	hat := view.Topics[0]
	if hat.ID != "i-hat" || hat.Label != "wearable AI camera hats" || hat.Why != "You are prototyping a smart cap." || hat.Kind != "project" || hat.LastSeenAt == "" {
		t.Fatalf("interest topic = %+v", hat)
	}
	finds = app.topicFindsView()
	if finds.Status != "none" || strings.Join(finds.Topics, "|") != "wearable AI camera hats|on-device speech recognition" {
		t.Fatalf("with interests: %+v", finds)
	}

	// Pins, renames and removals apply to interests by id.
	if _, err := svc.update(UserTopicUpdate{ID: "i-asr", Action: "rename", Label: "speech models"}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.update(UserTopicUpdate{ID: "i-hat", Action: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Topics) != 1 || view.Topics[0].Label != "speech models" || view.Topics[0].Original != "on-device speech recognition" {
		t.Fatalf("after rename and remove: %+v", view.Topics)
	}
	if len(view.Removed) != 1 || view.Removed[0].Why == "" {
		t.Fatalf("removed = %+v", view.Removed)
	}
	// A renamed interest that is removed is refused under both names.
	if _, err := svc.update(UserTopicUpdate{ID: "i-asr", Action: "remove"}); err != nil {
		t.Fatal(err)
	}
	user := app.interestsUserState()
	if got := strings.Join(user.Removed, "|"); got != "on-device speech recognition|speech models|wearable AI camera hats" {
		t.Fatalf("the next pass is not told what was removed: %v", got)
	}
	if !user.RemovedIDs["i-hat"] || !user.RemovedIDs["i-asr"] {
		t.Fatalf("removed ids %v", user.RemovedIDs)
	}
}

func TestShownInterestsAlwaysCarryWhy(t *testing.T) {
	app := newTopicFindsTestApp(t)
	app.interestsSvc().now = func() time.Time { return topicTestNow }
	svc := app.topicsSvc()
	storeTestInterests(t, app,
		testInterest("i-hat", "wearable AI camera hats", "You are prototyping a smart cap.", "smart hat camera"),
		testInterest("i-asr", "on-device speech recognition", "You fix recording quality.", "speaker diarization on-device"),
	)
	view := svc.view()
	if !view.Interests || len(view.Topics) != 2 {
		t.Fatalf("view = %+v", view)
	}
	// The dialog contract: a shown topic always says why. It goes red the
	// moment any stage of the pipeline ships a topic without its why line.
	for _, topic := range view.Topics {
		if topic.Why == "" {
			t.Fatalf("shown topic %q lost its why line: %+v", topic.Label, topic)
		}
	}
}

func TestTopicFindsSendInterestNamesAndQueries(t *testing.T) {
	rig := newHandoffTestRig(t)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	ctx := context.Background()
	scheduler := NewCronScheduler(rig.state, func(string, any) {}, func(string, string) {}, nil)
	rig.app.cronScheduler = scheduler
	rig.app.ctx = ctx
	runCtx, cancel := context.WithCancel(ctx)
	go scheduler.Run(runCtx)
	select {
	case <-scheduler.loadedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not load")
	}
	t.Cleanup(func() {
		cancel()
		scheduler.Stop()
	})
	previousReady, previousDispatch := topicFindsProviderReady, topicFindsDispatch
	t.Cleanup(func() { topicFindsProviderReady, topicFindsDispatch = previousReady, previousDispatch })
	topicFindsProviderReady = func(*App) bool { return true }
	topicFindsDispatch = func(*App, string) error { return nil }
	svc := rig.app.topicsSvc()
	svc.now = func() time.Time { return topicTestNow }
	rig.app.interestsSvc().now = func() time.Time { return topicTestNow }
	svc.collect = func(at time.Time) topicInput { return topicInput{Now: at} }

	// Research reads now require real, current source evidence. These names
	// are explicit public notes, rather than dangling synthetic activity IDs.
	readable := []interests.Interest{
		testInterest("i-hat", "wearable AI camera hats", "You are prototyping a smart cap.", "smart hat camera", "XIAO ESP32-S3 Sense camera"),
		testInterest("i-asr", "on-device speech recognition", "You fix recording quality.", "speaker diarization on-device"),
	}
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	for i, it := range readable {
		id := "research-fixture-" + it.ID
		body := withMemoryReadProvenance(buildOKFMemoryMarkdown(id, it.Name, topicTestNow, "manual", "user_insight", []string{it.Name}), memoryReadSource("manual"))
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		readable[i].Evidence = []interests.Ref{{Key: "about:" + id + "#0", Source: interests.SourceAbout, At: topicHoursAgo(2).UnixMilli()}}
	}
	storeTestInterests(t, rig.app, readable...)
	if _, err := svc.update(UserTopicUpdate{ID: "i-asr", Action: "rename", Label: "speech models"}); err != nil {
		t.Fatal(err)
	}
	if status, err := rig.app.startTopicFinds("manual"); err != nil || status != "started" {
		t.Fatalf("start = %q, %v", status, err)
	}
	sent := svc.store.load().Finds.Sent
	if len(sent) != 2 || sent[0].Label != "wearable AI camera hats" || strings.Join(sent[0].Queries, "|") != "smart hat camera|XIAO ESP32-S3 Sense camera" {
		t.Fatalf("sent = %+v", sent)
	}
	if sent[1].Label != "speech models" || len(sent[1].Queries) != 0 {
		t.Fatalf("a renamed interest must search the user's name alone: %+v", sent[1])
	}
	prompt, ok := rig.app.appResearchPrompt(rig.app.findTopicFindsJob(), researchViaClaudeCode)
	if !ok {
		t.Fatal("no prompt for the topic search")
	}
	for _, want := range []string{
		`["wearable AI camera hats","speech models"]`,
		`{"wearable AI camera hats":["smart hat camera","XIAO ESP32-S3 Sense camera"]}`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt misses %s:\n%s", want, prompt)
		}
	}
	for _, leaked := range []string{"You are prototyping", "speaker diarization", "activity:", "about:"} {
		if strings.Contains(prompt, leaked) {
			t.Fatalf("the prompt carries %q", leaked)
		}
	}
	// Daily News takes the same names.
	if got := rig.app.collectUserInterests(); strings.Join(got, "|") != "wearable AI camera hats|speech models" {
		t.Fatalf("news interests = %v", got)
	}

	// Off preserves the saved interests without rewriting their file.
	path := rig.app.interestsSvc().store.path
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.setEnabled(false); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatalf("Off changed the saved interests: %s, %v", after, err)
	}
	if filepath.Base(path) != interestsStateFileName {
		t.Fatalf("path %s", path)
	}
}

func TestInterestsPassAfterOffPreservesStoredList(t *testing.T) {
	answer := `{"interests":[{"name":"wearable AI camera hats","kind":"project","confidence":0.9,"evidence":["A1"],"queries":["smart hat camera"]}]}`
	svc, _ := fakeInterestsService(t, answer)
	if _, err := svc.store.update(func(st *interestsStateFile) {
		st.Interests = []interests.Interest{testInterest("saved", "on-device speech recognition", "Saved before the switch", "on-device speech models")}
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(svc.store.path)
	if err != nil {
		t.Fatal(err)
	}
	on := true
	svc.enabled = func() bool { return on }
	model := svc.model
	svc.model = func(ctx context.Context, prompt, name string) (string, error) {
		// The user turns the feature off while the model runs.
		on = false
		return model(ctx, prompt, name)
	}
	if svc.maybeRun(context.Background(), "test") {
		t.Fatal("a pass that ended after Off stored its list")
	}
	if on {
		t.Fatal("the pass did not reach the switch-off during the model call")
	}
	if after, err := os.ReadFile(svc.store.path); err != nil || string(after) != string(before) {
		t.Fatalf("a pass finishing after Off changed the saved interests: %s, %v", after, err)
	}
}
