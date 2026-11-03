package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"isoform-relay/internal/push"
	"isoform-relay/internal/wire"
)

// Brief is a stored encrypted brief. Version is the envelope sequence number.
type Brief struct {
	Version uint64
	AsOf    string
	Body    []byte
}

// PutBrief stores a brief for a device of the hub and keeps the newest
// KeptBriefs. With notify it queues one push for the brief, once.
func (s *Store) PutBrief(ctx context.Context, hubID string, env wire.Envelope, body []byte, asOf string, notify bool, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = deviceInHub(ctx, tx, hubID, env.Dev); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO briefs(device_id,version,as_of,body,stored_at) VALUES(?,?,?,?,?)", env.Dev, int64(env.Seq), asOf, string(body), millis(now))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 && notify {
		if err = push.Enqueue(ctx, tx, env.Dev, strconv.FormatUint(env.Seq, 10), push.KindBrief, now); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM briefs WHERE device_id=? AND version NOT IN (SELECT version FROM briefs WHERE device_id=? ORDER BY version DESC LIMIT ?)", env.Dev, env.Dev, KeptBriefs); err != nil {
		return err
	}
	return tx.Commit()
}

// LatestBrief returns the device's newest brief, or ErrNotFound.
func (s *Store) LatestBrief(ctx context.Context, deviceID string) (Brief, error) {
	briefs, err := s.ListBriefs(ctx, deviceID, 1)
	if err != nil {
		return Brief{}, err
	}
	if len(briefs) == 0 {
		return Brief{}, ErrNotFound
	}
	return briefs[0], nil
}

// ListBriefs returns up to limit briefs, newest first.
func (s *Store) ListBriefs(ctx context.Context, deviceID string, limit int) ([]Brief, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT version,as_of,body FROM briefs WHERE device_id=? ORDER BY version DESC LIMIT ?", deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	briefs := []Brief{}
	for rows.Next() {
		var version int64
		var b Brief
		var body string
		if err = rows.Scan(&version, &b.AsOf, &body); err != nil {
			return nil, err
		}
		b.Version = uint64(version)
		b.Body = []byte(body)
		briefs = append(briefs, b)
	}
	return briefs, rows.Err()
}

// deviceHub returns the hub that owns a device.
func deviceHub(ctx context.Context, q querier, deviceID string) (string, error) {
	var hubID string
	err := q.QueryRowContext(ctx, "SELECT hub_id FROM devices WHERE id=?", deviceID).Scan(&hubID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return hubID, err
}
