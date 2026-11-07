package main

import (
	"regexp"
	"strings"
	"time"
)

// Update events carry closed values and version strings only. The allowlist
// in productTelemetryProperties enforces the same shapes; these helpers build
// the properties the callers send.

const (
	updateHowQuit       = "quit"
	updateHowRestartNow = "restart_now"
)

var updateVersionPattern = regexp.MustCompile(`^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}(-[0-9A-Za-z.]{1,16})?$`)

// updateErrorClass maps a check error to a closed class. The message text is
// never sent: it can carry URLs or service detail.
func updateErrorClass(msg string) string {
	switch {
	case strings.Contains(msg, "request failed"), strings.HasPrefix(msg, "Failed to check"):
		return "network"
	case strings.Contains(msg, "invalid response"), strings.Contains(msg, "Invalid response"),
		strings.Contains(msg, "No R2 update asset"), strings.Contains(msg, "feed"):
		return "feed"
	case strings.Contains(msg, "returned "):
		return "http"
	}
	return "other"
}

// recordUpdateCheck reports one release check: none, found, or error with its class.
func (a *App) recordUpdateCheck(info UpdateInfo) {
	if !isReleaseChannel() {
		return
	}
	props := map[string]any{"result": "none"}
	switch {
	case info.Error != "":
		props["result"] = "error"
		props["error_class"] = updateErrorClass(info.Error)
	case info.Available:
		props["result"] = "found"
	}
	a.telemetry.Capture("update.checked", props)
}

// recordQuitProbe reports the pre-swap probe of a quit install: passed or
// failed, and the reason class from probeOutcome.
func (a *App) recordQuitProbe(passed bool, reason string) {
	result := "failed"
	if passed {
		result = "passed"
	}
	a.telemetry.Capture("update.quit_probe", map[string]any{"result": result, "reason": reason})
}

// recordUpdateRolledBack reports a quit install that did not start and was
// restored: from is the failed version, to is the restored one.
func recordUpdateRolledBack(telemetry interface {
	Capture(string, map[string]any)
}, rec rolledBackRecord) {
	telemetry.Capture("update.rolled_back", map[string]any{"from": rec.From, "to": rec.To, "attempts": rec.Attempts})
}

// recordUpdateStaged reports a verified archive that is now waiting for the next quit.
func (a *App) recordUpdateStaged(to string) {
	if !updateVersionPattern.MatchString(to) {
		return
	}
	a.telemetry.Capture("update.staged", map[string]any{"from": Version, "to": to})
}

// recordUpdateInstalled reports an install that has swapped the bundle.
// stagedAt is unix milliseconds; the wait is whole days from staging to install.
func (a *App) recordUpdateInstalled(to, how string, stagedAt int64) {
	props := map[string]any{"from": Version, "to": to, "how": how}
	if stagedAt > 0 {
		days := int(time.Since(time.UnixMilli(stagedAt)) / (24 * time.Hour))
		if days >= 0 {
			props["waited_days"] = days
		}
	}
	a.telemetry.Capture("update.installed", props)
}

// recordUpdateRunning reports the first launch of the installed version. The
// release time comes from the feed's published_at when the feed has one.
func recordUpdateRunningEvent(telemetry interface {
	Capture(string, map[string]any)
}, rec installedRecord) {
	props := map[string]any{"version": Version, "how": rec.How}
	if rec.ReleasedAt > 0 {
		minutes := int(time.Since(time.UnixMilli(rec.ReleasedAt)) / time.Minute)
		if minutes >= 0 {
			props["release_to_run_minutes"] = minutes
		}
	}
	telemetry.Capture("update.running", props)
}

// parseReleaseTimeMillis reads the feed's RFC 3339 publish time. An absent or
// unreadable value is 0, and the release-to-running measure is then omitted.
func parseReleaseTimeMillis(value string) int64 {
	if value == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// updateTelemetryVersion accepts only a version-shaped string, so free text
// can never ride in a version property.
func updateTelemetryVersion(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && updateVersionPattern.MatchString(s)
}

func updateErrorClassAllowed(class string) bool {
	switch class {
	case "network", "http", "feed", "other":
		return true
	}
	return false
}

func updateProbeReasonAllowed(reason string) bool {
	switch reason {
	case probeReasonClean, probeReasonTimeout, probeReasonExitStatus, probeReasonSignal, probeReasonLaunchError:
		return true
	}
	return false
}

func updateHowAllowed(how string) bool {
	return how == updateHowQuit || how == updateHowRestartNow
}

// updateTelemetryInt accepts a whole number from 0 to max, in any numeric type a caller uses.
func updateTelemetryInt(v any, max int) (int, bool) {
	var n int
	switch x := v.(type) {
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		n = int(x)
	default:
		return 0, false
	}
	return n, n >= 0 && n <= max
}
