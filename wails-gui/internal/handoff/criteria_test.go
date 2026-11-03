package handoff

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCriteriaFromBodyReadsTheAcceptanceSection(t *testing.T) {
	authored := "# Objective\n\nWrite the outline.\n\n" +
		"# Plan\n\n1. Read the brief\n\n" +
		"# Acceptance criteria\n" +
		"- The outline exists at /Users/x/Docs/q3-outline.md\n" +
		"* [ ] Nothing was sent to Sam\n" +
		"3) Each section has a heading\n\n" +
		"# Open questions\n- Which deck?\n"
	got := CriteriaFromBody(authored)
	want := []string{
		"The outline exists at /Users/x/Docs/q3-outline.md",
		"Nothing was sent to Sam",
		"Each section has a heading",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("criteria = %#v, want %#v", got, want)
	}
}

func TestCriteriaFromBodyAcceptsEquivalentHeadings(t *testing.T) {
	cases := []struct {
		name    string
		heading string
	}{
		{"lowercase", "## acceptance criteria"},
		{"success", "### Success criteria"},
		{"how to verify", "## How to verify"},
		{"trailing colon", "# Verification:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CriteriaFromBody(c.heading + "\n- The report is saved\n")
			if len(got) != 1 || got[0] != "The report is saved" {
				t.Fatalf("criteria under %q = %#v", c.heading, got)
			}
		})
	}
}

func TestCriteriaFromBodyIsTolerant(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"no section", "# Objective\n\n- a bullet\n", nil},
		{"empty section", "# Acceptance criteria\n\n# Open questions\n- x\n", nil},
		{"crlf line endings", "# Acceptance criteria\r\n- one\r\n- two\r\n", []string{"one", "two"}},
		{"prose line counts", "# Acceptance criteria\nThe file exists.\n", []string{"The file exists."}},
		{"empty", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CriteriaFromBody(c.body); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("criteria = %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestCriteriaFromBodyBoundsCountAndLength(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Acceptance criteria\n")
	for i := 0; i < maxCriteria+5; i++ {
		b.WriteString("- criterion\n")
	}
	if got := CriteriaFromBody(b.String()); len(got) != maxCriteria {
		t.Fatalf("kept %d criteria, want %d", len(got), maxCriteria)
	}

	long := strings.Repeat("é", maxCriterionRunes+10)
	got := CriteriaFromBody("# Acceptance criteria\n- " + long + "\n")
	if len(got) != 1 || !strings.HasSuffix(got[0], "...") || len([]rune(got[0])) != maxCriterionRunes+3 {
		t.Fatalf("long criterion not bounded: %d runes", len([]rune(got[0])))
	}
}

func TestCriteriaRoundTripThroughTheFile(t *testing.T) {
	s := newTestStore(t)
	s.Now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }
	entry := Entry{
		ID:           "hf_verified",
		Title:        "Outline",
		Status:       StatusNeedsReview,
		Body:         "# Acceptance criteria\n- The outline exists\n",
		Criteria:     []string{"The outline exists", "Quote \"Sam\": yes"},
		Verification: &Verification{Met: 1, Total: 2, Note: "The outline is missing\nfrom the folder"},
	}
	if _, err := s.Save(entry); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("hf_verified")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusNeedsReview {
		t.Fatalf("status = %q", got.Status)
	}
	if !reflect.DeepEqual(got.Criteria, entry.Criteria) {
		t.Fatalf("criteria = %#v", got.Criteria)
	}
	if got.Verification == nil || got.Verification.Met != 1 || got.Verification.Total != 2 ||
		got.Verification.Note != entry.Verification.Note {
		t.Fatalf("verification = %+v", got.Verification)
	}
}

func TestLegacyFilesReadWithoutCriteriaOrVerification(t *testing.T) {
	legacy := "---\nid: hf_old\ntitle: Old\nstatus: completed\nsource: triage\n---\n\nbody\n"
	e := Parse("hf_old", legacy)
	if e.Status != StatusCompleted || len(e.Criteria) != 0 || e.Verification != nil {
		t.Fatalf("legacy entry = %+v", e)
	}
	// A malformed criteria or verification value is ignored, not fatal.
	broken := "---\nid: hf_broken\ntitle: Broken\nstatus: needs_review\ncriteria: not json\nverification: {oops\n---\n\nbody\n"
	e = Parse("hf_broken", broken)
	if e.Status != StatusNeedsReview || e.Criteria != nil || e.Verification != nil {
		t.Fatalf("broken entry = %+v", e)
	}
}
