package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

// The status loop must not wait behind a recorder's transcription drain.
// A transition in progress reports no active microphone until the next poll.
func (a *App) observationAudioStatus(paused bool) BackgroundAudioStatus {
	if paused {
		return BackgroundAudioStatus{}
	}
	locked := a.isBackgroundListeningScreenLocked()
	if !a.backgroundAudioMu.TryLock() {
		return BackgroundAudioStatus{ScreenLocked: locked}
	}
	defer a.backgroundAudioMu.Unlock()
	return a.backgroundAudioStatusLocked(locked)
}

func observationDoingNow(view DoingNow, front activitymon.AppInfo, audio BackgroundAudioStatus, inCall bool) DoingNow {
	view.Microphone = "off"
	view.InCall = inCall
	if view.Kind == doingNowPaused {
		view.Microphone, view.InCall = "paused", false
		return view
	}
	if audio.MicrophonePaused || audio.ScreenLocked || audio.SuspendedReason == backgroundAudioSuspendedPrivacyPause {
		view.Microphone = "paused"
	} else if audio.Running {
		for _, source := range audio.ActiveSources {
			if source == string(backgroundListeningSourceMicrophone) {
				view.Microphone = "listening"
			}
		}
	}
	if view.Kind == doingNowWatching || view.Kind == doingNowWorking {
		view.FrontAppHidden = observationAppHidden(front.AppName, front.BundleID) || activitymon.IsPrivateBrowsing(front.WindowTitle) ||
			(activitymon.IsBrowser(front.BundleID) && strings.TrimSpace(front.WindowTitle) == "")
		if !view.FrontAppHidden {
			view.FrontApp = front.AppName
		}
	}
	return view
}

func observationAppHidden(name, bundleID string) bool {
	_, sensitive := pii.IsSensitiveApp(bundleID)
	return sensitive || pii.IsSensitiveAppName(name) || isSystemNoiseApp(name, bundleID)
}

type AppSuggestion struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
	ActiveMs int64  `json:"activeMs"`
	Category string `json:"category"`
}

// Only installed-app metadata is cached; observed usage and privacy decisions
// are recomputed for every read. The lock also coalesces concurrent scans.
type observationCategoryCache struct {
	mu         sync.Mutex
	dayStart   int64
	categories map[string]string
}

// GetPrivateAppSuggestions returns up to five eligible apps used today, through current privacy gates.
func (a *App) GetPrivateAppSuggestions() ([]AppSuggestion, error) {
	return a.privateAppSuggestions(time.Now(), listSystemApps, readObservationAppCategory)
}

func (a *App) privateAppSuggestions(now time.Time, enumerate func() []SystemApp, category func(SystemApp) string) ([]AppSuggestion, error) {
	empty := []AppSuggestion{}
	if a == nil || a.IsClosedEyesModeEnabled() || a.activity == nil || a.activity.store == nil {
		return empty, nil
	}
	start, _ := captureCountWindow(0, now)
	store := a.activity.store
	sessions, err := store.modelSessionsOverlapping(start.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return nil, err
	}
	usage := map[string][]AppUsageStat{}
	for _, session := range sessions {
		allowed, err := a.activitySessionAllowsWithPolicy(store, session, policy)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		rows, err := store.reader().Query(`SELECT data FROM activity_events WHERE session_id = ? AND kind = 'app_switch' AND timestamp < ? ORDER BY timestamp`, session.ID, now.UnixMilli())
		if err != nil {
			return nil, err
		}
		var events []activitymon.ActivityEvent
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var event activitymon.ActivityEvent
			if json.Unmarshal([]byte(raw), &event) == nil {
				events = append(events, event)
			}
		}
		readErr := rows.Err()
		rows.Close()
		if readErr != nil {
			return nil, readErr
		}
		for i, event := range events {
			end := min(session.EndTime, now.UnixMilli(), event.Timestamp+5*60*1000)
			if i+1 < len(events) {
				end = min(end, events[i+1].Timestamp)
			}
			active := end - max(event.Timestamp, start.UnixMilli())
			if active > 0 && !event.PrivateWindow && !activitymon.IsPrivateBrowsing(event.WindowTitle) {
				usage[session.ID] = append(usage[session.ID], AppUsageStat{AppName: event.AppName, BundleID: event.AppBundleID, ActiveMs: active})
			}
		}
	}
	if len(usage) == 0 {
		return empty, nil
	}
	cache := &a.observationCategories
	cache.mu.Lock()
	if cache.dayStart != start.UnixMilli() || cache.categories == nil {
		cache.dayStart = start.UnixMilli()
		cache.categories = map[string]string{}
		for _, app := range enumerate() {
			cache.categories[strings.ToLower(app.BundleID)] = category(app)
		}
	}
	categories := cache.categories
	cache.mu.Unlock()

	// Enumeration may be slow: revalidate every contributing source after it.
	policy, err = a.activityReadPrivacyPolicy()
	if err != nil {
		return nil, err
	}
	var apps []AppUsageStat
	for _, session := range sessions {
		if len(usage[session.ID]) == 0 {
			continue
		}
		current, err := store.GetSessionByID(session.ID)
		if err != nil {
			return nil, err
		}
		if current == nil {
			continue
		}
		allowed, err := a.activitySessionAllowsWithPolicy(store, *current, policy)
		if err != nil {
			return nil, err
		}
		if allowed {
			apps = append(apps, usage[session.ID]...)
		}
	}
	if a.IsClosedEyesModeEnabled() || !pii.TryLockSensitiveAppsForPublication() {
		return empty, nil
	}
	defer pii.UnlockSensitiveAppsForPublication()
	suggestions := selectPrivateAppSuggestions(apps, categories)
	if a.IsClosedEyesModeEnabled() {
		return empty, nil
	}
	return suggestions, nil
}

func selectPrivateAppSuggestions(apps []AppUsageStat, categories map[string]string) []AppSuggestion {
	byBundle := map[string]AppSuggestion{}
	for _, app := range apps {
		id := strings.ToLower(strings.TrimSpace(app.BundleID))
		name := strings.TrimSpace(app.AppName)
		if id == "" || name == "" || app.ActiveMs <= 0 || observationAppHidden(name, id) || observationIsSelf(name, id) {
			continue
		}
		category := categories[id]
		// The existing registry recognises messaging/mail bundle IDs without
		// relying on a captured window title or an app's display name.
		if topicPeopleApp("", id) {
			category = "messaging"
		}
		if category != "finance" && category != "health" && category != "messaging" {
			continue
		}
		row := byBundle[id]
		row.Name, row.BundleID, row.Category = name, app.BundleID, category
		row.ActiveMs += app.ActiveMs
		byBundle[id] = row
	}
	out := make([]AppSuggestion, 0, len(byBundle))
	for _, app := range byBundle {
		out = append(out, app)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ActiveMs != out[j].ActiveMs {
			return out[i].ActiveMs > out[j].ActiveMs
		}
		return out[i].BundleID < out[j].BundleID
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func observationIsSelf(name, bundleID string) bool {
	switch strings.ToLower(name) {
	case "isoform", "yansu", "isoform dev", "yansu desktop":
		return true
	}
	return strings.HasPrefix(bundleID, "com.wails.isoform") || strings.HasPrefix(bundleID, "com.wails.yansu")
}
