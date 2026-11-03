// Package cronsecret shares the desktop scheduler credential with the yansu CLI.
// macOS stores the credential in Keychain; other systems use a private file.
package cronsecret

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Service is the legacy Keychain service and the namespace prefix for current
// generation.<UUID> entries. New credentials use a unique service per rotation.
const Service = "com.isoform.cron-api"

// ErrUnavailable means that the platform credential store cannot be accessed.
var ErrUnavailable = errors.New("cron credential store unavailable")

// Option selects a credential-store setting.
type Option func(*options)

type options struct {
	keychain   string
	trustedCLI string
}

// WithKeychain selects an existing macOS Keychain without changing the user's
// default or search list. It is useful for isolated integration tests.
func WithKeychain(path string) Option {
	return func(opts *options) { opts.keychain = path }
}

// WithTrustedCLI permits the desktop's specific bundled yansu executable to
// read its macOS credential. The desktop must resolve this from its own bundle,
// never PATH or an untrusted request. Other executables are not granted access.
// This option affects Store only; Load cannot change the item's access list.
func WithTrustedCLI(path string) Option {
	return func(opts *options) { opts.trustedCLI = path }
}

func configure(values []Option) options {
	var opts options
	for _, value := range values {
		value(&opts)
	}
	return opts
}

// Load reads the credential for dataDir. Only the desktop writes or migrates
// macOS credentials, so its signed identity owns the Keychain item's access.
func Load(dataDir string, opts ...Option) (string, error) {
	account, err := canonicalDir(dataDir)
	if err != nil {
		return "", err
	}
	return load(account, configure(opts))
}

// Store persists the current desktop credential for dataDir. On macOS it removes
// any old cron-secret file only after the Keychain write has been verified.
func Store(dataDir, secret string, opts ...Option) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("cron API secret must not be empty")
	}
	account, err := canonicalDir(dataDir)
	if err != nil {
		return err
	}
	settings := configure(opts)
	if settings.trustedCLI != "" {
		cli, err := filepath.Abs(settings.trustedCLI)
		if err != nil {
			return fmt.Errorf("resolve trusted cron CLI: %w", err)
		}
		resolved, err := filepath.EvalSymlinks(cli)
		if err != nil {
			return fmt.Errorf("resolve trusted cron CLI: %w", err)
		}
		// Resolve parent aliases such as /var -> /private/var, but never follow
		// a substituted executable symlink into a shared helper.
		info, err := os.Lstat(cli)
		if err != nil {
			return fmt.Errorf("inspect trusted cron CLI: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return errors.New("trusted cron CLI must be a regular executable")
		}
		settings.trustedCLI = resolved
	}
	return store(account, secret, settings)
}

// Keep the legacy credential until the native store reads back the exact value
// it wrote. A successful write alone does not establish that it can be read.
func storeVerifiedKeychain(dir, secret string, write func() error, read func() (string, error)) error {
	if err := write(); err != nil {
		return err
	}
	stored, err := read()
	if err != nil {
		return err
	}
	if stored != secret {
		return errors.New("cron Keychain credential verification failed")
	}
	err = os.Remove(filepath.Join(dir, "cron-secret"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// The CLI reads the verified Keychain credential. Cleanup must not
		// take scheduling down when only the obsolete file cannot be removed.
		log.Printf("[cronsecret] Keychain credential verified, but could not remove legacy cron-secret: %v", err)
	}
	return nil
}

// Canonicalize even before the data directory exists, so a symlinked home or
// parent does not give the desktop and CLI different Keychain accounts.
func canonicalDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("cron credential data directory must not be empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve cron credential directory: %w", err)
	}
	return resolveParents(abs)
}

func resolveParents(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve cron credential directory: %w", err)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolveParents(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}

func readLegacy(dir string) (string, error) {
	path := filepath.Join(dir, "cron-secret")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	// The file-backed account belongs to this directory's published endpoint.
	// A credential leaf alias must not borrow another directory's secret.
	if !info.Mode().IsRegular() {
		return "", errors.New("cron credential must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", errors.New("cron API secret is empty")
	}
	return secret, nil
}
