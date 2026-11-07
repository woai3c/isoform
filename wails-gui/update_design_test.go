package main

import (
	"runtime"
	"testing"
)

// A failed probe marks the staged version Restart-now-only, and the mark
// survives a restart through pending.json.
func TestProbeFailureMarksRestartNeededAndPersistsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probes")
	}
	f := newPreparedUpdateFixture(t)
	p := f.prepare(t)
	writeProbeCandidate(t, p.candidate(), "exit 3")
	steps := f.steps(nil)
	steps.probe = probeAt
	if err := f.app.installPreparedOnQuit(f.bundle, steps); err == nil {
		t.Fatal("refused probe accepted")
	}
	if !f.app.updater.restartNeeded {
		t.Fatal("probe failure not marked in memory")
	}
	next := &App{updater: &updater{}}
	if !next.restorePendingUpdate() || !next.updater.restartNeeded || !next.GetUpdateStatus().RestartNeeded {
		t.Fatalf("flag lost across launch: %+v", next.updater)
	}
}

// While a newer release downloads behind a ready one, no progress reaches the
// cards: the ready update stays as it was.
func TestNewerDownloadBehindReadyUpdateShowsNoProgress(t *testing.T) {
	app := &App{updater: &updater{status: UpdateStatus{Status: "update-downloaded", Version: "0.2.0"}, quietDownload: true}}
	app.broadcastUpdateStatus(UpdateStatus{Status: "downloading", Percent: 42})
	if got := app.GetUpdateStatus(); got.Status != "update-downloaded" || got.Version != "0.2.0" {
		t.Fatalf("progress shown behind a ready update: %+v", got)
	}
	app.updater.quietDownload = false
	app.broadcastUpdateStatus(UpdateStatus{Status: "downloading", Percent: 42})
	if got := app.GetUpdateStatus(); got.Status != "downloading" {
		t.Fatalf("progress hidden when nothing is ready: %+v", got)
	}
}
