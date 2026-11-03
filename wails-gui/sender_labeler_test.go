package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/storage"
)

func newLabelerTestStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.New(filepath.Join(dir, "labeler-test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSenderLabeler_NilStoreReturnsUnknown(t *testing.T) {
	lbl := newSenderLabeler(nil)
	got := lbl.Label(context.Background(), "wechat", "张三", "", "", 0)
	if !strings.HasPrefix(got, "[Unknown:") {
		t.Errorf("nil store should produce [Unknown:...], got %q", got)
	}
	stats := lbl.Stats()
	if stats.UnknownMsgCount != 1 || stats.SelfMsgCount != 0 {
		t.Errorf("stats = %+v, want unknown=1", stats)
	}
}

func TestSenderLabeler_LabelsSelf(t *testing.T) {
	s := newLabelerTestStore(t)
	self, err := s.EnsureSelf(context.Background(), "Yetone", nil)
	if err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}
	if _, err := s.BindAppHandle(context.Background(), self.ID, "wechat", "wxid_self", "yetone"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}

	lbl := newSenderLabeler(s)
	got := lbl.Label(context.Background(), "wechat", "yetone", "hello", "", 0)
	if got != "[YOU]" {
		t.Errorf("got %q, want [YOU]", got)
	}
	if lbl.Stats().SelfMsgCount != 1 {
		t.Errorf("SelfMsgCount = %d, want 1", lbl.Stats().SelfMsgCount)
	}
}

func TestSenderLabeler_LabelsContactWithRelationship(t *testing.T) {
	s := newLabelerTestStore(t)
	contact, err := s.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind:         storage.PrincipalKindContact,
		DisplayName:  "Zhang San",
		Relationship: "friend",
		Aliases:      []string{"张三"},
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := s.BindAppHandle(context.Background(), contact.ID, "wechat", "", "张三"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}

	lbl := newSenderLabeler(s)
	got := lbl.Label(context.Background(), "wechat", "张三", "今天天气怎么样", "", 0)
	if got != "[Contact: Zhang San, friend]" {
		t.Errorf("got %q, want [Contact: Zhang San, friend]", got)
	}
	if lbl.Stats().ContactMsgCount != 1 {
		t.Errorf("ContactMsgCount = %d, want 1", lbl.Stats().ContactMsgCount)
	}
}

func TestSenderLabeler_AutoBucketsUnknownSenderAsAnonymous(t *testing.T) {
	s := newLabelerTestStore(t)
	lbl := newSenderLabeler(s)

	got := lbl.Label(context.Background(), "telegram", "@stranger", "first message", "", 0)
	if !strings.HasPrefix(got, "[Anonymous:") {
		t.Errorf("got %q, want [Anonymous: ...]", got)
	}
	if lbl.Stats().AnonymousMsgCount != 1 {
		t.Errorf("AnonymousMsgCount = %d, want 1", lbl.Stats().AnonymousMsgCount)
	}

	// Repeat invocation should hit the cache and not double-count
	// (each invocation always bumps stats — that's per-message, not per-cache).
	got2 := lbl.Label(context.Background(), "telegram", "@stranger", "second message", "", 0)
	if got2 != got {
		t.Errorf("cached label changed: %q -> %q", got, got2)
	}
	if lbl.Stats().AnonymousMsgCount != 2 {
		t.Errorf("AnonymousMsgCount = %d after second call, want 2", lbl.Stats().AnonymousMsgCount)
	}
}

func TestSenderLabeler_CachesAcrossCalls(t *testing.T) {
	s := newLabelerTestStore(t)
	// Bind a contact so the resolver returns a deterministic match.
	contact, err := s.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "C",
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := s.BindAppHandle(context.Background(), contact.ID, "slack", "", "@c"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}

	lbl := newSenderLabeler(s)
	for range 5 {
		lbl.Label(context.Background(), "slack", "@c", "hi", "", 0)
	}
	if got := lbl.Stats().ContactMsgCount; got != 5 {
		t.Errorf("ContactMsgCount after 5 identical sender calls = %d, want 5", got)
	}
	// Different sender — cache miss.
	lbl.Label(context.Background(), "slack", "@d", "hi", "", 0)
	if got := lbl.Stats().UnknownMsgCount + lbl.Stats().AnonymousMsgCount; got != 1 {
		t.Errorf("non-contact sender should produce 1 unknown/anon stat, got %d", got)
	}
}

func TestRecordWeChatSenderSightings(t *testing.T) {
	s := newLabelerTestStore(t)
	lbl := newSenderLabeler(s)

	msgs := []wechatVisionMessage{
		{Sender: "张三", Text: "今天开会吗"},
		{Sender: "me", Text: "开"}, // self marker → skipped
		{Sender: "我", Text: "几点"}, // self marker → skipped
		{Sender: "李四", Text: "下午三点"},
		{Sender: "", Text: "no sender"},                 // empty → skipped
		{Sender: "I'm new here, say hi!", Text: "junk"}, // implausible → resolver guard drops it
	}
	recordWeChatSenderSightings(lbl, msgs, "sess-1", 1000)

	anons, err := s.ListPrincipalsByKind(context.Background(), storage.PrincipalKindAnonymous)
	if err != nil {
		t.Fatalf("ListPrincipalsByKind: %v", err)
	}
	names := map[string]bool{}
	for _, p := range anons {
		names[p.DisplayName] = true
	}
	if !names["张三"] || !names["李四"] {
		t.Errorf("expected 张三 and 李四 as anonymous principals, got %v", names)
	}
	if len(anons) != 2 {
		t.Errorf("want exactly 2 anonymous principals (self markers + empty + junk skipped), got %d: %v", len(anons), names)
	}

	// The bucket must be bound to the "wechat" app so the Who you know page renders
	// it as "WeChat · <name>".
	var zhangsan storage.PrincipalRow
	for _, p := range anons {
		if p.DisplayName == "张三" {
			zhangsan = p
		}
	}
	handles, err := s.ListAppHandlesByPrincipal(context.Background(), zhangsan.ID)
	if err != nil {
		t.Fatalf("ListAppHandlesByPrincipal: %v", err)
	}
	if len(handles) != 1 || handles[0].App != "wechat" {
		t.Errorf("want one wechat app handle, got %+v", handles)
	}
}

func TestRecordWeChatSenderSightings_NilLabelerNoPanic(t *testing.T) {
	recordWeChatSenderSightings(nil, []wechatVisionMessage{{Sender: "x", Text: "y"}}, "s", 0)
}

func TestSenderLabeler_EmptySenderCountsAsUnknown(t *testing.T) {
	lbl := newSenderLabeler(nil)
	got := lbl.Label(context.Background(), "wechat", "", "", "", 0)
	if got != "—" {
		t.Errorf("got %q, want —", got)
	}
	if lbl.Stats().UnknownMsgCount != 1 {
		t.Errorf("UnknownMsgCount = %d, want 1", lbl.Stats().UnknownMsgCount)
	}
}

func TestSenderLabeler_AppCaseInsensitivityInCache(t *testing.T) {
	lbl := newSenderLabeler(nil)
	lbl.Label(context.Background(), "WeChat", "x", "", "", 0)
	lbl.Label(context.Background(), "wechat", "x", "", "", 0)
	// Both should hit the same cache key (app lowercased) so this should
	// produce a single cache entry — but stats bump per call so total
	// should be 2.
	if got := lbl.Stats().UnknownMsgCount; got != 2 {
		t.Errorf("UnknownMsgCount = %d, want 2", got)
	}
	if got := len(lbl.cache); got != 1 {
		t.Errorf("cache entries = %d, want 1 (app should be case-folded)", got)
	}
}

func TestSenderLabeler_RecordsSightingForKnownContact(t *testing.T) {
	s := newLabelerTestStore(t)
	contact, err := s.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "Zhang San",
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := s.BindAppHandle(context.Background(), contact.ID, "wechat", "", "张三"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}

	lbl := newSenderLabeler(s)
	lbl.Label(context.Background(), "wechat", "张三", "今天有空吗?", "", 0)
	lbl.Label(context.Background(), "wechat", "张三", "吃饭去", "", 0)

	stats, err := s.GetPrincipalStats(context.Background(), contact.ID)
	if err != nil {
		t.Fatalf("GetPrincipalStats: %v", err)
	}
	if stats.TotalMessages != 2 {
		t.Errorf("TotalMessages = %d, want 2", stats.TotalMessages)
	}
	if len(stats.ByApp) != 1 || stats.ByApp[0].App != "wechat" {
		t.Errorf("ByApp = %+v", stats.ByApp)
	}
	if len(stats.RecentMessages) != 2 {
		t.Errorf("RecentMessages len = %d, want 2", len(stats.RecentMessages))
	}
}

func TestSenderLabeler_AutoBucketsAlsoRecordSightings(t *testing.T) {
	s := newLabelerTestStore(t)
	lbl := newSenderLabeler(s)

	// First call against a fresh sender creates an Anonymous bucket via the
	// resolver and immediately records its first sighting.
	lbl.Label(context.Background(), "telegram", "@new_person", "hi yansu", "", 0)

	all, err := s.ListPrincipalsByKind(context.Background(), storage.PrincipalKindAnonymous)
	if err != nil {
		t.Fatalf("ListPrincipalsByKind: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 anonymous principal, got %d", len(all))
	}
	stats, err := s.GetPrincipalStats(context.Background(), all[0].ID)
	if err != nil {
		t.Fatalf("GetPrincipalStats: %v", err)
	}
	if stats.TotalMessages != 1 {
		t.Errorf("TotalMessages = %d, want 1", stats.TotalMessages)
	}
	if len(stats.RecentMessages) != 1 || stats.RecentMessages[0].Preview != "hi yansu" {
		t.Errorf("recent messages wrong: %+v", stats.RecentMessages)
	}
}

func TestSenderLabeler_UnknownSenderSkipsSightingWrite(t *testing.T) {
	// nil store → resolver is nil too → no Anonymous bucket auto-arrival.
	// Sender resolves as Unknown, so we should NOT write any sighting
	// (there is no principal to attribute it to).
	lbl := newSenderLabeler(nil)
	got := lbl.Label(context.Background(), "telegram", "stranger", "hi", "", 0)
	if got != "[Unknown: stranger]" {
		t.Errorf("got %q, want [Unknown: stranger]", got)
	}
	// Nothing to assert against storage (it's nil); the test verifies the
	// nil-store branch does not panic.
}

func TestSenderLabeler_LongPreviewTruncated(t *testing.T) {
	s := newLabelerTestStore(t)
	contact, _ := s.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "T",
	})
	_, _ = s.BindAppHandle(context.Background(), contact.ID, "slack", "", "@t")

	lbl := newSenderLabeler(s)
	long := strings.Repeat("一二三四五", 50) // 250 runes, well over senderPreviewMaxRunes
	lbl.Label(context.Background(), "slack", "@t", long, "", 0)

	stats, _ := s.GetPrincipalStats(context.Background(), contact.ID)
	if len(stats.RecentMessages) != 1 {
		t.Fatalf("expected 1 recent message")
	}
	preview := stats.RecentMessages[0].Preview
	if len([]rune(preview)) > senderPreviewMaxRunes+1 { // +1 for the appended ellipsis
		t.Errorf("preview not truncated: %d runes", len([]rune(preview)))
	}
	if !strings.HasSuffix(preview, "…") {
		t.Errorf("truncated preview missing ellipsis suffix: %q", preview)
	}
}

func TestSenderLabeler_MonotonicTimestampsAllowAllPreviews(t *testing.T) {
	s := newLabelerTestStore(t)
	contact, _ := s.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "M",
	})
	_, _ = s.BindAppHandle(context.Background(), contact.ID, "wechat", "", "@m")

	lbl := newSenderLabeler(s)
	// Fire 5 calls back-to-back well within the same millisecond — the
	// labeler's tsCounter monotonicity must give each a distinct seen_at so
	// the rolling-window PRIMARY KEY (principal_id, seen_at) doesn't drop
	// previews on collision.
	for i := range 5 {
		_ = i
		lbl.Label(context.Background(), "wechat", "@m", "msg", "", 0)
	}

	stats, _ := s.GetPrincipalStats(context.Background(), contact.ID)
	if len(stats.RecentMessages) != 5 {
		t.Errorf("expected 5 distinct previews, got %d", len(stats.RecentMessages))
	}
}

func TestSenderLabeler_FormatSenderLabelFallbacks(t *testing.T) {
	s := newLabelerTestStore(t)
	// Contact with empty DisplayName — label should fall back to raw sender.
	contact, err := s.CreatePrincipal(context.Background(), storage.PrincipalRow{
		Kind: storage.PrincipalKindContact, DisplayName: "",
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := s.BindAppHandle(context.Background(), contact.ID, "discord", "d-1", "@nameless"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}
	lbl := newSenderLabeler(s)
	got := lbl.Label(context.Background(), "discord", "@nameless", "", "", 0)
	if got != "[Contact: @nameless]" {
		t.Errorf("got %q, want [Contact: @nameless]", got)
	}
}
