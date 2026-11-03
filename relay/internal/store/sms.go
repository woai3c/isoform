package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Text messages (PROTOCOL.md section 18): a hub may subscribe one verified
// phone number to texts the relay sends through an SMS provider. The store
// keeps a number twice, as a keyed hash for lookup and as ciphertext under the
// relay's SMS key, and never in the clear. It keeps no outbound bodies, and an
// inbound reply only until the hub acknowledges it, sealed the same way.
// Hashing and sealing happen in package sms; this file stores opaque values.
//
// A verification (a code texted to a number) lives apart from the verified
// subscription, so a hub changing its number keeps texting the old one until
// the new one is confirmed.

const (
	// SMSPending is reported for a hub with a verification and no subscription.
	SMSPending = "pending_verification"
	// SMSActive is a verified subscription that may receive texts.
	SMSActive = "active"
	// SMSStopped is a verified subscription whose number replied STOP.
	SMSStopped = "stopped"

	// KeptSMSReplies bounds the replies queued for one hub; the oldest go first.
	KeptSMSReplies = 50
	// SMSInboundKeep is how long provider message ids are remembered, so a
	// replayed webhook is ignored. Provider signatures carry no timestamp, so
	// the window is long; a row is a hash and a time.
	SMSInboundKeep = 400 * 24 * time.Hour
	// smsVerificationKeep is how long an expired verification is kept, so the
	// hub can still be told its code expired, before it and its encrypted
	// number are removed.
	smsVerificationKeep = 24 * time.Hour
	// smsWrongCodeWindow is the period the per-number wrong-code budget counts in.
	smsWrongCodeWindow = 24 * time.Hour
)

var (
	// ErrOptedOut reports a number that replied STOP and has not replied START.
	ErrOptedOut = errors.New("number opted out")
	// ErrQuota reports a subscription at its daily or monthly cap.
	ErrQuota = errors.New("text quota reached")
	// ErrAttempts reports a verification code that was guessed wrong too often.
	ErrAttempts = errors.New("too many verification attempts")
	// ErrLocked reports a number with too many wrong codes today, across codes and hubs.
	ErrLocked = errors.New("too many wrong codes for this number")
)

const smsSchema = `
CREATE TABLE IF NOT EXISTS sms_subscriptions (
	hub_id TEXT PRIMARY KEY REFERENCES hubs(id) ON DELETE CASCADE,
	number_hash TEXT NOT NULL UNIQUE,
	number_ct TEXT NOT NULL,
	state TEXT NOT NULL CHECK (state IN ('active','stopped')),
	verified_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sms_verifications (
	hub_id TEXT PRIMARY KEY REFERENCES hubs(id) ON DELETE CASCADE,
	number_hash TEXT NOT NULL,
	number_ct TEXT NOT NULL,
	code_hash TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sms_verifications_number ON sms_verifications(number_hash);
CREATE TABLE IF NOT EXISTS sms_usage (
	number_hash TEXT PRIMARY KEY,
	day_key TEXT NOT NULL,
	day_count INTEGER NOT NULL,
	month_key TEXT NOT NULL,
	month_count INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sms_optouts (
	number_hash TEXT PRIMARY KEY,
	at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sms_inbound (
	message_hash TEXT PRIMARY KEY,
	received_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sms_replies (
	id TEXT PRIMARY KEY,
	hub_id TEXT NOT NULL REFERENCES hubs(id) ON DELETE CASCADE,
	body_ct TEXT NOT NULL,
	received_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sms_replies_hub ON sms_replies(hub_id, received_at);
CREATE TABLE IF NOT EXISTS sms_limits (
	key TEXT PRIMARY KEY,
	window_end INTEGER NOT NULL,
	count INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sms_enabled (
	hub_id TEXT PRIMARY KEY REFERENCES hubs(id) ON DELETE CASCADE,
	enabled_at INTEGER NOT NULL
);
`

// SMSSubscription is one hub's verified subscription. NumberHash and NumberCT
// are produced by package sms; the store compares the hash and never reads
// the ciphertext. Usage is counted per number, so it survives a subscription
// being removed and created again.
type SMSSubscription struct {
	HubID      string
	NumberHash string
	NumberCT   string
	State      string
	VerifiedAt time.Time
}

// SMSVerification is a code texted to a number and waiting for the hub.
type SMSVerification struct {
	HubID      string
	NumberHash string
	NumberCT   string
	CodeHash   string
	ExpiresAt  time.Time
	Attempts   int
}

// SMSUsage is the texts counted for one number in the current UTC day and month.
type SMSUsage struct {
	DayKey     string
	DayCount   int
	MonthKey   string
	MonthCount int
}

// SMSReply is one inbound text queued for its hub. BodyCT is sealed by package sms.
type SMSReply struct {
	ID         string
	HubID      string
	BodyCT     string
	ReceivedAt time.Time
}

// SMSOptChange is what a STOP or START changed. Changed is false when the
// number was already in that state. Known is whether the number had a
// subscription or a verification. Hubs lists the hubs whose state changed.
type SMSOptChange struct {
	Changed bool
	Known   bool
	Hubs    []string
}

// SMSDayKey and SMSMonthKey name the UTC periods the caps count in.
func SMSDayKey(now time.Time) string   { return now.UTC().Format("2006-01-02") }
func SMSMonthKey(now time.Time) string { return now.UTC().Format("2006-01") }

func scanSMSSubscription(row *sql.Row) (SMSSubscription, error) {
	var sub SMSSubscription
	var verified int64
	err := row.Scan(&sub.HubID, &sub.NumberHash, &sub.NumberCT, &sub.State, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return SMSSubscription{}, ErrNotFound
	}
	if err != nil {
		return SMSSubscription{}, err
	}
	sub.VerifiedAt = fromMillis(verified)
	return sub, nil
}

func scanSMSVerification(row *sql.Row) (SMSVerification, error) {
	var v SMSVerification
	var expires int64
	err := row.Scan(&v.HubID, &v.NumberHash, &v.NumberCT, &v.CodeHash, &expires, &v.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return SMSVerification{}, ErrNotFound
	}
	if err != nil {
		return SMSVerification{}, err
	}
	v.ExpiresAt = fromMillis(expires)
	return v, nil
}

const (
	smsSubscriptionSelect = "SELECT hub_id,number_hash,number_ct,state,verified_at FROM sms_subscriptions "
	smsVerificationSelect = "SELECT hub_id,number_hash,number_ct,code_hash,expires_at,attempts FROM sms_verifications "
)

// SMSSubscriptionOf returns the hub's verified subscription or ErrNotFound.
func (s *Store) SMSSubscriptionOf(ctx context.Context, hubID string) (SMSSubscription, error) {
	return scanSMSSubscription(s.db.QueryRowContext(ctx, smsSubscriptionSelect+"WHERE hub_id=?", hubID))
}

// SMSVerificationOf returns the hub's verification, expired or not, or ErrNotFound.
func (s *Store) SMSVerificationOf(ctx context.Context, hubID string) (SMSVerification, error) {
	return scanSMSVerification(s.db.QueryRowContext(ctx, smsVerificationSelect+"WHERE hub_id=?", hubID))
}

// SMSSubscriptionByNumber returns the verified subscription for a number, or ErrNotFound.
func (s *Store) SMSSubscriptionByNumber(ctx context.Context, numberHash string) (SMSSubscription, error) {
	return scanSMSSubscription(s.db.QueryRowContext(ctx, smsSubscriptionSelect+"WHERE number_hash=?", numberHash))
}

// SMSUsageOf returns the number's counts for the periods of now. Counts from
// an earlier day or month read as zero.
func (s *Store) SMSUsageOf(ctx context.Context, numberHash string, now time.Time) (SMSUsage, error) {
	return smsUsage(ctx, s.db, numberHash, now)
}

func smsUsage(ctx context.Context, q querier, numberHash string, now time.Time) (SMSUsage, error) {
	usage := SMSUsage{DayKey: SMSDayKey(now), MonthKey: SMSMonthKey(now)}
	var stored SMSUsage
	err := q.QueryRowContext(ctx, "SELECT day_key,day_count,month_key,month_count FROM sms_usage WHERE number_hash=?", numberHash).
		Scan(&stored.DayKey, &stored.DayCount, &stored.MonthKey, &stored.MonthCount)
	if errors.Is(err, sql.ErrNoRows) {
		return usage, nil
	}
	if err != nil {
		return usage, err
	}
	if stored.DayKey == usage.DayKey {
		usage.DayCount = stored.DayCount
	}
	if stored.MonthKey == usage.MonthKey {
		usage.MonthCount = stored.MonthCount
	}
	return usage, nil
}

// SMSOptedOut reports whether the number replied STOP and has not resumed.
func (s *Store) SMSOptedOut(ctx context.Context, numberHash string) (bool, error) {
	return smsOptedOut(ctx, s.db, numberHash)
}

func smsOptedOut(ctx context.Context, q querier, numberHash string) (bool, error) {
	var found int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM sms_optouts WHERE number_hash=?", numberHash).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// hubExists reports ErrNotFound for a hub id the relay has never seen.
func hubExists(ctx context.Context, q querier, hubID string) error {
	var found int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM hubs WHERE id=?", hubID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// StartSMSVerification stores a code hash for the number as the hub's only
// verification, replacing an earlier one. The hub's subscription is left as
// it is. A number that replied STOP is refused with ErrOptedOut, and a hub
// the relay has never seen with ErrNotFound.
func (s *Store) StartSMSVerification(ctx context.Context, v SMSVerification, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = hubExists(ctx, tx, v.HubID); err != nil {
		return err
	}
	if stopped, err := smsOptedOut(ctx, tx, v.NumberHash); err != nil {
		return err
	} else if stopped {
		return ErrOptedOut
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sms_verifications(hub_id,number_hash,number_ct,code_hash,expires_at,attempts,created_at)
	VALUES(?,?,?,?,?,0,?) ON CONFLICT(hub_id) DO UPDATE SET number_hash=excluded.number_hash,number_ct=excluded.number_ct,
	code_hash=excluded.code_hash,expires_at=excluded.expires_at,attempts=0,created_at=excluded.created_at`,
		v.HubID, v.NumberHash, v.NumberCT, v.CodeHash, millis(v.ExpiresAt), millis(now)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSMSVerification removes the hub's verification if it still holds
// codeHash, used when the code could not be texted.
func (s *Store) DeleteSMSVerification(ctx context.Context, hubID, codeHash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sms_verifications WHERE hub_id=? AND code_hash=?", hubID, codeHash)
	return err
}

// ConfirmSMS checks a code against the hub's verification. matches compares
// the stored code hash for the verification's number. A wrong code counts an
// attempt on the verification and on the number: the code is spent after
// maxAttempts wrong ones (ErrAttempts) or at its expiry (ErrExpired), and the
// number accepts no code at all after maxWrongPerNumber wrong ones in a day,
// across codes and hubs (ErrLocked). A right code makes the number the hub's
// active subscription and removes any other hub's subscription for it,
// because the hub has just proved it holds the phone. It returns the
// subscription and the hubs that lost the number.
func (s *Store) ConfirmSMS(ctx context.Context, hubID string, matches func(numberHash, codeHash string) bool, maxAttempts, maxWrongPerNumber int, now time.Time) (SMSSubscription, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SMSSubscription{}, nil, err
	}
	defer tx.Rollback()
	v, err := scanSMSVerification(tx.QueryRowContext(ctx, smsVerificationSelect+"WHERE hub_id=?", hubID))
	if err != nil {
		return SMSSubscription{}, nil, err
	}
	switch {
	case v.Attempts >= maxAttempts:
		return SMSSubscription{}, nil, ErrAttempts
	case !v.ExpiresAt.After(now):
		return SMSSubscription{}, nil, ErrExpired
	}
	wrongKey := WrongCodeKey(v.NumberHash)
	wrong, err := smsLimitCount(ctx, tx, wrongKey, now)
	if err != nil {
		return SMSSubscription{}, nil, err
	}
	if wrong >= maxWrongPerNumber {
		return SMSSubscription{}, nil, ErrLocked
	}
	if !matches(v.NumberHash, v.CodeHash) {
		if _, err = tx.ExecContext(ctx, "UPDATE sms_verifications SET attempts=attempts+1 WHERE hub_id=?", hubID); err != nil {
			return SMSSubscription{}, nil, err
		}
		if _, _, err = smsLimitTake(ctx, tx, wrongKey, maxWrongPerNumber, smsWrongCodeWindow, now); err != nil {
			return SMSSubscription{}, nil, err
		}
		if err = tx.Commit(); err != nil {
			return SMSSubscription{}, nil, err
		}
		if v.Attempts+1 >= maxAttempts {
			return SMSSubscription{}, nil, ErrAttempts
		}
		return SMSSubscription{}, nil, ErrForbidden
	}
	if stopped, err := smsOptedOut(ctx, tx, v.NumberHash); err != nil {
		return SMSSubscription{}, nil, err
	} else if stopped {
		return SMSSubscription{}, nil, ErrOptedOut
	}
	displaced, err := smsHubsWhere(ctx, tx, "sms_subscriptions", "number_hash=? AND hub_id<>?", v.NumberHash, hubID)
	if err != nil {
		return SMSSubscription{}, nil, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sms_subscriptions WHERE number_hash=? AND hub_id<>?", v.NumberHash, hubID); err != nil {
		return SMSSubscription{}, nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sms_subscriptions(hub_id,number_hash,number_ct,state,verified_at,updated_at) VALUES(?,?,?,?,?,?)
	ON CONFLICT(hub_id) DO UPDATE SET number_hash=excluded.number_hash,number_ct=excluded.number_ct,state=excluded.state,
	verified_at=excluded.verified_at,updated_at=excluded.updated_at`, hubID, v.NumberHash, v.NumberCT, SMSActive, millis(now), millis(now)); err != nil {
		return SMSSubscription{}, nil, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sms_verifications WHERE hub_id=?", hubID); err != nil {
		return SMSSubscription{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return SMSSubscription{}, nil, err
	}
	return SMSSubscription{HubID: hubID, NumberHash: v.NumberHash, NumberCT: v.NumberCT, State: SMSActive, VerifiedAt: now}, displaced, nil
}

// DeleteSMSSubscription removes the hub's subscription and verification.
func (s *Store) DeleteSMSSubscription(ctx context.Context, hubID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"sms_subscriptions", "sms_verifications"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE hub_id=?", hubID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReserveSMSSend counts one text against the hub's number before it is sent.
// Only an active subscription whose number has not opted out may send:
// ErrNotFound when there is none, ErrConflict when it is stopped (the
// subscription is returned), ErrOptedOut, and ErrQuota at either cap (the
// usage is returned). It returns the subscription, so the caller can open
// the number, and the usage after counting.
func (s *Store) ReserveSMSSend(ctx context.Context, hubID string, dayCap, monthCap int, now time.Time) (SMSSubscription, SMSUsage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SMSSubscription{}, SMSUsage{}, err
	}
	defer tx.Rollback()
	sub, err := scanSMSSubscription(tx.QueryRowContext(ctx, smsSubscriptionSelect+"WHERE hub_id=?", hubID))
	if err != nil {
		return SMSSubscription{}, SMSUsage{}, err
	}
	if sub.State != SMSActive {
		return sub, SMSUsage{}, ErrConflict
	}
	if stopped, err := smsOptedOut(ctx, tx, sub.NumberHash); err != nil {
		return SMSSubscription{}, SMSUsage{}, err
	} else if stopped {
		return sub, SMSUsage{}, ErrOptedOut
	}
	usage, err := smsUsage(ctx, tx, sub.NumberHash, now)
	if err != nil {
		return SMSSubscription{}, SMSUsage{}, err
	}
	if usage.DayCount >= dayCap || usage.MonthCount >= monthCap {
		return sub, usage, ErrQuota
	}
	usage.DayCount++
	usage.MonthCount++
	if _, err = tx.ExecContext(ctx, `INSERT INTO sms_usage(number_hash,day_key,day_count,month_key,month_count) VALUES(?,?,?,?,?)
	ON CONFLICT(number_hash) DO UPDATE SET day_key=excluded.day_key,day_count=excluded.day_count,month_key=excluded.month_key,month_count=excluded.month_count`, // gitleaks:allow
		sub.NumberHash, usage.DayKey, usage.DayCount, usage.MonthKey, usage.MonthCount); err != nil {
		return SMSSubscription{}, SMSUsage{}, err
	}
	return sub, usage, tx.Commit()
}

// RefundSMSSend returns one counted text the provider refused outright. A
// count from a period that has already rolled over is left alone.
func (s *Store) RefundSMSSend(ctx context.Context, numberHash string, usage SMSUsage) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sms_usage SET
	day_count=CASE WHEN day_key=? AND day_count>0 THEN day_count-1 ELSE day_count END,
	month_count=CASE WHEN month_key=? AND month_count>0 THEN month_count-1 ELSE month_count END
	WHERE number_hash=?`, usage.DayKey, usage.MonthKey, numberHash)
	return err
}

// SMSOptOut records a STOP from the number: its subscription becomes stopped
// and verifications for it are removed. Opt-outs are kept for good, including
// for numbers the relay never texted, because the number asked not to be
// texted again; a row is a hash and a time.
func (s *Store) SMSOptOut(ctx context.Context, numberHash string, now time.Time) (SMSOptChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SMSOptChange{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT INTO sms_optouts(number_hash,at) VALUES(?,?) ON CONFLICT(number_hash) DO NOTHING", numberHash, millis(now))
	if err != nil {
		return SMSOptChange{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return SMSOptChange{}, err
	}
	change := SMSOptChange{Changed: inserted == 1}
	active, err := smsHubsWhere(ctx, tx, "sms_subscriptions", "number_hash=? AND state=?", numberHash, SMSActive)
	if err != nil {
		return SMSOptChange{}, err
	}
	pending, err := smsHubsWhere(ctx, tx, "sms_verifications", "number_hash=?", numberHash)
	if err != nil {
		return SMSOptChange{}, err
	}
	stopped, err := smsHubsWhere(ctx, tx, "sms_subscriptions", "number_hash=? AND state=?", numberHash, SMSStopped)
	if err != nil {
		return SMSOptChange{}, err
	}
	change.Hubs = append(active, pending...)
	change.Known = len(change.Hubs)+len(stopped) > 0
	if _, err = tx.ExecContext(ctx, "DELETE FROM sms_verifications WHERE number_hash=?", numberHash); err != nil {
		return SMSOptChange{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE sms_subscriptions SET state=?,updated_at=? WHERE number_hash=? AND state=?", SMSStopped, millis(now), numberHash, SMSActive); err != nil {
		return SMSOptChange{}, err
	}
	return change, tx.Commit()
}

// SMSOptIn records a START from the number: the opt-out is lifted and a
// stopped subscription becomes active again.
func (s *Store) SMSOptIn(ctx context.Context, numberHash string, now time.Time) (SMSOptChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SMSOptChange{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM sms_optouts WHERE number_hash=?", numberHash)
	if err != nil {
		return SMSOptChange{}, err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return SMSOptChange{}, err
	}
	change := SMSOptChange{Changed: removed == 1}
	if change.Hubs, err = smsHubsWhere(ctx, tx, "sms_subscriptions", "number_hash=? AND state=?", numberHash, SMSStopped); err != nil {
		return SMSOptChange{}, err
	}
	change.Known = len(change.Hubs) > 0
	if _, err = tx.ExecContext(ctx, "UPDATE sms_subscriptions SET state=?,updated_at=? WHERE number_hash=? AND state=?", SMSActive, millis(now), numberHash, SMSStopped); err != nil {
		return SMSOptChange{}, err
	}
	return change, tx.Commit()
}

// smsHubsWhere lists hub ids from one of the two hub-keyed SMS tables.
func smsHubsWhere(ctx context.Context, tx *sql.Tx, table, where string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT hub_id FROM "+table+" WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hubs := []string{}
	for rows.Next() {
		var hubID string
		if err = rows.Scan(&hubID); err != nil {
			return nil, err
		}
		hubs = append(hubs, hubID)
	}
	return hubs, rows.Err()
}

// RecordSMSInbound remembers a provider message id hash. It reports false for
// one already seen, so a replayed or retried webhook is handled once.
func (s *Store) RecordSMSInbound(ctx context.Context, messageHash string, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, "INSERT INTO sms_inbound(message_hash,received_at) VALUES(?,?) ON CONFLICT(message_hash) DO NOTHING", messageHash, millis(now))
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	return inserted == 1, err
}

// ForgetSMSInbound drops a message id whose handling failed, so the
// provider's retry is handled instead of ignored.
func (s *Store) ForgetSMSInbound(ctx context.Context, messageHash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sms_inbound WHERE message_hash=?", messageHash)
	return err
}

// AddSMSReply queues one sealed reply for the hub until the hub acknowledges
// it or it expires. Only the newest KeptSMSReplies are kept.
func (s *Store) AddSMSReply(ctx context.Context, reply SMSReply, expires time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = hubExists(ctx, tx, reply.HubID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sms_replies(id,hub_id,body_ct,received_at,expires_at) VALUES(?,?,?,?,?)",
		reply.ID, reply.HubID, reply.BodyCT, millis(reply.ReceivedAt), millis(expires)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sms_replies WHERE hub_id=? AND id NOT IN
	(SELECT id FROM sms_replies WHERE hub_id=? ORDER BY received_at DESC, rowid DESC LIMIT ?)`, reply.HubID, reply.HubID, KeptSMSReplies); err != nil {
		return err
	}
	return tx.Commit()
}

// SMSRepliesOf lists the hub's unexpired queued replies, oldest first.
func (s *Store) SMSRepliesOf(ctx context.Context, hubID string, now time.Time, limit int) ([]SMSReply, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,hub_id,body_ct,received_at FROM sms_replies WHERE hub_id=? AND expires_at>? ORDER BY received_at, rowid LIMIT ?", hubID, millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	replies := []SMSReply{}
	for rows.Next() {
		var r SMSReply
		var received int64
		if err = rows.Scan(&r.ID, &r.HubID, &r.BodyCT, &received); err != nil {
			return nil, err
		}
		r.ReceivedAt = fromMillis(received)
		replies = append(replies, r)
	}
	return replies, rows.Err()
}

// AckSMSReply deletes a reply the hub has taken. Another hub's id is ignored.
func (s *Store) AckSMSReply(ctx context.Context, hubID, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sms_replies WHERE hub_id=? AND id=?", hubID, id)
	return err
}

// TakeSMSLimit counts one event against a fixed window that survives a
// restart. It reports false, and how long until the window ends, when the
// window already holds limit events; a refused event is not counted.
func (s *Store) TakeSMSLimit(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (bool, time.Duration, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	allowed, wait, err := smsLimitTake(ctx, tx, key, limit, window, now)
	if err != nil {
		return false, 0, err
	}
	return allowed, wait, tx.Commit()
}

// WrongCodeKey is the limit key of a number's wrong-code budget.
func WrongCodeKey(numberHash string) string { return "wrong:" + numberHash }

// SMSLimitWait is how long until the key's current window ends, or zero.
func (s *Store) SMSLimitWait(ctx context.Context, key string, now time.Time) (time.Duration, error) {
	var end int64
	err := s.db.QueryRowContext(ctx, "SELECT window_end FROM sms_limits WHERE key=? AND window_end>?", key, millis(now)).Scan(&end)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return fromMillis(end).Sub(now), nil
}

func smsLimitCount(ctx context.Context, q querier, key string, now time.Time) (int, error) {
	var count int
	err := q.QueryRowContext(ctx, "SELECT count FROM sms_limits WHERE key=? AND window_end>?", key, millis(now)).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return count, err
}

func smsLimitTake(ctx context.Context, tx *sql.Tx, key string, limit int, window time.Duration, now time.Time) (bool, time.Duration, error) {
	var end int64
	var count int
	err := tx.QueryRowContext(ctx, "SELECT window_end,count FROM sms_limits WHERE key=?", key).Scan(&end, &count)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && end <= millis(now)):
		_, err = tx.ExecContext(ctx, `INSERT INTO sms_limits(key,window_end,count) VALUES(?,?,1)
		ON CONFLICT(key) DO UPDATE SET window_end=excluded.window_end,count=1`, key, millis(now.Add(window)))
		return err == nil, 0, err
	case err != nil:
		return false, 0, err
	case count >= limit:
		return false, fromMillis(end).Sub(now), nil
	default:
		_, err = tx.ExecContext(ctx, "UPDATE sms_limits SET count=count+1 WHERE key=?", key)
		return err == nil, 0, err
	}
}

// sweepSMS removes expired replies, remembered message ids past
// SMSInboundKeep, verifications a day past their expiry, usage rows older
// than the previous month, and limit windows that have ended.
func sweepSMS(ctx context.Context, tx *sql.Tx, now time.Time) (int64, error) {
	firstOfMonth := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	steps := []struct {
		query string
		args  []any
	}{
		{"DELETE FROM sms_replies WHERE expires_at<=?", []any{millis(now)}},
		{"DELETE FROM sms_inbound WHERE received_at<=?", []any{millis(now.Add(-SMSInboundKeep))}},
		{"DELETE FROM sms_verifications WHERE expires_at<=?", []any{millis(now.Add(-smsVerificationKeep))}},
		{"DELETE FROM sms_usage WHERE month_key<?", []any{SMSMonthKey(firstOfMonth.AddDate(0, -1, 0))}},
		{"DELETE FROM sms_limits WHERE window_end<=?", []any{millis(now)}},
	}
	var total int64
	for _, step := range steps {
		result, err := tx.ExecContext(ctx, step.query, step.args...)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

// EnableSMS records that the hub turned text messages on. Enabling again
// keeps the first time. A hub the relay has never seen is ErrNotFound.
func (s *Store) EnableSMS(ctx context.Context, hubID string, now time.Time) error {
	if err := hubExists(ctx, s.db, hubID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO sms_enabled(hub_id,enabled_at) VALUES(?,?)", hubID, millis(now))
	return err
}

// SMSEntitled reports whether the hub may ask for codes and send texts: it
// turned text messages on, or it has a paired phone whose bearer has not
// expired.
func (s *Store) SMSEntitled(ctx context.Context, hubID string, now time.Time) (bool, error) {
	var entitled bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sms_enabled WHERE hub_id=?)
	OR EXISTS(SELECT 1 FROM devices WHERE hub_id=? AND expires_at>?)`, hubID, hubID, millis(now)).Scan(&entitled)
	return entitled, err
}

// Kinds of SMSSealedValue.
const (
	SMSSealedSubscription = "subscription"
	SMSSealedVerification = "verification"
	SMSSealedReply        = "reply"
)

// SMSSealedValue is one ciphertext the store holds: a subscription's or a
// verification's number, or a queued reply. ID is the reply id.
type SMSSealedValue struct {
	Kind  string
	HubID string
	ID    string
	CT    string
}

// SMSSealedValues lists every ciphertext the store holds, so the keys can be
// rotated.
func (s *Store) SMSSealedValues(ctx context.Context) ([]SMSSealedValue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT 'subscription',hub_id,'',number_ct FROM sms_subscriptions
	UNION ALL SELECT 'verification',hub_id,'',number_ct FROM sms_verifications
	UNION ALL SELECT 'reply',hub_id,id,body_ct FROM sms_replies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []SMSSealedValue{}
	for rows.Next() {
		var v SMSSealedValue
		if err = rows.Scan(&v.Kind, &v.HubID, &v.ID, &v.CT); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

// ReplaceSMSSealed swaps one ciphertext for ct when the row still holds the
// listed one; a row changed or removed since is left as it is.
func (s *Store) ReplaceSMSSealed(ctx context.Context, v SMSSealedValue, ct string) error {
	var err error
	switch v.Kind {
	case SMSSealedSubscription:
		_, err = s.db.ExecContext(ctx, "UPDATE sms_subscriptions SET number_ct=? WHERE hub_id=? AND number_ct=?", ct, v.HubID, v.CT)
	case SMSSealedVerification:
		_, err = s.db.ExecContext(ctx, "UPDATE sms_verifications SET number_ct=? WHERE hub_id=? AND number_ct=?", ct, v.HubID, v.CT)
	case SMSSealedReply:
		_, err = s.db.ExecContext(ctx, "UPDATE sms_replies SET body_ct=? WHERE hub_id=? AND id=? AND body_ct=?", ct, v.HubID, v.ID, v.CT)
	default:
		err = errors.New("unknown sealed value kind")
	}
	return err
}
