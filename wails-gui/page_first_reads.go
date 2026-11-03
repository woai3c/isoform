package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// chatFirstPageSize is the chat's first page: its last 50 messages, the page
// the window opens on (frontend lib/chat-page-size.ts). Older ones load on
// scroll.
const chatFirstPageSize = 50

type pageFirstReadWarmer struct {
	mu      sync.Mutex
	ready   bool
	painted bool
	started bool
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}
}

func (a *App) pageFirstReadsPainted() { a.schedulePageFirstReads(false) }
func (a *App) pageFirstReadsReady()   { a.schedulePageFirstReads(true) }

// First paint can occur while onboarding is still mounted. Only affirmative
// completion permits background reads; missing or malformed state fails closed.
func (a *App) pageFirstReadsAllowed(ctx context.Context) bool {
	if ctx.Err() != nil || !a.pageSnapshotAllowed() {
		return false
	}
	raw, err := a.store.GetUIState(ctx, onboardingStateKey)
	var state struct{ Completed bool }
	return err == nil && json.Unmarshal([]byte(raw), &state) == nil && state.Completed
}

func (a *App) schedulePageFirstReads(ready bool) {
	w := &a.pageWarm
	w.mu.Lock()
	if ready {
		w.ready = true
	} else {
		w.painted = true
	}
	w.mu.Unlock()
	a.resumePageFirstReads()
}

func (a *App) resumePageFirstReads() {
	w := &a.pageWarm
	w.mu.Lock()
	if !w.ready || !w.painted || w.started || w.stopped {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	admission, finishAdmission := context.WithTimeout(parent, time.Second)
	if !a.pageFirstReadsAllowed(admission) {
		finishAdmission()
		return
	}
	steps := a.pageFirstReadSteps(admission)
	finishAdmission()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.stopped {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	w.started = true
	w.cancel, w.done = cancel, make(chan struct{})
	done := w.done
	go func() {
		defer close(done)
		defer cancel()
		runPageFirstReads(ctx, steps, a.pageFirstReadsAllowed, 150*time.Millisecond)
	}()
}

// Every query inherits cancellation and a deadline. Idle gaps leave time for
// foreground work; page reads never fan out.
func runPageFirstReads(ctx context.Context, steps []func(context.Context), allowed func(context.Context) bool, gap time.Duration) {
	for _, step := range steps {
		timer := time.NewTimer(gap)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		stepCtx, cancel := context.WithTimeout(ctx, time.Second)
		if stepCtx.Err() != nil || !allowed(stepCtx) {
			cancel()
			return
		}
		step(stepCtx)
		cancel()
	}
}

// Prime backing-store pages, not foreground projections whose legacy readers
// can use background contexts. No personal result is retained. File-backed pages
// prime directory metadata only; source lineage and body reads stay foreground.
func (a *App) pageFirstReadSteps(admission context.Context) []func(context.Context) {
	// Capture the data root before queuing work. A cancelled worker must not
	// follow a later HOME change during test teardown or create directories.
	root, rootErr := flavorDataDir()
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	start, end := day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()
	gated := func(consent string, read func(context.Context)) func(context.Context) {
		// Do not schedule a denied page, even if consent changes before its
		// turn. Admitted pages still recheck immediately before reading.
		if !a.pageWarmConsent(admission, consent) {
			return nil
		}
		return func(ctx context.Context) {
			if !a.pageFirstReadsAllowed(ctx) || !a.pageWarmConsent(ctx, consent) {
				return
			}
			read(ctx)
		}
	}
	steps := []func(context.Context){
		gated("", func(ctx context.Context) {
			// Existing identity only: warming never creates a chat or retries a write.
			if main, err := a.store.LoadMainSession(ctx); err == nil {
				_, _ = a.persistenceService().LoadStreamItemsPage(ctx, main.LocalID, -1, chatFirstPageSize)
			}
		}),
		gated("", func(ctx context.Context) { _, _ = a.store.ReadMainChatView(ctx, start) }),
		gated("", func(ctx context.Context) { _, _ = a.store.ReadMainChatWaitingCandidates(ctx, now.UnixMilli(), "~", 51) }),
		gated("activity", func(ctx context.Context) {
			if a.activity != nil && a.activity.store != nil {
				_, _ = a.activity.store.GetActivitiesInRangeContext(ctx, start, end)
			}
		}),
		gated("activity", func(ctx context.Context) {
			if a.activity != nil && a.activity.store != nil {
				_, _, _ = a.activity.store.loadActivityThreads(ctx, start)
			}
		}),
		gated("activity", func(ctx context.Context) {
			if a.activity != nil && a.activity.store != nil {
				_, _ = a.activity.store.modelSessionsOverlappingContext(ctx, day.AddDate(0, 0, -30).UnixMilli(), end)
			}
		}),
		gated("", func(ctx context.Context) {
			if store, err := a.userPurposeStore(); err == nil {
				_, _ = store.ListDetailsAllContext(ctx)
			}
		}),
		gated(triageEnableMemoryKey, func(ctx context.Context) {
			if rootErr == nil {
				warmPageDirectory(ctx, filepath.Join(root, "memory"))
			}
		}),
		gated(triageEnableKnowledgeKey, func(ctx context.Context) {
			if rootErr == nil {
				warmPageDirectory(ctx, filepath.Join(root, "knowledge"))
			}
		}),
		gated(triageEnableSOPKey, func(ctx context.Context) {
			if a.sopStore != nil {
				warmPageDirectory(ctx, a.sopStore.Dir())
			}
		}),
		gated("people", func(ctx context.Context) { _, _ = a.store.ListPrincipals(ctx) }),
		gated("people", func(ctx context.Context) { _, _ = a.store.GetAllPrincipalStats(ctx) }),
		gated("", func(ctx context.Context) {
			if svc := a.engagementService(); svc != nil {
				_, _ = engagementSettingsView(ctx, svc)
			}
		}),
		gated("", func(ctx context.Context) {
			_, _ = a.pageNativePermissionsContext(ctx, getPlatformPermissions)
			a.pagePermissions.mu.Lock()
			done := a.pagePermissions.running
			a.pagePermissions.mu.Unlock()
			if done != nil {
				select {
				case <-ctx.Done():
				case <-done:
				}
			}
		}),
	}
	admitted := steps[:0]
	for _, step := range steps {
		if step != nil {
			admitted = append(admitted, step)
		}
	}
	return admitted
}

// Optional preloading requires affirmative switches, never the legacy
// "missing means on" foreground default. Imported contacts need current terms.
func (a *App) pageWarmConsent(ctx context.Context, consent string) bool {
	switch consent {
	case "":
		return true
	case "people":
		path, err := contactsConsentPath()
		return ctx.Err() == nil && err == nil && hasCurrentContactsConsent(path)
	case "activity":
		consent = permissionUIStateKey("screen_recording")
	}
	value, err := a.store.GetUIState(ctx, consent)
	return err == nil && value == "true" && ctx.Err() == nil
}

func warmPageDirectory(ctx context.Context, dir string) {
	if ctx.Err() != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if strings.HasSuffix(entry.Name(), ".md") || entry.IsDir() {
			_, _ = os.Lstat(filepath.Join(dir, entry.Name()))
		}
	}
}
