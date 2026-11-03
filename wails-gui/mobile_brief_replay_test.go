package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMobileBriefReplay replays stored briefs through the brief prompt
// (backlog item 65). It runs only when YANSU_BRIEF_REPLAY_DIR names a folder
// of stored brief JSON files; YANSU_BRIEF_REPLAY_OUT receives, per brief, the
// prompt (<name>.prompt.txt) and, when a model reply sits next to it
// (<name>.reply.json), the text the brief would keep (<name>.result.txt).
// It never calls a model and never reads the app's data folders.
func TestMobileBriefReplay(t *testing.T) {
	dir, out := os.Getenv("YANSU_BRIEF_REPLAY_DIR"), os.Getenv("YANSU_BRIEF_REPLAY_OUT")
	if dir == "" || out == "" {
		t.Skip("set YANSU_BRIEF_REPLAY_DIR and YANSU_BRIEF_REPLAY_OUT to replay stored briefs")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no stored briefs in %s: %v", dir, err)
	}
	const language = "English"
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var brief MobileBriefV1
		if err := json.Unmarshal(raw, &brief); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		brief.Narrative, brief.NarrativeStatus = nil, ""
		_, brief.Offers = mobileBriefClosing(brief, language)
		cards := mobileBriefPatternCards(brief, language)
		picks := mobileBriefPicks(brief)
		profile := (*App)(nil).mobileBriefProfileFor(brief)
		profile.Name = os.Getenv("YANSU_BRIEF_REPLAY_NAME")
		prompt := buildMobileBriefNarrativePrompt(brief, picks, profile, cards, language)
		if err := os.WriteFile(filepath.Join(out, name+".prompt.txt"), []byte(prompt), 0o600); err != nil {
			t.Fatal(err)
		}
		var result strings.Builder
		fmt.Fprintf(&result, "%s %s: %s, day items=%d\n", brief.Slot, brief.LocalDate, mobileBriefPickCounts(picks), len(mobileBriefDayItems(brief)))
		reply, err := os.ReadFile(filepath.Join(out, name+".reply.json"))
		if err == nil {
			paragraphs, _, err := mobileBriefNarrativeFromReply(brief, picks, string(reply), language)
			if err != nil {
				fmt.Fprintf(&result, "check failed: %s (%v)\n", mobileBriefNarrativeRule(err), err)
			}
			for _, paragraph := range paragraphs {
				result.WriteString("\n" + paragraph.Text + "\n")
			}
			fmt.Fprintf(&result, "\nwords=%d\n", mobileBriefNarrativeWords(paragraphs))
		}
		if err := os.WriteFile(filepath.Join(out, name+".result.txt"), []byte(result.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
