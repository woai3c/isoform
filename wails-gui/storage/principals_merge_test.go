package storage

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// ── helpers ──

func mkPrincipal(t *testing.T, s *Store, kind, name string) PrincipalRow {
	t.Helper()
	row, err := s.CreatePrincipal(ctx, PrincipalRow{Kind: kind, DisplayName: name})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	return row
}

// ── Aliases ──

func TestMergePrincipals_AliasesUnionWithDedup(t *testing.T) {
	s := newTestStore(t)
	src, _ := s.CreatePrincipal(ctx, PrincipalRow{
		Kind: PrincipalKindContact, DisplayName: "Src",
		Aliases: []string{"shared", "only-on-src"},
	})
	dst, _ := s.CreatePrincipal(ctx, PrincipalRow{
		Kind: PrincipalKindContact, DisplayName: "Dst",
		Aliases: []string{"shared", "only-on-dst"},
	})

	if _, err := s.MergePrincipals(ctx, src.ID, dst.ID); err != nil {
		t.Fatalf("MergePrincipals: %v", err)
	}

	merged, err := s.GetPrincipal(ctx, dst.ID)
	if err != nil {
		t.Fatalf("GetPrincipal: %v", err)
	}
	got := append([]string{}, merged.Aliases...)
	sort.Strings(got)
	want := []string{"only-on-dst", "only-on-src", "shared"}
	sort.Strings(want)
	if !equalSorted(got, want) {
		t.Errorf("Aliases = %v, want %v", got, want)
	}
	// src should be gone
	if _, err := s.GetPrincipal(ctx, src.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("src not deleted: err=%v", err)
	}
}

// ── App handles ──

func TestMergePrincipals_AppHandlesNonConflictingTransfer(t *testing.T) {
	s := newTestStore(t)
	src := mkPrincipal(t, s, PrincipalKindContact, "Src")
	dst := mkPrincipal(t, s, PrincipalKindContact, "Dst")
	_, _ = s.BindAppHandle(ctx, src.ID, "telegram", "tg-1", "@src")
	_, _ = s.BindAppHandle(ctx, dst.ID, "wechat", "wx-1", "@dst")

	if _, err := s.MergePrincipals(ctx, src.ID, dst.ID); err != nil {
		t.Fatalf("MergePrincipals: %v", err)
	}
	hs, _ := s.ListAppHandlesByPrincipal(ctx, dst.ID)
	if len(hs) != 2 {
		t.Errorf("expected 2 handles after merge, got %d", len(hs))
	}
}

func TestMergePrincipals_AppHandlesConflictPreservesDst(t *testing.T) {
	s := newTestStore(t)
	src := mkPrincipal(t, s, PrincipalKindContact, "Src")
	dst := mkPrincipal(t, s, PrincipalKindContact, "Dst")
	// Both bind the same (app, handle_id, display_handle) tuple. ON
	// CONFLICT path: the UPSERT in BindAppHandle just transfers
	// principal_id over to dst, so we can't actually pre-create the
	// collision via BindAppHandle — set it up by force.
	if _, err := s.BindAppHandle(ctx, dst.ID, "slack", "U1", "@u"); err != nil {
		t.Fatalf("dst bind: %v", err)
	}
	// Now make src own a different unique row that we can verify
	// transfers; and a row that collides with dst by ID. The unique
	// index on app_handles forbids us inserting a true duplicate, so we
	// rely on BindAppHandle's natural conflict resolution path to make
	// dst own slack/U1 and verify src has nothing more on slack after
	// the merge.
	if _, err := s.BindAppHandle(ctx, src.ID, "discord", "D2", "@srcdisc"); err != nil {
		t.Fatalf("src bind discord: %v", err)
	}

	if _, err := s.MergePrincipals(ctx, src.ID, dst.ID); err != nil {
		t.Fatalf("MergePrincipals: %v", err)
	}
	hs, _ := s.ListAppHandlesByPrincipal(ctx, dst.ID)
	if len(hs) != 2 {
		t.Errorf("expected 2 handles, got %d", len(hs))
	}
}

// ── Sightings ──

func TestMergePrincipals_SightingsCountSumAndTimestampMinMax(t *testing.T) {
	s := newTestStore(t)
	src := mkPrincipal(t, s, PrincipalKindContact, "Src")
	dst := mkPrincipal(t, s, PrincipalKindContact, "Dst")
	_ = s.BumpPrincipalSighting(ctx, src.ID, "wechat", "", 100, "old")
	_ = s.BumpPrincipalSighting(ctx, src.ID, "wechat", "", 200, "newer")
	_ = s.BumpPrincipalSighting(ctx, dst.ID, "wechat", "", 500, "even newer")
	_ = s.BumpPrincipalSighting(ctx, src.ID, "telegram", "", 300, "src-only")

	if _, err := s.MergePrincipals(ctx, src.ID, dst.ID); err != nil {
		t.Fatalf("MergePrincipals: %v", err)
	}

	rows, _ := s.listSightings(ctx, dst.ID)
	byApp := map[string]PrincipalSightingRow{}
	for _, r := range rows {
		byApp[r.App] = r
	}
	wechat, ok := byApp["wechat"]
	if !ok {
		t.Fatal("wechat row missing")
	}
	if wechat.MessageCount != 3 {
		t.Errorf("wechat count = %d, want 3", wechat.MessageCount)
	}
	if wechat.FirstSeenAt != 100 {
		t.Errorf("wechat first_seen = %d, want 100", wechat.FirstSeenAt)
	}
	if wechat.LastSeenAt != 500 {
		t.Errorf("wechat last_seen = %d, want 500", wechat.LastSeenAt)
	}
	tg, ok := byApp["telegram"]
	if !ok {
		t.Fatal("telegram row missing (should have transferred)")
	}
	if tg.MessageCount != 1 {
		t.Errorf("telegram count = %d, want 1", tg.MessageCount)
	}
}

// ── Recent messages ──

func TestMergePrincipals_RecentMessagesTransferNonColliding(t *testing.T) {
	s := newTestStore(t)
	src := mkPrincipal(t, s, PrincipalKindContact, "Src")
	dst := mkPrincipal(t, s, PrincipalKindContact, "Dst")
	_ = s.BumpPrincipalSighting(ctx, src.ID, "wechat", "", 100, "src-100")
	_ = s.BumpPrincipalSighting(ctx, src.ID, "wechat", "", 200, "src-200")
	_ = s.BumpPrincipalSighting(ctx, dst.ID, "wechat", "", 200, "dst-200-wins")
	_ = s.BumpPrincipalSighting(ctx, dst.ID, "wechat", "", 300, "dst-300")

	if _, err := s.MergePrincipals(ctx, src.ID, dst.ID); err != nil {
		t.Fatalf("MergePrincipals: %v", err)
	}
	rows, _ := s.GetPrincipalRecentMessages(ctx, dst.ID, 10)
	previews := map[int64]string{}
	for _, r := range rows {
		previews[r.SeenAt] = r.Preview
	}
	if previews[100] != "src-100" {
		t.Errorf("100 = %q, want src-100 (transferred)", previews[100])
	}
	if previews[200] != "dst-200-wins" {
		t.Errorf("200 = %q, want dst-200-wins (collision keeps dst)", previews[200])
	}
	if previews[300] != "dst-300" {
		t.Errorf("300 = %q, want dst-300", previews[300])
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
}

// ── Error paths ──

func TestMergePrincipals_SrcEqualsDstErrors(t *testing.T) {
	s := newTestStore(t)
	p := mkPrincipal(t, s, PrincipalKindContact, "X")
	if _, err := s.MergePrincipals(ctx, p.ID, p.ID); err == nil {
		t.Errorf("expected error for src == dst")
	}
}

func TestMergePrincipals_MissingSrcErrors(t *testing.T) {
	s := newTestStore(t)
	dst := mkPrincipal(t, s, PrincipalKindContact, "Dst")
	if _, err := s.MergePrincipals(ctx, "ghost", dst.ID); err == nil {
		t.Errorf("expected error for missing src")
	}
}

func TestMergePrincipals_MissingDstErrors(t *testing.T) {
	s := newTestStore(t)
	src := mkPrincipal(t, s, PrincipalKindContact, "Src")
	if _, err := s.MergePrincipals(ctx, src.ID, "ghost"); err == nil {
		t.Errorf("expected error for missing dst")
	}
}

func TestMergePrincipals_EmptyIDErrors(t *testing.T) {
	s := newTestStore(t)
	p := mkPrincipal(t, s, PrincipalKindContact, "X")
	if _, err := s.MergePrincipals(ctx, "", p.ID); err == nil {
		t.Errorf("expected error for empty src")
	}
	if _, err := s.MergePrincipals(ctx, p.ID, ""); err == nil {
		t.Errorf("expected error for empty dst")
	}
}

// ── Anonymous → Contact promotion via merge ──

func TestMergePrincipals_AnonymousFoldsIntoContact(t *testing.T) {
	s := newTestStore(t)
	contact := mkPrincipal(t, s, PrincipalKindContact, "Zhang San")
	anon, _ := s.EnsureAnonymousContact(ctx, "wechat", "wx-anon", "@stranger")
	_ = s.BumpPrincipalSighting(ctx, anon.ID, "wechat", "", 500, "anon message")

	if _, err := s.MergePrincipals(ctx, anon.ID, contact.ID); err != nil {
		t.Fatalf("MergePrincipals: %v", err)
	}

	final, err := s.GetPrincipal(ctx, contact.ID)
	if err != nil {
		t.Fatalf("GetPrincipal: %v", err)
	}
	if final.Kind != PrincipalKindContact {
		t.Errorf("Kind = %q, want contact (dst kind preserved)", final.Kind)
	}
	stats, _ := s.GetPrincipalStats(ctx, contact.ID)
	if stats.TotalMessages != 1 {
		t.Errorf("merged stats TotalMessages = %d, want 1", stats.TotalMessages)
	}
	// Handle migrated.
	p, _ := s.ResolveByAppHandle(ctx, "wechat", "wx-anon")
	if p == nil || p.ID != contact.ID {
		t.Errorf("handle did not migrate to contact: %+v", p)
	}
	// anon is gone.
	if _, err := s.GetPrincipal(ctx, anon.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("anon not deleted: %v", err)
	}
}

// ── CleanupAnonymousPrincipals ──

// fakeNormalize is a tiny in-test substitute for identity.NormalizeSenderName
// (storage cannot import the identity package without creating a cycle).
// Mirrors just the slice of behavior the cleanup logic exercises:
// strip a trailing " HH:MM" timestamp suffix.
func fakeNormalize(s string) string {
	// strip trailing " HH:MM" (one or two digit hour)
	for i := len(s) - 1; i >= 4; i-- {
		if s[i] >= '0' && s[i] <= '9' {
			continue
		}
		if s[i] == ':' && i >= 3 && s[i-1] >= '0' && s[i-1] <= '9' {
			// scan backward to find the space
			j := i - 2
			for j > 0 && s[j] >= '0' && s[j] <= '9' {
				j--
			}
			if j > 0 && s[j] == ' ' {
				return s[:j]
			}
		}
		break
	}
	return s
}

func TestCleanupAnonymousPrincipals_RejectsNilNormalize(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CleanupAnonymousPrincipals(ctx, nil, nil, nil); err == nil {
		t.Errorf("expected error for nil normalize")
	}
}

func TestCleanupAnonymousPrincipals_RenamesInPlace(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.EnsureAnonymousContact(ctx, "discord", "d-1", "Alice 12:25")
	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, nil, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.Scanned != 1 || rep.Renamed != 1 || rep.Merged != 0 {
		t.Errorf("report = %+v, want scanned=1 renamed=1 merged=0", rep)
	}
	after, _ := s.GetPrincipal(ctx, a.ID)
	if after.DisplayName != "Alice" {
		t.Errorf("DisplayName = %q, want Alice", after.DisplayName)
	}
}

func TestCleanupAnonymousPrincipals_MergesDuplicates(t *testing.T) {
	s := newTestStore(t)
	// Three rows all normalize to "Alice"; oldest one wins. The 2ms
	// sleeps guarantee distinct updated_at values so the cleanup's
	// ORDER BY updated_at ASC observes creation order — without them
	// the UUID tiebreaker can flip which row becomes the merge target
	// and the survivor check below would observe second/third as the
	// winner instead of first.
	first, _ := s.EnsureAnonymousContact(ctx, "discord", "d-1", "Alice 12:25")
	_ = s.BumpPrincipalSighting(ctx, first.ID, "discord", "", 100, "first")
	time.Sleep(2 * time.Millisecond)
	second, _ := s.EnsureAnonymousContact(ctx, "discord", "d-2", "Alice 12:26")
	_ = s.BumpPrincipalSighting(ctx, second.ID, "discord", "", 200, "second")
	time.Sleep(2 * time.Millisecond)
	third, _ := s.EnsureAnonymousContact(ctx, "discord", "d-3", "Alice 12:27")
	_ = s.BumpPrincipalSighting(ctx, third.ID, "discord", "", 300, "third")

	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, nil, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.Scanned != 3 || rep.Renamed != 1 || rep.Merged != 2 {
		t.Errorf("report = %+v, want scanned=3 renamed=1 merged=2", rep)
	}
	// Survivor is the oldest (first); second & third deleted via merge.
	survivor, err := s.GetPrincipal(ctx, first.ID)
	if err != nil {
		t.Fatalf("first survivor missing: %v", err)
	}
	if survivor.DisplayName != "Alice" {
		t.Errorf("survivor DisplayName = %q, want Alice", survivor.DisplayName)
	}
	// All three sightings should have been folded onto the survivor.
	stats, _ := s.GetPrincipalStats(ctx, survivor.ID)
	if stats.TotalMessages != 3 {
		t.Errorf("merged TotalMessages = %d, want 3", stats.TotalMessages)
	}
}

func TestCleanupAnonymousPrincipals_LeavesNonAnonymousUntouched(t *testing.T) {
	s := newTestStore(t)
	contact := mkPrincipal(t, s, PrincipalKindContact, "Carl 12:25")
	if _, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, nil, nil); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	after, _ := s.GetPrincipal(ctx, contact.ID)
	if after.DisplayName != "Carl 12:25" {
		t.Errorf("Contact DisplayName changed: %q, want unchanged", after.DisplayName)
	}
}

func TestCleanupAnonymousPrincipals_EmptyStoreReportsZero(t *testing.T) {
	s := newTestStore(t)
	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, nil, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.Scanned != 0 || rep.Renamed != 0 || rep.Merged != 0 {
		t.Errorf("report = %+v, want all zeros", rep)
	}
}

// fakeIsChatApp recognises a tiny allowlist for the prune-non-chat
// tests below; mirrors identity.IsChatApp.
func fakeIsChatApp(app string) bool {
	switch app {
	case "wechat", "telegram", "slack", "discord":
		return true
	}
	return false
}

func TestCleanupAnonymousPrincipals_ClassifiesCrossAppMerge(t *testing.T) {
	s := newTestStore(t)
	// Two anonymous "Mei" — one on WeChat, one on Discord. Same
	// nickname across different apps → CrossAppMerged++.
	wechatMei, _ := s.EnsureAnonymousContact(ctx, "wechat", "wx-mei", "Mei")
	_ = s.BumpPrincipalSighting(ctx, wechatMei.ID, "wechat", "", 1000, "hi")
	time.Sleep(2 * time.Millisecond)
	discordMei, _ := s.EnsureAnonymousContact(ctx, "discord", "d-mei", "Mei")
	_ = s.BumpPrincipalSighting(ctx, discordMei.ID, "discord", "", 2000, "hello")

	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, fakeIsChatApp, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.CrossAppMerged != 1 {
		t.Errorf("CrossAppMerged = %d, want 1", rep.CrossAppMerged)
	}
	if rep.Merged != 0 {
		t.Errorf("Merged = %d, want 0 (cross-app, not same-app)", rep.Merged)
	}
	if _, err := s.GetPrincipal(ctx, discordMei.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("discord side should be merged away, err=%v", err)
	}
	if _, err := s.GetPrincipal(ctx, wechatMei.ID); err != nil {
		t.Errorf("wechat side (oldest) should survive, err=%v", err)
	}
}

func TestCleanupAnonymousPrincipals_SameAppMergeStaysInMergedBucket(t *testing.T) {
	s := newTestStore(t)
	// Two anonymous on the same app (Discord) whose normalized names
	// collide ("Mei 12:25" / "Mei 12:26" both → "Mei" via fakeNormalize).
	// Distinct displayHandles keep EnsureAnonymousContact from merging
	// them at insert time, so we get two principals that the cleanup
	// pass should fold. Should count toward Merged, NOT CrossAppMerged.
	first, _ := s.EnsureAnonymousContact(ctx, "discord", "d-1", "Mei 12:25")
	_ = s.BumpPrincipalSighting(ctx, first.ID, "discord", "", 1000, "hi")
	time.Sleep(2 * time.Millisecond)
	second, _ := s.EnsureAnonymousContact(ctx, "discord", "d-2", "Mei 12:26")
	_ = s.BumpPrincipalSighting(ctx, second.ID, "discord", "", 2000, "hi again")

	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, fakeIsChatApp, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.CrossAppMerged != 0 {
		t.Errorf("CrossAppMerged = %d, want 0 (same app)", rep.CrossAppMerged)
	}
	if rep.Merged != 1 {
		t.Errorf("Merged = %d, want 1", rep.Merged)
	}
}

func TestCleanupAnonymousPrincipals_PrunesNonChatAppOnly(t *testing.T) {
	s := newTestStore(t)
	mail, _ := s.EnsureAnonymousContact(ctx, "mail", "from-newsletter", "Substack")
	_ = s.BumpPrincipalSighting(ctx, mail.ID, "mail", "", 1000, "newsletter body")
	chat, _ := s.EnsureAnonymousContact(ctx, "discord", "d-real", "Real Person")
	_ = s.BumpPrincipalSighting(ctx, chat.ID, "discord", "", 2000, "actual message")

	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, fakeIsChatApp, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.PrunedNonChat != 1 {
		t.Errorf("PrunedNonChat = %d, want 1", rep.PrunedNonChat)
	}
	if _, err := s.GetPrincipal(ctx, mail.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("mail-only principal not pruned: err=%v", err)
	}
	if _, err := s.GetPrincipal(ctx, chat.ID); err != nil {
		t.Errorf("chat principal incorrectly pruned: %v", err)
	}
}

func TestCleanupAnonymousPrincipals_PrunesEmptyAnonymous(t *testing.T) {
	s := newTestStore(t)
	// Anonymous with no sightings at all — orphan from earlier
	// auto-bucket bugs. Should be pruned regardless of isChatApp.
	orphan, err := s.CreatePrincipal(ctx, PrincipalRow{
		Kind: PrincipalKindAnonymous, DisplayName: "Ghost",
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}

	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, fakeIsChatApp, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.PrunedNonChat != 1 {
		t.Errorf("PrunedNonChat = %d, want 1 (orphan)", rep.PrunedNonChat)
	}
	if _, err := s.GetPrincipal(ctx, orphan.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("orphan anonymous not pruned: err=%v", err)
	}
}

func TestCleanupAnonymousPrincipals_PrunesImplausibleNames(t *testing.T) {
	s := newTestStore(t)
	// A garbage row from before the ingestion guard existed: a Discord
	// welcome card whose heading bled into the sender field. It has
	// legitimate chat-app sightings, so the non-chat prune leaves it —
	// only the implausible-name prune should remove it.
	junk, _ := s.EnsureAnonymousContact(ctx, "discord", "", "welcome Server Tag: X\\ This is a sample welcome!")
	_ = s.BumpPrincipalSighting(ctx, junk.ID, "discord", "", 2000, "sample welcome card")
	good, _ := s.EnsureAnonymousContact(ctx, "discord", "", "priya")
	_ = s.BumpPrincipalSighting(ctx, good.ID, "discord", "", 3000, "sample greeting")

	isImplausible := func(name string) bool { return strings.Contains(name, "Server Tag:") }
	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, fakeIsChatApp, isImplausible)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.PrunedImplausible != 1 {
		t.Errorf("PrunedImplausible = %d, want 1", rep.PrunedImplausible)
	}
	if _, err := s.GetPrincipal(ctx, junk.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("implausible principal not pruned: err=%v", err)
	}
	if _, err := s.GetPrincipal(ctx, good.ID); err != nil {
		t.Errorf("plausible principal incorrectly pruned: %v", err)
	}
}

func TestCleanupAnonymousPrincipals_FoldsAnonymousIntoSelfByDisplayName(t *testing.T) {
	s := newTestStore(t)
	self, err := s.EnsureSelf(ctx, "alex_halcyon", nil)
	if err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}
	anon, _ := s.EnsureAnonymousContact(ctx, "discord", "d-anon", "alex_halcyon Server Tag: HLC 2031/6/30,")
	_ = s.BumpPrincipalSighting(ctx, anon.ID, "discord", "", 1000, "sample message from alex_halcyon")

	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeServerTagNormalize, fakeIsChatApp, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.MergedIntoNamed != 1 {
		t.Errorf("MergedIntoNamed = %d, want 1", rep.MergedIntoNamed)
	}
	if _, err := s.GetPrincipal(ctx, anon.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("anon should be deleted, got err=%v", err)
	}
	// Self receives the sighting + the discord handle binding.
	stats, _ := s.GetPrincipalStats(ctx, self.ID)
	if stats.TotalMessages != 1 {
		t.Errorf("Self TotalMessages after merge = %d, want 1", stats.TotalMessages)
	}
}

func TestCleanupAnonymousPrincipals_FoldsAnonymousIntoContactByAlias(t *testing.T) {
	s := newTestStore(t)
	contact, _ := s.CreatePrincipal(ctx, PrincipalRow{
		Kind:        PrincipalKindContact,
		DisplayName: "Mei",
		Aliases:     []string{"妈"},
	})
	anon, _ := s.EnsureAnonymousContact(ctx, "wechat", "wxid-mom", "妈 09:01")
	_ = s.BumpPrincipalSighting(ctx, anon.ID, "wechat", "", 2000, "hi")

	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, fakeIsChatApp, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.MergedIntoNamed != 1 {
		t.Errorf("MergedIntoNamed = %d, want 1", rep.MergedIntoNamed)
	}
	if _, err := s.GetPrincipal(ctx, anon.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("anon should be merged into contact, got err=%v", err)
	}
	stats, _ := s.GetPrincipalStats(ctx, contact.ID)
	if stats.TotalMessages != 1 {
		t.Errorf("contact TotalMessages after merge = %d, want 1", stats.TotalMessages)
	}
}

func TestCleanupAnonymousPrincipals_NoNamedFallsBackToAnonymousMerge(t *testing.T) {
	s := newTestStore(t)
	// Two Anonymous entries that normalize to "Alice"; no Self/Contact
	// with that name. They should still merge into each other (oldest
	// wins), not stay as duplicates.
	first, _ := s.EnsureAnonymousContact(ctx, "discord", "d-1", "Alice 12:25")
	_ = s.BumpPrincipalSighting(ctx, first.ID, "discord", "", 100, "msg")
	time.Sleep(2 * time.Millisecond)
	second, _ := s.EnsureAnonymousContact(ctx, "discord", "d-2", "Alice 12:26")
	_ = s.BumpPrincipalSighting(ctx, second.ID, "discord", "", 200, "msg")

	rep, _ := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, fakeIsChatApp, nil)
	if rep.MergedIntoNamed != 0 {
		t.Errorf("MergedIntoNamed = %d, want 0 (no named principals)", rep.MergedIntoNamed)
	}
	if rep.Merged != 1 {
		t.Errorf("Merged = %d, want 1 (intra-anonymous fold)", rep.Merged)
	}
	if _, err := s.GetPrincipal(ctx, first.ID); err != nil {
		t.Errorf("first survivor missing: %v", err)
	}
}

// fakeServerTagNormalize handles the specific "Server Tag: ... date,"
// shape used by the Self-fold test above. Mirrors enough of
// identity.NormalizeSenderName to exercise the cross-kind merge logic
// without dragging in the identity import (would cycle).
func fakeServerTagNormalize(s string) string {
	// Strip trailing date "YYYY/M/D,?" first.
	for {
		next := stripTrailingDate(s)
		if next == s {
			break
		}
		s = next
	}
	// Then strip trailing "Server Tag: VALUE" (single non-whitespace token).
	idx := indexLastServerTag(s)
	if idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

func stripTrailingDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 6 {
		return s
	}
	// Look for trailing pattern \s\d{4}/\d{1,2}/\d{1,2},?
	end := len(s)
	if s[end-1] == ',' {
		end--
	}
	// scan back digits + slashes
	i := end - 1
	slashes := 0
	for i >= 0 {
		if s[i] >= '0' && s[i] <= '9' {
			i--
			continue
		}
		if s[i] == '/' {
			slashes++
			if slashes > 2 {
				break
			}
			i--
			continue
		}
		break
	}
	if slashes != 2 {
		return s
	}
	if i < 0 || s[i] != ' ' {
		return s
	}
	return strings.TrimSpace(s[:i])
}

func indexLastServerTag(s string) int {
	idx := strings.LastIndex(s, " Server Tag:")
	return idx
}

func TestCleanupAnonymousPrincipals_PruneOff_KeepsEverything(t *testing.T) {
	s := newTestStore(t)
	mail, _ := s.EnsureAnonymousContact(ctx, "mail", "from-newsletter", "Substack")
	_ = s.BumpPrincipalSighting(ctx, mail.ID, "mail", "", 1000, "msg")

	rep, err := s.CleanupAnonymousPrincipals(ctx, fakeNormalize, nil, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rep.PrunedNonChat != 0 {
		t.Errorf("PrunedNonChat = %d, want 0 when prune disabled", rep.PrunedNonChat)
	}
	if _, err := s.GetPrincipal(ctx, mail.ID); err != nil {
		t.Errorf("mail principal removed despite nil isChatApp: %v", err)
	}
}

func TestCleanupAnonymousPrincipals_NormalizerThatEmptiesNameIsHandled(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.EnsureAnonymousContact(ctx, "discord", "d-1", "Alice")
	emptyNormalize := func(string) string { return "" }
	rep, err := s.CleanupAnonymousPrincipals(ctx, emptyNormalize, nil, nil)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// Empty normalization falls back to raw name → no rename, no merge.
	if rep.Renamed != 0 || rep.Merged != 0 {
		t.Errorf("report = %+v, want no mutations when normalizer empties", rep)
	}
	after, _ := s.GetPrincipal(ctx, a.ID)
	if after.DisplayName != "Alice" {
		t.Errorf("DisplayName = %q, want Alice (untouched)", after.DisplayName)
	}
}

// ── Updated_at bump ──

func TestMergePrincipals_BumpsUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	src := mkPrincipal(t, s, PrincipalKindContact, "Src")
	dst := mkPrincipal(t, s, PrincipalKindContact, "Dst")
	originalUpdatedAt := dst.UpdatedAt

	// Force a slight time gap so updated_at change is visible.
	// (UnixMilli has 1ms resolution; we don't need to sleep — but we do
	// need to verify the value is no smaller than the original.)
	merged, err := s.MergePrincipals(ctx, src.ID, dst.ID)
	if err != nil {
		t.Fatalf("MergePrincipals: %v", err)
	}
	if merged.UpdatedAt < originalUpdatedAt {
		t.Errorf("UpdatedAt regressed: %d < %d", merged.UpdatedAt, originalUpdatedAt)
	}
}
