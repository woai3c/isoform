package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
)

const (
	ComputerUseAppAccessPolicyVersion = "computer-use-app-access-v1"
	ComputerUseAppAccessMode          = "computer_use_only"
	managedComposioAppAccessEnabled   = false
)

type ComputerUseAppDefinitionView struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Category     string `json:"category"`
	EntryURL     string `json:"entryUrl,omitempty"`
	NativeBundle string `json:"nativeBundle,omitempty"`
}

type ComputerUseAppAccessSnapshotView struct {
	PolicyVersion   string                         `json:"policyVersion"`
	Mode            string                         `json:"mode"`
	OAuthFallback   bool                           `json:"oauthFallback"`
	Available       bool                           `json:"available"`
	Accessibility   bool                           `json:"accessibility"`
	ScreenRecording bool                           `json:"screenRecording"`
	ScreenLocked    bool                           `json:"screenLocked"`
	Status          string                         `json:"status"`
	StatusReason    string                         `json:"statusReason"`
	Apps            []ComputerUseAppDefinitionView `json:"apps"`
}

type ComputerUseBrowserProbeView struct {
	Bundle       string   `json:"bundle"`
	Name         string   `json:"name"`
	PID          int      `json:"pid,omitempty"`
	Active       bool     `json:"active"`
	Running      bool     `json:"running"`
	UseCount     int      `json:"useCount,omitempty"`
	WindowTitles []string `json:"windowTitles,omitempty"`
}

type ComputerUseAppAccessProbeView struct {
	Snapshot    ComputerUseAppAccessSnapshotView `json:"snapshot"`
	ProbedAt    int64                            `json:"probedAt"`
	Browsers    []ComputerUseBrowserProbeView    `json:"browsers"`
	ReceiptHash string                           `json:"receiptHash"`
}

var computerUseAppCatalog = []ComputerUseAppDefinitionView{
	{ID: "gmail", Label: "Gmail", Category: "communication", EntryURL: "https://mail.google.com/"},
	{ID: "google-calendar", Label: "Google Calendar", Category: "planning", EntryURL: "https://calendar.google.com/"},
	{ID: "google-drive", Label: "Google Drive", Category: "documents", EntryURL: "https://drive.google.com/"},
	{ID: "google-docs", Label: "Google Docs", Category: "documents", EntryURL: "https://docs.google.com/"},
	{ID: "slack", Label: "Slack", Category: "communication", EntryURL: "https://app.slack.com/", NativeBundle: "com.tinyspeck.slackmacgap"},
	{ID: "notion", Label: "Notion", Category: "documents", EntryURL: "https://www.notion.so/", NativeBundle: "notion.id"},
	{ID: "github", Label: "GitHub", Category: "development", EntryURL: "https://github.com/"},
	{ID: "linear", Label: "Linear", Category: "planning", EntryURL: "https://linear.app/", NativeBundle: "com.linear"},
	{ID: "microsoft-teams", Label: "Microsoft Teams", Category: "communication", EntryURL: "https://teams.microsoft.com/", NativeBundle: "com.microsoft.teams2"},
}

type computerUseBrowserDefinition struct {
	Bundle string
	Name   string
}

var computerUseBrowserCatalog = []computerUseBrowserDefinition{
	{Bundle: "com.apple.Safari", Name: "Safari"},
	{Bundle: "com.google.Chrome", Name: "Google Chrome"},
	{Bundle: "company.thebrowser.Browser", Name: "Arc"},
	{Bundle: "com.microsoft.edgemac", Name: "Microsoft Edge"},
	{Bundle: "com.brave.Browser", Name: "Brave Browser"},
}

func computerUseAppDefinitions() []ComputerUseAppDefinitionView {
	result := make([]ComputerUseAppDefinitionView, len(computerUseAppCatalog))
	copy(result, computerUseAppCatalog)
	return result
}

func computerUsePermissionFlags() (bool, bool) {
	var accessibility, screenRecording bool
	for _, item := range getPlatformPermissions().Items {
		switch item.Key {
		case "accessibility":
			accessibility = item.Granted
		case "screen_recording":
			screenRecording = item.Granted
		}
	}
	return accessibility, screenRecording
}

func (a *App) computerUseAppAccessSnapshot() ComputerUseAppAccessSnapshotView {
	accessibility, screenRecording := computerUsePermissionFlags()
	available := a != nil && a.cuaDriver != nil && activitymon.ComputerUseSupported()
	locked := activitymon.IsScreenLocked()
	status := "ready"
	reason := "Isoform can reach supported app and browser sessions. Sign in through each app normally when needed; no provider connection is required."
	switch {
	case !available:
		status = "unavailable"
		reason = "Computer Use is unavailable on this device."
	case !accessibility:
		status = "permission_required"
		reason = "Enable Accessibility for Isoform before it can inspect or operate apps."
	case locked:
		status = "locked"
		reason = "Unlock this computer before Isoform reads or operates an app."
	case !screenRecording:
		status = "read_only_ready"
		reason = "Accessibility reads are available. Enable Screen Recording for visual fallback in apps with incomplete accessibility trees."
	}
	return ComputerUseAppAccessSnapshotView{
		PolicyVersion:   ComputerUseAppAccessPolicyVersion,
		Mode:            ComputerUseAppAccessMode,
		OAuthFallback:   false,
		Available:       available,
		Accessibility:   accessibility,
		ScreenRecording: screenRecording,
		ScreenLocked:    locked,
		Status:          status,
		StatusReason:    reason,
		Apps:            computerUseAppDefinitions(),
	}
}

func (a *App) GetComputerUseAppAccessSnapshot() ComputerUseAppAccessSnapshotView {
	return a.computerUseAppAccessSnapshot()
}

func computerUseBrowserBundle(bundle string) bool {
	bundle = strings.TrimSpace(bundle)
	for _, candidate := range computerUseBrowserCatalog {
		if bundle == candidate.Bundle {
			return true
		}
	}
	return false
}

func (a *App) ProbeComputerUseAppAccess() (ComputerUseAppAccessProbeView, error) {
	snapshot := a.computerUseAppAccessSnapshot()
	result := ComputerUseAppAccessProbeView{
		Snapshot: snapshot,
		ProbedAt: time.Now().UnixMilli(),
		Browsers: []ComputerUseBrowserProbeView{},
	}
	if !snapshot.Available {
		return result, errors.New(snapshot.StatusReason)
	}
	if !snapshot.Accessibility {
		return result, errors.New(snapshot.StatusReason)
	}
	if snapshot.ScreenLocked {
		return result, errors.New(snapshot.StatusReason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cuaDriverCallTimeout+5*time.Second)
	defer cancel()
	response, err := a.cuaDriver.Call(ctx, "list_apps", map[string]any{"days": 14})
	if err != nil {
		return result, err
	}
	if response == nil || !response.OK {
		if response == nil {
			return result, errors.New("Computer Use returned no app inventory")
		}
		return result, errors.New(response.ErrorMessage())
	}
	var payload struct {
		Apps []struct {
			Bundle   string `json:"bundle"`
			Name     string `json:"name"`
			PID      int    `json:"pid"`
			Active   bool   `json:"active"`
			Running  bool   `json:"running"`
			UseCount int    `json:"use_count"`
			Windows  []struct {
				Title string `json:"title"`
			} `json:"windows"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(response.Result, &payload); err != nil {
		return result, err
	}
	for _, app := range payload.Apps {
		if !computerUseBrowserBundle(app.Bundle) {
			continue
		}
		browser := ComputerUseBrowserProbeView{
			Bundle:   app.Bundle,
			Name:     app.Name,
			PID:      app.PID,
			Active:   app.Active,
			Running:  app.Running,
			UseCount: app.UseCount,
		}
		for _, window := range app.Windows {
			if title := strings.TrimSpace(window.Title); title != "" {
				browser.WindowTitles = append(browser.WindowTitles, agencyBoundedText(title, 240))
			}
		}
		result.Browsers = append(result.Browsers, browser)
	}
	sort.SliceStable(result.Browsers, func(i, j int) bool {
		if result.Browsers[i].Running != result.Browsers[j].Running {
			return result.Browsers[i].Running
		}
		if result.Browsers[i].UseCount != result.Browsers[j].UseCount {
			return result.Browsers[i].UseCount > result.Browsers[j].UseCount
		}
		return result.Browsers[i].Name < result.Browsers[j].Name
	})
	canonical, _ := json.Marshal(result.Browsers)
	result.ReceiptHash = agencyPayloadHash(canonical)
	return result, nil
}
