package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/storage"
)

// TestInterestsIntegration runs one interests pass on a copy of a real
// data folder (activity.db and memory/) with the signed-in Claude Code and
// no tools at all. It costs a few cents of the user's Claude allowance and
// reads the user's own summaries, so it runs only when pointed at a copy:
//
//	cd wails-gui && YANSU_INTERESTS_DATA=/path/to/copy YANSU_INTERESTS_MODEL=haiku go test -run TestInterestsIntegration -count=1 -v .
//
// The folder is opened read-write (migrations), so never point it at the
// live data folder.
func TestInterestsIntegration(t *testing.T) {
	dir := os.Getenv("YANSU_INTERESTS_DATA")
	if dir == "" {
		t.Skip("set YANSU_INTERESTS_DATA to a copy of a data folder to run against the real Claude Code")
	}
	model := os.Getenv("YANSU_INTERESTS_MODEL")
	if model == "" {
		model = "haiku"
	}
	store, err := NewActivityStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	// The pause log, the calendar, the chats and the people list are in
	// sessions.db.
	if _, err := os.Stat(filepath.Join(dir, "sessions.db")); err == nil {
		sessions, err := storage.New(filepath.Join(dir, "sessions.db"))
		if err != nil {
			t.Fatal(err)
		}
		app.store = sessions
	}
	memories := interestTestMemories(t, filepath.Join(dir, "memory"))
	// The CLI keeps the real home folder, where its sign-in is; the app's
	// own reads of the home folder go to a temporary one. TestMain moved
	// HOME aside for this run, so the real home is what TestMain captured
	// before the switch.
	realHome := testRealHomeDir
	if realHome == "" {
		realHome, _ = os.UserHomeDir()
	}
	if realHome == "" {
		t.Fatal("the real home directory is unknown; cannot sign the CLI in")
	}
	t.Setenv("HOME", t.TempDir())

	svc := &interestsService{
		store: newInterestsStore(t.TempDir()),
		now:   time.Now,
		collect: func(from, now time.Time) interestInput {
			in := collectInterestInput(app, from, now)
			in.Memories = memories
			items, _ := interestItems(in)
			counts := map[interests.Source]int{}
			for _, it := range items {
				counts[it.Source]++
			}
			t.Logf("evidence by source: %v (pause log unreadable: %v)", counts, in.PausesUnknown)
			return in
		},
		allowed: func() bool { return true },
		model: func(ctx context.Context, prompt, model string) (string, error) {
			cmd := exec.CommandContext(ctx, "claude", "-p", "--safe-mode", "--tools", "",
				"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--no-session-persistence",
				"--output-format", "json", "--model", model, prompt)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "HOME="+realHome)
			out, err := cmd.Output()
			if err != nil {
				return "", err
			}
			var res struct {
				Result string  `json:"result"`
				Cost   float64 `json:"total_cost_usd"`
			}
			if err := json.Unmarshal(out, &res); err != nil {
				return "", err
			}
			t.Logf("%s: %d prompt runes, $%.4f", model, len([]rune(prompt)), res.Cost)
			return res.Result, nil
		},
		lightModel: func() string { return model },
		mainModel:  func() string { return model },
		user:       func() interestsUserState { return interestsUserState{Pinned: map[string]bool{}} },
		language:   func() string { return "English" },
	}
	if !svc.maybeRun(context.Background(), "integration") {
		t.Fatal("no pass ran")
	}
	list := svc.list()
	if len(list) < 3 {
		t.Fatalf("only %d interests", len(list))
	}
	for _, it := range list {
		stats := it.Stats(time.Local)
		t.Logf("[%s] %s (%.2f, score %.2f): %d items, %.0f min, %d days, %v :: %s :: %q",
			it.Kind, it.Name, it.Confidence, it.Score, stats.Items, stats.Minutes, stats.Days, stats.Sources, it.Why, it.Queries)
		for _, text := range append([]string{it.Name}, it.Queries...) {
			for _, own := range interestsOwnNames {
				if strings.Contains(strings.ToLower(text), strings.ToLower(own)) {
					t.Errorf("%q carries the app's name", text)
				}
			}
		}
	}
}

// interestTestMemories reads About you entries the way GetMemoryEntries
// does, from another folder.
func interestTestMemories(t *testing.T, dir string) []MemoryEntry {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	var out []MemoryEntry
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(raw)
		fm := parseMemoryFrontmatter(content)
		out = append(out, MemoryEntry{
			ID: strings.TrimSuffix(filepath.Base(path), ".md"), Title: extractTitle(content), Content: content,
			Dimension: extractMemoryDimension(content), UpdatedAt: fm.UpdatedAt, Apps: fm.Apps,
		})
	}
	return out
}
