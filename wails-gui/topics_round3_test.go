package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestTopicChatBudgetExpiryKeepsPreviousCompleteSnapshot(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	seedThreadMoment(t, a, threadFixtureMoment{id: "beta", title: "Beta release", app: "Preview", start: now.Add(-time.Hour), end: now.Add(-time.Minute), entities: []Entity{{Type: entityProject, Name: "Beta"}}})
	before, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(before) != 2 {
		t.Fatalf("before: %+v %v", before, err)
	}
	svc := a.topicsSvc()
	raw, err := os.ReadFile(svc.store.path)
	if err != nil {
		t.Fatal(err)
	}
	var expire context.CancelFunc
	svc.registryChatContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		child, cancel := context.WithCancel(parent)
		expire = cancel
		return child, cancel
	}
	derived := 0
	svc.registryDuringChat = func(string) { derived++; expire() }
	if _, err := collectAndReadTopicViewsForTest(a); err == nil {
		t.Fatal("partial derivation reported success")
	}
	if derived != 1 {
		t.Fatalf("expected expiry mid-derivation, derived=%d", derived)
	}
	after, err := os.ReadFile(svc.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(raw) {
		t.Fatalf("budget erased/replaced saved snapshots: %s", after)
	}
	svc.registryDuringChat = nil
	svc.registryChatContext = nil
	for _, view := range before {
		if _, err := a.ListMainChatTopicItems(view.ID, "", 50); err != nil {
			t.Fatalf("previous panel unavailable: %v", err)
		}
	}
}

func TestTopicSensitiveAdditionScrubsImmediatelyAndKeepsIndependentProject(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprintf("closed_%t", closed), func(t *testing.T) {
			a, _, now := registryChatTestApp(t)
			seedThreadMoment(t, a, threadFixtureMoment{id: "beta", title: "Beta release", app: "Preview", start: now.Add(-time.Hour), end: now.Add(-time.Minute), entities: []Entity{{Type: entityProject, Name: "Beta"}}})
			if _, err := collectAndReadTopicViewsForTest(a); err != nil {
				t.Fatal(err)
			}
			a.closedEyes.enabled.Store(closed)
			if err := a.AddSensitiveApp("com.example.code", "Code"); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(a.topicsSvc().store.path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "Atlas") || strings.Contains(string(raw), "obj:atlas") {
				t.Fatalf("sensitive project remains at rest: %s", raw)
			}
			if !strings.Contains(string(raw), "Beta") {
				t.Fatalf("independent project scrubbed: %s", raw)
			}
			if a.IsClosedEyesModeEnabled() != closed {
				t.Fatal("sensitive change altered Closed Eyes")
			}
		})
	}
}

func TestTopicPageLargeMemoryHashesOutsideBarriers(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	memory, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	knowledge, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("# Fixture\n\n" + strings.Repeat("Ordinary local fixture text. ", 256))
	const perDirectory = 1536
	for _, dir := range []string{memory, knowledge} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < perDirectory; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("fixture-%04d.md", i)), content, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	svc := a.topicsSvc()
	hashes, heldChecks := 0, 0
	var heldTotal, heldMax time.Duration
	svc.registryFilesHashed = func() { hashes++; assertTopicBarriersAvailable(t, a) }
	svc.registryFileStats = func(elapsed time.Duration) {
		if a.closedEyes.mu.TryLock() {
			a.closedEyes.mu.Unlock()
			return
		}
		heldChecks++
		heldTotal += elapsed
		if elapsed > heldMax {
			heldMax = elapsed
		}
	}
	started := time.Now()
	if _, err := a.ListMainChatTopicItems(views[0].ID, "", 50); err != nil {
		t.Fatal(err)
	}
	t.Logf("files=%d bytes=%d, page=%s, content hash passes=%d outside locks, locked metadata checks=%d total=%s max=%s", perDirectory*2, len(content)*perDirectory*2, time.Since(started), hashes, heldChecks, heldTotal, heldMax)
	if hashes != 1 || heldChecks == 0 {
		t.Fatalf("hash/check counts: %d/%d", hashes, heldChecks)
	}
	assertTopicBarriersAvailable(t, a)
	// A same-size in-place edit with restored mtime still invalidates via change time.
	revision, err := a.captureTopicPageRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(memory, "fixture-0000.md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	content[0] = 'X'
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if release, err := a.validateTopicPageRevision(t.Context(), revision); err == nil {
		release()
		t.Fatal("external edit did not invalidate metadata revision")
	}
}

func TestTopicWithdrawalRejectsProjectionPublishedAfterResolution(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	w, err := a.topicWithdrawalForSource("activity", []string{"atlas"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.topicsSvc().store.update(func(st *topicsStateFile) {
		st.Projects = append(st.Projects, topicProject{ID: "late", Name: "Later derivative", ActivityIDs: []string{"atlas"}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.withTopicWithdrawal(w, func() error { return a.activity.store.DeleteActivity("atlas") }); err == nil {
		t.Fatal("stale dependency resolution allowed mutation")
	}
	if row, err := a.activity.store.GetActivityByID("atlas"); err != nil || row == nil {
		t.Fatalf("stale resolution removed source: %+v %v", row, err)
	}
}

func TestTopicSensitiveStartupScrubsStoredNames(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetUIState(t.Context(), "sensitive_apps", `{"com.example.code":"Code"}`); err != nil {
		t.Fatal(err)
	}
	pii.SetUserSensitiveApps(nil)
	a.loadUserSensitiveApps()
	raw, err := os.ReadFile(a.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Atlas") {
		t.Fatalf("startup retained protected project: %s", raw)
	}
}

func TestTopicSensitiveAdditionScrubsMarkdownGoalAncestry(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ id, app, title string }{{"private", "Code", "Confidential roadmap"}, {"independent", "Preview", "Independent plan"}} {
		session := "session-atlas"
		if fixture.id == "independent" {
			now := a.topicsSvc().now()
			seedThreadMoment(t, a, threadFixtureMoment{id: "independent-source", title: "Independent context", app: "Preview", start: now.Add(-time.Hour), end: now.Add(-time.Minute)})
			session = "session-independent-source"
		}
		content := "# Source\n\n- **Apps**: " + fixture.app + "\n- **Activity Session**: " + session + "\n\nOrdinary source observation."
		if err := os.WriteFile(filepath.Join(dir, fixture.id+".md"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), fixture.id, PurposeKindGoal, fixture.title, false, func(in *CreatePurposeOpenLoopInput) {
			in.Evidence[0].SourceKind = "memory_markdown"
			in.Evidence[0].SourceID = fixture.id
			in.Evidence[0].SourceHash = agencyPayloadHash([]byte(content))
		})
	}
	if before, err := collectAndReadTopicViewsForTest(a); err != nil || len(before) != 3 {
		t.Fatalf("before: %+v %v", before, err)
	}
	a.closedEyes.enabled.Store(true)
	if err := a.AddSensitiveApp("com.example.code", "Code"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Confidential roadmap") {
		t.Fatalf("protected Markdown ancestry retained: %s", raw)
	}
	if !strings.Contains(string(raw), "Independent plan") {
		t.Fatalf("independent Markdown project removed: %s", raw)
	}
}

func TestTopicSensitiveAdditionRejectsCorrectedMarkdownVersion(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	old, err := a.saveMemoryEntry("private-version", "# Original\n\n- **Apps**: Code\n\nAn earlier private observation.", false)
	if err != nil {
		t.Fatal(err)
	}
	goal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "prior-version", PurposeKindGoal, "Confidential prior roadmap", false, func(in *CreatePurposeOpenLoopInput) {
		in.Evidence[0].SourceKind = "memory_markdown"
		in.Evidence[0].SourceID = old.ID
		in.Evidence[0].SourceHash = agencyPayloadHash([]byte(strings.TrimSpace(old.Content)))
	})
	if before, err := collectAndReadTopicViewsForTest(a); err != nil || len(before) != 2 {
		t.Fatalf("before: %+v %v", before, err)
	}
	metadata, err := topicMarkdownRevision("memory", old.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise an already-saved direct Markdown chat derivative as well as
	// the real Goal projection; cleanup must qualify both original versions.
	if _, err := a.topicsSvc().store.update(func(st *topicsStateFile) {
		st.ChatTopics = append(st.ChatTopics, topicChatSnapshot{Topic: ChatTopic{ID: "legacy-direct", Name: "Confidential direct summary", Refs: []TodayChatContextRef{{SourceKind: "memory", SourceID: old.ID}}}, MarkdownRevisions: map[string]string{"memory:" + old.ID: metadata}})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveMemoryEntry(old.ID, "# Correction\n\nIndependent current content."); err != nil {
		t.Fatal(err)
	}
	a.closedEyes.enabled.Store(true)
	if err := a.AddSensitiveApp("com.example.code", "Code"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{goal.Current.Title, "Confidential direct summary"} {
		if strings.Contains(string(raw), text) {
			t.Fatalf("superseded private derivative survived: %s", raw)
		}
	}
	current, err := a.GetMemoryEntry(old.ID)
	if err != nil || !strings.Contains(current.Content, "Independent current content") {
		t.Fatalf("source correction removed: %+v %v", current, err)
	}
}

func TestTopicSensitiveAdditionScrubsDirectSessionGoal(t *testing.T) {
	for _, kind := range []string{"activity_session", "meeting"} {
		t.Run(kind, func(t *testing.T) {
			a, _, _ := registryChatTestApp(t)
			goal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "direct-session", PurposeKindGoal, "Confidential session plan", false, func(in *CreatePurposeOpenLoopInput) {
				in.Evidence[0].SourceKind = kind
				in.Evidence[0].SourceID = "session-atlas"
				in.Evidence[0].SourceHash = agencyPayloadHash([]byte("captured session context"))
			})
			if before, err := collectAndReadTopicViewsForTest(a); err != nil || len(before) != 2 {
				t.Fatalf("direct session projection: %+v %v", before, err)
			}
			a.closedEyes.enabled.Store(true)
			if err := a.AddSensitiveApp("com.example.code", "Code"); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(a.topicsSvc().store.path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), goal.Current.Title) {
				t.Fatalf("sensitive session goal retained: %s", raw)
			}
		})
	}
}
