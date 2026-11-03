package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"wails-gui/internal/activitybuf"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

// Exercise the real snapshot wait, finalization, SQLite commit failure and
// restart, with both possible orders of call end / rotation completion.
func TestRecordingSnapshotProofSurvivesOverlappingCallEnd(t *testing.T) {
	for _, order := range []string{"end-before-snapshots", "snapshots-before-end"} {
		for _, private := range []bool{false, true} {
			t.Run(order+map[bool]string{false: "/later-exclusion", true: "/private-title"}[private], func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				// Filesystem mtimes use the real clock, outside the test bubble.
				before := time.Now().Add(time.Hour)
				synctest.Test(t, func(t *testing.T) {
					app, w, root := newProvenanceTestCall(t)
					store := app.activity.store
					defer func() { app.activity.store.Close() }()
					epoch, _ := app.automaticMeetingRecordingEpoch()
					write := newSnapshotWrite("snapshot-only.jpg")
					title := "Project notes"
					if private {
						title = "Notes - Google Chrome (Incognito)"
					}
					parsed, _ := json.Marshal(map[string]any{"title": title, "metadata": map[string]string{"bundleId": "com.example.snapshot-only"}})
					am := app.activity
					am.buf, am.capture = activitybuf.NewRingBuffer(64, time.Hour), failingScreenCapture{}
					am.inMeeting, am.meetingAppName, am.meetingBundleID = true, "Zoom", "us.zoom.xos"
					am.meetingStartMs, am.meetingRecordingEpoch, am.meetingAudioWriter = time.Now().Add(-time.Second).UnixMilli(), epoch, w
					am.allSnapshots = []snapshotEntry{{relPath: write.relPath, timestamp: time.Now().UnixMilli(), app: "Snapshot Notes", parsedJSON: string(parsed), write: write}}
					am.buf.Push(activitymon.ActivityEvent{Kind: activitymon.EventClick, Timestamp: time.Now().UnixMilli(), AppName: "Zoom", AppBundleID: "us.zoom.xos"})
					// The owner can commit; only the held rotation fails.
					if _, err := store.db.Exec(`CREATE TRIGGER fail_rotation BEFORE INSERT ON activity_sessions WHEN NEW.trigger_kind = 'meeting_flush' BEGIN SELECT RAISE(ABORT,'injected rotation failure'); END`); err != nil {
						t.Fatal(err)
					}
					done := make(chan struct{})
					go func() { defer close(done); app.extractSession(meetingSliceTrigger) }()
					defer func() { write.finish(1, true); <-done }()
					// Advance only the failed final-capture retry, then hold at
					// awaitSnapshotWrites without expiring its five-second budget.
					time.Sleep(200 * time.Millisecond)
					synctest.Wait()
					initial := w.metaSnapshot()
					if len(initial.PendingSources) != 1 || len(am.allSnapshots) != 0 {
						t.Fatalf("rotation did not reach the snapshot wait: %+v", initial)
					}
					select {
					case <-done:
						t.Fatal("rotation did not wait for the snapshot")
					default:
					}
					rotationID := initial.PendingSources[0].SessionID
					var owner ActivitySessionRow
					saveOwner := func() {
						t.Helper()
						app.meetingRecordingMu.Lock()
						var ok bool
						owner, _, ok = app.saveCallRecordingLocked(w, epoch, "meeting_ended")
						app.meetingRecordingMu.Unlock()
						if !ok {
							t.Fatal("could not finalize owner")
						}
					}
					if order == "end-before-snapshots" {
						saveOwner()
						if app.meetingTranscriptAllowed(owner, nil) {
							t.Fatal("unfinished snapshot proof authorized the recording")
						}
					}
					write.finish(1, true)
					<-done
					if order == "snapshots-before-end" {
						saveOwner()
					}
					if _, err := store.GetSessionByID(rotationID); err == nil {
						t.Fatal("injected rotation commit did not fail")
					}
					if app.meetingTranscriptAllowed(owner, nil) {
						t.Fatal("missing rotation authorized before recovery")
					}
					if _, err := store.db.Exec(`DROP TRIGGER fail_rotation`); err != nil {
						t.Fatal(err)
					}
					restartProvenanceTestStore(t, app, root)
					store = app.activity.store
					for i := 0; i < 2; i++ {
						store.recoverMeetingAudioTracks(before, false)
					}
					rotation, err := store.GetSessionByID(rotationID)
					if err != nil || rotation == nil || !strings.Contains(rotation.AppNames, "com.example.snapshot-only") || !strings.Contains(rotation.AppNames, "Snapshot Notes") {
						t.Fatalf("recovery lost snapshot identities: %+v %v", rotation, err)
					}
					if private {
						if rotation.TriageStatus != "protected" || app.meetingTranscriptAllowed(owner, nil) {
							t.Fatalf("private snapshot became readable: %+v", rotation)
						}
					} else {
						if !app.meetingTranscriptAllowed(owner, nil) {
							t.Fatal("complete, allowed proof could not recover")
						}
						previous := pii.GetUserSensitiveApps()
						pii.AddUserSensitiveApp("com.example.snapshot-only", "Snapshot Notes")
						defer pii.SetUserSensitiveApps(previous)
						if app.meetingTranscriptAllowed(owner, nil) {
							t.Fatal("snapshot-only app escaped a later exclusion")
						}
					}
					assertRecordingProofDoesNotPublish(t, app)
				})
			})
		}
	}
}

func newProvenanceTestCall(t *testing.T) (*App, *meetingAudioWriter, string) {
	t.Helper()
	app := mainChatTestApp(t)
	root := t.TempDir()
	store, err := NewActivityStore(root)
	if err != nil {
		t.Fatal(err)
	}
	app.activity = &activityMonitorState{store: store}
	w, err := newMeetingAudioWriter(store.AudioDir())
	if err != nil {
		t.Fatal(err)
	}
	w.SetMeeting("Zoom", "us.zoom.xos", "pending-snapshot-call")
	w.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, time.Now(), constSamples(meetingAudioSampleRate, 2000)))
	return app, w, root
}

func restartProvenanceTestStore(t *testing.T, app *App, root string) {
	t.Helper()
	if err := app.activity.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewActivityStore(root)
	if err != nil {
		t.Fatal(err)
	}
	app.activity.store = store
}

func assertRecordingProofDoesNotPublish(t *testing.T, app *App) {
	t.Helper()
	engine := &recordingFixtureEngine{}
	app.whisper = &whisperState{server: engine}
	app.recoverUntranscribedMeetings()
	if engine.calls != 0 {
		t.Fatal("blocked recording reached ASR")
	}
	entries, err := app.GetKnowledgeEntries()
	if err != nil || len(entries) != 0 {
		t.Fatalf("blocked recording published to Library: %d %v", len(entries), err)
	}
}

func TestRecordingIncompleteProofStaysDeniedAfterRestart(t *testing.T) {
	for _, state := range []string{"raw", "finalized", "existing-incomplete-row", "completion-write-failed"} {
		t.Run(state, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			app, w, root := newProvenanceTestCall(t)
			defer func() { app.activity.store.Close() }()
			proof := activitySourceProvenance{SessionID: "unfinished-rotation", Apps: []string{"Zoom"}, StartTime: 1, EndTime: 2}
			if err := w.reserveActivitySession(proof, false); err != nil {
				t.Fatal(err)
			}
			var owner ActivitySessionRow
			if state == "raw" {
				// Abrupt process exit: no Abort, Finalize or snapshot completion.
				w.micFile.Close()
				w.sysFile.Close()
			} else {
				epoch, _ := app.automaticMeetingRecordingEpoch()
				var ok bool
				owner, _, ok = app.saveCallRecordingLocked(w, epoch, "meeting_ended")
				if !ok {
					t.Fatal("could not finalize owner")
				}
				if state == "existing-incomplete-row" {
					// A row reconstructed by older recovery does not upgrade the
					// unfinished journal into completed privacy evidence.
					if err := app.activity.store.SaveSession(ActivitySessionRow{ID: proof.SessionID, AppNames: `["Zoom"]`, TriageStatus: "dismissed", IsMeeting: true, TriggerKind: recoveredRecordingTrigger}, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if state == "completion-write-failed" {
					path := meetingWAVMetaPath(app.activity.store.AudioDir(), owner.AudioPath)
					before, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
					proof.Apps = append(proof.Apps, "com.example.snapshot-only")
					if err := w.completeActivitySession(proof, false); err == nil {
						t.Fatal("injected sidecar write did not fail")
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, before, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			restartProvenanceTestStore(t, app, root)
			store := app.activity.store
			for i := 0; i < 2; i++ {
				store.recoverMeetingAudioTracks(time.Now().Add(time.Hour), false)
			}
			paths, err := filepath.Glob(filepath.Join(store.AudioDir(), "*", "*.wav"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("pending audio was lost: %v %v", paths, err)
			}
			rel, err := filepath.Rel(store.AudioDir(), paths[0])
			if err != nil {
				t.Fatal(err)
			}
			meta := readMeetingWAVMeta(store.AudioDir(), rel)
			incomplete := false
			for _, pending := range meta.PendingSources {
				if pending.SessionID == proof.SessionID && !pending.Complete {
					incomplete = true
				}
			}
			if !incomplete {
				t.Fatalf("restart discarded the unfinished proof: %+v", meta)
			}
			if state != "existing-incomplete-row" {
				if row, _ := store.GetSessionByID(proof.SessionID); row != nil {
					t.Fatalf("incomplete proof invented a source: %+v", row)
				}
			}
			previous := pii.GetUserSensitiveApps()
			pii.AddUserSensitiveApp("com.example.snapshot-only", "Snapshot Notes")
			defer pii.SetUserSensitiveApps(previous)
			for _, call := range meetingSessions(t, store) {
				if call.AudioPath != "" && app.meetingTranscriptAllowed(call, nil) {
					t.Fatal("unfinished proof authorized after restart")
				}
			}
			if owner.ID != "" && app.meetingTranscriptAllowed(owner, nil) {
				t.Fatal("existing owner bypassed incomplete proof")
			}
			assertRecordingProofDoesNotPublish(t, app)
		})
	}
}

func TestRecordingFinalSidecarPrivacyIsMonotonic(t *testing.T) {
	for i := 0; i < 8; i++ {
		w, err := newMeetingAudioWriter(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		w.SetMeeting("Zoom", "us.zoom.xos", "final-sidecar")
		w.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, time.Now(), constSamples(16000, 2000)))
		rotation := activitySourceProvenance{SessionID: "rotation", Apps: []string{"Zoom"}, StartTime: 10, EndTime: 20}
		if err := w.reserveActivitySession(rotation, false); err != nil {
			t.Fatal(err)
		}
		if err := w.completeActivitySession(activitySourceProvenance{SessionID: "owner"}, true); err != nil {
			t.Fatal(err)
		}
		initial := w.metaSnapshot()
		rotation.Apps, rotation.Private = []string{"com.example.snapshot-only"}, true
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			if _, _, err := w.Finalize(); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := w.completeActivitySession(rotation, false); err != nil {
				t.Error(err)
			}
		}()
		go func() { defer wg.Done(); w.commitActivitySession("owner") }()
		wg.Wait()
		// A narrower completed retry and an older provisional reservation
		// cannot erase completed privacy facts or its time interval.
		narrower := rotation
		narrower.StartTime, narrower.EndTime = 15, 16
		if err := w.completeActivitySession(narrower, false); err != nil {
			t.Fatal(err)
		}
		if err := w.reserveActivitySession(activitySourceProvenance{SessionID: "rotation"}, false); err != nil {
			t.Fatal(err)
		}
		meta := readMeetingWAVMeta(w.audioDir, w.finalRel)
		if len(meta.PendingSources) != 1 || !meta.PendingSources[0].Complete || !meta.PendingSources[0].Private || !stringSliceContains(meta.PendingSources[0].Apps, "com.example.snapshot-only") {
			t.Fatalf("finalization lost completed proof: %+v", meta)
		}
		if meta.PendingSources[0].StartTime != 10 || meta.PendingSources[0].EndTime != 20 {
			t.Fatalf("completed privacy interval shrank: %+v", meta.PendingSources[0])
		}
		if !meta.CaptureSource.Private || !stringSliceContains(meta.CaptureSource.Apps, "com.example.snapshot-only") || len(meta.CaptureSource.Apps) != 3 {
			t.Fatalf("final privacy envelope shrank: %+v", meta)
		}
		if initial.PendingSources[0].Complete || initial.CaptureSource.Private {
			t.Fatal("metadata snapshot was mutated by concurrent completion")
		}
		if w.metaPath() != meetingWAVMetaPath(w.audioDir, w.finalRel) {
			if _, err := os.Stat(w.metaPath()); !os.IsNotExist(err) {
				t.Fatalf("stale raw journal remains: %v", err)
			}
		}
		w.commitActivitySession("rotation")
		if err := w.completeActivitySession(rotation, false); err == nil {
			t.Fatal("closed writer re-reserved a committed dependency")
		}
		meta = readMeetingWAVMeta(w.audioDir, w.finalRel)
		if len(meta.PendingSources) != 0 || !meta.CaptureSource.Private || !stringSliceContains(meta.CaptureSource.Apps, "com.example.snapshot-only") {
			t.Fatalf("commit discarded monotonic privacy: %+v", meta)
		}
	}
}

func TestRecordingDiscardRejectsLateProvenance(t *testing.T) {
	w, err := newMeetingAudioWriter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proof := activitySourceProvenance{SessionID: "discarded"}
	if err := w.reserveActivitySession(proof, false); err != nil {
		t.Fatal(err)
	}
	w.Abort()
	w.commitActivitySession(proof.SessionID)
	if err := w.completeActivitySession(proof, false); err == nil {
		t.Fatal("discarded recording accepted a late completion")
	}
	if files, err := filepath.Glob(filepath.Join(w.audioDir, w.relDir, "*")); err != nil || len(files) != 0 {
		t.Fatalf("late completion recreated discarded files: %v %v", files, err)
	}
}
