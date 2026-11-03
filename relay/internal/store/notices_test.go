package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/push"
	"isoform-relay/internal/wire"
)

// noticeEnvelope builds an h2d event envelope for a notice on device dev.
func noticeEnvelope(t *testing.T, hubID, devID string, seq uint64) (wire.Envelope, []byte) {
	t.Helper()
	env := wire.Envelope{V: 1, Hub: hubID, Dev: devID, Dir: "h2d", Seq: seq, Kind: "event", CT: ct(t, 60)}
	body, err := env.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return env, body
}

func testNotice(t *testing.T) push.Notice {
	t.Helper()
	return push.Notice{ID: uuid.NewString(), Moment: "decision_due", CT: ct(t, 120), Category: push.CategoryDecision, Level: push.LevelTimeSensitive}
}

func TestNoticeOutboxRowKeepsTheSealedFieldsAndTheClaimCarriesThem(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID := newHub(t, s)
	dev := seedDevice(t, s, hubID, time.Now().Add(24*time.Hour))
	now := time.Now()
	if _, err := s.RegisterPushWithGatewayGrant(ctx, dev.ID, strings.Repeat("ab", 32), "development", "grant-value", now); err != nil {
		t.Fatal(err)
	}
	notice := testNotice(t)
	env, body := noticeEnvelope(t, hubID, dev.ID, 3)
	if err := s.AddNoticeResponse(ctx, hubID, env, body, notice, now); err != nil {
		t.Fatal(err)
	}

	var kind, moment, noticeID, sealed, category, collapse, level, eventID string
	err := s.db.QueryRow(`SELECT kind,moment_kind,notice_id,ct,category,collapse_id,level,event_id FROM push_outbox WHERE device_id=?`, dev.ID).
		Scan(&kind, &moment, &noticeID, &sealed, &category, &collapse, &level, &eventID)
	if err != nil {
		t.Fatal(err)
	}
	if kind != push.KindMoment || moment != "decision_due" || noticeID != notice.ID || sealed != notice.CT ||
		category != push.CategoryDecision || collapse != notice.ID || level != push.LevelTimeSensitive || eventID != "event:3" {
		t.Fatalf("outbox row %q %q %q %q %q %q %q %q", kind, moment, noticeID, sealed, category, collapse, level, eventID)
	}

	claim, err := s.ClaimPush(ctx, now.Add(time.Second), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	if claim.Kind != push.KindMoment || claim.Notice.ID != notice.ID || claim.Notice.CT != notice.CT || claim.Notice.Moment != "decision_due" ||
		claim.Notice.Category != push.CategoryDecision || claim.Notice.Collapse != notice.ID || claim.Notice.Level != push.LevelTimeSensitive {
		t.Fatalf("claim notice %+v", claim.Notice)
	}
	if claim.GatewayGrant != "grant-value" {
		t.Fatalf("claim gateway grant %q", claim.GatewayGrant)
	}

	// A repeated envelope queues nothing; a conflicting one is refused.
	if err := s.AddNoticeResponse(ctx, hubID, env, body, notice, now); err != nil {
		t.Fatalf("identical retry refused: %v", err)
	}
	conflicting, conflictBody := noticeEnvelope(t, hubID, dev.ID, 3)
	if err := s.AddNoticeResponse(ctx, hubID, conflicting, conflictBody, notice, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("different envelope under a used sequence: %v", err)
	}
	if n := count(t, s, "SELECT count(*) FROM push_outbox WHERE device_id=?", dev.ID); n != 1 {
		t.Fatalf("pushes queued for one notice: %d", n)
	}
}

func TestInvalidNoticeStoresNothingAndQueuesNothing(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID := newHub(t, s)
	dev := seedDevice(t, s, hubID, time.Now().Add(24*time.Hour))
	now := time.Now()
	if _, err := s.RegisterPush(ctx, dev.ID, strings.Repeat("cd", 32), "development", now); err != nil {
		t.Fatal(err)
	}
	bad := testNotice(t)
	bad.Level = "critical"
	env, body := noticeEnvelope(t, hubID, dev.ID, 4)
	if err := s.AddNoticeResponse(ctx, hubID, env, body, bad, now); !errors.Is(err, push.ErrInvalidNotice) {
		t.Fatalf("invalid notice accepted: %v", err)
	}
	if n := count(t, s, "SELECT count(*) FROM envelopes WHERE device_id=? AND dir='h2d' AND seq=4", dev.ID); n != 0 {
		t.Fatalf("refused notice stored its envelope: %d", n)
	}
	if n := count(t, s, "SELECT count(*) FROM push_outbox WHERE device_id=?", dev.ID); n != 0 {
		t.Fatalf("refused notice queued a push: %d", n)
	}
}

func TestNoticeWithoutRegistrationStoresTheEventOnly(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID := newHub(t, s)
	dev := seedDevice(t, s, hubID, time.Now().Add(24*time.Hour))
	env, body := noticeEnvelope(t, hubID, dev.ID, 1)
	if err := s.AddNoticeResponse(ctx, hubID, env, body, testNotice(t), time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "SELECT count(*) FROM envelopes WHERE device_id=? AND dir='h2d'", dev.ID); n != 1 {
		t.Fatalf("event envelope not stored: %d", n)
	}
	if n := count(t, s, "SELECT count(*) FROM push_outbox WHERE device_id=?", dev.ID); n != 0 {
		t.Fatalf("push queued without a registration: %d", n)
	}
}

func TestNoticeReceiptsStoreOnceAndForwardInOrder(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID := newHub(t, s)
	dev := seedDevice(t, s, hubID, time.Now().Add(24*time.Hour))
	other := seedDevice(t, s, newHub(t, s), time.Now().Add(24*time.Hour))
	now := time.Now().UTC().Truncate(time.Second)
	noticeID := uuid.NewString()

	delivered := Receipt{NoticeID: noticeID, State: "delivered", At: now}
	if inserted, err := s.AddNoticeReceipt(ctx, dev.ID, delivered, now); err != nil || !inserted {
		t.Fatalf("first receipt: inserted=%v err=%v", inserted, err)
	}
	if inserted, err := s.AddNoticeReceipt(ctx, dev.ID, delivered, now.Add(time.Minute)); err != nil || inserted {
		t.Fatalf("repeated receipt stored again: inserted=%v err=%v", inserted, err)
	}
	acted := Receipt{NoticeID: noticeID, State: "acted", Action: "approve", At: now}
	if inserted, err := s.AddNoticeReceipt(ctx, dev.ID, acted, now); err != nil || !inserted {
		t.Fatalf("acted receipt: inserted=%v err=%v", inserted, err)
	}
	if _, err := s.AddNoticeReceipt(ctx, other.ID, Receipt{NoticeID: noticeID, State: "opened", At: now}, now); err != nil {
		t.Fatal(err)
	}

	pending, err := s.UnforwardedReceipts(ctx, hubID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].State != "delivered" || pending[1].State != "acted" || pending[1].Action != "approve" || !pending[0].At.Equal(now) {
		t.Fatalf("pending receipts for the hub %+v", pending)
	}
	if err = s.MarkReceiptForwarded(ctx, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	pending, err = s.UnforwardedReceipts(ctx, hubID, 10)
	if err != nil || len(pending) != 1 || pending[0].State != "acted" {
		t.Fatalf("after forwarding: %+v %v", pending, err)
	}
}

func TestSweepRemovesOldNoticeReceipts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID := newHub(t, s)
	dev := seedDevice(t, s, hubID, time.Now().Add(24*time.Hour))
	now := time.Now()
	old := now.Add(-NoticeReceiptKeep - time.Hour)
	if _, err := s.AddNoticeReceipt(ctx, dev.ID, Receipt{NoticeID: uuid.NewString(), State: "opened", At: old}, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddNoticeReceipt(ctx, dev.ID, Receipt{NoticeID: uuid.NewString(), State: "opened", At: now}, now); err != nil {
		t.Fatal(err)
	}
	swept, err := s.Sweep(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Receipts != 1 || count(t, s, "SELECT count(*) FROM notice_receipts") != 1 {
		t.Fatalf("swept %+v, rows left %d", swept, count(t, s, "SELECT count(*) FROM notice_receipts"))
	}
}

// releaseTwoOutbox is push_outbox as the second release created it: it has the
// kind column but none of the moment columns.
const releaseTwoOutbox = `
CREATE TABLE push_outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	event_id TEXT NOT NULL,
	notification_id TEXT NOT NULL UNIQUE,
	state TEXT NOT NULL CHECK (state IN ('queued','inflight','accepted','permanent','exhausted','cancelled')),
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	next_at INTEGER NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	lease_until INTEGER NOT NULL DEFAULT 0,
	generation INTEGER NOT NULL DEFAULT 0,
	kind TEXT NOT NULL DEFAULT 'brief',
	UNIQUE(device_id, event_id)
);
CREATE INDEX push_outbox_due ON push_outbox(state, next_at);
`

func TestOpenAddsNoticeColumnsAndReceiptTableToReleaseTwo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = old.Exec(releaseTwoOutbox); err != nil {
		t.Fatal(err)
	}
	if _, err = old.Exec("INSERT INTO push_outbox(device_id,event_id,notification_id,kind,state,created_at,expires_at,next_at) VALUES(?,?,?,'needs_you','queued',?,?,?)", uuid.NewString(), "event:5", uuid.NewString(), now.UnixMilli(), now.Add(time.Hour).UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	// The second open finds the columns present and must leave them alone.
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range []string{"kind", "moment_kind", "notice_id", "ct", "category", "collapse_id", "level"} {
			if n := count(t, s, "SELECT count(*) FROM pragma_table_info('push_outbox') WHERE name=?", column); n != 1 {
				t.Fatalf("column %s count %d", column, n)
			}
		}
		if n := count(t, s, "SELECT count(*) FROM push_outbox WHERE kind='needs_you' AND ct='' AND moment_kind=''"); n != 1 {
			t.Fatalf("release-two row changed by the migration: %d", n)
		}
		if n := count(t, s, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='notice_receipts'"); n != 1 {
			t.Fatalf("notice_receipts table missing: %d", n)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
