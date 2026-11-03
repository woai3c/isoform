package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// lark-cli (github.com/larksuite/cli) lets the agent operate Feishu/Lark from
// the terminal (messages, docs, Base, Sheets, Calendar, Mail, contacts,
// meetings, tasks) via the bundled bun runtime (`bun x @larksuite/cli ...`).
//
// We register it with the CLI's own `config init` using the credentials the
// user's scan-created PersonalAgent app produced (stored locally per brand by
// saveFeishuLocalCredentials). Each brand gets a named profile ("feishu" /
// "lark"), so both can be connected at once; the CLI keeps the secret in the OS
// keychain and `--brand` selects the host (feishu.cn vs larksuite). The agent
// picks an account with `lark-cli --profile <brand> ...`.
const larkCliPackage = "@larksuite/cli@latest"

// larkCliProfileEntry mirrors one app profile in ~/.lark-cli/config.json.
type larkCliProfileEntry struct {
	Name  string `json:"name"`
	AppID string `json:"appId"`
	Brand string `json:"brand"`
	Users []any  `json:"users"`
}

type larkCliConfigFile struct {
	Apps []larkCliProfileEntry `json:"apps"`
}

func larkCliConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".lark-cli", "config.json"), nil
}

// larkCliConfigure registers (or updates) the lark-cli profile for the given
// brand via `config init`. Non-interactive (no browser): the app already exists
// from the scan flow, so this only writes the credential + brand. Best-effort.
func larkCliConfigure(brand, appID, appSecret string) error {
	brand = normalizeFeishuBrand(brand)
	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	if appID == "" || appSecret == "" {
		return fmt.Errorf("lark-cli configure: app_id and app_secret required")
	}
	bun := installedBunPath()
	if bun == "" {
		return fmt.Errorf("bun runtime not available")
	}
	// A different app_id under the same brand means the user re-created the agent.
	// Drop the stale profile first so its old user-login (Users) cannot carry over
	// and falsely report personal-identity authorization for the new app.
	if existing := larkCliProfileAppID(brand); existing != "" && existing != appID {
		larkCliRemoveProfile(brand)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "x", larkCliPackage,
		"config", "init",
		"--app-id", appID,
		"--app-secret-stdin",
		"--brand", brand,
		"--name", brand,
	)
	cmd.Stdin = strings.NewReader(appSecret + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("lark-cli config init: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	log.Printf("[lark-cli] configured profile %q (app %s)", brand, appID)
	return nil
}

// larkCliRemoveProfile drops the lark-cli profile for the given brand. No-op
// when bun is unavailable; best-effort.
func larkCliRemoveProfile(brand string) {
	brand = normalizeFeishuBrand(brand)
	bun := installedBunPath()
	if bun == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "x", larkCliPackage, "profile", "remove", brand)
	if err := cmd.Run(); err != nil {
		log.Printf("[lark-cli] remove profile %q: %v", brand, err)
	}
}

// readLarkCliApps returns the app profiles in ~/.lark-cli/config.json, or nil.
func readLarkCliApps() []larkCliProfileEntry {
	path, err := larkCliConfigPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg larkCliConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return cfg.Apps
}

// larkCliConfiguredProfiles reads which brand profiles lark-cli has configured.
func larkCliConfiguredProfiles() map[string]bool {
	out := map[string]bool{}
	for _, app := range readLarkCliApps() {
		if b := normalizeFeishuBrand(app.Brand); b != "" {
			out[b] = true
		}
	}
	return out
}

// larkCliProfileAppID returns the app_id stored for the brand's lark-cli
// profile, or "" when the brand has no profile.
func larkCliProfileAppID(brand string) string {
	brand = normalizeFeishuBrand(brand)
	for _, app := range readLarkCliApps() {
		if normalizeFeishuBrand(app.Brand) == brand {
			return strings.TrimSpace(app.AppID)
		}
	}
	return ""
}

// larkCliSkillEnabled reports whether the bundled lark-cli skill is enabled in
// the skill hub (the agent only sees it when enabled).
func larkCliSkillEnabled() bool {
	state, err := loadSkillHubState()
	if err != nil {
		return false
	}
	s, ok := state.Skills[bundledLarkSkillSlug]
	return ok && s.Enabled
}

// LarkCliStatus reports whether the agent can operate Feishu/Lark via lark-cli.
// Operable (the composer-chip signal) means the agent actually has the
// capability: the skill is enabled, a profile is configured, and bun can run it.
type LarkCliStatus struct {
	Runnable    bool `json:"runnable"`     // bun runtime present
	Configured  bool `json:"configured"`   // at least one brand profile registered
	Enabled     bool `json:"enabled"`      // lark-cli skill enabled in the skill hub
	ScopesReady bool `json:"scopes_ready"` // a configured brand has full permissions granted
	Operable    bool `json:"operable"`     // agent can operate Feishu/Lark right now
}

func (a *App) GetLarkCliStatus() LarkCliStatus {
	profiles := larkCliConfiguredProfiles()
	scopesReady := false
	for brand := range profiles {
		if creds := loadFeishuLocalCredentials(brand); creds != nil && creds.PermissionsReady {
			scopesReady = true
			break
		}
	}
	runnable := installedBunPath() != ""
	configured := len(profiles) > 0
	enabled := larkCliSkillEnabled()
	return LarkCliStatus{
		Runnable:    runnable,
		Configured:  configured,
		Enabled:     enabled,
		ScopesReady: scopesReady,
		Operable:    runnable && configured && enabled,
	}
}

// larkCliUserAuthed reports whether the brand's profile has a logged-in user for
// the CURRENTLY connected app. Matching on appID (not just brand) stops a stale
// user-login left over from a previously connected app under the same brand
// profile from falsely reporting personal-identity authorization for a freshly
// created agent.
func larkCliUserAuthed(brand, appID string) bool {
	brand = normalizeFeishuBrand(brand)
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return false
	}
	for _, app := range readLarkCliApps() {
		if normalizeFeishuBrand(app.Brand) == brand && strings.TrimSpace(app.AppID) == appID && len(app.Users) > 0 {
			return true
		}
	}
	return false
}

// LarkAccountStatus is the per-brand lark-cli readiness for the settings card.
type LarkAccountStatus struct {
	Runnable    bool `json:"runnable"`
	Configured  bool `json:"configured"`   // this brand has an app profile
	ScopesReady bool `json:"scopes_ready"` // full permissions granted for this brand
	UserAuthed  bool `json:"user_authed"`  // a user is logged in (personal-data access)
}

func (a *App) GetLarkAccountStatus(brand string) LarkAccountStatus {
	brand = normalizeFeishuBrand(brand)
	configured := larkCliConfiguredProfiles()[brand]
	scopesReady := false
	appID := ""
	if creds := loadFeishuLocalCredentials(brand); creds != nil {
		appID = creds.AppID
		scopesReady = creds.PermissionsReady
	}
	return LarkAccountStatus{
		Runnable:    installedBunPath() != "",
		Configured:  configured,
		ScopesReady: scopesReady,
		UserAuthed:  larkCliUserAuthed(brand, appID),
	}
}

// extractJSONObject returns the first top-level JSON object found in b. The
// bundled `bun x` runtime can prepend lines like "lark-cli vX installed
// successfully" before the actual JSON payload on first run, so we cannot
// json.Unmarshal the whole stdout blindly.
func extractJSONObject(b []byte) []byte {
	start := bytes.IndexByte(b, '{')
	end := bytes.LastIndexByte(b, '}')
	if start < 0 || end < start {
		return b
	}
	return b[start : end+1]
}

// LarkUserLogin is the device-flow payload for upgrading a brand to user identity.
type LarkUserLogin struct {
	VerificationURL string `json:"verification_url"`
	UserCode        string `json:"user_code"`
	DeviceCode      string `json:"device_code"`
	ExpiresIn       int    `json:"expires_in"`
}

// LarkCliUserLoginStart begins the lark-cli user-identity device flow for the
// given brand and returns the QR payload. The user scans verification_url in
// Feishu/Lark; the desktop polls LarkCliUserLoginPoll until authorized.
func (a *App) LarkCliUserLoginStart(brand string) (*LarkUserLogin, error) {
	brand = normalizeFeishuBrand(brand)
	bun := installedBunPath()
	if bun == "" {
		return nil, fmt.Errorf("bun runtime not available")
	}
	if !larkCliConfiguredProfiles()[brand] {
		return nil, fmt.Errorf("%s app not connected", brand)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "x", larkCliPackage,
		"auth", "login", "--no-wait", "--json", "--domain", "all", "--profile", brand)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lark-cli auth login: %w", err)
	}
	var resp struct {
		DeviceCode      string `json:"device_code"`
		VerificationURL string `json:"verification_url"`
		UserCode        string `json:"user_code"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := json.Unmarshal(extractJSONObject(out), &resp); err != nil {
		return nil, fmt.Errorf("parse auth login: %w", err)
	}
	if resp.DeviceCode == "" || resp.VerificationURL == "" {
		return nil, fmt.Errorf("auth login: missing device_code/verification_url")
	}
	expires := resp.ExpiresIn
	if expires <= 0 {
		expires = 600
	}
	// lark-cli does not surface user_code as a top-level field; it is carried in
	// the verification_url query. Recover it so the panel can show the code.
	userCode := resp.UserCode
	if userCode == "" {
		if u, perr := url.Parse(resp.VerificationURL); perr == nil {
			userCode = u.Query().Get("user_code")
		}
	}
	return &LarkUserLogin{
		VerificationURL: resp.VerificationURL,
		UserCode:        userCode,
		DeviceCode:      resp.DeviceCode,
		ExpiresIn:       expires,
	}, nil
}

// LarkCliUserLoginPoll completes (or checks) the device flow with the given
// device code. Returns "connected" | "pending" | "expired" | "error". The
// underlying --device-code call blocks until authorized, so we bound it with a
// short timeout and treat a timeout as still-pending.
func (a *App) LarkCliUserLoginPoll(brand, deviceCode string) (string, error) {
	brand = normalizeFeishuBrand(brand)
	bun := installedBunPath()
	if bun == "" {
		return "", fmt.Errorf("bun runtime not available")
	}
	if strings.TrimSpace(deviceCode) == "" {
		return "expired", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "x", larkCliPackage,
		"auth", "login", "--device-code", deviceCode, "--json", "--profile", brand)
	out, err := cmd.Output()
	deadlineHit := ctx.Err() == context.DeadlineExceeded
	// The --device-code call persists the authorized user into config.json on
	// success (and returns a user object whose JSON shape is not stable across
	// versions). Treat the config state as the authoritative signal rather than
	// guessing the CLI's success payload. Match against the connected app_id so a
	// stale login under the same brand can't be mistaken for success. Check this
	// even when our timeout cut the call off, so an authorization that completed
	// right at the deadline isn't dropped and re-polled for another full cycle.
	connectedAppID := ""
	if creds := loadFeishuLocalCredentials(brand); creds != nil {
		connectedAppID = creds.AppID
	}
	if larkCliUserAuthed(brand, connectedAppID) {
		return "connected", nil
	}
	if deadlineHit {
		return "pending", nil // still waiting for the user to authorize
	}
	if err != nil && len(out) == 0 {
		return "pending", nil // transient; keep polling
	}
	var resp struct {
		OK    bool `json:"ok"`
		Error *struct {
			Subtype string `json:"subtype"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal(extractJSONObject(out), &resp); jsonErr != nil {
		return "pending", nil
	}
	if resp.Error != nil {
		switch resp.Error.Subtype {
		case "authorization_pending", "slow_down":
			return "pending", nil
		case "expired_token", "invalid_grant":
			return "expired", nil
		default:
			return "error", nil
		}
	}
	if resp.OK {
		return "connected", nil
	}
	return "pending", nil
}
