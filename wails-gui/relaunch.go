package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
)

// Quit and reopen. macOS applies a Screen Recording grant made while the app
// runs only after a relaunch, and says so in its own "Quit & Reopen" dialog.
// That dialog reopens the app by bundle id through Launch Services, which
// can pick another registered copy of the bundle (an old development build,
// or one that no longer exists) and then nothing comes back. RelaunchApp
// reopens this exact bundle by path instead, once this process has exited
// (the single-instance lock would turn an earlier launch away). The setup
// sequence keeps its answers in ui_state, so it resumes at the next step.

// relaunchEnvKeys are the launch settings a relaunch carries over. `open`
// starts the new process from launchd, not from this one, so a build started
// with a local relay, another flavor or an isolated data home would otherwise
// come back with the defaults.
var relaunchEnvKeys = []string{
	appFlavorEnv,
	agentHomeEnv,
	"ISOFORM_RELAY_URL",
	"YANSU_RELAY_URL",
	"ISOFORM_MOBILE_REMOTE_ACCESS",
	"YANSU_MOBILE_STATE_DIR",
	"YANSU_BROWSER_BRIDGE",
}

// relaunchPending is set from the first RelaunchApp until this process
// exits, so a second tap or a slow quit never starts a second reopen. It
// clears when the reopen fails or gives up.
var relaunchPending atomic.Bool

// relaunchFailedEvent tells the window the reopen did not happen, so the
// card stops saying it is reopening.
const relaunchFailedEvent = "relaunch:failed"

// relaunchEnv returns KEY=VALUE for each relaunchEnvKeys entry set in this
// process.
func relaunchEnv() []string {
	env := []string{}
	for _, key := range relaunchEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

// relaunchableBundle refuses a bundle that could not be opened again once
// this process is gone: none at all, a translocated copy (macOS makes a new
// one per launch from a quarantined download), or one on a mounted volume
// such as a disk image.
func relaunchableBundle(bundle string) error {
	if bundle == "" {
		return fmt.Errorf("not running from an app bundle")
	}
	if strings.Contains(bundle, "/AppTranslocation/") {
		return fmt.Errorf("%s is a translocated copy; move the app to Applications first", bundle)
	}
	if strings.HasPrefix(bundle, "/Volumes/") {
		return fmt.Errorf("%s is on a mounted volume; move the app to Applications first", bundle)
	}
	info, err := os.Stat(bundle)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", bundle, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not an app bundle", bundle)
	}
	return nil
}

// ScreenRecordingRelaunch says whether Isoform has to reopen before it can
// see the screen, with the product name for the words around it.
type ScreenRecordingRelaunch struct {
	Needed bool   `json:"needed"`
	App    string `json:"app"`
}

// GetScreenRecordingRelaunch reports whether Screen Recording was turned on
// after this launch, so Isoform cannot see the screen until it reopens.
func (a *App) GetScreenRecordingRelaunch() ScreenRecordingRelaunch {
	return ScreenRecordingRelaunch{Needed: screenRecordingNeedsRelaunch(), App: productName()}
}

// RelaunchApp quits Isoform and opens this same app bundle again once this
// process has exited. A second call while one is under way does nothing.
func (a *App) RelaunchApp() error {
	if !relaunchPending.CompareAndSwap(false, true) {
		log.Printf("[relaunch] already reopening")
		return nil
	}
	bundle := findAppBundle()
	if err := relaunchableBundle(bundle); err != nil {
		relaunchPending.Store(false)
		log.Printf("[relaunch] cannot reopen: %v", err)
		return err
	}
	if err := startRelaunchAfterExit(os.Getpid(), bundle, relaunchEnv(), a.relaunchGaveUp); err != nil {
		relaunchPending.Store(false)
		log.Printf("[relaunch] could not schedule the reopen of %s: %v", bundle, err)
		return err
	}
	log.Printf("[relaunch] quitting; %s opens again once this process has exited", bundle)
	performRealQuit()
	return nil
}

// relaunchGaveUp runs when the waiting shell ends while this process is
// still alive: the quit stalled, nothing was opened, and a tap may try again.
func (a *App) relaunchGaveUp() {
	relaunchPending.Store(false)
	a.emitBrowserEvent(relaunchFailedEvent)
}
