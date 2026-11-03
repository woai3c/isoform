package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon/appparser"
)

func mkParsedJSON(t *testing.T, contentType, title, body, bundleID string) string {
	t.Helper()
	pc := appparser.ParsedContent{
		ContentType: contentType,
		Title:       title,
		Body:        body,
		Metadata:    map[string]string{"bundleId": bundleID},
	}
	b, err := json.Marshal(pc)
	if err != nil {
		t.Fatalf("marshal parsed content: %v", err)
	}
	return string(b)
}

// TestResolveRealApp covers the §3a reverse-inference: bundleId must win over a
// wrong raw snapshot.app (the validation #6/#25 mislabel pattern).
func TestResolveRealApp(t *testing.T) {
	tests := []struct {
		name     string
		parsed   *appparser.ParsedContent
		rawApp   string
		expected string
	}{
		{
			name:     "bundleId overrides wrong raw app (validation #6)",
			parsed:   &appparser.ParsedContent{Title: "Maya Chen (DM) - Isoform - Slack", Metadata: map[string]string{"bundleId": "com.tinyspeck.slackmacgap"}},
			rawApp:   "Discord", // raw app is wrong here
			expected: "Slack",
		},
		{
			name:     "title suffix fallback when no bundleId",
			parsed:   &appparser.ParsedContent{Title: "Halcyon planning - Google Chrome - Alex"},
			rawApp:   "WeChat",
			expected: "Chrome",
		},
		{
			name:     "WeChat parser key canonicalized to bundle name (no double-labeling)",
			parsed:   nil,
			rawApp:   "tencent_chat",
			expected: "WeChat",
		},
		{
			name:     "unknown bundle prefers window owner name over generic last segment",
			parsed:   &appparser.ParsedContent{Metadata: map[string]string{"bundleId": "com.termius-dmg.mac"}},
			rawApp:   "Termius",
			expected: "Termius",
		},
		{
			name:     "unknown bundle id derives last segment only when no owner name",
			parsed:   &appparser.ParsedContent{Metadata: map[string]string{"bundleId": "com.west2online.ClashX"}},
			rawApp:   "",
			expected: "ClashX",
		},
		{
			name:     "raw app fallback when parsed nil",
			parsed:   nil,
			rawApp:   "cmux",
			expected: "cmux",
		},
		{
			name:     "unknown everything yields placeholder",
			parsed:   nil,
			rawApp:   "",
			expected: "?",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRealApp(tc.parsed, tc.rawApp); got != tc.expected {
				t.Errorf("resolveRealApp = %q, want %q", got, tc.expected)
			}
		})
	}
}

// TestCollapseScrolling verifies the §3b fold: consecutive same-conversation
// snapshots collapse to a single observation (validation #27-36 scroll burst).
func TestCollapseScrolling(t *testing.T) {
	title := "halcyon-research (Channel) - Isoform - Slack"
	body := "alex_halcyon: sample browser architecture notes"
	snaps := []ActivitySnapshotRow{
		{ID: 1, SessionID: "s", Timestamp: 1000, ParsedJSON: mkParsedJSON(t, "conversation", title, body+" one", "com.tinyspeck.slackmacgap")},
		{ID: 2, SessionID: "s", Timestamp: 11000, ParsedJSON: mkParsedJSON(t, "conversation", title, body+" one two", "com.tinyspeck.slackmacgap")},
		{ID: 3, SessionID: "s", Timestamp: 21000, ParsedJSON: mkParsedJSON(t, "conversation", title, body+" one two three", "com.tinyspeck.slackmacgap")},
	}
	obs := collapseScrolling(snaps, nil)
	if len(obs) != 1 {
		t.Fatalf("expected 1 collapsed observation, got %d", len(obs))
	}
	if len(obs[0].SnapshotIDs) != 3 {
		t.Errorf("expected 3 folded snapshot ids, got %d", len(obs[0].SnapshotIDs))
	}
	if obs[0].RealApp != "Slack" {
		t.Errorf("expected RealApp Slack, got %q", obs[0].RealApp)
	}

	// A different conversation must NOT fold into the same bucket.
	snaps = append(snaps, ActivitySnapshotRow{
		ID: 4, SessionID: "s", Timestamp: 31000,
		ParsedJSON: mkParsedJSON(t, "terminal", "cmux session", "go build ./... compiling packages now", "com.cmuxterm.app"),
	})
	if got := len(collapseScrolling(snaps, nil)); got != 2 {
		t.Errorf("expected 2 observations across two conversations, got %d", got)
	}
}

// TestExtractEntities covers the §4 deterministic rules over URL / chat titles.
func TestExtractEntities(t *testing.T) {
	prObs := Observation{URL: "https://github.com/Isoform/something/pull/1930"}
	ents := extractEntities(prObs)
	wantPR := map[string]bool{"pr": false, "repo": false, "project": false}
	for _, e := range ents {
		if _, ok := wantPR[e.Type]; ok {
			wantPR[e.Type] = true
		}
	}
	for typ, found := range wantPR {
		if !found {
			t.Errorf("PR url: missing expected entity type %q (got %+v)", typ, ents)
		}
	}

	dmObs := Observation{Title: "Maya Chen (DM) - Isoform - Slack"}
	if !hasEntity(extractEntities(dmObs), entityPerson, "Maya Chen") {
		t.Errorf("DM title: expected person Maya Chen, got %+v", extractEntities(dmObs))
	}

	chObs := Observation{Title: "halcyon-research (Channel) - Isoform - Slack"}
	if !hasEntity(extractEntities(chObs), entityChannel, "halcyon-research") {
		t.Errorf("channel title: expected channel entity, got %+v", extractEntities(chObs))
	}

	// PR link pasted into a chat message body (browser_url empty) must resolve —
	// this is what makes a Slack/WeChat mention stitch with the browser PR.
	bodyPR := Observation{Body: "看 https://github.com/Isoform/something/pull/1941 这个 PR"}
	if !hasEntity(extractEntities(bodyPR), entityRepo, "Isoform/something") {
		t.Errorf("body PR link: expected repo from message body, got %+v", extractEntities(bodyPR))
	}
	if !hasEntity(extractEntities(bodyPR), entityPR, "Isoform/something#1941") {
		t.Errorf("body PR link: expected pr from message body, got %+v", extractEntities(bodyPR))
	}
}

// TestExtractEntitiesSkipsGoModDeps locks the entity-flood fix: version-suffixed
// github paths ("github.com/x/y v1.18.1") are go-module build deps, not repos the
// user is working on — they must not become entities (flooding tags / false-merging
// unrelated builds that share a common dependency).
func TestExtractEntitiesSkipsGoModDeps(t *testing.T) {
	o := Observation{Body: "go: downloading github.com/emirpasic/gods v1.18.1\ngo: downloading github.com/golang/groupcache v0.0.1"}
	if hasEntity(extractEntities(o), entityRepo, "emirpasic/gods") {
		t.Errorf("go-mod dependency must NOT become a repo entity, got %+v", extractEntities(o))
	}
	if !hasEntity(extractEntities(Observation{Body: "看了 github.com/Isoform/something 这个仓库"}), entityRepo, "Isoform/something") {
		t.Error("plain repo URL (no version) should still resolve")
	}
	if !hasEntity(extractEntities(Observation{Body: "github.com/Isoform/something/pull/1955"}), entityPR, "Isoform/something#1955") {
		t.Error("PR URL should still resolve")
	}
}

// TestCandidateAppsFilterUnknown locks the acceptance precision fix: the "?"
// placeholder (failed app inference) must never inflate the cross-app apps list.
func TestCandidateAppsFilterUnknown(t *testing.T) {
	seq := ObservationSequence{Observations: []Observation{
		{Index: 0, RealApp: "cmux", StartMs: 100, EndMs: 200},
		{Index: 1, RealApp: unknownAppName, StartMs: 300, EndMs: 400},
	}}
	cand := candidateFromExtracted(extractedEvent{SourceIndices: []int{0, 1}}, seq)
	for _, a := range cand.Apps {
		if a == unknownAppName {
			t.Fatalf("apps must not contain %q placeholder, got %v", unknownAppName, cand.Apps)
		}
	}
	if len(cand.Apps) != 1 || cand.Apps[0] != "cmux" {
		t.Errorf("expected apps=[cmux], got %v", cand.Apps)
	}
}

// TestRepoLinkResolvesGitRemoteSlug locks the recall fix: a terminal path whose
// directory name differs from the GitHub slug still yields the canonical slug,
// so it can stitch with the browser/chat repo entity (sth-airjelly vs
// Isoform/something acceptance gap).
func TestRepoLinkResolvesGitRemoteSlug(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = git@github.com:Isoform/something.git\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Observation{Body: "ran: cd " + dir + " && git log --oneline"}
	ents := repoLinkEntities(o)
	if !hasEntity(ents, entityRepo, "Isoform/something") {
		t.Fatalf("expected repo Isoform/something from git remote, got %+v", ents)
	}
	if !hasEntity(ents, entityProject, "something") {
		t.Errorf("expected project something, got %+v", ents)
	}
}

// TestRepresentativeWeChatSnapshot locks the keyboard-input coarse filter: the
// WeChat VLM is fed only when the user actually typed (a keyboard frame), not
// when passively browsing the chat list — which bounds vision cost.
func TestRepresentativeWeChatSnapshot(t *testing.T) {
	snaps := []ActivitySnapshotRow{
		{ID: 1, Trigger: "click", FilePath: "a.jpg", FocusRectW: 0.5, FocusRectH: 0.5},
		{ID: 2, Trigger: "keyboard", FilePath: "b.jpg", FocusRectW: 0.5, FocusRectH: 0.5},
		{ID: 3, Trigger: "visual_change", FilePath: "c.jpg", FocusRectW: 0.5, FocusRectH: 0.5},
	}
	// A keyboard frame is PREFERRED when present, even if a later non-keyboard
	// frame exists (keyboard = fresh, captured while typing).
	got, ok := representativeWeChatSnapshot(&Observation{SnapshotIDs: []int64{1, 2, 3}}, snaps)
	if !ok || got.ID != 2 {
		t.Errorf("expected preferred keyboard frame id=2, got id=%d ok=%v", got.ID, ok)
	}
	// No keyboard frame -> fall back to the LAST valid frame (any trigger).
	// WeChat snapshots are sparse and rarely keyboard-triggered, so a
	// keyboard-only gate would skip almost every real conversation.
	got, ok = representativeWeChatSnapshot(&Observation{SnapshotIDs: []int64{1, 3}}, snaps)
	if !ok || got.ID != 3 {
		t.Errorf("expected fallback to last valid frame id=3, got id=%d ok=%v", got.ID, ok)
	}
	// Any frame but NO focus rect -> skipped: cropping would fall back to the
	// full screen and the VLM would misidentify the window (browser-instead-of-chat).
	noRect := []ActivitySnapshotRow{
		{ID: 4, Trigger: "keyboard", FilePath: "d.jpg", FocusRectW: 0, FocusRectH: 0},
		{ID: 5, Trigger: "click", FilePath: "e.jpg", FocusRectW: 0, FocusRectH: 0},
	}
	if _, ok := representativeWeChatSnapshot(&Observation{SnapshotIDs: []int64{4, 5}}, noRect); ok {
		t.Error("frames without a focus rect should be skipped")
	}
}

// TestDropPreTodayConversation locks the per-day cut: a pre-today message (whether
// absolute-dated or 昨天/前天) is dropped whole — including its own body — and only
// today's bare-time messages remain; an all-historical body falls back to itself.
func TestDropPreTodayConversation(t *testing.T) {
	day := time.Date(2031, 7, 5, 13, 0, 0, 0, time.Local).UnixMilli()
	body := "alex_halcyon [2031/6/21 18:01]: alpha? Priya [昨天13:56]: 示例日程 Priya [10:42]: 草稿已更新"
	got := dropPreTodayConversation(body, day)
	for _, drop := range []string{"alpha", "日程", "昨天"} {
		if strings.Contains(got, drop) {
			t.Errorf("should drop pre-today content %q, got %q", drop, got)
		}
	}
	if !strings.Contains(got, "草稿") {
		t.Errorf("should keep today's message, got %q", got)
	}
	allOld := "alex_halcyon [2031/6/21 18:01]: only alpha"
	if dropPreTodayConversation(allOld, day) != allOld {
		t.Error("all-historical body should fall back to itself, not be emptied")
	}
}

// TestIsTodayStamp locks the per-app timestamp parsing: every absolute-date form
// observed across Discord / Slack / Feishu must classify older messages as
// not-today, while bare times and explicit today markers stay today. ref = 7/5.
func TestIsTodayStamp(t *testing.T) {
	ref := time.Date(2031, 7, 5, 13, 0, 0, 0, time.Local)
	tests := []struct {
		stamp string
		today bool
	}{
		{"10:42", true},            // bare time (all apps)
		{"今天 10:42", true},         // explicit CJK today
		{"Today at 10:42", true},   // Slack explicit today
		{"7/5/31", true},           // Slack M/D/YY == ref
		{"昨天13:56", false},         // Discord/Feishu relative
		{"前天 08:00", false},        // relative
		{"Yesterday", false},       // Slack relative
		{"2031/6/21 18:01", false}, // Discord Y/M/D
		{"6月29日", false},           // Feishu M月日
		{"04月25日", false},          // Feishu zero-padded M月日
		{"6月21日", false},           // Discord CJK M月日
		{"7/4/31", false},          // Slack M/D/YY, prior day
		{"Mar 30th", false},        // Slack Mon Dth
		{"May 11th", false},        // Slack Mon Dth
		{"2031年7月5日", true},        // CJK Y年M月日 == ref
		{"7月5日", true},             // CJK M月日 == ref
	}
	for _, tc := range tests {
		if got := isTodayStamp(tc.stamp, ref); got != tc.today {
			t.Errorf("isTodayStamp(%q) = %v, want %v", tc.stamp, got, tc.today)
		}
	}
}

// TestCompactTextTail / isConversationApp lock the conversation tail-truncation:
// conversation apps stream oldest-first, so the body must keep the LATEST messages.
func TestCompactTextTail(t *testing.T) {
	if got := compactTextTail("aa bb cc dd", 5); got != "…cc dd" {
		t.Errorf("compactTextTail = %q, want %q", got, "…cc dd")
	}
	if got := compactTextTail("short", 50); got != "short" {
		t.Errorf("compactTextTail short = %q, want short", got)
	}
	if !isConversationApp("Discord") || !isConversationApp("WeChat") || isConversationApp("Chrome") {
		t.Error("isConversationApp classification wrong")
	}
}

// TestOrderFocusFirst locks the thumbnail ordering: focus-valid frames move to
// the front so topThumbnails picks cropped-to-window frames over full-screen ones.
func TestOrderFocusFirst(t *testing.T) {
	eq := func(a, b []int64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	if got := orderFocusFirst([]int64{1, 2, 3, 4}, []int64{2, 4}); !eq(got, []int64{2, 4, 1, 3}) {
		t.Errorf("orderFocusFirst = %v, want [2 4 1 3]", got)
	}
	if got := orderFocusFirst([]int64{1, 2, 3}, nil); !eq(got, []int64{1, 2, 3}) {
		t.Errorf("no focus should be unchanged, got %v", got)
	}
}

// TestSelectThumbnailsDominantApp locks the cover-frame rule: the event's
// dominant app leads (so a "Join Slack huddle" cover is a Slack frame, not the
// background Chrome frame that was frontmost when the notification fired), while
// each group keeps its chronological order. Legacy rows without per-frame apps
// fall back to plain chronological order.
func TestSelectThumbnailsDominantApp(t *testing.T) {
	eq := func(a, b []int64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	// Frame 1 is background Chrome; the huddle (Slack) dominates 3-to-2.
	refs := semanticSourceRefs{
		SnapshotIDs:  []int64{1, 2, 3, 4, 5},
		SnapshotApps: []string{"Chrome", "Slack", "Slack", "Slack", "Chrome"},
	}
	if got := selectThumbnails(refs); !eq(got, []int64{2, 3, 4, 1}) {
		t.Errorf("dominant-app cover = %v, want [2 3 4 1]", got)
	}
	// Unknown ("?") frames never win, even when most frequent.
	unknownHeavy := semanticSourceRefs{
		SnapshotIDs:  []int64{1, 2, 3, 4, 5},
		SnapshotApps: []string{"?", "?", "?", "Slack", "Slack"},
	}
	if got := selectThumbnails(unknownHeavy); !eq(got, []int64{4, 5, 1, 2}) {
		t.Errorf("unknown-heavy cover = %v, want [4 5 1 2]", got)
	}
	// Legacy row: no per-frame apps -> chronological.
	legacy := semanticSourceRefs{SnapshotIDs: []int64{1, 2, 3, 4, 5}}
	if got := selectThumbnails(legacy); !eq(got, []int64{1, 2, 3, 4}) {
		t.Errorf("legacy cover = %v, want [1 2 3 4]", got)
	}
	// Short event (frames <= maxThumbnails): reordering must still run, since
	// the cover is ids[0] even when no truncation is needed. This is the common
	// "Slack huddle invite over a Chrome page" shape that the dominant-app rule
	// exists for; a count-based early return would leave Chrome as the cover.
	short := semanticSourceRefs{
		SnapshotIDs:  []int64{1, 2, 3},
		SnapshotApps: []string{"Chrome", "Slack", "Slack"},
	}
	if got := selectThumbnails(short); !eq(got, []int64{2, 3, 1}) {
		t.Errorf("short-event cover = %v, want [2 3 1]", got)
	}
}

// TestIsOwnActivitySurface verifies our own Activity UI is detected (and later
// dropped), while real cross-language content is not misflagged.
func TestIsOwnActivitySurface(t *testing.T) {
	ownUI := "Activity Events Timeline Summary All recorded data is auto-redacted on device. Passwords API keys"
	if !isOwnActivitySurface(ownUI) {
		t.Error("own Activity UI not detected in ax_text")
	}
	if !isOwnActivitySurface("", ownUI) {
		t.Error("own Activity UI not detected via OCR arg")
	}
	if isOwnActivitySurface("门口最近没摊位 城管赶 华宇广场 中高考 摆摊") {
		t.Error("real WeChat 摆摊 content misflagged as own UI")
	}
}

// TestIsMetaNoiseEvent locks the meta-noise filter: drop mechanical actions and
// "looking at our own activity surface", but keep real work (incl. developing
// the activity system itself).
func TestIsMetaNoiseEvent(t *testing.T) {
	noise := []string{
		"Open WeChat", "Switch to Chrome app", "Glance at WeChat notification",
		"Browse own activity timeline", "Review activity timeline in cmux",
		"Check Airjelly activity notifications", "View activity summary",
	}
	for _, title := range noise {
		if !isMetaNoiseEvent(extractedEvent{Title: title}) {
			t.Errorf("expected meta-noise (should drop): %q", title)
		}
	}
	keep := []string{
		"Research cold brew coffee", "Debug Airjelly semantic events pipeline",
		"Review activity system architecture", "Discuss Isoform cost with Maya Chen",
	}
	for _, title := range keep {
		if isMetaNoiseEvent(extractedEvent{Title: title}) {
			t.Errorf("should NOT be meta-noise (keep): %q", title)
		}
	}
	// Self-admitted empty: the description itself says nothing happened -> drop.
	emptyDesc := []extractedEvent{
		{Title: "Browse WeChat tennis group chats", Description: "User glanced at WeChat. No substantive interaction observed."},
		{Title: "Brief WeChat app interactions", Description: "Momentary WeChat window — just app titles/headers, no real conversation."},
		{Title: "Scroll WeChat groups", Description: "No coherent thread of activity engaged."},
	}
	for _, ev := range emptyDesc {
		if !isMetaNoiseEvent(ev) {
			t.Errorf("expected meta-noise (self-admitted empty): %q", ev.Title)
		}
	}
	// Empty browser new-tab page carries no activity -> drop.
	if !isMetaNoiseEvent(extractedEvent{Title: "Browse Chrome new tab page"}) {
		t.Error("expected meta-noise: empty new-tab page")
	}
	// Substantive browsing (real content) must be kept even with a "Browse" title.
	if isMetaNoiseEvent(extractedEvent{Title: "Browse Halcyon travel guides", Description: "Read example.com itineraries and attractions for Halcyon."}) {
		t.Error("should NOT be meta-noise: substantive browsing")
	}
}

// coObs builds an observation with a real app and raw body/url, then lets the
// PRODUCTION entity-linker fill its entities — so coherence tests run on the same
// data path as live (no hand-injected topics, per review #1).
func coObs(idx int, app, body, url string) Observation {
	return Observation{Index: idx, RealApp: app, Body: body, URL: url, StartMs: int64(idx+1) * 1000, EndMs: int64(idx+1)*1000 + 500}
}

// annotated runs the real annotateEntities over a set of observations.
func annotated(obs ...Observation) ObservationSequence {
	seq := ObservationSequence{Observations: obs}
	annotateEntities(&seq)
	return seq
}

// TestCoherenceClusters locks the deterministic stage of the coherence gate
// (review #1/#3/#6): nodes are OBSERVATIONS, the only deterministic edge is a
// shared repo/pr identity, and entities come from the production annotateEntities
// path. Coherent (single app, or repo/pr links all) -> (nil,false); otherwise the
// repo/pr clusters + needsVerify=true (a content verifier decides the rest).
func TestCoherenceClusters(t *testing.T) {
	const repoURL = "https://github.com/Isoform/something"
	const prURL = "https://github.com/Isoform/something/pull/1955"

	tests := []struct {
		name        string
		seq         ObservationSequence
		ev          extractedEvent
		wantVerify  bool
		wantCluster [][]int
	}{
		{
			name:       "single real app -> coherent (no-op)",
			seq:        annotated(coObs(0, "cmux", "go test ./...", ""), coObs(1, "cmux", "git status", "")),
			ev:         extractedEvent{Title: "Build backend", SourceIndices: []int{0, 1}},
			wantVerify: false,
		},
		{
			// PR is the ONLY deterministic proof of same concrete work (P1): both
			// observations reference the same PR -> coherent without an LLM call.
			name: "cross-app shared PR -> coherent (no-op)",
			seq: annotated(
				coObs(0, "cmux", "gh pr view github.com/Isoform/something/pull/1955", ""),
				coObs(1, "Chrome", "", prURL),
			),
			ev:         extractedEvent{Title: "Work on something PR 1955", SourceIndices: []int{0, 1}},
			wantVerify: false,
		},
		{
			// Same repo but NO shared PR: repo proximity is NOT proof of same work
			// (same repo holds different bugs) -> must go to the verifier (P1).
			name: "cross-app same repo, no PR -> needs verify (repo not sufficient)",
			seq: annotated(
				coObs(0, "cmux", "working in github.com/Isoform/something", ""),
				coObs(1, "Chrome", "", repoURL),
			),
			ev:          extractedEvent{Title: "Work on something", SourceIndices: []int{0, 1}},
			wantVerify:  true,
			wantCluster: [][]int{{0, 1}},
		},
		{
			// Review #1 / the B站 case: cmux carries a repo, Chrome video carries no
			// entity -> repo/pr cannot link them -> needs the content verifier.
			name: "cross-app, repo vs entity-less video -> needs verify",
			seq: annotated(
				coObs(0, "cmux", "building github.com/Isoform/something", ""),
				coObs(1, "Chrome", "bilibili documentary about wildlife", ""),
			),
			ev:          extractedEvent{Title: "Design VLM routing", SourceIndices: []int{0, 1}},
			wantVerify:  true,
			wantCluster: [][]int{{0}, {1}},
		},
		{
			// Pure-topic cross-app research has NO structured entity in production,
			// so the deterministic gate cannot prove it coheres -> verifier decides
			// (it must NOT be auto-merged or auto-split here; that is the verifier's call).
			name: "pure-topic cross-app -> needs verify (not auto-decided)",
			seq: annotated(
				coObs(0, "Chrome", "cold brew coffee brewing guide", ""),
				coObs(1, "WeChat", "聊咖啡 cold brew", ""),
			),
			ev:          extractedEvent{Title: "Research cold brew", SourceIndices: []int{0, 1}},
			wantVerify:  true,
			wantCluster: [][]int{{0}, {1}},
		},
		{
			name: "shared repo across apps + unrelated video -> repo cluster + video cluster",
			seq: annotated(
				coObs(0, "cmux", "github.com/Isoform/something tests", ""),
				coObs(1, "Chrome", "", repoURL),
				coObs(2, "Chrome", "youtube music video", ""),
			),
			ev:          extractedEvent{Title: "Mixed", SourceIndices: []int{0, 1, 2}},
			wantVerify:  true,
			wantCluster: [][]int{{0, 1}, {2}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusters, needs := coherenceClusters(tc.ev, tc.seq)
			if needs != tc.wantVerify {
				t.Fatalf("needsVerify = %v, want %v (clusters=%v)", needs, tc.wantVerify, clusters)
			}
			if tc.wantCluster != nil && !clustersEqual(clusters, tc.wantCluster) {
				t.Errorf("clusters = %v, want %v", clusters, tc.wantCluster)
			}
		})
	}
}

// TestApplyCoherenceOrchestration locks the gate orchestration with an injected
// verifier (no LLM): coherent passthrough, verifier repartition + index remap,
// budget-exhaustion deterministic fallback (#5), verifier-error abort (#2), and
// the dropped-strong-cluster safety net (#4).
func TestApplyCoherenceOrchestration(t *testing.T) {
	ctx := context.Background()
	// cmux repo vs entity-less Chrome video -> coherenceClusters returns 2 clusters.
	seq := annotated(
		coObs(0, "cmux", "building github.com/Isoform/something", ""),
		coObs(1, "Chrome", "bilibili documentary about wildlife", ""),
	)
	ev := extractedEvent{Title: "Design VLM routing", Category: "build", SourceIndices: []int{0, 1}}

	t.Run("coherent event passes through without calling verifier", func(t *testing.T) {
		single := annotated(coObs(0, "cmux", "go test", ""), coObs(1, "cmux", "git push", ""))
		called := false
		verify := func(context.Context, ObservationSequence) ([]extractedEvent, error) {
			called = true
			return nil, nil
		}
		out, err := applyCoherence(ctx, []extractedEvent{{Title: "Build", SourceIndices: []int{0, 1}}}, single, &coherenceBudget{remaining: 8}, verify)
		if err != nil || called {
			t.Fatalf("coherent event must not call verifier: called=%v err=%v", called, err)
		}
		if len(out) != 1 {
			t.Fatalf("want 1 passthrough event, got %d", len(out))
		}
	})

	t.Run("verifier repartitions and indices remap to original seq", func(t *testing.T) {
		verify := func(_ context.Context, sub ObservationSequence) ([]extractedEvent, error) {
			return []extractedEvent{
				{Title: "Build something", Category: "build", SourceIndices: []int{0}},
				{Title: "Watch documentary", Category: "explore", SourceIndices: []int{1}},
			}, nil
		}
		out, err := applyCoherence(ctx, []extractedEvent{ev}, seq, &coherenceBudget{remaining: 8}, verify)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 2 {
			t.Fatalf("want 2 events, got %d", len(out))
		}
		gotIdx := map[int]bool{}
		for _, e := range out {
			for _, i := range e.SourceIndices {
				gotIdx[i] = true
			}
		}
		if !gotIdx[0] || !gotIdx[1] {
			t.Errorf("remapped indices = %v, want {0,1}", gotIdx)
		}
	})

	t.Run("budget exhausted -> deterministic split, no verifier call", func(t *testing.T) {
		called := false
		verify := func(context.Context, ObservationSequence) ([]extractedEvent, error) {
			called = true
			return nil, nil
		}
		out, err := applyCoherence(ctx, []extractedEvent{ev}, seq, &coherenceBudget{remaining: 0}, verify)
		if err != nil || called {
			t.Fatalf("exhausted budget must not call verifier: called=%v err=%v", called, err)
		}
		if len(out) != 2 {
			t.Fatalf("deterministic split want 2 events, got %d", len(out))
		}
		for _, e := range out {
			if e.Title == ev.Title {
				t.Errorf("deterministic split must not reuse merged title %q", ev.Title)
			}
		}
	})

	t.Run("verifier error aborts pass (stays pending)", func(t *testing.T) {
		verify := func(context.Context, ObservationSequence) ([]extractedEvent, error) {
			return nil, errTestVerify
		}
		if _, err := applyCoherence(ctx, []extractedEvent{ev}, seq, &coherenceBudget{remaining: 8}, verify); err == nil {
			t.Fatal("verifier error must propagate up")
		}
	})

	t.Run("safety net restores a repo/pr cluster the verifier dropped", func(t *testing.T) {
		two := annotated(
			coObs(0, "cmux", "github.com/Isoform/something work", ""),
			coObs(1, "Chrome", "", "https://github.com/Isoform/other"),
		)
		evTwo := extractedEvent{Title: "Mixed repos", Category: "build", SourceIndices: []int{0, 1}}
		verify := func(_ context.Context, sub ObservationSequence) ([]extractedEvent, error) {
			return []extractedEvent{{Title: "Work on something", Category: "build", SourceIndices: []int{0}}}, nil
		}
		out, err := applyCoherence(ctx, []extractedEvent{evTwo}, two, &coherenceBudget{remaining: 8}, verify)
		if err != nil {
			t.Fatal(err)
		}
		covered := map[int]bool{}
		for _, e := range out {
			for _, i := range e.SourceIndices {
				covered[i] = true
			}
		}
		if !covered[1] {
			t.Errorf("safety net must restore dropped repo cluster (idx 1), out=%v", out)
		}
	})

	// Same repo across two apps, no PR -> ONE repo cluster but needsVerify=true.
	// The fallback paths (budget exhausted / permanent error) must NOT pass the
	// original over-merge through; they must split by app (P1 fallback fix).
	sameRepo := annotated(
		coObs(0, "cmux", "working in github.com/Isoform/something", ""),
		coObs(1, "Chrome", "", "https://github.com/Isoform/something"),
	)
	evSameRepo := extractedEvent{Title: "Over-merged same repo", Category: "build", SourceIndices: []int{0, 1}}

	assertSplitNotOriginal := func(t *testing.T, out []extractedEvent) {
		t.Helper()
		if len(out) != 2 {
			t.Fatalf("single-cluster fallback must split by app into 2 events, got %d: %v", len(out), out)
		}
		covered := map[int]bool{}
		for _, e := range out {
			if e.Title == evSameRepo.Title {
				t.Errorf("fallback must NOT pass the original over-merged event through")
			}
			for _, i := range e.SourceIndices {
				covered[i] = true
			}
		}
		if !covered[0] || !covered[1] {
			t.Errorf("fallback must keep all observations, covered=%v", covered)
		}
	}

	t.Run("single repo cluster + budget exhausted -> split by app, not original", func(t *testing.T) {
		verify := func(context.Context, ObservationSequence) ([]extractedEvent, error) {
			return nil, fmt.Errorf("verifier must not be called")
		}
		out, err := applyCoherence(ctx, []extractedEvent{evSameRepo}, sameRepo, &coherenceBudget{remaining: 0}, verify)
		if err != nil {
			t.Fatal(err)
		}
		assertSplitNotOriginal(t, out)
	})

	t.Run("single repo cluster + permanent verifier error -> split by app, not original", func(t *testing.T) {
		verify := func(context.Context, ObservationSequence) ([]extractedEvent, error) {
			return nil, fmt.Errorf("HTTP 400 high risk content")
		}
		out, err := applyCoherence(ctx, []extractedEvent{evSameRepo}, sameRepo, &coherenceBudget{remaining: 8}, verify)
		if err != nil {
			t.Fatalf("permanent error must not abort the pass: %v", err)
		}
		assertSplitNotOriginal(t, out)
	})

	t.Run("incomplete verifier coverage -> deterministic fallback, not original, no abort", func(t *testing.T) {
		verify := func(context.Context, ObservationSequence) ([]extractedEvent, error) {
			return nil, errVerifyIncomplete
		}
		out, err := applyCoherence(ctx, []extractedEvent{evSameRepo}, sameRepo, &coherenceBudget{remaining: 8}, verify)
		if err != nil {
			t.Fatalf("incomplete coverage must fall back, not abort: %v", err)
		}
		assertSplitNotOriginal(t, out)
	})

	t.Run("verifier double-referencing an index is deduped to one event", func(t *testing.T) {
		// Verifier (sub space) puts index 0 into BOTH events; dedup keeps it once.
		verify := func(_ context.Context, sub ObservationSequence) ([]extractedEvent, error) {
			return []extractedEvent{
				{Title: "A", Category: "build", SourceIndices: []int{0}},
				{Title: "B", Category: "explore", SourceIndices: []int{0, 1}},
			}, nil
		}
		out, err := applyCoherence(ctx, []extractedEvent{ev}, seq, &coherenceBudget{remaining: 8}, verify)
		if err != nil {
			t.Fatal(err)
		}
		count := map[int]int{}
		for _, e := range out {
			for _, i := range e.SourceIndices {
				count[i]++
			}
		}
		if count[0] != 1 {
			t.Errorf("index 0 must appear exactly once after dedup, got %d (out=%v)", count[0], out)
		}
	})
}

// TestVerifyCoverageComplete locks the dropped_indices completeness rule: every
// input index must be in an event OR dropped; an undeclared index (silent
// omission) is incomplete -> caller falls back instead of trusting the partition.
func TestVerifyCoverageComplete(t *testing.T) {
	tests := []struct {
		name    string
		events  []extractedEvent
		dropped []int
		n       int
		want    bool
	}{
		{
			name:   "all indices covered by events",
			events: []extractedEvent{{SourceIndices: []int{0, 1}}, {SourceIndices: []int{2}}},
			n:      3, want: true,
		},
		{
			name:    "covered by events + dropped",
			events:  []extractedEvent{{SourceIndices: []int{0}}},
			dropped: []int{1, 2},
			n:       3, want: true,
		},
		{
			name:   "index 2 silently omitted -> incomplete",
			events: []extractedEvent{{SourceIndices: []int{0, 1}}},
			n:      3, want: false,
		},
		{
			name:   "out-of-range index ignored, real one still missing",
			events: []extractedEvent{{SourceIndices: []int{0, 99}}},
			n:      2, want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifyCoverageComplete(tc.events, tc.dropped, tc.n); got != tc.want {
				t.Errorf("verifyCoverageComplete = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSubSequence locks the sub-sequence span aggregation: a valid first
// observation with StartMs==0 must NOT be treated as an unset sentinel and
// overwritten by a later, larger StartMs (the min/max must stay correct), and
// indices must be reindexed 0..n-1 with a mapping back to the original sequence.
func TestSubSequence(t *testing.T) {
	seq := ObservationSequence{Observations: []Observation{
		{Index: 0, RealApp: "cmux", StartMs: 0, EndMs: 100}, // valid obs at epoch 0
		{Index: 1, RealApp: "Chrome", StartMs: 5000, EndMs: 5200},
		{Index: 2, RealApp: "WeChat", StartMs: 3000, EndMs: 3100},
		{Index: 3, RealApp: "Slack", StartMs: 4000, EndMs: 4050},
	}}
	// Pick a subset out of order; only indices 0,2,3 (skip 1 and an out-of-range).
	sub, mapping := subSequence(seq, []int{2, 0, 3, 99})

	if sub.StartMs != 0 {
		t.Errorf("StartMs = %d, want 0 (min must not be overwritten by the sentinel)", sub.StartMs)
	}
	if sub.EndMs != 4050 {
		t.Errorf("EndMs = %d, want 4050 (max across the subset)", sub.EndMs)
	}
	if len(sub.Observations) != 3 {
		t.Fatalf("want 3 observations (out-of-range 99 skipped), got %d", len(sub.Observations))
	}
	// Contiguously reindexed 0..n-1, and mapping projects back to the originals.
	wantOrig := []int{2, 0, 3}
	for i, o := range sub.Observations {
		if o.Index != i {
			t.Errorf("sub.Observations[%d].Index = %d, want %d", i, o.Index, i)
		}
		if mapping[i] != wantOrig[i] {
			t.Errorf("mapping[%d] = %d, want %d", i, mapping[i], wantOrig[i])
		}
	}
}

var errTestVerify = fmt.Errorf("verify boom")

// clustersEqual compares two partitions set-wise (each a sorted index slice).
func clustersEqual(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, ca := range a {
		matched := false
		for j, cb := range b {
			if used[j] || len(ca) != len(cb) {
				continue
			}
			same := true
			for k := range ca {
				if ca[k] != cb[k] {
					same = false
					break
				}
			}
			if same {
				used[j] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func hasEntity(ents []Entity, typ, name string) bool {
	for _, e := range ents {
		if e.Type == typ && e.Name == name {
			return true
		}
	}
	return false
}

// TestGateMerge covers the §6 conservative cross-app gating contract.
func TestGateMerge(t *testing.T) {
	repoEnt := []Entity{{Type: entityRepo, Name: "Isoform/something"}}
	personEnt := []Entity{{Type: entityPerson, Name: "Maya Chen"}}

	tests := []struct {
		name      string
		a, b      eventCandidate
		wantMerge bool
	}{
		{
			name:      "cross-app shared repo merges (the something-preview / computer-use case)",
			a:         eventCandidate{Apps: []string{"cmux"}, Entities: repoEnt, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"Slack"}, Entities: repoEnt, StartMs: 600000, EndMs: 700000},
			wantMerge: true,
		},
		{
			name:      "cross-app no shared strong entity never merges (even adjacent)",
			a:         eventCandidate{Apps: []string{"cmux"}, Entities: []Entity{{Type: entityProject, Name: "ohmyzsh"}}, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"Chrome"}, Entities: repoEnt, StartMs: 1100, EndMs: 2000},
			wantMerge: false,
		},
		{
			name:      "person-only cross-app merges when time-proximate",
			a:         eventCandidate{Apps: []string{"Slack"}, Entities: personEnt, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"WeChat"}, Entities: personEnt, StartMs: 60000, EndMs: 70000},
			wantMerge: true,
		},
		{
			name:      "same app adjacent does NOT merge without a shared strong entity",
			a:         eventCandidate{Apps: []string{"cmux"}, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"cmux"}, StartMs: 2000, EndMs: 3000},
			wantMerge: false,
		},
		{
			name:      "same app with shared repo merges",
			a:         eventCandidate{Apps: []string{"cmux"}, Entities: repoEnt, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"cmux"}, Entities: repoEnt, StartMs: 2000, EndMs: 3000},
			wantMerge: true,
		},
		{
			name:      "same repo far apart is STILL a candidate (time no longer decides — day boundary + LLM do)",
			a:         eventCandidate{Apps: []string{"cmux"}, Entities: repoEnt, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"cmux"}, Entities: repoEnt, StartMs: 3 * 60 * 60 * 1000, EndMs: 3*60*60*1000 + 1000},
			wantMerge: true,
		},
		{
			name:      "DJI: inconsistent topic names merge via fuzzy subject",
			a:         eventCandidate{Apps: []string{"Chrome"}, Entities: []Entity{{Type: entityTopic, Name: "DJI Pocket 2 camera"}}, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"WeChat"}, Entities: []Entity{{Type: entityTopic, Name: "DJI Pocket 2 camera review"}}, StartMs: 60000, EndMs: 70000},
			wantMerge: true,
		},
		{
			name:      "DJI: topic vs project name merge via fuzzy subject",
			a:         eventCandidate{Apps: []string{"Chrome"}, Entities: []Entity{{Type: entityTopic, Name: "DJI Pocket 2 camera"}}, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"cmux"}, Entities: []Entity{{Type: entityProject, Name: "DJI Pocket 2"}}, StartMs: 60000, EndMs: 70000},
			wantMerge: true,
		},
		{
			name:      "single shared token, time-DISTANT does NOT over-connect (>10min apart)",
			a:         eventCandidate{Entities: []Entity{{Type: entityTopic, Name: "Sony A7 camera"}}, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Entities: []Entity{{Type: entityTopic, Name: "Sony FX3 camera"}}, StartMs: 11 * 60 * 1000, EndMs: 11*60*1000 + 1000},
			wantMerge: false,
		},
		{
			name:      "single shared token, time-ADJACENT becomes candidate (Exampleton vs Halcyon travel, <2min)",
			a:         eventCandidate{Apps: []string{"Chrome"}, Entities: []Entity{{Type: entityTopic, Name: "Exampleton travel"}}, StartMs: 1000, EndMs: 52000},
			b:         eventCandidate{Apps: []string{"WeChat"}, Entities: []Entity{{Type: entityTopic, Name: "Halcyon travel"}}, StartMs: 104000, EndMs: 121000},
			wantMerge: true,
		},
		{
			name:      "no shared token, time-adjacent stays out (coffee vs travel, the false-merge guard)",
			a:         eventCandidate{Apps: []string{"cmux"}, Entities: []Entity{{Type: entityTopic, Name: "cold brew coffee"}}, StartMs: 0, EndMs: 1000},
			b:         eventCandidate{Apps: []string{"Chrome"}, Entities: []Entity{{Type: entityTopic, Name: "Exampleton travel"}}, StartMs: 19000, EndMs: 70000},
			wantMerge: false,
		},
		{
			name:      "single shared token, time-adjacent but missing timestamps stays out",
			a:         eventCandidate{Entities: []Entity{{Type: entityTopic, Name: "Exampleton travel"}}, StartMs: 0, EndMs: 0},
			b:         eventCandidate{Entities: []Entity{{Type: entityTopic, Name: "Halcyon travel"}}, StartMs: 0, EndMs: 0},
			wantMerge: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := gateMerge(tc.a, tc.b); got != tc.wantMerge {
				t.Errorf("gateMerge = %v, want %v", got, tc.wantMerge)
			}
		})
	}
}
