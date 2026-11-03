package tools

import (
	"strings"
	"testing"
)

func TestEditMatch_L0_RejectsLinePrefix(t *testing.T) {
	file := "line1\nline2\nline3\n"
	old := "     1\tline1\n     2\tline2"
	_, _, err := FindMatches(file, old)
	if err != ErrLinePrefix {
		t.Errorf("expected ErrLinePrefix, got %v", err)
	}
}

func TestEditMatch_L1_Exact(t *testing.T) {
	file := "hello\nworld\ngoodbye\n"
	ms, lvl, err := FindMatches(file, "world")
	if err != nil {
		t.Fatalf("match failed: %v", err)
	}
	if lvl != MatchL1 {
		t.Errorf("level = %s, want L1", lvl)
	}
	if len(ms) != 1 {
		t.Fatalf("expected 1 match, got %d", len(ms))
	}
	if ms[0].Start != 6 || ms[0].End != 11 {
		t.Errorf("span = [%d, %d), want [6, 11)", ms[0].Start, ms[0].End)
	}
}

func TestEditMatch_L1_Multiple(t *testing.T) {
	file := "foo\nfoo\nfoo\n"
	ms, lvl, err := FindMatches(file, "foo")
	if err != nil {
		t.Fatalf("match failed: %v", err)
	}
	if lvl != MatchL1 {
		t.Errorf("level = %s, want L1", lvl)
	}
	if len(ms) != 3 {
		t.Errorf("matches = %d, want 3", len(ms))
	}
}

func TestEditMatch_L2_CRLF(t *testing.T) {
	file := "alpha\r\nbeta\r\ngamma\r\n"
	old := "alpha\nbeta" // model uses LF
	ms, lvl, err := FindMatches(file, old)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if lvl != MatchL2 {
		t.Errorf("level = %s, want L2", lvl)
	}
	if len(ms) != 1 {
		t.Errorf("got %d matches", len(ms))
	}
}

func TestEditMatch_L3_TrailingWhitespace(t *testing.T) {
	file := "foo\nbar   \nbaz\n"
	old := "foo\nbar\nbaz"
	ms, lvl, err := FindMatches(file, old)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if lvl != MatchL3 {
		t.Errorf("level = %s, want L3", lvl)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d matches", len(ms))
	}
}

func TestEditMatch_L3_BOM(t *testing.T) {
	bom := string([]byte{0xEF, 0xBB, 0xBF})
	file := bom + "foo\nbar\n"
	old := "foo\nbar"
	_, lvl, err := FindMatches(file, old)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	// L1 byte-exact works after BOM because the model didn't include it.
	if lvl != MatchL1 && lvl != MatchL3 {
		t.Errorf("level = %s, want L1 or L3", lvl)
	}
}

func TestEditMatch_L4_Dedent(t *testing.T) {
	// File has lines indented 8 spaces. Model typed old_string with no
	// indent. L4 should dedent old_string, match the body, and return
	// the original indented region so ReindentNewString can reapply it.
	file := "class A {\n        foo()\n        bar()\n}\n"
	old := "foo()\nbar()"
	ms, lvl, err := FindMatches(file, old)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if lvl != MatchL4 {
		t.Errorf("level = %s, want L4", lvl)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d matches", len(ms))
	}
	// ReplaceWith should be the original indented region so we can do
	// the reindent dance.
	if !strings.Contains(ms[0].ReplaceWith, "        foo()") {
		t.Errorf("ReplaceWith missing indent: %q", ms[0].ReplaceWith)
	}
}

func TestEditMatch_L4_Reindent(t *testing.T) {
	file := "class A {\n        foo()\n        bar()\n}\n"
	old := "foo()\nbar()"
	ms, _, err := FindMatches(file, old)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	newStr := "baz()\nqux()"
	reindented := ReindentNewString(newStr, ms[0].ReplaceWith)
	want := "        baz()\n        qux()"
	if reindented != want {
		t.Errorf("reindent = %q, want %q", reindented, want)
	}
}

func TestEditMatch_L5_WhitespaceLoose(t *testing.T) {
	// File has multiple spaces between tokens; model wrote single space.
	file := "func  foo(x  int)  {\n    return  x\n}\n"
	old := "func foo(x int) {"
	ms, lvl, err := FindMatches(file, old)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if lvl != MatchL5 {
		t.Errorf("level = %s, want L5", lvl)
	}
	if len(ms) != 1 {
		t.Errorf("got %d matches", len(ms))
	}
}

func TestEditMatch_L6_AnchorBody(t *testing.T) {
	// File and old_string anchor lines match exactly; middle body has
	// whitespace drift that passes normalizeLineWS.
	file := "begin\n  a = 1\n  b = 2\n  c = 3\nend\n"
	old := "begin\na   =   1\nb = 2\nc = 3\nend"
	_, lvl, err := FindMatches(file, old)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	// May legitimately succeed at L5 (whitespace collapse covers the
	// same input). Both are valid answers; test both.
	if lvl != MatchL5 && lvl != MatchL6 {
		t.Errorf("level = %s, want L5 or L6", lvl)
	}
}

func TestEditMatch_NotFound(t *testing.T) {
	_, _, err := FindMatches("hello world\n", "banana")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestApplyMatch_ExactReplace(t *testing.T) {
	file := "hello\nworld\ngoodbye\n"
	ms, _, err := FindMatches(file, "world")
	if err != nil {
		t.Fatal(err)
	}
	ms[0].ReplaceWith = "planet"
	got := ApplyMatch(file, ms[0])
	want := "hello\nplanet\ngoodbye\n"
	if got != want {
		t.Errorf("apply = %q, want %q", got, want)
	}
}

func TestApplyMatch_L4_WithReindent(t *testing.T) {
	file := "class A {\n        foo()\n        bar()\n}\n"
	old := "foo()\nbar()"
	newStr := "baz()\nqux()"

	ms, lvl, err := FindMatches(file, old)
	if err != nil {
		t.Fatal(err)
	}
	if lvl != MatchL4 {
		t.Fatalf("level = %s, want L4", lvl)
	}
	ms[0].ReplaceWith = ReindentNewString(newStr, ms[0].ReplaceWith)
	got := ApplyMatch(file, ms[0])
	want := "class A {\n        baz()\n        qux()\n}\n"
	if got != want {
		t.Errorf("apply = %q, want %q", got, want)
	}
}

func TestRejectLinePrefix_Partial(t *testing.T) {
	// A mixed input (some lines have prefix, some don't) should NOT be
	// L0-rejected — that's likely a pasted excerpt, not a wholesale
	// cat -n output.
	old := "     1\theader\nno prefix here"
	err := rejectLinePrefix(old)
	if err == ErrLinePrefix {
		t.Error("partial prefix should not trigger L0 rejection")
	}
}

func TestCommonLeadingWhitespace(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"uniform spaces", []string{"    a", "    b"}, "    "},
		{"uniform tabs", []string{"\t\tfoo", "\t\tbar"}, "\t\t"},
		{"mixed", []string{" a", "  b"}, " "},
		{"one zero", []string{"a", "  b"}, ""},
		{"skip blank lines", []string{"    a", "", "    b"}, "    "},
	}
	for _, c := range cases {
		if got := commonLeadingWhitespace(c.lines); got != c.want {
			t.Errorf("%s: commonLeadingWhitespace(%v) = %q, want %q", c.name, c.lines, got, c.want)
		}
	}
}

func TestCollapseWhitespace(t *testing.T) {
	in := "foo   bar\tbaz"
	collapsed, mp := collapseWhitespace(in)
	if collapsed != "foo bar baz" {
		t.Errorf("collapsed = %q, want 'foo bar baz'", collapsed)
	}
	// Position 0 in collapsed should map to position 0 in original.
	if mp[0] != 0 {
		t.Errorf("mp[0] = %d, want 0", mp[0])
	}
	// Position of 'b' (index 4 in collapsed) should map to position of
	// 'b' (index 6) in original.
	if mp[4] != 6 {
		t.Errorf("mp[4] = %d, want 6", mp[4])
	}
}

func TestEditMatch_EOLPreservedInReplace(t *testing.T) {
	// After L2 hits, we normalize the file; we document that the
	// caller should use the normalized file for subsequent work. The
	// test here ensures L2 at least returns a usable match.
	file := "foo\r\nbar\r\n"
	old := "foo\nbar"
	ms, lvl, err := FindMatches(file, old)
	if err != nil {
		t.Fatal(err)
	}
	if lvl != MatchL2 {
		t.Errorf("level = %s, want L2", lvl)
	}
	if len(ms) == 0 {
		t.Fatal("expected at least 1 match")
	}
}
