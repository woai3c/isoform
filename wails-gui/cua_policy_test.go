package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCuaPolicyDenial walks every contract rule the proxy enforces.
// Cases are written against the legacy cua-driver JSON shape (the
// shape callers still speak; the translator rewrites internally),
// so any change in unmarshalling shape gets caught before it ships.
//
// History: pre-alma the policy denied raw-coord clicks and drags
// because trycua/cua's pixel path was .cghidEventTap and forced the
// target frontmost. With the bundled alma daemon's bgclick-rev path
// raw-coord click/drag are focus-safe, so we now allow them and
// only gate hotkeys the WindowServer routes itself.
func TestCuaPolicyDenial(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		toolName string
		args     string
		wantHas  string // substring that must be present; "" means must be allowed
	}{
		{
			name:     "non-call methods skip validation entirely",
			method:   "list-tools",
			toolName: "",
			args:     "{}",
			wantHas:  "",
		},
		{
			name:     "click with element_index is allowed (AX path)",
			method:   "call",
			toolName: "click",
			args:     `{"pid":1,"window_id":2,"element_index":3}`,
			wantHas:  "",
		},
		{
			name:     "click with raw x/y is allowed (bgclick path, no focus steal)",
			method:   "call",
			toolName: "click",
			args:     `{"pid":1,"x":100,"y":200}`,
			wantHas:  "",
		},
		{
			name:     "click with visual ref is allowed (OCR path)",
			method:   "call",
			toolName: "click",
			args:     `{"pid":1,"ref":"v3"}`,
			wantHas:  "",
		},
		{
			name:     "click with AX ref is allowed (alma-native shape)",
			method:   "call",
			toolName: "click",
			args:     `{"pid":1,"ref":"a17"}`,
			wantHas:  "",
		},
		{
			name:     "drag is allowed (alma routes through bgclick)",
			method:   "call",
			toolName: "drag",
			args:     `{"pid":1,"from_x":0,"from_y":0,"to_x":10,"to_y":10}`,
			wantHas:  "",
		},
		{
			name:     "hotkey cmd+S is allowed (in-app save, no focus pull)",
			method:   "call",
			toolName: "hotkey",
			args:     `{"pid":1,"keys":["cmd","s"]}`,
			wantHas:  "",
		},
		{
			name:     "hotkey cmd+L is denied (omnibox)",
			method:   "call",
			toolName: "hotkey",
			args:     `{"pid":1,"keys":["cmd","l"]}`,
			wantHas:  "cmd+L",
		},
		{
			name:     "hotkey cmd+1 is denied (tab switch)",
			method:   "call",
			toolName: "hotkey",
			args:     `{"pid":1,"keys":["cmd","1"]}`,
			wantHas:  "cmd+1",
		},
		{
			name:     "hotkey cmd+9 is denied (last-tab shortcut)",
			method:   "call",
			toolName: "hotkey",
			args:     `{"pid":1,"keys":["cmd","9"]}`,
			wantHas:  "cmd+9",
		},
		{
			name:     "hotkey Cmd+L (uppercased) is also denied",
			method:   "call",
			toolName: "hotkey",
			args:     `{"pid":1,"keys":["Cmd","L"]}`,
			wantHas:  "cmd+L",
		},
		{
			name:     "hotkey cmd+tab is denied (WindowServer-level switch)",
			method:   "call",
			toolName: "hotkey",
			args:     `{"pid":1,"keys":["cmd","tab"]}`,
			wantHas:  "cmd+Tab",
		},
		{
			name:     "hotkey without cmd modifier passes (e.g. raw enter)",
			method:   "call",
			toolName: "hotkey",
			args:     `{"pid":1,"keys":["return"]}`,
			wantHas:  "",
		},
		{
			name:     "press cmd+L (alma-native shape) is denied",
			method:   "call",
			toolName: "press",
			args:     `{"pid":1,"key":"l","modifiers":["cmd"]}`,
			wantHas:  "cmd+L",
		},
		{
			name:     "type_text passes regardless of args (no focus-steal vector)",
			method:   "call",
			toolName: "type_text",
			args:     `{"pid":1,"text":"hello"}`,
			wantHas:  "",
		},
		{
			name:     "screenshot passes",
			method:   "call",
			toolName: "screenshot",
			args:     `{"pid":1,"window_id":2}`,
			wantHas:  "",
		},
		{
			name:     "malformed args blob skips validation (let daemon surface schema error)",
			method:   "call",
			toolName: "click",
			args:     `not json`,
			wantHas:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cuaPolicyDenial(c.method, c.toolName, json.RawMessage(c.args))
			switch {
			case c.wantHas == "" && got != "":
				t.Errorf("want allowed, got denial: %s", got)
			case c.wantHas != "" && !strings.Contains(got, c.wantHas):
				t.Errorf("want denial containing %q, got: %s", c.wantHas, got)
			}
		})
	}
}
