package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
	"wails-gui/storage"
)

var mcpRetentionReadFences sync.Map
var mcpRetentionReadFenceID atomic.Uint64

func init() {
	// Observe an actual SQLite read without changing its value. A temporary
	// view keeps the real capture tables and their revision triggers intact.
	sqlite.MustRegisterScalarFunction("mcp_retention_read_fence", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if value, ok := mcpRetentionReadFences.Load(args[0]); ok {
			value.(func())()
		}
		return args[1], nil
	})
}

func installMCPRetentionReadFence(t *testing.T, store *ActivityStore, read func()) {
	t.Helper()
	rows, err := store.db.Query(`PRAGMA main.table_info(activity_snapshots)`)
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("retention-fence-%d", mcpRetentionReadFenceID.Add(1))
	mcpRetentionReadFences.Store(id, read)
	t.Cleanup(func() { mcpRetentionReadFences.Delete(id) })
	selects := make([]string, 0, len(columns))
	for _, name := range columns {
		value := `"` + name + `"`
		if name == "parsed_json" {
			value = fmt.Sprintf(`mcp_retention_read_fence('%s',"%s") AS "%s"`, id, name, name)
		}
		selects = append(selects, value)
	}
	// Only this reader connection sees the view. Retention uses another real
	// connection to the same WAL database and its original tables.
	if _, err := store.db.Exec(`CREATE TEMP VIEW activity_snapshots AS SELECT ` + strings.Join(selects, ",") + ` FROM main.activity_snapshots`); err != nil {
		t.Fatal(err)
	}
}

func TestMCPReadRetentionBetweenCaptureAndFinalGate(t *testing.T) {
	for _, tool := range []string{"activity_search", "people_lookup"} {
		for _, deletion := range []string{"retention", "explicit"} {
			t.Run(tool+"/"+deletion, func(t *testing.T) {
				isolateActivityPrivacyTest(t)
				app := newMCPReadTestApp(t)
				const source = "racing-source"
				at := seedMCPReadActivity(t, app, source, "Editor", "com.example.editor", "project captured title", "", "done")
				tx, err := app.activity.store.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				if err := app.activity.store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: source, Timestamp: at, App: "Editor", AXText: "project captured snapshot", ParsedJSON: `{}`}}); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				person, err := app.store.CreatePrincipal(app.ctx, storage.PrincipalRow{ID: "racing-person", Kind: storage.PrincipalKindContact, DisplayName: "project colleague", CreatedFrom: "activity"})
				if err != nil {
					t.Fatal(err)
				}
				if err := app.store.BumpPrincipalSighting(app.ctx, person.ID, "Editor", source, at, ""); err != nil {
					t.Fatal(err)
				}
				writeMCPReadMemory(t, "derived", source, "project remembered fact")
				writer, err := NewActivityStore(configDirOrFallback())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { writer.Close() })

				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				p, err := app.mcpReadPrivacy(ctx)
				if err != nil {
					t.Fatal(err)
				}
				entered, release := make(chan struct{}), make(chan struct{})
				var armed atomic.Bool
				var reached, released sync.Once
				unblock := func() { released.Do(func() { close(release) }) }
				defer unblock()
				installMCPRetentionReadFence(t, app.activity.store, func() {
					// Search materializes its candidate first. Stop only when the
					// gate has already read that session and its capture events.
					if !armed.Load() || p.eventChecks == 0 {
						return
					}
					reached.Do(func() {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
						}
					})
				})
				args := map[string]any{"query": "project", "name": "project"}
				if got := callMCPReadTest(t, app, tool, args); len(got.Items) != 1 {
					t.Fatalf("readable control failed before interleaving: %+v", got)
				}

				armed.Store(true)
				result := mcpReadResult{}
				done := make(chan error, 1)
				go func() {
					args := mcpReadArguments{Query: "project", Name: "project", Limit: 20}
					switch tool {
					case "activity_search":
						done <- p.activitySearch(args, &result)
					case "people_lookup":
						done <- p.peopleLookup(args, &result)
					}
				}()
				select {
				case <-entered:
				case err := <-done:
					t.Fatalf("read completed before capture fence: %v, %+v", err, result)
				case <-ctx.Done():
					t.Fatal("capture fence was not reached")
				}
				switch deletion {
				case "retention":
					err = writer.deleteSessionForRetention(source)
				case "explicit":
					err = writer.DeleteSession(source)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := writer.GetSessionByID(source); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("deletion did not commit before read resumed: %v", err)
				}
				if err := ctx.Err(); err != nil {
					t.Fatalf("retention fence expired before deletion committed: %v", err)
				}
				unblock()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("read did not complete after retention fence")
				}
				if err := ctx.Err(); err != nil {
					t.Fatalf("retention fence expired before read completed: %v", err)
				}
				if len(result.Items) != 0 || p.sessions[source] {
					t.Fatalf("stale captures bypassed final provenance gate after %s: %+v", deletion, result)
				}
				for _, memoryTool := range []string{"memory_list", "memory_search"} {
					got := callMCPReadTest(t, app, memoryTool, map[string]any{"query": "project"})
					want := 0
					if deletion == "retention" {
						want = 1
					}
					if len(got.Items) != want || (want == 1 && got.Items[0].ID.Text != "derived") {
						t.Fatalf("%s did not preserve %s derivative policy: %+v", memoryTool, deletion, got)
					}
				}
			})
		}
	}
}
