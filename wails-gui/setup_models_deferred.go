package main

import (
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/models"
)

// Later on setup's "Runs on this computer" (handoff-2026-10-29-1523-setup-v2.2.0
// §2): the confirmed models wait until the computer is plugged in and idle,
// then download like any confirmed job. Settings › Models shows them as
// "Waiting for power and idle" until then.

const (
	// deferredModelIdle is how long no keyboard or mouse input counts as idle.
	deferredModelIdle = 5 * time.Minute
	// deferredModelPoll is how often the power source and idle time are read.
	deferredModelPoll = time.Minute
)

// deferredModelReady reports a computer on AC power that has been idle for
// deferredModelIdle. An unknown power source is not plugged in.
func deferredModelReady(power activitymon.PowerState, idle time.Duration) bool {
	return power.Known && !power.OnBattery && idle >= deferredModelIdle
}

// releaseDeferredModels polls until the manager stops. Each tick that finds
// deferred jobs and a ready computer releases them; the jobs then admit
// against the network and disk as usual.
func releaseDeferredModels(manager *models.Manager, every time.Duration, ready func() bool) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-manager.Done():
			return
		case <-ticker.C:
			if manager.HasDeferred() && ready() {
				manager.ReleaseDeferred()
			}
		}
	}
}

func (a *App) watchDeferredModels(manager *models.Manager) {
	go releaseDeferredModels(manager, deferredModelPoll, func() bool {
		return deferredModelReady(activitymon.PowerSource(), activitymon.IdleTime())
	})
}

// PlanSetupModelDownload plans setup's "Runs on this computer" choice: the given
// choices, with the downloads for skipJobs left out (a row switched off).
func (a *App) PlanSetupModelDownload(choices map[string]string, skipJobs []string) (models.Plan, error) {
	m, err := a.setupModelManager()
	if err != nil {
		return models.Plan{}, err
	}
	return m.BuildPlanSkipping(choices, nil, skipJobs)
}

// DeferModelDownloads confirms a reviewed plan for when the computer is
// plugged in and idle.
func (a *App) DeferModelDownloads(plan models.Plan) error {
	m, err := a.setupModelManager()
	if err != nil {
		return err
	}
	m.MonitorNetwork()
	return m.Defer(plan)
}
