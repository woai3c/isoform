package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/internal/pii"
)

func snapshotTestTime() time.Time {
	return time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
}

func snapshotTestHash() [32]byte {
	var h [32]byte
	copy(h[:], "0123456789abcdef0123456789abcdef")
	return h
}

func testTopicSnapshot(id string) *topicSnapshot {
	return &topicSnapshot{
		Version:    topicSnapshotSchemaVersion,
		PolicyHash: snapshotTestHash(),
		At:         snapshotTestTime(),
		Topics: []topicSnapshotEntry{{
			Topic: TopicView{ID: id, Kind: TopicKindProject, Name: "Topic " + id, Status: TopicStatusToday},
			Refs: []topicSourceReference{
				{Kind: topicRefApp, Value: "com.example." + id},
				{Kind: topicRefSession, Value: "session-" + id},
				{Kind: topicRefTimeRange, Start: 100, End: 200},
			},
		}},
	}
}

func TestTopicSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := newTopicSnapshotStore(dir)
	want := testTopicSnapshot("round-trip")
	if err := store.Replace(want); err != nil {
		t.Fatal(err)
	}
	if got := store.Current(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Current mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	fresh := newTopicSnapshotStore(dir)
	loaded := fresh.Load()
	if loaded == nil || !reflect.DeepEqual(loaded, want) {
		t.Fatalf("Load mismatch:\n got=%+v\nwant=%+v", loaded, want)
	}
	if fresh.Current() != loaded {
		t.Fatal("Load did not become Current")
	}
	// The file on disk carries the schema version, so a future reader can
	// reject it.
	raw, err := os.ReadFile(fresh.path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk topicSnapshot
	if err := json.Unmarshal(raw, &onDisk); err != nil || onDisk.Version != topicSnapshotSchemaVersion {
		t.Fatalf("on-disk schema version missing: %v %+v", err, onDisk)
	}
}

func TestTopicSnapshotAtomicReplaceSurvivesCrash(t *testing.T) {
	dir := t.TempDir()
	store := newTopicSnapshotStore(dir)
	if err := store.Replace(testTopicSnapshot("first")); err != nil {
		t.Fatal(err)
	}
	// A crash between the temp write and the rename leaves only a partial temp
	// file. Simulate it: write a half-written temp, never rename it.
	tmp := store.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(`{"version":1,"topics":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := store.Load()
	if loaded == nil || len(loaded.Topics) != 1 || loaded.Topics[0].Topic.ID != "first" {
		t.Fatalf("committed snapshot was corrupted by a partial temp: %+v", loaded)
	}
	// A later replace overwrites the temp and renames cleanly.
	if err := store.Replace(testTopicSnapshot("second")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("temp file left behind after a successful replace")
	}
	if got := store.Current(); got == nil || got.Topics[0].Topic.ID != "second" {
		t.Fatalf("replace after crash: %+v", got)
	}
}

func TestTopicSnapshotCorruptFileIsNoSnapshot(t *testing.T) {
	dir := t.TempDir()
	store := newTopicSnapshotStore(dir)
	if err := os.WriteFile(store.path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store.Load() != nil {
		t.Fatal("corrupt file loaded as a snapshot")
	}
	if store.Current() != nil {
		t.Fatal("corrupt file became Current")
	}
}

func TestTopicSnapshotForeignSchemaIsNoSnapshot(t *testing.T) {
	dir := t.TempDir()
	for _, version := range []int{0, 2, 999} {
		store := newTopicSnapshotStore(dir)
		raw, err := json.Marshal(map[string]any{"version": version, "topics": []any{}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store.path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if store.Load() != nil {
			t.Fatalf("schema version %d loaded as a snapshot", version)
		}
	}
}

func TestTopicSnapshotReplaceNilPublishesEmpty(t *testing.T) {
	dir := t.TempDir()
	store := newTopicSnapshotStore(dir)
	if err := store.Replace(testTopicSnapshot("cleared")); err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(nil); err != nil {
		t.Fatal(err)
	}
	if snapshot := store.Current(); snapshot == nil || len(snapshot.Topics) != 0 {
		t.Fatalf("nil replace did not publish an empty snapshot: %+v", snapshot)
	}
	reloaded := newTopicSnapshotStore(dir)
	if snapshot := reloaded.Load(); snapshot == nil || len(snapshot.Topics) != 0 {
		t.Fatalf("nil replace did not persist an empty snapshot: %+v", snapshot)
	}
}

func TestTopicSnapshotMigrationPreservesVerifiedView(t *testing.T) {
	dir := t.TempDir()
	store := newTopicsStore(dir)
	snap := newTopicSnapshotStore(dir)
	policyHash, err := currentTopicPolicyHash()
	if err != nil {
		t.Fatal(err)
	}
	view := TopicView{ID: "migrated", Kind: TopicKindProject, Name: "Migrated topic"}
	state := topicsStateFile{Version: topicsStateVersion, Today: &topicTodaySnapshot{Views: []TopicView{view}, Policy: policyHash, At: snapshotTestTime()}}
	if _, err := store.saveLocked(state); err != nil {
		t.Fatal(err)
	}
	svc := &topicsService{store: store, snapshots: snap}
	svc.migrateSnapshotIfNeeded()
	got := snap.Current()
	if got == nil || len(got.Topics) != 1 || got.Topics[0].Topic.ID != "migrated" || got.PolicyHash != policyHash {
		t.Fatalf("migration lost the verified view: %+v", got)
	}
	if len(got.Topics[0].Refs) != 0 {
		t.Fatalf("migration invented references: %+v", got.Topics[0].Refs)
	}
	fresh := newTopicSnapshotStore(dir)
	if loaded := fresh.Load(); loaded == nil || len(loaded.Topics) != 1 {
		t.Fatalf("migration was not persisted: %+v", loaded)
	}
}

func TestTopicSnapshotMigrationSkipsChangedPolicy(t *testing.T) {
	dir := t.TempDir()
	store := newTopicsStore(dir)
	snap := newTopicSnapshotStore(dir)
	state := topicsStateFile{Version: topicsStateVersion, Today: &topicTodaySnapshot{
		Views:  []TopicView{{ID: "x", Kind: TopicKindProject, Name: "X"}},
		Policy: [32]byte{1, 2, 3},
		At:     snapshotTestTime(),
	}}
	if _, err := store.saveLocked(state); err != nil {
		t.Fatal(err)
	}
	svc := &topicsService{store: store, snapshots: snap}
	svc.migrateSnapshotIfNeeded()
	if snap.Current() != nil {
		t.Fatal("a view verified under a changed policy was migrated")
	}
}

func TestTopicSnapshotMigrationSkipsNoView(t *testing.T) {
	dir := t.TempDir()
	store := newTopicsStore(dir)
	snap := newTopicSnapshotStore(dir)
	svc := &topicsService{store: store, snapshots: snap}
	svc.migrateSnapshotIfNeeded()
	if snap.Current() != nil {
		t.Fatal("an empty store produced a snapshot")
	}
}

func TestTopicSnapshotFilterPerScope(t *testing.T) {
	cases := []struct {
		name   string
		ref    topicSourceReference
		policy topicPrivacyPolicy
	}{
		{"app", topicSourceReference{Kind: topicRefApp, Value: "com.example.secret"}, topicPrivacyPolicy{Apps: []string{"com.example.secret"}}},
		{"window", topicSourceReference{Kind: topicRefWindow, Value: "private-window"}, topicPrivacyPolicy{Windows: []string{"private-window"}}},
		{"session", topicSourceReference{Kind: topicRefSession, Value: "session-1"}, topicPrivacyPolicy{Sessions: []string{"session-1"}}},
		{"time range", topicSourceReference{Kind: topicRefTimeRange, Start: 100, End: 200}, topicPrivacyPolicy{TimeRanges: []topicTimeRange{{Start: 150, End: 300}}}},
		{"account", topicSourceReference{Kind: topicRefAccount, Value: "acct-1"}, topicPrivacyPolicy{Accounts: []string{"acct-1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := &topicSnapshot{Version: topicSnapshotSchemaVersion, Topics: []topicSnapshotEntry{{Topic: TopicView{ID: "t", Name: "T"}, Refs: []topicSourceReference{tc.ref}}}}
			if got := filterTopicsSnapshot(snap, tc.policy); len(got) != 0 {
				t.Fatalf("%s reference survived its exclusion: %+v", tc.name, got)
			}
			if got := filterTopicsSnapshot(snap, topicPrivacyPolicy{}); len(got) != 1 {
				t.Fatalf("%s reference dropped under an empty policy: %+v", tc.name, got)
			}
			unrelated := topicPrivacyPolicy{Apps: []string{"other"}, Windows: []string{"other"}, Sessions: []string{"other"}, Accounts: []string{"other"}}
			if got := filterTopicsSnapshot(snap, unrelated); len(got) != 1 {
				t.Fatalf("%s reference dropped under an unrelated policy: %+v", tc.name, got)
			}
		})
	}
}

func TestTopicSnapshotFilterTimeRangeEdges(t *testing.T) {
	entry := func(ref topicSourceReference) topicSnapshotEntry {
		return topicSnapshotEntry{Topic: TopicView{ID: "t", Name: "T"}, Refs: []topicSourceReference{ref}}
	}
	// An open-ended exclusion (End == 0) is an ongoing pause and still catches
	// a source that started before it.
	ongoing := &topicSnapshot{Version: topicSnapshotSchemaVersion, Topics: []topicSnapshotEntry{entry(topicSourceReference{Kind: topicRefTimeRange, Start: 100, End: 200})}}
	if got := filterTopicsSnapshot(ongoing, topicPrivacyPolicy{TimeRanges: []topicTimeRange{{Start: 150}}}); len(got) != 0 {
		t.Fatalf("ongoing exclusion missed its source: %+v", got)
	}
	// A half-open range does not overlap one that merely touches its end.
	touching := &topicSnapshot{Version: topicSnapshotSchemaVersion, Topics: []topicSnapshotEntry{entry(topicSourceReference{Kind: topicRefTimeRange, Start: 100, End: 200})}}
	if got := filterTopicsSnapshot(touching, topicPrivacyPolicy{TimeRanges: []topicTimeRange{{Start: 200, End: 300}}}); len(got) != 1 {
		t.Fatalf("touching ranges overlapped: %+v", got)
	}
	// A range entirely before the exclusion survives.
	before := &topicSnapshot{Version: topicSnapshotSchemaVersion, Topics: []topicSnapshotEntry{entry(topicSourceReference{Kind: topicRefTimeRange, Start: 100, End: 200})}}
	if got := filterTopicsSnapshot(before, topicPrivacyPolicy{TimeRanges: []topicTimeRange{{Start: 300, End: 400}}}); len(got) != 1 {
		t.Fatalf("disjoint ranges overlapped: %+v", got)
	}
}

func benchTopicSnapshot(topics, refsPerTopic int) *topicSnapshot {
	entries := make([]topicSnapshotEntry, 0, topics)
	for n := 0; n < topics; n++ {
		refs := make([]topicSourceReference, 0, refsPerTopic)
		for r := 0; r < refsPerTopic; r++ {
			switch r % 4 {
			case 0:
				refs = append(refs, topicSourceReference{Kind: topicRefApp, Value: fmt.Sprintf("app-%d-%d", n, r)})
			case 1:
				refs = append(refs, topicSourceReference{Kind: topicRefSession, Value: fmt.Sprintf("session-%d-%d", n, r)})
			case 2:
				refs = append(refs, topicSourceReference{Kind: topicRefTimeRange, Start: int64(r), End: int64(r) + 10})
			case 3:
				refs = append(refs, topicSourceReference{Kind: topicRefAccount, Value: fmt.Sprintf("acct-%d-%d", n, r)})
			}
		}
		entries = append(entries, topicSnapshotEntry{Topic: TopicView{ID: fmt.Sprintf("topic-%d", n), Name: fmt.Sprintf("Topic %d", n)}, Refs: refs})
	}
	return &topicSnapshot{Version: topicSnapshotSchemaVersion, Topics: entries}
}

func TestTopicSnapshotFilterBenchmark(t *testing.T) {
	snap := benchTopicSnapshot(100, 1000)
	// Nothing matches, so the filter must scan every reference of every topic.
	policy := topicPrivacyPolicy{
		Apps:       []string{"none"},
		Sessions:   []string{"none"},
		Accounts:   []string{"none"},
		TimeRanges: []topicTimeRange{{Start: -100, End: -50}},
	}
	best := time.Duration(0)
	for i := 0; i < 3; i++ {
		started := time.Now()
		got := filterTopicsSnapshot(snap, policy)
		elapsed := time.Since(started)
		if best == 0 || elapsed < best {
			best = elapsed
		}
		if len(got) != 100 {
			t.Fatalf("filter dropped topics: got=%d want=100", len(got))
		}
	}
	t.Logf("filter 100 topics x 1000 references: best=%s", best)
	if best > 5*time.Millisecond {
		t.Fatalf("filter exceeded budget: %s", best)
	}
}

func TestTopicAppMatcherMatchesPrivacyPredicates(t *testing.T) {
	previousSensitive := pii.GetUserSensitiveApps()
	previousVoice := GetUserVoiceInputApps()
	t.Cleanup(func() {
		pii.SetUserSensitiveApps(previousSensitive)
		SetUserVoiceInputApps(previousVoice)
	})

	if len(activityPrivacyParserApps) == 0 {
		t.Fatal("no activity privacy parsers registered")
	}
	parser := activityPrivacyParserApps[0]
	pii.SetUserSensitiveApps(map[string]string{
		"com.example.private": "Custom Sensitive",
		"com.example.parser":  parser.Label,
		"kr.example.private":  "Private Bundle",
	})
	SetUserVoiceInputApps(map[string]string{
		"com.example.voice.helper": "Voice Helper",
	})

	voiceApps := GetUserVoiceInputApps()
	policyApps := []string{"com.example.policy", "Editor", "K", "something", "\u212Aapp"}
	apps := topicStringSet(policyApps)
	matcher := newTopicAppMatcher(policyApps, voiceApps, 15)
	values := []string{
		"COM.1PASSWORD.DESKTOP",
		"app:COM.1PASSWORD.DESKTOP",
		"  app:com.1password.desktop  ",
		"  App:com.1password.desktop  ",
		"com.example.private.helper",
		"CUSTOM SENSITIVE",
		"  custom sensitive  ",
		parser.Name,
		"com.goodsnooze.MacWhisper",
		"com.goodsnooze.MacWhisper.helper",
		"com.example.voice",
		"com.example.voice.helper",
		"com.example.voice.helper.extra",
		"COM.EXAMPLE.POLICY.helper",
		"EDITOR",
		"Editor.helper",
		"K",
		"ſomething",
		"\u212Aapp",
		"Kapp",
		"kapp",
		"\u212Aeepassxc",
		"\u212AEEPASSXC",
		"\u212Ar.example.private.x",
		"Kr.example.private.x",
		"app-0-0",
		"com.example.safe",
	}
	for _, value := range values {
		want := activitySourceAppIsSensitive(value, value) || topicVoiceInputAppReference(value, voiceApps)
		for app := range apps {
			bundlePrefix := strings.Contains(app, ".") && strings.HasPrefix(strings.ToLower(value), strings.ToLower(app))
			if strings.EqualFold(value, app) || bundlePrefix {
				want = true
			}
		}
		if got := matcher.excludes(value); got != want {
			t.Errorf("matcher excludes %q = %v, old predicates = %v", value, got, want)
		}
	}

	asciiPolicyApps := []string{"com.example.ascii"}
	asciiMatcher := newTopicAppMatcher(asciiPolicyApps, voiceApps, 1)
	asciiValue := "COM.EXAMPLE.ASCII.helper"
	if asciiMatcher.hasPolicyUnicode {
		t.Fatal("ASCII-only policy was marked as Unicode")
	}
	if !topicAppMatcherMayMatch(asciiMatcher, asciiValue) {
		t.Fatal("ASCII-only policy skipped its fast-path candidate")
	}
	want := activitySourceAppIsSensitive(asciiValue, asciiValue) || topicVoiceInputAppReference(asciiValue, voiceApps)
	for _, app := range asciiPolicyApps {
		bundlePrefix := strings.Contains(app, ".") && strings.HasPrefix(strings.ToLower(asciiValue), strings.ToLower(app))
		if strings.EqualFold(asciiValue, app) || bundlePrefix {
			want = true
		}
	}
	if got := asciiMatcher.excludes(asciiValue); got != want {
		t.Errorf("ASCII-only matcher excludes %q = %v, old predicates = %v", asciiValue, got, want)
	}
}

func BenchmarkFilterTopicsSnapshot(b *testing.B) {
	snap := benchTopicSnapshot(100, 1000)
	policy := topicPrivacyPolicy{Apps: []string{"none"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filterTopicsSnapshot(snap, policy)
	}
}

func TestTopicReferencesForRuns(t *testing.T) {
	runs := map[string]ActivityTimelineRunRow{
		"run-ok":        {ID: "run-ok", AppKey: "com.example.code", AppName: "Code", SessionIDs: []string{"s-ok"}},
		"run-sensitive": {ID: "run-sensitive", AppKey: "com.1password.8", AppName: "1Password", SessionIDs: []string{"s-private"}},
		"run-missing":   {ID: "run-missing", AppKey: "com.example.missing", AppName: "Missing", SessionIDs: []string{"s-missing"}},
	}
	sessions := map[string]*ActivitySessionRow{
		"s-ok": {ID: "s-ok", StartTime: 100, EndTime: 200, AppNames: `["Code"]`, MeetingApp: "Zoom"},
	}
	refs := topicReferencesForRuns([]string{"run-ok", "run-sensitive", "run-missing"}, runs, sessions)
	byKey := map[string]bool{}
	for _, ref := range refs {
		byKey[ref.key()] = true
	}
	for _, want := range []topicSourceReference{
		{Kind: topicRefApp, Value: "com.example.code"},
		{Kind: topicRefApp, Value: "Code"},
		{Kind: topicRefSession, Value: "s-ok"},
		{Kind: topicRefTimeRange, Start: 100, End: 200},
		{Kind: topicRefApp, Value: "Zoom"},
	} {
		if !byKey[want.key()] {
			t.Fatalf("missing reference %+v in %+v", want, refs)
		}
	}
	for _, ref := range refs {
		if ref.Value == "com.1password.8" || ref.Value == "1Password" || ref.Value == "s-private" || ref.Value == "s-missing" {
			t.Fatalf("sensitive or missing source leaked into references: %+v", ref)
		}
	}
}

func TestTopicViewSourceReferencesFollowingUsesActivityEvidence(t *testing.T) {
	refsByActivity := map[string][]topicSourceReference{
		"a1": {{Kind: topicRefSession, Value: "s1"}},
		"a2": {{Kind: topicRefApp, Value: "com.example.app"}},
	}
	stored := []interests.Interest{{
		ID: "followed",
		Evidence: []interests.Ref{
			{Source: interests.SourceActivity, Key: "activity:a1"},
			{Source: interests.SourceActivity, Key: "activity:a2"},
			{Source: interests.SourceGoal, Key: "goal:g1"},
		},
	}}
	result := topicRegistryResult{views: []TopicView{{ID: "followed", Kind: TopicKindFollowing, Name: "Followed"}}}
	refs := topicViewSourceReferences(result.views[0], result, refsByActivity, stored)
	if len(refs) != 5 {
		t.Fatalf("following references: %+v", refs)
	}
	want := map[string]bool{
		"activity:a1": true, "activity:a2": true, "goal:g1": true,
		"s1": true, "com.example.app": true,
	}
	for _, ref := range refs {
		delete(want, ref.Value)
	}
	if len(want) != 0 {
		t.Fatalf("following source references missing: %+v", want)
	}
}

func TestTopicSnapshotSavedAfterBuild(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) == 0 {
		t.Fatalf("build produced no views: %+v %v", views, err)
	}
	snap := svc.snapshots.Current()
	if snap == nil || len(snap.Topics) == 0 {
		t.Fatal("a successful build did not save a snapshot")
	}
	if len(snap.Topics) != len(views) {
		t.Fatalf("snapshot topics=%d views=%d", len(snap.Topics), len(views))
	}
	var hasSession, hasBundle, hasAppName, hasTimeRange bool
	for _, entry := range snap.Topics {
		for _, ref := range entry.Refs {
			switch {
			case ref.Kind == topicRefSession && ref.Value == "session-atlas":
				hasSession = true
			case ref.Kind == topicRefApp && ref.Value == "com.example.code":
				hasBundle = true
			case ref.Kind == topicRefApp && ref.Value == "Code":
				hasAppName = true
			case ref.Kind == topicRefTimeRange:
				hasTimeRange = true
			}
		}
	}
	if !hasSession || !hasBundle || !hasAppName || !hasTimeRange {
		t.Fatalf("snapshot lost provenance scopes (session=%t bundle=%t name=%t time=%t): %+v", hasSession, hasBundle, hasAppName, hasTimeRange, snap.Topics)
	}
}

func TestTopicSnapshotPublicationKeepsStabilizedOrder(t *testing.T) {
	a, svc := newTopicsReadPathTestApp(t)
	now := time.Now()
	previous := []TopicView{
		{ID: "A", Kind: TopicKindProject, Name: "Atlas", PinnedToSidebar: true},
		{ID: "B", Kind: TopicKindProject, Name: "Beta", PinnedToSidebar: true},
		{ID: "C", Kind: TopicKindProject, Name: "Cedar", PinnedToSidebar: true},
	}
	if _, err := svc.store.update(func(state *topicsStateFile) {
		state.Today = &topicTodaySnapshot{Views: previous, At: now}
	}); err != nil {
		t.Fatal(err)
	}
	state := svc.store.load()
	svc.registryWorkerMu.Lock()
	svc.registryRefresh = make(chan struct{}, 1)
	svc.registryWorkerMu.Unlock()
	t.Cleanup(func() {
		svc.registryWorkerMu.Lock()
		svc.registryRefresh = nil
		svc.registryWorkerMu.Unlock()
	})

	result := topicRegistryResult{views: []TopicView{
		{ID: "C", Kind: TopicKindProject, Name: "Cedar", PinnedToSidebar: true},
		{ID: "A", Kind: TopicKindProject, Name: "Atlas updated", PinnedToSidebar: true},
		{ID: "B", Kind: TopicKindProject, Name: "Beta", PinnedToSidebar: true},
	}}
	expected, err := svc.captureRegistryRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cache := &topicRegistryCache{result: result, revision: expected, at: now, until: now.Add(time.Hour)}
	cache.databaseFiles, err = svc.registryDatabaseFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	candidate := buildTopicSnapshot(result, now, expected.policy, map[string][]topicSourceReference{}, nil)
	if err := svc.publishRegistrySnapshot(t.Context(), expected, state, result, cache, candidate); err != nil {
		t.Fatal(err)
	}
	if !svc.commitBuiltSnapshot(candidate, false, false) {
		t.Fatal("candidate snapshot was not committed")
	}
	got, err := a.GetTodayTopics()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, view := range got.Topics {
		order = append(order, view.ID)
	}
	if strings.Join(order, ",") != "A,B,C" || got.Topics[0].Name != "Atlas updated" {
		t.Fatalf("snapshot after stabilized publication: order=%v topics=%+v", order, got.Topics)
	}
}

func TestTopicSnapshotFileIsolation(t *testing.T) {
	// The snapshot lives beside topics.json in the state dir, never touching
	// the activity database or the real data folder.
	dir := t.TempDir()
	store := newTopicSnapshotStore(dir)
	if filepath.Base(store.path) != topicSnapshotFileName {
		t.Fatalf("unexpected snapshot path: %s", store.path)
	}
	if filepath.Dir(store.path) != dir {
		t.Fatalf("snapshot path outside the state dir: %s", store.path)
	}
}
