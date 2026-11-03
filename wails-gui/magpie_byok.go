package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Serialize config saves with bootstrap so an old bootstrap snapshot cannot
// put a withdrawn credential back. The only authority is ai-provider.json.
var magpieByokMu sync.Mutex

func (s *magpieSidecar) reconcileByok() error {
	magpieByokMu.Lock()
	defer magpieByokMu.Unlock()
	_, err := reconcileMagpieByokFromConfig(s.stateDir)
	return err
}

func reconcileMagpieByokFromConfig(stateDir string) (AIProviderConfig, error) {
	var cfg AIProviderConfig
	if stateDir == "" {
		return cfg, errMagpieStateDir
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(stateDir), aiProviderConfigFileName))
	if err == nil {
		if json.Unmarshal(data, &cfg) != nil {
			// A partial config cannot authorize retaining any credential.
			cfg = AIProviderConfig{}
		}
	} else if !os.IsNotExist(err) {
		// Remove the copy even when the source cannot be read; return the
		// source error so startup does not continue on an unknown config.
		if cleanupErr := reconcileMagpieByok(stateDir, AIProviderConfig{}); cleanupErr != nil {
			return cfg, cleanupErr
		}
		return cfg, fmt.Errorf("read model provider config: %w", err)
	}
	return cfg, reconcileMagpieByok(stateDir, cfg)
}

// Reconcile only Isoform's bundled store, never the user's magpie config.
// Magpie v0.1.1172 stores providers in a top-level providers array alongside
// groups and other settings. Keep every unrelated field as raw JSON.
// Its .bak and .bad-* recovery files must not retain revoked keys either.
func reconcileMagpieByok(stateDir string, cfg AIProviderConfig) (reconcileErr error) {
	if stateDir == "" {
		return errMagpieStateDir
	}
	path := filepath.Join(stateDir, "config", "magpie", "providers.json")
	backups, err := filepath.Glob(path + ".bad-*")
	if err != nil {
		return err
	}
	interrupted, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".isoform-provider-*"))
	if err != nil {
		return err
	}
	backups = append(backups, interrupted...)
	// The authoritative source uses the same private atomic writer, in
	// Isoform's data root. Interrupted writes there can hold the old key too.
	interrupted, err = filepath.Glob(filepath.Join(filepath.Dir(stateDir), ".isoform-provider-*"))
	if err != nil {
		return err
	}
	backups = append(backups, interrupted...)
	// Interrupted writes and known-bad recovery files are never inputs to
	// recovery. Remove them even if the primary store cannot be decoded.
	for _, backup := range backups {
		if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove bundled magpie provider recovery file: %w", err)
		}
	}
	// Keep the last-good bytes available for the recovery attempt below,
	// then retire them on both successful and failed reconciliation paths.
	defer func() {
		if err := os.Remove(path + ".bak"); err != nil && !os.IsNotExist(err) {
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("remove bundled magpie provider backup: %w", err))
		}
	}()
	root := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read bundled magpie providers: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &root); err != nil || root == nil {
			// Honor magpie's last-good recovery before removing its copies.
			backup, readErr := os.ReadFile(path + ".bak")
			root = map[string]json.RawMessage{}
			if readErr != nil || json.Unmarshal(backup, &root) != nil || root == nil {
				return fmt.Errorf("bundled magpie providers config is invalid")
			}
		}
	}
	var providers []json.RawMessage
	if raw, ok := root["providers"]; ok {
		if err := json.Unmarshal(raw, &providers); err != nil {
			return fmt.Errorf("bundled magpie providers list is invalid")
		}
	}
	kept := make([]json.RawMessage, 0, len(providers)+1)
	for _, raw := range providers {
		var identity struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return fmt.Errorf("bundled magpie provider is invalid")
		}
		// provider add in the pinned magpie creates numbered providers on
		// key changes. Retire those historical copies as well.
		suffix, numbered := strings.CutPrefix(identity.ID, magpieByokName+"-")
		number, _ := strconv.Atoi(suffix)
		if identity.ID != magpieByokName && !(numbered && number >= 2) {
			kept = append(kept, raw)
		}
	}
	if args, _, _, ok := magpieByokArgs(cfg); ok {
		provider := map[string]any{
			"id": magpieByokName, "name": magpieByokName,
			"key":    strings.TrimSpace(cfg.APIKey),
			"models": strings.Split(strings.TrimPrefix(args[2], "models="), ","),
		}
		endpoint, base, _ := strings.Cut(args[0], "=")
		if endpoint == "url" {
			endpoint = "chat"
		}
		provider[endpoint] = base
		raw, _ := json.Marshal(provider)
		kept = append(kept, raw)
	}
	if !existed && len(kept) == 0 {
		return nil
	}
	root["providers"], _ = json.Marshal(kept)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if bytes.Equal(data, out) {
		return nil
	}
	return writeMagpieConfigFile(path, out)
}

// Write privately and rename atomically, without making a credential backup.
func writeMagpieConfigFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".isoform-provider-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
