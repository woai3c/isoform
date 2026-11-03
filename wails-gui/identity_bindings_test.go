package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

var bindingsCtx = context.Background()

func newBindingsTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.New(filepath.Join(dir, "bindings-test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &App{store: store}
}

func TestApp_EnsureSelfProfile_UpdatesExistingRow(t *testing.T) {
	a := newBindingsTestApp(t)
	first, err := a.EnsureSelfProfile("Alex", []string{"ah"})
	if err != nil {
		t.Fatalf("EnsureSelfProfile first: %v", err)
	}
	if first.Kind != storage.PrincipalKindSelf {
		t.Errorf("Kind = %q, want self", first.Kind)
	}

	second, err := a.EnsureSelfProfile("Different", nil)
	if err != nil {
		t.Fatalf("EnsureSelfProfile second: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second call created new row")
	}
	if second.DisplayName != "Different" {
		t.Errorf("DisplayName = %q, want updated name Different", second.DisplayName)
	}
}

func TestApp_CreateContact_BindsHandlesInOneShot(t *testing.T) {
	a := newBindingsTestApp(t)
	contact, err := a.CreateContact("Zhang San", "friend", []string{"张三"}, []AppHandleDTO{
		{App: "wechat", HandleID: "", DisplayHandle: "@zhangsan"},
		{App: "telegram", HandleID: "tg-001", DisplayHandle: "@zhangsan_tg"},
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if contact.ID == "" {
		t.Errorf("ID missing")
	}
	if contact.Kind != storage.PrincipalKindContact {
		t.Errorf("Kind = %q, want contact", contact.Kind)
	}
	if !containsStr(contact.Aliases, "张三") {
		t.Errorf("Aliases missing 张三: %v", contact.Aliases)
	}
	if len(contact.AppHandles) != 2 {
		t.Errorf("AppHandles len = %d, want 2", len(contact.AppHandles))
	}
}

func TestApp_ListPrincipals_IncludesHandles(t *testing.T) {
	a := newBindingsTestApp(t)
	c, err := a.CreateContact("X", "friend", nil, []AppHandleDTO{
		{App: "slack", HandleID: "U001", DisplayHandle: "@x"},
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	all, err := a.ListPrincipals()
	if err != nil {
		t.Fatalf("ListPrincipals: %v", err)
	}
	var found *PrincipalDTO
	for i := range all {
		if all[i].ID == c.ID {
			found = &all[i]
			break
		}
	}
	if found == nil {
		t.Fatal("created contact missing from ListPrincipals output")
	}
	if len(found.AppHandles) != 1 {
		t.Errorf("AppHandles len = %d, want 1", len(found.AppHandles))
	}
}

func TestApp_UpdatePrincipal_RefusesKindChange(t *testing.T) {
	a := newBindingsTestApp(t)
	c, err := a.CreateContact("Y", "friend", nil, nil)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	c.Kind = storage.PrincipalKindSelf
	_, err = a.UpdatePrincipal(c)
	if err == nil {
		t.Errorf("expected kind-change rejection")
	}
}

func TestApp_UpdatePrincipal_UpdatesFieldsAndAliases(t *testing.T) {
	a := newBindingsTestApp(t)
	c, err := a.CreateContact("Z", "friend", []string{"old"}, nil)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	c.DisplayName = "Zara"
	c.Relationship = "colleague"
	c.Notes = "from work"
	c.Aliases = []string{"zee", "zara"}
	updated, err := a.UpdatePrincipal(c)
	if err != nil {
		t.Fatalf("UpdatePrincipal: %v", err)
	}
	if updated.DisplayName != "Zara" || updated.Relationship != "colleague" || updated.Notes != "from work" {
		t.Errorf("fields not persisted: %+v", updated)
	}
	if !containsStr(updated.Aliases, "zee") || !containsStr(updated.Aliases, "zara") {
		t.Errorf("Aliases = %v, want [zee zara]", updated.Aliases)
	}
}

func TestApp_DeletePrincipal_RemovesAndCascades(t *testing.T) {
	a := newBindingsTestApp(t)
	c, err := a.CreateContact("D", "friend", nil, []AppHandleDTO{
		{App: "slack", HandleID: "U999"},
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if err := a.DeletePrincipal(c.ID); err != nil {
		t.Fatalf("DeletePrincipal: %v", err)
	}
	rows, err := a.ListPrincipals()
	if err != nil {
		t.Fatalf("ListPrincipals: %v", err)
	}
	for _, r := range rows {
		if r.ID == c.ID {
			t.Errorf("DeletePrincipal did not remove row")
		}
	}
}

func TestApp_BindAndUnbindAppHandle(t *testing.T) {
	a := newBindingsTestApp(t)
	c, err := a.CreateContact("E", "friend", nil, nil)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	h, err := a.BindAppHandle(c.ID, "discord", "d-001", "@e")
	if err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}
	if h.ID == 0 || h.App != "discord" {
		t.Errorf("unexpected handle: %+v", h)
	}
	if err := a.UnbindAppHandle(h.ID); err != nil {
		t.Fatalf("UnbindAppHandle: %v", err)
	}
	if err := a.UnbindAppHandle(h.ID); err == nil {
		t.Errorf("second unbind should error")
	}
}

func TestApp_PromoteAnonymousToContact(t *testing.T) {
	a := newBindingsTestApp(t)
	// Trigger anonymous bucket creation through the resolver path.
	store := a.store
	anon, err := store.EnsureAnonymousContact(bindingsCtx, "wechat", "wxid_a", "Stranger")
	if err != nil {
		t.Fatalf("EnsureAnonymousContact: %v", err)
	}

	promoted, err := a.PromoteAnonymousToContact(anon.ID, "Mei", "family", []string{"妈"})
	if err != nil {
		t.Fatalf("PromoteAnonymousToContact: %v", err)
	}
	if promoted.Kind != storage.PrincipalKindContact || promoted.DisplayName != "Mei" {
		t.Errorf("promotion fields wrong: %+v", promoted)
	}
}

func TestApp_RequiresStore(t *testing.T) {
	a := &App{}
	if _, err := a.ListPrincipals(); err == nil {
		t.Errorf("expected error without store")
	}
	if _, err := a.EnsureSelfProfile("x", nil); err == nil {
		t.Errorf("expected error without store")
	}
	if err := a.DeletePrincipal("x"); err == nil {
		t.Errorf("expected error without store")
	}
}

func TestApp_DTOPreservesEmptyAliases(t *testing.T) {
	a := newBindingsTestApp(t)
	c, err := a.CreateContact("Q", "", nil, nil)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if c.Aliases == nil {
		t.Errorf("DTO Aliases should be empty slice, not nil")
	}
}

func TestApp_CreateContactWithDuplicateHandleRebindsToCreated(t *testing.T) {
	a := newBindingsTestApp(t)
	first, err := a.CreateContact("First", "friend", nil, []AppHandleDTO{
		{App: "slack", HandleID: "U-shared"},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := a.CreateContact("Second", "friend", nil, []AppHandleDTO{
		{App: "slack", HandleID: "U-shared"},
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	// The shared slack handle should now belong to second.
	p, err := a.store.ResolveByAppHandle(bindingsCtx, "slack", "U-shared")
	if err != nil {
		t.Fatalf("ResolveByAppHandle: %v", err)
	}
	if p == nil || p.ID != second.ID {
		t.Errorf("handle did not transfer to second contact: got %+v, want %s", p, second.ID)
	}
	// First should still exist as a principal — only the handle moved.
	if _, err := a.store.GetPrincipal(bindingsCtx, first.ID); err != nil {
		t.Errorf("first contact deleted unexpectedly: %v", err)
	}
}

// ── helpers ──

func containsStr(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// ── CleanupAnonymousPrincipals ──

func TestApp_CleanupAnonymousPrincipals_NormalizesAndMerges(t *testing.T) {
	a := newBindingsTestApp(t)
	// Three anonymous buckets that should collapse to "maya_chen".
	// 2ms sleeps make updated_at strictly increasing so the cleanup's
	// ORDER BY observes (first, second, third) order — without them
	// the UUID tiebreaker can flip the merge winner under same-ms
	// inserts and the survivor check would fail.
	first, _ := a.store.EnsureAnonymousContact(bindingsCtx, "discord", "d-1", "maya_chen 10:10")
	_ = a.store.BumpPrincipalSighting(bindingsCtx, first.ID, "discord", "", 100, "msg-1")
	time.Sleep(2 * time.Millisecond)
	second, _ := a.store.EnsureAnonymousContact(bindingsCtx, "discord", "d-2", "maya_chen OP Original Poster 10:11")
	_ = a.store.BumpPrincipalSighting(bindingsCtx, second.ID, "discord", "", 200, "msg-2")
	time.Sleep(2 * time.Millisecond)
	third, _ := a.store.EnsureAnonymousContact(bindingsCtx, "discord", "d-3", "maya_chen 10:12")
	_ = a.store.BumpPrincipalSighting(bindingsCtx, third.ID, "discord", "", 300, "msg-3")
	// Plus an unrelated anonymous that shouldn't be touched.
	other, _ := a.store.EnsureAnonymousContact(bindingsCtx, "wechat", "wx-1", "张三 09:01")
	_ = a.store.BumpPrincipalSighting(bindingsCtx, other.ID, "wechat", "", 50, "hi")

	rep, err := a.CleanupAnonymousPrincipals()
	if err != nil {
		t.Fatalf("CleanupAnonymousPrincipals: %v", err)
	}
	if rep.Scanned != 4 {
		t.Errorf("Scanned = %d, want 4", rep.Scanned)
	}
	if rep.Renamed < 2 {
		t.Errorf("Renamed = %d, want >= 2 (maya_chen survivor + 张三)", rep.Renamed)
	}
	if rep.Merged != 2 {
		t.Errorf("Merged = %d, want 2 (two duplicate maya_chen rows folded)", rep.Merged)
	}
	survivor, err := a.GetPrincipalStats(first.ID)
	if err != nil {
		t.Fatalf("first survivor missing: %v", err)
	}
	if survivor.TotalMessages != 3 {
		t.Errorf("survivor TotalMessages = %d, want 3", survivor.TotalMessages)
	}
	cleaned, _ := a.store.GetPrincipal(bindingsCtx, other.ID)
	if cleaned.DisplayName != "张三" {
		t.Errorf("张三 displayName = %q, want 张三", cleaned.DisplayName)
	}
}

func TestApp_CleanupAnonymousPrincipals_RequiresStore(t *testing.T) {
	a := &App{}
	if _, err := a.CleanupAnonymousPrincipals(); err == nil {
		t.Errorf("expected error without store")
	}
}

// ── Merge ──

func TestApp_MergePrincipals_HappyPath(t *testing.T) {
	a := newBindingsTestApp(t)
	src, _ := a.CreateContact("Zhang San (telegram)", "friend", nil, []AppHandleDTO{
		{App: "telegram", HandleID: "tg-1", DisplayHandle: "@zs"},
	})
	dst, _ := a.CreateContact("Zhang San", "friend", []string{"老叶"}, []AppHandleDTO{
		{App: "wechat", DisplayHandle: "@zs-wx"},
	})

	_ = a.store.BumpPrincipalSighting(bindingsCtx, src.ID, "telegram", "", 1000, "tg msg")
	_ = a.store.BumpPrincipalSighting(bindingsCtx, dst.ID, "wechat", "", 2000, "wx msg")

	merged, err := a.MergePrincipals(src.ID, dst.ID)
	if err != nil {
		t.Fatalf("MergePrincipals: %v", err)
	}
	if merged.ID != dst.ID {
		t.Errorf("merged ID = %q, want %q", merged.ID, dst.ID)
	}
	if len(merged.AppHandles) != 2 {
		t.Errorf("merged handles len = %d, want 2", len(merged.AppHandles))
	}
	stats, err := a.GetPrincipalStats(dst.ID)
	if err != nil {
		t.Fatalf("GetPrincipalStats: %v", err)
	}
	if stats.TotalMessages != 2 {
		t.Errorf("merged TotalMessages = %d, want 2", stats.TotalMessages)
	}
	// src is gone
	if _, err := a.GetPrincipalStats(src.ID); err == nil {
		t.Errorf("expected src to be deleted")
	}
}

func TestApp_MergePrincipals_RequiresStore(t *testing.T) {
	a := &App{}
	if _, err := a.MergePrincipals("a", "b"); err == nil {
		t.Errorf("expected error without store")
	}
}

func TestApp_MergePrincipals_RejectsSameID(t *testing.T) {
	a := newBindingsTestApp(t)
	p, _ := a.CreateContact("X", "", nil, nil)
	if _, err := a.MergePrincipals(p.ID, p.ID); err == nil {
		t.Errorf("expected error for src == dst")
	}
}

// Sanity check: PromoteAnonymousToContact refuses non-anonymous principals.
func TestApp_PromoteAnonymousRefusesNonAnonymous(t *testing.T) {
	a := newBindingsTestApp(t)
	c, err := a.CreateContact("AlreadyContact", "friend", nil, nil)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	_, err = a.PromoteAnonymousToContact(c.ID, "X", "", nil)
	if err == nil || !strings.Contains(err.Error(), "not anonymous") {
		t.Errorf("expected not-anonymous error, got %v", err)
	}
}

// ── People-page bindings ──

func TestApp_GetPrincipalStats_AggregatesAcrossApps(t *testing.T) {
	a := newBindingsTestApp(t)
	contact, err := a.CreateContact("Zhang San", "friend", nil, nil)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	_ = a.store.BumpPrincipalSighting(bindingsCtx, contact.ID, "wechat", "", 1000, "hi")
	_ = a.store.BumpPrincipalSighting(bindingsCtx, contact.ID, "wechat", "", 2000, "yo")
	_ = a.store.BumpPrincipalSighting(bindingsCtx, contact.ID, "telegram", "", 3000, "tg msg")

	stats, err := a.GetPrincipalStats(contact.ID)
	if err != nil {
		t.Fatalf("GetPrincipalStats: %v", err)
	}
	if stats.PrincipalID != contact.ID {
		t.Errorf("PrincipalID = %q, want %q", stats.PrincipalID, contact.ID)
	}
	if stats.TotalMessages != 3 {
		t.Errorf("TotalMessages = %d, want 3", stats.TotalMessages)
	}
	if stats.FirstSeenAt != 1000 || stats.LastSeenAt != 3000 {
		t.Errorf("first=%d last=%d, want 1000/3000", stats.FirstSeenAt, stats.LastSeenAt)
	}
	if len(stats.ByApp) != 2 {
		t.Errorf("ByApp len = %d, want 2", len(stats.ByApp))
	}
	if len(stats.RecentMessages) != 3 {
		t.Errorf("RecentMessages len = %d, want 3", len(stats.RecentMessages))
	}
}

func TestApp_GetPrincipalStats_ZeroForNeverSighted(t *testing.T) {
	a := newBindingsTestApp(t)
	contact, err := a.CreateContact("Newcomer", "friend", nil, nil)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	stats, err := a.GetPrincipalStats(contact.ID)
	if err != nil {
		t.Fatalf("GetPrincipalStats: %v", err)
	}
	if stats.TotalMessages != 0 || len(stats.ByApp) != 0 || len(stats.RecentMessages) != 0 {
		t.Errorf("expected zero stats, got %+v", stats)
	}
}

func TestApp_GetPrincipalStats_MissingPrincipalErrors(t *testing.T) {
	a := newBindingsTestApp(t)
	if _, err := a.GetPrincipalStats("does-not-exist"); err == nil {
		t.Errorf("expected error for missing principal")
	}
}

func TestApp_GetPrincipalStats_RequiresStore(t *testing.T) {
	a := &App{}
	if _, err := a.GetPrincipalStats("x"); err == nil {
		t.Errorf("expected error without store")
	}
}

func TestApp_ListPrincipalsWithStats_IncludesEveryone(t *testing.T) {
	a := newBindingsTestApp(t)
	self, _ := a.EnsureSelfProfile("Me", nil)
	c1, _ := a.CreateContact("Zhang San", "friend", nil, []AppHandleDTO{
		{App: "wechat", DisplayHandle: "@zs"},
	})
	c2, _ := a.CreateContact("Newcomer", "colleague", nil, nil)

	// Only c1 has sightings; c2 should still appear with zero stats.
	_ = a.store.BumpPrincipalSighting(bindingsCtx, c1.ID, "wechat", "", 1000, "hi")

	all, err := a.ListPrincipalsWithStats()
	if err != nil {
		t.Fatalf("ListPrincipalsWithStats: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 entries (self + 2 contacts), got %d", len(all))
	}
	byID := map[string]PrincipalWithStatsDTO{}
	for _, p := range all {
		byID[p.Principal.ID] = p
	}
	if byID[self.ID].Principal.Kind != "self" {
		t.Errorf("self entry missing or wrong kind")
	}
	if byID[c1.ID].Stats.TotalMessages != 1 {
		t.Errorf("c1 TotalMessages = %d, want 1", byID[c1.ID].Stats.TotalMessages)
	}
	if byID[c2.ID].Stats.TotalMessages != 0 {
		t.Errorf("c2 TotalMessages = %d, want 0", byID[c2.ID].Stats.TotalMessages)
	}
	if len(byID[c1.ID].Principal.AppHandles) != 1 {
		t.Errorf("c1 AppHandles len = %d, want 1", len(byID[c1.ID].Principal.AppHandles))
	}
}

func TestApp_ListPrincipalsWithStats_RequiresStore(t *testing.T) {
	a := &App{}
	if _, err := a.ListPrincipalsWithStats(); err == nil {
		t.Errorf("expected error without store")
	}
}

func TestApp_ListPrincipalsWithStats_EmptyDB(t *testing.T) {
	a := newBindingsTestApp(t)
	all, err := a.ListPrincipalsWithStats()
	if err != nil {
		t.Fatalf("ListPrincipalsWithStats: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 entries, got %d", len(all))
	}
}

// Sanity check: UpdatePrincipal on a missing id returns ErrNoRows.
func TestApp_UpdatePrincipal_MissingReturnsErrNoRows(t *testing.T) {
	a := newBindingsTestApp(t)
	_, err := a.UpdatePrincipal(PrincipalDTO{ID: "ghost", Kind: storage.PrincipalKindContact, DisplayName: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errMissingPrincipal()) && !strings.Contains(err.Error(), "no rows") {
		t.Errorf("err = %v, want sql.ErrNoRows-shaped error", err)
	}
}

// errMissingPrincipal is a sentinel-style helper to make the test above
// less brittle if the error layering changes — we treat any "no rows"
// shaped error as acceptable.
func errMissingPrincipal() error { return errors.New("no rows in result set") }

// ── qualifyDisplayName ──

func TestQualifyDisplayName(t *testing.T) {
	type tc struct {
		name        string
		kind        string
		displayName string
		handles     []AppHandleDTO
		want        string
	}
	cases := []tc{
		{
			name:        "anonymous_single_app_gets_qualifier",
			kind:        storage.PrincipalKindAnonymous,
			displayName: "Mei",
			handles:     []AppHandleDTO{{App: "wechat", DisplayHandle: "Mei"}},
			want:        "WeChat · Mei",
		},
		{
			name:        "anonymous_discord_brand_casing",
			kind:        storage.PrincipalKindAnonymous,
			displayName: "alex_halcyon",
			handles:     []AppHandleDTO{{App: "discord", DisplayHandle: "alex_halcyon"}},
			want:        "Discord · alex_halcyon",
		},
		{
			name:        "anonymous_multi_app_drops_qualifier",
			kind:        storage.PrincipalKindAnonymous,
			displayName: "Mei",
			handles: []AppHandleDTO{
				{App: "wechat", DisplayHandle: "Mei"},
				{App: "discord", DisplayHandle: "Mei"},
			},
			want: "Mei",
		},
		{
			name:        "anonymous_no_handles_falls_back",
			kind:        storage.PrincipalKindAnonymous,
			displayName: "Mei",
			handles:     nil,
			want:        "Mei",
		},
		{
			name:        "self_never_qualifies",
			kind:        storage.PrincipalKindSelf,
			displayName: "alex_halcyon",
			handles:     []AppHandleDTO{{App: "discord", DisplayHandle: "alex_halcyon"}},
			want:        "alex_halcyon",
		},
		{
			name:        "contact_never_qualifies",
			kind:        storage.PrincipalKindContact,
			displayName: "妈",
			handles:     []AppHandleDTO{{App: "wechat", DisplayHandle: "Mei"}},
			want:        "妈",
		},
		{
			name:        "duplicate_app_entries_count_as_one",
			kind:        storage.PrincipalKindAnonymous,
			displayName: "Bob",
			handles: []AppHandleDTO{
				{App: "discord", DisplayHandle: "Bob"},
				{App: "Discord", DisplayHandle: "Bob#1234"},
			},
			want: "Discord · Bob",
		},
		{
			name:        "empty_display_returns_empty",
			kind:        storage.PrincipalKindAnonymous,
			displayName: "",
			handles:     []AppHandleDTO{{App: "discord", DisplayHandle: "x"}},
			want:        "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := qualifyDisplayName(c.kind, c.displayName, c.handles)
			if got != c.want {
				t.Errorf("qualifyDisplayName(%s, %q, %+v) = %q, want %q",
					c.kind, c.displayName, c.handles, got, c.want)
			}
		})
	}
}

func TestApp_ListPrincipals_PopulatesQualifiedDisplayName(t *testing.T) {
	a := newBindingsTestApp(t)
	anon, _ := a.store.EnsureAnonymousContact(bindingsCtx, "discord", "d-1", "alex_halcyon")
	contact, _ := a.CreateContact("Mei", "family", []string{"妈"}, []AppHandleDTO{
		{App: "wechat", HandleID: "wx-1", DisplayHandle: "Mei"},
	})

	rows, err := a.ListPrincipals()
	if err != nil {
		t.Fatalf("ListPrincipals: %v", err)
	}
	var anonDTO, contactDTO *PrincipalDTO
	for i := range rows {
		switch rows[i].ID {
		case anon.ID:
			anonDTO = &rows[i]
		case contact.ID:
			contactDTO = &rows[i]
		}
	}
	if anonDTO == nil {
		t.Fatal("anonymous principal missing from list")
	}
	if anonDTO.QualifiedDisplayName != "Discord · alex_halcyon" {
		t.Errorf("anonymous QualifiedDisplayName = %q, want %q",
			anonDTO.QualifiedDisplayName, "Discord · alex_halcyon")
	}
	if contactDTO == nil {
		t.Fatal("contact missing from list")
	}
	if contactDTO.QualifiedDisplayName != "Mei" {
		t.Errorf("contact QualifiedDisplayName = %q, want %q",
			contactDTO.QualifiedDisplayName, "Mei")
	}
}
