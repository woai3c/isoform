package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"
)

// AuthorizeHub creates a hub on first sight with its credential hash, then
// requires later connections to present the same hash.
func (s *Store) AuthorizeHub(ctx context.Context, hubID, credentialHash string, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO hubs(id,credential_hash,last_seen_at,created_at) VALUES(?,?,0,?)", hubID, credentialHash, millis(now)); err != nil {
		return err
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, "SELECT credential_hash FROM hubs WHERE id=?", hubID).Scan(&stored); err != nil {
		return err
	}
	if !sameDigest(stored, credentialHash) {
		return ErrUnauthorized
	}
	return nil
}

// TouchHub records that the hub was seen at now.
func (s *Store) TouchHub(ctx context.Context, hubID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, "UPDATE hubs SET last_seen_at=? WHERE id=?", millis(now), hubID)
	return err
}

// HubLastSeen returns the last time the hub was seen, or the zero time.
func (s *Store) HubLastSeen(ctx context.Context, hubID string) (time.Time, error) {
	var seen int64
	err := s.db.QueryRowContext(ctx, "SELECT last_seen_at FROM hubs WHERE id=?", hubID).Scan(&seen)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil || seen == 0 {
		return time.Time{}, err
	}
	return fromMillis(seen), nil
}

// SetHubGoodbye stores only the authenticated hub's bounded lifecycle state.
func (s *Store) SetHubGoodbye(ctx context.Context, hubID, cause, reason string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO hub_goodbyes(hub_id,cause,reason,at) VALUES(?,?,?,?)
		ON CONFLICT(hub_id) DO UPDATE SET cause=excluded.cause,reason=excluded.reason,at=excluded.at`, hubID, cause, reason, millis(now))
	return err
}

func (s *Store) ClearHubGoodbye(ctx context.Context, hubID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM hub_goodbyes WHERE hub_id=?", hubID)
	return err
}

// HubGoodbye returns the stored goodbye: its cause, its optional reason and when it was said.
func (s *Store) HubGoodbye(ctx context.Context, hubID string) (string, string, time.Time, error) {
	var cause, reason string
	var at int64
	err := s.db.QueryRowContext(ctx, "SELECT cause,reason,at FROM hub_goodbyes WHERE hub_id=?", hubID).Scan(&cause, &reason, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, nil
	}
	if err != nil {
		return "", "", time.Time{}, err
	}
	return cause, reason, fromMillis(at), nil
}

// sameDigest compares two hex digests in constant time.
func sameDigest(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
