package native

import (
	"strings"
	"testing"
)

func TestBashSearchNarrowingFamily_GroupsAppendedGrepFilters(t *testing.T) {
	first := bashInput(`find /repo -maxdepth 6 -name "*.go" 2>/dev/null | xargs grep -l "sub2api" 2>/dev/null | xargs grep -l "usage" 2>/dev/null | head -20`)
	second := bashInput(`find /repo -maxdepth 6 -name "*.go" 2>/dev/null | xargs grep -l "sub2api" 2>/dev/null | xargs grep -l "usage" 2>/dev/null | xargs grep -l "latency" 2>/dev/null | head -20`)

	familyA, filtersA, okA := bashSearchNarrowingFamily("Bash", first)
	familyB, filtersB, okB := bashSearchNarrowingFamily("Bash", second)
	if !okA || !okB {
		t.Fatalf("expected both commands to be recognised as search narrowing, okA=%v okB=%v", okA, okB)
	}
	if familyA != familyB {
		t.Fatalf("families differ:\nA=%s\nB=%s", familyA, familyB)
	}
	if filtersA != 2 || filtersB != 3 {
		t.Fatalf("filter counts = %d/%d, want 2/3", filtersA, filtersB)
	}
	if !strings.Contains(familyA, "find /repo -maxdepth 6 -name \"*.go\"") {
		t.Fatalf("family missing normalized base: %s", familyA)
	}
}

func TestSearchNarrowingGuardTripsAfterEmptyResults(t *testing.T) {
	states := map[string]SearchNarrowingLoopState{}
	for i := 0; i < SearchNarrowingLoopThreshold; i++ {
		cmd := bashInput(`find /repo -maxdepth 6 -name "*.go" | xargs grep -l "sub2api" | xargs grep -l "` + string(rune('a'+i)) + `"`)
		got := CheckForSearchNarrowingLoop(states, "Bash", cmd)
		if got.Kind != "ok" {
			t.Fatalf("call %d kind=%s, want ok", i+1, got.Kind)
		}
		RecordSearchNarrowingResult(states, "Bash", cmd, ContentBlock{Type: "tool_result", Content: ""})
	}

	next := bashInput(`find /repo -maxdepth 6 -name "*.go" | xargs grep -l "sub2api" | xargs grep -l "z" | xargs grep -l "latency"`)
	got := CheckForSearchNarrowingLoop(states, "Bash", next)
	if got.Kind != "hard" {
		t.Fatalf("kind=%s, want hard", got.Kind)
	}
	if got.Count != SearchNarrowingLoopThreshold+1 {
		t.Fatalf("count=%d, want %d", got.Count, SearchNarrowingLoopThreshold+1)
	}
}

func TestSearchNarrowingGuardResetsOnNonEmptyResult(t *testing.T) {
	states := map[string]SearchNarrowingLoopState{}
	cmd := bashInput(`find /repo -maxdepth 6 -name "*.go" | xargs grep -l "sub2api" | xargs grep -l "usage"`)
	for i := 0; i < SearchNarrowingLoopThreshold; i++ {
		RecordSearchNarrowingResult(states, "Bash", cmd, ContentBlock{Type: "tool_result", Content: ""})
	}
	RecordSearchNarrowingResult(states, "Bash", cmd, ContentBlock{Type: "tool_result", Content: "internal/foo.go\n"})

	got := CheckForSearchNarrowingLoop(states, "Bash", cmd)
	if got.Kind != "ok" || got.Count != 1 {
		t.Fatalf("after non-empty reset kind=%s count=%d, want ok/1", got.Kind, got.Count)
	}
}

func TestSearchNarrowingGuardDoesNotMatchSingleGrep(t *testing.T) {
	input := bashInput(`grep -R "sub2api" /repo`)
	_, _, ok := bashSearchNarrowingFamily("Bash", input)
	if ok {
		t.Fatal("single grep should not be treated as narrowing pipeline")
	}
}

func bashInput(command string) string {
	return `{"command":` + quoteJSONString(command) + `}`
}

func quoteJSONString(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(s) + `"`
}
