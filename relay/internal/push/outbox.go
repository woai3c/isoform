package push

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Outcome is the classified result of one provider attempt.
type Outcome string

const (
	// Accepted means the provider accepted the notification (not delivery).
	Accepted Outcome = "accepted"
	// Retry means the attempt failed in a way that may succeed later.
	Retry Outcome = "retry"
	// Permanent means the notification must not be sent again.
	Permanent Outcome = "permanent"
	// TokenInvalid means the provider reported the token as dead.
	TokenInvalid Outcome = "tokeninvalid"
)

// Claim is a leased notification. Only a claim with the current generation
// may validate or finish its row. Its token is never serialized.
type Claim struct {
	ID             int64
	Generation     int64
	HubID          string
	DeviceID       string
	EventID        string
	NotificationID string
	Kind           string
	Notice         Notice
	Token          string
	Environment    string
	GatewayGrant   string
	Attempts       int
	RegisteredAt   time.Time
	ExpiresAt      time.Time
}

// ClaimNext leases the next due notification, or returns nil when none is due.
// Expired work is cancelled, and abandoned leases are requeued or exhausted.
func ClaimNext(ctx context.Context, db *sql.DB, now time.Time, lease time.Duration) (*Claim, error) {
	if lease <= 0 || lease > 5*time.Minute {
		return nil, errors.New("push lease must be positive and at most five minutes")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	n := now.UnixMilli()
	if _, err = tx.ExecContext(ctx, "UPDATE push_outbox SET state='cancelled',lease_until=0,generation=generation+1 WHERE state IN ('queued','inflight') AND expires_at<=?", n); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE push_outbox SET state='exhausted',lease_until=0,generation=generation+1 WHERE state='inflight' AND lease_until<=? AND attempts>=?", n, MaxAttempts); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE push_outbox SET state='queued',lease_until=0,next_at=?,generation=generation+1 WHERE state='inflight' AND lease_until<=? AND attempts<?", now.Add(RetryBase).UnixMilli(), n, MaxAttempts); err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM push_outbox WHERE state='queued' AND next_at<=? AND attempts<? ORDER BY next_at,id LIMIT 1", n, MaxAttempts).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE push_outbox SET state='inflight',attempts=attempts+1,generation=generation+1,lease_until=min(?,expires_at) WHERE id=?", now.Add(lease).UnixMilli(), id); err != nil {
		return nil, err
	}
	claim, err := readClaim(ctx, tx, id, now)
	if err != nil {
		return nil, err
	}
	return &claim, tx.Commit()
}

// ValidateClaim confirms the claim still owns its lease. Call it immediately
// before a send. It cannot recall a request that is already in flight.
func ValidateClaim(ctx context.Context, db *sql.DB, claim Claim, now time.Time) error {
	current, err := readClaim(ctx, db, claim.ID, now)
	if err != nil {
		return err
	}
	if current.Generation != claim.Generation {
		return ErrLeaseLost
	}
	return nil
}

// FinishClaim records an attempt's outcome if the claim is still current.
func FinishClaim(ctx context.Context, db *sql.DB, claim Claim, outcome Outcome, now time.Time) error {
	switch outcome {
	case Accepted, Retry, Permanent, TokenInvalid:
	default:
		return errors.New("invalid push outcome")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := readClaim(ctx, tx, claim.ID, now)
	if err != nil {
		return err
	}
	if current.Generation != claim.Generation {
		return ErrLeaseLost
	}
	state, next := string(outcome), now
	switch outcome {
	case Retry:
		state = "exhausted"
		if claim.Attempts < MaxAttempts {
			state = "queued"
			next = now.Add(RetryBase * time.Duration(1<<(claim.Attempts-1)))
		}
	case TokenInvalid:
		state = "permanent"
	}
	if _, err = tx.ExecContext(ctx, "UPDATE push_outbox SET state=?,next_at=?,lease_until=0,generation=generation+1 WHERE id=?", state, next.UnixMilli(), claim.ID); err != nil {
		return err
	}
	if outcome == TokenInvalid {
		// Remove only the registration used by this claim.
		result, deleteErr := tx.ExecContext(ctx, "DELETE FROM push_devices WHERE device_id=? AND token=? AND gateway_grant=?",
			claim.DeviceID, claim.Token, claim.GatewayGrant)
		if deleteErr != nil {
			return deleteErr
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted > 0 {
			if err = cancelWork(ctx, tx, "device_id=?", claim.DeviceID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// readClaim loads a leased row that is still inflight and unexpired.
func readClaim(ctx context.Context, q Querier, id int64, now time.Time) (Claim, error) {
	var claim Claim
	var registered, expires int64
	err := q.QueryRowContext(ctx, `SELECT p.id,p.generation,d.hub_id,p.device_id,p.event_id,p.notification_id,p.kind,p.moment_kind,p.notice_id,p.ct,p.category,p.collapse_id,p.level,pd.token,pd.environment,pd.gateway_grant,p.attempts,pd.registered_at,p.expires_at
	FROM push_outbox p JOIN push_devices pd ON pd.device_id=p.device_id JOIN devices d ON d.id=p.device_id
	WHERE p.id=? AND p.state='inflight' AND p.lease_until>? AND p.expires_at>?`, id, now.UnixMilli(), now.UnixMilli()).Scan(&claim.ID, &claim.Generation, &claim.HubID, &claim.DeviceID, &claim.EventID, &claim.NotificationID, &claim.Kind,
		&claim.Notice.Moment, &claim.Notice.ID, &claim.Notice.CT, &claim.Notice.Category, &claim.Notice.Collapse, &claim.Notice.Level,
		&claim.Token, &claim.Environment, &claim.GatewayGrant, &claim.Attempts, &registered, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrLeaseLost
	}
	if err != nil {
		return Claim{}, err
	}
	claim.RegisteredAt = time.UnixMilli(registered).UTC()
	claim.ExpiresAt = time.UnixMilli(expires).UTC()
	return claim, nil
}
