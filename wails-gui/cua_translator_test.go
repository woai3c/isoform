package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCuaTranslatorGetWindowStateDefaultsToAXFirst(t *testing.T) {
	translator := newCuaTranslator()
	out, toolName, err := translator.translateRequest([]byte(`{"method":"call","name":"get_window_state","args":{"pid":123}}`))
	if err != nil {
		t.Fatalf("translateRequest returned error: %v", err)
	}
	if toolName != "get_window_state" {
		t.Fatalf("toolName = %q, want get_window_state", toolName)
	}

	var req struct {
		Cmd  string         `json:"cmd"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal translated request: %v", err)
	}
	if req.Cmd != "get_app_state" {
		t.Fatalf("cmd = %q, want get_app_state", req.Cmd)
	}
	if req.Args["include_screenshot"] != false {
		t.Fatalf("include_screenshot = %#v, want false", req.Args["include_screenshot"])
	}
}

func TestCuaTranslatorGetWindowStatePreservesExplicitScreenshotFalse(t *testing.T) {
	translator := newCuaTranslator()
	out, _, err := translator.translateRequest([]byte(`{"method":"call","name":"get_window_state","args":{"pid":123,"include_screenshot":false}}`))
	if err != nil {
		t.Fatalf("translateRequest returned error: %v", err)
	}

	var req struct {
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal translated request: %v", err)
	}
	if req.Args["include_screenshot"] != false {
		t.Fatalf("include_screenshot = %#v, want false", req.Args["include_screenshot"])
	}
}

func TestTranslatorPinsImagePathsToTheTempFolder(t *testing.T) {
	dir := filepath.Join(os.TempDir(), cuaShotDirName)
	for _, tc := range []struct{ in, base string }{
		{"/Users/someone/.ssh/authorized_keys", "authorized_keys.png"},
		{"../../Library/LaunchAgents/evil.plist", "evil.plist.png"},
		{"/tmp/page.jpg", "page.jpg"},
	} {
		args := map[string]any{"out": tc.in, "screenshot_path": tc.in}
		pinCuaImagePaths(args)
		for _, key := range []string{"out", "screenshot_path"} {
			got, _ := args[key].(string)
			if filepath.Dir(got) != dir || filepath.Base(got) != tc.base {
				t.Fatalf("%s %q -> %q, want %s/%s", key, tc.in, got, dir, tc.base)
			}
		}
	}
	args := map[string]any{"out": "..."}
	pinCuaImagePaths(args)
	if got, _ := args["out"].(string); filepath.Dir(got) != dir || !strings.HasPrefix(filepath.Base(got), "shot-") {
		t.Fatalf("an empty name -> %q", got)
	}
	token := map[string]any{"token_path": "/Users/someone/.zshrc"}
	pinCuaImagePaths(token)
	if got, _ := token["token_path"].(string); filepath.Dir(got) != dir || filepath.Base(got) != "zshrc" {
		t.Fatalf("token_path -> %q", got)
	}
	defaulted := map[string]any{"include_screenshot": true}
	pinCuaImagePaths(defaulted)
	if got, _ := defaulted["screenshot_path"].(string); filepath.Dir(got) != dir || !strings.HasPrefix(filepath.Base(got), "alma-cu-state-") {
		t.Fatalf("a snapshot screenshot without a path -> %q", got)
	}
	odd := map[string]any{"token_path": []any{"/Users/someone/.zshrc"}}
	pinCuaImagePaths(odd)
	if got, _ := odd["token_path"].(string); filepath.Dir(got) != dir {
		t.Fatalf("a token_path of another type -> %v", odd["token_path"])
	}
	none := map[string]any{"bundle": "com.google.Chrome"}
	pinCuaImagePaths(none)
	if _, ok := none["out"]; ok {
		t.Fatal("no path was named, none is added")
	}
}
