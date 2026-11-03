package main

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
	"wails-gui/storage"
)

var mcpBriefReadObservers sync.Map
var mcpBriefReadObserverID atomic.Uint64

func init() {
	// Observe actual SQLite row reads without replacing storage methods or
	// changing their values, ordering, filtering or privacy decisions.
	sqlite.MustRegisterScalarFunction("mcp_brief_observe_read", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if counter, ok := mcpBriefReadObservers.Load(args[0]); ok {
			counter.(*atomic.Uint64).Add(1)
		}
		return args[1], nil
	})
}

func observeMCPBriefTableReads(t *testing.T, table, column string) *atomic.Uint64 {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(configDirOrFallback(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`PRAGMA table_info("` + table + `")`)
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
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	id := fmt.Sprintf("brief-observer-%d", mcpBriefReadObserverID.Add(1))
	counter := &atomic.Uint64{}
	mcpBriefReadObservers.Store(id, counter)
	t.Cleanup(func() { mcpBriefReadObservers.Delete(id) })
	selects := make([]string, 0, len(columns))
	for _, name := range columns {
		value := `"` + name + `"`
		if name == column {
			value = fmt.Sprintf(`mcp_brief_observe_read('%s',"%s") AS "%s"`, id, name, name)
		}
		selects = append(selects, value)
	}
	if _, err := db.Exec(`ALTER TABLE "` + table + `" RENAME TO "mcp_observed_` + table + `"`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE VIEW "` + table + `" AS SELECT ` + strings.Join(selects, ",") + ` FROM "mcp_observed_` + table + `"`); err != nil {
		t.Fatal(err)
	}
	return counter
}

func TestMCPReadBriefSharesCompletedReadsAcrossItems(t *testing.T) {
	a := newMCPReadTestApp(t)
	person, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "brief-colleague", Kind: storage.PrincipalKindContact, DisplayName: "Reviewed Colleague", CreatedFrom: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	// Cross the 200-row boundary so repeated provenance paging is visible.
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("brief-sighting-%03d", i)
		at := seedMCPReadActivity(t, a, id, "Editor", "com.example.editor", "Visible project", "", "")
		if err := a.store.BumpPrincipalSighting(a.ctx, person.ID, "Editor", id, at, ""); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	if err := a.store.UpsertCalendarEvents(a.ctx, []storage.CalendarEventRow{{ID: "brief-calendar", Source: "eventkit", Title: "Reviewed event", StartAt: start.UnixMilli(), EndAt: start.Add(time.Hour).UnixMilli()}}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	personReads := observeMCPBriefTableReads(t, "principal_session_sightings", "last_seen_at")
	calendarReads := observeMCPBriefTableReads(t, "calendar_events", "title")
	personRef := MobileBriefRef{SourceKind: "principal", SourceID: person.ID}
	dayRef := MobileBriefRef{SourceKind: mobileBriefDayShapeSource, SourceID: now.Format("2006-01-02")}
	storeMCPBriefReferences(t, personRef, dayRef)
	baseline := callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(baseline.Items) != 2 || personReads.Load() == 0 || calendarReads.Load() == 0 {
		t.Fatalf("real read observers did not see both sources: %+v person=%d calendar=%d", baseline, personReads.Load(), calendarReads.Load())
	}
	personBaseline, calendarBaseline := personReads.Swap(0), calendarReads.Swap(0)
	refs := make([]MobileBriefRef, 0, 20)
	for i := 0; i < 10; i++ {
		refs = append(refs, personRef, dayRef)
	}
	storeMCPBriefReferences(t, refs...)
	got := callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 20 || got.Truncated || got.Withheld != 0 || got.Omitted != 0 {
		t.Fatalf("repeated complete sources changed the brief: %+v", got)
	}
	if personReads.Load() != personBaseline || calendarReads.Load() != calendarBaseline {
		t.Errorf("completed sources reread per item: principal rows=%d (one lookup=%d), calendar rows=%d (one lookup=%d)", personReads.Load(), personBaseline, calendarReads.Load(), calendarBaseline)
	}
	for _, item := range got.Items {
		if item.Source == "calendar_day" && (item.Snippet == nil || item.Snippet.Text != "1 readable calendar events") {
			t.Fatalf("calendar aggregate was not retained: %+v", item)
		}
	}
	// A new request must revalidate privacy instead of retaining a prior
	// request's successful person or calendar-day decision.
	if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetUIState(a.ctx, permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
		t.Fatal(err)
	}
	got = callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 0 || got.Withheld != 20 {
		t.Fatalf("request memo crossed a privacy change: %+v", got)
	}
}

func TestMCPReadBriefMemoDoesNotRetainTraversalFailure(t *testing.T) {
	a := newMCPReadTestApp(t)
	store, err := a.purposeProductStore()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UnixMilli()
	id := seedMCPBriefPurposeTree(t, a, 1, 1)[0]
	child := id
	for i := 0; i < 7; i++ {
		key := fmt.Sprint("memo-depth-", i)
		evidence := purposeTestEvidence(key, PurposeEvidenceExplicitUser, PurposeStanceSupport, at)
		evidence.SourceKind, evidence.SourceID = "purpose", id
		root, _, err := store.Create(purposeTestCreateInput(key, PurposeKindCommitment, key, []PurposeEvidenceInput{evidence}, at))
		if err != nil {
			t.Fatal(err)
		}
		id = root.OpenLoop.ID
		if i == 3 {
			child = id
		}
	}
	storeMCPBriefReferences(t, MobileBriefRef{SourceKind: "purpose", SourceID: id}, MobileBriefRef{SourceKind: "purpose", SourceID: child})
	got := callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 1 || got.Items[0].ID.Text != mcpReadText(child).Text || !got.Truncated || got.Omitted != 1 || got.Withheld != 0 {
		t.Fatalf("path-dependent failure poisoned a later standalone read: %+v", got)
	}
	// A shallow composite success also cannot skip its descendants' depth
	// checks when a later item reaches that same composite along a deep path.
	storeMCPBriefReferences(t, MobileBriefRef{SourceKind: "purpose", SourceID: child}, MobileBriefRef{SourceKind: "purpose", SourceID: id})
	got = callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 1 || got.Items[0].ID.Text != mcpReadText(child).Text || !got.Truncated || got.Omitted != 1 || got.Withheld != 0 {
		t.Fatalf("cached shallow composite bypassed a later depth limit: %+v", got)
	}
	// Both references can also belong to one item, where the local traversal
	// cache must revalidate a shallow success reached later at a deeper path.
	brief := mobileBriefStoredBrief(t, time.Now().Format("2006-01-02"), mobileBriefSlotMorning, 1)
	brief.Sections = MobileBriefSections{Moved: []MobileBriefItem{{ID: "shared-paths", Refs: []string{"child", "root"}}}}
	brief.Evidence = []MobileBriefRef{{Ref: "child", SourceKind: "purpose", SourceID: child}, {Ref: "root", SourceKind: "purpose", SourceID: id}}
	brief.Limits.TruncatedSections = nil
	storeMCPBrief(t, brief)
	got = callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 0 || !got.Truncated || got.Omitted != 1 || got.Withheld != 0 {
		t.Fatalf("same-item shallow cache bypassed a deeper traversal: %+v", got)
	}
}

func TestMCPReadBriefMemoDoesNotRetainPartialCalendarAggregate(t *testing.T) {
	a := newMCPReadTestApp(t)
	at := seedMCPReadActivity(t, a, "budget-calendar", "Editor", "com.example.editor", "Visible project", "", "")
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local).UnixMilli()
	if err := a.store.UpsertCalendarEvents(a.ctx, []storage.CalendarEventRow{
		{ID: "readable", Source: "eventkit", Title: "Visible event", StartAt: start, EndAt: start + 60000},
		{ID: "budget", Source: "eventkit", Title: "Needs session verification", SessionID: "budget-calendar", StartAt: start, EndAt: start + 60000},
	}, at); err != nil {
		t.Fatal(err)
	}
	ref := MobileBriefRef{SourceKind: mobileBriefDayShapeSource, SourceID: now.Format("2006-01-02")}
	storeMCPBriefReferences(t, ref, ref)
	p, err := a.mcpReadPrivacy(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.sessionChecks = mcpReadSessionCheckLimit
	got := mcpReadResult{}
	if err := p.briefToday(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 || !got.Truncated || got.Omitted != 2 || got.Withheld != 0 {
		t.Fatalf("partial calendar aggregate became a completed memo entry: %+v", got)
	}
}

func TestMCPReadBriefSharesDeniedPrincipalReadsAcrossItems(t *testing.T) {
	a := newMCPReadTestApp(t)
	person, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "denied-colleague", Kind: storage.PrincipalKindContact, DisplayName: "Private Colleague", CreatedFrom: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("denied-sighting-%03d", i)
		at := seedMCPReadActivity(t, a, id, "Editor", "com.example.editor", "Visible project", "", "")
		if err := a.store.BumpPrincipalSighting(a.ctx, person.ID, "Editor", id, at, ""); err != nil {
			t.Fatal(err)
		}
	}
	const privateID = "denied-private-sighting"
	at := seedMCPReadActivity(t, a, privateID, "Google Chrome", "com.google.Chrome", "Private project - Incognito", "", "")
	if err := a.store.BumpPrincipalSighting(a.ctx, person.ID, "Google Chrome", privateID, at-int64(time.Hour/time.Millisecond), ""); err != nil {
		t.Fatal(err)
	}
	reads := observeMCPBriefTableReads(t, "principal_session_sightings", "last_seen_at")
	ref := MobileBriefRef{SourceKind: "principal", SourceID: person.ID}
	storeMCPBriefReferences(t, ref)
	baseline := callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(baseline.Items) != 0 || baseline.Withheld != 1 || baseline.Truncated || reads.Load() == 0 {
		t.Fatalf("real denied read was not observed: %+v reads=%d", baseline, reads.Load())
	}
	oneRead := reads.Swap(0)
	refs := make([]MobileBriefRef, 20)
	for i := range refs {
		refs[i] = ref
	}
	storeMCPBriefReferences(t, refs...)
	got := callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 0 || got.Withheld != 20 || got.Truncated || got.Omitted != 0 {
		t.Fatalf("memo changed denied principal results: %+v", got)
	}
	if reads.Load() != oneRead {
		t.Errorf("denied principal reread per item: rows=%d, one lookup=%d", reads.Load(), oneRead)
	}
	// Denial is request-local: a later request must inspect current evidence.
	if _, err := a.activity.store.db.Exec(`UPDATE activity_events SET data='{"appBundleId":"com.google.Chrome","appName":"Google Chrome","windowTitle":"Visible project"}' WHERE session_id=?`, privateID); err != nil {
		t.Fatal(err)
	}
	got = callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 20 || got.Withheld != 0 || got.Truncated {
		t.Fatalf("denial crossed a request boundary: %+v", got)
	}
}

func TestMCPReadBriefMemoDoesNotRetainPrincipalBudgetFailure(t *testing.T) {
	a := newMCPReadTestApp(t)
	person, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "limited-colleague", Kind: storage.PrincipalKindContact, DisplayName: "Visible Colleague", CreatedFrom: "activity"})
	if err != nil {
		t.Fatal(err)
	}
	at := seedMCPReadActivity(t, a, "limited-principal-source", "Editor", "com.example.editor", "Visible project", "", "")
	if err := a.store.BumpPrincipalSighting(a.ctx, person.ID, "Editor", "limited-principal-source", at, ""); err != nil {
		t.Fatal(err)
	}
	ref := MobileBriefRef{SourceKind: "principal", SourceID: person.ID}
	storeMCPBriefReferences(t, ref, ref)
	p, err := a.mcpReadPrivacy(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.sessionChecks = mcpReadSessionCheckLimit
	got := mcpReadResult{}
	if err := p.briefToday(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 || !got.Truncated || got.Omitted != 2 || got.Withheld != 0 {
		t.Fatalf("principal budget failure became a completed denial: %+v", got)
	}
}

func TestMCPReadBriefPrincipalDepthFailureDoesNotPoisonLaterItem(t *testing.T) {
	a := newMCPReadTestApp(t)
	person, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "deep-colleague", Kind: storage.PrincipalKindContact, DisplayName: "Visible Colleague", CreatedFrom: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := a.purposeProductStore()
	if err != nil {
		t.Fatal(err)
	}
	kind, id := "principal", person.ID
	at := time.Now().Add(-time.Hour).UnixMilli()
	for i := 0; i < 7; i++ {
		key := fmt.Sprint("principal-depth-", i)
		evidence := purposeTestEvidence(key, PurposeEvidenceExplicitUser, PurposeStanceSupport, at)
		evidence.SourceKind, evidence.SourceID = kind, id
		root, _, err := store.Create(purposeTestCreateInput(key, PurposeKindCommitment, key, []PurposeEvidenceInput{evidence}, at))
		if err != nil {
			t.Fatal(err)
		}
		kind, id = "purpose", root.OpenLoop.ID
	}
	storeMCPBriefReferences(t, MobileBriefRef{SourceKind: kind, SourceID: id}, MobileBriefRef{SourceKind: "principal", SourceID: person.ID})
	got := callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 1 || got.Items[0].Source != "person" || got.Items[0].ID.Text != person.ID ||
		!got.Truncated || got.Omitted != 1 || got.Withheld != 0 {
		t.Fatalf("principal depth failure poisoned a later item: %+v", got)
	}
}

func TestMCPReadBriefMemoDoesNotRetainPrincipalStorageFailure(t *testing.T) {
	for _, table := range []string{"app_handles", "principal_session_sightings", "activity_sessions", "activity_events"} {
		t.Run(table, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			person, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "recovering-colleague", Kind: storage.PrincipalKindContact, DisplayName: "Visible Colleague", CreatedFrom: "activity"})
			if err != nil {
				t.Fatal(err)
			}
			const session = "recovering-principal-source"
			at := seedMCPReadActivity(t, a, session, "Editor", "com.example.editor", "Visible project", "", "")
			if err := a.store.BumpPrincipalSighting(a.ctx, person.ID, "Editor", session, at, ""); err != nil {
				t.Fatal(err)
			}
			p, err := a.mcpReadPrivacy(a.ctx)
			if err != nil {
				t.Fatal(err)
			}
			denied := map[string]bool{}
			completed := map[string]mcpReadItem{}
			newItem := func() *mcpBriefReader {
				return &mcpBriefReader{p: p, cache: map[string]mcpBriefCachedRead{}, checked: map[string]bool{}, deniedPrincipals: denied, completed: completed}
			}
			db := a.activity.store.db
			if !strings.HasPrefix(table, "activity_") {
				db, err = sql.Open("sqlite", filepath.Join(configDirOrFallback(), "sessions.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			}
			// Temporarily remove a real table from the query's namespace. This
			// exercises storage errors without replacing rows or storage APIs.
			unavailable := "mcp_unavailable_" + table
			if _, err := db.Exec(`ALTER TABLE "` + table + `" RENAME TO "` + unavailable + `"`); err != nil {
				t.Fatal(err)
			}
			if item, ok := newItem().reference("principal", person.ID, 0); ok {
				t.Errorf("storage failure returned an unverified person: %+v", item)
			}
			if denied[person.ID] {
				t.Error("storage failure became a shared privacy denial")
			}
			if _, err := db.Exec(`ALTER TABLE "` + unavailable + `" RENAME TO "` + table + `"`); err != nil {
				t.Fatal(err)
			}
			// The next item shares the request's privacy and completed-read
			// memos, but must retry an earlier unavailable source after recovery.
			item, ok := newItem().reference("principal", person.ID, 0)
			if !ok || item.Source != "person" || item.ID.Text != person.ID {
				t.Fatalf("storage recovery remained poisoned by a failed read: ok=%v item=%+v", ok, item)
			}
		})
	}
}
