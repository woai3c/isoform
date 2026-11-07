package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A quit install has no helper waiting for the new app, so the rollback runs
// at launch. The trial record names the installed version and keeps the
// previous app in Root/previous.app until the new app acknowledges healthy.
//
// Each launch before the ack adds one attempt. After quitTrialMaxUnacked
// launches have not acked, the next launch restores the previous app, shows the
// same recovery message as the click path, and relaunches the previous app.
const quitTrialName = "quit-trial.json"

// quitTrialMaxUnacked is two strikes: one unacked launch may be a force-quit
// before startup finished, so the rollback waits for a second.
const quitTrialMaxUnacked = 2

const rolledBackName = "rolled-back.json"

type quitTrial struct {
	Version  string `json:"version"`
	From     string `json:"from"`
	Root     string `json:"root"`
	Attempts int    `json:"attempts"`
}

type rolledBackRecord struct {
	From     string `json:"from"` // the version that was rolled back
	To       string `json:"to"`   // the version restored
	Attempts int    `json:"attempts"`
}

func quitTrialPath(dataHome string) string {
	return filepath.Join(dataHome, "updates", quitTrialName)
}

func readQuitTrial(dataHome string) (quitTrial, bool) {
	var t quitTrial
	raw, err := os.ReadFile(quitTrialPath(dataHome))
	if err != nil || json.Unmarshal(raw, &t) != nil {
		return quitTrial{}, false
	}
	return t, true
}

func writeQuitTrial(dataHome string, t quitTrial) error {
	if err := os.MkdirAll(filepath.Join(dataHome, "updates"), 0700); err != nil {
		return err
	}
	return writeUpdateJSON(quitTrialPath(dataHome), t)
}

// keptTransaction reports whether root is the transaction folder beside bundle.
// Only such a folder is ever removed, so a bad record cannot point elsewhere.
func keptTransaction(root, bundle string) bool {
	return bundle != "" && root != "" && filepath.Dir(root) == filepath.Dir(bundle) &&
		strings.HasPrefix(filepath.Base(root), ".yansu-transaction-")
}

// ackQuitInstallTrial runs where the click path acknowledges startup, after
// storage is ready. The version has started healthy: the record and the kept
// previous app are no longer needed.
func ackQuitInstallTrial(dataHome, version, bundle string) {
	t, ok := readQuitTrial(dataHome)
	if !ok || t.Version != version {
		return
	}
	_ = os.Remove(quitTrialPath(dataHome))
	if keptTransaction(t.Root, bundle) {
		_ = os.RemoveAll(t.Root)
	}
	log.Printf("[update] %s started healthy; previous app removed", version)
}

// runQuitInstallTrial runs early in main, before the singleton and Wails. It
// returns true when it has rolled back and relaunched the previous app; the
// caller then exits without starting this version.
func runQuitInstallTrial(dataHome, version, bundle string, launch func(bundle string) error) bool {
	t, ok := readQuitTrial(dataHome)
	if !ok || t.Version != version || bundle == "" {
		return false
	}
	if t.Attempts < quitTrialMaxUnacked {
		t.Attempts++
		if err := writeQuitTrial(dataHome, t); err != nil {
			log.Printf("[update] record trial attempt: %v", err)
		}
		return false
	}
	backup := filepath.Join(t.Root, "previous.app")
	if !keptTransaction(t.Root, bundle) {
		log.Printf("[update] rollback skipped: kept folder %q is not beside the app", t.Root)
		return false
	}
	if _, err := os.Stat(backup); err != nil {
		log.Printf("[update] rollback skipped: previous app is gone: %v", err)
		return false
	}
	if err := relaunchableBundle(bundle); err != nil {
		log.Printf("[update] rollback skipped: %v", err)
		return false
	}
	u := &bundleUpdate{Root: t.Root, Bundle: bundle, Backup: backup, Version: version, DataHome: dataHome}
	if err := u.restore(); err != nil {
		log.Printf("[update] rollback failed: %v", err)
		return false
	}
	_ = os.Remove(quitTrialPath(dataHome))
	// The same recovery record the click path leaves: the restored app shows it
	// and blocks the failed version from background downloads.
	recovery := updateRecoveryRecord{
		Detail:   rollbackRecoveryDetail("en", version, t.From),
		Version:  version,
		Previous: t.From,
	}
	if err := writeUpdateJSON(filepath.Join(dataHome, "updates", "last-error.json"), recovery); err != nil {
		log.Printf("[update] save rollback message: %v", err)
	}
	if err := writeUpdateJSON(filepath.Join(dataHome, "updates", rolledBackName),
		rolledBackRecord{From: version, To: t.From, Attempts: t.Attempts}); err != nil {
		log.Printf("[update] save rollback event: %v", err)
	}
	log.Printf("[update] %s did not start after %d launches; restored %s", version, t.Attempts, t.From)
	if err := launch(bundle); err != nil {
		log.Printf("[update] reopen restored app: %v", err)
	}
	return true
}

// rollbackRecoveryCopy is the one-time message after a rollback. {new} is the
// version that did not start and {old} the version the person is back on.
var rollbackRecoveryCopy = map[string]string{
	"en":    "Isoform {new} didn't start, so you're back on {old}. The next version installs as usual.",
	"zh-CN": "Isoform {new} 未能启动，已回到 {old}。下一个版本会照常安装。",
}

func rollbackRecoveryDetail(lang, failed, previous string) string {
	return strings.NewReplacer("{new}", failed, "{old}", previous).Replace(rollbackRecoveryCopy[normalizeUILanguage(lang)])
}

// launchRestoredApp reopens the bundle at path with this process's executable name.
func launchRestoredApp(bundle string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(bundle, "Contents", "MacOS", filepath.Base(exe)))
	detachUpdateHelper(cmd)
	return cmd.Start()
}

// reportRolledBackUpdate runs once at launch of the restored app and reports the rollback.
func (a *App) reportRolledBackUpdate() {
	home, err := flavorDataDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, "updates", rolledBackName)
	raw, err := os.ReadFile(path)
	var rec rolledBackRecord
	if err != nil || json.Unmarshal(raw, &rec) != nil {
		return
	}
	recordUpdateRolledBack(a.telemetry, rec)
	_ = os.Remove(path)
}
