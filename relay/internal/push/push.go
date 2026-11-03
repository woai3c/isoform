// Package push stores APNs registrations and the outbox of notifications.
// Credentials and tokens stay inside this package and the APNs worker. Rows
// are fenced by a generation counter so stale workers cannot finish a claim.
package push

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxDevicesPerHub caps enabled push registrations for one hub.
	MaxDevicesPerHub = 10
	// MaxAttempts is the number of sends before a notification is exhausted.
	MaxAttempts = 5
	// RetryBase is the first retry delay; each later retry doubles it.
	RetryBase = 15 * time.Minute
	// Expiry bounds how long a notification may stay queued.
	Expiry = 24 * time.Hour
)

// Push kinds say what a notification announces. The kind is stored with each
// queued notification and selects its alert text and APNs interruption level.
const (
	// KindBrief announces that a new brief is stored.
	KindBrief = "brief"
	// KindNeedsYou announces that requests are waiting for the user.
	KindNeedsYou = "needs_you"
	// KindMoment carries a sealed notice. Its text is decrypted on the phone;
	// the relay sends the fixed fallback text for its moment kind (PROTOCOL.md 14).
	KindMoment = "moment"
)

var (
	// ErrCapacity reports that the hub already has MaxDevicesPerHub registrations.
	ErrCapacity = errors.New("push device limit reached for this hub")
	// ErrLeaseLost reports that a claim no longer owns its outbox row.
	ErrLeaseLost = errors.New("push claim is no longer current")
	// ErrUnknownKind reports a push kind this relay does not send.
	ErrUnknownKind = errors.New("push kind is not recognized")
)

// Configuration describes the APNs environment this relay serves. An empty
// Environment means APNs is not configured.
type Configuration struct {
	Environment string
	Gateway     bool
}

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Register stores the device's APNs token. A token identifies one app install,
// so a new holder takes it over and its previous registration stops. Changing
// the token cancels this device's pending sends; re-registering the same token
// keeps them. It returns the registration time.
func Register(ctx context.Context, db *sql.DB, hubID, deviceID, token, environment string, now time.Time) (time.Time, error) {
	return RegisterWithGatewayGrant(ctx, db, hubID, deviceID, token, environment, "", now)
}

// RegisterWithGatewayGrant stores a token and its optional push-gateway grant.
func RegisterWithGatewayGrant(ctx context.Context, db *sql.DB, hubID, deviceID, token, environment, gatewayGrant string, now time.Time) (time.Time, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	// A token that is moving to this device does not count against the cap:
	// its previous holder's registration ends with the move.
	var others int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM push_devices p JOIN devices d ON d.id=p.device_id
	WHERE d.hub_id=? AND p.device_id<>? AND NOT (p.token=? AND p.environment=?)`, hubID, deviceID, token, environment).Scan(&others); err != nil {
		return time.Time{}, err
	}
	if others >= MaxDevicesPerHub {
		return time.Time{}, ErrCapacity
	}
	if err = cancelWork(ctx, tx, "device_id IN (SELECT device_id FROM push_devices WHERE token=? AND environment=? AND device_id<>?)", token, environment, deviceID); err != nil {
		return time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM push_devices WHERE token=? AND environment=? AND device_id<>?", token, environment, deviceID); err != nil {
		return time.Time{}, err
	}
	var oldToken, oldEnvironment string
	err = tx.QueryRowContext(ctx, "SELECT token,environment FROM push_devices WHERE device_id=?", deviceID).Scan(&oldToken, &oldEnvironment)
	switch {
	case err == nil:
		if oldToken != token || oldEnvironment != environment {
			if err = cancelWork(ctx, tx, "device_id=?", deviceID); err != nil {
				return time.Time{}, err
			}
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return time.Time{}, err
	}
	now = now.UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO push_devices(device_id,token,environment,gateway_grant,registered_at) VALUES(?,?,?,?,?)
	ON CONFLICT(device_id) DO UPDATE SET token=excluded.token,environment=excluded.environment,gateway_grant=excluded.gateway_grant,registered_at=excluded.registered_at`, deviceID, token, environment, gatewayGrant, now.UnixMilli())
	if err != nil {
		return time.Time{}, err
	}
	return now, tx.Commit()
}

// Remove deletes the device's registration and cancels its pending sends.
func Remove(ctx context.Context, db *sql.DB, deviceID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = cancelWork(ctx, tx, "device_id=?", deviceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM push_devices WHERE device_id=?", deviceID); err != nil {
		return err
	}
	return tx.Commit()
}

// Enqueue queues one notification of the given kind for an event on a device
// with a registration. It is a no-op for devices without one. Repeating an
// event queues nothing new.
func Enqueue(ctx context.Context, q Querier, deviceID, eventID, kind string, now time.Time) error {
	switch kind {
	case KindBrief, KindNeedsYou:
	default:
		return ErrUnknownKind
	}
	var registered int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM push_devices WHERE device_id=?", deviceID).Scan(&registered); err != nil {
		return err
	}
	if registered == 0 {
		return nil
	}
	_, err := q.ExecContext(ctx, `INSERT INTO push_outbox(device_id,event_id,notification_id,kind,state,created_at,expires_at,next_at)
	VALUES(?,?,?,?,'queued',?,?,?) ON CONFLICT(device_id,event_id) DO NOTHING`, deviceID, eventID, uuid.NewString(), kind, now.UnixMilli(), now.Add(Expiry).UnixMilli(), now.UnixMilli())
	return err
}

// Sweep deletes finished outbox rows created before cutoff.
func Sweep(ctx context.Context, q Querier, cutoff time.Time) (int64, error) {
	result, err := q.ExecContext(ctx, "DELETE FROM push_outbox WHERE state IN ('accepted','permanent','exhausted','cancelled') AND created_at<=?", cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// cancelWork ends queued and in-flight sends matching a fixed predicate.
func cancelWork(ctx context.Context, q Querier, predicate string, args ...any) error {
	_, err := q.ExecContext(ctx, "UPDATE push_outbox SET state='cancelled',lease_until=0,generation=generation+1 WHERE state IN ('queued','inflight') AND "+predicate, args...)
	return err
}
