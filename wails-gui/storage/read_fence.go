package storage

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

// ErrReadRevisionChanged rejects publication based on a stale database read.
var ErrReadRevisionChanged = errors.New("source database changed before publication")

// ReadRevision tracks writes on the store connection and commits made through
// other connections. It is meaningful only for a database with one persistent
// connection, as used by Store and the activity store.
type ReadRevision struct {
	changes     int64
	dataVersion int64
}

type revisionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// DatabaseRevision must be captured before reading the source facts that a
// later publication uses. A changed stamp rejects even unrelated writes so
// source updates cannot slip through a queued publication.
func DatabaseRevision(ctx context.Context, queryer revisionQueryer) (ReadRevision, error) {
	var revision ReadRevision
	err := queryer.QueryRowContext(ctx, "SELECT total_changes(), data_version FROM pragma_data_version").Scan(&revision.changes, &revision.dataVersion)
	return revision, err
}

func (s *Store) ReadRevision(ctx context.Context) (ReadRevision, error) {
	release, err := s.lockContextUseRevision(ctx)
	if err != nil {
		return ReadRevision{}, err
	}
	defer release()
	revision, err := DatabaseRevision(ctx, s.db)
	// Usage receipts are not source mutations. Subtract only this Store's
	// exact log row changes, never unrelated or external connection writes.
	revision.changes -= s.contextUses.changes
	return revision, err
}

// LockDatabaseRevision holds the source database unchanged until release. The
// sole connection excludes local writers; BEGIN IMMEDIATE also excludes other
// connections. No calls through db may be made while the fence is held.
func LockDatabaseRevision(ctx context.Context, db *sql.DB, expected ReadRevision) (func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
		return nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
			_ = conn.Close()
		})
	}
	current, err := DatabaseRevision(ctx, conn)
	if err == nil && current != expected {
		err = ErrReadRevisionChanged
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// LockReadRevision keeps this store unchanged through publication of a read.
// No store methods may be called until the returned release function runs.
func (s *Store) LockReadRevision(ctx context.Context, expected ReadRevision) (func(), error) {
	releaseLog, err := s.lockContextUseRevision(ctx)
	if err != nil {
		return nil, err
	}
	expected.changes += s.contextUses.changes
	releaseDB, err := LockDatabaseRevision(ctx, s.db, expected)
	if err != nil {
		releaseLog()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { releaseDB(); releaseLog() }) }, nil
}
