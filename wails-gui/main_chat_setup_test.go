package main

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/storage"
)

// setupFakes swaps the live checks for a test-controlled set of permissions
// and assistant readiness. The user already has a name, so the name step is
// closed silently; nameFakes reopens it.
func setupFakes(t *testing.T, granted map[string]bool, keys []string, assistant *bool) {
	t.Helper()
	prevPerms, prevAssistant := mainChatSetupPermissions, mainChatSetupAssistantReady
	prevSelf, prevAccount := mainChatSetupSelfName, mainChatSetupAccountName
	mainChatSetupSelfName = func(*App) string { return "Ada" }
	mainChatSetupAccountName = func() string { return "" }
	t.Cleanup(func() { mainChatSetupSelfName, mainChatSetupAccountName = prevSelf, prevAccount })
	mainChatSetupPermissions = func(*App) PlatformPermissions {
		items := []PermissionItem{}
		for _, key := range keys {
			items = append(items, PermissionItem{Key: key, Granted: granted[key]})
		}
		return PlatformPermissions{Platform: "darwin", Items: items, Supported: true}
	}
	mainChatSetupAssistantReady = func(*App) bool { return *assistant }
	t.Cleanup(func() { mainChatSetupPermissions, mainChatSetupAssistantReady = prevPerms, prevAssistant })
}

func mainChatItems(t *testing.T, app *App) []storage.StreamItemRow {
	t.Helper()
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items, err := app.store.LoadStreamItems(context.Background(), main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func setupRecords(t *testing.T, app *App) []string {
	t.Helper()
	var out []string
	for _, item := range mainChatItems(t, app) {
		if item.Kind == "system" && item.ToolName == mainChatSetupTool {
			out = append(out, item.Content)
		}
	}
	return out
}

func TestMainChatIntroIsPostedOnceInTheRenderersLanguage(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := true
	setupFakes(t, map[string]bool{}, nil, &assistant)

	// First launch: the language is not known yet, so startup posts nothing.
	app.ensureMainChatIntroOnStartup()
	if items := mainChatItems(t, app); len(items) != 0 {
		t.Fatalf("startup posted before the language was known: %+v", items)
	}
	// The renderer's first read posts it, once.
	for i := 0; i < 3; i++ {
		if _, err := app.GetMainChatSetup("en"); err != nil {
			t.Fatal(err)
		}
	}
	app.ensureMainChatIntroOnStartup()
	var intros []storage.StreamItemRow
	for _, item := range mainChatItems(t, app) {
		if item.Kind == "assistant" {
			intros = append(intros, item)
		}
	}
	if len(intros) != 1 {
		t.Fatalf("intro posted %d times, want 1", len(intros))
	}
	if !strings.HasPrefix(intros[0].Content, "Hi, I'm Isoform.") || !strings.Contains(intros[0].Content, "just tell me here") || strings.Contains(intros[0].Content, "off your plate this week?") {
		t.Fatalf("intro = %q", intros[0].Content)
	}

	// A first launch in Chinese gets the Chinese message from the renderer.
	zh := mainChatTestApp(t)
	if _, err := zh.GetMainChatSetup("zh-CN"); err != nil {
		t.Fatal(err)
	}
	if items := mainChatItems(t, zh); len(items) != 1 || !strings.HasPrefix(items[0].Content, "你好，我是 Isoform。") || !strings.Contains(items[0].Content, "随时在这里告诉我") || strings.Contains(items[0].Content, "先问一句") {
		t.Fatalf("zh intro = %+v", items)
	}
	// A later launch with the language saved posts at startup, but only once
	// onboarding is over: macOS restarts the app in the middle of onboarding
	// after a Screen Recording grant, and the renderer saves the language on
	// its first mount.
	later := mainChatTestApp(t)
	ctx := context.Background()
	if err := later.store.SetUIState(ctx, "ui.language", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"", `{"completed":false}`, "{not json"} {
		if err := later.store.SetUIState(ctx, onboardingStateKey, state); err != nil {
			t.Fatal(err)
		}
		later.ensureMainChatIntroOnStartup()
		if items := mainChatItems(t, later); len(items) != 0 {
			t.Fatalf("startup posted during onboarding (state %q): %+v", state, items)
		}
	}
	if err := later.store.SetUIState(ctx, onboardingStateKey, `{"version":2,"completed":true}`); err != nil {
		t.Fatal(err)
	}
	later.ensureMainChatIntroOnStartup()
	if items := mainChatItems(t, later); len(items) != 1 || !strings.HasPrefix(items[0].Content, "你好") {
		t.Fatalf("startup intro with a saved language = %+v", items)
	}
}

func TestMainChatIntroSkipsAnExistingConversationAndItsFirstTask(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := true
	setupFakes(t, map[string]bool{"microphone": true}, []string{"microphone"}, &assistant)
	ctx := context.Background()
	main, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(ctx, main.LocalID, storage.StreamItemRow{ID: "u1", Kind: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range mainChatItems(t, app) {
		if item.Kind == "assistant" {
			t.Fatalf("intro posted into an existing conversation: %q", item.Content)
		}
	}
	// Everything is in place and the user already talks to Isoform: nothing to ask.
	if state.Next != "" {
		t.Fatalf("an established main chat was offered %q", state.Next)
	}
}

func TestMainChatSetupNeverReasksAndResumes(t *testing.T) {
	app := mainChatTestApp(t)
	keys := []string{"screen_recording", "accessibility", "microphone", "calendars", "notifications", "contacts", "automation_messages"}
	granted := map[string]bool{"screen_recording": true}
	assistant := false
	setupFakes(t, granted, keys, &assistant)

	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	if state.Next != mainChatSetupStepAssistant {
		t.Fatalf("next = %q, want assistant", state.Next)
	}
	// Steps follow the fixed order and skip keys the sequence does not ask for
	// (Messages automation) and keys the platform does not list (system audio).
	var order []string
	for _, step := range state.Steps {
		order = append(order, step.Key)
	}
	want := "name,assistant,screen_recording,accessibility,microphone,calendars,notifications"
	if strings.Join(order, ",") != want {
		t.Fatalf("order = %v", order)
	}
	// Screen Recording was already on: closed silently, and a later revoke
	// does not reopen it.
	granted["screen_recording"] = false
	if state, _ = app.GetMainChatSetup("en"); state.Next != mainChatSetupStepAssistant || state.Steps[2].Status != mainChatSetupGranted {
		t.Fatalf("revoked screen recording reopened: next=%q steps=%+v", state.Next, state.Steps)
	}

	// Not now on the assistant skips it; accessibility is next.
	state, _ = app.AnswerMainChatSetup(mainChatSetupStepAssistant, mainChatSetupNotNow)
	if state.Next != "accessibility" {
		t.Fatalf("next after assistant not now = %q", state.Next)
	}
	// An answer for a step that is not shown now changes nothing.
	if state, _ = app.AnswerMainChatSetup("notifications", mainChatSetupNotNow); state.Next != "accessibility" || state.Steps[6].Status != mainChatSetupPending {
		t.Fatalf("out-of-turn not now was recorded: %+v", state)
	}
	// "granted" without the live grant is not recorded.
	if state, _ = app.AnswerMainChatSetup("accessibility", mainChatSetupGranted); state.Next != "accessibility" {
		t.Fatalf("unverified grant advanced to %q", state.Next)
	}
	// The user turns it on in System Settings; the next read records it.
	granted["accessibility"] = true
	if state, _ = app.GetMainChatSetup("en"); state.Next != "microphone" {
		t.Fatalf("next after settings grant = %q", state.Next)
	}

	// A restart (a new App over the same store) resumes at the same step.
	restarted := &App{store: app.store, activeTags: map[string]bool{}, eventBroadcaster: NewEventBroadcaster()}
	if state, _ = restarted.GetMainChatSetup("en"); state.Next != "microphone" {
		t.Fatalf("after restart next = %q, want microphone", state.Next)
	}

	// Microphone granted through the card; revoking it later never brings it back.
	granted["microphone"] = true
	if state, _ = app.AnswerMainChatSetup("microphone", mainChatSetupGranted); state.Next != "calendars" {
		t.Fatalf("next after microphone = %q", state.Next)
	}
	granted["microphone"] = false
	if state, _ = app.GetMainChatSetup("en"); state.Next != "calendars" {
		t.Fatalf("revoked microphone was asked again: next = %q", state.Next)
	}
	// The assistant becoming ready, then not, does not bring its card back.
	assistant = true
	app.GetMainChatSetup("en")
	assistant = false
	if state, _ = app.GetMainChatSetup("en"); state.Next != "calendars" {
		t.Fatalf("assistant flap reopened a step: next = %q", state.Next)
	}

	for _, key := range []string{"calendars", "notifications"} {
		state, _ = app.AnswerMainChatSetup(key, mainChatSetupNotNow)
	}
	if state.Next != "" {
		t.Fatalf("a first task followed the last permission: next = %q", state.Next)
	}
	// Answering again changes nothing.
	app.AnswerMainChatSetup("calendars", mainChatSetupNotNow)

	records := setupRecords(t, app)
	wantRecords := []string{
		"Assistant: not now.",
		"Accessibility is on.",
		"Microphone is on.",
		"Calendars: not now.",
		"Notifications: not now.",
	}
	if strings.Join(records, "|") != strings.Join(wantRecords, "|") {
		t.Fatalf("records = %v\nwant %v", records, wantRecords)
	}
}

func TestMainChatSetupKeepsAnUnreadableStateAside(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := false
	setupFakes(t, map[string]bool{}, []string{"microphone"}, &assistant)
	ctx := context.Background()
	if _, err := app.GetMainChatSetup("en"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetUIState(ctx, mainChatSetupStateKey, "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetMainChatSetup("en"); err != nil {
		t.Fatal(err)
	}
	if kept, _ := app.store.GetUIState(ctx, mainChatSetupCorruptKey); kept != "{not json" {
		t.Fatalf("unreadable state not kept aside: %q", kept)
	}
	// The intro is already in the chat, so it is not posted again.
	intros := 0
	for _, item := range mainChatItems(t, app) {
		if item.Kind == "assistant" {
			intros++
		}
	}
	if intros != 1 {
		t.Fatalf("intro count after reset = %d, want 1", intros)
	}
}

func TestMainChatSetupRejectsUnknownStepsAndAnswers(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := true
	setupFakes(t, map[string]bool{}, []string{"microphone"}, &assistant)
	if _, err := app.AnswerMainChatSetup("full_disk", mainChatSetupNotNow); err == nil {
		t.Fatal("unknown step accepted")
	}
	if _, err := app.AnswerMainChatSetup("microphone", "maybe"); err == nil {
		t.Fatal("unknown answer accepted")
	}
	// With the assistant ready, the microphone is the first thing to ask.
	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	if state.Next != "microphone" {
		t.Fatalf("next = %q", state.Next)
	}
	if _, err := (&App{}).GetMainChatSetup("en"); err == nil {
		t.Fatal("no store should be an error")
	}
}

func TestMainChatSetupRecordLinesSpeakTheUILanguage(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := false
	setupFakes(t, map[string]bool{}, []string{"microphone"}, &assistant)
	ctx := context.Background()
	if err := app.store.SetUIState(ctx, "ui.language", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetMainChatSetup("zh-CN"); err != nil {
		t.Fatal(err)
	}
	app.AnswerMainChatSetup(mainChatSetupStepAssistant, mainChatSetupNotNow)
	app.AnswerMainChatSetup("microphone", mainChatSetupNotNow)
	if records := setupRecords(t, app); strings.Join(records, "|") != "助手：暂不。|麦克风：暂不。" {
		t.Fatalf("zh records = %v", records)
	}
	if got := mainChatSetupRecordText("calendars", mainChatSetupGranted, "en"); got != "Calendars is on." {
		t.Fatalf("en record = %q", got)
	}
}

func TestMainChatSetupRebuildsAnswersFromTheChatAndClosesTheFirstTask(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := false
	setupFakes(t, map[string]bool{}, []string{"microphone", "calendars"}, &assistant)
	ctx := context.Background()
	app.GetMainChatSetup("en")
	app.AnswerMainChatSetup(mainChatSetupStepAssistant, mainChatSetupNotNow)
	app.AnswerMainChatSetup("microphone", mainChatSetupNotNow)
	if err := app.store.SetUIState(ctx, mainChatSetupStateKey, "{not json"); err != nil {
		t.Fatal(err)
	}
	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	if state.Next != "calendars" {
		t.Fatalf("answers were not rebuilt from the chat: next = %q", state.Next)
	}
	// Onboarding already started a first task: the main chat does not ask.
	if err := app.CloseMainChatSetupSteps([]string{mainChatSetupStepFirstTask}); err != nil {
		t.Fatal(err)
	}
	state, _ = app.AnswerMainChatSetup("calendars", mainChatSetupNotNow)
	if state.Next != "" {
		t.Fatalf("first task still offered after onboarding closed it: next = %q", state.Next)
	}
	for _, line := range setupRecords(t, app) {
		if line == "Setup finished." {
			t.Fatal("closing the first task from onboarding posted a line")
		}
	}
}

func TestMainChatSetupDoesNotReaskWhatOnboardingAsked(t *testing.T) {
	app := mainChatTestApp(t)
	keys := []string{"screen_recording", "accessibility", "microphone", "calendars", "notifications", "contacts", "automation_messages"}
	granted := map[string]bool{"screen_recording": true}
	assistant := false
	setupFakes(t, granted, keys, &assistant)

	// The onboarding window showed its permissions (Screen Recording turned
	// on there), the user chose to hear nothing, and skipped the first task.
	// Unknown keys, Messages automation (not a chat ask) and the assistant
	// are ignored.
	shown := []string{"screen_recording", "accessibility", "automation_messages", "contacts", "calendars"}
	if err := app.CloseMainChatSetupSteps(shown); err != nil {
		t.Fatal(err)
	}
	if err := app.CloseMainChatSetupSteps([]string{"notifications", mainChatSetupStepFirstTask, mainChatSetupStepAssistant, "full_disk"}); err != nil {
		t.Fatal(err)
	}
	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	asked := map[string]bool{}
	for _, step := range state.Steps {
		status[step.Key] = step.Status
		asked[step.Key] = step.Asked
	}
	want := map[string]string{
		mainChatSetupStepAssistant: mainChatSetupPending,
		"screen_recording":         mainChatSetupGranted,
		"accessibility":            mainChatSetupNotNow,
		"microphone":               mainChatSetupPending,
		"calendars":                mainChatSetupNotNow,
		"notifications":            mainChatSetupNotNow,
	}
	for key, value := range want {
		if status[key] != value {
			t.Errorf("%s = %q, want %q", key, status[key], value)
		}
	}
	// The assistant is still asked: the chat cannot answer without one.
	if state.Next != mainChatSetupStepAssistant {
		t.Fatalf("next = %q, want assistant", state.Next)
	}
	// Progress counts the chat's own asks only: the assistant and the microphone.
	for key, want := range map[string]bool{mainChatSetupStepAssistant: true, "microphone": true, "screen_recording": false, "contacts": false} {
		if asked[key] != want {
			t.Errorf("asked[%s] = %v, want %v", key, asked[key], want)
		}
	}
	if _, ok := status[mainChatSetupStepFirstTask]; ok {
		t.Error("the retired first-task step was returned")
	}
	if records := setupRecords(t, app); len(records) != 0 {
		t.Fatalf("closing from onboarding posted lines: %v", records)
	}
	// The first message invites a request instead of asking for a first task
	// again, and mentions the asks below.
	var intro string
	for _, item := range mainChatItems(t, app) {
		if item.Kind == "assistant" {
			intro = item.Content
		}
	}
	if strings.Contains(intro, "off your plate this week?") || !strings.Contains(intro, "just tell me here") || !strings.Contains(intro, "one at a time") {
		t.Fatalf("intro after onboarding = %q", intro)
	}

	// The chat's own answer is kept; onboarding's is given again from the
	// live state. Contacts from an older onboarding version is ignored.
	state, _ = app.AnswerMainChatSetup(mainChatSetupStepAssistant, mainChatSetupNotNow)
	if state.Next != "microphone" {
		t.Fatalf("next after assistant = %q", state.Next)
	}
	granted["contacts"] = true
	if err := app.CloseMainChatSetupSteps([]string{"contacts", mainChatSetupStepAssistant}); err != nil {
		t.Fatal(err)
	}
	state, _ = app.GetMainChatSetup("en")
	for _, step := range state.Steps {
		if step.Key == "contacts" {
			t.Fatal("contacts must only be offered by the People consent sheet")
		}
		// The assistant was shown and answered in the chat: it stays in the count.
		if step.Key == mainChatSetupStepAssistant && (!step.Asked || step.Status != mainChatSetupNotNow) {
			t.Fatalf("assistant after not now = %+v", step)
		}
	}
	if _, err := (&App{}).GetMainChatSetup("en"); err == nil {
		t.Fatal("no store should be an error")
	}
	if err := (&App{}).CloseMainChatSetupSteps(nil); err == nil {
		t.Fatal("closing without a store should be an error")
	}
}

func TestMainChatIntroSaysOnlyWhatIsTrue(t *testing.T) {
	cases := []struct {
		moreBelow   bool
		has, hasNot []string
	}{
		{true, []string{"just tell me here", "one at a time"}, []string{"off your plate this week?"}},
		{false, []string{"just tell me here"}, []string{"off your plate this week?", "one at a time"}},
	}
	for _, c := range cases {
		got := mainChatIntroText("en", c.moreBelow)
		if !strings.HasPrefix(got, "Hi, I'm Isoform.") {
			t.Errorf("%+v: %q", c, got)
		}
		for _, s := range c.has {
			if !strings.Contains(got, s) {
				t.Errorf("%+v: missing %q in %q", c, s, got)
			}
		}
		for _, s := range c.hasNot {
			if strings.Contains(got, s) {
				t.Errorf("%+v: unexpected %q in %q", c, s, got)
			}
		}
		zh := mainChatIntroText("zh-CN", c.moreBelow)
		if strings.Count(zh, "\n\n") != strings.Count(got, "\n\n") || !strings.HasPrefix(zh, "你好，我是 Isoform。") || !strings.Contains(zh, "随时在这里告诉我") || strings.Contains(zh, "先问一句") {
			t.Errorf("%+v: zh does not match en: %q", c, zh)
		}
	}

	// Everything already in place: invite a request without asking for a task.
	app := mainChatTestApp(t)
	assistant := true
	setupFakes(t, map[string]bool{"microphone": true}, []string{"microphone"}, &assistant)
	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	if state.Next != "" {
		t.Fatalf("next = %q", state.Next)
	}
	items := mainChatItems(t, app)
	if len(items) != 1 || strings.Contains(items[0].Content, "one at a time") || !strings.Contains(items[0].Content, "just tell me here") || strings.Contains(items[0].Content, "off your plate this week?") {
		t.Fatalf("intro with nothing to ask = %+v", items)
	}
	asked := 0
	for _, step := range state.Steps {
		if step.Key == mainChatSetupStepFirstTask {
			t.Fatal("the retired first-task step was returned")
		}
		if step.Asked {
			asked++
		}
	}
	if asked != 0 {
		t.Fatalf("asked steps = %d, want 0: %+v", asked, state.Steps)
	}
}

func TestMainChatSetupDoesNotReturnLegacyFirstTask(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := true
	setupFakes(t, map[string]bool{"microphone": true}, []string{"microphone"}, &assistant)
	legacy, err := json.Marshal(mainChatSetupStored{
		IntroAt:     1,
		IntroPosted: true,
		Answers:     map[string]string{},
		Offered:     mainChatSetupStepFirstTask,
		Shown:       map[string]bool{mainChatSetupStepFirstTask: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetUIState(context.Background(), mainChatSetupStateKey, string(legacy)); err != nil {
		t.Fatal(err)
	}
	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	if state.Next != "" {
		t.Fatalf("legacy first task became current: %q", state.Next)
	}
	for _, step := range state.Steps {
		if step.Key == mainChatSetupStepFirstTask {
			t.Fatalf("legacy first task was returned: %+v", step)
		}
	}
}

// The record lines the phone shows name each permission the way the desktop's
// setup cards and the onboarding window do.
func TestMainChatSetupPermissionNamesMatchTheLocales(t *testing.T) {
	for lang, names := range mainChatSetupPermissionNames {
		raw, err := os.ReadFile(filepath.Join("frontend", "src", "i18n", "locales", lang, "chat.json"))
		if err != nil {
			t.Fatal(err)
		}
		var chat struct {
			Setup struct {
				Names map[string]string `json:"names"`
			} `json:"setup"`
		}
		if err := json.Unmarshal(raw, &chat); err != nil {
			t.Fatal(err)
		}
		for _, key := range mainChatSetupPermissionOrder {
			if names[key] != chat.Setup.Names[key] {
				t.Errorf("%s %s: Go says %q, chat.json says %q", lang, key, names[key], chat.Setup.Names[key])
			}
		}
	}
}

func TestMainChatSetupOnboardingCanChangeItsAnswers(t *testing.T) {
	app := mainChatTestApp(t)
	keys := []string{"screen_recording", "microphone", "notifications", "calendars"}
	granted := map[string]bool{}
	assistant := true
	setupFakes(t, granted, keys, &assistant)
	ctx := context.Background()
	statusOf := func(state MainChatSetupState, key string) string {
		for _, step := range state.Steps {
			if step.Key == key {
				return step.Status
			}
		}
		return ""
	}

	// Calendars left off, then turned on after going back: on, not "not now".
	app.CloseMainChatSetupSteps([]string{"calendars", "screen_recording"})
	granted["calendars"] = true
	app.CloseMainChatSetupSteps([]string{"calendars", "screen_recording"})
	// Off, then back to Normal: the chat asks for notifications after all.
	app.CloseMainChatSetupSteps([]string{"notifications"})
	if err := app.ReopenMainChatSetupSteps([]string{"notifications", "microphone"}); err != nil {
		t.Fatal(err)
	}
	app.CloseMainChatSetupSteps([]string{mainChatSetupStepFirstTask})
	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"calendars":        mainChatSetupGranted,
		"screen_recording": mainChatSetupNotNow,
		"notifications":    mainChatSetupPending,
		"microphone":       mainChatSetupPending,
	} {
		if got := statusOf(state, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// The chat's own answer is never changed or reopened by onboarding.
	app.AnswerMainChatSetup("microphone", mainChatSetupNotNow)
	granted["microphone"] = true
	app.CloseMainChatSetupSteps([]string{"microphone"})
	app.ReopenMainChatSetupSteps([]string{"microphone"})
	if state, _ = app.GetMainChatSetup("en"); statusOf(state, "microphone") != mainChatSetupNotNow {
		t.Fatalf("onboarding changed the chat's answer: %+v", state.Steps)
	}

	// An unreadable progress value is rebuilt with onboarding's answers,
	// which leave no line in the chat.
	if err := app.store.SetUIState(ctx, mainChatSetupStateKey, "{not json"); err != nil {
		t.Fatal(err)
	}
	if state, err = app.GetMainChatSetup("en"); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"calendars":        mainChatSetupGranted,
		"screen_recording": mainChatSetupNotNow,
		"microphone":       mainChatSetupNotNow,
	} {
		if got := statusOf(state, key); got != want {
			t.Errorf("after rebuild %s = %q, want %q", key, got, want)
		}
	}
	if state.Next != "notifications" {
		t.Fatalf("after rebuild next = %q, want notifications", state.Next)
	}
	if err := (&App{}).ReopenMainChatSetupSteps(nil); err == nil {
		t.Fatal("reopening without a store should be an error")
	}
}

// nameFakes reads the name from the store, as production does, and makes
// the Mac account's full name accountName.
func nameFakes(t *testing.T, accountName string) {
	t.Helper()
	prevSelf, prevAccount := mainChatSetupSelfName, mainChatSetupAccountName
	mainChatSetupSelfName = storedSelfName
	mainChatSetupAccountName = func() string { return accountName }
	t.Cleanup(func() { mainChatSetupSelfName, mainChatSetupAccountName = prevSelf, prevAccount })
}

func TestMainChatSetupAsksForTheNameFirstAndRecordsIt(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := true
	setupFakes(t, map[string]bool{}, []string{"microphone"}, &assistant)
	nameFakes(t, "Ada Lovelace")

	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	if state.Next != mainChatSetupStepName {
		t.Fatalf("next = %q, want name", state.Next)
	}
	first := state.Steps[0]
	if first.Kind != mainChatSetupKindName || !first.Asked || first.Suggestion != "Ada Lovelace" {
		t.Fatalf("name step = %+v", first)
	}
	for _, step := range state.Steps[1:] {
		if step.Suggestion != "" {
			t.Fatalf("%s carries a suggestion: %+v", step.Key, step)
		}
	}
	// The first message mentions the asks below; the name is one of them.
	intro := mainChatItems(t, app)
	if len(intro) != 1 || !strings.Contains(intro[0].Content, "one at a time") {
		t.Fatalf("intro = %+v", intro)
	}

	// "granted" before a name is stored is not recorded.
	if state, _ = app.AnswerMainChatSetup(mainChatSetupStepName, mainChatSetupGranted); state.Next != mainChatSetupStepName {
		t.Fatalf("name closed without a stored name: next = %q", state.Next)
	}
	// The card saves the name into the People "self" profile, then answers.
	if _, err := app.EnsureSelfProfile("Ada", nil); err != nil {
		t.Fatal(err)
	}
	if state, _ = app.AnswerMainChatSetup(mainChatSetupStepName, mainChatSetupGranted); state.Next != "microphone" {
		t.Fatalf("next after the name = %q", state.Next)
	}
	if records := setupRecords(t, app); strings.Join(records, "|") != "I'll call you Ada." {
		t.Fatalf("records = %v", records)
	}
	var payload map[string]string
	for _, item := range mainChatItems(t, app) {
		if item.ToolName == mainChatSetupTool {
			if err := json.Unmarshal([]byte(item.ToolInput), &payload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if payload["step"] != mainChatSetupStepName || payload["answer"] != mainChatSetupGranted || payload["name"] != "Ada" {
		t.Fatalf("record payload = %v", payload)
	}
	// The sidebar and greetings read the same name.
	if got := app.getUserDisplayName(); got != "Ada" {
		t.Fatalf("display name = %q", got)
	}

	// A restart keeps it answered, and the step now suggests the stored name.
	restarted := &App{store: app.store, activeTags: map[string]bool{}, eventBroadcaster: NewEventBroadcaster()}
	state, _ = restarted.GetMainChatSetup("en")
	if state.Next != "microphone" || state.Steps[0].Status != mainChatSetupGranted || state.Steps[0].Suggestion != "Ada" {
		t.Fatalf("after restart: next=%q name=%+v", state.Next, state.Steps[0])
	}
	if got := mainChatSetupNameRecordText("Ada", "zh-CN"); got != "我会称呼你为 Ada。" {
		t.Fatalf("zh name line = %q", got)
	}
}

func TestMainChatSetupNameIsAskedOnceAndFollowsTheMenu(t *testing.T) {
	assistant := true

	// A name given in onboarding (or before this step existed) closes the
	// step silently: no card, no line, not counted.
	known := mainChatTestApp(t)
	setupFakes(t, map[string]bool{}, []string{"microphone"}, &assistant)
	nameFakes(t, "")
	if _, err := known.EnsureSelfProfile("Bo", nil); err != nil {
		t.Fatal(err)
	}
	state, _ := known.GetMainChatSetup("en")
	if state.Next != "microphone" || state.Steps[0].Status != mainChatSetupGranted || state.Steps[0].Asked {
		t.Fatalf("known name: next=%q name=%+v", state.Next, state.Steps[0])
	}
	if records := setupRecords(t, known); len(records) != 0 {
		t.Fatalf("known name posted %v", records)
	}

	// Renamed from the bottom-left menu while the card is shown: the next read
	// records it, once.
	renamed := mainChatTestApp(t)
	if state, _ = renamed.GetMainChatSetup("en"); state.Next != mainChatSetupStepName || state.Steps[0].Suggestion != "" {
		t.Fatalf("no account name: %+v", state.Steps[0])
	}
	if _, err := renamed.EnsureSelfProfile("Bo", nil); err != nil {
		t.Fatal(err)
	}
	renamed.GetMainChatSetup("en")
	renamed.GetMainChatSetup("en")
	if records := setupRecords(t, renamed); strings.Join(records, "|") != "I'll call you Bo." {
		t.Fatalf("renamed records = %v", records)
	}

	// Not now closes it for good; a name set later adds no line.
	later := mainChatTestApp(t)
	later.GetMainChatSetup("en")
	if state, _ = later.AnswerMainChatSetup(mainChatSetupStepName, mainChatSetupNotNow); state.Next != "microphone" {
		t.Fatalf("next after not now = %q", state.Next)
	}
	if _, err := later.EnsureSelfProfile("Cy", nil); err != nil {
		t.Fatal(err)
	}
	if state, _ = later.GetMainChatSetup("en"); state.Steps[0].Status != mainChatSetupNotNow {
		t.Fatalf("not now reopened: %+v", state.Steps[0])
	}
	if records := setupRecords(t, later); strings.Join(records, "|") != "Your name: not now." {
		t.Fatalf("later records = %v", records)
	}
}

// Binding WeChat or Teams (no display handle) before any name leaves the
// name step open with the account-name suggestion.
func TestMainChatSetupNameStaysOpenAfterAnUnnamedIntegrationBind(t *testing.T) {
	app := mainChatTestApp(t)
	assistant := true
	setupFakes(t, map[string]bool{}, []string{"microphone"}, &assistant)
	nameFakes(t, "Ada Lovelace")
	if err := app.bindIntegrationSelfIdentity(context.Background(), "wechat", "wx-1", ""); err != nil {
		t.Fatal(err)
	}
	state, err := app.GetMainChatSetup("en")
	if err != nil {
		t.Fatal(err)
	}
	if state.Next != mainChatSetupStepName || state.Steps[0].Status != mainChatSetupPending || state.Steps[0].Suggestion != "Ada Lovelace" {
		t.Fatalf("after an unnamed bind: next=%q name=%+v", state.Next, state.Steps[0])
	}
	// The card's save names the existing self principal.
	if _, err := app.EnsureSelfProfile("Ada", nil); err != nil {
		t.Fatal(err)
	}
	if state, _ = app.AnswerMainChatSetup(mainChatSetupStepName, mainChatSetupGranted); state.Next != "microphone" {
		t.Fatalf("next after the name = %q", state.Next)
	}
}

func TestAccountFullNameSkipsTheLoginName(t *testing.T) {
	// Whatever this machine's account is, the suggestion is never the short
	// login name and never padded.
	got := accountFullName()
	if got != strings.TrimSpace(got) {
		t.Fatalf("untrimmed: %q", got)
	}
	if u, err := user.Current(); err == nil && got != "" && got == u.Username {
		t.Fatalf("suggested the login name %q", got)
	}
}
