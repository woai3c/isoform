package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"isoform-relay/internal/wire"
)

// Pairing states. open -> claimed -> approved | declined; expiry is by time.
const (
	stateOpen     = "open"
	stateClaimed  = "claimed"
	stateApproved = "approved"
	stateDeclined = "declined"
	stateExpired  = "expired"
)

// Claim is a device's claim on an open pairing.
type Claim struct {
	Hub       string
	TokenHash string
	DeviceID  string
	DevicePub string
	CT        string
}

// PendingClaim is a claimed pairing the hub has not yet answered.
type PendingClaim struct {
	PairingID string
	DeviceID  string
	DevicePub string
	CT        string
}

// Approval creates a device and stores the encrypted approval for the phone.
type Approval struct {
	PairingID  string
	DeviceID   string
	BearerHash string
	Scopes     []string
	ExpiresAt  time.Time
	CT         string
}

// PairingStatus is what a device sees when it polls its pairing.
type PairingStatus struct {
	State      string
	ApprovalCT string
}

// OpenPairing records a new invitation and closes any other open one of the hub.
func (s *Store) OpenPairing(ctx context.Context, hubID, pairingID, claimHash string, expiresAt, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM pairings WHERE id=?", pairingID).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM pairings WHERE hub_id=? AND state=?", hubID, stateOpen); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO pairings(id,hub_id,claim_hash,state,expires_at,created_at) VALUES(?,?,?,?,?,?)", pairingID, hubID, claimHash, stateOpen, millis(expiresAt), millis(now)); err != nil {
		return err
	}
	return tx.Commit()
}

// ClosePairing cancels an open pairing. Other states are left unchanged.
func (s *Store) ClosePairing(ctx context.Context, hubID, pairingID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM pairings WHERE id=? AND hub_id=? AND state=?", pairingID, hubID, stateOpen)
	return err
}

// ClaimPairing stores a device's claim after checking the claim token hash in
// constant time. Repeating the identical claim succeeds without change. It
// returns the hub that owns the pairing so the caller can forward the claim.
func (s *Store) ClaimPairing(ctx context.Context, pairingID string, c Claim, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var hubID, storedHash, state, deviceID, devicePub, claimCT string
	var expires int64
	err = tx.QueryRowContext(ctx, "SELECT hub_id,claim_hash,state,expires_at,device_id,device_pub,claim_ct FROM pairings WHERE id=?", pairingID).Scan(&hubID, &storedHash, &state, &expires, &deviceID, &devicePub, &claimCT)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !sameDigest(storedHash, c.TokenHash) {
		return "", ErrForbidden
	}
	if hubID != c.Hub {
		return "", ErrNotFound
	}
	if state == stateClaimed && deviceID == c.DeviceID && devicePub == c.DevicePub && claimCT == c.CT {
		return hubID, nil
	}
	if state != stateOpen {
		return "", ErrConflict
	}
	if expires <= millis(now) {
		return "", ErrExpired
	}
	if _, err = tx.ExecContext(ctx, "UPDATE pairings SET state=?,device_id=?,device_pub=?,claim_ct=? WHERE id=? AND state=?", stateClaimed, c.DeviceID, c.DevicePub, c.CT, pairingID, stateOpen); err != nil {
		return "", err
	}
	return hubID, tx.Commit()
}

// PollPairing reports a pairing's state to the device that holds its claim
// token. An open pairing has not been claimed, so polling it is a conflict.
func (s *Store) PollPairing(ctx context.Context, pairingID, tokenHash string, now time.Time) (PairingStatus, error) {
	var storedHash, state, approval string
	var expires int64
	err := s.db.QueryRowContext(ctx, "SELECT claim_hash,state,expires_at,approval_ct FROM pairings WHERE id=?", pairingID).Scan(&storedHash, &state, &expires, &approval)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingStatus{}, ErrNotFound
	}
	if err != nil {
		return PairingStatus{}, err
	}
	if !sameDigest(storedHash, tokenHash) {
		return PairingStatus{}, ErrForbidden
	}
	switch state {
	case stateOpen:
		return PairingStatus{}, ErrConflict
	case stateClaimed:
		if expires <= millis(now) {
			state = stateExpired
		}
	case stateApproved:
		return PairingStatus{State: state, ApprovalCT: approval}, nil
	}
	return PairingStatus{State: state}, nil
}

// ConsumeApproval deletes the approval ciphertext once it has been served.
func (s *Store) ConsumeApproval(ctx context.Context, pairingID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE pairings SET approval_ct='' WHERE id=? AND state=?", pairingID, stateApproved)
	return err
}

// ApprovePairing creates the device record and stores the approval. The
// pairing must be claimed by the same device and not yet expired.
func (s *Store) ApprovePairing(ctx context.Context, hubID string, a Approval, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ownerHub, state, claimedDevice string
	var expires int64
	err = tx.QueryRowContext(ctx, "SELECT hub_id,state,expires_at,device_id FROM pairings WHERE id=?", a.PairingID).Scan(&ownerHub, &state, &expires, &claimedDevice)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && ownerHub != hubID) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != stateClaimed || claimedDevice != a.DeviceID {
		return ErrConflict
	}
	if expires <= millis(now) {
		return ErrExpired
	}
	if err = insertDevice(ctx, tx, hubID, Device{ID: a.DeviceID, BearerHash: a.BearerHash, Scopes: a.Scopes, ExpiresAt: a.ExpiresAt}, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE pairings SET state=?,approval_ct=?,claim_ct='' WHERE id=?", stateApproved, a.CT, a.PairingID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// DeclinePairing ends a claimed pairing without creating a device.
func (s *Store) DeclinePairing(ctx context.Context, hubID, pairingID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ownerHub, state string
	err = tx.QueryRowContext(ctx, "SELECT hub_id,state FROM pairings WHERE id=?", pairingID).Scan(&ownerHub, &state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && ownerHub != hubID) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != stateClaimed {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "UPDATE pairings SET state=?,claim_ct='' WHERE id=?", stateDeclined, pairingID); err != nil {
		return err
	}
	return tx.Commit()
}

// ClaimedPairings lists the hub's unanswered claims that have not expired.
func (s *Store) ClaimedPairings(ctx context.Context, hubID string, now time.Time) ([]PendingClaim, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,device_id,device_pub,claim_ct FROM pairings WHERE hub_id=? AND state=? AND expires_at>? ORDER BY created_at,id", hubID, stateClaimed, millis(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := []PendingClaim{}
	for rows.Next() {
		var c PendingClaim
		if err = rows.Scan(&c.PairingID, &c.DeviceID, &c.DevicePub, &c.CT); err != nil {
			return nil, err
		}
		claims = append(claims, c)
	}
	return claims, rows.Err()
}

// scopesJSON encodes scopes as a JSON array, never null.
func scopesJSON(scopes []string) (string, error) {
	if scopes == nil {
		scopes = []string{}
	}
	raw, err := json.Marshal(scopes)
	if err != nil || !wire.ValidScopes(scopes) {
		return "", ErrConflict
	}
	return string(raw), nil
}
