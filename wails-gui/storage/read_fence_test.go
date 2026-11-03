package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestReadRevisionDetectsLocalAndExternalWrites(t *testing.T) {
	for _, writer := range []string{"store connection", "external connection"} {
		t.Run(writer, func(t *testing.T) {
			store, path := proactiveItemsTestStore(t)
			before, err := store.ReadRevision(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			db := store.db
			if writer == "external connection" {
				db, err = sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			}
			if _, err := db.ExecContext(t.Context(), "UPDATE sessions SET label='changed' WHERE local_id='chat'"); err != nil {
				t.Fatal(err)
			}
			after, err := store.ReadRevision(t.Context())
			if err != nil || after == before {
				t.Fatalf("write did not invalidate revision: before=%+v after=%+v err=%v", before, after, err)
			}
			order, err := store.AppendUserStreamItemAtomicIfCurrent(t.Context(), "chat", StreamItemRow{ID: "rejected", Kind: "user"}, before, nil)
			if order != -1 || !errors.Is(err, ErrReadRevisionChanged) {
				t.Fatalf("stale read admitted: order=%d err=%v", order, err)
			}
			rows, err := store.LoadStreamItems(t.Context(), "chat")
			if err != nil || len(rows) != 0 {
				t.Fatalf("stale append became visible: rows=%+v err=%v", rows, err)
			}
		})
	}
}

func TestReadFenceExcludesLocalAndExternalWritersUntilRelease(t *testing.T) {
	store, path := proactiveItemsTestStore(t)
	revision, err := store.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.LockReadRevision(t.Context(), revision)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := store.db.ExecContext(ctx, "UPDATE sessions SET label='private' WHERE local_id='chat'"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("local writer was not fenced: %v", err)
	}
	external, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	if _, err := external.ExecContext(t.Context(), "UPDATE sessions SET label='private' WHERE local_id='chat'"); err == nil {
		t.Fatal("external writer changed the fenced source")
	}
	release()
	release()
	if _, err := store.db.ExecContext(t.Context(), "UPDATE sessions SET label='private' WHERE local_id='chat'"); err != nil {
		t.Fatalf("release did not restore writes: %v", err)
	}
	if release, err := store.LockReadRevision(t.Context(), revision); !errors.Is(err, ErrReadRevisionChanged) {
		if release != nil {
			release()
		}
		t.Fatalf("stale source acquired a fence: %v", err)
	}
}

func TestAppendUserStreamItemAtomicRechecksAfterWaitingForConnection(t *testing.T) {
	store, _ := proactiveItemsTestStore(t)
	revision, err := store.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := store.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	started, result := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := store.AppendUserStreamItemAtomicIfCurrent(t.Context(), "chat", StreamItemRow{ID: "rejected", Kind: "user"}, revision, nil)
		result <- err
	}()
	<-started
	if _, err := conn.ExecContext(t.Context(), "UPDATE sessions SET label='withdrawn' WHERE local_id='chat'"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrReadRevisionChanged) {
			t.Fatalf("queued append failed to detect withdrawal: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued append did not resume")
	}
	rows, err := store.LoadStreamItems(t.Context(), "chat")
	if err != nil || len(rows) != 0 {
		t.Fatalf("queued append published a withdrawn draft: rows=%+v err=%v", rows, err)
	}
}

func TestAppendUserStreamItemAtomicFinalValidationIsInvisibleAndRollsBack(t *testing.T) {
	store, path := proactiveItemsTestStore(t)
	external, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	revision, err := store.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	withdrawn := errors.New("source withdrawn before commit")
	validated := false
	order, err := store.AppendUserStreamItemAtomicIfCurrent(t.Context(), "chat", StreamItemRow{ID: "rejected", Kind: "user", Content: "Withdrawn title"}, revision, func() error {
		validated = true
		var count int
		if err := external.QueryRowContext(t.Context(), "SELECT count(*) FROM stream_items WHERE id='rejected'").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Error("phone reader observed unvalidated history")
		}
		return withdrawn
	})
	if !validated || order != -1 || !errors.Is(err, withdrawn) {
		t.Fatalf("final source validation result: called=%v order=%d err=%v", validated, order, err)
	}
	rows, err := store.LoadStreamItems(t.Context(), "chat")
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected row survived rollback: rows=%+v err=%v", rows, err)
	}
	revision, err = store.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	order, err = store.AppendUserStreamItemAtomicIfCurrent(t.Context(), "chat", StreamItemRow{ID: "accepted", Kind: "user", Content: "Allowed title"}, revision, nil)
	if err != nil || order != 0 {
		t.Fatalf("allowed source did not publish after rejection: order=%d err=%v", order, err)
	}
}
