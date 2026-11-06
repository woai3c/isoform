package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// streamPageTestApp prepares a chat with one item and keeps background work
// out of the way: the refresh waits an hour, so a test runs it explicitly.
func streamPageTestApp(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	a, session, _ := registryChatTestApp(t)
	a.streamPages.mu.Lock()
	a.streamPages.settings()
	a.streamPages.launchDelay = time.Hour
	a.streamPages.eventDelay = time.Hour
	a.streamPages.mu.Unlock()
	appendStreamPageItem(t, a, session, "one", "first words")
	// The memory folder counts as settled once it has been still for a moment;
	// a copy's stamp holds only then, as it does in a running app.
	waitForMemorySettle(t, a)
	return a, session
}

func waitForMemorySettle(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot, err := a.memorySnapshot(); err == nil && snapshot.settled {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the memory folder did not settle")
}

func appendStreamPageItem(t *testing.T, a *App, session, id, content string) {
	t.Helper()
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), session, storage.StreamItemRow{ID: id, Kind: "user", Content: content}); err != nil {
		t.Fatal(err)
	}
}

func latestPage(t *testing.T, a *App, session string) StreamItemPageData {
	t.Helper()
	page, err := a.LoadStreamItemsPage(session, -1, 50)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func pageHas(page StreamItemPageData, id string) bool {
	for _, item := range page.Items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func setStreamPageRecomputeWait(a *App, versionWait, recomputeWait time.Duration) {
	a.streamPages.mu.Lock()
	a.streamPages.versionWait = versionWait
	a.streamPages.recomputeWait = recomputeWait
	a.streamPages.mu.Unlock()
}

// While the stream version holds, the first page is served from its saved copy
// with no computation and no permission retry on the read path.
func TestLatestStreamPageIsServedFromItsCopyWhileTheVersionHolds(t *testing.T) {
	a, session := streamPageTestApp(t)
	if page := latestPage(t, a, session); !pageHas(page, "one") {
		t.Fatal("the cold read did not show the stored item")
	}
	computes, retries := a.streamPages.computes.Load(), a.streamPages.retries.Load()
	for i := 0; i < 5; i++ {
		if page := latestPage(t, a, session); !pageHas(page, "one") {
			t.Fatalf("read %d lost the stored item", i)
		}
	}
	if n := a.streamPages.computes.Load(); n != computes {
		t.Fatalf("reads with the version unchanged recomputed %d times", n-computes)
	}
	if n := a.streamPages.retries.Load(); n != retries {
		t.Fatal("a read ran the permission retries; they belong to the background refresh")
	}
}

// A newer item is never hidden by the copy: a changed version recomputes.
func TestLatestStreamPageShowsANewerItemAtOnce(t *testing.T) {
	a, session := streamPageTestApp(t)
	latestPage(t, a, session)
	appendStreamPageItem(t, a, session, "two", "second words")
	if page := latestPage(t, a, session); !pageHas(page, "two") {
		t.Fatal("a newer item was hidden behind the saved copy")
	}
}

// If the version cannot be read in time, the copy is served and the missing
// item arrives with the background refresh.
func TestLatestStreamPageServesTheCopyWhenTheVersionTimesOut(t *testing.T) {
	a, session := streamPageTestApp(t)
	latestPage(t, a, session)
	appendStreamPageItem(t, a, session, "two", "second words")
	setStreamPageRecomputeWait(a, 0, time.Second)
	if page := latestPage(t, a, session); pageHas(page, "two") {
		t.Fatal("the timeout path should serve the copy")
	}
	setStreamPageRecomputeWait(a, streamPageVersionDeadline, streamPageRecomputeWait)
	a.refreshStreamPages()
	if page := latestPage(t, a, session); !pageHas(page, "two") {
		t.Fatal("the refresh did not add the newer item")
	}
}

// A privacy change drops the copy: the next read computes and saves a new one.
func TestLatestStreamPageIsDroppedWhenThePrivacyStampChanges(t *testing.T) {
	a, session := streamPageTestApp(t)
	latestPage(t, a, session)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	before := a.streamPages.computes.Load()
	pii.SetUserSensitiveApps(map[string]string{"com.example.private": "work"})
	latestPage(t, a, session)
	if n := a.streamPages.computes.Load(); n != before+1 {
		t.Fatalf("a privacy change did not recompute the page: %d -> %d", before, n)
	}
}

// Every change to a stored item reaches the next read: an edit, a withdrawal
// (an in-place tool-input change) and a removal.
func TestLatestStreamPageShowsEditsWithdrawalsAndRemovals(t *testing.T) {
	a, session := streamPageTestApp(t)
	appendStreamPageItem(t, a, session, "two", "second words")
	latestPage(t, a, session)
	ctx := t.Context()
	if err := a.store.UpdateStreamItemContent(ctx, "two", "edited words"); err != nil {
		t.Fatal(err)
	}
	if page := latestPage(t, a, session); !streamPageContent(page, "two", "edited words") {
		t.Fatal("an edit did not reach the next read")
	}
	if err := a.store.UpdateStreamItemToolInput(ctx, "two", `{"withdrawn":true}`); err != nil {
		t.Fatal(err)
	}
	if page := latestPage(t, a, session); !streamPageToolInput(page, "two", `{"withdrawn":true}`) {
		t.Fatal("a withdrawal did not reach the next read")
	}
	if err := a.store.DeleteStreamItemByID(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	if page := latestPage(t, a, session); pageHas(page, "two") {
		t.Fatal("a removed item is still shown")
	}
}

func streamPageContent(page StreamItemPageData, id, content string) bool {
	for _, item := range page.Items {
		if item.ID == id {
			return item.Content == content
		}
	}
	return false
}

func streamPageToolInput(page StreamItemPageData, id, toolInput string) bool {
	for _, item := range page.Items {
		if item.ID == id {
			return item.ToolInput == toolInput
		}
	}
	return false
}

func streamPageHasContent(page StreamItemPageData, content string) bool {
	for _, item := range page.Items {
		if item.Content == content {
			return true
		}
	}
	return false
}

// A saved copy that holds a brief lead is dropped by any withdrawal before the
// next read: the withheld words do not appear, and the copy is not served. The
// copy is planted, so the test proves the drop, not the lead's projection.
func TestAWithdrawalDropsASavedCopyThatHoldsABriefLead(t *testing.T) {
	const lead = "Withdrawn brief words"
	withdrawals := map[string]func(){
		"session withdrawal": func() {
			publishPrivacyWithdrawal(privacyWithdrawalScope{Kind: privacyWithdrawalSession, ID: "gone"})
		},
		"brief privacy epoch": briefPrivacyChanged,
		"sensitive app": func() {
			pii.SetUserSensitiveApps(map[string]string{"com.example.secret": "work"})
		},
	}
	for name, withdraw := range withdrawals {
		t.Run(name, func(t *testing.T) {
			previous := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
			a, session := streamPageTestApp(t)
			latestPage(t, a, session)
			key := streamPageKey{session: session, limit: 50}
			stamp := a.streamPageStamp(t.Context())
			snap, ok := readStreamPageSnapshot(key, stamp)
			if !ok {
				t.Fatal("the first read did not save a copy")
			}
			snap.Page.Items[0].Content = lead
			if err := writeStreamPageSnapshot(key, snap); err != nil {
				t.Fatal(err)
			}
			if page := latestPage(t, a, session); !streamPageHasContent(page, lead) {
				t.Fatal("the planted copy was not served before the withdrawal")
			}
			computes := a.streamPages.computes.Load()
			withdraw()
			page := latestPage(t, a, session)
			if streamPageHasContent(page, lead) {
				t.Fatal("withheld brief words were served after a withdrawal")
			}
			if a.streamPages.computes.Load() == computes {
				t.Fatal("the copy was served after a withdrawal; it should have been dropped")
			}
		})
	}
}

// A saved copy survives a restart: the durable privacy epoch is read back from
// the briefs folder, so the first read after a launch serves the copy with no
// computation. The in-memory counters reset on launch; the copy must not depend
// on them.
func TestASavedFirstPageSurvivesARestart(t *testing.T) {
	a, session := streamPageTestApp(t)
	adoptTestBriefPrivacyEpoch(a) // load the briefs folder's epoch, so changes are saved
	briefPrivacyChanged()         // a durable privacy change, saved before the copy
	latestPage(t, a, session)
	computes := a.streamPages.computes.Load()
	restart := func() {
		mobileBriefPrivacyEpoch.Store(0)
		mobileBriefReadEpoch.Store(0)
		mobileBriefPrivacyEpochMu.Lock()
		mobileBriefPrivacyEpochSavedDir = ""
		mobileBriefPrivacyEpochMu.Unlock()
	}
	restart()
	if page := latestPage(t, a, session); !pageHas(page, "one") {
		t.Fatal("the first read after a restart lost the stored item")
	}
	if n := a.streamPages.computes.Load(); n != computes {
		t.Fatalf("a restart dropped the saved first page: %d computations", n-computes)
	}
}

// adoptTestBriefPrivacyEpoch makes the app load the briefs folder's saved
// privacy epoch, which writes later changes to that folder.
func adoptTestBriefPrivacyEpoch(a *App) {
	dir, err := mobileBriefStateDir()
	if err != nil {
		panic(err)
	}
	// The epoch file is written beside the briefs, which exist once a brief is stored.
	if err := os.MkdirAll(filepath.Join(dir, mobileBriefDirName), 0o700); err != nil {
		panic(err)
	}
	_ = a.savedCopyStamp(context.Background())
}
