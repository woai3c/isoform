package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func mkContact(t *testing.T, s *Store, name string) PrincipalRow {
	t.Helper()
	row, err := s.CreatePrincipal(ctx, PrincipalRow{
		Kind:        PrincipalKindContact,
		DisplayName: name,
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	return row
}

// ── Bump basics ──

func TestBumpPrincipalSighting_FirstWriteInsertsRow(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "A")

	if err := s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, ""); err != nil {
		t.Fatalf("BumpPrincipalSighting: %v", err)
	}
	rows, err := s.listSightings(ctx, p.ID)
	if err != nil {
		t.Fatalf("listSightings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	if rows[0].MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", rows[0].MessageCount)
	}
	if rows[0].FirstSeenAt != 1000 || rows[0].LastSeenAt != 1000 {
		t.Errorf("first=%d last=%d, want both 1000", rows[0].FirstSeenAt, rows[0].LastSeenAt)
	}
}

func TestBumpPrincipalSighting_SecondCallIncrementsAndKeepsFirstSeen(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "B")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, "")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 5000, "")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 3000, "") // out-of-order

	rows, _ := s.listSightings(ctx, p.ID)
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	if rows[0].MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", rows[0].MessageCount)
	}
	if rows[0].FirstSeenAt != 1000 {
		t.Errorf("FirstSeenAt = %d, want 1000", rows[0].FirstSeenAt)
	}
	if rows[0].LastSeenAt != 5000 {
		t.Errorf("LastSeenAt = %d, want 5000 (MAX wins)", rows[0].LastSeenAt)
	}
}

func TestBumpPrincipalSighting_PerAppRows(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "C")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, "")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "telegram", "", 2000, "")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1500, "")

	rows, _ := s.listSightings(ctx, p.ID)
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2", len(rows))
	}
	// Order is by count DESC, so wechat (count=2) first.
	if rows[0].App != "wechat" || rows[0].MessageCount != 2 {
		t.Errorf("rows[0] = %+v", rows[0])
	}
	if rows[1].App != "telegram" || rows[1].MessageCount != 1 {
		t.Errorf("rows[1] = %+v", rows[1])
	}
}

func TestBumpPrincipalSighting_NormalizesAppCase(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "D")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "WeChat", "", 1000, "")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 2000, "")

	rows, _ := s.listSightings(ctx, p.ID)
	if len(rows) != 1 {
		t.Fatalf("app case not normalized: %v", rows)
	}
	if rows[0].MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", rows[0].MessageCount)
	}
}

func TestBumpPrincipalSighting_DefaultsTSWhenZero(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "E")

	if err := s.BumpPrincipalSighting(ctx, p.ID, "telegram", "", 0, ""); err != nil {
		t.Fatalf("BumpPrincipalSighting: %v", err)
	}
	rows, _ := s.listSightings(ctx, p.ID)
	if rows[0].FirstSeenAt == 0 || rows[0].LastSeenAt == 0 {
		t.Errorf("default ts not applied: %+v", rows[0])
	}
}

func TestBumpPrincipalSighting_RejectsEmptyInputs(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "F")
	if err := s.BumpPrincipalSighting(ctx, "", "wechat", "", 1, ""); err == nil {
		t.Errorf("empty principal should error")
	}
	if err := s.BumpPrincipalSighting(ctx, p.ID, "  ", "", 1, ""); err == nil {
		t.Errorf("empty app should error")
	}
}

// ── Recent messages window ──

func TestBumpPrincipalSighting_RecentMessageRecorded(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "G")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, "  hello  ")
	rows, err := s.GetPrincipalRecentMessages(ctx, p.ID, 10)
	if err != nil {
		t.Fatalf("GetPrincipalRecentMessages: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	if rows[0].Preview != "hello" {
		t.Errorf("Preview = %q, want 'hello' (trimmed)", rows[0].Preview)
	}
	if rows[0].App != "wechat" || rows[0].SeenAt != 1000 {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestBumpPrincipalSighting_EmptyPreviewSkipped(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "H")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, "")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 2000, "   ")

	rows, _ := s.GetPrincipalRecentMessages(ctx, p.ID, 10)
	if len(rows) != 0 {
		t.Errorf("empty preview should not insert: got %d rows", len(rows))
	}
}

func TestBumpPrincipalSighting_RollingWindowTrims(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "I")

	const total = defaultRecentMessagesWindow + 5
	for i := 1; i <= total; i++ {
		if err := s.BumpPrincipalSighting(ctx, p.ID, "telegram", "", int64(i*10), "msg"); err != nil {
			t.Fatalf("Bump %d: %v", i, err)
		}
	}
	rows, _ := s.GetPrincipalRecentMessages(ctx, p.ID, total)
	if len(rows) != defaultRecentMessagesWindow {
		t.Errorf("len = %d, want %d", len(rows), defaultRecentMessagesWindow)
	}
	// Newest preserved: row 0 is most recent.
	if rows[0].SeenAt != int64(total*10) {
		t.Errorf("newest SeenAt = %d, want %d", rows[0].SeenAt, total*10)
	}
}

func TestBumpPrincipalSighting_SameTSCollisionDropsSecond(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "J")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, "first")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, "second-dropped")

	rows, _ := s.GetPrincipalRecentMessages(ctx, p.ID, 10)
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1 (same-ts collision)", len(rows))
	}
	if rows[0].Preview != "first" {
		t.Errorf("Preview = %q, want 'first'", rows[0].Preview)
	}
	// Counter should have bumped both times even though preview was dropped.
	sg, _ := s.listSightings(ctx, p.ID)
	if sg[0].MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", sg[0].MessageCount)
	}
}

// ── Cascade ──

func TestBumpPrincipalSighting_CascadesOnPrincipalDelete(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "K")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, "hi")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "telegram", "", 2000, "yo")
	if err := s.DeletePrincipal(ctx, p.ID); err != nil {
		t.Fatalf("DeletePrincipal: %v", err)
	}
	rows, _ := s.listSightings(ctx, p.ID)
	if len(rows) != 0 {
		t.Errorf("sightings not cascaded: %d remain", len(rows))
	}
	recent, _ := s.GetPrincipalRecentMessages(ctx, p.ID, 10)
	if len(recent) != 0 {
		t.Errorf("recent messages not cascaded: %d remain", len(recent))
	}
}

// ── GetPrincipalStats ──

func TestGetPrincipalStats_AggregatesAcrossApps(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "L")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, "a")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 5000, "b")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "telegram", "", 3000, "c")

	stats, err := s.GetPrincipalStats(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPrincipalStats: %v", err)
	}
	if stats.TotalMessages != 3 {
		t.Errorf("TotalMessages = %d, want 3", stats.TotalMessages)
	}
	if stats.FirstSeenAt != 1000 {
		t.Errorf("FirstSeenAt = %d, want 1000", stats.FirstSeenAt)
	}
	if stats.LastSeenAt != 5000 {
		t.Errorf("LastSeenAt = %d, want 5000", stats.LastSeenAt)
	}
	if len(stats.ByApp) != 2 {
		t.Errorf("ByApp len = %d, want 2", len(stats.ByApp))
	}
	if len(stats.RecentMessages) != 3 {
		t.Errorf("RecentMessages len = %d, want 3", len(stats.RecentMessages))
	}
}

func TestGetPrincipalStats_MissingPrincipalErrors(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetPrincipalStats(ctx, "ghost")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestGetPrincipalStats_NoSightingsReturnsZeroStats(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "M")

	stats, err := s.GetPrincipalStats(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPrincipalStats: %v", err)
	}
	if stats.TotalMessages != 0 || len(stats.ByApp) != 0 || len(stats.RecentMessages) != 0 {
		t.Errorf("expected zero stats, got %+v", stats)
	}
	if stats.PrincipalID != p.ID {
		t.Errorf("PrincipalID = %q, want %q", stats.PrincipalID, p.ID)
	}
}

// ── GetAllPrincipalStats ──

func TestGetAllPrincipalStats_IncludesNeverSeenPrincipals(t *testing.T) {
	s := newTestStore(t)
	a := mkContact(t, s, "Alice")
	b := mkContact(t, s, "Bob")

	_ = s.BumpPrincipalSighting(ctx, a.ID, "wechat", "", 1000, "msg")

	all, err := s.GetAllPrincipalStats(ctx)
	if err != nil {
		t.Fatalf("GetAllPrincipalStats: %v", err)
	}
	if _, ok := all[a.ID]; !ok {
		t.Errorf("Alice missing from result")
	}
	if _, ok := all[b.ID]; !ok {
		t.Errorf("Bob (no sightings) missing from result")
	}
	if all[a.ID].TotalMessages != 1 {
		t.Errorf("Alice total = %d, want 1", all[a.ID].TotalMessages)
	}
	if all[b.ID].TotalMessages != 0 {
		t.Errorf("Bob total = %d, want 0", all[b.ID].TotalMessages)
	}
}

// ── Concurrency ──

func TestBumpPrincipalSighting_ConcurrentBumpsAccumulate(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "RaceA")

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan error, N)
	for i := range N {
		go func(i int) {
			defer wg.Done()
			if err := s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", int64(i+1), ""); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent bump: %v", err)
	}
	rows, _ := s.listSightings(ctx, p.ID)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].MessageCount != N {
		t.Errorf("MessageCount = %d, want %d", rows[0].MessageCount, N)
	}
}

func TestGetPrincipalRecentMessages_LimitDefault(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "N")
	for i := 1; i <= 5; i++ {
		_ = s.BumpPrincipalSighting(ctx, p.ID, "slack", "", int64(i), "msg")
	}
	rows, err := s.GetPrincipalRecentMessages(ctx, p.ID, 0)
	if err != nil {
		t.Fatalf("GetPrincipalRecentMessages: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("len = %d, want 5", len(rows))
	}
	// limit==0 → default window applied; verify ordering newest-first
	if rows[0].SeenAt != 5 {
		t.Errorf("newest first violated: %+v", rows)
	}
}

func TestGetPrincipalRecentMessages_ExplicitLimit(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "O")
	for i := 1; i <= 5; i++ {
		_ = s.BumpPrincipalSighting(ctx, p.ID, "slack", "", int64(i), "msg")
	}
	rows, _ := s.GetPrincipalRecentMessages(ctx, p.ID, 2)
	if len(rows) != 2 {
		t.Errorf("len = %d, want 2", len(rows))
	}
}

// Sanity: long preview round-trips intact (no chunking).
// ── Session sightings ──

func TestBumpPrincipalSighting_RecordsSessionSighting(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "Sess1")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "sess-a", 1000, "msg")
	rows, err := s.ListPrincipalSessionSightings(ctx, p.ID, 10)
	if err != nil {
		t.Fatalf("ListPrincipalSessionSightings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	if rows[0].SessionID != "sess-a" {
		t.Errorf("SessionID = %q, want sess-a", rows[0].SessionID)
	}
	if rows[0].FirstSeenAt != 1000 || rows[0].LastSeenAt != 1000 {
		t.Errorf("first=%d last=%d, want both 1000", rows[0].FirstSeenAt, rows[0].LastSeenAt)
	}
}

func TestBumpPrincipalSighting_SessionMINMaxOnRepeat(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "Sess2")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "sess-a", 2000, "first")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "sess-a", 5000, "later")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "sess-a", 1000, "earlier") // backfill

	rows, _ := s.ListPrincipalSessionSightings(ctx, p.ID, 10)
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1 (same session)", len(rows))
	}
	if rows[0].FirstSeenAt != 1000 {
		t.Errorf("FirstSeenAt = %d, want 1000 (MIN)", rows[0].FirstSeenAt)
	}
	if rows[0].LastSeenAt != 5000 {
		t.Errorf("LastSeenAt = %d, want 5000 (MAX)", rows[0].LastSeenAt)
	}
}

func TestBumpPrincipalSighting_MultipleSessionsOrderedByLastSeen(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "Sess3")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "sess-a", 1000, "a")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "sess-b", 2000, "b")
	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "sess-c", 3000, "c")

	rows, _ := s.ListPrincipalSessionSightings(ctx, p.ID, 10)
	if len(rows) != 3 {
		t.Fatalf("len = %d, want 3", len(rows))
	}
	// Newest first.
	wantOrder := []string{"sess-c", "sess-b", "sess-a"}
	for i, w := range wantOrder {
		if rows[i].SessionID != w {
			t.Errorf("rows[%d].SessionID = %q, want %q", i, rows[i].SessionID, w)
		}
	}
}

func TestBumpPrincipalSighting_EmptySessionIDSkipped(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "Sess4")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "", 1000, "no session")
	rows, _ := s.ListPrincipalSessionSightings(ctx, p.ID, 10)
	if len(rows) != 0 {
		t.Errorf("empty sessionID should not insert session row, got %d", len(rows))
	}
}

func TestBumpPrincipalSighting_SessionCascadesOnPrincipalDelete(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "Sess5")

	_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", "sess-a", 1000, "msg")
	if err := s.DeletePrincipal(ctx, p.ID); err != nil {
		t.Fatalf("DeletePrincipal: %v", err)
	}
	rows, _ := s.ListPrincipalSessionSightings(ctx, p.ID, 10)
	if len(rows) != 0 {
		t.Errorf("session sightings not cascaded: %d remain", len(rows))
	}
}

func TestListSessionPrincipalSightings_ReturnsByLastSeenDesc(t *testing.T) {
	s := newTestStore(t)
	a := mkContact(t, s, "Alice")
	b := mkContact(t, s, "Bob")
	c := mkContact(t, s, "Carol")

	_ = s.BumpPrincipalSighting(ctx, a.ID, "discord", "sess-X", 1000, "a")
	_ = s.BumpPrincipalSighting(ctx, b.ID, "discord", "sess-X", 2000, "b")
	_ = s.BumpPrincipalSighting(ctx, c.ID, "discord", "sess-X", 3000, "c")
	// Unrelated session: should not appear.
	_ = s.BumpPrincipalSighting(ctx, a.ID, "discord", "sess-Y", 5000, "a-Y")

	rows, err := s.ListSessionPrincipalSightings(ctx, "sess-X", 50)
	if err != nil {
		t.Fatalf("ListSessionPrincipalSightings: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len = %d, want 3", len(rows))
	}
	want := []string{c.ID, b.ID, a.ID}
	for i, w := range want {
		if rows[i].PrincipalID != w {
			t.Errorf("rows[%d].PrincipalID = %s, want %s", i, rows[i].PrincipalID, w)
		}
	}
	if rows[0].DisplayName != "Carol" || rows[0].Kind != PrincipalKindContact {
		t.Errorf("first row enrichment wrong: %+v", rows[0])
	}
}

func TestListSessionPrincipalSightings_DeletedPrincipalDropped(t *testing.T) {
	s := newTestStore(t)
	a := mkContact(t, s, "Alice")
	_ = s.BumpPrincipalSighting(ctx, a.ID, "discord", "sess-Z", 1000, "a")
	if err := s.DeletePrincipal(ctx, a.ID); err != nil {
		t.Fatalf("DeletePrincipal: %v", err)
	}
	rows, _ := s.ListSessionPrincipalSightings(ctx, "sess-Z", 10)
	if len(rows) != 0 {
		t.Errorf("len = %d, want 0 (principal deleted, cascade)", len(rows))
	}
}

func TestListSessionPrincipalSightings_EmptySession(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.ListSessionPrincipalSightings(ctx, "no-such-session", 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len = %d, want 0", len(rows))
	}
}

func TestListPrincipalSessionSightings_LimitDefault(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "Sess6")

	for i := 1; i <= 5; i++ {
		sid := fmt.Sprintf("s-%d", i)
		_ = s.BumpPrincipalSighting(ctx, p.ID, "discord", sid, int64(i*100), "msg")
	}
	rows, err := s.ListPrincipalSessionSightings(ctx, p.ID, 0)
	if err != nil {
		t.Fatalf("ListPrincipalSessionSightings: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("len = %d, want 5 (default cap)", len(rows))
	}
}

func TestBumpPrincipalSighting_LongPreviewRoundtrips(t *testing.T) {
	s := newTestStore(t)
	p := mkContact(t, s, "P")
	long := strings.Repeat("中文测试", 50) + "\nwith newline and \"quote\""
	_ = s.BumpPrincipalSighting(ctx, p.ID, "wechat", "", 1000, long)
	rows, _ := s.GetPrincipalRecentMessages(ctx, p.ID, 10)
	if len(rows) != 1 || rows[0].Preview != long {
		t.Errorf("preview not preserved: %q", rows[0].Preview)
	}
}
