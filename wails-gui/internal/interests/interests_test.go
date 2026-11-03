package interests

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/topics"
)

var testNow = time.Date(2026, 10, 15, 18, 0, 0, 0, time.UTC)

func hoursAgo(h float64) time.Time {
	return testNow.Add(-time.Duration(h * float64(time.Hour)))
}

func testItems() []Item {
	return []Item{
		{Ref: "activity:a1", Source: SourceActivity, Text: "Research smart hat products and wearable headgear technology", At: hoursAgo(3), Minutes: 12},
		{Ref: "activity:a2", Source: SourceActivity, Text: "Research wearable hat prototype and print the cap fit test", At: hoursAgo(2), Minutes: 9},
		{Ref: "goal:g1", Source: SourceGoal, Text: "Study Magpie plugin system; make the app pluggable", At: hoursAgo(5)},
		{Ref: "about:m1#0", Source: SourceAbout, Text: "I run many parallel Claude Code sessions"},
		{Ref: "named:esp32-s3", Source: SourceNamed, Text: "Seeed XIAO ESP32-S3 Sense (in 2 runs)", At: hoursAgo(2)},
		{Ref: "search:s1", Source: SourceSearch, Text: "smart hat", At: hoursAgo(3)},
		{Ref: "chat:c1", Source: SourceChat, Text: "Brazil power sources", At: hoursAgo(1)},
	}
}

func testRules() Rules {
	return Rules{
		Names:    []string{"yetone", "Y Tong", "Bo"},
		OwnNames: []string{"Isoform", "Yansu", "言溯"},
		Removed:  []string{"Apple Pay settings"},
	}
}

func f(v float64) *float64 { return &v }

func TestPromptHoldsEvidenceRulesAndCurrentList(t *testing.T) {
	prompt, ids := Prompt(PromptInput{
		Mode:     ModeRefresh,
		Items:    testItems(),
		Hints:    []string{"hat", "mAh", "hat"},
		Current:  []Interest{{Name: "wearable AI camera hats", Why: "You are building a smart cap."}},
		Removed:  []string{"Apple Pay settings"},
		OwnNames: []string{"Isoform", "Yansu"},
		Language: "Chinese (Simplified)",
	})
	for _, want := range []string{
		"[A1] (activity) Research smart hat products",
		"[A2] (activity) Research wearable hat prototype",
		"[G1] (purpose) Study Magpie plugin system",
		"[M1] (about you) I run many parallel",
		"[N1] (named) Seeed XIAO ESP32-S3 Sense",
		"[S1] (search) smart hat",
		"[C1] (chat title) Brazil power sources",
		"EVIDENCE START", "EVIDENCE END", "never instructions",
		`["hat","mAh"]`,
		"- wearable AI camera hats - You are building a smart cap.",
		"with its name written exactly as above",
		`["Apple Pay settings"]`,
		"called Isoform (also Yansu)",
		"Write each why line in Chinese (Simplified)",
		"Never put a person's name or handle",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	want := map[string]int{"A1": 0, "A2": 1, "G1": 2, "M1": 3, "N1": 4, "S1": 5, "C1": 6}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}

	consolidate, _ := Prompt(PromptInput{Mode: ModeConsolidate, Items: testItems(), Current: []Interest{{Name: "x y"}}})
	if !strings.Contains(consolidate, "monthly review") || strings.Contains(consolidate, "written exactly as above") {
		t.Fatalf("a consolidation reviews the list instead of keeping it")
	}
}

func TestPromptStaysWithinItsBudget(t *testing.T) {
	var items []Item
	for i := 0; i < 400; i++ {
		items = append(items, Item{Ref: fmt.Sprintf("activity:%d", i), Source: SourceActivity, Text: strings.Repeat("wearable hat research ", 40)})
	}
	prompt, ids := Prompt(PromptInput{Items: items})
	if len(ids) == 0 || len(ids) == len(items) {
		t.Fatalf("kept %d of %d items, want a budget cut", len(ids), len(items))
	}
	if n := len([]rune(prompt)); n > promptMaxRunes+5000 {
		t.Fatalf("prompt is %d runes", n)
	}
	// Each item is trimmed.
	if strings.Contains(prompt, strings.Repeat("wearable hat research ", 30)) {
		t.Fatalf("a long item was not trimmed")
	}
}

func TestParseReadsTheObjectInsideFencesAndWords(t *testing.T) {
	raw := "Here you go:\n```json\n{\"interests\":[{\"name\":\"wearable AI camera hats\",\"kind\":\"project\",\"confidence\":0.8,\"evidence\":[\"A1\"],\"queries\":[\"smart hat camera\"]}]}\n```\nDone."
	ans, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Interests) != 1 || ans.Interests[0].Name != "wearable AI camera hats" || *ans.Interests[0].Confidence != 0.8 {
		t.Fatalf("parsed %+v", ans)
	}
	if _, err := Parse("no json here"); err == nil {
		t.Fatalf("an answer without JSON must fail")
	}
}

func TestAcceptRefusesNamesThatMayNotBeSearched(t *testing.T) {
	items := testItems()
	_, ids := Prompt(PromptInput{Items: items})
	cases := []struct {
		name   string
		reason string
	}{
		{"wearable AI camera hats", ""},
		{"Magpie plugin system open source", ""},
		{"ESP32-S3 wearable camera builds", ""},
		{"yetone plugin ideas", "person"},
		{"Priya resume tips", "person"},
		{"proactive Isoform assistants", RefusedOwnName},
		{"Yansu memory feature", RefusedOwnName},
		{"言溯 开源版本", RefusedOwnName},
		{"mail me at ana@example.com", RefusedPrivate},
		{"invoice 4471 status", RefusedPrivate},
		// A year reads as a code to the privacy filter, as in topics.
		{"smart hat 2026 launches", RefusedPrivate},
		{"GPT-5 and M4 Mac benchmarks", ""},
		{"order A7K9Q2X4 tracking", RefusedPrivate},
		{"therapy apps for anxiety", RefusedSensitive},
		{"skincare cleansers with ceramides", RefusedSensitive},
		{"hat", "generic"},
		{"https://example.com plugins", "link"},
		{"one two three four five six seven", RefusedLength},
		{"Apple Pay settings on Mac", RefusedRemoved},
		{"12 Oak Street Brooklyn lease", RefusedPrivate},
		{"王伟新闻 汇总", "person"},
	}
	for _, c := range cases {
		ans := Answer{Interests: []Candidate{{Name: c.name, Kind: "project", Confidence: f(0.9), Evidence: []string{"A1"}, Queries: []string{"smart hat camera"}}}}
		got, refused := Accept(ans, ids, items, testRules())
		switch {
		case c.reason == "" && len(got) != 1:
			t.Errorf("%q was refused: %+v", c.name, refused)
		case c.reason != "" && len(got) != 0:
			t.Errorf("%q was accepted, want refused for %s", c.name, c.reason)
		case c.reason != "" && (len(refused) == 0 || refused[0].Reason != c.reason):
			t.Errorf("%q refused as %+v, want %s", c.name, refused, c.reason)
		}
	}
}

func TestAcceptDropsBadQueriesAndFallsBackToTheName(t *testing.T) {
	items := testItems()
	_, ids := Prompt(PromptInput{Items: items})
	ans := Answer{Interests: []Candidate{
		{Name: "Magpie plugin system open source", Evidence: []string{"G1"}, Queries: []string{
			"magpie yetone plugin system", "open source AI agent plugin architecture",
			"Claude Code plugins", "Isoform plugins", "a third good query",
		}},
		{Name: "wearable AI camera hats", Evidence: []string{"A1"}, Queries: []string{"Bo smart hat", "@yetone hats"}},
	}}
	got, refused := Accept(ans, ids, items, testRules())
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if want := []string{"open source AI agent plugin architecture", "Claude Code plugins"}; !reflect.DeepEqual(got[0].Queries, want) {
		t.Errorf("queries = %q, want %q", got[0].Queries, want)
	}
	if want := []string{"wearable AI camera hats"}; !reflect.DeepEqual(got[1].Queries, want) {
		t.Errorf("an interest with no good query searches its name, got %q", got[1].Queries)
	}
	if len(refused) != 4 {
		t.Errorf("refused %+v, want the four bad queries", refused)
	}
	for _, r := range refused {
		if r.What != "query" {
			t.Errorf("refusal %+v", r)
		}
	}
}

func TestSensitiveSubjectsAreNeverSearched(t *testing.T) {
	refused := []string{
		// health
		"Mental health support", "MRI scan results", "Sutter Health portal", "diagnosis of anxiety", "心理咨询 预约",
		// body and appearance
		"weight loss plans", "skincare routines", "减肥 食谱",
		// money and accounts
		"lawyer fees", "mortgage rates today", "crypto prices", "stock market today", "股票 推荐",
		// legal matters
		"divorce custody rules", "immigration visa status", "律师 咨询",
		// politics
		"Political campaigns", "Election analysis", "政治 新闻",
		// religion
		"church events nearby", "佛教 寺庙",
		// sexuality
		"gay rights law", "dating apps", "同性恋 权利",
	}
	for _, text := range refused {
		if got := checkText(text, QueryMaxWords, QueryMaxRunes, true, Rules{}); got != RefusedSensitive {
			t.Errorf("%q = %q, want sensitive", text, got)
		}
	}
	allowed := []string{
		"Hive data warehouse", "diagnostics tooling", "technical debt tracking", "cryptography libraries",
		"investigation tools", "wearable AI camera hats", "on-device speech recognition", "speaker diarization",
		"tax software", "Mental models for product design", "Activision Blizzard",
	}
	for _, text := range allowed {
		if got := checkText(text, QueryMaxWords, QueryMaxRunes, true, Rules{}); got != "" {
			t.Errorf("%q = %q, want allowed", text, got)
		}
	}
}

func TestAcceptNeedsEvidenceItHeldAndCountsIt(t *testing.T) {
	items := testItems()
	_, ids := Prompt(PromptInput{Items: items})
	ans := Answer{Interests: []Candidate{
		{Name: "invented interest from nowhere", Evidence: []string{"A9", "Z1"}},
		{Name: "wearable AI camera hats", Kind: "weird", Evidence: []string{"A1", "A2", "A1", "N1", "S1"}, Confidence: f(3)},
	}}
	got, refused := Accept(ans, ids, items, testRules())
	if len(got) != 1 || len(refused) != 1 || refused[0].Reason != RefusedEvidence {
		t.Fatalf("got %+v refused %+v", got, refused)
	}
	it := got[0]
	if it.Kind != KindTheme || it.Confidence != 1 {
		t.Errorf("kind %q confidence %v, want theme and 1", it.Kind, it.Confidence)
	}
	stats := it.Stats(time.UTC)
	if stats.Items != 4 || stats.Minutes != 21 || stats.Days != 1 || !stats.LastAt.Equal(hoursAgo(2)) {
		t.Errorf("stats %+v", stats)
	}
	if want := []Source{SourceActivity, SourceNamed, SourceSearch}; !reflect.DeepEqual(stats.Sources, want) {
		t.Errorf("sources %v, want %v", stats.Sources, want)
	}
}

func TestAcceptMergesNearDuplicatesInOneAnswer(t *testing.T) {
	items := testItems()
	_, ids := Prompt(PromptInput{Items: items})
	ans := Answer{Interests: []Candidate{
		{Name: "smart hat wearable camera", Evidence: []string{"A1"}, Confidence: f(0.6)},
		{Name: "wearable camera smart hats", Evidence: []string{"A2"}, Confidence: f(0.8)},
	}}
	got, _ := Accept(ans, ids, items, testRules())
	if len(got) != 1 || len(got[0].Evidence) != 2 || got[0].Confidence != 0.8 || got[0].Name != "smart hat wearable camera" {
		t.Fatalf("got %+v", got)
	}
}

func TestSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"personal memory for AI assistants", "AI assistant memory personalization", true},
		{"wearable AI camera hats", "wearable AI camera hat", true},
		{"silent speech interface sensors", "silent speech interface", true},
		{"wearable AI camera hats", "proactive personal AI assistants", false},
		{"Claude Code multi-agent workflows", "AI meeting recording and transcription", false},
	}
	for _, c := range cases {
		if got := Similarity(c.a, c.b) >= sameInterest; got != c.same {
			t.Errorf("Similarity(%q, %q) = %.2f", c.a, c.b, Similarity(c.a, c.b))
		}
	}
}

func refs(keys ...string) []Ref {
	var out []Ref
	for i, key := range keys {
		out = append(out, Ref{Key: key, Source: SourceActivity, At: hoursAgo(float64(i + 1)).UnixMilli(), Minutes: 5})
	}
	return out
}

func TestMergeRefreshKeepsIDsNamesAndJoinsEvidence(t *testing.T) {
	current := []Interest{
		{ID: "id-hat", Key: "wearable ai camera hats", Name: "wearable AI camera hats", Evidence: refs("a1", "a2"), FirstAt: 1},
		{ID: "id-mem", Key: "personal memory for ai assistants", Name: "personal memory for AI assistants", Evidence: refs("a3")},
		{ID: "id-magpie", Key: "magpie plugin system open source", Name: "Magpie plugin system open source", Evidence: refs("g1", "a4", "a5")},
	}
	fresh := []Interest{
		{Key: "wearable ai camera hat", Name: "wearable AI camera hat", Why: "new why", Confidence: 0.9, Evidence: refs("a2", "a6")},
		{Key: "ai assistant memory personalization", Name: "AI assistant memory personalization", Evidence: refs("a7")},
		// Worded differently, but citing what the stored one cites.
		{Key: "open-source ai agent plugins", Name: "open-source AI agent plugins", Evidence: refs("g1", "a4")},
		{Key: "silent speech interface sensors", Name: "silent speech interface sensors", Evidence: refs("a8")},
	}
	got := Merge(current, fresh, MergeOptions{Mode: ModeRefresh, Now: testNow, Window: RefreshWindow})
	byID := map[string]Interest{}
	for _, it := range got {
		byID[it.ID] = it
	}
	if len(got) != 4 {
		t.Fatalf("got %d interests: %+v", len(got), got)
	}
	hat := byID["id-hat"]
	if hat.Name != "wearable AI camera hats" || hat.Why != "new why" || hat.Confidence != 0.9 || hat.FirstAt != 1 || hat.SeenAt != testNow.UnixMilli() {
		t.Errorf("hat %+v", hat)
	}
	if keys := refKeys(hat.Evidence); !reflect.DeepEqual(keys, []string{"a1", "a2", "a6"}) {
		t.Errorf("hat evidence %v", keys)
	}
	if byID["id-mem"].Name != "personal memory for AI assistants" {
		t.Errorf("a refresh renamed a stored interest: %+v", byID["id-mem"])
	}
	if byID["id-magpie"].Name != "Magpie plugin system open source" {
		t.Errorf("the reworded interest was not matched by its evidence: %+v", got)
	}
	newOne := byID[IDFor("silent speech interface sensors")]
	if newOne.Name != "silent speech interface sensors" || newOne.FirstAt != testNow.UnixMilli() {
		t.Errorf("new interest %+v", newOne)
	}
}

func refKeys(list []Ref) []string {
	var out []string
	for _, r := range list {
		out = append(out, r.Key)
	}
	return out
}

func TestMergeRefreshKeepsMissingInterestsUntilTheirEvidenceAges(t *testing.T) {
	old := Ref{Key: "a-old", Source: SourceActivity, At: testNow.Add(-20 * 24 * time.Hour).UnixMilli()}
	current := []Interest{
		{ID: "recent", Name: "on-device speech recognition", Evidence: refs("a1")},
		{ID: "stale", Name: "Brazil hydro power", Evidence: []Ref{old}},
		{ID: "pinned", Name: "electric car impact", Evidence: []Ref{old}},
	}
	got := Merge(current, nil, MergeOptions{Mode: ModeRefresh, Keep: map[string]bool{"pinned": true}, Now: testNow, Window: RefreshWindow})
	var ids []string
	for _, it := range got {
		ids = append(ids, it.ID)
	}
	if !reflect.DeepEqual(ids, []string{"recent", "pinned"}) {
		t.Fatalf("kept %v, want the recent one and the pinned one", ids)
	}
}

func TestMergeConsolidateRenamesAndDrops(t *testing.T) {
	current := []Interest{
		{ID: "id-hat", Key: "smart hat technology", Name: "smart hat technology", Evidence: refs("a1")},
		{ID: "id-gone", Name: "Brazil hydro power", Evidence: refs("a2")},
		{ID: "id-pin", Name: "electric car impact", Evidence: refs("a3")},
	}
	fresh := []Interest{{Key: "smart hat wearable camera technology", Name: "smart hat wearable camera technology", Evidence: refs("a1", "a4")}}
	got := Merge(current, fresh, MergeOptions{Mode: ModeConsolidate, Keep: map[string]bool{"id-pin": true}, Now: testNow, Window: ConsolidateWindow})
	if len(got) != 2 || got[0].ID != "id-hat" || got[0].Name != "smart hat wearable camera technology" || got[1].ID != "id-pin" {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeNeverFoldsAFreshInterestIntoARemovedOne(t *testing.T) {
	current := []Interest{{ID: "id-removed", Key: "magpie plugins", Name: "Magpie plugins", Evidence: refs("a1", "a2", "a3", "a4")}}
	// Related name (one word in three shared) and three of four refs held.
	fresh := []Interest{{Key: "magpie hardware", Name: "Magpie hardware", Evidence: refs("a1", "a2", "a3", "a9")}}
	if s := matchScore(fresh[0], current[0]); s == 0 {
		t.Fatalf("the fixture must match without the removal, score %v", s)
	}
	for _, mode := range []Mode{ModeRefresh, ModeConsolidate} {
		got := Merge(current, fresh, MergeOptions{Mode: mode, Removed: map[string]bool{"id-removed": true}, Now: testNow, Window: RefreshWindow})
		if len(got) != 2 {
			t.Fatalf("%s: got %+v", mode, got)
		}
		var ids []string
		for _, it := range got {
			ids = append(ids, it.ID)
		}
		if got[0].ID == "id-removed" || got[0].Name != "Magpie hardware" || got[1].ID != "id-removed" {
			t.Fatalf("%s: the fresh interest was folded into the removed one: %v", mode, ids)
		}
	}
}

func TestMergeKeepsTheStoredWhyWhenTheAnswerHasNone(t *testing.T) {
	current := []Interest{
		{ID: "id-hat", Key: "wearable ai camera hats", Name: "wearable AI camera hats", Why: "You are prototyping a smart cap.", WhyVerified: true, Evidence: refs("a1", "a2"), FirstAt: 1},
	}
	fresh := []Interest{
		{Key: "wearable ai camera hat", Name: "wearable AI camera hat", Confidence: 0.9, Evidence: refs("a2", "a6")},
	}
	for _, mode := range []Mode{ModeRefresh, ModeConsolidate} {
		got := Merge(current, fresh, MergeOptions{Mode: mode, Now: testNow, Window: RefreshWindow})
		if len(got) != 1 || got[0].Why != "You are prototyping a smart cap." || !got[0].WhyVerified {
			t.Fatalf("%s: an empty answer why erased the stored one: %+v", mode, got)
		}
	}
	// A why the answer wrote is verified: it was learned from the evidence
	// the prompt held. A carried stored why keeps its verification as stored.
	freshWhy := []Interest{{Key: "wearable ai camera hat", Name: "wearable AI camera hat", Why: "Still building it.", Evidence: refs("a2")}}
	if got := Merge(current, freshWhy, MergeOptions{Mode: ModeRefresh, Now: testNow, Window: RefreshWindow}); got[0].Why != "Still building it." || !got[0].WhyVerified {
		t.Fatalf("a fresh why was not verified: %+v", got[0])
	}
	unverified := []Interest{{ID: "id-hat", Key: "wearable ai camera hats", Name: "wearable AI camera hats", Why: "You are prototyping a smart cap.", Evidence: refs("a1")}}
	if got := Merge(unverified, fresh, MergeOptions{Mode: ModeRefresh, Now: testNow, Window: RefreshWindow}); got[0].Why != "You are prototyping a smart cap." || got[0].WhyVerified {
		t.Fatalf("a carried unverified why turned verified: %+v", got[0])
	}
}

func TestDedupeFoldsNearDuplicatesIntoOneRow(t *testing.T) {
	stored := []Interest{
		{ID: "id-old", Key: "silent speech emg wearable", Name: "silent speech EMG wearable", Why: "You follow silent speech interfaces.", Confidence: 0.7, Evidence: refs("a1", "a2"), FirstAt: 10, SeenAt: 20},
		{ID: "id-new", Key: "silent speech emg wearables", Name: "silent speech EMG wearables", Confidence: 0.9, Queries: []string{"silent speech EMG"}, Evidence: refs("a3"), FirstAt: 30, SeenAt: 40},
	}
	got := Dedupe(stored, DedupeOptions{})
	if len(got) != 1 {
		t.Fatalf("near-duplicates did not fold: %+v", got)
	}
	one := got[0]
	if one.ID != "id-old" || one.Name != "silent speech EMG wearable" || one.Why != "You follow silent speech interfaces." ||
		one.Confidence != 0.9 || one.FirstAt != 10 || one.SeenAt != 40 {
		t.Fatalf("folded %+v", one)
	}
	if keys := refKeys(one.Evidence); !reflect.DeepEqual(keys, []string{"a1", "a2", "a3"}) {
		t.Fatalf("evidence %v", keys)
	}
	if strings.Join(one.Queries, "|") != "silent speech EMG" {
		t.Fatalf("queries %v", one.Queries)
	}

	// The id the user pinned or renamed wins the fold, whichever is older.
	kept := Dedupe(stored, DedupeOptions{Keep: map[string]bool{"id-new": true}})
	if len(kept) != 1 || kept[0].ID != "id-new" || kept[0].Why != "You follow silent speech interfaces." {
		t.Fatalf("the chosen id did not win the fold: %+v", kept)
	}
	// Two chosen rows never fold into each other.
	both := Dedupe(stored, DedupeOptions{Keep: map[string]bool{"id-old": true, "id-new": true}})
	if len(both) != 2 {
		t.Fatalf("two pinned or renamed rows folded: %+v", both)
	}
	// A removed row never folds, either way around.
	removed := Dedupe(stored, DedupeOptions{Removed: map[string]bool{"id-new": true}})
	if len(removed) != 2 {
		t.Fatalf("a removed row folded: %+v", removed)
	}
}

func TestDedupePrefersTheNonEmptyWhy(t *testing.T) {
	got := Dedupe([]Interest{
		{ID: "a", Name: "silent speech EMG wearable", Evidence: refs("a1"), FirstAt: 10},
		{ID: "b", Name: "silent speech EMG wearables", Why: "You follow silent speech interfaces.", Evidence: refs("a2"), FirstAt: 30},
	}, DedupeOptions{})
	if len(got) != 1 || got[0].ID != "a" || got[0].Why != "You follow silent speech interfaces." {
		t.Fatalf("the fold did not prefer the non-empty why: %+v", got)
	}
	// A verified why beats an unverified one, whichever row survives.
	verified := Dedupe([]Interest{
		{ID: "a", Name: "silent speech EMG wearable", Why: "Older unverified line.", Evidence: refs("a1"), FirstAt: 10},
		{ID: "b", Name: "silent speech EMG wearables", Why: "You follow silent speech interfaces.", WhyVerified: true, Evidence: refs("a2"), FirstAt: 30},
	}, DedupeOptions{})
	if len(verified) != 1 || verified[0].ID != "a" || verified[0].Why != "You follow silent speech interfaces." || !verified[0].WhyVerified {
		t.Fatalf("the fold did not prefer the verified why: %+v", verified)
	}
}

func TestDedupeNeverRelinksWithheldEvidence(t *testing.T) {
	readable := func(it Interest) bool {
		if len(it.Evidence) == 0 {
			return false
		}
		for _, ref := range it.Evidence {
			if ref.Key == "withheld" {
				return false
			}
		}
		return true
	}
	// The withheld reference is forgotten, never re-linked; the readable twin
	// keeps the row going.
	got := Dedupe([]Interest{
		{ID: "held", Name: "silent speech EMG wearable", Why: "You follow silent speech interfaces.", Evidence: []Ref{{Key: "withheld", Source: SourceSearch}}, FirstAt: 10},
		{ID: "fresh", Name: "silent speech EMG wearables", Evidence: refs("a2"), FirstAt: 30},
	}, DedupeOptions{Readable: readable})
	if len(got) != 1 || got[0].ID != "held" || len(got[0].Evidence) != 1 || got[0].Evidence[0].Key != "a2" {
		t.Fatalf("withheld evidence was re-linked: %+v", got)
	}
	// With nothing readable left, the pair cannot fold at all.
	unreadable := Dedupe([]Interest{
		{ID: "held", Name: "silent speech EMG wearable", Evidence: []Ref{{Key: "withheld", Source: SourceSearch}}, FirstAt: 10},
		{ID: "also-held", Name: "silent speech EMG wearables", Evidence: []Ref{{Key: "withheld", Source: SourceNamed}}, FirstAt: 30},
	}, DedupeOptions{Readable: readable})
	if len(unreadable) != 2 {
		t.Fatalf("a pair with no readable evidence folded: %+v", unreadable)
	}
}

func TestAcceptAddsThePeopleTheModelSawAndStripsNamesFromWhyLines(t *testing.T) {
	items := testItems()
	_, ids := Prompt(PromptInput{Items: items})
	ans := Answer{
		People: []string{"Tomasz Nowak", "Jonathon Reyes"},
		Interests: []Candidate{
			{Name: "Tomasz Nowak grant tracking", Evidence: []string{"A1"}},
			{Name: "research grant writing", Why: "You help Jonathon Reyes with grants.", Evidence: []string{"A1"},
				Queries: []string{"Tomasz Nowak grant", "Jonathon Reyes podcast", "research grant deadlines"}},
		},
	}
	got, refused := Accept(ans, ids, items, testRules())
	if len(got) != 1 || got[0].Name != "research grant writing" {
		t.Fatalf("got %+v", got)
	}
	if strings.Join(got[0].Queries, "|") != "research grant deadlines" {
		t.Fatalf("queries %q", got[0].Queries)
	}
	// The name words go; the rest of the sentence stays.
	if got[0].Why != "You help with grants." {
		t.Fatalf("why = %q, want the sentence without the names", got[0].Why)
	}
	if topics.MentionsPerson(got[0].Why, append(testRules().Names, ans.People...)) {
		t.Fatalf("a why line kept a name: %q", got[0].Why)
	}
	if len(refused) != 3 {
		t.Fatalf("refused %+v", refused)
	}
}

func TestRankOrdersByEvidenceRecencyAndFeedback(t *testing.T) {
	interest := func(id string, age time.Duration, minutes float64, sources ...Source) Interest {
		it := Interest{ID: id, Key: id, Name: id, Confidence: 0.8}
		for i, source := range sources {
			it.Evidence = append(it.Evidence, Ref{Key: fmt.Sprintf("%s-%d", id, i), Source: source, At: testNow.Add(-age).UnixMilli(), Minutes: minutes})
		}
		return it
	}
	order := func(ranked []Interest) []string {
		var out []string
		for _, it := range ranked {
			out = append(out, it.ID)
		}
		return out
	}
	// More time, more sources and newer evidence each rank higher.
	cases := [][]Interest{
		{interest("thin", 2*time.Hour, 2, SourceActivity), interest("long", 2*time.Hour, 60, SourceActivity)},
		{interest("one", 2*time.Hour, 10, SourceActivity), interest("three", 2*time.Hour, 10, SourceActivity, SourceGoal, SourceAbout)},
		{interest("old", 10*24*time.Hour, 10, SourceActivity), interest("new", 2*time.Hour, 10, SourceActivity)},
	}
	for _, c := range cases {
		if got := order(Rank(c, nil, nil, testNow, time.UTC)); got[0] != c[1].ID {
			t.Errorf("order %v, want %s first", got, c[1].ID)
		}
	}
	thin, long := interest("thin", 2*time.Hour, 2, SourceActivity), interest("long", 2*time.Hour, 60, SourceActivity)
	if got := order(Rank([]Interest{thin, long}, map[string]float64{"thin": 3}, nil, testNow, time.UTC)); got[0] != "thin" {
		t.Fatalf("feedback weight did not lift the interest: %v", got)
	}
}

func TestRankNeverCutsAPinnedInterest(t *testing.T) {
	var list []Interest
	for i := 0; i < MaxStored+5; i++ {
		list = append(list, Interest{ID: fmt.Sprintf("i%d", i), Name: fmt.Sprintf("interest %d", i), Confidence: 0.9 - float64(i)*0.01, Evidence: refs("a")})
	}
	last := list[len(list)-1].ID
	ranked := Rank(list, nil, map[string]bool{last: true}, testNow, time.UTC)
	if len(ranked) != MaxStored {
		t.Fatalf("kept %d", len(ranked))
	}
	found := false
	for _, it := range ranked {
		found = found || it.ID == last
	}
	if !found {
		t.Fatalf("the pinned interest was cut")
	}
}

func TestRecheckAppliesNamesLearnedSince(t *testing.T) {
	stored := []Interest{
		{ID: "a", Name: "Nowak grant tracking", Queries: []string{"Nowak grant", "research grants"}},
		{ID: "b", Name: "research grant writing", Why: "You help Nowak.", Queries: []string{"Nowak grant"}},
		{ID: "c", Name: "Nowak lab news", Queries: []string{"Nowak lab"}},
	}
	got := Recheck(stored, Rules{Names: []string{"Ana Nowak"}}, map[string]bool{"c": true})
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Fatalf("got %+v", got)
	}
	// The why loses the name word and keeps the rest of the sentence.
	if strings.Join(got[0].Queries, "|") != "research grant writing" || got[0].Why != "You help" {
		t.Fatalf("b = %+v", got[0])
	}
	// A pinned interest whose name may no longer be searched keeps no
	// query, not its name as one.
	if len(got[1].Queries) != 0 {
		t.Fatalf("pinned c = %+v", got[1])
	}
	pinned := Recheck([]Interest{{ID: "d", Name: "Mental health support", Queries: []string{"therapy apps"}}}, Rules{}, map[string]bool{"d": true})
	if len(pinned) != 1 || len(pinned[0].Queries) != 0 {
		t.Fatalf("a pinned sensitive interest is still searched: %+v", pinned)
	}
	if SafeToSend("Nowak lab news", Rules{Names: []string{"Ana Nowak"}}) || SafeToSend("Mental health support", Rules{}) ||
		!SafeToSend("research grant writing", Rules{}) || !SafeToSend("Magpie", Rules{}) {
		t.Fatal("SafeToSend disagrees with the checks")
	}
}

// Addresses never go to a search, whatever screen masking leaves alone:
// the query check is stricter than the pii patterns on purpose.
func TestAddressesNeverGoToASearch(t *testing.T) {
	for _, text := range []string{
		"12 Oak Street Brooklyn lease", "221B Baker Street flat", "Hauptstrasse 12 Berlin permit",
		"Berliner Straße 7 Wohnung", "4 Privet Drive Little Whinging",
		// Look like addresses to the query check, and are dropped with them.
		"2025 Budget Shared Drive", "2 Hour Drive to Napa", "10 Tips Street Food", "2 Way Sync Drive",
	} {
		if got := checkText(text, QueryMaxWords, QueryMaxRunes, true, Rules{}); got != RefusedPrivate {
			t.Errorf("%q = %q, want private", text, got)
		}
	}
	for _, text := range []string{"ESP32-S3 Sense camera build", "Top 10 tips for wearable cameras", "version 2 release notes"} {
		if got := checkText(text, QueryMaxWords, QueryMaxRunes, true, Rules{}); got != "" {
			t.Errorf("%q = %q, want allowed", text, got)
		}
	}
}

// A version, an exit code or a status code in a name is not a verification
// code: pii.RedactCodes must leave it alone, so Recheck keeps the interest.
func TestRecheckKeepsNamesWithDeveloperCodes(t *testing.T) {
	stored := []Interest{
		{ID: "a", Name: "Python 3.12 code style", Queries: []string{"Python 3.12 code style"}},
		{ID: "b", Name: "Exit code 127 debugging", Queries: []string{"Exit code 127"}},
		{ID: "c", Name: "HTTP status code 404 pages", Queries: []string{"status code 404"}},
	}
	got := Recheck(stored, Rules{}, nil)
	if len(got) != len(stored) {
		t.Fatalf("interests with version or status numbers must stay: %+v", got)
	}
	for i, it := range got {
		if it.Name != stored[i].Name {
			t.Errorf("name %d = %q, want %q", i, it.Name, stored[i].Name)
		}
	}
	// The gate still bites the other way: a name holding a short code goes.
	dropped := Recheck([]Interest{{ID: "p", Name: "my PIN is 482", Queries: []string{"my PIN is 482"}}}, Rules{}, nil)
	if len(dropped) != 0 {
		t.Fatalf("an interest holding a short code must go: %+v", dropped)
	}
}
