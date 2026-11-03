package main

import (
	"net/http"
	"time"

	"wails-gui/internal/engagement"
)

// serveNotificationPreferences changes only the phone's editable global rules.
// The existing kind route remains the single writer for individual switches.
func (bridge *mobileBridge) serveNotificationPreferences(w http.ResponseWriter, r *http.Request, device mobileDeviceRecord) {
	var input struct {
		IdempotencyKey string  `json:"idempotencyKey"`
		QuietStart     *string `json:"quietStart"`
		QuietEnd       *string `json:"quietEnd"`
	}
	if !mobileDecode(w, r, &input) {
		return
	}
	key, valid := mobileKey(device.DeviceID, input.IdempotencyKey)
	if !valid {
		mobileJSON(w, http.StatusBadRequest, nil, "an idempotency key of 1 to 128 bytes is required")
		return
	}
	if input.QuietStart == nil || input.QuietEnd == nil {
		mobileJSON(w, http.StatusBadRequest, nil, "quietStart and quietEnd are required")
		return
	}
	start, startOK := mobileNotificationClock(*input.QuietStart)
	end, endOK := mobileNotificationClock(*input.QuietEnd)
	if !startOK || !endOK {
		mobileJSON(w, http.StatusBadRequest, nil, "quiet hours must use HH:MM from 00:00 to 23:59")
		return
	}
	svc := bridge.app.engagementService()
	if svc == nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errEngagementUnavailable.Error())
		return
	}
	bridge.runReceipted(w, device, key, r.Method+" "+r.URL.Path, func(w http.ResponseWriter) {
		// Hold the service lock through persistence so a phone change cannot restore
		// an old interruption budget over a concurrent computer preference change.
		svc.mu.Lock()
		rules := svc.rulesRow()
		rules.QuietStartMinutes = int(start / time.Minute)
		rules.QuietEndMinutes = int(end / time.Minute)
		err := svc.store.SetEngagementRules(bridge.ctx, rules)
		if err == nil {
			svc.prefs.Quiet = engagement.QuietWindow{Start: start, End: end}
		}
		svc.mu.Unlock()
		if err != nil {
			mobileJSON(w, http.StatusInternalServerError, nil, "could not save the notification preferences")
			return
		}
		view, err := engagementSettingsView(bridge.ctx, svc)
		if err != nil {
			mobileJSON(w, http.StatusInternalServerError, nil, "could not read the notification settings")
			return
		}
		mobileJSON(w, http.StatusOK, view, "")
	})
}

func mobileNotificationClock(raw string) (time.Duration, bool) {
	if len(raw) != 5 || raw[2] != ':' {
		return 0, false
	}
	for index, char := range raw {
		if index != 2 && (char < '0' || char > '9') {
			return 0, false
		}
	}
	return engagementParseClock(raw)
}
