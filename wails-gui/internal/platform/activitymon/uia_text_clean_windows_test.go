//go:build windows

package activitymon

import (
	"strings"
	"testing"
)

func TestCleanFlatText_StripsPUAIconGlyphs(t *testing.T) {
	// Cursor's file tree exposes the same row twice: once with the
	// Octicon glyph appended (U+EAB6), once without. Both should collapse
	// to a single "notes" entry.
	raw := "notes\nnotes\nroadmap.md\nnotes"
	got := cleanFlatText(raw)
	lines := strings.Split(got, "\n")
	count := 0
	for _, l := range lines {
		if l == "notes" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one 'notes' after cleaning, got %d in: %q", count, got)
	}
	if !strings.Contains(got, "roadmap.md") {
		t.Errorf("unrelated line 'roadmap.md' got dropped: %q", got)
	}
}

func TestCleanFlatText_StripsZeroWidth(t *testing.T) {
	// "示例消息是否完整？" appearing once clean and once with a trailing ZWSP
	// must collapse to one entry. This is the canonical Slack dup pattern
	// the user reported.
	raw := "Priya  3:43 PM\n示例消息是否完整？\nPriya\nhttps://example.com\n示例消息是否完整？​"
	got := cleanFlatText(raw)
	count := strings.Count(got, "示例消息是否完整？")
	if count != 1 {
		t.Fatalf("expected exactly one body line, got %d in: %q", count, got)
	}
}

func TestCleanFlatText_CollapsesIntraLineDuplication(t *testing.T) {
	// Chromium emits self-concatenated Name strings — collapse "X X"
	// patterns in a single line back to "X".
	cases := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "exact halves no separator",
			input:  "article-zero.mdarticle-zero.md",
			expect: "article-zero.md",
		},
		{
			name:   "space separator",
			input:  "Source Control - 1 pending Source Control - 1 pending",
			expect: "Source Control - 1 pending",
		},
		{
			name:   "dash separator",
			input:  "foo-bar-baz-foo-bar-baz",
			expect: "foo-bar-baz",
		},
		{
			name:   "too short to collapse",
			input:  "ok ok",
			expect: "ok ok", // legitimate repetition
		},
		{
			name:   "asymmetric — not duplication",
			input:  "hello hello world",
			expect: "hello hello world",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanFlatText(tc.input)
			if got != tc.expect {
				t.Errorf("\n  input:  %q\n  got:    %q\n  expect: %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestCleanFlatText_DedupsAcrossLargeGap(t *testing.T) {
	// Slack chat with 20+ entries between two identical message bodies.
	// PS window-8 can't catch this; Go window-32 should.
	var b strings.Builder
	b.WriteString("Priya  3:43 PM\n")
	b.WriteString("示例消息是否完整？\n")
	for i := 0; i < 25; i++ {
		b.WriteString("noise-line-")
		b.WriteByte(byte('A' + i))
		b.WriteString("\n")
	}
	b.WriteString("示例消息是否完整？")
	got := cleanFlatText(b.String())
	count := strings.Count(got, "示例消息是否完整？")
	if count != 1 {
		t.Fatalf("expected window-32 dedup to catch 25-apart duplicate, got %d copies", count)
	}
}

func TestCleanFlatText_DropsEmptyLines(t *testing.T) {
	raw := "hello\n\n\nworld\n   \nfoo"
	got := cleanFlatText(raw)
	if strings.Contains(got, "\n\n") {
		t.Errorf("empty lines should be dropped: %q", got)
	}
	for _, want := range []string{"hello", "world", "foo"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing line %q in output: %q", want, got)
		}
	}
}

func TestCleanFlatText_PreservesOrderAndContent(t *testing.T) {
	// Sanity: a clean input without duplication or noise comes through
	// unchanged.
	raw := "Slack\nMessages\nMaya Chen  4:32 PM\n示例回复"
	got := cleanFlatText(raw)
	if got != raw {
		t.Errorf("clean input was modified:\n  in:  %q\n  out: %q", raw, got)
	}
}

func TestCleanFlatText_EmptyInput(t *testing.T) {
	if got := cleanFlatText(""); got != "" {
		t.Errorf("empty input should yield empty output, got %q", got)
	}
}

func TestCleanFlatText_RespectsWindowSizeForLegitimateRepeats(t *testing.T) {
	// A common app phrase that repeats far apart (more than the 32-entry
	// dedup window) should NOT collapse — the second occurrence is
	// genuinely a different element on screen, not a Chromium tree
	// artifact.
	var b strings.Builder
	b.WriteString("Send\n")
	// Generate 40 unique filler lines using counter-based names so they
	// don't dedup against each other (we want to exhaust the window
	// without dropping content).
	for i := 0; i < 40; i++ {
		b.WriteString("filler-")
		// 2-digit suffix keeps strings unique through 40 iterations.
		b.WriteByte(byte('0' + (i / 10)))
		b.WriteByte(byte('0' + (i % 10)))
		b.WriteString("\n")
	}
	b.WriteString("Send")
	got := cleanFlatText(b.String())
	if strings.Count(got, "Send") != 2 {
		t.Errorf("expected 2 'Send' entries 40 apart (past window), got: %q", got)
	}
}

func TestStripIntraLineDup_LongRealisticExample(t *testing.T) {
	in := "Explorer Section: build-your-own-agentic-cli Explorer Section: build-your-own-agentic-cli"
	want := "Explorer Section: build-your-own-agentic-cli"
	got := stripIntraLineDup(in)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNormalizeUIAFragment_StripsButPreservesContent(t *testing.T) {
	// PUA in middle and at end; CJK content must survive.
	in := "你好世界​"
	want := "你好世界"
	got := normalizeUIAFragment(in)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNormalizeUIAFragment_PreservesTabs(t *testing.T) {
	in := "col1\tcol2\tcol3"
	got := normalizeUIAFragment(in)
	if got != in {
		t.Errorf("tabs should survive normalization: got %q", got)
	}
}

func TestStripLeadingTime(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no-prefix", "Sample text", "Sample text"},
		{"slack-followup-cjk", "10:15示例后续消息", "示例后续消息"},
		{"slack-followup-short-hour", "6:34另一条示例消息", "另一条示例消息"},
		{"with-PM-and-space", "10:34 PM sample body", "sample body"},
		{"with-AM-and-space", "8:01 AM sample body", "sample body"},
		{"with-space-only", "10:34 sample body", "sample body"},
		{"time-not-at-start", "Sample 10:15", "Sample 10:15"},
		{"plain-digits", "12345", "12345"},
		{"empty-after-strip", "10:34", ""},
		{"too-short", "12", "12"},
		{"colon-only", ":34", ":34"},
		{"weird-hour-still-stripped", "99:99x", "x"}, // permissive: we don't validate range
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripLeadingTime(tc.in)
			if got != tc.want {
				t.Errorf("stripLeadingTime(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripTrailingReactionDigits(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"cjk-with-1-digit", "示例回复1", "示例回复"},
		{"cjk-with-2-digit", "示例回复12", "示例回复"},
		{"cjk-with-3-digit-untouched", "示例回复123", "示例回复123"},
		{"latin-identifier-untouched", "level1", "level1"},
		{"version-untouched", "v2", "v2"},
		{"step-untouched", "step3", "step3"},
		{"ascii-punct-untouched", "okay!1", "okay!1"}, // ASCII '!' — too risky
		{"ascii-hyphen-untouched", "filler-01", "filler-01"},
		{"no-trailing-digit", "hello", "hello"},
		{"all-digits-untouched", "12", "12"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripTrailingReactionDigits(tc.in)
			if got != tc.want {
				t.Errorf("stripTrailingReactionDigits(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanFlatText_DedupsTimePrefixVariations(t *testing.T) {
	// Synthetic Slack follow-up fixture: "10:15<body>" and "<body>"
	// appearing 1-2 entries apart. Both forms should collapse to a single
	// line, keeping the time-prefixed one (whichever appeared first —
	// preserves more context).
	raw := strings.Join([]string{
		"示例线程的第一条消息",
		"10:15这是一条用于测试的后续消息",
		"https://example.com/p1",
		"这是一条用于测试的后续消息",
		"10:15无法复现的示例",
		"https://example.com/p2",
		"无法复现的示例",
	}, "\n")
	got := cleanFlatText(raw)
	if strings.Count(got, "这是一条用于测试的后续消息") != 1 {
		t.Errorf("expected one synthetic follow-up entry, got: %q", got)
	}
	if strings.Count(got, "无法复现的示例") != 1 {
		t.Errorf("expected one '无法复现的示例' entry, got: %q", got)
	}
	// The kept entry should be the time-prefixed one (came first in walk
	// order). Verify the URL between still survives.
	if !strings.Contains(got, "10:15这是一条用于测试的后续消息") {
		t.Errorf("time-prefixed form should be preserved: %q", got)
	}
}

func TestCleanFlatText_DedupsReactionCount(t *testing.T) {
	// Slack reaction-count pattern: "示例回复1" (body + reaction count) and
	// "示例回复" (body alone) should collapse, keeping whichever came first.
	raw := "Maya Chen  10:25 AM\n示例回复1\nhttps://example.com/p1\n示例回复"
	got := cleanFlatText(raw)
	if strings.Count(got, "示例回复") != 1 {
		t.Errorf("expected one '示例回复' entry, got: %q", got)
	}
}

func TestCleanFlatText_PreservesNonCollidingTimeLines(t *testing.T) {
	// Two different messages that both happen to be sent at 12:34 should
	// NOT collapse — different bodies, different keys.
	raw := "12:34hello world\n12:34goodbye"
	got := cleanFlatText(raw)
	if strings.Count(got, "\n") != 1 {
		t.Errorf("two distinct messages collapsed: %q", got)
	}
}

func TestCleanFlatText_PreservesBareTimestamp(t *testing.T) {
	// A standalone "12:34" line should NOT make every later text body
	// dedup to it. The empty-key-after-strip fallback handles this.
	raw := "12:34\nhello\nworld"
	got := cleanFlatText(raw)
	for _, want := range []string{"12:34", "hello", "world"} {
		if !strings.Contains(got, want) {
			t.Errorf("standalone %q got dropped: %q", want, got)
		}
	}
}

func TestMatchHeaderName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Priya  10:25 AM", "Priya"},
		{"Maya Chen  3:43 PM", "Maya Chen"},
		{"Foo Bar  8:01 AM", "Foo Bar"},
		{"Priya  Today at 3:22 PM", "Priya"}, // Slack date context
		{"Maya Chen  Yesterday at 4:34 PM", "Maya Chen"},
		// Slack actually uses NO-BREAK SPACE between name and time.
		// normalizeUIAFragment folds those to ASCII before matchHeaderName
		// runs, so the matcher only sees ASCII here.
		{"Priya 10:25 AM", ""}, // single space — not the Slack format
		{"Priya  sample", ""},  // tail has no time
		{"sample", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := matchHeaderName(tc.in)
			if got != tc.want {
				t.Errorf("matchHeaderName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMatchHeaderName_NBSP(t *testing.T) {
	// Slack's actual UIA emits NBSP-separated headers. Verify that the
	// full pipeline (normalize -> match) recognizes them as headers.
	const nbsp = " "
	in := "Priya" + nbsp + nbsp + "10:51 AM"
	normalized := normalizeUIAFragment(in)
	if got := matchHeaderName(normalized); got != "Priya" {
		t.Errorf("NBSP-separated header missed: got %q", got)
	}
}

func TestFindTimePattern(t *testing.T) {
	cases := []struct {
		in     string
		expect int // -1 = no match
	}{
		{"hello 10:34 world", 6},
		{"Today at 3:22 PM", 9},
		{"3:45", 0},
		{"3:45 AM", 0},
		{"hi", -1},
		{"version 1.2.3", -1},
		{"p0000000000000000", -1}, // long digit string — not a time
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := findTimePattern(tc.in); got != tc.expect {
				t.Errorf("findTimePattern(%q) = %d, want %d", tc.in, got, tc.expect)
			}
		})
	}
}

func TestMatchCompoundMessage(t *testing.T) {
	cases := []struct {
		in, name, body string
	}{
		{"Priya: sample body. 10:16 AM.", "Priya", "sample body"},
		{"Maya Chen: draft ready. 4:01 PM.", "Maya Chen", "draft ready"},
		{"Name: body. 3:45.", "Name", "body"},
		// negatives — the trailing-time gate must reject these:
		{"Note: this is important", "", ""},
		{"TODO: fix the bug", "", ""},
		{"Re: meeting tomorrow", "", ""},
		{"URL: https://example.com", "", ""},
		// no colon at all:
		{"hello world", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			gotName, gotBody := matchCompoundMessage(tc.in)
			if gotName != tc.name || gotBody != tc.body {
				t.Errorf("matchCompoundMessage(%q) = (%q, %q), want (%q, %q)",
					tc.in, gotName, gotBody, tc.name, tc.body)
			}
		})
	}
}

func TestLooksLikeTime(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"10:25", true},
		{"4:34", true},
		{"10:34 PM", true},
		{"8:01 AM", true},
		{"hello", false},
		{"", false},
		{"10:34 X", false},   // wrong tail
		{"10:34 PMM", false}, // too long
		{"12:345", false},    // too many digits in MM
		{"123:45", false},    // too many digits in HH
		{"10:34 PM extra", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := looksLikeTime(tc.in); got != tc.want {
				t.Errorf("looksLikeTime(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanFlatText_DropsSenderAloneAfterHeader(t *testing.T) {
	// Slack profile-link pattern: "Priya  10:25 AM" header, body, then
	// "Priya" alone as the click target. Drop the standalone name.
	raw := strings.Join([]string{
		"Priya  10:25 AM",
		"请检查这份示例草稿？",
		"Priya",
		"https://example.com/p1",
	}, "\n")
	got := cleanFlatText(raw)
	if strings.Count(got, "Priya") != 1 {
		t.Errorf("expected one 'Priya' (the header), got: %q", got)
	}
	if !strings.Contains(got, "Priya  10:25 AM") {
		t.Errorf("header should be preserved: %q", got)
	}
}

func TestCleanFlatText_DropsCompoundFragmentsAfter(t *testing.T) {
	// "Priya: sample body. 10:16 AM." compound followed by "Priya"
	// and "sample body" as siblings — both fragments should be dropped.
	raw := strings.Join([]string{
		"Priya: sample body. 10:16 AM.",
		"Priya",
		"https://example.com/p1",
		"sample body",
		"has paused their notifications",
	}, "\n")
	got := cleanFlatText(raw)
	if strings.Count(got, "Priya") != 1 {
		t.Errorf("expected one 'Priya' (compound), got: %q", got)
	}
	if strings.Count(got, "sample body") != 1 {
		t.Errorf("expected one 'sample body' (compound), got: %q", got)
	}
	if !strings.Contains(got, "has paused their notifications") {
		t.Errorf("subsequent unrelated content got dropped: %q", got)
	}
}

func TestCleanFlatText_KeepsNoteColonAnnotations(t *testing.T) {
	// "Note: foo" / "TODO: bar" should NOT pollute the dedup set —
	// they don't end with a time, so they're not compound messages.
	raw := strings.Join([]string{
		"Note: handle the case",
		"Note",
		"foo",
		"TODO: fix this",
		"TODO",
		"this",
	}, "\n")
	got := cleanFlatText(raw)
	for _, want := range []string{"Note: handle the case", "Note", "foo", "TODO: fix this", "TODO", "this"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, missing in: %q", want, got)
		}
	}
}

func TestCleanFlatText_MultipleSendersAcrossMessages(t *testing.T) {
	// Multiple legit "Priya  H:MM PM" headers across different messages
	// must each stay (different times → different lines → not duplicate).
	raw := strings.Join([]string{
		"Priya  3:43 PM",
		"sample first",
		"Priya  4:34 PM",
		"sample second",
		"Priya  4:35 PM",
		"sample third",
	}, "\n")
	got := cleanFlatText(raw)
	if strings.Count(got, "Priya  3:43 PM") != 1 ||
		strings.Count(got, "Priya  4:34 PM") != 1 ||
		strings.Count(got, "Priya  4:35 PM") != 1 {
		t.Errorf("legit distinct headers got collapsed: %q", got)
	}
}

func TestSplitTrailingFilename(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		wantBody     string
		wantFilename string
	}{
		{
			name:         "cjk-body-plus-image-png",
			in:           "示例附件说明image.png",
			wantBody:     "示例附件说明",
			wantFilename: "image.png",
		},
		{
			name:         "cjk-body-plus-mac-screenshot",
			in:           "示例截图正文截屏2031-07-26 下午3.12.04.png",
			wantBody:     "示例截图正文",
			wantFilename: "截屏2031-07-26 下午3.12.04.png",
		},
		{
			name:         "english-body-plus-image",
			in:           "sample attachment screenshot.png",
			wantBody:     "sample attachment",
			wantFilename: "screenshot.png",
		},
		{
			name:         "bare-filename-no-body",
			in:           "image.png",
			wantBody:     "",
			wantFilename: "",
		},
		{
			name:         "bare-mac-screenshot-no-body",
			in:           "截屏2031-07-26 下午3.12.04.png",
			wantBody:     "",
			wantFilename: "",
		},
		{
			name:         "no-filename-at-all",
			in:           "just a sample message",
			wantBody:     "",
			wantFilename: "",
		},
		{
			name:         "extension-mid-string",
			in:           "see file.png for sample details",
			wantBody:     "",
			wantFilename: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, filename := splitTrailingFilename(tc.in)
			if body != tc.wantBody || filename != tc.wantFilename {
				t.Errorf("splitTrailingFilename(%q) = (%q, %q), want (%q, %q)",
					tc.in, body, filename, tc.wantBody, tc.wantFilename)
			}
		})
	}
}

func TestSplitTrailingThreadMetadata(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantHead string
		wantLen  int
	}{
		{
			name:     "image-with-thread-summary",
			in:       "image.png 4 repliesLast reply 2 days agoView thread",
			wantHead: "image.png",
			wantLen:  3,
		},
		{
			name:     "no-trailing-thread",
			in:       "just a sample message",
			wantHead: "",
			wantLen:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head, frags := splitTrailingThreadMetadata(tc.in)
			if head != tc.wantHead {
				t.Errorf("head: got %q want %q", head, tc.wantHead)
			}
			if len(frags) != tc.wantLen {
				t.Errorf("fragments: got %d want %d (%v)", len(frags), tc.wantLen, frags)
			}
		})
	}
}

func TestSplitTrailingFilesIndicator(t *testing.T) {
	head, frags := splitTrailingFilesIndicator("示例设计说明2 files Download all")
	if head != "示例设计说明" {
		t.Errorf("head: got %q", head)
	}
	if len(frags) != 2 || frags[0] != "2 files" || frags[1] != "Download all" {
		t.Errorf("fragments wrong: %v", frags)
	}

	// Negative case: no trailing indicator
	h, f := splitTrailingFilesIndicator("plain message")
	if h != "" || f != nil {
		t.Errorf("expected empty for plain message, got (%q, %v)", h, f)
	}
}

func TestCleanFlatText_DropsBodyFilenameCompoundFragments(t *testing.T) {
	// Synthetic Slack-style pattern: a body+filename compound (one Name
	// string with no separator) followed by the body and filename as
	// separate sibling Names. All three should collapse to the compound only.
	raw := strings.Join([]string{
		"Maya Chen  3:28 PM",
		"示例附件说明image.png",
		"https://example.com/p1",
		"示例附件说明",
		"image.png",
		"Toggle file",
	}, "\n")
	got := cleanFlatText(raw)
	if strings.Count(got, "示例附件说明") != 1 {
		t.Errorf("expected one '示例附件说明' line, got: %q", got)
	}
	// "image.png" appears once in the compound. The standalone is dropped.
	if strings.Count(got, "image.png") != 1 {
		t.Errorf("expected one 'image.png' line, got: %q", got)
	}
	if !strings.Contains(got, "Toggle file") {
		t.Errorf("unrelated content got dropped: %q", got)
	}
}

func TestCleanFlatText_DropsThreadMetadataFragments(t *testing.T) {
	raw := strings.Join([]string{
		"Maya Chen  3:26 PM",
		"image.png 4 repliesLast reply 2 days agoView thread",
		"https://example.com/p1",
		"4 replies",
		"Last reply 2 days ago",
		"View thread",
		"More actions",
	}, "\n")
	got := cleanFlatText(raw)
	if strings.Count(got, "4 replies") != 1 {
		t.Errorf("'4 replies' not collapsed: %q", got)
	}
	if strings.Count(got, "View thread") != 1 {
		t.Errorf("'View thread' not collapsed: %q", got)
	}
	if !strings.Contains(got, "More actions") {
		t.Errorf("unrelated content got dropped: %q", got)
	}
}

func TestCleanFlatText_DropsFilesIndicatorFragments(t *testing.T) {
	raw := strings.Join([]string{
		"Alex  3:20 PM",
		"示例设计说明2 files Download all",
		"https://example.com/p1",
		"示例设计说明",
		"2 files",
		"Toggle 2 files",
		"Download all",
	}, "\n")
	got := cleanFlatText(raw)
	lines := strings.Split(got, "\n")
	contains := func(s string) int {
		n := 0
		for _, l := range lines {
			if l == s {
				n++
			}
		}
		return n
	}
	// Body, "2 files", and "Download all" should not appear as
	// standalone lines — they were registered as subsumed by the
	// compound. "Toggle 2 files" stays (different string).
	if contains("示例设计说明") != 0 {
		t.Errorf("body standalone should be dropped, got: %q", got)
	}
	if contains("2 files") != 0 {
		t.Errorf("'2 files' standalone should be dropped, got: %q", got)
	}
	if contains("Download all") != 0 {
		t.Errorf("'Download all' standalone should be dropped, got: %q", got)
	}
	if contains("Toggle 2 files") != 1 {
		t.Errorf("'Toggle 2 files' should survive (different string), got: %q", got)
	}
	// Compound stays.
	if contains("示例设计说明2 files Download all") != 1 {
		t.Errorf("compound should survive, got: %q", got)
	}
}

func TestCleanFlatText_URLLinesAreNotCompounded(t *testing.T) {
	// Slack message-permalink URLs end in p<id>.png shape — must NOT
	// trip the trailingFilenameRE matcher.
	raw := strings.Join([]string{
		"https://files.slack.com/files-pri/T0000000000-F0000000000/image.png",
		"image.png",
	}, "\n")
	got := cleanFlatText(raw)
	if !strings.Contains(got, "files.slack.com") {
		t.Errorf("URL line was dropped: %q", got)
	}
	if !strings.Contains(got, "image.png") {
		t.Errorf("standalone image.png was incorrectly dropped: %q", got)
	}
}

func TestCleanFlatText_DropsFlatCompoundWhenFormattedFollows(t *testing.T) {
	// Synthetic Chromium UIA fixture: the weather forecast appears both
	// as a single super-long Name (no line breaks) AND as individual child
	// Names with proper layout below. The flat version must be dropped;
	// the formatted lines stay.
	flat := "Halcyon 示例天气如下：今天（7月28日）- 气温：20°C ~ 28°C- 天气：晴间多云- 体感：午后体感温度约30°C- 湿度：60%左右明天（7月29日）- 气温：19°C ~ 26°C- 天气：微风需要我帮你查一下 Halcyon 的示例景点吗？"
	raw := strings.Join([]string{
		"yansuAPP  2 minutes ago",
		flat,
		"yansu",
		"APP",
		"https://example.com/p1",
		"Halcyon 示例天气如下：",
		"今天（7月28日）",
		"- 气温：20°C ~ 28°C",
		"- 天气：晴间多云",
		"- 体感：午后体感温度约30°C",
		"- 湿度：60%左右",
		"明天（7月29日）",
		"- 气温：19°C ~ 26°C",
		"- 天气：微风",
		"需要我帮你查一下 Halcyon 的示例景点吗？",
		"Also send as direct message",
	}, "\n")
	got := cleanFlatText(raw)
	if strings.Contains(got, flat) {
		t.Errorf("flat compound should be dropped, still present in:\n%s", got)
	}
	for _, want := range []string{
		"Halcyon 示例天气如下：",
		"今天（7月28日）",
		"明天（7月29日）",
		"需要我帮你查一下 Halcyon 的示例景点吗？",
		"Also send as direct message",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted line %q got dropped: %s", want, got)
		}
	}
}

func TestCleanFlatText_KeepsUniqueLongLine(t *testing.T) {
	// A long line whose content does NOT reappear in following lines
	// must NOT be dropped — there is no flattened-dup to suppress.
	// Built from diverse content so stripIntraLineDup doesn't halve it.
	uniqueLong := "第一段说明文字内容相当多但是是一句话叙述某件事情，紧接着第二段补充内容，然后第三段进一步阐述细节情况，最后第四段做出总结性的论述。"
	raw := strings.Join([]string{
		"Header",
		uniqueLong,
		"Unrelated line one",
		"Unrelated line two",
		"Unrelated line three",
	}, "\n")
	got := cleanFlatText(raw)
	if !strings.Contains(got, uniqueLong) {
		t.Errorf("unique long line was wrongly dropped: %s", got)
	}
}

func TestCleanFlatText_KeepsLongURL(t *testing.T) {
	// A long URL that ends with a recognizable extension would otherwise
	// also pass the length threshold — the URL gate must protect it.
	longURL := "https://very.long.example.com/" + strings.Repeat("path/", 30) + "image.png"
	raw := strings.Join([]string{
		"Some header",
		longURL,
		"image.png",
		"path",
		"path",
	}, "\n")
	got := cleanFlatText(raw)
	if !strings.Contains(got, longURL) {
		t.Errorf("URL got wrongly dropped: %s", got)
	}
}

func TestStripAllWhitespace(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello world", "helloworld"},
		{"  spaced  out  ", "spacedout"},
		{"line1\nline2", "line1line2"},
		{"tab\there", "tabhere"},
		{"中文 空格", "中文空格"},
		{"全角　空格", "全角空格"}, // U+3000 IDEOGRAPHIC SPACE
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripAllWhitespace(tc.in); got != tc.want {
			t.Errorf("stripAllWhitespace(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
