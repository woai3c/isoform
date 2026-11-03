package sms

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/store"
)

const (
	alice = "+14155550123"
	bob   = "+14155550199"
)

// fixture is a service over a real SQLite store with the fake provider and a
// clock the test moves.
type fixture struct {
	t     *testing.T
	dir   string
	store *store.Store
	fake  *Fake
	svc   *Service

	mu      sync.Mutex
	clock   time.Time
	replied []string
	changed []string
	msgSeq  int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	vault, err := NewVault(VaultKeys{Seal: bytes.Repeat([]byte{42}, KeySize)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, dir: dir, store: st, fake: NewFake([]byte("webhook-secret")), clock: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	cfg := Config{Provider: "fake", DailyCap: DefaultDailyCap, MonthlyCap: DefaultMonthlyCap, HourlyCap: DefaultHourlyCap, CodesPerHour: DefaultCodesPerHour, KeywordAnswersPerHour: DefaultKeywordAnswersPerHour, Countries: []string{"+1"}, HelpContact: "help@example.com"}
	f.svc = NewService(st, f.fake, vault, cfg)
	f.svc.Now = f.now
	f.svc.OnReply = func(hubID string) { f.mu.Lock(); f.replied = append(f.replied, hubID); f.mu.Unlock() }
	f.svc.OnState = func(hubID string) { f.mu.Lock(); f.changed = append(f.changed, hubID); f.mu.Unlock() }
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

// hub is a hub that turned text messages on, as the Mac does before it asks
// for a code.
func (f *fixture) hub() string {
	f.t.Helper()
	id := f.bareHub()
	if _, err := f.svc.Enable(context.Background(), id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// bareHub is a hub the relay knows that never turned text messages on.
func (f *fixture) bareHub() string {
	f.t.Helper()
	id := uuid.NewString()
	if err := f.store.AuthorizeHub(context.Background(), id, strings.Repeat("a", 64), f.now()); err != nil {
		f.t.Fatal(err)
	}
	return id
}

var codePattern = regexp.MustCompile(`[0-9]{6}`)

// lastCode is the code in the newest text sent to the number.
func (f *fixture) lastCode(number string) string {
	f.t.Helper()
	texts := f.fake.SentTo(number)
	if len(texts) == 0 {
		f.t.Fatal("no code was texted")
	}
	code := codePattern.FindString(texts[len(texts)-1].Body)
	if code == "" {
		f.t.Fatalf("no code in %q", texts[len(texts)-1].Body)
	}
	return code
}

// subscribe verifies the number for the hub.
func (f *fixture) subscribe(hubID, number string) {
	f.t.Helper()
	ctx := context.Background()
	if _, err := f.svc.StartVerification(ctx, hubID, number, "en"); err != nil {
		f.t.Fatalf("start: %v", err)
	}
	status, err := f.svc.Confirm(ctx, hubID, f.lastCode(number))
	if err != nil || status.State != store.SMSActive {
		f.t.Fatalf("confirm: %+v %v", status, err)
	}
}

// inbound posts a signed webhook for a text from the number.
func (f *fixture) inbound(from, body string) error {
	f.mu.Lock()
	f.msgSeq++
	id := fmt.Sprintf("in-%d", f.msgSeq)
	f.mu.Unlock()
	return f.inboundWithID(from, body, id)
}

func (f *fixture) inboundWithID(from, body, id string) error {
	form := url.Values{"From": {from}, "To": {"+18005550100"}, "Body": {body}, "MessageId": {id}}
	r, _ := http.NewRequest(http.MethodPost, "http://relay.test/v1/sms/inbound", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Fake-Signature", f.fake.Sign(form))
	return f.svc.HandleInbound(context.Background(), r)
}

func (f *fixture) state(hubID string) string {
	f.t.Helper()
	status, err := f.svc.Status(context.Background(), hubID)
	if err != nil {
		f.t.Fatal(err)
	}
	return status.State
}

// databaseBytes reads the database and its WAL, to prove what is not in them.
func (f *fixture) databaseBytes() []byte {
	f.t.Helper()
	var all []byte
	for _, name := range []string{"relay.sqlite", "relay.sqlite-wal"} {
		raw, err := os.ReadFile(filepath.Join(f.dir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			f.t.Fatal(err)
		}
		all = append(all, raw...)
	}
	return all
}

func TestVerificationFlowKeepsNoPlaintextNumber(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	if s := f.state(hubID); s != "none" {
		t.Fatalf("new hub state %q", s)
	}
	status, err := f.svc.StartVerification(ctx, hubID, "+1 (415) 555-0123", "en")
	if err != nil || status.State != store.SMSPending || !status.CodeExpiresAt.Equal(f.now().Add(CodeTTL)) {
		t.Fatalf("start: %+v %v", status, err)
	}
	texts := f.fake.SentTo(alice)
	if len(texts) != 1 || !strings.Contains(texts[0].Body, "Reply HELP for help, STOP to cancel") {
		t.Fatalf("code texts %+v", texts)
	}
	code := f.lastCode(alice)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, err = f.svc.Confirm(ctx, hubID, wrong); !errors.Is(err, ErrCodeWrong) {
		t.Fatalf("wrong code: %v", err)
	}
	if _, err = f.svc.Confirm(ctx, f.hub(), code); !errors.Is(err, ErrNoPending) {
		t.Fatalf("another hub confirmed this hub's code: %v", err)
	}
	status, err = f.svc.Confirm(ctx, hubID, code)
	if err != nil || status.State != store.SMSActive || !status.CodeExpiresAt.IsZero() {
		t.Fatalf("confirm: %+v %v", status, err)
	}
	if _, err = f.svc.Confirm(ctx, hubID, code); !errors.Is(err, ErrNoPending) {
		t.Fatalf("second confirm: %v", err)
	}
	// The same number again needs no new code.
	if status, err = f.svc.StartVerification(ctx, hubID, alice, "en"); err != nil || status.State != store.SMSActive || len(f.fake.SentTo(alice)) != 1 {
		t.Fatalf("restart for the active number: %+v %v", status, err)
	}
	if _, err = f.svc.Send(ctx, hubID, "Isoform: \"Q4 deck\" is ready to review."); err != nil {
		t.Fatal(err)
	}
	if err = f.inbound(alice, "a private reply about the Q4 deck"); err != nil {
		t.Fatal(err)
	}
	raw := f.databaseBytes()
	for _, secret := range []string{"4155550123", "Q4 deck", code} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("the database holds %q in the clear", secret)
		}
	}
}

func TestVerificationCodeExpires(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	if _, err := f.svc.StartVerification(ctx, hubID, alice, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.fake.SentTo(alice)[0].Body, "验证码") {
		t.Fatal("the Chinese code text was not used")
	}
	code := f.lastCode(alice)
	f.advance(CodeTTL + time.Second)
	if _, err := f.svc.Confirm(ctx, hubID, code); !errors.Is(err, ErrCodeSpent) {
		t.Fatalf("expired code: %v", err)
	}
	if status, _ := f.svc.Status(ctx, hubID); status.State != "none" || !status.CodeExpiresAt.IsZero() {
		t.Fatalf("an expired code is still reported as out: %+v", status)
	}
	// A new code works.
	if _, err := f.svc.StartVerification(ctx, hubID, alice, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Confirm(ctx, hubID, f.lastCode(alice)); err != nil {
		t.Fatalf("fresh code: %v", err)
	}
}

func TestVerificationCodeAttempts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	if _, err := f.svc.StartVerification(ctx, hubID, alice, "en"); err != nil {
		t.Fatal(err)
	}
	code := f.lastCode(alice)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 1; i < CodeAttempts; i++ {
		if _, err := f.svc.Confirm(ctx, hubID, wrong); !errors.Is(err, ErrCodeWrong) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := f.svc.Confirm(ctx, hubID, wrong); !errors.Is(err, ErrCodeSpent) {
		t.Fatalf("last attempt: %v", err)
	}
	if _, err := f.svc.Confirm(ctx, hubID, code); !errors.Is(err, ErrCodeSpent) {
		t.Fatalf("the right code after the attempts ran out: %v", err)
	}
}

func TestVerificationLimits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	for i := 0; i < codesPerHub; i++ {
		if _, err := f.svc.StartVerification(ctx, hubID, alice, "en"); err != nil {
			t.Fatalf("code %d: %v", i, err)
		}
	}
	_, err := f.svc.StartVerification(ctx, hubID, alice, "en")
	var retry *RetryError
	if !errors.As(err, &retry) || !errors.Is(err, ErrTooMany) || retry.Wait <= 0 {
		t.Fatalf("fourth code in an hour: %v", err)
	}
	// Many hubs cannot flood one number with codes.
	for i := 0; i < codesPerNumber-codesPerHub; i++ {
		if _, err = f.svc.StartVerification(ctx, f.hub(), bob, "en"); err != nil {
			t.Fatal(err)
		}
	}
	f.advance(2 * time.Hour)
	for i := 0; i < codesPerHub; i++ {
		if _, err = f.svc.StartVerification(ctx, f.hub(), bob, "en"); err != nil && !errors.Is(err, ErrTooMany) {
			t.Fatal(err)
		}
	}
	if got := len(f.fake.SentTo(bob)); got != codesPerNumber {
		t.Fatalf("bob received %d codes, the daily limit is %d", got, codesPerNumber)
	}
	for _, raw := range []string{"4155550123", "+1415", "not a number"} {
		if _, err = f.svc.StartVerification(ctx, hubID, raw, "en"); !errors.Is(err, ErrInvalidNumber) {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	for _, number := range []string{"+442079460100", "+18765550100", "+12425550100", "+19005550100", "+18885550100", "+14151230100", "+12115550100", "+14565550100"} {
		if _, err = f.svc.StartVerification(ctx, hubID, number, "en"); !errors.Is(err, ErrCountry) {
			t.Fatalf("%s is not a US or Canadian personal number: %v", number, err)
		}
	}
	// A hub the relay never saw has no entitlement either.
	if _, err = f.svc.StartVerification(ctx, uuid.NewString(), "+14155550111", "en"); !errors.Is(err, ErrNotEntitled) {
		t.Fatalf("unknown hub: %v", err)
	}
}

func TestSendEnforcesStateBodyAndQuota(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("send without a subscription: %v", err)
	}
	if _, err := f.svc.StartVerification(ctx, hubID, alice, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("send while pending: %v", err)
	}
	if _, err := f.svc.Confirm(ctx, hubID, f.lastCode(alice)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"", " \n ", strings.Repeat("x", MaxBodyRunes+1), "bad\x07"} {
		if _, err := f.svc.Send(ctx, hubID, body); !errors.Is(err, ErrInvalidBody) {
			t.Fatalf("body %q: %v", body, err)
		}
	}
	before := len(f.fake.SentTo(alice))
	for i := 0; i < DefaultDailyCap; i++ {
		status, err := f.svc.Send(ctx, hubID, fmt.Sprintf("Isoform: text %d", i))
		if err != nil || status.SentToday != i+1 || status.SentThisMonth != i+1 {
			t.Fatalf("send %d: %+v %v", i, status, err)
		}
	}
	_, err := f.svc.Send(ctx, hubID, "Isoform: one too many")
	var retry *RetryError
	if !errors.As(err, &retry) || !errors.Is(err, ErrQuota) || retry.Wait != 15*time.Hour {
		t.Fatalf("over the daily cap: %v", err)
	}
	if got := len(f.fake.SentTo(alice)) - before; got != DefaultDailyCap {
		t.Fatalf("%d texts sent, cap %d", got, DefaultDailyCap)
	}
	f.advance(24 * time.Hour)
	status, err := f.svc.Send(ctx, hubID, "Isoform: a new day")
	if err != nil || status.SentToday != 1 || status.SentThisMonth != DefaultDailyCap+1 {
		t.Fatalf("next day: %+v %v", status, err)
	}

	// A refusal is refunded; an uncertain failure is counted.
	f.fake.FailWith(fmt.Errorf("%w (status 400, code 21211)", ErrRejected))
	if _, err = f.svc.Send(ctx, hubID, "Isoform: refused"); !errors.Is(err, ErrSendFailed) {
		t.Fatalf("rejected: %v", err)
	}
	if status, _ = f.svc.Status(ctx, hubID); status.SentToday != 1 {
		t.Fatalf("rejected text counted: %+v", status)
	}
	f.fake.FailWith(ErrUnavailable)
	if _, err = f.svc.Send(ctx, hubID, "Isoform: maybe"); !errors.Is(err, ErrSendFailed) {
		t.Fatalf("unavailable: %v", err)
	}
	if status, _ = f.svc.Status(ctx, hubID); status.SentToday != 2 {
		t.Fatalf("uncertain text not counted: %+v", status)
	}
	f.fake.FailWith(nil)
}

func TestMonthlyCapSurvivesResubscribing(t *testing.T) {
	f := newFixture(t)
	f.svc.Config.MonthlyCap = 3
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	for i := 0; i < 3; i++ {
		if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); err != nil {
			t.Fatal(err)
		}
		f.advance(24 * time.Hour)
	}
	if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrQuota) {
		t.Fatalf("over the monthly cap: %v", err)
	}
	// Removing and verifying again, even from another hub, does not reset it.
	if err := f.svc.Unsubscribe(ctx, hubID); err != nil {
		t.Fatal(err)
	}
	other := f.hub()
	f.subscribe(other, alice)
	if _, err := f.svc.Send(ctx, other, "Isoform: hi"); !errors.Is(err, ErrQuota) {
		t.Fatalf("a new subscription reset the cap: %v", err)
	}
	f.advance(20 * 24 * time.Hour)
	if _, err := f.svc.Send(ctx, other, "Isoform: next month"); err != nil {
		t.Fatalf("next month: %v", err)
	}
}

func TestHourlyCapsBoundEveryText(t *testing.T) {
	f := newFixture(t)
	f.svc.Config.HourlyCap = 3
	f.svc.Config.CodesPerHour = 2
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice) // the code counts against the code budget only
	for i := 0; i < 3; i++ {
		if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrBusy) {
		t.Fatalf("over the hourly cap: %v", err)
	}
	if _, err := f.svc.StartVerification(ctx, f.hub(), bob, "en"); err != nil {
		t.Fatalf("second code: %v", err)
	}
	if _, err := f.svc.StartVerification(ctx, f.hub(), "+14155550177", "en"); !errors.Is(err, ErrBusy) {
		t.Fatalf("over the code budget: %v", err)
	}
	// The caps are kept in the store, so a restarted service still holds them.
	restarted := NewService(f.store, f.fake, f.svc.Vault, f.svc.Config)
	restarted.Now = f.now
	if _, err := restarted.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrBusy) {
		t.Fatalf("the hourly cap reset with the process: %v", err)
	}
	if status, _ := f.svc.Status(ctx, hubID); status.SentToday != 3 {
		t.Fatalf("a text held by the hourly cap was counted: %+v", status)
	}
	// STOP is still confirmed.
	if err := f.inbound(alice, "STOP"); err != nil {
		t.Fatal(err)
	}
	texts := f.fake.SentTo(alice)
	if texts[len(texts)-1].Body != stopReply {
		t.Fatal("the STOP confirmation was held by the hourly cap")
	}
}

func TestStopKeywordsStopEverything(t *testing.T) {
	for _, keyword := range []string{"STOP", "stop", " Unsubscribe ", "CANCEL", "end", "Quit.", "STOPALL"} {
		t.Run(keyword, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			hubID := f.hub()
			f.subscribe(hubID, alice)
			if err := f.inbound(alice, keyword); err != nil {
				t.Fatal(err)
			}
			if s := f.state(hubID); s != store.SMSStopped {
				t.Fatalf("state %q after %q", s, keyword)
			}
			if len(f.changed) != 1 || f.changed[0] != hubID {
				t.Fatalf("hub not told: %v", f.changed)
			}
			sent := len(f.fake.SentTo(alice))
			if last := f.fake.SentTo(alice)[sent-1].Body; last != stopReply {
				t.Fatalf("confirmation %q", last)
			}
			// Once: a second STOP is not confirmed again.
			if err := f.inbound(alice, "STOP"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrOptedOut) {
				t.Fatalf("send after STOP: %v", err)
			}
			if _, err := f.svc.StartVerification(ctx, f.hub(), alice, "en"); !errors.Is(err, ErrOptedOut) {
				t.Fatalf("code after STOP: %v", err)
			}
			if _, err := f.svc.StartVerification(ctx, hubID, alice, "en"); !errors.Is(err, ErrOptedOut) {
				t.Fatalf("code to own stopped number: %v", err)
			}
			if err := f.inbound(alice, "Y"); err != nil {
				t.Fatal(err)
			}
			if replies, _ := f.svc.Replies(ctx, hubID, 10); len(replies) != 0 {
				t.Fatalf("a stopped number's reply was forwarded: %v", replies)
			}
			if got := len(f.fake.SentTo(alice)); got != sent {
				t.Fatalf("a stopped number received %d more texts", got-sent)
			}
		})
	}
}

func TestStartAndHelp(t *testing.T) {
	for _, keyword := range []string{"START", "unstop"} {
		f := newFixture(t)
		ctx := context.Background()
		hubID := f.hub()
		f.subscribe(hubID, alice)
		if err := f.inbound(alice, "STOP"); err != nil {
			t.Fatal(err)
		}
		if err := f.inbound(alice, keyword); err != nil {
			t.Fatal(err)
		}
		if s := f.state(hubID); s != store.SMSActive {
			t.Fatalf("%s: state %q", keyword, s)
		}
		texts := f.fake.SentTo(alice)
		if texts[len(texts)-1].Body != startText(DefaultDailyCap) {
			t.Fatalf("%s: reply %q", keyword, texts[len(texts)-1].Body)
		}
		if _, err := f.svc.Send(ctx, hubID, "Isoform: back"); err != nil {
			t.Fatalf("%s: send after resume: %v", keyword, err)
		}
		// START from a number that never stopped says nothing.
		before := len(f.fake.SentTo(alice))
		if err := f.inbound(alice, "START"); err != nil {
			t.Fatal(err)
		}
		if len(f.fake.SentTo(alice)) != before {
			t.Fatalf("%s: START without STOP was answered", keyword)
		}
	}

	f := newFixture(t)
	for i := 0; i < 4; i++ {
		if err := f.inbound(bob, "help"); err != nil {
			t.Fatal(err)
		}
	}
	texts := f.fake.SentTo(bob)
	if len(texts) != helpPerNumber || texts[0].Body != helpText(DefaultDailyCap, "help@example.com") {
		t.Fatalf("help replies %+v", texts)
	}
}

func TestInboundSignatureAndReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)

	form := url.Values{"From": {alice}, "To": {"+18005550100"}, "Body": {"STOP"}, "MessageId": {"forged"}}
	for _, signature := range []string{"", "00", f.fake.Sign(url.Values{"Body": {"Y"}})} {
		r, _ := http.NewRequest(http.MethodPost, "http://relay.test/v1/sms/inbound", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if signature != "" {
			r.Header.Set("X-Fake-Signature", signature)
		}
		if err := f.svc.HandleInbound(ctx, r); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("signature %q: %v", signature, err)
		}
	}
	if f.state(hubID) != store.SMSActive {
		t.Fatal("a forged STOP took effect")
	}

	for i := 0; i < 3; i++ {
		if err := f.inboundWithID(alice, "Y", "same-id"); err != nil {
			t.Fatal(err)
		}
	}
	replies, err := f.svc.Replies(ctx, hubID, 10)
	if err != nil || len(replies) != 1 || replies[0].Body != "Y" {
		t.Fatalf("replayed webhook: %+v %v", replies, err)
	}
	if len(f.replied) != 1 {
		t.Fatalf("hub kicked %d times", len(f.replied))
	}
}

func TestRepliesForwardAndAck(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	for _, body := range []string{"Y", "  can you move it to 3pm?\r\n", "\x00"} {
		if err := f.inbound(alice, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.inbound(bob, "Y"); err != nil {
		t.Fatal(err)
	}
	replies, err := f.svc.Replies(ctx, hubID, 10)
	if err != nil || len(replies) != 2 || replies[0].Body != "Y" || replies[1].Body != "can you move it to 3pm?" {
		t.Fatalf("replies %+v %v", replies, err)
	}
	if err = f.svc.Ack(ctx, f.hub(), replies[0].ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := f.svc.Replies(ctx, hubID, 10); len(again) != 2 {
		t.Fatal("another hub acknowledged this hub's reply")
	}
	if err = f.svc.Ack(ctx, hubID, replies[0].ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := f.svc.Replies(ctx, hubID, 10); len(again) != 1 {
		t.Fatalf("ack did not remove the reply: %+v", again)
	}
	f.advance(ReplyTTL + time.Minute)
	if again, _ := f.svc.Replies(ctx, hubID, 10); len(again) != 0 {
		t.Fatal("an expired reply is still delivered")
	}
}

func TestInboundRateLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	for i := 0; i < repliesPerNumber+5; i++ {
		if err := f.inbound(alice, fmt.Sprintf("reply %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	replies, _ := f.svc.Replies(ctx, hubID, 100)
	if len(replies) != repliesPerNumber {
		t.Fatalf("%d replies forwarded, limit %d", len(replies), repliesPerNumber)
	}
	// STOP is honoured past the limit.
	if err := f.inbound(alice, "STOP"); err != nil {
		t.Fatal(err)
	}
	if f.state(hubID) != store.SMSStopped {
		t.Fatal("STOP over the inbound limit was ignored")
	}
}

func TestConfirmingTakesTheNumberFromAnotherHub(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first, second := f.hub(), f.hub()
	f.subscribe(first, alice)
	f.subscribe(second, alice)
	if s := f.state(first); s != "none" {
		t.Fatalf("first hub still holds the number: %q", s)
	}
	if len(f.changed) != 1 || f.changed[0] != first {
		t.Fatalf("first hub not told: %v", f.changed)
	}
	if err := f.inbound(alice, "Y"); err != nil {
		t.Fatal(err)
	}
	if replies, _ := f.svc.Replies(ctx, second, 10); len(replies) != 1 {
		t.Fatal("the reply did not reach the hub that holds the number")
	}
	if replies, _ := f.svc.Replies(ctx, first, 10); len(replies) != 0 {
		t.Fatal("the reply reached the hub that lost the number")
	}
}

func TestProviderOptOutStopsTheSubscription(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	f.fake.FailWith(ErrRecipientStopped)
	if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrOptedOut) {
		t.Fatalf("send: %v", err)
	}
	f.fake.FailWith(nil)
	if f.state(hubID) != store.SMSStopped {
		t.Fatal("the provider's opt-out did not stop the subscription")
	}
	if status, _ := f.svc.Status(ctx, hubID); status.SentToday != 0 {
		t.Fatalf("a refused text was counted: %+v", status)
	}
}

func TestUnsubscribe(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	if err := f.svc.Unsubscribe(ctx, hubID); err != nil {
		t.Fatal(err)
	}
	if f.state(hubID) != "none" {
		t.Fatal("subscription kept")
	}
	if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("send after unsubscribe: %v", err)
	}
	if err := f.inbound(alice, "Y"); err != nil {
		t.Fatal(err)
	}
	if replies, _ := f.svc.Replies(ctx, hubID, 10); len(replies) != 0 {
		t.Fatal("a reply was forwarded after unsubscribing")
	}
}

// TestNothingLogsNumbersOrBodies runs the whole flow with log output captured.
func TestNothingLogsNumbersOrBodies(t *testing.T) {
	var captured bytes.Buffer
	log.SetOutput(&captured)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	_, _ = f.svc.Send(ctx, hubID, "Isoform: secret plans")
	f.fake.FailWith(fmt.Errorf("%w (status 400, code 21211)", ErrRejected))
	_, _ = f.svc.Send(ctx, hubID, "Isoform: secret plans")
	f.fake.FailWith(ErrUnavailable)
	_ = f.inbound(alice, "help")
	f.fake.FailWith(nil)
	_ = f.inbound(alice, "secret reply")
	if strings.Contains(captured.String(), "4155550123") || strings.Contains(captured.String(), "secret") {
		t.Fatalf("log carries a number or a body: %s", captured.String())
	}
}

func TestChangingNumberKeepsTheOldOneUntilConfirmed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	status, err := f.svc.StartVerification(ctx, hubID, bob, "en")
	if err != nil || status.State != store.SMSActive || status.CodeExpiresAt.IsZero() {
		t.Fatalf("code for a new number: %+v %v", status, err)
	}
	if _, err = f.svc.Send(ctx, hubID, "Isoform: still the old number"); err != nil {
		t.Fatal(err)
	}
	if texts := f.fake.SentTo(alice); texts[len(texts)-1].Body != "Isoform: still the old number" {
		t.Fatal("the old number stopped receiving before the new one was confirmed")
	}
	// A code that cannot be texted leaves the subscription alone.
	f.fake.FailWith(ErrUnavailable)
	if _, err = f.svc.StartVerification(ctx, hubID, "+14155550177", "en"); !errors.Is(err, ErrSendFailed) {
		t.Fatalf("failed code: %v", err)
	}
	f.fake.FailWith(nil)
	if status, _ = f.svc.Status(ctx, hubID); status.State != store.SMSActive || !status.CodeExpiresAt.IsZero() {
		t.Fatalf("after a failed code: %+v", status)
	}
	f.advance(time.Hour) // past the per-hub code limit
	if _, err = f.svc.StartVerification(ctx, hubID, bob, "en"); err != nil {
		t.Fatal(err)
	}
	if status, err = f.svc.Confirm(ctx, hubID, f.lastCode(bob)); err != nil || status.State != store.SMSActive {
		t.Fatalf("confirm the new number: %+v %v", status, err)
	}
	if _, err = f.svc.Send(ctx, hubID, "Isoform: the new number"); err != nil {
		t.Fatal(err)
	}
	if texts := f.fake.SentTo(bob); texts[len(texts)-1].Body != "Isoform: the new number" {
		t.Fatal("the new number does not receive")
	}
	if err = f.inbound(alice, "Y"); err != nil {
		t.Fatal(err)
	}
	if replies, _ := f.svc.Replies(ctx, hubID, 10); len(replies) != 0 {
		t.Fatal("the old number's reply was forwarded")
	}
}

func TestWrongCodesLockTheNumberAcrossCodesAndHubs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	guess := func(hubID string, times int) string {
		if _, err := f.svc.StartVerification(ctx, hubID, alice, "en"); err != nil {
			t.Fatal(err)
		}
		code := f.lastCode(alice)
		wrong := "000000"
		if code == wrong {
			wrong = "111111"
		}
		for i := 0; i < times; i++ {
			if _, err := f.svc.Confirm(ctx, hubID, wrong); !errors.Is(err, ErrCodeWrong) {
				t.Fatalf("wrong guess %d: %v", i, err)
			}
		}
		return code
	}
	guess(f.hub(), CodeAttempts-1)
	guess(f.hub(), CodeAttempts-1)
	last := f.hub()
	code := guess(last, WrongCodesPerNumber-2*(CodeAttempts-1))
	if _, err := f.svc.Confirm(ctx, last, code); !errors.Is(err, ErrTooMany) {
		t.Fatalf("the right code after the number's wrong-code budget: %v", err)
	}
	f.advance(25 * time.Hour)
	code = guess(last, 0)
	if _, err := f.svc.Confirm(ctx, last, code); err != nil {
		t.Fatalf("the next day: %v", err)
	}
}

func TestFailedHandlingIsRetried(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"sms_replies", "sms_optouts"} {
		if _, err = db.Exec("CREATE TRIGGER fail_" + table + " BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT, 'test'); END"); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.inboundWithID(alice, "Y", "retry-reply"); err == nil {
		t.Fatal("a reply that could not be queued reported success")
	}
	if err = f.inboundWithID(alice, "STOP", "retry-stop"); err == nil {
		t.Fatal("a STOP that could not be recorded reported success")
	}
	for _, table := range []string{"sms_replies", "sms_optouts"} {
		if _, err = db.Exec("DROP TRIGGER fail_" + table); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.inboundWithID(alice, "Y", "retry-reply"); err != nil {
		t.Fatal(err)
	}
	if replies, _ := f.svc.Replies(ctx, hubID, 10); len(replies) != 1 {
		t.Fatal("the retried reply was ignored as a duplicate")
	}
	if err = f.inboundWithID(alice, "STOP", "retry-stop"); err != nil {
		t.Fatal(err)
	}
	if f.state(hubID) != store.SMSStopped {
		t.Fatal("the retried STOP was ignored as a duplicate")
	}
}

func TestStopFromAnUnknownNumber(t *testing.T) {
	f := newFixture(t)
	if err := f.inbound(bob, "STOP"); err != nil {
		t.Fatal(err)
	}
	if len(f.fake.SentTo(bob)) != 0 {
		t.Fatal("a number the relay never texted got a STOP confirmation")
	}
	if _, err := f.svc.StartVerification(context.Background(), f.hub(), bob, "en"); !errors.Is(err, ErrOptedOut) {
		t.Fatalf("code to a number that opted out: %v", err)
	}
	if len(f.fake.SentTo(bob)) != 0 {
		t.Fatal("an opted-out number got a code")
	}
}

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func TestCodesAndSendsNeedTheEntitlement(t *testing.T) {
	f := newFixture(t)
	f.svc.Config.CodesPerHour = 1
	ctx := context.Background()
	hubID := f.bareHub()
	for i := 0; i < 3; i++ {
		if _, err := f.svc.StartVerification(ctx, hubID, alice, "en"); !errors.Is(err, ErrNotEntitled) {
			t.Fatalf("code for a hub without the entitlement: %v", err)
		}
	}
	if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); !errors.Is(err, ErrNotEntitled) {
		t.Fatalf("send for a hub without the entitlement: %v", err)
	}
	if len(f.fake.Sent()) != 0 {
		t.Fatal("a hub without the entitlement got a text out")
	}
	// The refusals spent nothing: the one code of the budget is still there.
	if _, err := f.svc.Enable(ctx, hubID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.StartVerification(ctx, hubID, alice, "en"); err != nil {
		t.Fatalf("code after enable: %v", err)
	}
	if _, err := f.svc.Enable(ctx, uuid.NewString()); !errors.Is(err, ErrUnknownHub) {
		t.Fatalf("enable for an unknown hub: %v", err)
	}
}

func TestKeywordAnswersGoOnlyToAllowedNumbersWithinTheirOwnBudget(t *testing.T) {
	f := newFixture(t)
	f.svc.Config.HourlyCap = 1
	f.svc.Config.KeywordAnswersPerHour = 2
	ctx := context.Background()
	// A number the relay could never subscribe gets no answer, but its STOP
	// is still recorded.
	for _, from := range []string{"+447700900123", "+14565550100", "+18765550100"} {
		for _, keyword := range []string{"HELP", "STOP", "START", "STOP"} {
			if err := f.inbound(from, keyword); err != nil {
				t.Fatal(err)
			}
		}
		if sent := f.fake.SentTo(from); len(sent) != 0 {
			t.Fatalf("%s got %d keyword answers", from, len(sent))
		}
		if stopped, err := f.store.SMSOptedOut(ctx, f.svc.Vault.HashNumber(from)); err != nil || !stopped {
			t.Fatalf("%s: STOP not recorded: %v %v", from, stopped, err)
		}
	}
	// HELP answers spend their own budget, not the hubs' hourly cap.
	for _, from := range []string{bob, "+14155550177", "+14155550178"} {
		if err := f.inbound(from, "HELP"); err != nil {
			t.Fatal(err)
		}
	}
	if sent := len(f.fake.Sent()); sent != 2 {
		t.Fatalf("%d HELP answers, budget 2", sent)
	}
	hubID := f.hub()
	f.subscribe(hubID, alice)
	if _, err := f.svc.Send(ctx, hubID, "Isoform: hi"); err != nil {
		t.Fatalf("a hub's text after keyword answers: %v", err)
	}
}

func TestRotatedKeyOpensOldValuesAndResealMovesThem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hubID := f.hub()
	f.subscribe(hubID, alice)
	if err := f.inbound(alice, "Y"); err != nil {
		t.Fatal(err)
	}
	stopper := f.hub()
	f.subscribe(stopper, bob)
	if err := f.inbound(bob, "STOP"); err != nil {
		t.Fatal(err)
	}
	old := bytes.Repeat([]byte{42}, KeySize)
	rotated, err := NewVault(VaultKeys{Seal: bytes.Repeat([]byte{43}, KeySize), Previous: old, Hash: old})
	if err != nil {
		t.Fatal(err)
	}
	f.svc.Vault = rotated
	if replies, err := f.svc.Replies(ctx, hubID, 10); err != nil || len(replies) != 1 || replies[0].Body != "Y" {
		t.Fatalf("a reply sealed before the rotation: %+v %v", replies, err)
	}
	moved, stuck, err := f.svc.Reseal(ctx)
	if err != nil || moved != 3 || stuck != 0 {
		t.Fatalf("reseal moved %d, stuck %d: %v", moved, stuck, err)
	}
	if moved, _, _ = f.svc.Reseal(ctx); moved != 0 {
		t.Fatalf("a second reseal moved %d", moved)
	}
	// Without the previous key everything still opens, and the opt-out, kept
	// under the long-lived hash key, still holds.
	f.svc.Vault, err = NewVault(VaultKeys{Seal: bytes.Repeat([]byte{43}, KeySize), Hash: old})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Send(ctx, hubID, "Isoform: after the rotation"); err != nil {
		t.Fatalf("send after the rotation: %v", err)
	}
	if replies, err := f.svc.Replies(ctx, hubID, 10); err != nil || len(replies) != 1 {
		t.Fatalf("a resealed reply: %+v %v", replies, err)
	}
	if _, err = f.svc.StartVerification(ctx, f.hub(), bob, "en"); !errors.Is(err, ErrOptedOut) {
		t.Fatalf("an opt-out was forgotten by the rotation: %v", err)
	}
}
