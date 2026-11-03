package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Attachments are sealed files a hub uploads for its phones, today the spoken
// brief (PROTOCOL.md section 17). The relay stores the ciphertext as given and
// never opens it; the key travels inside the end-to-end-sealed brief document.
// Bodies live on disk under <state dir>/attachments/<hubId>/<id>, the table
// holds the metadata. Only a hub that already exists and has a live device
// with brief:read may upload; each hub keeps at most KeptAttachments and
// MaxAttachmentBytesPerHub, the whole store at most MaxAttachmentStoreBytes,
// and every attachment expires after AttachmentTTL.

const (
	// MaxAttachmentBytes bounds one attachment body.
	MaxAttachmentBytes = 16 << 20
	// KeptAttachments is how many attachments one hub keeps; the oldest go first.
	KeptAttachments = 28
	// AttachmentTTL is how long an attachment stays downloadable.
	AttachmentTTL = 14 * 24 * time.Hour
	// attachmentReadScope is the device scope an upload needs a live holder of.
	attachmentReadScope = `%"brief:read"%`
)

// Quotas are variables so tests can lower them without writing gigabytes.
var (
	// MaxAttachmentBytesPerHub bounds one hub's stored bytes; the hub's oldest
	// attachments make room for a new one.
	MaxAttachmentBytesPerHub int64 = 96 << 20
	// MaxAttachmentStoreBytes bounds every hub together. A new attachment that
	// would cross it is refused, never another hub's file removed.
	MaxAttachmentStoreBytes int64 = 2 << 30
)

var (
	// ErrTooLarge reports an attachment over MaxAttachmentBytes, or an empty one.
	ErrTooLarge = errors.New("attachment is too large")
	// ErrStorageFull reports that the store is at MaxAttachmentStoreBytes.
	ErrStorageFull = errors.New("attachment storage is full")
	// ErrNoReader reports a hub without a live device that can read briefs.
	ErrNoReader = errors.New("hub has no paired device that reads briefs")
)

const attachmentsSchema = `
CREATE TABLE IF NOT EXISTS attachments (
	id TEXT PRIMARY KEY,
	hub_id TEXT NOT NULL REFERENCES hubs(id) ON DELETE CASCADE,
	size INTEGER NOT NULL,
	stored_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS attachments_hub ON attachments(hub_id, stored_at);
CREATE INDEX IF NOT EXISTS attachments_expiry ON attachments(expires_at);
`

// AuthorizeExistingHub checks a hub credential without creating the hub. An
// unknown hub and a wrong credential both report ErrUnauthorized.
func (s *Store) AuthorizeExistingHub(ctx context.Context, hubID, credentialHash string) error {
	var stored string
	err := s.db.QueryRowContext(ctx, "SELECT credential_hash FROM hubs WHERE id=?", hubID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if !sameDigest(stored, credentialHash) {
		return ErrUnauthorized
	}
	return nil
}

// HubCanReceiveAttachments reports whether the hub has a live paired device
// holding brief:read, the only reason to store an attachment for it.
func (s *Store) HubCanReceiveAttachments(ctx context.Context, hubID string, now time.Time) (bool, error) {
	var readers int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM devices WHERE hub_id=? AND expires_at>? AND scopes LIKE ?", hubID, millis(now), attachmentReadScope).Scan(&readers)
	if err != nil {
		return false, err
	}
	return readers > 0, nil
}

// attachmentPath is where a hub's attachment body lives.
func (s *Store) attachmentPath(hubID, id string) string {
	return filepath.Join(s.files, "attachments", hubID, id)
}

// PutAttachment streams an attachment body to disk and records it. body may be
// longer than MaxAttachmentBytes; the extra byte is detected and the upload
// refused with ErrTooLarge, so a caller can wrap the body in a size limit and
// still get the right error. The hub must exist and have a live reader. The
// hub's oldest attachments make room under its byte quota and its count cap;
// a body that would cross the store-wide cap is refused with ErrStorageFull.
func (s *Store) PutAttachment(ctx context.Context, hubID, id string, body io.Reader, now time.Time) error {
	if s.files == "" {
		return errors.New("attachment storage is not configured")
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM hubs WHERE id=?", hubID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	readable, err := s.HubCanReceiveAttachments(ctx, hubID, now)
	if err != nil {
		return err
	}
	if !readable {
		return ErrNoReader
	}
	dir := filepath.Dir(s.attachmentPath(hubID, id))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	size, err := io.Copy(tmp, io.LimitReader(body, MaxAttachmentBytes+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return err
	}
	if size == 0 || size > MaxAttachmentBytes {
		cleanup()
		return ErrTooLarge
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		cleanup()
		return err
	}

	removed, err := s.recordAttachment(ctx, hubID, id, size, now)
	if err != nil {
		cleanup()
		return err
	}
	for _, old := range removed {
		_ = os.Remove(s.attachmentPath(old.hubID, old.id))
	}
	if err := os.Rename(tmpName, s.attachmentPath(hubID, id)); err != nil {
		cleanup()
		return err
	}
	return nil
}

type attachmentRef struct {
	hubID, id string
}

// recordAttachment writes the row inside one transaction, enforcing the
// ownership, quota and cap rules, and returns the rows it evicted so their
// files can go.
func (s *Store) recordAttachment(ctx context.Context, hubID, id string, size int64, now time.Time) ([]attachmentRef, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// An id that belongs to another hub is never overwritten.
	var owner string
	switch err = tx.QueryRowContext(ctx, "SELECT hub_id FROM attachments WHERE id=?", id).Scan(&owner); {
	case err == nil:
		if owner != hubID {
			return nil, ErrConflict
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, err
	}
	// Expired rows and the row being replaced do not count against anyone.
	var removed []attachmentRef
	expired, err := tx.QueryContext(ctx, "SELECT hub_id,id FROM attachments WHERE expires_at<=? OR id=?", millis(now), id)
	if err != nil {
		return nil, err
	}
	for expired.Next() {
		var ref attachmentRef
		if err = expired.Scan(&ref.hubID, &ref.id); err != nil {
			expired.Close()
			return nil, err
		}
		removed = append(removed, ref)
	}
	expired.Close()
	if err = expired.Err(); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM attachments WHERE expires_at<=? OR id=?", millis(now), id); err != nil {
		return nil, err
	}
	// The hub's oldest attachments make room under its byte quota and count cap.
	for {
		var count int
		var used int64
		if err = tx.QueryRowContext(ctx, "SELECT count(*), COALESCE(SUM(size),0) FROM attachments WHERE hub_id=?", hubID).Scan(&count, &used); err != nil {
			return nil, err
		}
		if count < KeptAttachments && used+size <= MaxAttachmentBytesPerHub {
			break
		}
		if count == 0 {
			// Nothing left to evict and the body alone is over the quota.
			return nil, ErrTooLarge
		}
		var oldest string
		if err = tx.QueryRowContext(ctx, "SELECT id FROM attachments WHERE hub_id=? ORDER BY stored_at, id LIMIT 1", hubID).Scan(&oldest); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM attachments WHERE id=?", oldest); err != nil {
			return nil, err
		}
		removed = append(removed, attachmentRef{hubID: hubID, id: oldest})
	}
	var total int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(size),0) FROM attachments").Scan(&total); err != nil {
		return nil, err
	}
	if total+size > MaxAttachmentStoreBytes {
		return nil, ErrStorageFull
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO attachments(id,hub_id,size,stored_at,expires_at) VALUES(?,?,?,?,?)",
		id, hubID, size, millis(now), millis(now.Add(AttachmentTTL))); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return removed, nil
}

// GetAttachment returns the file path and size of a hub's attachment, or
// ErrNotFound when it is unknown, belongs to another hub, or has expired.
func (s *Store) GetAttachment(ctx context.Context, hubID, id string, now time.Time) (string, int64, error) {
	var size, expires int64
	err := s.db.QueryRowContext(ctx, "SELECT size,expires_at FROM attachments WHERE id=? AND hub_id=?", id, hubID).Scan(&size, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, err
	}
	if expires <= millis(now) {
		return "", 0, ErrNotFound
	}
	path := s.attachmentPath(hubID, id)
	if _, err := os.Stat(path); err != nil {
		return "", 0, ErrNotFound
	}
	return path, size, nil
}

// sweepAttachments removes expired attachments, files first, and returns how
// many rows went. It runs outside the main sweep transaction because it
// touches the file system.
func (s *Store) sweepAttachments(ctx context.Context, now time.Time) (int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT hub_id,id FROM attachments WHERE expires_at<=?", millis(now))
	if err != nil {
		return 0, err
	}
	var refs []attachmentRef
	for rows.Next() {
		var ref attachmentRef
		if err = rows.Scan(&ref.hubID, &ref.id); err != nil {
			rows.Close()
			return 0, err
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, ref := range refs {
		_ = os.Remove(s.attachmentPath(ref.hubID, ref.id))
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM attachments WHERE expires_at<=?", millis(now))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
