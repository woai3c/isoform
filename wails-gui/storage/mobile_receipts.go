package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MobileReceiptRetention is how long a stored phone outcome can still be
// replayed. A retry older than this is answered as a new request.
const MobileReceiptRetention = 7 * 24 * time.Hour

// MobileReceipt is the stored answer to one successful phone POST
// (PROTOCOL.md section 15.2). RequestKey is the namespaced key
// "mobile:<deviceId>:<idempotencyKey>", so two phones never collide.
type MobileReceipt struct {
	DeviceID   string
	RequestKey string
	Route      string
	Status     int
	Body       string
	At         int64
}

// MobileReceipt loads the receipt for a namespaced request key.
func (s *Store) MobileReceipt(ctx context.Context, requestKey string) (MobileReceipt, bool, error) {
	var receipt MobileReceipt
	err := s.db.QueryRowContext(ctx, `
		SELECT device_id, request_key, route, status, body, at
		FROM mobile_receipts WHERE request_key = ?
	`, requestKey).Scan(&receipt.DeviceID, &receipt.RequestKey, &receipt.Route, &receipt.Status, &receipt.Body, &receipt.At)
	if errors.Is(err, sql.ErrNoRows) {
		return MobileReceipt{}, false, nil
	}
	if err != nil {
		return MobileReceipt{}, false, err
	}
	return receipt, true, nil
}

// SaveMobileReceipt records the outcome of a request. The first receipt for a
// key wins, so a racing duplicate never overwrites the answer a phone already
// received.
func (s *Store) SaveMobileReceipt(ctx context.Context, receipt MobileReceipt) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO mobile_receipts (request_key, device_id, route, status, body, at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, receipt.RequestKey, receipt.DeviceID, receipt.Route, receipt.Status, receipt.Body, receipt.At)
	return err
}

// PruneMobileReceipts deletes receipts older than MobileReceiptRetention and
// returns how many were removed.
func (s *Store) PruneMobileReceipts(ctx context.Context, now time.Time) (int64, error) {
	cutoff := now.Add(-MobileReceiptRetention).UnixMilli()
	result, err := s.db.ExecContext(ctx, `DELETE FROM mobile_receipts WHERE at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
