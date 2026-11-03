package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testContactsScheduler(t *testing.T, src contactSource) (*contactsSyncScheduler, chan string) {
	t.Helper()
	im, _ := contactsTestImporter(t)
	notices := make(chan string, 20)
	s := &contactsSyncScheduler{importer: im, source: src, allowed: func() bool { return true }, wake: make(chan struct{}, 1)}
	s.notify = func() { notices <- s.status() }
	return s, notices
}

func TestContactsSyncGrantAndIncremental(t *testing.T) {
	ctx := context.Background()
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{
		{ID: "a", FullName: "Ana", Phones: []string{"+14155550101"}},
		{ID: "b", FullName: "Bo"},
	}}
	s, _ := testContactsScheduler(t, src)
	s.refresh(ctx)
	if src.calls != 0 {
		t.Fatal("source read before consent")
	}
	if err := recordContactsConsent(s.importer.consentPath, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	s.allowed = func() bool { return false }
	s.refresh(ctx)
	if src.calls != 0 {
		t.Fatal("source read before OS grant")
	}
	s.allowed = func() bool { return true }
	s.refresh(ctx)
	if src.calls != 1 {
		t.Fatalf("grant reads = %d", src.calls)
	}
	ana := principalForHandle(t, s.importer.store, "contacts", "a")
	bo := principalForHandle(t, s.importer.store, "contacts", "b")
	if ana == nil || bo == nil {
		t.Fatal("grant did not import all contacts")
	}
	handlesBefore, _ := s.importer.store.ListAppHandlesByPrincipal(ctx, bo.ID)
	// App-start refresh enumerates the source but leaves unchanged cards alone.
	s.refresh(ctx)
	unchanged, _ := s.importer.store.GetPrincipal(ctx, bo.ID)
	if unchanged.UpdatedAt != bo.UpdatedAt {
		t.Fatal("unchanged contact was rewritten")
	}
	src.list[0].FullName = "Ana Lee"
	src.list[0].Phones = []string{"+14155550102"}
	src.list = append(src.list, systemContact{ID: "c", FullName: "Chen"})
	s.refresh(ctx)
	changed := principalForHandle(t, s.importer.store, "contacts", "a")
	if changed.ID != ana.ID || changed.DisplayName != "Ana Lee" {
		t.Fatal("refresh lost identity or name")
	}
	if principalForHandle(t, s.importer.store, "imessage", "+14155550101") != nil {
		t.Fatal("stale imported phone retained")
	}
	if principalForHandle(t, s.importer.store, "imessage", "+14155550102") == nil {
		t.Fatal("new phone missing")
	}
	if principalForHandle(t, s.importer.store, "contacts", "c") == nil {
		t.Fatal("new contact missing")
	}
	handlesAfter, _ := s.importer.store.ListAppHandlesByPrincipal(ctx, bo.ID)
	if len(handlesBefore) != len(handlesAfter) || handlesBefore[0].ID != handlesAfter[0].ID {
		t.Fatal("unchanged handles replaced")
	}
	last, _ := s.importer.store.GetUIState(ctx, contactsLastImportKey)
	if last == "" || s.status() != "idle" {
		t.Fatal("successful sync has no status")
	}
	// Removing a native card withdraws its source-owned handles and cached photo.
	src.list = src.list[1:]
	s.refresh(ctx)
	if principalForHandle(t, s.importer.store, "imessage", "+14155550102") != nil {
		t.Fatal("deleted card's phone retained")
	}
}

type waitingContactSource struct{ started chan struct{} }

func (s waitingContactSource) source() string { return contactsSourceSystem }
func (s waitingContactSource) contacts(ctx context.Context) ([]systemContact, error) {
	close(s.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestContactsSyncCancel(t *testing.T) {
	src := waitingContactSource{started: make(chan struct{})}
	s, _ := testContactsScheduler(t, src)
	if err := recordContactsConsent(s.importer.consentPath, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); s.refresh(context.Background()) }()
	<-src.started
	s.cancelCurrent()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync cancellation blocked")
	}
	rows, _ := s.importer.store.ListPrincipals(context.Background())
	last, _ := s.importer.store.GetUIState(context.Background(), contactsLastImportKey)
	if len(rows) != 0 || last != "" || s.status() != "idle" {
		t.Fatal("cancelled sync changed state")
	}
}

func TestContactsSyncTicksStartupPauseAndShutdown(t *testing.T) {
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ana"}}}
	s, notices := testContactsScheduler(t, src)
	if err := recordContactsConsent(s.importer.consentPath, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); s.run(ctx, ticks) }()
	waitSync := func() {
		t.Helper()
		for {
			select {
			case state := <-notices:
				if state == "idle" {
					return
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no sync completion")
			}
		}
	}
	waitSync()
	if src.calls != 1 {
		t.Fatal("no app-start sync")
	}
	ticks <- time.Now()
	waitSync()
	if src.calls != 2 {
		t.Fatal("no scheduled refresh")
	}
	if err := s.importer.store.SetUIState(ctx, contactsSyncPausedKey, "true"); err != nil {
		t.Fatal(err)
	}
	s.refresh(ctx)
	if src.calls != 2 {
		t.Fatal("removed contacts were reimported while paused")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestContactsSyncFailureDoesNotAdvanceCheckpoint(t *testing.T) {
	src := &fakeContactSource{kind: contactsSourceSystem, err: errors.New("source failed")}
	s, _ := testContactsScheduler(t, src)
	if err := recordContactsConsent(s.importer.consentPath, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	s.refresh(context.Background())
	last, _ := s.importer.store.GetUIState(context.Background(), contactsLastImportKey)
	if last != "" || s.status() != "error" {
		t.Fatal("failed source reported success")
	}
}

func TestContactsSyncRemovalWinsQueuedRefresh(t *testing.T) {
	src := &fakeContactSource{kind: contactsSourceSystem, list: []systemContact{{ID: "a", FullName: "Ana"}}}
	s, notices := testContactsScheduler(t, src)
	ctx := context.Background()
	if err := recordContactsConsent(s.importer.consentPath, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	contactsImportMu.Lock()
	done := make(chan struct{})
	go func() { defer close(done); s.refresh(ctx) }()
	select {
	case <-notices:
	case <-time.After(2 * time.Second):
		contactsImportMu.Unlock()
		t.Fatal("refresh did not reach import lock")
	}
	// Model removal winning the shared import lock after the worker's gate.
	err := s.importer.store.SetUIState(ctx, contactsSyncPausedKey, "true")
	contactsImportMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if src.calls != 0 {
		t.Fatal("queued refresh reimported after removal")
	}
}
