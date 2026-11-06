package main

import (
	"io/fs"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// AboutInfo contains device and updater metadata, never keys or observed content.
type AboutInfo struct {
	Version   string `json:"version"`
	Build     string `json:"build"`
	BuildTime string `json:"buildTime"`
	// Commit names a staging build by its short commit; a release never has one.
	Commit              string       `json:"commit,omitempty"`
	Computer            string       `json:"computer"`
	OS                  string       `json:"os"`
	Phones              []string     `json:"phones"`
	DataBytes           int64        `json:"dataBytes"`
	BackgroundUpdates   bool         `json:"backgroundUpdates"`
	LastChecked         int64        `json:"lastChecked"`
	Later               bool         `json:"later"`
	ReleaseNotesVersion string       `json:"releaseNotesVersion"`
	ReleaseNotes        string       `json:"releaseNotes"`
	ReleaseNotesURL     string       `json:"releaseNotesUrl"`
	Update              UpdateStatus `json:"update"`
}

// GetAboutInfo supplies Settings > About without an update request.
func (a *App) GetAboutInfo() (AboutInfo, error) {
	out := AboutInfo{Version: Version, Build: "development", BuildTime: BuildTime,
		Computer: runtime.GOARCH, OS: runtime.GOOS, Phones: []string{},
		BackgroundUpdates: backgroundUpdatesEnabled(), Update: UpdateStatus{Status: "idle"},
		ReleaseNotesVersion: Version}
	if a.updater != nil {
		// Read the selected release and its status together. About never fetches
		// latest: the feed may have advanced beyond the verified download.
		a.updater.mu.Lock()
		out.Update = a.updater.status
		out.LastChecked = a.updater.lastChecked
		out.Later = a.updater.later
		if selected := a.updater.info; selected.Latest != "" && (selected.Available || selected.Latest == strings.TrimPrefix(Version, "v")) {
			out.ReleaseNotesVersion = selected.Latest
			out.ReleaseNotes = selected.ReleaseNotes
		}
		a.updater.mu.Unlock()
	}
	out.ReleaseNotesURL = "https://github.com/" + desktopReleasesRepo + "/releases/tag/" +
		url.PathEscape("v"+strings.TrimPrefix(out.ReleaseNotesVersion, "v"))
	if appFlavor() == appFlavorIsoform && !isReleaseChannel() {
		out.Update = UpdateStatus{Status: "disabled"}
	}
	if isReleaseChannel() {
		out.Build = "release"
	} else if a.GetEnvironment().IsStaging {
		out.Build = "staging"
	}
	out.Commit = aboutStagingCommit(out.Build)
	if runtime.GOOS == "darwin" {
		if raw, err := exec.Command("/usr/sbin/sysctl", "-n", "hw.model").Output(); err == nil {
			out.Computer = strings.TrimSpace(string(raw))
		}
		if raw, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output(); err == nil {
			out.OS = "macOS " + strings.TrimSpace(string(raw))
		}
	}
	if bridge := a.mobileBridgeForUse(); bridge != nil {
		for _, device := range bridge.connectionStatus().Devices {
			out.Phones = append(out.Phones, device.DeviceName)
		}
	}
	root, err := flavorDataDir()
	if err != nil {
		return out, err
	}
	// Lstat through WalkDir counts files without following any data-folder links.
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			if info, e := entry.Info(); e == nil {
				out.DataBytes += info.Size()
			}
		}
		return nil
	})
	return out, nil
}

// OpenDataFolder reveals Isoform's folder in the computer's file manager.
func (a *App) OpenDataFolder() error {
	root, err := flavorDataDir()
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", root)
	case "windows":
		cmd = exec.Command("explorer", root)
	default:
		cmd = exec.Command("xdg-open", root)
	}
	return cmd.Run()
}

// aboutStagingCommit is the short commit a staging build shows in Build (ST85).
// Only a staging build names its commit; a release build never does.
func aboutStagingCommit(build string) string {
	if build != "staging" {
		return ""
	}
	commit := strings.TrimSpace(GitCommit)
	if len(commit) > 8 {
		commit = commit[:8]
	}
	return commit
}
