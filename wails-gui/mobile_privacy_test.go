package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestMobileSeesMapsPermissionsToJobs(t *testing.T) {
	perms := PlatformPermissions{Items: []PermissionItem{
		{Key: mailPermissionKey, Granted: true, Toggleable: true, Enabled: true},
		{Key: calendarPermissionKey, Granted: true, Toggleable: true, Enabled: false},
		{Key: remindersPermissionKey, Granted: false},
		{Key: "microphone", Granted: true, Toggleable: true, Enabled: true},
		{Key: "screen_recording", Granted: true},
		{Key: "contacts", Granted: false},
	}}
	view := mobileSeesFrom(perms, "claude-code")
	got := map[string]bool{}
	order := []string{}
	for _, source := range view.Sources {
		got[source.Job] = source.On
		order = append(order, source.Job)
	}
	if strings.Join(order, ",") != "mail,calendar,calls,screen,assistant,contacts" {
		t.Fatalf("order = %v", order)
	}
	if !got["mail"] || got["calendar"] || !got["calls"] || !got["screen"] || !got["assistant"] || got["contacts"] || view.Assistant != "claude_code" {
		t.Fatalf("view = %+v", view)
	}
	if managed := mobileSeesFrom(perms, "managed"); managed.Assistant != "" || len(managed.Sources) != 5 {
		t.Fatalf("no CLI, no assistant row: %+v", managed)
	}
}

func TestMobileSeesShowPostsOneNoticeOnTheMac(t *testing.T) {
	bridge, _, _ := mobileTestGoalBridge(t)
	shown := []string{}
	mobileSeesShowHook = func(title, body string) { shown = append(shown, title) }
	t.Cleanup(func() { mobileSeesShowHook = nil })
	device := mobileTestGoalDevice(mobileScopeBriefRead)
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/privacy/show", `{"idempotencyKey":"p0","job":"bank"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown job answered %d", code)
	}
	body := `{"idempotencyKey":"p1","job":"contacts"}`
	if code, response := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/privacy/show", body); code != http.StatusOK {
		t.Fatalf("show answered %d %s", code, response)
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, "/privacy/show", body); code != http.StatusOK || len(shown) != 1 || shown[0] != "Turn on Contacts for Isoform" {
		t.Fatalf("a replay must not show twice: %v", shown)
	}
	if code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(), http.MethodGet, "/privacy", ""); code != http.StatusForbidden || !strings.Contains(body, mobileScopeBriefRead) {
		t.Fatalf("privacy without brief:read answered %d", code)
	}
}
