package storage

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SchemaVersionError prevents writes beyond the audited schema compatibility ceiling.
// Callers can use errors.As to present a localized error with both versions.
type SchemaVersionError struct {
	StoredVersion    int
	SupportedVersion int
}

type snapshotOptions struct {
	availableBytes func(string) (uint64, error)
	removeFile     func(string) error
}

const stalePendingSnapshotAge = 24 * time.Hour

func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf("database schema version %d is newer than this Isoform build supports (%d); install a newer Isoform build to open this data safely", e.StoredVersion, e.SupportedVersion)
}

func checkSchemaVersion(db *sql.DB) (int, error) {
	return checkSchemaVersionCeiling(db, supportedSchemaVersion)
}

func checkSchemaVersionCeiling(db *sql.DB, ceiling int) (int, error) {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if version > ceiling {
		return version, &SchemaVersionError{StoredVersion: version, SupportedVersion: ceiling}
	}
	return version, nil
}

func openReadOnlyDatabase(dbPath string) (*sql.DB, error) {
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absPath)
	// A Windows drive path must be /C:/... in a file URI, not its host.
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func checkExistingSchemaVersion(dbPath string) error {
	info, err := os.Stat(dbPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat database: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}
	// SQLite can create WAL/SHM sidecars even for a mode=ro connection. Reject
	// an already-newer main header without opening SQLite at all. The SQLite
	// check below remains necessary because WAL commits can carry a newer
	// version than the checkpointed main file.
	file, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database header: %w", err)
	}
	var header [100]byte
	_, readErr := io.ReadFull(file, header[:])
	file.Close()
	if readErr != nil {
		return fmt.Errorf("read database header: %w", readErr)
	}
	if string(header[:16]) != "SQLite format 3\x00" {
		return fmt.Errorf("invalid SQLite database header")
	}
	// SQLite stores the application-defined user_version as a signed 32-bit
	// big-endian integer at byte offset 60 in its database file header.
	mainVersion := int(int32(binary.BigEndian.Uint32(header[60:64])))
	if mainVersion > supportedSchemaVersion {
		return &SchemaVersionError{StoredVersion: mainVersion, SupportedVersion: supportedSchemaVersion}
	}
	db, err := openReadOnlyDatabase(dbPath)
	if err != nil {
		return fmt.Errorf("open database read-only: %w", err)
	}
	defer db.Close()
	_, err = checkSchemaVersion(db)
	return err
}

// snapshotBeforeMigration uses SQLite's consistent backup of committed data,
// including WAL contents. No snapshot is needed for a new, empty database.
// Failure to create or verify a snapshot stops migration; insufficient space
// explicitly skips it so a nearly full disk does not prevent startup.
func snapshotBeforeMigration(db *sql.DB, dbPath string, version int, options snapshotOptions) error {
	info, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("stat database: %w", err)
	}
	var pageCount, pageSize uint64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return fmt.Errorf("read database page count: %w", err)
	}
	if pageCount == 0 {
		return nil
	}
	lock, err := acquireSnapshotLock(dbPath)
	if err != nil {
		return err
	}
	if lock == nil {
		return fmt.Errorf("another process is creating a migration snapshot; retry after it finishes")
	}
	defer lock.Close()
	removeStalePendingSnapshots(dbPath, time.Now(), options.removeFile)
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return fmt.Errorf("read database page size: %w", err)
	}
	// The main file may be smaller than committed data still in the WAL.
	requiredBytes := max(uint64(info.Size()), pageCount*pageSize)
	freeBytes, err := options.availableBytes(filepath.Dir(dbPath))
	if err != nil {
		return fmt.Errorf("check free space: %w", err)
	}
	if freeBytes < requiredBytes {
		log.Printf("[storage] migration snapshot skipped: insufficient free space (available %d bytes, need %d bytes) for %s", freeBytes, requiredBytes, dbPath)
		return nil
	}

	// Reserve a private, unique filename. VACUUM INTO accepts an empty file.
	prefix := filepath.Base(dbPath) + ".snapshot-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	file, err := os.CreateTemp(filepath.Dir(dbPath), fmt.Sprintf("%s-v%d-*.pending", prefix, version))
	if err != nil {
		return fmt.Errorf("create snapshot file: %w", err)
	}
	snapshotPath := file.Name()
	verified := false
	defer func() {
		if !verified {
			if err := os.Remove(snapshotPath); err != nil && !os.IsNotExist(err) {
				log.Printf("[storage] remove incomplete snapshot %s: %v", snapshotPath, err)
			}
		}
	}()
	if err := file.Close(); err != nil {
		return fmt.Errorf("close snapshot file: %w", err)
	}
	if _, err := db.Exec("VACUUM INTO ?", snapshotPath); err != nil {
		return fmt.Errorf("vacuum into snapshot: %w", err)
	}
	if err := verifySnapshot(snapshotPath); err != nil {
		return err
	}
	// Only verified snapshots receive the final suffix. An interrupted VACUUM
	// cannot displace a good snapshot during a later retention pass.
	verifiedPath := strings.TrimSuffix(snapshotPath, ".pending") + ".db"
	if err := os.Rename(snapshotPath, verifiedPath); err != nil {
		return fmt.Errorf("publish verified snapshot: %w", err)
	}
	verified = true
	log.Printf("[storage] migration snapshot verified: %s (schema %d)", verifiedPath, version)
	pruneMigrationSnapshots(dbPath, options.removeFile)
	return nil
}

func verifySnapshot(snapshotPath string) error {
	db, err := openReadOnlyDatabase(snapshotPath)
	if err != nil {
		return fmt.Errorf("open snapshot for verification: %w", err)
	}
	defer db.Close()
	rows, err := db.Query("PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("check snapshot integrity: %w", err)
	}
	defer rows.Close()
	var results int
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("read snapshot integrity: %w", err)
		}
		if result != "ok" {
			return fmt.Errorf("snapshot integrity check failed: %s", result)
		}
		results++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read snapshot integrity: %w", err)
	}
	if results != 1 {
		return fmt.Errorf("snapshot integrity check returned %d results, expected one ok", results)
	}
	return nil
}

func pruneMigrationSnapshots(dbPath string, removeFile func(string) error) {
	entries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		log.Printf("[storage] list migration snapshots for cleanup: %v", err)
		return
	}
	var names []string
	prefix := filepath.Base(dbPath) + ".snapshot-"
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		names = append(names, entry.Name())
	}
	// Fixed-width UTC dates sort chronologically, independent of file mtimes.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names[min(2, len(names)):] {
		if err := removeFile(filepath.Join(filepath.Dir(dbPath), name)); err != nil && !os.IsNotExist(err) {
			log.Printf("[storage] remove old migration snapshot %s: %v", name, err)
		}
	}
}

// The lock file is deliberately retained: unlinking a lock file could let two
// processes hold locks on different inodes for the same database.
func acquireSnapshotLock(dbPath string) (*os.File, error) {
	file, err := os.OpenFile(dbPath+".snapshot.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open migration snapshot lock: %w", err)
	}
	locked, err := tryLockSnapshotFile(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("lock migration snapshots: %w", err)
	}
	if !locked {
		file.Close()
		return nil, nil
	}
	return file, nil
}

func cleanupStalePendingSnapshots(dbPath string, removeFile func(string) error) {
	lock, err := acquireSnapshotLock(dbPath)
	if err != nil {
		log.Printf("[storage] cleanup pending migration snapshots: %v", err)
		return
	}
	if lock == nil {
		return // A snapshot is still in progress, regardless of its age.
	}
	defer lock.Close()
	removeStalePendingSnapshots(dbPath, time.Now(), removeFile)
}

// Caller holds the snapshot lock. The age threshold also avoids touching
// recent pending files left by older binaries that predate this lock protocol.
func removeStalePendingSnapshots(dbPath string, now time.Time, removeFile func(string) error) {
	entries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		log.Printf("[storage] list pending migration snapshots: %v", err)
		return
	}
	prefix := filepath.Base(dbPath) + ".snapshot-"
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".pending") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			log.Printf("[storage] inspect pending migration snapshot %s: %v", entry.Name(), err)
			continue
		}
		if now.Sub(info.ModTime()) < stalePendingSnapshotAge {
			continue
		}
		if err := removeFile(filepath.Join(filepath.Dir(dbPath), entry.Name())); err != nil && !os.IsNotExist(err) {
			log.Printf("[storage] remove stale pending migration snapshot %s: %v", entry.Name(), err)
		}
	}
}
