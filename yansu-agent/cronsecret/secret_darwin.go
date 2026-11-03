//go:build darwin

package cronsecret

import (
	"errors"
	"fmt"
	"os"
)

func load(dir string, opts options) (string, error) {
	secret, err := keychainRead(dir, opts.keychain)
	if err == nil {
		if secret == "" {
			return "", errors.New("cron Keychain credential is empty")
		}
		return secret, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("start the desktop app to save or migrate the Keychain credential: %w", err)
	}
	return "", err
}

func store(dir, secret string, opts options) error {
	return storeVerifiedKeychain(dir, secret, func() error {
		return keychainWrite(dir, secret, opts.keychain, opts.trustedCLI)
	}, func() (string, error) {
		return keychainRead(dir, opts.keychain)
	})
}
