package topics

import (
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

func daysAgo(days float64) time.Time {
	return testNow.Add(-time.Duration(days * float64(24*time.Hour)))
}

func rankLabels(signals []Signal, opts Options) []string {
	if opts.Now.IsZero() {
		opts.Now = testNow
	}
	if opts.Location == nil {
		opts.Location = time.UTC
	}
	var out []string
	for _, topic := range Rank(signals, opts) {
		out = append(out, topic.Label)
	}
	return out
}

func indexOf(labels []string, want string) int {
	for i, label := range labels {
		if strings.EqualFold(label, want) {
			return i
		}
	}
	return -1
}

func TestRankRepeatedInterestOutranksOneOff(t *testing.T) {
	signals := []Signal{
		{Source: SourceSearch, Text: "wails drag regions", At: daysAgo(1)},
		{Source: SourceSearch, Text: "Wails drag regions not working", At: daysAgo(2), Count: 2},
		{Source: SourceWindow, Text: "Wails drag regions - Wails docs", At: daysAgo(1), Dwell: 25 * time.Minute},
		{Source: SourceChat, Text: "Fix wails drag regions on macOS", At: daysAgo(0.5)},
		{Source: SourceSearch, Text: "wails drag regions", At: daysAgo(3)},
		{Source: SourcePage, Text: "Sourdough starter feeding schedule", At: daysAgo(3)},
	}
	labels := rankLabels(signals, Options{})
	if len(labels) == 0 || !strings.EqualFold(labels[0], "wails drag regions") {
		t.Fatalf("labels = %v, want wails drag regions first", labels)
	}
	if indexOf(labels, "sourdough starter feeding") >= 0 || indexOf(labels, "sourdough starter") >= 0 {
		t.Fatalf("a single page visit became a topic: %v", labels)
	}
}

func TestRankPrefersSpecificPhraseOverItsWord(t *testing.T) {
	signals := []Signal{
		{Source: SourceSearch, Text: "kubernetes pod autoscaling", At: daysAgo(1)},
		{Source: SourceWindow, Text: "Kubernetes pod autoscaling guide", At: daysAgo(1), Dwell: 10 * time.Minute},
		{Source: SourceWindow, Text: "kubernetes pod autoscaling metrics", At: daysAgo(2), Dwell: 10 * time.Minute},
		{Source: SourceSearch, Text: "kubernetes pod autoscaling", At: daysAgo(3)},
	}
	labels := rankLabels(signals, Options{})
	if indexOf(labels, "kubernetes") >= 0 {
		t.Fatalf("the broad word won over the specific phrase: %v", labels)
	}
	if indexOf(labels, "kubernetes pod autoscaling") != 0 {
		t.Fatalf("labels = %v, want the specific phrase first", labels)
	}
	if len(labels) != 1 {
		t.Fatalf("overlapping phrases were repeated: %v", labels)
	}
}

func TestRankNeverEmitsLinksPathsAddressesOrCodes(t *testing.T) {
	signals := []Signal{
		{Source: SourcePage, Text: "https://example.com/private/report?id=42 quarterly roadmap", At: daysAgo(1)},
		{Source: SourcePage, Text: "quarterly roadmap /Users/ana/Documents/secret-plan.key", At: daysAgo(1)},
		{Source: SourceWindow, Text: "quarterly roadmap - ana.lopez@example.com", At: daysAgo(2)},
		{Source: SourceWindow, Text: "Order 48213377 tracking AB12CD34EF56 quarterly roadmap", At: daysAgo(2)},
		{Source: SourceWindow, Text: "example.com quarterly roadmap", At: daysAgo(2)},
		{Source: SourceWindow, Text: "quarterly roadmap", At: daysAgo(3)},
	}
	for _, topic := range Rank(signals, Options{Now: testNow, Location: time.UTC}) {
		for _, bad := range []string{"http", "example", "/", "@", "users", "48213377", "ab12cd34ef56", ".com", "secret-plan", "id"} {
			if strings.Contains(strings.ToLower(topic.Label), bad) {
				t.Fatalf("label %q carries %q", topic.Label, bad)
			}
		}
		if len(strings.Fields(topic.Label)) > maxPhrase {
			t.Fatalf("label %q is longer than %d words", topic.Label, maxPhrase)
		}
	}
	labels := rankLabels(signals, Options{})
	if indexOf(labels, "quarterly roadmap") < 0 {
		t.Fatalf("labels = %v, want the clean phrase kept", labels)
	}
}

func TestRankLeavesOutPeopleTheUserKnows(t *testing.T) {
	signals := []Signal{
		{Source: SourceMeeting, Text: "1:1 Ana Lopez pricing review", At: daysAgo(1)},
		{Source: SourceMeeting, Text: "Ana Lopez pricing review", At: daysAgo(8)},
		{Source: SourceChat, Text: "Prep for Ana pricing review", At: daysAgo(1)},
		{Source: SourceChat, Text: "Ana pricing review notes", At: daysAgo(3)},
	}
	labels := rankLabels(signals, Options{Names: []string{"Ana Lopez", "ana.lopez@example.com"}})
	for _, label := range labels {
		lower := strings.ToLower(label)
		if strings.Contains(lower, "ana") || strings.Contains(lower, "lopez") {
			t.Fatalf("a person's name became a topic: %v", labels)
		}
	}
	if indexOf(labels, "pricing review") < 0 {
		t.Fatalf("labels = %v, want pricing review", labels)
	}
}

func TestRankSkipsBlockedTopicsAndTheirPhrases(t *testing.T) {
	signals := []Signal{
		{Source: SourceSearch, Text: "rust async runtime", At: daysAgo(1), Count: 3},
		{Source: SourceWindow, Text: "rust async runtime comparison", At: daysAgo(1), Dwell: 20 * time.Minute},
		{Source: SourceSearch, Text: "swiftui list performance", At: daysAgo(2), Count: 2},
		{Source: SourceWindow, Text: "SwiftUI list performance", At: daysAgo(2), Dwell: 5 * time.Minute},
		{Source: SourceSearch, Text: "swiftui list performance", At: daysAgo(3)},
		{Source: SourceSearch, Text: "swiftui list performance", At: daysAgo(4)},
	}
	labels := rankLabels(signals, Options{Blocked: []string{"Rust async"}})
	for _, label := range labels {
		if strings.Contains(strings.ToLower(label), "rust async") {
			t.Fatalf("a blocked topic came back: %v", labels)
		}
	}
	if indexOf(labels, "swiftui list performance") < 0 {
		t.Fatalf("labels = %v, want the other topic kept", labels)
	}
}

func TestRankRecencyDwellAndWindow(t *testing.T) {
	signals := []Signal{
		// Same counts, but one is from this week and the other ten days ago.
		{Source: SourceSearch, Text: "vector databases", At: daysAgo(1)},
		{Source: SourceSearch, Text: "vector databases", At: daysAgo(2)},
		{Source: SourceSearch, Text: "vector databases", At: daysAgo(3)},
		{Source: SourceSearch, Text: "garden irrigation", At: daysAgo(10)},
		{Source: SourceSearch, Text: "garden irrigation", At: daysAgo(11)},
		{Source: SourceSearch, Text: "garden irrigation", At: daysAgo(12)},
		// Outside the window: never counted.
		{Source: SourceSearch, Text: "tax filing deadline", At: daysAgo(20)},
		{Source: SourceSearch, Text: "tax filing deadline", At: daysAgo(21)},
	}
	labels := rankLabels(signals, Options{})
	if indexOf(labels, "vector databases") != 0 || indexOf(labels, "garden irrigation") != 1 {
		t.Fatalf("labels = %v, want the recent topic before the older one", labels)
	}
	if indexOf(labels, "tax filing deadline") >= 0 {
		t.Fatalf("a topic outside the window came back: %v", labels)
	}

	var dwell []Signal
	for _, day := range []float64{1, 2, 3} {
		dwell = append(dwell,
			Signal{Source: SourceWindow, Text: "Figma auto layout", At: daysAgo(day), Dwell: time.Minute},
			Signal{Source: SourceWindow, Text: "Postgres query planner", At: daysAgo(day), Dwell: 75 * time.Minute})
	}
	labels = rankLabels(dwell, Options{})
	if indexOf(labels, "postgres query planner") != 0 {
		t.Fatalf("labels = %v, want the longer dwell first", labels)
	}
}

func TestRankFeedbackWeightsReorder(t *testing.T) {
	var signals []Signal
	for _, day := range []float64{1, 2, 3} {
		signals = append(signals,
			Signal{Source: SourceChat, Text: "electric bikes", At: daysAgo(day)},
			Signal{Source: SourceChat, Text: "home espresso machines", At: daysAgo(day)})
	}
	base := rankLabels(signals, Options{})
	if len(base) != 2 {
		t.Fatalf("labels = %v, want two topics", base)
	}
	loser := strings.ToLower(base[1])
	weighted := rankLabels(signals, Options{Weights: map[string]float64{KeyFor(loser): 3, KeyFor(base[0]): 0.3}})
	if !strings.EqualFold(weighted[0], loser) {
		t.Fatalf("weights did not reorder: base %v, weighted %v", base, weighted)
	}
}

func TestRankDropsAppNamesAndGenericTitles(t *testing.T) {
	signals := []Signal{
		{Source: SourceWindow, Text: "Untitled - Google Chrome", At: daysAgo(1), Dwell: 30 * time.Minute},
		{Source: SourceWindow, Text: "Inbox (3) - Gmail", At: daysAgo(1), Dwell: 30 * time.Minute},
		{Source: SourceWindow, Text: "New Tab", At: daysAgo(2), Dwell: 30 * time.Minute},
		{Source: SourceWindow, Text: "Slack | general", At: daysAgo(2), Dwell: 30 * time.Minute},
	}
	labels := rankLabels(signals, Options{})
	for _, label := range labels {
		switch strings.ToLower(label) {
		case "google chrome", "gmail", "untitled", "new tab", "slack", "inbox":
			t.Fatalf("an app or generic title became a topic: %v", labels)
		}
	}
}

func TestRankLimitAndStableIDs(t *testing.T) {
	var signals []Signal
	for _, text := range []string{
		"alpha protocol design", "beta release notes", "gamma ray bursts", "delta lake tables",
		"epsilon greedy bandits", "zeta function zeros", "theta healing claims", "iota ledger fees",
		"kappa architecture streams", "lambda calculus basics", "omicron variant data", "sigma rule detection",
	} {
		signals = append(signals,
			Signal{Source: SourceSearch, Text: text, At: daysAgo(1)},
			Signal{Source: SourceSearch, Text: text, At: daysAgo(2)},
			Signal{Source: SourceSearch, Text: text, At: daysAgo(3)})
	}
	got := Rank(signals, Options{Now: testNow, Location: time.UTC})
	if len(got) != defaultLimit {
		t.Fatalf("got %d topics, want the default limit %d", len(got), defaultLimit)
	}
	again := Rank(signals, Options{Now: testNow, Location: time.UTC})
	for i := range got {
		if got[i].ID != again[i].ID || got[i].ID != IDFor(got[i].Key) {
			t.Fatalf("ids are not stable: %+v vs %+v", got[i], again[i])
		}
	}
}

func TestRankHanText(t *testing.T) {
	signals := []Signal{
		{Source: SourceSearch, Text: "如何使用Wails拖拽", At: daysAgo(1)},
		{Source: SourceSearch, Text: "Wails拖拽 区域", At: daysAgo(2)},
		{Source: SourceSearch, Text: "Wails 拖拽", At: daysAgo(3)},
	}
	labels := rankLabels(signals, Options{})
	if len(labels) == 0 {
		t.Fatal("no topic from Han text")
	}
	for _, label := range labels {
		if strings.Contains(label, "如何") || strings.Contains(label, "使用") {
			t.Fatalf("a stop word stayed in %q", label)
		}
	}
}

func TestKeyForMatchesRankKeys(t *testing.T) {
	if got := KeyFor("  Wails  Drag-Regions, again "); got != "wails drag-regions" {
		t.Fatalf("KeyFor = %q", got)
	}
}

func TestDigestKeepsShortPhrasesWithoutNames(t *testing.T) {
	signals := []Signal{
		{Source: SourceSearch, Text: "wails drag regions", At: daysAgo(1), Count: 2},
		{Source: SourcePage, Text: "Wails drag regions - Ana Lopez blog", At: daysAgo(1)},
		{Source: SourcePage, Text: "https://example.com/x?y=1", At: daysAgo(1)},
	}
	phrases := Digest(signals, []string{"Ana Lopez"}, 10)
	if len(phrases) == 0 || !strings.EqualFold(phrases[0].Text, "wails drag regions") || phrases[0].Count != 3 {
		t.Fatalf("phrases = %+v", phrases)
	}
	for _, p := range phrases {
		lower := strings.ToLower(p.Text)
		if strings.Contains(lower, "ana") || strings.Contains(lower, "example") || strings.Contains(lower, "http") {
			t.Fatalf("digest kept %q", p.Text)
		}
		if p.Text == "wails drag" || p.Text == "drag regions" {
			t.Fatalf("a covered sub-phrase was kept: %+v", phrases)
		}
	}
}

func TestRankDropsNamesItWasNotToldAbout(t *testing.T) {
	signals := []Signal{
		{Source: SourcePage, Text: "Priya Raman - LinkedIn", At: daysAgo(1)},
		{Source: SourcePage, Text: "Priya Raman - LinkedIn", At: daysAgo(2)},
		{Source: SourcePage, Text: "Priya Raman | Speaker", At: daysAgo(3)},
		{Source: SourceChat, Text: "Ticket 2026-A7K9Q2 with Priya Raman", At: daysAgo(1)},
		{Source: SourceChat, Text: "Jane's budget review", At: daysAgo(1)},
		{Source: SourcePage, Text: "张伟 的主页", At: daysAgo(1)},
		{Source: SourcePage, Text: "张伟 的主页", At: daysAgo(2)},
		{Source: SourceWindow, Text: "Order tx-8f3k2m9 refund", At: daysAgo(1), Count: 3},
		{Source: SourceWindow, Text: "mybank.ch statement review", At: daysAgo(1), Count: 3},
		{Source: SourceWindow, Text: "quarterly report.pdf", At: daysAgo(1), Count: 3},
		{Source: SourceSearch, Text: "viewReturn swift", At: daysAgo(1), Count: 3},
	}
	for _, topic := range Rank(signals, Options{Now: testNow, Location: time.UTC, Names: []string{"Jane Doe"}}) {
		lower := strings.ToLower(topic.Label)
		for _, bad := range []string{"priya", "raman", "jane", "张伟", "a7k9q2", "tx-8f3k2m9", "mybank", "report.pdf"} {
			if strings.Contains(lower, bad) {
				t.Fatalf("label %q carries %q", topic.Label, bad)
			}
		}
		if topic.Label == "viewReturn" {
			t.Fatal("a code symbol counted as a product name")
		}
	}
	if got := KeyFor("node.js and vue.js on go1.25"); got != "node.js vue.js go1.25" {
		t.Fatalf("tool names were dropped: %q", got)
	}
}

func TestRankDropsLowercaseAndUnlistedNamesFromSearches(t *testing.T) {
	signals := []Signal{
		// A lower-case search for someone, on two days.
		{Source: SourceSearch, Text: "priya raman linkedin", At: daysAgo(1)},
		{Source: SourceSearch, Text: "priya raman linkedin", At: daysAgo(2)},
		// An unlisted surname, searched on two days.
		{Source: SourceSearch, Text: "okonkwo resume", At: daysAgo(1)},
		{Source: SourceSearch, Text: "okonkwo resume", At: daysAgo(2)},
		// A Han name in searches and in a chat title.
		{Source: SourceSearch, Text: "张伟 简历", At: daysAgo(1)},
		{Source: SourceSearch, Text: "张伟 简历", At: daysAgo(2)},
		{Source: SourceSearch, Text: "张伟 简历", At: daysAgo(3)},
		{Source: SourceChat, Text: "和 张伟 的合同", At: daysAgo(1)},
		// Everyday words that are also names stay usable in lower case.
		{Source: SourceChat, Text: "mark as read shortcut", At: daysAgo(1)},
		{Source: SourceChat, Text: "mark as read shortcut", At: daysAgo(2)},
		{Source: SourceChat, Text: "mark as read shortcut", At: daysAgo(3)},
	}
	labels := rankLabels(signals, Options{})
	for _, label := range labels {
		lower := strings.ToLower(label)
		for _, bad := range []string{"priya", "raman", "okonkwo", "张伟"} {
			if strings.Contains(lower, bad) {
				t.Fatalf("label %q carries the name %q (labels %v)", label, bad, labels)
			}
		}
	}
	if indexOf(labels, "read shortcut") < 0 {
		t.Fatalf("an everyday word was taken for a name: %v", labels)
	}
	phrases := Digest(signals[:2], nil, 10)
	for _, p := range phrases {
		if strings.Contains(strings.ToLower(p.Text), "priya") {
			t.Fatalf("the journal kept a lower-case name: %+v", phrases)
		}
	}
}

func TestRankPhrasesNeedThreeDays(t *testing.T) {
	two := []Signal{
		{Source: SourceSearch, Text: "wails drag regions", At: daysAgo(1), Count: 3},
		{Source: SourcePage, Text: "Wails drag regions guide", At: daysAgo(2)},
	}
	if labels := rankLabels(two, Options{}); len(labels) != 0 {
		t.Fatalf("a phrase only the browser showed on two days became a topic: %v", labels)
	}
	three := append(append([]Signal(nil), two...), Signal{Source: SourceSearch, Text: "wails drag regions", At: daysAgo(3)})
	if labels := rankLabels(three, Options{}); indexOf(labels, "wails drag regions") < 0 {
		t.Fatalf("three days of searches did not make a topic: %v", labels)
	}
	// Window, file and chat titles are titles too.
	for _, source := range []Source{SourceWindow, SourceFile, SourceChat} {
		titles := []Signal{
			{Source: source, Text: "okonkwo resume draft", At: daysAgo(1), Count: 3},
			{Source: source, Text: "okonkwo resume draft", At: daysAgo(2), Count: 3},
		}
		if labels := rankLabels(titles, Options{}); len(labels) != 0 {
			t.Fatalf("%s titles on two days became a topic: %v", source, labels)
		}
	}
	// A meeting, an About-you entry or a goal does not vouch for a title
	// beside it: two days are still two days.
	for _, source := range []Source{SourceMeeting, SourceMemory, SourceGoal} {
		with := append(append([]Signal(nil), two...), Signal{Source: source, Text: "Okonkwo wails drag regions", At: daysAgo(1)})
		if labels := rankLabels(with, Options{}); len(labels) != 0 {
			t.Fatalf("a search and a %s on two days became a topic: %v", source, labels)
		}
	}
	// One product-shaped word does not clear a name beside it, and neither
	// does an all-caps word, a "Mc" name or a lone inner-capital word;
	// summaries count as titles.
	for _, held := range [][]Signal{
		{{Source: SourceWindow, Text: "Okonkwo CV", At: daysAgo(1), Count: 3}, {Source: SourceWindow, Text: "Okonkwo CV", At: daysAgo(2), Count: 3}},
		{{Source: SourceSearch, Text: "okonkwo resume v2", At: daysAgo(1)}, {Source: SourceSearch, Text: "okonkwo resume v2", At: daysAgo(2)}},
		{{Source: SourceWindow, Text: "McCarthy", At: daysAgo(1), Count: 3}, {Source: SourceWindow, Text: "McCarthy", At: daysAgo(1.2), Count: 3}},
		{{Source: SourceWindow, Text: "NGUYEN", At: daysAgo(1), Count: 3}, {Source: SourceWindow, Text: "NGUYEN", At: daysAgo(1.2), Count: 3}},
		{{Source: SourceTimeline, Text: "Okonkwo resume notes", At: daysAgo(1)}, {Source: SourceTimeline, Text: "Okonkwo resume notes", At: daysAgo(1.2)}},
		{{Source: SourceEntity, Text: "Okonkwo resume", At: daysAgo(1)}, {Source: SourceEntity, Text: "Okonkwo resume", At: daysAgo(2)}},
		{{Source: SourceWindow, Text: "LeBron", At: daysAgo(1), Count: 3}, {Source: SourceWindow, Text: "LeBron", At: daysAgo(1.2), Count: 3}},
	} {
		for _, label := range rankLabels(held, Options{}) {
			lower := strings.ToLower(label)
			if strings.Contains(lower, "okonkwo") || strings.Contains(lower, "mccarthy") || strings.Contains(lower, "nguyen") || strings.Contains(lower, "lebron") {
				t.Fatalf("%q on under three days became a topic: %q", held[0].Text, label)
			}
		}
	}
	// A product shape is not a name: one day is enough.
	shaped := []Signal{
		{Source: SourceWindow, Text: "SwiftUI NavigationStack", At: daysAgo(1), Dwell: 30 * time.Minute},
		{Source: SourceWindow, Text: "SwiftUI NavigationStack", At: daysAgo(1.2), Dwell: 30 * time.Minute},
	}
	if labels := rankLabels(shaped, Options{}); len(labels) == 0 {
		t.Fatal("a product name in window titles was held back")
	}
}

func TestRankHanNamesNeedThreeDaysAndCommonWordsStay(t *testing.T) {
	var signals []Signal
	for _, text := range []string{"程小红 简历", "方晓东 简历", "欧阳娜娜 简历", "施佳琪 合同", "金莎 合同", "高明 合同"} {
		signals = append(signals,
			Signal{Source: SourceSearch, Text: text, At: daysAgo(1)},
			Signal{Source: SourceSearch, Text: text, At: daysAgo(2)},
			Signal{Source: SourceSearch, Text: text, At: daysAgo(3)})
	}
	for _, label := range rankLabels(signals, Options{}) {
		for _, bad := range []string{"程小红", "方晓东", "欧阳娜娜", "施佳琪", "金莎", "高明"} {
			if strings.Contains(label, bad) {
				t.Fatalf("label %q carries the name %q", label, bad)
			}
		}
	}
	// A name the lists miss is held back for three days in titles.
	unlisted := []Signal{
		{Source: SourceWindow, Text: "佘晓 简历", At: daysAgo(1), Count: 3},
		{Source: SourceSearch, Text: "佘晓 简历", At: daysAgo(2), Count: 3},
	}
	if labels := rankLabels(unlisted, Options{}); len(labels) != 0 {
		t.Fatalf("Han titles on two days became a topic: %v", labels)
	}
	var words []Signal
	for _, text := range []string{"黄金 etf", "周末 去哪玩", "程序 设计", "任务 管理"} {
		for _, day := range []float64{1, 2, 3} {
			words = append(words, Signal{Source: SourceSearch, Text: text, At: daysAgo(day)})
		}
	}
	labels := rankLabels(words, Options{})
	for _, want := range []string{"黄金 etf", "周末 去哪玩", "程序 设计", "任务 管理"} {
		if indexOf(labels, want) < 0 {
			t.Fatalf("an everyday word was taken for a name: want %q in %v", want, labels)
		}
	}
	// In searches every given name is held, in any case; in chat titles a
	// lower-case place word stays.
	for _, text := range []string{"sam altman interview", "mark zuckerberg podcast", "傅雷 家书", "于谦 相声"} {
		var looked []Signal
		for _, day := range []float64{1, 2, 3} {
			looked = append(looked, Signal{Source: SourceSearch, Text: text, At: daysAgo(day)})
		}
		for _, label := range rankLabels(looked, Options{}) {
			for _, name := range []string{"sam", "altman", "mark", "zuckerberg", "傅雷", "于谦"} {
				if strings.Contains(strings.ToLower(label), name) {
					t.Fatalf("%q kept the name %q in %q", text, name, label)
				}
			}
		}
	}
	for _, text := range []string{"jordan visa", "ross sea ice"} {
		var trip []Signal
		for _, day := range []float64{1, 2, 3} {
			trip = append(trip, Signal{Source: SourceChat, Text: text, At: daysAgo(day)})
		}
		if labels := rankLabels(trip, Options{}); indexOf(labels, text) < 0 {
			t.Fatalf("a place word was taken for a name: want %q in %v", text, labels)
		}
	}
}
