package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const updateAckEnv = "YANSU_UPDATE_ACK"
const updateHelperFlag = "--yansu-finish-update"

type bundleUpdate struct {
	Root       string
	Bundle     string
	Candidate  string
	Backup     string
	Executable string
	Version    string
	DataHome   string
	ParentPID  int
	// Quit installs swap the bundle and stop there: nothing reopens the app.
	Quit bool
}

type updateStartup struct {
	Version  string `json:"version"`
	DataHome string `json:"data_home"`
	Bundle   string `json:"bundle"`
	PID      int    `json:"pid"`
}

func prepareBundleUpdate(archive, bundle, version, dataHome string, verify func(string, string, string) error) (*bundleUpdate, error) {
	if err := dataHomeOutsideBundle(bundle, dataHome); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(filepath.Dir(bundle), ".yansu-transaction-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(root)
		}
	}()
	candidate, err := extractAppArchiveFile(archive, root)
	if err != nil {
		return nil, err
	}
	if err := verify(bundle, candidate, version); err != nil {
		return nil, fmt.Errorf("verify update app: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	txn := &bundleUpdate{Root: root, Bundle: bundle, Candidate: candidate,
		Backup: filepath.Join(root, "previous.app"), Executable: filepath.Base(exe),
		Version: version, DataHome: dataHome, ParentPID: os.Getpid()}
	ok = true
	return txn, nil
}

// Both renames stay on the same volume. No old bundle is deleted until the
// new process acknowledges startup, version and the same data home.
func (u *bundleUpdate) swapAndStart(start func(*bundleUpdate) error) error {
	if err := os.Rename(u.Bundle, u.Backup); err != nil {
		_ = os.RemoveAll(u.Root)
		return err
	}
	if err := os.Rename(u.Candidate, u.Bundle); err != nil {
		return errors.Join(err, u.restore())
	}
	if err := start(u); err != nil {
		return errors.Join(fmt.Errorf("start update helper: %w", err), u.restore())
	}
	return nil
}

func (u *bundleUpdate) restore() error {
	// Keep the verified old bundle even when restoration itself fails.
	failed := filepath.Join(u.Root, "failed.app")
	if _, err := os.Lstat(u.Bundle); err == nil {
		if err := os.Rename(u.Bundle, failed); err != nil {
			return fmt.Errorf("retain backup at %s: %w", u.Backup, err)
		}
	}
	if err := os.Rename(u.Backup, u.Bundle); err != nil {
		return fmt.Errorf("restore backup at %s: %w", u.Backup, err)
	}
	return os.RemoveAll(u.Root)
}

func writeUpdateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func startUpdateHelper(u *bundleUpdate) error {
	path := filepath.Join(u.Root, "transaction.json")
	if err := writeUpdateJSON(path, u); err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(u.Backup, "Contents", "MacOS", u.Executable), updateHelperFlag, path)
	detachUpdateHelper(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(u.Root, "ready")); err == nil {
			return nil
		}
		select {
		case err := <-done:
			return fmt.Errorf("helper exited before readiness: %v", err)
		case <-deadline.C:
			_ = cmd.Process.Kill()
			<-done
			return fmt.Errorf("update helper did not become ready")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// The helper runs from the retained old executable without starting Wails.
func runUpdateHelperCommand(args []string) (int, bool) {
	if len(args) == 0 || args[0] != updateHelperFlag {
		return 0, false
	}
	if runtime.GOOS != "darwin" || len(args) != 2 {
		return 2, true
	}
	data, err := os.ReadFile(args[1])
	var u bundleUpdate
	if err == nil {
		err = json.Unmarshal(data, &u)
	}
	if err != nil {
		log.Printf("[update] helper: %v", err)
		return 1, true
	}
	if err := os.WriteFile(filepath.Join(u.Root, "ready"), nil, 0600); err != nil {
		return 1, true
	}
	// The singleton and all old process handles must be released first. The
	// new app opens the moment the old process is gone.
	if !waitUpdateProcessExit(u.ParentPID, 30*time.Second) {
		err := u.restore()
		removeInstalledRecordIn(u.DataHome)
		log.Printf("[update] parent did not exit; restored bundle: %v", err)
		return 1, true
	}
	if u.Quit {
		// The person quit: the new app opens when they next launch Isoform. The
		// previous app stays in the transaction folder until that launch.
		log.Printf("[update] installed %s on quit; not reopening", u.Version)
		return 0, true
	}
	// This bounds mandatory core/storage readiness, not optional model work.
	// storageStartupState acknowledges before starting optional services.
	err = u.relaunch(execUpdateApp, 90*time.Second)
	if err != nil {
		log.Printf("[update] rollback: %v", err)
		return 1, true
	}
	return 0, true
}

func execUpdateApp(u *bundleUpdate, ack string) (*exec.Cmd, error) {
	cmd := exec.Command(filepath.Join(u.Bundle, "Contents", "MacOS", u.Executable))
	cmd.Env = append(os.Environ(), agentHomeEnv+"="+u.DataHome, updateAckEnv+"="+ack)
	detachUpdateHelper(cmd)
	return cmd, cmd.Start()
}

func (u *bundleUpdate) relaunch(launch func(*bundleUpdate, string) (*exec.Cmd, error), timeout time.Duration) error {
	ack := filepath.Join(u.Root, "started.json")
	cmd, err := launch(u, ack)
	if err == nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		deadline := time.NewTimer(timeout)
		defer deadline.Stop()
		for err == nil {
			if u.acknowledged(ack, cmd.Process.Pid) {
				return os.RemoveAll(u.Root)
			}
			select {
			case childErr := <-done:
				err = fmt.Errorf("updated app exited before startup: %v", childErr)
			case <-deadline.C:
				_ = cmd.Process.Kill()
				<-done
				err = fmt.Errorf("updated app did not acknowledge startup")
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	failure := err
	if err := u.restore(); err != nil {
		return errors.Join(failure, err)
	}
	// This error is displayed by the restored app, and retry requires a new
	// download. Never delete or reset the existing data home.
	_ = os.MkdirAll(filepath.Join(u.DataHome, "updates"), 0700)
	removeInstalledRecordIn(u.DataHome)
	if err := writeUpdateJSON(filepath.Join(u.DataHome, "updates", "last-error.json"), updateRecoveryRecord{Detail: failure.Error(), Version: u.Version}); err != nil {
		log.Printf("[update] save rollback error: %v", err)
	}
	if _, err := launch(u, ""); err != nil {
		return errors.Join(failure, fmt.Errorf("reopen restored app: %w", err))
	}
	return failure
}

func (u *bundleUpdate) acknowledged(path string, pid int) bool {
	data, err := os.ReadFile(path)
	var ack updateStartup
	return err == nil && json.Unmarshal(data, &ack) == nil && ack.Version == u.Version &&
		ack.DataHome == u.DataHome && ack.Bundle == u.Bundle && ack.PID == pid
}

func acknowledgeUpdateStartup() {
	path := os.Getenv(updateAckEnv)
	if path == "" {
		return
	}
	_ = os.Unsetenv(updateAckEnv)
	home, err := flavorDataDir()
	if err != nil {
		return
	}
	if err := writeUpdateJSON(path, updateStartup{Version: Version, DataHome: home, Bundle: findAppBundle(), PID: os.Getpid()}); err != nil {
		log.Printf("[update] acknowledge startup: %v", err)
	}
}

func (a *App) reportUpdateRecovery() {
	home, err := flavorDataDir()
	if err != nil {
		return
	}
	if v := loadBlockedVersion(home); v != "" {
		a.updater.mu.Lock()
		a.updater.blockedVersion = v
		a.updater.mu.Unlock()
	}
	path := filepath.Join(home, "updates", "last-error.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// Records from older helpers hold a bare string; newer ones name the version too.
	var rec updateRecoveryRecord
	if json.Unmarshal(data, &rec) != nil || rec.Detail == "" {
		var detail string
		if json.Unmarshal(data, &detail) != nil {
			return
		}
		rec = updateRecoveryRecord{Detail: detail}
	}
	a.updater.mu.Lock()
	a.updater.recoveryError = true
	newBlock := rec.Version != "" && rec.Version != a.updater.blockedVersion
	if newBlock {
		a.updater.blockedVersion = rec.Version
	}
	a.updater.mu.Unlock()
	if newBlock {
		if err := saveBlockedVersion(rec.Version); err != nil {
			log.Printf("[update] save blocked version: %v", err)
		}
	}
	a.broadcastUpdateStatus(UpdateStatus{Status: "error", Detail: rec.Detail})
	_ = os.Remove(path)
}
