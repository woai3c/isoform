package main

import (
	"context"
	"testing"

	"wails-gui/internal/engagement"
	"wails-gui/internal/sop"
)

func TestProductTelemetryPairingRequiresSuccessfulApproval(t *testing.T) {
	for _, decision := range []string{"approve", "decline", "invalid"} {
		t.Run(decision, func(t *testing.T) {
			telemetry, recorder := newRecordingTelemetry(t)
			bridge, relay, conn, invitation := mobileTestOpenInvitation(t)
			app := &App{telemetry: telemetry, mobileBridge: bridge}
			relay.nextFrame(t, "pairing_open")
			frame, _ := newMobileTestPhone(t).claimFrame(t, invitation, nil)
			conn.send(t, frame)
			mobileTestWaitFor(t, "a pending claim", func() bool {
				return bridge.connectionStatus().PendingPairing != nil
			})
			pairingID := invitation.Pairing
			if decision == "invalid" {
				pairingID = "not-the-pending-pairing"
			}
			_, err := app.ConfirmMobilePairing(pairingID, decision != "decline")
			if (err != nil) != (decision == "invalid") {
				t.Fatalf("decision %s: %v", decision, err)
			}
			want := 0
			if decision == "approve" {
				want = 1
				// Redelivery cannot count the consumed invitation again.
				if _, err := app.ConfirmMobilePairing(pairingID, true); err == nil {
					t.Fatal("approved the same pairing twice")
				}
			}
			if got := recorder.count(); got != want {
				t.Fatalf("decision %s queued %d events, want %d", decision, got, want)
			}
		})
	}
}

func TestProductTelemetryPlaybookCountsActualManualAndScheduledRuns(t *testing.T) {
	telemetry, recorder := newRecordingTelemetry(t)
	rig := newDogfoodRig(t)
	rig.app.telemetry = telemetry
	rig.runner.reply = "<playbook_result>Private result</playbook_result>"
	entry, err := rig.app.sopStore.Save(sop.Entry{
		ID: "telemetry-check", Title: "Private title", Body: "# Goal\nPrivate steps",
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := rig.app.RunPlaybookNow(entry.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	rig.waitForMoment(t, engagement.KindPlaybookResult, 1)
	if got := recorder.count(); got != 1 {
		t.Fatalf("manual run queued %d events, want one for successful completion", got)
	}
	job, err := rig.state.LoadCronJob(context.Background(), view.Schedule.JobID)
	if err != nil {
		t.Fatal(err)
	}
	rig.scheduler.executeJob(context.Background(), job, "scheduled")
	if got := recorder.count(); got != 2 {
		t.Fatalf("manual and scheduled runs queued %d events, want two", got)
	}
}

func TestProductTelemetryPlaybookCountsOnlySuccessfulCompletions(t *testing.T) {
	for _, trigger := range []string{"manual", "scheduled"} {
		for _, status := range []string{"skipped", "error", "ok"} {
			t.Run(trigger+"/"+status, func(t *testing.T) {
				telemetry, recorder := newRecordingTelemetry(t)
				rig := newDogfoodRig(t)
				rig.app.telemetry = telemetry
				entry, err := rig.app.sopStore.Save(sop.Entry{
					ID: "telemetry-completion", Title: "Private title", Body: "# Goal\nPrivate steps",
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := rig.app.playbookPlans.Update(entry.ID, func(plan *sop.Plan) {
					plan.Schedule = &sop.Schedule{JobID: "telemetry-job", Enabled: true}
				}); err != nil {
					t.Fatal(err)
				}
				event := CronRunLifecycleEvent{
					JobID: "telemetry-job", SurfaceKind: playbookSurfaceKind,
					RunID: "telemetry-run", TriggerKind: trigger, Status: "running",
				}
				rig.app.reportPlaybookRun(event)
				if got := recorder.count(); got != 0 {
					t.Errorf("running attempt queued %d events before it completed", got)
				}
				event.Status = status
				event.ResultSummary = "<playbook_result>Private result</playbook_result>"
				rig.app.reportPlaybookRun(event)
				want := 0
				if status == "ok" {
					want = 1
				}
				if got := recorder.count(); got != want {
					t.Fatalf("%s completion queued %d events, want %d", status, got, want)
				}
			})
		}
	}
}
