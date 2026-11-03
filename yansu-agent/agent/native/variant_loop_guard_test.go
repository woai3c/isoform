package native

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNumericVariantGuardCatchesURLQueryParameterSweep(t *testing.T) {
	counts := map[string]int{}
	values := []string{
		"10_goals",
		"season_10_goals",
		"career_10_goals",
		"season_10+",
		"10",
		"season",
	}
	for i, value := range values {
		input := bashInputForVariantTest(t, "opencli web read --url \"https://fbref.com/en/friv/players-who-played-for-multiple-clubs-countries.fcgi?level=franch&t1=158744&t2="+value+"&t3=--&t4=--\" -f yaml | head -30")
		got := CheckForNumericVariantLoop(counts, "Bash", input)
		if i < NumericVariantLoopThreshold && got.Kind != "ok" {
			t.Fatalf("call %d tripped early: %+v", i+1, got)
		}
		if i == NumericVariantLoopThreshold {
			if got.Kind != "hard" {
				t.Fatalf("call %d did not trip URL query variant guard: %+v", i+1, got)
			}
			if !strings.Contains(got.Family, "t2=%3Cparam%3E") {
				t.Fatalf("family did not normalize query params: %q", got.Family)
			}
			return
		}
		RecordNumericVariantResult(counts, "Bash", input, ContentBlock{
			Type:    "tool_result",
			Content: "Players who played for Crystal Palace FC",
		})
	}
	t.Fatal("URL query variant guard did not trip")
}

func TestNumericVariantGuardAllowsURLQuerySweepWithDifferentResults(t *testing.T) {
	counts := map[string]int{}
	values := []string{
		"159149",
		"169487",
		"170087",
		"177189",
		"184251",
		"187905",
	}
	for i, value := range values {
		input := bashInputForVariantTest(t, "opencli web read --url \"https://fbref.com/en/friv/players-who-played-for-multiple-clubs-countries.fcgi?level=franch&t1=158744&t2="+value+"&t3=--&t4=--\" -f yaml | head -30")
		got := CheckForNumericVariantLoop(counts, "Bash", input)
		if got.Kind != "ok" {
			t.Fatalf("call %d should not trip when results differ: %+v", i+1, got)
		}
		RecordNumericVariantResult(counts, "Bash", input, ContentBlock{
			Type:    "tool_result",
			Content: "Players who played for Crystal Palace FC and team " + value,
		})
	}
}

func bashInputForVariantTest(t *testing.T, command string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
