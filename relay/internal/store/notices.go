package store

import (
	"context"
	"time"
)

// NoticeReceiptKeep bounds how long receipts are kept. Receipts are measurements
// and carry no content, so they expire sooner than briefs.
const NoticeReceiptKeep = 30 * 24 * time.Hour

// Receipt is one device report about a notice. NoticeID is the notice id the
// phone read from the push. ID is the store's row number, used to mark a
// receipt as forwarded to its hub.
type Receipt struct {
	ID       int64
	DeviceID string
	NoticeID string
	State    string
	Action   string
	At       time.Time
}

// AddNoticeReceipt stores one receipt. It reports false when the same notice,
// state and action were already recorded for the device, so duplicates are not
// forwarded twice.
func (s *Store) AddNoticeReceipt(ctx context.Context, deviceID string, r Receipt, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO notice_receipts(notification_id,device_id,state,action,at,received_at,forwarded)
	VALUES(?,?,?,?,?,?,0) ON CONFLICT(notification_id,device_id,state,action) DO NOTHING`,
		r.NoticeID, deviceID, r.State, r.Action, millis(r.At), millis(now))
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	return inserted == 1, err
}

// UnforwardedReceipts lists a hub's receipts that were not yet queued to the
// hub, oldest first. A receipt stays unforwarded until the relay queues it on a
// connected hub, so receipts stored while the hub is offline go out on connect.
func (s *Store) UnforwardedReceipts(ctx context.Context, hubID string, limit int) ([]Receipt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.device_id,r.notification_id,r.state,r.action,r.at
	FROM notice_receipts r JOIN devices d ON d.id=r.device_id
	WHERE d.hub_id=? AND r.forwarded=0 ORDER BY r.id LIMIT ?`, hubID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := []Receipt{}
	for rows.Next() {
		var r Receipt
		var at int64
		if err = rows.Scan(&r.ID, &r.DeviceID, &r.NoticeID, &r.State, &r.Action, &at); err != nil {
			return nil, err
		}
		r.At = fromMillis(at)
		receipts = append(receipts, r)
	}
	return receipts, rows.Err()
}

// MarkReceiptForwarded records that a receipt was queued to its hub.
func (s *Store) MarkReceiptForwarded(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE notice_receipts SET forwarded=1 WHERE id=?", id)
	return err
}
