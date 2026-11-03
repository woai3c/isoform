package main

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func isolateSpotlightAppearanceHome(t *testing.T) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "qc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestSpotlightAppearanceUsesCurrentUILanguage(t *testing.T) {
	isolateSpotlightAppearanceHome(t)
	hb, handler := newTestHostBridge(t)
	hb.app.store = mainChatTestApp(t).store
	route := hostBridgeRoute{method: http.MethodGet, path: "/api/spotlight/theme", access: hostAccessSpotlight}

	// Reuse the same live bridge as the main UI changes its saved language.
	// No helper restart or host-language cache should be needed.
	for _, language := range []string{"", "zh-CN", "en"} {
		if err := hb.app.SaveUIState(uiLanguageStateKey, language); err != nil {
			t.Fatal(err)
		}
		response := serve(handler, hostBridgeRequest(route))
		if response.Code != http.StatusOK {
			t.Fatalf("language %q: status = %d, body = %s", language, response.Code, response.Body.String())
		}
		var appearance spotlightThemeResponse
		if err := json.Unmarshal(response.Body.Bytes(), &appearance); err != nil {
			t.Fatal(err)
		}
		if appearance.Language != normalizeUILanguage(language) {
			t.Errorf("language %q: response = %q", language, appearance.Language)
		}
		if appearance.Theme != currentThemePreference() {
			t.Errorf("theme = %q, want current theme %q", appearance.Theme, currentThemePreference())
		}
	}
}

func TestSpotlightAppearanceWithoutStoreDefaultsToEnglish(t *testing.T) {
	isolateSpotlightAppearanceHome(t)
	_, handler := newTestHostBridge(t)
	route := hostBridgeRoute{method: http.MethodGet, path: "/api/spotlight/theme", access: hostAccessSpotlight}
	response := serve(handler, hostBridgeRequest(route))
	var appearance spotlightThemeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &appearance); err != nil {
		t.Fatal(err)
	}
	if appearance.Language != "en" {
		t.Fatalf("language = %q, want en", appearance.Language)
	}
}

// A real shared ui_state store and the authenticated helper route exercise
// the cross-window boundary without using the main renderer's local cache.
func TestSpotlightAppearanceIgnoresRetiredTexture(t *testing.T) {
	isolateSpotlightAppearanceHome(t)
	hb, handler := newTestHostBridge(t)
	hb.app.store = mainChatTestApp(t).store
	route := hostBridgeRoute{method: http.MethodGet, path: "/api/spotlight/theme", access: hostAccessSpotlight}
	for _, stored := range []string{"", "false", "true", "false"} {
		if err := hb.app.SaveUIState("yansu.appearance.texture", stored); err != nil {
			t.Fatal(err)
		}
		response := serve(handler, hostBridgeRequest(route))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		var appearance map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &appearance); err != nil {
			t.Fatal(err)
		}
		if _, exists := appearance["texture"]; exists {
			t.Errorf("stored %q: helper must not expose retired texture preference", stored)
		}
	}
}
