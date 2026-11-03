package identity

import (
	"context"
	"errors"
	"testing"

	"wails-gui/storage"
)

// fakeStore is a deterministic in-memory Storage stand-in. Each Resolve*
// method returns whatever the test placed in the corresponding map; missing
// keys map to (nil, nil) to mirror real "no match" semantics.
type fakeStore struct {
	byAppHandle       map[string]*storage.PrincipalRow // key = app+"|"+handleID
	byDisplayHandle   map[string]*storage.PrincipalRow // key = app+"|"+displayHandle (lowercased)
	byAlias           map[string]*storage.PrincipalRow // key = name (lowercased)
	anonymous         []ensureAnonCall
	errResolveHandle  error
	errResolveDisplay error
	errResolveAlias   error
	errEnsureAnon     error
}

type ensureAnonCall struct {
	app, handleID, displayHandle string
}

func (f *fakeStore) ResolveByAppHandle(_ context.Context, app, handleID string) (*storage.PrincipalRow, error) {
	if f.errResolveHandle != nil {
		return nil, f.errResolveHandle
	}
	return f.byAppHandle[app+"|"+handleID], nil
}

func (f *fakeStore) ResolveByDisplayHandle(_ context.Context, app, displayHandle string) (*storage.PrincipalRow, error) {
	if f.errResolveDisplay != nil {
		return nil, f.errResolveDisplay
	}
	return f.byDisplayHandle[app+"|"+lower(displayHandle)], nil
}

func (f *fakeStore) ResolveByAlias(_ context.Context, name string) (*storage.PrincipalRow, error) {
	if f.errResolveAlias != nil {
		return nil, f.errResolveAlias
	}
	return f.byAlias[lower(name)], nil
}

func (f *fakeStore) EnsureAnonymousContact(_ context.Context, app, handleID, displayHandle string) (storage.PrincipalRow, error) {
	if f.errEnsureAnon != nil {
		return storage.PrincipalRow{}, f.errEnsureAnon
	}
	f.anonymous = append(f.anonymous, ensureAnonCall{app, handleID, displayHandle})
	return storage.PrincipalRow{
		ID:          "anon-" + handleID + displayHandle,
		Kind:        storage.PrincipalKindAnonymous,
		DisplayName: firstNonEmpty(displayHandle, handleID),
		Confidence:  0.3,
	}, nil
}

func lower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func selfRow() *storage.PrincipalRow {
	return &storage.PrincipalRow{ID: "p-self", Kind: storage.PrincipalKindSelf, DisplayName: "Yetone", Confidence: 1.0}
}

func contactRow(id, name string) *storage.PrincipalRow {
	return &storage.PrincipalRow{ID: id, Kind: storage.PrincipalKindContact, DisplayName: name, Relationship: "friend", Confidence: 1.0}
}

// ── Lookup ordering ──

func TestResolveSender_HandleIDWins(t *testing.T) {
	store := &fakeStore{
		byAppHandle:     map[string]*storage.PrincipalRow{"telegram|tg-1": contactRow("p-handle", "Wang")},
		byDisplayHandle: map[string]*storage.PrincipalRow{"telegram|@wang": contactRow("p-display", "Different")},
		byAlias:         map[string]*storage.PrincipalRow{"wang": contactRow("p-alias", "Different2")},
	}
	r := NewResolver(store, false)
	got, err := r.ResolveSender(context.Background(), "telegram", "tg-1", "@wang")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.PrincipalID != "p-handle" || got.Method != MethodHandleID {
		t.Errorf("got %+v, want p-handle via handle_id", got)
	}
}

func TestResolveSender_DisplayHandleAfterHandleIDMiss(t *testing.T) {
	store := &fakeStore{
		byDisplayHandle: map[string]*storage.PrincipalRow{"wechat|@yetone": selfRow()},
	}
	r := NewResolver(store, false)
	got, err := r.ResolveSender(context.Background(), "wechat", "", "@yetone")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectSelf {
		t.Errorf("Kind = %s, want self", got.Kind)
	}
	if got.Method != MethodDisplayHandle {
		t.Errorf("Method = %s, want display_handle", got.Method)
	}
}

func TestResolveSender_AliasFallback(t *testing.T) {
	store := &fakeStore{
		byAlias: map[string]*storage.PrincipalRow{"yetone": selfRow()},
	}
	r := NewResolver(store, false)
	got, err := r.ResolveSender(context.Background(), "wechat", "wxid_unknown", "Yetone")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectSelf {
		t.Errorf("Kind = %s, want self", got.Kind)
	}
	if got.Method != MethodAlias {
		t.Errorf("Method = %s, want alias", got.Method)
	}
}

// ── Anonymous bucket ──

func TestResolveSender_AutoBucketWhenEverythingMisses(t *testing.T) {
	store := &fakeStore{}
	r := NewResolver(store, true)
	got, err := r.ResolveSender(context.Background(), "slack", "U0001", "Stranger")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectAnonymous {
		t.Errorf("Kind = %s, want anonymous", got.Kind)
	}
	if got.Method != MethodAutoAnonymous {
		t.Errorf("Method = %s, want auto_anonymous", got.Method)
	}
	if len(store.anonymous) != 1 {
		t.Errorf("EnsureAnonymousContact calls = %d, want 1", len(store.anonymous))
	}
	if store.anonymous[0].app != "slack" || store.anonymous[0].handleID != "U0001" || store.anonymous[0].displayHandle != "Stranger" {
		t.Errorf("EnsureAnonymousContact call args = %+v", store.anonymous[0])
	}
}

func TestResolveSender_ImplausibleDisplayNameDoesNotAutoBucket(t *testing.T) {
	// A Discord welcome-card heading that bled into the sender field, with
	// no stable handleID. It must NOT become an anonymous principal.
	store := &fakeStore{}
	r := NewResolver(store, true)
	got, err := r.ResolveSender(context.Background(), "discord", "", "it's here Server Tag: A\\ I'm new here, say hi!")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectUnknown {
		t.Errorf("Kind = %s, want unknown for implausible name", got.Kind)
	}
	if len(store.anonymous) != 0 {
		t.Errorf("EnsureAnonymousContact called for junk name: %+v", store.anonymous)
	}
}

func TestResolveSender_ImplausibleNameStillBucketsWithStableHandle(t *testing.T) {
	// A stable handleID is trustworthy on its own, so a noisy display name
	// must not block bucketing when we have a real handle to key on.
	store := &fakeStore{}
	r := NewResolver(store, true)
	got, err := r.ResolveSender(context.Background(), "slack", "U0001", "Hi everyone, I'm new here!")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectAnonymous || got.Method != MethodAutoAnonymous {
		t.Errorf("got %+v, want anonymous via handleID despite noisy name", got)
	}
	if len(store.anonymous) != 1 {
		t.Errorf("EnsureAnonymousContact calls = %d, want 1", len(store.anonymous))
	}
}

func TestResolveSender_NoAutoBucketReturnsUnknown(t *testing.T) {
	store := &fakeStore{}
	r := NewResolver(store, false)
	got, err := r.ResolveSender(context.Background(), "slack", "U0001", "Stranger")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectUnknown {
		t.Errorf("Kind = %s, want unknown", got.Kind)
	}
	if got.Method != MethodUnknown {
		t.Errorf("Method = %s, want unknown", got.Method)
	}
	if len(store.anonymous) != 0 {
		t.Errorf("EnsureAnonymousContact unexpectedly called: %d", len(store.anonymous))
	}
}

func TestResolveSender_NoBucketWithoutApp(t *testing.T) {
	store := &fakeStore{}
	r := NewResolver(store, true)
	got, err := r.ResolveSender(context.Background(), "", "", "Stranger")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectUnknown {
		t.Errorf("Kind = %s, want unknown when no app provided", got.Kind)
	}
	if len(store.anonymous) != 0 {
		t.Errorf("auto-bucket should not fire without an app id")
	}
}

func TestResolveSender_NoBucketWithoutAnyIdentifier(t *testing.T) {
	store := &fakeStore{}
	r := NewResolver(store, true)
	got, err := r.ResolveSender(context.Background(), "telegram", "", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectUnknown {
		t.Errorf("Kind = %s, want unknown when no identifier", got.Kind)
	}
	if len(store.anonymous) != 0 {
		t.Errorf("auto-bucket should not fire without any identifier")
	}
}

// ── Edge cases ──

func TestResolveSender_TrimsAndLowercasesApp(t *testing.T) {
	store := &fakeStore{
		byAppHandle: map[string]*storage.PrincipalRow{"telegram|tg-1": contactRow("p", "P")},
	}
	r := NewResolver(store, false)
	got, err := r.ResolveSender(context.Background(), "  TELEGRAM ", "tg-1", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.PrincipalID != "p" {
		t.Errorf("app normalization failed: %+v", got)
	}
}

func TestResolveSender_HandlesStorageError(t *testing.T) {
	bang := errors.New("boom")
	store := &fakeStore{errResolveHandle: bang}
	r := NewResolver(store, false)
	_, err := r.ResolveSender(context.Background(), "x", "h", "")
	if !errors.Is(err, bang) {
		t.Errorf("err = %v, want bang", err)
	}
}

func TestResolveSender_AnonymousEnsureErrorPropagates(t *testing.T) {
	bang := errors.New("disk full")
	store := &fakeStore{errEnsureAnon: bang}
	r := NewResolver(store, true)
	// Use a chat app — non-chat apps no longer auto-bucket so the
	// EnsureAnonymous error path is never hit through them.
	_, err := r.ResolveSender(context.Background(), "wechat", "h", "")
	if !errors.Is(err, bang) {
		t.Errorf("err = %v, want disk full", err)
	}
}

func TestResolveSender_NonChatAppDoesNotAutoBucket(t *testing.T) {
	store := &fakeStore{}
	r := NewResolver(store, true)
	// Mail.app and similar email-shaped sources should never create an
	// Anonymous principal even with autoBucket on — they pollute the
	// Who you know page with non-human "senders" (newsletters, services,
	// transactional emails).
	got, err := r.ResolveSender(context.Background(), "mail", "", "Sentry")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Kind != SubjectUnknown {
		t.Errorf("Kind = %s, want unknown for non-chat app", got.Kind)
	}
	if len(store.anonymous) != 0 {
		t.Errorf("auto-bucket fired for non-chat app: %+v", store.anonymous)
	}
}

// ── ContractWithRealStore: round-trips via real storage to catch
// integration drift between the fake and the real implementation. ──

func TestResolveSender_RealStoreSelfDisplayHandle(t *testing.T) {
	s := newRealStore(t)
	self, err := s.EnsureSelf(context.Background(), "Yetone", []string{"yt"})
	if err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}
	if _, err := s.BindAppHandle(context.Background(), self.ID, "telegram", "tg-self", "@yetone"); err != nil {
		t.Fatalf("BindAppHandle: %v", err)
	}

	r := NewResolver(s, false)
	got, err := r.ResolveSender(context.Background(), "telegram", "tg-self", "")
	if err != nil {
		t.Fatalf("ResolveSender: %v", err)
	}
	if got.Kind != SubjectSelf || got.PrincipalID != self.ID {
		t.Errorf("got %+v, want self %s", got, self.ID)
	}
	if got.Method != MethodHandleID {
		t.Errorf("Method = %s, want handle_id", got.Method)
	}
}

func TestResolveSender_RealStoreAutoBucketDedupesAcrossCalls(t *testing.T) {
	s := newRealStore(t)
	r := NewResolver(s, true)
	a, err := r.ResolveSender(context.Background(), "wechat", "wxid_x", "@strange")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := r.ResolveSender(context.Background(), "wechat", "wxid_x", "@strange")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a.PrincipalID != b.PrincipalID {
		t.Errorf("auto-bucket did not dedup: %s vs %s", a.PrincipalID, b.PrincipalID)
	}
	if a.Kind != SubjectAnonymous {
		t.Errorf("Kind = %s, want anonymous", a.Kind)
	}
}
