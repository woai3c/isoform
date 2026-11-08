package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// mobileDefaultRelayURL is the hosted relay used when no relay address is
// configured and remote access is not enabled.
const mobileDefaultRelayURL = "https://relay.yansu.app"

// DefaultMobileRelayURL is the build-time relay address, stamped by
// scripts/build-wails.sh with -X main.DefaultMobileRelayURL from
// ISOFORM_DESKTOP_RELAY_URL. It is the default both the desktop and, through
// the pairing invitation, the iPhone use. Empty falls back to
// mobileDefaultRelayURL.
var DefaultMobileRelayURL string

// DefaultMobileRemoteAccess is the build-time default for remote access over
// iroh. scripts/build-wails.sh stamps it from ISOFORM_DESKTOP_REMOTE_ACCESS;
// "0" opts out.
var DefaultMobileRemoteAccess string

var legacyMobileRelayURLWarning sync.Once

// mobileBuiltInRelayURL is the default before any user or environment override.
func mobileBuiltInRelayURL() string {
	if stamped := strings.TrimSpace(DefaultMobileRelayURL); stamped != "" {
		return stamped
	}
	return mobileDefaultRelayURL
}

func mobileRemoteAccessEnabled() bool {
	switch strings.TrimSpace(os.Getenv("ISOFORM_MOBILE_REMOTE_ACCESS")) {
	case "1":
		return true
	case "0":
		return false
	default:
		return strings.TrimSpace(DefaultMobileRemoteAccess) != "0"
	}
}

func mobileRelayURLFromEnvironment() string {
	if override := strings.TrimSpace(os.Getenv("ISOFORM_RELAY_URL")); override != "" {
		return override
	}
	if legacy := strings.TrimSpace(os.Getenv("YANSU_RELAY_URL")); legacy != "" {
		legacyMobileRelayURLWarning.Do(func() {
			log.Printf("deprecated environment variable YANSU_RELAY_URL; use ISOFORM_RELAY_URL")
		})
		return legacy
	}
	return ""
}

func mobileRemoteAccessMode(saved string) bool {
	return strings.TrimSpace(saved) == "" &&
		mobileRelayURLFromEnvironment() == "" &&
		strings.TrimSpace(DefaultMobileRelayURL) == "" &&
		mobileRemoteAccessEnabled()
}

func mobileRemoteAccessOffReason(saved string) string {
	if mobileRemoteAccessMode(saved) {
		return ""
	}
	switch {
	case strings.TrimSpace(saved) != "":
		return "saved_relay"
	case mobileRelayURLFromEnvironment() != "":
		return "environment_relay"
	case strings.TrimSpace(DefaultMobileRelayURL) != "":
		return "build_relay"
	default:
		return "disabled"
	}
}

const mobileHubNameLimit = 120

var (
	errMobileRelayURL = errors.New("the relay address must be an https address; http is allowed only for 127.0.0.1 or localhost during development")
)

// mobileHubIdentityFile is hub-identity.json (mode 0600). The credential and the
// private key stay on this Mac; the relay stores only the credential's hash.
type mobileHubIdentityFile struct {
	Version       int    `json:"version"`
	HubID         string `json:"hubId"`
	HubCredential string `json:"hubCredential"`
	PrivateKey    string `json:"privateKey"`
	PublicKey     string `json:"publicKey"`
	CreatedAt     string `json:"createdAt"`
}

// mobileHubIdentity is the usable hub identity. Its key pair is X25519.
type mobileHubIdentity struct {
	HubID      string
	Credential string
	Private    *ecdh.PrivateKey
	Public     *ecdh.PublicKey
}

// mobileRelayConfig is relay.json. URL holds only the address the user entered;
// the effective relay is resolved by mobileEffectiveRelayURL.
type mobileRelayConfig struct {
	Version int    `json:"version"`
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

// mobileStateExists reports whether any iPhone connection state was saved.
func mobileStateExists(dir string) bool {
	for _, name := range []string{"hub-identity.json", "relay.json", "devices.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func ensureMobileStateDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.Chmod(dir, 0700)
}

func createMobileHubIdentity(dir string) (mobileHubIdentity, error) {
	credential, err := newMobileSecret()
	if err != nil {
		return mobileHubIdentity{}, err
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return mobileHubIdentity{}, err
	}
	identity := mobileHubIdentity{HubID: uuid.NewString(), Credential: credential, Private: private, Public: private.PublicKey()}
	if err := ensureMobileStateDirectory(dir); err != nil {
		return mobileHubIdentity{}, err
	}
	file := mobileHubIdentityFile{
		Version:       1,
		HubID:         identity.HubID,
		HubCredential: credential,
		PrivateKey:    base64.RawURLEncoding.EncodeToString(private.Bytes()),
		PublicKey:     base64.RawURLEncoding.EncodeToString(identity.Public.Bytes()),
		CreatedAt:     mobileTimestamp(time.Now()),
	}
	if err := writeMobilePrivateJSON(filepath.Join(dir, "hub-identity.json"), file); err != nil {
		return mobileHubIdentity{}, err
	}
	return identity, nil
}

// loadMobileHubIdentity returns nil, nil when no identity was created yet.
func loadMobileHubIdentity(dir string) (*mobileHubIdentity, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "hub-identity.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	invalid := errors.New("the saved phone connection identity is invalid")
	var file mobileHubIdentityFile
	if err := mobileDecodeStrict(raw, &file); err != nil || file.Version != 1 || !mobileValidUUID(file.HubID) {
		return nil, invalid
	}
	credential, err := base64.RawURLEncoding.DecodeString(file.HubCredential)
	if err != nil || len(credential) != 32 {
		return nil, invalid
	}
	privateRaw, err := base64.RawURLEncoding.DecodeString(file.PrivateKey)
	if err != nil {
		return nil, invalid
	}
	private, err := ecdh.X25519().NewPrivateKey(privateRaw)
	if err != nil {
		return nil, invalid
	}
	publicRaw, err := base64.RawURLEncoding.DecodeString(file.PublicKey)
	if err != nil || !bytes.Equal(private.PublicKey().Bytes(), publicRaw) {
		return nil, invalid
	}
	return &mobileHubIdentity{HubID: file.HubID, Credential: file.HubCredential, Private: private, Public: private.PublicKey()}, nil
}

func loadMobileRelayConfig(dir string) (mobileRelayConfig, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "relay.json"))
	if errors.Is(err, os.ErrNotExist) {
		return mobileRelayConfig{Version: 1}, nil
	}
	if err != nil {
		return mobileRelayConfig{}, err
	}
	var config mobileRelayConfig
	if err := mobileDecodeStrict(raw, &config); err != nil || config.Version != 1 {
		return mobileRelayConfig{}, errors.New("the saved phone relay settings are invalid")
	}
	return config, nil
}

func saveMobileRelayConfig(dir string, config mobileRelayConfig) error {
	if err := ensureMobileStateDirectory(dir); err != nil {
		return err
	}
	config.Version = 1
	return writeMobilePrivateJSON(filepath.Join(dir, "relay.json"), config)
}

// mobileEffectiveRelayURL resolves explicit relay settings. An empty result
// means an explicitly enabled local remote-access relay should be started.
func mobileEffectiveRelayURL(saved string) (string, error) {
	if strings.TrimSpace(saved) != "" {
		return validateMobileRelayURL(saved)
	}
	if override := mobileRelayURLFromEnvironment(); override != "" {
		return validateMobileRelayURL(override)
	}
	if mobileRemoteAccessMode(saved) {
		return "", nil
	}
	return validateMobileRelayURL(mobileBuiltInRelayURL())
}

// validateMobileRelayURL accepts an origin only: https anywhere, or http for the
// loopback development hosts named in PROTOCOL.md section 3.1.
func validateMobileRelayURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Opaque != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errMobileRelayURL
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if host := parsed.Hostname(); host != "127.0.0.1" && host != "localhost" {
			return "", errMobileRelayURL
		}
	default:
		return "", errMobileRelayURL
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// mobileComputerNameFunc reads the Mac's user-facing Computer Name, the name its owner chose in System Settings
// (`scutil --get ComputerName`). It is a variable so tests can supply the name a Mac would report.
var mobileComputerNameFunc = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", "ComputerName").Output()
	return string(out), err
}

// mobileHubName is the name the phone shows for this computer during pairing and in its status. It is the
// Computer Name the owner gave the Mac, with its words kept (P140). The Bonjour host name is the fallback when
// no Computer Name is set or it cannot be read, without its ".local" suffix.
func mobileHubName() string {
	name := ""
	if computer, err := mobileComputerNameFunc(); err == nil {
		name = strings.TrimSpace(computer)
	}
	if name == "" {
		host, err := os.Hostname()
		if err != nil {
			host = ""
		}
		name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(host), ".local"))
	}
	if name == "" {
		name = "Isoform on this computer"
	}
	return mobileTruncateUTF8(name, mobileHubNameLimit)
}

func mobileTruncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// mobileTimestamp formats a time as RFC 3339 UTC with second precision.
func mobileTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func mobileParseTimestamp(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed, err == nil
}

// mobileValidUUID accepts only canonical lowercase UUID version 4 values.
func mobileValidUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value && parsed.Version() == 4 && parsed.Variant() == uuid.RFC4122
}
