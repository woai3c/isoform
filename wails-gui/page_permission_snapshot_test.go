package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func permissionSnapshotFixture(granted bool) PlatformPermissions {
	return PlatformPermissions{Platform: runtime.GOOS, Supported: true, Items: []PermissionItem{{Key: "screen_recording", Granted: granted, Required: true}}}
}

func awaitPagePermissionRefresh(t *testing.T, a *App) {
	t.Helper()
	a.pagePermissions.mu.Lock()
	done := a.pagePermissions.running
	a.pagePermissions.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("native status refresh did not finish")
		}
	}
}

// Unit tests inject native status only; persisted files, App privacy transitions
// and event delivery all use the real implementation and temporary stores.
func TestPagePermissionSnapshotServedThenRefreshedAndRestored(t *testing.T) {
	a := newPageFirstReadTestApp(t)
	a.eventBroadcaster = NewEventBroadcaster()
	events := a.eventBroadcaster.AddClient("snapshot")
	defer a.eventBroadcaster.RemoveClient("snapshot")
	if got := a.pageNativePermissions(func() PlatformPermissions { return permissionSnapshotFixture(false) }); got.Items[0].Granted {
		t.Fatal("unexpected cold result")
	}
	path, err := pagePermissionSnapshotPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private snapshot mode: %v %v", info, err)
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("private snapshot directory mode: %v %v", info, err)
	}
	// A fresh App restores the last-good view before its native probe completes.
	restarted := &App{store: a.store, eventBroadcaster: a.eventBroadcaster}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() { awaitPagePermissionRefresh(t, restarted) }()
	got := restarted.pageNativePermissions(func() PlatformPermissions {
		close(entered)
		<-release
		return permissionSnapshotFixture(true)
	})
	if got.Items[0].Granted {
		t.Fatal("did not serve persisted last-good view")
	}
	got.Items[0].Key = "caller mutation"
	<-entered
	close(release)
	awaitPagePermissionRefresh(t, restarted)
	select {
	case raw := <-events:
		var event BrowserEvent
		if json.Unmarshal(raw, &event) != nil || event.Event != "permissions:changed" || len(event.Args) != 0 {
			t.Fatalf("unexpected update event: %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("changed snapshot did not emit existing update event")
	}
	restarted.pagePermissions.mu.Lock()
	defer restarted.pagePermissions.mu.Unlock()
	if !restarted.pagePermissions.value.View.Items[0].Granted || restarted.pagePermissions.value.View.Items[0].Key != "screen_recording" {
		t.Fatal("refresh failed or caller mutated retained state")
	}
}

func TestPagePermissionPrivacyTransitionsEraseAndFenceLateRefresh(t *testing.T) {
	for _, transition := range []string{"forget", "protected_app", "closed_eyes", "sign_out"} {
		t.Run(transition, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			var forgetID, forgetToken string
			if transition == "forget" {
				entry, err := a.SaveMemoryEntry("snapshot-forget", "# Fixture preference\n\nUse clear review notes.")
				if err != nil {
					t.Fatal(err)
				}
				preview, err := a.GetMemoryForgetPreview(entry.ID)
				if err != nil {
					t.Fatal(err)
				}
				forgetID, forgetToken = entry.ID, preview.ConfirmationToken
			}
			_ = a.pageNativePermissions(func() PlatformPermissions { return permissionSnapshotFixture(false) })
			entered, release := make(chan struct{}), make(chan struct{})
			_ = a.pageNativePermissions(func() PlatformPermissions {
				close(entered)
				<-release
				return permissionSnapshotFixture(true)
			})
			<-entered
			switch transition {
			case "forget":
				if err := a.ForgetMemoryEntry(forgetID, forgetToken); err != nil {
					t.Fatal(err)
				}
			case "protected_app":
				if err := a.AddSensitiveApp("com.example.snapshot", "Fixture"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = a.RemoveSensitiveApp("com.example.snapshot") })
			case "closed_eyes":
				if err := a.SetClosedEyesModeEnabled(true); err != nil {
					t.Fatal(err)
				}
			case "sign_out":
				NewAuthManager(a).Reset()
			}
			close(release)
			awaitPagePermissionRefresh(t, a)
			path, _ := pagePermissionSnapshotPath()
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("snapshot survived %s: %v", transition, err)
			}
			a.pagePermissions.mu.Lock()
			defer a.pagePermissions.mu.Unlock()
			if a.pagePermissions.value != nil {
				t.Fatal("late native probe repopulated erased snapshot")
			}
		})
	}
}

func TestPagePermissionNoSnapshotWhenWithheldOrStopped(t *testing.T) {
	a := newPageFirstReadTestApp(t)
	a.closedEyes.enabled.Store(true)
	_ = a.pageNativePermissions(func() PlatformPermissions { return permissionSnapshotFixture(false) })
	path, _ := pagePermissionSnapshotPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("paused read created a snapshot")
	}
	a.closedEyes.enabled.Store(false)
	a.stopPageFirstReads()
	_ = a.pageNativePermissions(func() PlatformPermissions { return permissionSnapshotFixture(false) })
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stopped worker created a snapshot")
	}
}

func TestPageFirstReadWorkerOrderCancellationAndPrivacy(t *testing.T) {
	for _, stop := range []string{"none", "cancel", "privacy"} {
		t.Run(stop, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			allowed := true
			var visited []int
			var active atomic.Int32
			steps := make([]func(context.Context), 7)
			for i := range steps {
				steps[i] = func(context.Context) {
					if active.Add(1) != 1 {
						t.Error("parallel page queries")
					}
					visited = append(visited, i)
					active.Add(-1)
					if i == 2 {
						switch stop {
						case "cancel":
							cancel()
						case "privacy":
							allowed = false
						}
					}
				}
			}
			runPageFirstReads(ctx, steps, func(context.Context) bool { return allowed }, 0)
			want := []int{0, 1, 2}
			if stop == "none" {
				want = []int{0, 1, 2, 3, 4, 5, 6}
			}
			if !reflect.DeepEqual(visited, want) {
				t.Fatalf("visited %v, want %v", visited, want)
			}
		})
	}
}

func TestPageBindingPerfLogContainsOnlyNameTimeAndCount(t *testing.T) {
	var output bytes.Buffer
	prior := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(prior)
	items := []string{"private title", "private query"}
	logPageBinding("GetMemoryEntries", time.Now(), &items)
	line := output.String()
	if !strings.Contains(line, "[perf] binding=GetMemoryEntries ms=") || !strings.Contains(line, " rows=2") || strings.Contains(line, "private") {
		t.Fatalf("unexpected performance log: %s", line)
	}
}

func TestPeopleBindingPerfLogsHideErrors(t *testing.T) {
	var output bytes.Buffer
	prior := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(prior)

	app := &App{}
	_, _ = app.GetPersonDetail("private-person")
	_, _ = app.ListPrincipalActivitySessions("private-person", 10)
	_, _ = app.GetPersonResearch("private-person")
	_, _ = app.ListVoiceSamples("private-person")
	_, _ = app.GetVoiceEnrollmentStatus("private-person")

	for _, name := range []string{
		"GetPersonDetail",
		"ListPrincipalActivitySessions",
		"GetPersonResearch",
		"ListVoiceSamples",
		"GetVoiceEnrollmentStatus",
	} {
		if !strings.Contains(output.String(), "[perf] binding="+name+" ms=") {
			t.Fatalf("missing performance log for %s: %s", name, output.String())
		}
	}
	if strings.Contains(output.String(), "private-person") {
		t.Fatalf("performance logs exposed a binding argument: %s", output.String())
	}
}
