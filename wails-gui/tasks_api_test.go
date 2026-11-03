package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"wails-gui/internal/handoff"
)

func TestCreateTaskValidation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		input   CreateTaskInput
		wantErr string
	}{
		{"empty", CreateTaskInput{}, "the title must be 1 to 200 bytes"},
		{"whitespace", CreateTaskInput{Title: "  \n"}, "the title must be 1 to 200 bytes"},
		{"long title", CreateTaskInput{Title: strings.Repeat("a", 201)}, "the title must be 1 to 200 bytes"},
		{"unicode bytes", CreateTaskInput{Title: strings.Repeat("界", 67)}, "the title must be 1 to 200 bytes"},
		{"long brief", CreateTaskInput{Title: "Task", Brief: strings.Repeat(" ", 8193)}, "the brief must be at most 8 KiB"},
		{"relative", CreateTaskInput{Title: "Task", WorkDir: "."}, "the work folder must be an absolute folder on this computer"},
		{"missing", CreateTaskInput{Title: "Task", WorkDir: filepath.Join(dir, "missing")}, "the work folder must be an absolute folder on this computer"},
		{"file", CreateTaskInput{Title: "Task", WorkDir: file}, "the work folder must be an absolute folder on this computer"},
		{"limits", CreateTaskInput{Title: strings.Repeat("a", 200), Brief: strings.Repeat("b", 8192)}, ""},
		{"empty optional fields", CreateTaskInput{Title: "Task"}, ""},
		{"folder", CreateTaskInput{Title: " Task ", Brief: " Brief ", WorkDir: " " + dir + "/. "}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := mainChatTestApp(t)
			var err error
			app.handoffStore, err = handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			view, err := app.CreateTask(tc.input)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				entries, listErr := app.handoffStore.List()
				if listErr != nil || len(entries) != 0 {
					t.Fatalf("invalid input saved entries: %+v, %v", entries, listErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if view.Title != strings.TrimSpace(tc.input.Title) || view.Body != strings.TrimSpace(tc.input.Brief) {
				t.Fatalf("normalization = %+v", view)
			}
			if tc.input.WorkDir != "" && view.Workdir != dir {
				t.Fatalf("workdir = %q", view.Workdir)
			}
		})
	}
}

func TestCreateTaskSharesPhonePersistenceAndEvents(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	app := bridge.app
	var err error
	app.handoffStore, err = handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.eventBroadcaster = NewEventBroadcaster()
	ch := app.eventBroadcaster.AddClient("tasks")
	defer app.eventBroadcaster.RemoveClient("tasks")
	input := CreateTaskInput{Title: " Task ", Brief: " Brief ", WorkDir: t.TempDir()}
	desktop, err := app.CreateTask(input)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"idempotencyKey": "task", "title": input.Title, "brief": input.Brief, "workDir": input.WorkDir})
	if err != nil {
		t.Fatal(err)
	}
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeHandoff), http.MethodPost, "/handoffs", string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("phone create = %d %s", response.Code, response.Body.String())
	}
	var phone struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(mobileTestDecodeReply(t, response).Data, &phone); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		source handoff.Source
	}{{desktop.ID, handoff.SourceDesktop}, {phone.ID, handoff.SourceMobile}} {
		saved, err := app.handoffStore.Get(tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Title != "Task" || saved.Body != "Brief" || saved.Status != handoff.StatusActive || saved.Source != tc.source || saved.SourceID != saved.ID || saved.Workdir != input.WorkDir || saved.CreatedAt == 0 {
			t.Fatalf("saved = %+v", saved)
		}
		for _, name := range []string{"handoff:updated", "today:needs-changed", "today:done-changed", "handoff:created", "today:needs-changed", "today:done-changed"} {
			raw := awaitBrowserEvent(t, ch, name)
			var event BrowserEvent
			if err := json.Unmarshal([]byte(raw), &event); err != nil {
				t.Fatal(err)
			}
			if event.Event != name || (strings.HasPrefix(name, "handoff:") && event.Args[0].(map[string]any)["id"] != saved.ID) || (strings.HasPrefix(name, "today:") && len(event.Args) != 0) {
				t.Fatalf("event = %s", raw)
			}
		}
	}
	reopened, err := handoff.NewStore(app.handoffStore.Dir())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.Get(desktop.ID)
	if err != nil || saved.Source != handoff.SourceDesktop {
		t.Fatalf("desktop source roundtrip = %+v, %v", saved, err)
	}
}

func TestCreateTaskStoreErrors(t *testing.T) {
	if _, err := (&App{}).CreateTask(CreateTaskInput{Title: "Task"}); !errors.Is(err, errTaskStoreNotReady) {
		t.Fatalf("missing store: %v", err)
	}
	store, err := handoff.NewStore(filepath.Join(t.TempDir(), "handoffs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Dir(), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	app := &App{handoffStore: store, eventBroadcaster: NewEventBroadcaster()}
	ch := app.eventBroadcaster.AddClient("failed")
	defer app.eventBroadcaster.RemoveClient("failed")
	if _, err := app.CreateTask(CreateTaskInput{Title: "Task"}); err == nil {
		t.Fatal("save error hidden")
	}
	select {
	case event := <-ch:
		t.Fatalf("failed save emitted %s", event)
	default:
	}
}

func TestTaskSuggestionsUseOffersWithoutPairedPhone(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	want, err := bridge.app.mobileOffers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err := bridge.app.ListTaskSuggestions()
	if err != nil || len(got) != 1 {
		t.Fatalf("desktop suggestions = %+v, %v", got, err)
	}
	for _, offer := range want.Offers {
		if offer.Kind == "suggestion" {
			continue
		}
		expected := TaskSuggestionView{ID: offer.ID, Kind: offer.Kind, Title: offer.Title, Why: offer.Why, WhyLocalized: offer.WhyLocalized, Prompt: offer.Prompt, Version: offer.Version, DeckKind: offer.DeckKind, Evidence: offer.Evidence}
		if !reflect.DeepEqual(got[0], expected) {
			t.Fatalf("desktop suggestion = %+v, want %+v", got[0], expected)
		}
	}
	if err := bridge.app.DecideTaskSuggestion("suggestion.idea", "dismiss"); !errors.Is(err, errTaskSuggestionNotFound) {
		t.Fatalf("legacy suggestion accepted: %v", err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "sources") || strings.Contains(string(raw), "executionPlan") {
		t.Fatalf("internal offer data exposed: %s", raw)
	}
}

func TestTaskSuggestionsUnavailableStoreIsEmpty(t *testing.T) {
	for _, app := range []*App{nil, {}, mainChatTestApp(t)} {
		got, err := app.ListTaskSuggestions()
		raw, _ := json.Marshal(got)
		if err != nil || string(raw) != "[]" {
			t.Fatalf("unavailable = %s, %v", raw, err)
		}
	}
}

func TestDecideTaskSuggestionSharesPhoneDecisions(t *testing.T) {
	for _, id := range []string{"find.find-one"} {
		for _, decision := range []string{"not_now", "dismiss"} {
			t.Run(id+"/"+decision, func(t *testing.T) {
				bridge := mobileTestOfferBridge(t)
				if err := bridge.app.DecideTaskSuggestion(id, decision); err != nil {
					t.Fatal(err)
				}
				got, err := bridge.app.ListTaskSuggestions()
				if err != nil {
					t.Fatal(err)
				}
				for _, offer := range got {
					if offer.ID == id {
						t.Fatalf("decided offer resurfaced: %+v", offer)
					}
				}
				if err := bridge.app.DecideTaskSuggestion(id, decision); !errors.Is(err, errTaskSuggestionNotFound) {
					t.Fatalf("repeat = %v", err)
				}
				if id == "find.find-one" {
					state := bridge.app.topicsSvc().store.load()
					if len(state.Finds.Feedback) != 1 || state.Finds.Feedback[0].Action != "less" {
						t.Fatalf("feedback = %+v", state.Finds.Feedback)
					}
				} else {
					item, err := bridge.app.activity.store.GetInitiative("idea")
					want := InitiativeStatusDeferred
					if decision == "dismiss" {
						want = InitiativeStatusDismissed
					}
					if err != nil || item.Status != want {
						t.Fatalf("decision status: %+v, %v", item, err)
					}
				}
			})
		}
	}
}

func TestDecideTaskSuggestionErrors(t *testing.T) {
	app := &App{}
	for _, tc := range []struct{ id, decision string }{{"", "dismiss"}, {"bad/id", "dismiss"}, {"suggestion.idea", ""}, {"suggestion.idea", "accept"}} {
		if err := app.DecideTaskSuggestion(tc.id, tc.decision); err == nil {
			t.Fatalf("invalid decision accepted: %+v", tc)
		}
	}
	if err := app.DecideTaskSuggestion("suggestion.idea", "dismiss"); !errors.Is(err, errMobileStoreUnavailable) {
		t.Fatalf("unavailable = %v", err)
	}
	bridge := mobileTestOfferBridge(t)
	if err := bridge.app.DecideTaskSuggestion("suggestion.missing", "dismiss"); !errors.Is(err, errTaskSuggestionNotFound) {
		t.Fatalf("missing = %v", err)
	}
	if err := bridge.app.store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bridge.app.decideTaskSuggestion(context.Background(), "suggestion.idea", "dismiss", "", ""); err == nil {
		t.Fatal("closed store error hidden")
	}
}
