package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"wails-gui/internal/pii"
)

// savedCopyStamp is the privacy stamp that saved copies (the finds view and the
// first stream page) are made under. It holds only what survives a restart:
//   - the durable privacy epoch (#588): every privacy change advances it, and it
//     is saved beside the briefs, so a restart reads the same value back;
//   - Closed Eyes, the sensitive apps and the observation pauses;
//   - the activity privacy counts (durable, in the database);
//   - the memory folder's signal, once the folder has settled.
//
// It deliberately leaves out the in-memory counters (the brief read epoch, the
// memory write revision, the context-use epoch). They reset on launch, so a
// copy made before a restart would never match after it, and every launch would
// drop every copy. Withdrawals reach the durable epoch through briefPrivacyChanged.
//
// Returns "" when any part cannot be read, which means no copy is served.
func (a *App) savedCopyStamp(ctx context.Context) string {
	dir, err := mobileBriefStateDir()
	if err != nil || dir == "" {
		return ""
	}
	// Load the saved epoch before reading it, so the first read after a launch
	// compares against the value saved before the restart.
	adoptMobileBriefPrivacyEpoch(filepath.Join(dir, mobileBriefDirName))
	snapshot, err := a.memorySnapshot()
	if err != nil {
		return ""
	}
	pauses, err := a.observationPausesContext(ctx)
	if err != nil && a.store != nil {
		return ""
	}
	activity, err := a.mobileBriefActivityPrivacyStamp(ctx, time.Now().UnixMilli())
	if err != nil {
		return ""
	}
	return mobileBriefModelDigest(struct {
		PrivacyEpoch  uint64
		ClosedEyes    bool
		SensitiveApps map[string]string
		Pauses        []observationPause
		Activity      [2]int64
		Memory        string
	}{
		PrivacyEpoch:  mobileBriefPrivacyEpoch.Load(),
		ClosedEyes:    a.IsClosedEyesModeEnabled(),
		SensitiveApps: pii.GetUserSensitiveApps(),
		Pauses:        pauses,
		Activity:      [2]int64{activity.sessions, activity.protected},
		Memory:        durableMemoryIdentity(snapshot),
	})
}

// durableMemoryIdentity names the memory folder by its signal, which survives a
// restart. A folder still settling is named by its pointer, so its stamp never
// holds while it is changing within the same second.
func durableMemoryIdentity(snapshot *memoryExternalSnapshot) string {
	if snapshot == nil {
		return ""
	}
	if snapshot.settled {
		return fmt.Sprintf("settled|%s|%x", snapshot.dirKey, snapshot.signal)
	}
	return fmt.Sprintf("unsettled|%p", snapshot)
}
