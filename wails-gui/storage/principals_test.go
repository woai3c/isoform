package storage

import (
	"database/sql"
	"errors"
	"sort"
	"sync"
	"testing"
)

// ── helpers ──

func mustCreate(t *testing.T, s *Store, p PrincipalRow) PrincipalRow {
	t.Helper()
	row, err := s.CreatePrincipal(ctx, p)
	if err != nil {
		t.Fatalf("CreatePrincipal(%+v): %v", p, err)
	}
	return row
}

// ── CreatePrincipal ──

func TestCreatePrincipal_RejectsInvalidKind(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreatePrincipal(ctx, PrincipalRow{Kind: "wizard", DisplayName: "x"})
	if err == nil {
		t.Fatal("expected error for invalid kind")
	}
}

func TestCreatePrincipal_AssignsIDAndTimestamps(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{
		Kind:        PrincipalKindContact,
		DisplayName: "Alice",
	})
	if row.ID == "" {
		t.Errorf("ID was not generated")
	}
	if row.CreatedAt == 0 || row.UpdatedAt == 0 {
		t.Errorf("timestamps not set: created=%d updated=%d", row.CreatedAt, row.UpdatedAt)
	}
	if row.Confidence != 1.0 {
		t.Errorf("default confidence = %v, want 1.0", row.Confidence)
	}
}

func TestCreatePrincipal_PersistsAliases(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{
		Kind:        PrincipalKindContact,
		DisplayName: "Bob",
		Aliases:     []string{"bobby", "Robert", "bobby"}, // duplicate dropped
	})

	got, err := s.GetPrincipal(ctx, row.ID)
	if err != nil {
		t.Fatalf("GetPrincipal: %v", err)
	}
	if got.DisplayName != "Bob" {
		t.Errorf("DisplayName = %q, want Bob", got.DisplayName)
	}
	want := []string{"Robert", "bobby"}
	if !equalSorted(got.Aliases, want) {
		t.Errorf("Aliases = %v, want %v", got.Aliases, want)
	}
}

// ── UpdatePrincipal ──

func TestUpdatePrincipal_ReplacesFieldsAndAliases(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{
		Kind:        PrincipalKindContact,
		DisplayName: "Carl",
		Aliases:     []string{"old1", "old2"},
	})

	row.DisplayName = "Carlos"
	row.Aliases = []string{"new1"}
	row.Relationship = "friend"
	row.Notes = "from school"

	updated, err := s.UpdatePrincipal(ctx, row)
	if err != nil {
		t.Fatalf("UpdatePrincipal: %v", err)
	}
	if updated.DisplayName != "Carlos" {
		t.Errorf("DisplayName = %q, want Carlos", updated.DisplayName)
	}
	if updated.Relationship != "friend" {
		t.Errorf("Relationship = %q, want friend", updated.Relationship)
	}
	if !equalSorted(updated.Aliases, []string{"new1"}) {
		t.Errorf("Aliases = %v, want [new1]", updated.Aliases)
	}
}

func TestUpdatePrincipal_MissingReturnsErrNoRows(t *testing.T) {
	s := newTestStore(t)
	_, err := s.UpdatePrincipal(ctx, PrincipalRow{ID: "ghost", Kind: PrincipalKindContact, DisplayName: "x"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdatePrincipal missing = %v, want sql.ErrNoRows", err)
	}
}

// ── DeletePrincipal + cascade ──

func TestDeletePrincipal_CascadesAliasesAndHandles(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{
		Kind:        PrincipalKindContact,
		DisplayName: "Dave",
		Aliases:     []string{"davey"},
	})
	if _, err := s.BindAppHandle(ctx, row.ID, "telegram", "tg-123", "@dave"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}

	if err := s.DeletePrincipal(ctx, row.ID); err != nil {
		t.Fatalf("DeletePrincipal: %v", err)
	}

	if _, err := s.GetPrincipal(ctx, row.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetPrincipal after delete = %v, want sql.ErrNoRows", err)
	}
	if hs, err := s.ListAppHandlesByPrincipal(ctx, row.ID); err != nil || len(hs) != 0 {
		t.Errorf("handles not cascaded: err=%v len=%d", err, len(hs))
	}
}

func TestDeletePrincipal_MissingReturnsErrNoRows(t *testing.T) {
	s := newTestStore(t)
	if err := s.DeletePrincipal(ctx, "ghost"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeletePrincipal missing = %v, want sql.ErrNoRows", err)
	}
}

// ── List / ListByKind ──

func TestListPrincipals_OrdersByKindAndName(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "Zara"})
	mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "Alice"})
	mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContentSubject, DisplayName: "VideoX"})

	rows, err := s.ListPrincipals(ctx)
	if err != nil {
		t.Fatalf("ListPrincipals: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("ListPrincipals returned %d rows, want 3", len(rows))
	}
	if rows[0].DisplayName != "Alice" || rows[1].DisplayName != "Zara" || rows[2].DisplayName != "VideoX" {
		t.Errorf("unexpected order: %v", names(rows))
	}
}

func TestListPrincipalsByKind_Filters(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "A"})
	mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "B"})
	mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContentSubject, DisplayName: "C"})

	rows, err := s.ListPrincipalsByKind(ctx, PrincipalKindContact)
	if err != nil {
		t.Fatalf("ListPrincipalsByKind: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("contact count = %d, want 2", len(rows))
	}
}

func TestListPrincipalsByKind_RejectsInvalid(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ListPrincipalsByKind(ctx, "alien"); err == nil {
		t.Errorf("expected error for invalid kind")
	}
}

// ── EnsureSelf ──

func TestEnsureSelf_CreatesThenIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	first, err := s.EnsureSelf(ctx, "Yetone", []string{"yt"})
	if err != nil {
		t.Fatalf("EnsureSelf first: %v", err)
	}
	if first.Kind != PrincipalKindSelf {
		t.Errorf("Kind = %q, want self", first.Kind)
	}
	if first.CreatedFrom != "onboarding" {
		t.Errorf("CreatedFrom = %q, want onboarding", first.CreatedFrom)
	}

	second, err := s.EnsureSelf(ctx, "Different Name", []string{"ignored"})
	if err != nil {
		t.Fatalf("EnsureSelf second: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second call created new row: first=%q second=%q", first.ID, second.ID)
	}
	if second.DisplayName != "Yetone" {
		t.Errorf("EnsureSelf overwrote DisplayName: got %q, want Yetone", second.DisplayName)
	}
}

func TestEnsureSelf_UniqueConstraintBlocksManualSecond(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.EnsureSelf(ctx, "First", nil); err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}
	_, err := s.CreatePrincipal(ctx, PrincipalRow{Kind: PrincipalKindSelf, DisplayName: "Second"})
	if !errors.Is(err, ErrSelfAlreadyExists) {
		t.Errorf("second self CreatePrincipal = %v, want ErrSelfAlreadyExists", err)
	}
}

// ── BindAppHandle ──

func TestBindAppHandle_NormalizesAppCase(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "E"})
	h, err := s.BindAppHandle(ctx, row.ID, "Telegram", "tg-42", "@e")
	if err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}
	if h.App != "telegram" {
		t.Errorf("App = %q, want telegram (lowercased)", h.App)
	}
}

func TestBindAppHandle_DedupsOnUniqueTuple(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "F"})
	h1, err := s.BindAppHandle(ctx, row.ID, "wechat", "wxid_1", "@f")
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}
	h2, err := s.BindAppHandle(ctx, row.ID, "wechat", "wxid_1", "@f")
	if err != nil {
		t.Fatalf("second bind: %v", err)
	}
	if h1.ID != h2.ID {
		t.Errorf("duplicate bind produced new row: %d vs %d", h1.ID, h2.ID)
	}
	hs, err := s.ListAppHandlesByPrincipal(ctx, row.ID)
	if err != nil {
		t.Fatalf("ListAppHandlesByPrincipal: %v", err)
	}
	if len(hs) != 1 {
		t.Errorf("List returned %d rows, want 1 after dedup", len(hs))
	}
}

func TestBindAppHandle_RebindsToDifferentPrincipal(t *testing.T) {
	s := newTestStore(t)
	rowA := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "A"})
	rowB := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "B"})
	if _, err := s.BindAppHandle(ctx, rowA.ID, "slack", "U0001", ""); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if _, err := s.BindAppHandle(ctx, rowB.ID, "slack", "U0001", ""); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	p, err := s.ResolveByAppHandle(ctx, "slack", "U0001")
	if err != nil {
		t.Fatalf("ResolveByAppHandle: %v", err)
	}
	if p == nil || p.ID != rowB.ID {
		t.Errorf("rebind did not transfer ownership; got %+v", p)
	}
}

func TestBindAppHandle_MissingPrincipalReturnsErrNoRows(t *testing.T) {
	s := newTestStore(t)
	_, err := s.BindAppHandle(ctx, "ghost", "telegram", "tg-1", "@g")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("BindAppHandle missing principal = %v, want sql.ErrNoRows", err)
	}
}

func TestBindAppHandle_RejectsEmptyInputs(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "G"})
	if _, err := s.BindAppHandle(ctx, row.ID, "", "h", "d"); err == nil {
		t.Errorf("empty app should error")
	}
	if _, err := s.BindAppHandle(ctx, row.ID, "telegram", "", ""); err == nil {
		t.Errorf("empty handle id and display should error")
	}
}

// ── UnbindAppHandle ──

func TestUnbindAppHandle(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "H"})
	h, _ := s.BindAppHandle(ctx, row.ID, "discord", "d-1", "@h")

	if err := s.UnbindAppHandle(ctx, h.ID); err != nil {
		t.Fatalf("UnbindAppHandle: %v", err)
	}
	if err := s.UnbindAppHandle(ctx, h.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("second unbind = %v, want sql.ErrNoRows", err)
	}
}

// ── ResolveByAppHandle ──

func TestResolveByAppHandle_MissingReturnsNilNil(t *testing.T) {
	s := newTestStore(t)
	p, err := s.ResolveByAppHandle(ctx, "telegram", "nope")
	if err != nil {
		t.Errorf("err = %v, want nil for missing handle", err)
	}
	if p != nil {
		t.Errorf("p = %+v, want nil", p)
	}
}

func TestResolveByAppHandle_EmptyArgsReturnNilNil(t *testing.T) {
	s := newTestStore(t)
	if p, err := s.ResolveByAppHandle(ctx, "", "x"); err != nil || p != nil {
		t.Errorf("empty app: p=%v err=%v", p, err)
	}
	if p, err := s.ResolveByAppHandle(ctx, "telegram", ""); err != nil || p != nil {
		t.Errorf("empty handle: p=%v err=%v", p, err)
	}
}

func TestResolveByAppHandle_AppMatchIsCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "I"})
	if _, err := s.BindAppHandle(ctx, row.ID, "telegram", "tg-X", "@i"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	p, err := s.ResolveByAppHandle(ctx, "TELEGRAM", "tg-X")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p == nil || p.ID != row.ID {
		t.Errorf("uppercase lookup failed: %+v", p)
	}
}

// ── ResolveByDisplayHandle ──

func TestResolveByDisplayHandle_IsCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "J"})
	if _, err := s.BindAppHandle(ctx, row.ID, "wechat", "", "wxid_J"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	p, err := s.ResolveByDisplayHandle(ctx, "wechat", "WXID_J")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p == nil || p.ID != row.ID {
		t.Errorf("case-insensitive display match failed: %+v", p)
	}
}

// ── ResolveByAlias ──

func TestResolveByAlias_AliasOutranksDisplayName(t *testing.T) {
	s := newTestStore(t)
	a := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "Same Name"})
	b := mustCreate(t, s, PrincipalRow{
		Kind:        PrincipalKindContact,
		DisplayName: "Other",
		Aliases:     []string{"Same Name"},
	})

	p, err := s.ResolveByAlias(ctx, "Same Name")
	if err != nil {
		t.Fatalf("ResolveByAlias: %v", err)
	}
	if p == nil {
		t.Fatal("got nil")
	}
	// alias match (on b) should win over display_name match (on a)
	if p.ID != b.ID {
		t.Errorf("alias did not outrank display_name: got %s (a=%s b=%s)", p.ID, a.ID, b.ID)
	}
}

func TestResolveByAlias_DisplayNameFallback(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "K"})

	p, err := s.ResolveByAlias(ctx, "k") // case-insensitive
	if err != nil {
		t.Fatalf("ResolveByAlias: %v", err)
	}
	if p == nil || p.ID != row.ID {
		t.Errorf("display_name fallback failed: %+v", p)
	}
}

func TestResolveByAlias_MissingReturnsNilNil(t *testing.T) {
	s := newTestStore(t)
	p, err := s.ResolveByAlias(ctx, "nobody")
	if err != nil || p != nil {
		t.Errorf("p=%v err=%v, want both nil", p, err)
	}
}

func TestResolveByAlias_EmptyReturnsNilNil(t *testing.T) {
	s := newTestStore(t)
	p, err := s.ResolveByAlias(ctx, "  ")
	if err != nil || p != nil {
		t.Errorf("whitespace input: p=%v err=%v", p, err)
	}
}

// ── EnsureAnonymousContact ──

func TestEnsureAnonymousContact_CreatesOnceThenReuses(t *testing.T) {
	s := newTestStore(t)
	a, err := s.EnsureAnonymousContact(ctx, "telegram", "tg-99", "@stranger")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if a.Kind != PrincipalKindAnonymous {
		t.Errorf("Kind = %q, want anonymous", a.Kind)
	}
	b, err := s.EnsureAnonymousContact(ctx, "telegram", "tg-99", "@stranger")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("anonymous bucket not deduplicated: %s vs %s", a.ID, b.ID)
	}
}

func TestEnsureAnonymousContact_DedupsByDisplayHandleAlone(t *testing.T) {
	s := newTestStore(t)
	a, err := s.EnsureAnonymousContact(ctx, "wechat", "", "wxid_only")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := s.EnsureAnonymousContact(ctx, "wechat", "", "wxid_only")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("dedup by display handle failed: %s vs %s", a.ID, b.ID)
	}
}

func TestEnsureAnonymousContact_RejectsEmptyApp(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.EnsureAnonymousContact(ctx, "", "h", "d"); err == nil {
		t.Errorf("empty app should error")
	}
}

// ── PromoteAnonymousToContact ──

func TestPromoteAnonymousToContact(t *testing.T) {
	s := newTestStore(t)
	anon, err := s.EnsureAnonymousContact(ctx, "wechat", "wxid_x", "Stranger")
	if err != nil {
		t.Fatalf("EnsureAnonymousContact: %v", err)
	}

	promoted, err := s.PromoteAnonymousToContact(ctx, anon.ID, "Zhang San", "friend", []string{"张三"})
	if err != nil {
		t.Fatalf("PromoteAnonymousToContact: %v", err)
	}
	if promoted.Kind != PrincipalKindContact {
		t.Errorf("Kind = %q, want contact", promoted.Kind)
	}
	if promoted.DisplayName != "Zhang San" || promoted.Relationship != "friend" {
		t.Errorf("fields not updated: %+v", promoted)
	}
	if !equalSorted(promoted.Aliases, []string{"张三"}) {
		t.Errorf("Aliases = %v, want [张三]", promoted.Aliases)
	}

	// handle should still resolve to the same id
	p, err := s.ResolveByAppHandle(ctx, "wechat", "wxid_x")
	if err != nil {
		t.Fatalf("ResolveByAppHandle: %v", err)
	}
	if p == nil || p.ID != promoted.ID || p.Kind != PrincipalKindContact {
		t.Errorf("handle did not survive promotion: %+v", p)
	}
}

func TestPromoteAnonymousToContact_RefusesNonAnonymous(t *testing.T) {
	s := newTestStore(t)
	row := mustCreate(t, s, PrincipalRow{Kind: PrincipalKindContact, DisplayName: "Already Contact"})
	if _, err := s.PromoteAnonymousToContact(ctx, row.ID, "X", "", nil); err == nil {
		t.Errorf("expected error promoting non-anonymous")
	}
}

// ── Concurrency ──

func TestEnsureSelf_ConcurrentCallersConverge(t *testing.T) {
	s := newTestStore(t)
	const N = 10
	var wg sync.WaitGroup
	ids := make([]string, N)
	errs := make([]error, N)
	wg.Add(N)
	for i := range N {
		go func(i int) {
			defer wg.Done()
			r, err := s.EnsureSelf(ctx, "Race", []string{"r"})
			ids[i], errs[i] = r.ID, err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d EnsureSelf: %v", i, err)
		}
	}
	for i := 1; i < N; i++ {
		if ids[i] != ids[0] {
			t.Errorf("goroutine %d got id %q, want %q", i, ids[i], ids[0])
		}
	}
	all, err := s.ListPrincipalsByKind(ctx, PrincipalKindSelf)
	if err != nil {
		t.Fatalf("ListPrincipalsByKind: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("multiple self rows persisted: %d", len(all))
	}
}

// ── helpers ──

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string{}, a...)
	bb := append([]string{}, b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func names(rows []PrincipalRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.DisplayName
	}
	return out
}
