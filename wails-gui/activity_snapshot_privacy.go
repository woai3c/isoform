package main

import (
	"encoding/json"
	"strings"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/platform/browserhistory"
)

// Retention preserves source identity, not the page content, title, URL path,
// query or credentials. URLs exist only while checking a live snapshot.
type activitySnapshotPrivacyIdentity struct {
	Apps    []string `json:"apps,omitempty"`
	Hosts   []string `json:"hosts,omitempty"`
	Private bool     `json:"private,omitempty"`
	URLs    []string `json:"-"`
}

func readActivitySnapshotPrivacyIdentity(app, browserURL, parsed string) (activitySnapshotPrivacyIdentity, error) {
	identity := activitySnapshotPrivacyIdentity{Apps: []string{app}, URLs: []string{browserURL}}
	if strings.TrimSpace(parsed) != "" {
		var content struct {
			URL      string            `json:"url"`
			Title    string            `json:"title"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(parsed), &content); err != nil {
			return identity, err
		}
		identity.Apps = append(identity.Apps, content.Metadata["app"], content.Metadata["bundleId"])
		identity.URLs = append(identity.URLs, content.URL)
		identity.Private = activitymon.IsPrivateBrowsing(content.Title)
	}
	for _, rawURL := range identity.URLs {
		if host := browserhistory.Host(rawURL); host != "" {
			identity.Hosts = append(identity.Hosts, host)
		}
	}
	identity.Apps = compactStrings(identity.Apps)
	identity.Hosts = compactStrings(identity.Hosts)
	return identity, nil
}
