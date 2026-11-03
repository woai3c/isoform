package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Email (PROTOCOL.md section 19): a hub may verify one address of its own to
// receive the results it chose to have emailed. Like a phone number, the
// address is kept as a keyed hash and as ciphertext under the relay's email
// key, never in the clear, and no message is stored. Hashing and sealing
// happen in package email; this file stores opaque values. Limits reuse the
// stored windows of the text channel (TakeSMSLimit) under "email:" keys.

// emailVerificationKeep is how long an expired email code is kept.
const emailVerificationKeep = 24 * time.Hour

const emailSchema = `
CREATE TABLE IF NOT EXISTS email_subscriptions (
	hub_id TEXT PRIMARY KEY REFERENCES hubs(id) ON DELETE CASCADE,
	address_hash TEXT NOT NULL,
	address_ct TEXT NOT NULL,
	verified_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS email_verifications (
	hub_id TEXT PRIMARY KEY REFERENCES hubs(id) ON DELETE CASCADE,
	address_hash TEXT NOT NULL,
	address_ct TEXT NOT NULL,
	code_hash TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS email_usage (
	hub_id TEXT PRIMARY KEY REFERENCES hubs(id) ON DELETE CASCADE,
	day_key TEXT NOT NULL,
	day_count INTEGER NOT NULL
);
`

// EmailSubscription is one hub's verified address.
type EmailSubscription struct {
	HubID       string
	AddressHash string
	AddressCT   string
	VerifiedAt  time.Time
}

// EmailVerification is a code mailed to an address and waiting for the hub.
type EmailVerification struct {
	HubID       string
	AddressHash string
	AddressCT   string
	CodeHash    string
	ExpiresAt   time.Time
	Attempts    int
}

// EmailUsage is a hub's emails in the current UTC day.
type EmailUsage struct {
	DayKey   string
	DayCount int
}

// EmailSubscriptionOf returns the hub's verified address, or ErrNotFound.
func (s *Store) EmailSubscriptionOf(ctx context.Context, hubID string) (EmailSubscription, error) {
	var sub EmailSubscription
	var verified int64
	err := s.db.QueryRowContext(ctx, "SELECT hub_id,address_hash,address_ct,verified_at FROM email_subscriptions WHERE hub_id=?", hubID).
		Scan(&sub.HubID, &sub.AddressHash, &sub.AddressCT, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailSubscription{}, ErrNotFound
	}
	if err != nil {
		return EmailSubscription{}, err
	}
	sub.VerifiedAt = fromMillis(verified)
	return sub, nil
}

// EmailVerificationOf returns the hub's open verification, or ErrNotFound.
func (s *Store) EmailVerificationOf(ctx context.Context, hubID string) (EmailVerification, error) {
	return scanEmailVerification(s.db.QueryRowContext(ctx,
		"SELECT hub_id,address_hash,address_ct,code_hash,expires_at,attempts FROM email_verifications WHERE hub_id=?", hubID))
}

func scanEmailVerification(row *sql.Row) (EmailVerification, error) {
	var v EmailVerification
	var expires int64
	err := row.Scan(&v.HubID, &v.AddressHash, &v.AddressCT, &v.CodeHash, &expires, &v.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailVerification{}, ErrNotFound
	}
	if err != nil {
		return EmailVerification{}, err
	}
	v.ExpiresAt = fromMillis(expires)
	return v, nil
}

// EmailUsageOf returns the hub's count for the day of now.
func (s *Store) EmailUsageOf(ctx context.Context, hubID string, now time.Time) (EmailUsage, error) {
	return emailUsage(ctx, s.db, hubID, now)
}

func emailUsage(ctx context.Context, q querier, hubID string, now time.Time) (EmailUsage, error) {
	day := now.UTC().Format("2006-01-02")
	usage := EmailUsage{DayKey: day}
	var key string
	var count int
	err := q.QueryRowContext(ctx, "SELECT day_key,day_count FROM email_usage WHERE hub_id=?", hubID).Scan(&key, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return usage, nil
	}
	if err != nil {
		return EmailUsage{}, err
	}
	if key == day {
		usage.DayCount = count
	}
	return usage, nil
}

// StartEmailVerification stores a code hash for the address as the hub's
// only verification, replacing an earlier one. The hub's subscription is left
// as it is, so a new address takes over only once confirmed.
func (s *Store) StartEmailVerification(ctx context.Context, v EmailVerification, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = hubExists(ctx, tx, v.HubID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_verifications(hub_id,address_hash,address_ct,code_hash,expires_at,attempts,created_at)
	VALUES(?,?,?,?,?,0,?) ON CONFLICT(hub_id) DO UPDATE SET address_hash=excluded.address_hash,address_ct=excluded.address_ct,
	code_hash=excluded.code_hash,expires_at=excluded.expires_at,attempts=0,created_at=excluded.created_at`,
		v.HubID, v.AddressHash, v.AddressCT, v.CodeHash, millis(v.ExpiresAt), millis(now)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteEmailVerification removes the hub's verification if it still holds
// codeHash, used when the code could not be mailed.
func (s *Store) DeleteEmailVerification(ctx context.Context, hubID, codeHash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM email_verifications WHERE hub_id=? AND code_hash=?", hubID, codeHash)
	return err
}

// ConfirmEmail checks a code against the hub's verification. A wrong code
// counts an attempt; the code is spent after maxAttempts wrong ones
// (ErrAttempts) or at its expiry (ErrExpired). A right code makes the
// address the hub's subscription.
func (s *Store) ConfirmEmail(ctx context.Context, hubID string, matches func(addressHash, codeHash string) bool, maxAttempts int, now time.Time) (EmailSubscription, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EmailSubscription{}, err
	}
	defer tx.Rollback()
	v, err := scanEmailVerification(tx.QueryRowContext(ctx,
		"SELECT hub_id,address_hash,address_ct,code_hash,expires_at,attempts FROM email_verifications WHERE hub_id=?", hubID))
	if err != nil {
		return EmailSubscription{}, err
	}
	switch {
	case v.Attempts >= maxAttempts:
		return EmailSubscription{}, ErrAttempts
	case !v.ExpiresAt.After(now):
		return EmailSubscription{}, ErrExpired
	}
	if !matches(v.AddressHash, v.CodeHash) {
		if _, err = tx.ExecContext(ctx, "UPDATE email_verifications SET attempts=attempts+1 WHERE hub_id=?", hubID); err != nil {
			return EmailSubscription{}, err
		}
		if err = tx.Commit(); err != nil {
			return EmailSubscription{}, err
		}
		if v.Attempts+1 >= maxAttempts {
			return EmailSubscription{}, ErrAttempts
		}
		return EmailSubscription{}, ErrForbidden
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_subscriptions(hub_id,address_hash,address_ct,verified_at,updated_at) VALUES(?,?,?,?,?)
	ON CONFLICT(hub_id) DO UPDATE SET address_hash=excluded.address_hash,address_ct=excluded.address_ct,
	verified_at=excluded.verified_at,updated_at=excluded.updated_at`, hubID, v.AddressHash, v.AddressCT, millis(now), millis(now)); err != nil {
		return EmailSubscription{}, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM email_verifications WHERE hub_id=?", hubID); err != nil {
		return EmailSubscription{}, err
	}
	if err = tx.Commit(); err != nil {
		return EmailSubscription{}, err
	}
	return EmailSubscription{HubID: hubID, AddressHash: v.AddressHash, AddressCT: v.AddressCT, VerifiedAt: now}, nil
}

// DeleteEmailSubscription removes the hub's subscription and verification.
func (s *Store) DeleteEmailSubscription(ctx context.Context, hubID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"email_subscriptions", "email_verifications"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE hub_id=?", hubID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReserveEmailSend counts one email against the hub's daily cap before it is
// sent: ErrNotFound without a subscription, ErrQuota at the cap (the usage is
// returned). It returns the subscription, so the caller can open the address.
func (s *Store) ReserveEmailSend(ctx context.Context, hubID string, dayCap int, now time.Time) (EmailSubscription, EmailUsage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EmailSubscription{}, EmailUsage{}, err
	}
	defer tx.Rollback()
	var sub EmailSubscription
	var verified int64
	err = tx.QueryRowContext(ctx, "SELECT hub_id,address_hash,address_ct,verified_at FROM email_subscriptions WHERE hub_id=?", hubID).
		Scan(&sub.HubID, &sub.AddressHash, &sub.AddressCT, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailSubscription{}, EmailUsage{}, ErrNotFound
	}
	if err != nil {
		return EmailSubscription{}, EmailUsage{}, err
	}
	sub.VerifiedAt = fromMillis(verified)
	usage, err := emailUsage(ctx, tx, hubID, now)
	if err != nil {
		return EmailSubscription{}, EmailUsage{}, err
	}
	if usage.DayCount >= dayCap {
		return sub, usage, ErrQuota
	}
	usage.DayCount++
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_usage(hub_id,day_key,day_count) VALUES(?,?,?)
	ON CONFLICT(hub_id) DO UPDATE SET day_key=excluded.day_key,day_count=excluded.day_count`, hubID, usage.DayKey, usage.DayCount); err != nil {
		return EmailSubscription{}, EmailUsage{}, err
	}
	return sub, usage, tx.Commit()
}

// RefundEmailSend returns one counted email the provider refused outright.
func (s *Store) RefundEmailSend(ctx context.Context, hubID string, usage EmailUsage) error {
	_, err := s.db.ExecContext(ctx, `UPDATE email_usage SET
	day_count=CASE WHEN day_key=? AND day_count>0 THEN day_count-1 ELSE day_count END WHERE hub_id=?`, usage.DayKey, hubID)
	return err
}
