package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMobileRelayURLValidation(t *testing.T) {
	accepted := map[string]string{
		"https://relay.example":  "https://relay.example",
		"https://relay.example/": "https://relay.example",
		"http://127.0.0.1:8787":  "http://127.0.0.1:8787",
		"http://localhost:8787":  "http://localhost:8787",
	}
	for raw, want := range accepted {
		got, err := validateMobileRelayURL(raw)
		if err != nil || got != want {
			t.Fatalf("%s: got %q, %v; want %q", raw, got, err, want)
		}
	}
	rejected := []string{
		"", "http://relay.example", "http://[::1]:8787", "ws://relay.example", "ftp://relay.example",
		"https://", "https://user@relay.example", "https://relay.example/path",
		"https://relay.example?token=1", "https://relay.example#fragment",
	}
	for _, raw := range rejected {
		if _, err := validateMobileRelayURL(raw); err == nil {
			t.Fatalf("%q was accepted", raw)
		}
	}
}

func TestMobileHubIdentityIsPrivateStableAndVerified(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mobile")
	if identity, err := loadMobileHubIdentity(dir); err != nil || identity != nil {
		t.Fatal("reading a missing identity created one")
	}
	created, err := createMobileHubIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Credential) != 43 || !mobileValidUUID(created.HubID) {
		t.Fatalf("identity has the wrong shape: %+v", created.HubID)
	}
	for path, mode := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, "hub-identity.json"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s is not owner-only: %v", path, err)
		}
	}
	loaded, err := loadMobileHubIdentity(dir)
	if err != nil || loaded == nil {
		t.Fatalf("identity did not reload: %v", err)
	}
	if loaded.HubID != created.HubID || loaded.Credential != created.Credential || string(loaded.Public.Bytes()) != string(created.Public.Bytes()) {
		t.Fatal("identity changed across a reload")
	}
	path := filepath.Join(dir, "hub-identity.json")
	var file mobileHubIdentityFile
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := mobileDecodeStrict(raw, &file); err != nil {
		t.Fatal(err)
	}
	file.PublicKey = strings.Repeat("A", 43)
	if err := writeMobilePrivateJSON(path, file); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMobileHubIdentity(dir); err == nil {
		t.Fatal("an identity whose public key does not match its private key was accepted")
	}
}

func TestMobileEffectiveRelayURLPolicy(t *testing.T) {
	previousRelay := DefaultMobileRelayURL
	previousRemoteAccess := DefaultMobileRemoteAccess
	t.Cleanup(func() {
		DefaultMobileRelayURL = previousRelay
		DefaultMobileRemoteAccess = previousRemoteAccess
	})
	DefaultMobileRelayURL = ""
	DefaultMobileRemoteAccess = ""
	t.Setenv("ISOFORM_RELAY_URL", "")
	t.Setenv("YANSU_RELAY_URL", "")
	t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", "0")
	if got, err := mobileEffectiveRelayURL(""); err != nil || got != mobileDefaultRelayURL {
		t.Fatalf("default relay: %q %v", got, err)
	}
	t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", "1")
	if got, err := mobileEffectiveRelayURL(""); err != nil || got != "" {
		t.Fatalf("opted-in remote access: %q %v", got, err)
	}
	t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", "")
	DefaultMobileRelayURL = " https://stamped.example "
	if got, err := mobileEffectiveRelayURL(""); err != nil || got != "https://stamped.example" {
		t.Fatalf("build-time relay: %q %v", got, err)
	}
	t.Setenv("ISOFORM_RELAY_URL", "http://127.0.0.1:9000")
	t.Setenv("YANSU_RELAY_URL", "http://127.0.0.1:9001")
	if got, err := mobileEffectiveRelayURL(""); err != nil || got != "http://127.0.0.1:9000" {
		t.Fatalf("current environment override must win: %q %v", got, err)
	}
	t.Setenv("ISOFORM_RELAY_URL", "")
	t.Setenv("YANSU_RELAY_URL", "http://127.0.0.1:9001")
	if got, err := mobileEffectiveRelayURL(""); err != nil || got != "http://127.0.0.1:9001" {
		t.Fatalf("legacy development override: %q %v", got, err)
	}
	if got, err := mobileEffectiveRelayURL("https://saved.example"); err != nil || got != "https://saved.example" {
		t.Fatalf("saved relay must win: %q %v", got, err)
	}
	t.Setenv("ISOFORM_RELAY_URL", "")
	t.Setenv("YANSU_RELAY_URL", "")
}

func TestMobileRemoteAccessModeDetection(t *testing.T) {
	previousRelay := DefaultMobileRelayURL
	previousRemoteAccess := DefaultMobileRemoteAccess
	t.Cleanup(func() {
		DefaultMobileRelayURL = previousRelay
		DefaultMobileRemoteAccess = previousRemoteAccess
	})
	DefaultMobileRelayURL = ""
	DefaultMobileRemoteAccess = ""
	t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", "1")
	t.Setenv("ISOFORM_RELAY_URL", "")
	t.Setenv("YANSU_RELAY_URL", "")
	if !mobileRemoteAccessMode("") {
		t.Fatal("explicitly enabled remote mode was not selected")
	}
	if mobileRemoteAccessMode("https://saved.example") {
		t.Fatal("saved URL selected remote mode")
	}
	t.Setenv("ISOFORM_RELAY_URL", "https://env.example")
	if mobileRemoteAccessMode("") {
		t.Fatal("environment URL selected remote mode")
	}
	t.Setenv("ISOFORM_RELAY_URL", "")
	t.Setenv("YANSU_RELAY_URL", "https://legacy-env.example")
	if mobileRemoteAccessMode("") {
		t.Fatal("legacy environment URL selected remote mode")
	}
	t.Setenv("YANSU_RELAY_URL", "")
	DefaultMobileRelayURL = "https://stamped.example"
	if mobileRemoteAccessMode("") {
		t.Fatal("stamped URL selected remote mode")
	}
}

func TestMobileRemoteAccessGatePrecedence(t *testing.T) {
	previousRelay := DefaultMobileRelayURL
	previousRemoteAccess := DefaultMobileRemoteAccess
	t.Cleanup(func() {
		DefaultMobileRelayURL = previousRelay
		DefaultMobileRemoteAccess = previousRemoteAccess
	})

	gateEnvironments := []struct {
		name  string
		value string
	}{
		{name: "unset"},
		{name: "enabled", value: "1"},
		{name: "disabled", value: "0"},
	}
	gateStamps := []struct {
		name  string
		value string
	}{
		{name: "unstamped"},
		{name: "stamped", value: "1"},
		{name: "opted-out", value: "0"},
	}
	relayConfigurations := []struct {
		name  string
		saved string
	}{
		{name: "unconfigured"},
		{name: "saved", saved: "https://saved.example"},
	}

	for _, environment := range gateEnvironments {
		for _, stamp := range gateStamps {
			for _, relay := range relayConfigurations {
				t.Run(environment.name+"/"+stamp.name+"/"+relay.name, func(t *testing.T) {
					t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", environment.value)
					t.Setenv("ISOFORM_RELAY_URL", "")
					t.Setenv("YANSU_RELAY_URL", "")
					DefaultMobileRelayURL = ""
					DefaultMobileRemoteAccess = stamp.value

					wantEnabled := stamp.value != "0"
					switch environment.value {
					case "1":
						wantEnabled = true
					case "0":
						wantEnabled = false
					}
					if got := mobileRemoteAccessEnabled(); got != wantEnabled {
						t.Fatalf("mobileRemoteAccessEnabled() = %t, want %t", got, wantEnabled)
					}
					wantRemoteMode := relay.saved == "" && wantEnabled
					if got := mobileRemoteAccessMode(relay.saved); got != wantRemoteMode {
						t.Fatalf("mobileRemoteAccessMode(%q) = %t, want %t", relay.saved, got, wantRemoteMode)
					}
				})
			}
		}
	}
}

func TestMobileRemoteAccessOffReason(t *testing.T) {
	previousRelay := DefaultMobileRelayURL
	previousRemoteAccess := DefaultMobileRemoteAccess
	t.Cleanup(func() {
		DefaultMobileRelayURL = previousRelay
		DefaultMobileRemoteAccess = previousRemoteAccess
	})

	cases := []struct {
		name, gate, saved, environmentRelay, buildRelay, want string
	}{
		{name: "remote mode", want: ""},
		{name: "saved relay", gate: "0", saved: " https://saved.example ", want: "saved_relay"},
		{name: "environment relay", gate: "0", environmentRelay: "https://env.example", want: "environment_relay"},
		{name: "build relay", gate: "0", buildRelay: "https://build.example", want: "build_relay"},
		{name: "disabled", gate: "0", want: "disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			DefaultMobileRelayURL = tc.buildRelay
			DefaultMobileRemoteAccess = ""
			t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", tc.gate)
			t.Setenv("ISOFORM_RELAY_URL", tc.environmentRelay)
			t.Setenv("YANSU_RELAY_URL", "")
			if got := mobileRemoteAccessOffReason(tc.saved); got != tc.want {
				t.Fatalf("mobileRemoteAccessOffReason(%q) = %q, want %q", tc.saved, got, tc.want)
			}
		})
	}
}

func TestMobileHubNameHasNoBonjourSuffixAndFitsTheLimit(t *testing.T) {
	name := mobileHubName()
	if name == "" || len(name) > mobileHubNameLimit || strings.HasSuffix(name, ".local") {
		t.Fatalf("hub name %q", name)
	}
	if got := mobileTruncateUTF8(strings.Repeat("\u00e9", 100), 121); len(got) > 121 || len(got) == 0 {
		t.Fatalf("truncation split a rune: %d bytes", len(got))
	}
}

// P140: the phone shows the name the owner gave the Mac. The host name is used only when no Computer Name is set
// or it cannot be read, and then it loses its Bonjour suffix.
func TestMobileHubNamePrefersTheComputerNameAndFallsBackToTheHostname(t *testing.T) {
	saved := mobileComputerNameFunc
	t.Cleanup(func() { mobileComputerNameFunc = saved })

	mobileComputerNameFunc = func() (string, error) { return "Bo's MacBook Pro\n", nil }
	if got := mobileHubName(); got != "Bo's MacBook Pro" {
		t.Fatalf("hub name = %q, want the Computer Name with its words kept", got)
	}

	host, _ := os.Hostname()
	wantHost := strings.TrimSuffix(strings.TrimSpace(host), ".local")
	for name, reader := range map[string]func() (string, error){
		"unreadable": func() (string, error) { return "", errors.New("scutil unavailable") },
		"unset":      func() (string, error) { return " \n", nil },
	} {
		mobileComputerNameFunc = reader
		got := mobileHubName()
		if strings.HasSuffix(got, ".local") {
			t.Fatalf("%s: hub name %q keeps the Bonjour suffix", name, got)
		}
		if wantHost != "" && got != mobileTruncateUTF8(wantHost, mobileHubNameLimit) {
			t.Fatalf("%s: hub name = %q, want the host name %q", name, got, wantHost)
		}
		if got == "" {
			t.Fatalf("%s: hub name is empty", name)
		}
	}
}
