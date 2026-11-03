package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// A publication owns the file/policy barriers before reading sources. The
// activity revision is captured before that read and fenced only after all its
// queries finish. No activity queries may follow lockActivity until release.
// The main-store writer transaction independently fences its policy and rows.
type mobileOfferPublication struct {
	activity *ActivityStore
	revision storage.ReadRevision
	files    [sha256.Size]byte
	paths    []mobileOfferAdmissionPath
	releases []func()
	once     sync.Once
}

type mobileOfferAdmissionPath struct {
	key, path string
	directory bool
}

type mobileOfferPublicationLock struct {
	try     func() bool
	release func()
}

func (a *App) beginMobileOfferPublication(ctx context.Context, reader *App) (*mobileOfferPublication, error) {
	if a == nil || reader == nil || reader.activity == nil || reader.activity.store == nil {
		return nil, errMobileStoreUnavailable
	}
	paths, err := mobileOfferSourcePaths(reader)
	if err != nil {
		return nil, err
	}
	topics, interests := a.topicsSvc().store, a.interestsSvc().store
	publication := &mobileOfferPublication{activity: reader.activity.store, paths: paths}
	locks := []mobileOfferPublicationLock{
		{a.closedEyes.mu.TryLock, a.closedEyes.mu.Unlock},
		{memoryMutationMu.TryLock, memoryMutationMu.Unlock},
		{memoryFileLocks.publication.TryLock, memoryFileLocks.publication.Unlock},
		{pii.TryLockSensitiveAppsForPublication, pii.UnlockSensitiveAppsForPublication},
		{userVoiceInputAppsPublicationMu.TryRLock, userVoiceInputAppsPublicationMu.RUnlock},
		{mobileBriefPublicationMu.TryRLock, mobileBriefPublicationMu.RUnlock},
		{topics.publication.TryRLock, topics.publication.RUnlock},
		{interests.publication.TryRLock, interests.publication.RUnlock},
	}
	if reader.handoffStore != nil {
		locks = append(locks, mobileOfferPublicationLock{reader.handoffStore.TryLockForPublication, reader.handoffStore.UnlockForPublication})
	}
	locks = append(locks, mobileOfferPublicationLock{publication.activity.readPrivacyMu.TryLock, publication.activity.readPrivacyMu.Unlock})
	// Summary capture can own the activity fence while reading memory. Never
	// wait with a partially acquired group, which would invert that order.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, lock := range locks {
			if !lock.try() {
				break
			}
			publication.releases = append(publication.releases, lock.release)
		}
		if len(publication.releases) == len(locks) {
			break
		}
		for i := len(publication.releases) - 1; i >= 0; i-- {
			publication.releases[i]()
		}
		publication.releases = nil
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	publication.files, err = publication.readFiles(ctx)
	if err == nil {
		publication.revision, err = storage.DatabaseRevision(ctx, publication.activity.db)
	}
	if err != nil {
		publication.release()
		return nil, err
	}
	return publication, nil
}

// This fingerprint takes no publication locks or database connections. Readers
// can compare it around a source read, including beneath a publication guard.
func (a *App) mobileOfferSourceFiles(ctx context.Context, reader *App) ([sha256.Size]byte, error) {
	paths, err := mobileOfferSourcePaths(reader)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return (&mobileOfferPublication{paths: paths}).readFiles(ctx)
}

func mobileOfferSourcePaths(reader *App) ([]mobileOfferAdmissionPath, error) {
	if reader == nil {
		return nil, errMobileStoreUnavailable
	}
	memory, err := memoryDir()
	if err != nil {
		return nil, err
	}
	if reader.memoryStore != nil {
		memory = reader.memoryStore.baseDir
	}
	knowledge, err := knowledgeDir()
	if err != nil {
		return nil, err
	}
	topicsDir, err := topicsStateDir()
	if err != nil {
		return nil, err
	}
	briefDir, err := mobileBriefStateDir()
	if err != nil {
		return nil, err
	}
	paths := []mobileOfferAdmissionPath{
		{"memory", memory, true}, {"knowledge", knowledge, true},
		{"topics", filepath.Join(topicsDir, topicsStateFileName), false},
		{"interests", filepath.Join(topicsDir, interestsStateFileName), false},
		{"briefs", filepath.Join(briefDir, mobileBriefDirName), true},
	}
	if reader.handoffStore != nil {
		paths = append(paths, mobileOfferAdmissionPath{"handoffs", reader.handoffStore.Dir(), true})
	}
	return paths, nil
}

func (p *mobileOfferPublication) release() {
	p.once.Do(func() {
		for i := len(p.releases) - 1; i >= 0; i-- {
			p.releases[i]()
		}
	})
}

func (p *mobileOfferPublication) lockActivity(ctx context.Context) error {
	release, err := storage.LockDatabaseRevision(ctx, p.activity.db, p.revision)
	if errors.Is(err, storage.ErrReadRevisionChanged) {
		return errMobileOfferUnavailable
	}
	if err != nil {
		return err
	}
	p.releases = append(p.releases, release)
	return nil
}

func (p *mobileOfferPublication) validateFiles(ctx context.Context) error {
	current, err := p.readFiles(ctx)
	if err != nil {
		return err
	}
	if current != p.files {
		return errMobileOfferUnavailable
	}
	return nil
}

// Only source markdown and small JSON privacy/provenance files are hashed.
// Include legacy revision history and semantic controls; ignore media. Bounds
// fail closed rather than leave a partial fingerprint eligible for admission.
func (p *mobileOfferPublication) readFiles(ctx context.Context) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	digest := sha256.New()
	budget := mobileOfferAdmissionFileBudget{files: 10000, bytes: 64 << 20}
	for _, path := range p.paths {
		if err := mobileOfferAdmissionPathHash(ctx, digest, path.key, path.path, path.directory, 0, &budget); err != nil {
			return result, err
		}
	}
	copy(result[:], digest.Sum(nil))
	return result, ctx.Err()
}

type mobileOfferAdmissionFileBudget struct {
	files int
	bytes int64
}

func mobileOfferAdmissionPathHash(ctx context.Context, digest hash.Hash, key, path string, directory bool, depth int, budget *mobileOfferAdmissionFileBudget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		mobileOfferAdmissionHash(digest, key+"/missing", nil)
		return nil
	}
	if err != nil {
		return err
	}
	if directory {
		if !info.IsDir() || depth > 4 {
			return errMobileOfferUnavailable
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		if len(entries) > budget.files {
			return errMobileOfferUnavailable
		}
		mobileOfferAdmissionHash(digest, key+"/directory", nil)
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || (depth == 0 && (name == memoryHistoryDirName || name == ".semantic")) {
				if depth == 0 && name != memoryHistoryDirName && name != ".semantic" {
					continue
				}
				if err := mobileOfferAdmissionPathHash(ctx, digest, key+"/"+name, filepath.Join(path, name), true, depth+1, budget); err != nil {
					return err
				}
				continue
			}
			switch filepath.Ext(name) {
			case ".md", ".json", ".jsonl":
				if err := mobileOfferAdmissionPathHash(ctx, digest, key+"/"+name, filepath.Join(path, name), false, depth+1, budget); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return errMobileOfferUnavailable
	}
	budget.files--
	budget.bytes -= info.Size()
	if budget.files < 0 || budget.bytes < 0 {
		return errMobileOfferUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	content := sha256.New()
	// Bound a file that grows after Lstat as well as the original size.
	read, err := io.Copy(content, io.LimitReader(mobileOfferAdmissionReader{ctx, file}, info.Size()+1))
	if err != nil {
		return err
	}
	if read != info.Size() {
		return errMobileOfferUnavailable
	}
	mobileOfferAdmissionHash(digest, key, content.Sum(nil))
	return nil
}

type mobileOfferAdmissionReader struct {
	ctx context.Context
	io.Reader
}

func (r mobileOfferAdmissionReader) Read(bytes []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(bytes)
}

func mobileOfferAdmissionHash(digest hash.Hash, key string, data []byte) {
	_ = binary.Write(digest, binary.LittleEndian, uint64(len(key)))
	_, _ = io.Copy(digest, strings.NewReader(key))
	_ = binary.Write(digest, binary.LittleEndian, uint64(len(data)))
	_, _ = digest.Write(data)
}
