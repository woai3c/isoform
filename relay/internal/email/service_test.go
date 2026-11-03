package email

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/store"
)

const owner = "Owner@Example.com"

type fixture struct {
	t     *testing.T
	store *store.Store
	fake  *Fake
	svc   *Service

	mu    sync.Mutex
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	vault, err := NewVault(bytes.Repeat([]byte{7}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, store: st, fake: NewFake(), clock: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	f.svc = NewService(st, f.fake, vault, Config{Provider: "fake", DailyCap: 2, HourlyCap: DefaultHourlyCap, CodesPerHour: DefaultCodesPerHour, CodesPerDay: DefaultCodesPerDay})
	f.svc.Now = f.now
	return f
}

func (f *fixture) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}

func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(d)
}

func (f *fixture) hub() string {
	f.t.Helper()
	id := uuid.NewString()
	if err := f.store.AuthorizeHub(context.Background(), id, strings.Repeat("a", 64), f.now()); err != nil {
		f.t.Fatal(err)
	}
	return id
}

var codePattern = regexp.MustCompile(`[0-9]{6}`)

func (f *fixture) lastCode() string {
	f.t.Helper()
	sent := f.fake.Sent()
	if len(sent) == 0 {
		f.t.Fatal("no code was mailed")
	}
	return codePattern.FindString(sent[len(sent)-1].Subject)
}

func TestVerifyThenSendOnlyToTheVerifiedAddress(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hub := f.hub()

	if _, err := f.svc.Send(ctx, hub, "Isoform: check", "Downloads 1,240"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("send before verifying: %v", err)
	}
	status, err := f.svc.StartVerification(ctx, hub, owner, "en")
	if err != nil || status.State != "pending_verification" || status.CodeExpiresAt.IsZero() {
		t.Fatalf("start: %+v %v", status, err)
	}
	mailed := f.fake.Sent()
	if len(mailed) != 1 || mailed[0].To != "owner@example.com" || !strings.Contains(mailed[0].Text, "10 minutes") {
		t.Fatalf("code email: %+v", mailed)
	}
	if _, err := f.svc.Confirm(ctx, hub, "000000"); !errors.Is(err, ErrCodeWrong) && f.lastCode() != "000000" {
		t.Fatalf("wrong code: %v", err)
	}
	status, err = f.svc.Confirm(ctx, hub, f.lastCode())
	if err != nil || status.State != "active" {
		t.Fatalf("confirm: %+v %v", status, err)
	}

	status, err = f.svc.Send(ctx, hub, "Isoform: PostHog morning check", "Downloads 1,240 (+8%)\nD7 retention 31%")
	if err != nil || status.SentToday != 1 {
		t.Fatalf("send: %+v %v", status, err)
	}
	last := f.fake.Sent()[len(f.fake.Sent())-1]
	if last.To != "owner@example.com" || last.Subject != "Isoform: PostHog morning check" || !strings.Contains(last.Text, "\nD7 retention 31%") {
		t.Fatalf("sent: %+v", last)
	}

	// Another hub cannot send to this address: it has none of its own.
	if _, err := f.svc.Send(ctx, f.hub(), "Isoform: x", "y"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("another hub sent: %v", err)
	}
}

func TestCodesExpireAndWrongCodesSpendThem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hub := f.hub()
	if _, err := f.svc.Confirm(ctx, hub, "123456"); !errors.Is(err, ErrNoPending) {
		t.Fatalf("confirm with nothing pending: %v", err)
	}
	if _, err := f.svc.StartVerification(ctx, hub, owner, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	if subject := f.fake.Sent()[0].Subject; !strings.HasPrefix(subject, "Isoform 验证码") {
		t.Fatalf("zh-CN subject %q", subject)
	}
	right := f.lastCode()
	wrong := "111111"
	if wrong == right {
		wrong = "222222"
	}
	for i := 0; i < CodeAttempts-1; i++ {
		if _, err := f.svc.Confirm(ctx, hub, wrong); !errors.Is(err, ErrCodeWrong) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := f.svc.Confirm(ctx, hub, wrong); !errors.Is(err, ErrCodeSpent) {
		t.Fatalf("last wrong attempt: %v", err)
	}
	if _, err := f.svc.Confirm(ctx, hub, right); !errors.Is(err, ErrCodeSpent) {
		t.Fatalf("right code after the attempts ran out: %v", err)
	}

	if _, err := f.svc.StartVerification(ctx, hub, owner, "en"); err != nil {
		t.Fatal(err)
	}
	f.advance(CodeTTL + time.Second)
	if _, err := f.svc.Confirm(ctx, hub, f.lastCode()); !errors.Is(err, ErrCodeSpent) {
		t.Fatalf("expired code: %v", err)
	}
}

func TestLimitsAndRules(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hub := f.hub()
	if _, err := f.svc.StartVerification(ctx, hub, "not an address", "en"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("bad address: %v", err)
	}
	if _, err := f.svc.StartVerification(ctx, hub, "Owner <owner@example.com>", "en"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("named address: %v", err)
	}
	for i := 0; i < codesPerHub; i++ {
		if _, err := f.svc.StartVerification(ctx, hub, owner, "en"); err != nil {
			t.Fatalf("code %d: %v", i, err)
		}
	}
	var retry *RetryError
	if _, err := f.svc.StartVerification(ctx, hub, owner, "en"); !errors.As(err, &retry) || !errors.Is(err, ErrTooMany) {
		t.Fatalf("codes per hub: %v", err)
	}
	if _, err := f.svc.Confirm(ctx, hub, f.lastCode()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Send(ctx, hub, "Hello", "body"); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("subject without the prefix: %v", err)
	}
	if _, err := f.svc.Send(ctx, hub, "Isoform: x", "bad\x00body"); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("control character: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.svc.Send(ctx, hub, "Isoform: x", "y"); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := f.svc.Send(ctx, hub, "Isoform: x", "y"); !errors.Is(err, ErrQuota) {
		t.Fatalf("daily cap: %v", err)
	}
	f.advance(24 * time.Hour)
	f.fake.FailWith(ErrRejected)
	if _, err := f.svc.Send(ctx, hub, "Isoform: x", "y"); !errors.Is(err, ErrSendFailed) {
		t.Fatalf("refused send: %v", err)
	}
	if status, _ := f.svc.Status(ctx, hub); status.SentToday != 0 {
		t.Fatalf("a refused email must not count: %+v", status)
	}
	if err := f.svc.Unsubscribe(ctx, hub); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.svc.Status(ctx, hub); status.State != "none" {
		t.Fatalf("after unsubscribe: %+v", status)
	}
}

func TestLoadConfig(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if c, err := LoadConfig(get); err != nil || c.Enabled() {
		t.Fatalf("off by default: %+v %v", c, err)
	}
	env["ISOFORM_RELAY_EMAIL_PROVIDER"] = "smtp"
	if _, err := LoadConfig(get); err == nil {
		t.Fatal("smtp without a key must fail")
	}
	env["ISOFORM_RELAY_EMAIL_KEY"] = "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc="
	env["ISOFORM_RELAY_SMTP_HOST"] = "smtp.example.com"
	env["ISOFORM_RELAY_EMAIL_FROM"] = "Isoform <notify@example.com>"
	c, err := LoadConfig(get)
	if err != nil || !c.Enabled() || c.SMTP.Port != 587 || c.SMTP.Mode != "" || c.DailyCap != DefaultDailyCap {
		t.Fatalf("smtp config: %+v %v", c, err)
	}
	env["ISOFORM_RELAY_SMTP_MODE"] = "plain"
	if _, err := LoadConfig(get); err == nil {
		t.Fatal("plain SMTP to a remote host must fail")
	}
}

func TestSendsWithoutAnAddressSpendNoRelayBudget(t *testing.T) {
	f := newFixture(t)
	f.svc.Config.HourlyCap = 1
	ctx := context.Background()
	stranger := f.hub()
	for i := 0; i < 5; i++ {
		if _, err := f.svc.Send(ctx, stranger, "Isoform: x", "y"); !errors.Is(err, ErrNotActive) {
			t.Fatalf("send %d without an address: %v", i, err)
		}
	}
	owner := f.hub()
	if _, err := f.svc.StartVerification(ctx, owner, "owner@example.com", "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Confirm(ctx, owner, f.lastCode()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Send(ctx, owner, "Isoform: x", "y"); err != nil {
		t.Fatalf("the budget was spent by refused sends: %v", err)
	}
	if _, err := f.svc.Send(ctx, owner, "Isoform: x", "y"); !errors.Is(err, ErrBusy) {
		t.Fatalf("relay-wide cap: %v", err)
	}
	if status, _ := f.svc.Status(ctx, owner); status.SentToday != 1 {
		t.Fatalf("a send refused by the relay-wide cap must not count: %+v", status)
	}
}
