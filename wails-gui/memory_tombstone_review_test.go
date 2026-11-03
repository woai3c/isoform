package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryTombstoneReviewScopeSeparatesFactsFromProvenance(t *testing.T) {
	original := "# Editor preferences\n\n- **Source**: activity\n- **Activity Session**: shared-session\n\n## Memory Points\n\n- Prefers Go over Rust.\n- Uses a quiet workspace.\n"
	tests := []struct {
		name, candidate string
		wantForgotten   bool
	}{
		{"forgotten point in new record", "# Build workflow\n\n- **Source**: activity\n\n## Memory Points\n\n- Prefers Go over Rust.\n- Deploys from a dedicated staging environment.\n", true},
		{"shared session", "# Calendar habit\n\n- **Source**: activity\n- **Activity Session**: shared-session\n\n## Memory Points\n\n- Reviews the calendar each morning.\n", false},
		{"explicit user edit", "# Editor preferences\n\n- **Source**: user_edit\n\n## Memory Points\n\n- Prefers Go over Rust.\n- Uses a quiet workspace.\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now()
			if err := recordMemoryTombstone(dir, "forgotten", []string{original}, now); err != nil {
				t.Fatal(err)
			}
			forgotten, err := isMemoryForgotten(dir, "another-record", tt.candidate, now)
			if err != nil || forgotten != tt.wantForgotten {
				t.Fatalf("fact suppression=%v err=%v want=%v", forgotten, err, tt.wantForgotten)
			}
		})
	}
}

func TestMemoryTombstoneReviewDoesNotPoisonMergedParents(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	synthesis := "# Combined insight\n\n- **Source**: dream\n- **MergedFrom**: parent-a, parent-b\n\nCombined private observations.\n"
	if err := recordMemoryTombstone(dir, "synthesis", []string{synthesis}, now); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{"parent-a", "parent-b"} {
		forgotten, err := isMemoryForgotten(dir, parent, "# Source\n\nAn independently retained observation.", now)
		if err != nil || forgotten {
			t.Fatalf("unremoved source %s suppressed: forgotten=%v err=%v", parent, forgotten, err)
		}
	}
}

func TestMemoryTombstoneReviewPreservesUnselectedExistingRecord(t *testing.T) {
	app, dir := newForgetTestApp(t)
	content := "# Shared preference\n\n- **Source**: activity\n\n## Memory Points\n\n- Prefers Go over Rust.\n"
	if _, err := app.saveMemoryEntry("retained", content, false); err != nil {
		t.Fatal(err)
	}
	if err := recordMemoryTombstone(dir, "forgotten", []string{content}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := checkMemoryWriteAllowed(dir, "retained", content, true); err != nil {
		t.Fatalf("unselected existing record at %s suppressed: %v", filepath.Join(dir, "retained.md"), err)
	}
}

// Unit-test adapter for general forget-flow fixtures. It is deliberately lexical;
// semantic-policy tests below supply their own structured model responses.
func configureTestMemoryCanonicalizer(t *testing.T, app *App) {
	t.Helper()
	app.memoryCanonicalizer = func(prompt, model string) (string, error) {
		_, input, ok := strings.Cut(prompt, "Record JSON string:\n")
		if !ok {
			t.Fatal("canonicalization prompt missing record JSON")
		}
		var record string
		if err := json.Unmarshal([]byte(input), &record); err != nil {
			return "", err
		}
		response := memorySemanticResponse{Version: memorySemanticVersion, Facts: []memorySemanticFact{{Subject: "self", Relation: "record", Value: memoryForgetHash(record), Polarity: "positive", Qualifiers: []string{}}}}
		data, err := json.Marshal(response)
		return string(data), err
	}
}

func TestMemoryTombstoneReviewLegacyMarkersExpireWithoutPermanentBlock(t *testing.T) {
	dir := t.TempDir()
	for _, expired := range []bool{false, true} {
		expiry := time.Now().Add(time.Hour)
		if expired {
			expiry = time.Now().Add(-time.Second)
		}
		data, _ := json.Marshal([]memoryTombstone{{Version: 0, Keys: []string{memoryForgetIDKey("legacy-parent")}, ExpiresAt: expiry.UnixMilli()}})
		if err := os.WriteFile(filepath.Join(dir, memoryTombstonesFile), data, 0600); err != nil {
			t.Fatal(err)
		}
		forgotten, err := isMemoryForgotten(dir, "unrelated", "# Unrelated", time.Now())
		if err != nil || forgotten {
			t.Fatalf("legacy marker blocks an unrelated record: forgotten=%v err=%v", forgotten, err)
		}
		forgotten, err = isMemoryForgotten(dir, "legacy-parent", "# Legacy record", time.Now())
		if err != nil || forgotten == expired {
			t.Fatalf("legacy marker lost its original expiry: forgotten=%v expired=%v err=%v", forgotten, expired, err)
		}
	}
}

func TestMemoryTombstoneReviewLateVersionReferencesRespectCorrection(t *testing.T) {
	app, dir := newForgetTestApp(t)
	old := "# Insight\n\n- **Source**: dream\n\nA removed private fact.\n"
	current := "# Insight\n\n- **Source**: user_edit\n\nThe user's independent correction.\n"
	if err := os.WriteFile(filepath.Join(dir, "source.md"), []byte(current), 0600); err != nil {
		t.Fatal(err)
	}
	prepared := memoryForgetSemantics{Version: memorySemanticVersion, Model: effectiveLightTaskModel(app), RemovedVersions: []string{memoryForgetHistoricalSelector("source", old)}}
	if err := recordMemoryTombstone(dir, "removed-root", []string{"# Removed root"}, time.Now(), prepared); err != nil {
		t.Fatal(err)
	}
	oldRef := "memory:source:1:" + taskContextHash(strings.TrimSpace(old))[:16]
	currentRef := "memory:source:2:" + taskContextHash(strings.TrimSpace(current))[:16]
	for _, tt := range []struct {
		name    string
		refs    []string
		blocked bool
	}{
		{"old version", []string{oldRef}, true},
		{"current version", []string{currentRef}, false},
		{"legacy ambiguous", nil, true},
		{"qualified current plus bare", []string{currentRef, "source"}, false},
		{"old plus current", []string{oldRef, currentRef}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			encoded, _ := json.Marshal(tt.refs)
			content := "# Late derived observation\n\n- **Source**: dream\n- **MergedFrom**: source\n- **MergedFromRefs**: " + string(encoded) + "\n\nA synthesized observation.\n"
			_, err := app.saveMemoryEntry("late-result", content, false)
			if tt.blocked && !errors.Is(err, errMemoryForgotten) {
				t.Fatalf("erased reference accepted: %v", err)
			}
			if !tt.blocked && err != nil {
				t.Fatalf("current corrected reference blocked: %v", err)
			}
		})
	}
	forgotten, err := isMemoryForgotten(dir, "source", old, time.Now())
	if err != nil || !forgotten {
		t.Fatalf("erased old version can be restored: %v, %v", forgotten, err)
	}
	forgotten, err = isMemoryForgotten(dir, "source", current, time.Now())
	if err != nil || forgotten {
		t.Fatalf("current correction blocked: %v, %v", forgotten, err)
	}
	forgotten, err = isMemoryHistoricalReferenceForgotten(dir, "source", []string{oldRef}, time.Now().Add(31*24*time.Hour))
	if err != nil || forgotten {
		t.Fatalf("version guard did not expire: %v, %v", forgotten, err)
	}
}
