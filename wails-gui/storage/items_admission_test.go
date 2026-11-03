package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendUserStreamItemAtomicRejectsWithoutPublishingPartialRow(t *testing.T) {
	for _, failure := range []string{"session update", "cancellation", "duplicate ID"} {
		t.Run(failure, func(t *testing.T) {
			store := newTestStore(t)
			ctx := context.Background()
			if err := store.EnsureProjectStructure(ctx, ProjectRow{ID: "p", Name: "P", Path: filepath.Join(t.TempDir(), "p")}, []WorkspaceRow{{ID: "w", ProjectID: "p", Label: "W", IsDefault: true}}, nil); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveSession(ctx, SessionRow{LocalID: "chat", ProjectID: "p", WorkspaceID: "w", Label: "Chat"}); err != nil {
				t.Fatal(err)
			}
			cursor, err := store.AppendUserStreamItemAtomic(ctx, "chat", StreamItemRow{ID: "existing", Kind: "user", Content: "Keep this request"})
			if err != nil {
				t.Fatal(err)
			}
			item := StreamItemRow{ID: "rejected", Kind: "user", Content: "Must not be published"}
			attemptCtx := ctx
			switch failure {
			case "session update":
				_, err = store.db.Exec(`CREATE TRIGGER reject_update BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'session update rejected'); END`)
			case "cancellation":
				// The real SQLite statement stays inside the uncommitted append
				// until its context expires, after INSERT returned its order.
				_, err = store.db.Exec(`CREATE TRIGGER reject_update BEFORE UPDATE ON sessions BEGIN SELECT sum(n) FROM (WITH RECURSIVE numbers(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM numbers WHERE n < 1000000000) SELECT n FROM numbers); END`)
				var cancel context.CancelFunc
				attemptCtx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			case "duplicate ID":
				item.ID = "existing"
			}
			if err != nil {
				t.Fatal(err)
			}
			if order, err := store.AppendUserStreamItemAtomic(attemptCtx, "chat", item); err == nil || order != -1 {
				t.Fatalf("failed append returned order=%d err=%v", order, err)
			}
			if failure == "cancellation" && attemptCtx.Err() != context.DeadlineExceeded {
				t.Fatalf("append failed before cancellation: %v", attemptCtx.Err())
			}
			if _, err := store.db.Exec(`DROP TRIGGER IF EXISTS reject_update`); err != nil {
				t.Fatal(err)
			}
			page, err := store.LoadStreamItemsAfter(ctx, "chat", cursor, 10)
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("failed append became visible: %+v, %v", page, err)
			}
			existing, err := store.LoadStreamItem(ctx, "existing")
			if err != nil || existing == nil || existing.Content != "Keep this request" {
				t.Fatalf("failed append changed prior row: %+v, %v", existing, err)
			}
			order, err := store.AppendUserStreamItemAtomic(ctx, "chat", StreamItemRow{ID: "accepted", Kind: "user", Content: "Accepted request"})
			if err != nil || order <= cursor {
				t.Fatalf("accepted append = %d after %d, %v", order, cursor, err)
			}
			page, err = store.LoadStreamItemsAfter(ctx, "chat", cursor, 10)
			if err != nil || len(page.Items) != 1 || page.Items[0].ID != "accepted" {
				t.Fatalf("forward cursor missed accepted append: %+v, %v", page, err)
			}
		})
	}
}
