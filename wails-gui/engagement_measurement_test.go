package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestEngagementAutoPauseExactThresholdAndDistinctNegatives(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		sent, dismissed, mutedOverlap int
		paused                        bool
	}{
		{"below minimum", 19, 19, 0, false},
		{"below rate", 20, 11, 0, false},
		{"exactly sixty percent", 20, 12, 0, true},
		{"dismiss and mute count once", 20, 6, 6, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newEngagementTestRig(t)
			ctx := context.Background()
			for i := 0; i < tc.sent; i++ {
				row, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
					Kind: "work_blocked", DedupeKey: fmt.Sprintf("threshold:%d", i),
					State: storage.EngagementStateSent, Channel: "push", CreatedAt: rig.clock.Add(-time.Hour).UnixMilli(), SentAt: rig.clock.Add(-time.Minute).UnixMilli(),
				})
				if err != nil {
					t.Fatal(err)
				}
				for _, state := range []string{storage.EngagementReceiptDismissed, storage.EngagementReceiptMuted} {
					count := tc.dismissed
					if state == storage.EngagementReceiptMuted {
						count = tc.mutedOverlap
					}
					if i < count {
						if err := rig.store.RecordEngagementReceipt(ctx, storage.EngagementReceiptRow{MomentID: row.ID, Channel: "push", State: state, At: rig.clock.UnixMilli()}); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err := rig.svc.autoPause(ctx, engagement.KindWorkBlocked); err != nil {
				t.Fatal(err)
			}
			if rig.svc.Paused(engagement.KindWorkBlocked) != tc.paused {
				t.Fatalf("paused = %v", !tc.paused)
			}
			if !tc.paused {
				return
			}
			prefs, err := rig.store.ListEngagementPrefs(ctx)
			if err != nil || len(prefs) != 1 || prefs[0].Enabled != 0 {
				t.Fatalf("pause not persisted: %+v %v", prefs, err)
			}
			// The explanation and disabled switch survive even after the cohort ages out.
			rig.clock = rig.clock.Add(31 * 24 * time.Hour)
			fresh := newEngagementService(rig.store, nil, engagementDelivery{})
			fresh.now = rig.svc.now
			if err := fresh.load(ctx); err != nil {
				t.Fatal(err)
			}
			if !fresh.Paused(engagement.KindWorkBlocked) {
				t.Fatal("restart lost pause reason")
			}
			if err := fresh.MuteKind(ctx, engagement.KindWorkBlocked, rig.clock.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := fresh.SetKindEnabled(ctx, engagement.KindWorkBlocked, true); err != nil {
				t.Fatal(err)
			}
			restarted := newEngagementService(rig.store, nil, engagementDelivery{})
			restarted.now = rig.svc.now
			if err := restarted.load(ctx); err != nil {
				t.Fatal(err)
			}
			if restarted.Paused(engagement.KindWorkBlocked) || !restarted.Prefs().Enabled[engagement.KindWorkBlocked] || !restarted.Prefs().MutedUntil[engagement.KindWorkBlocked].IsZero() {
				t.Fatal("resume or mute clearing did not survive restart")
			}
		})
	}
}

func TestEngagementCompletionUsesLinkedItemWithinTwoHours(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	items, err := handoff.NewStore(filepath.Join(t.TempDir(), "handoff"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: rig.store, engagement: rig.svc, handoffStore: items}
	rig.svc.completion = app.engagementItemCompletedAt
	sent := rig.clock.Add(-3 * time.Hour)
	for _, tc := range []struct {
		id     string
		after  time.Duration
		status handoff.Status
		acted  bool
	}{
		{"inside", time.Hour, handoff.StatusCompleted, true},
		{"boundary", 2 * time.Hour, handoff.StatusCompleted, true},
		{"late", 2*time.Hour + time.Millisecond, handoff.StatusCompleted, false},
		{"already-complete", -time.Millisecond, handoff.StatusCompleted, false},
		{"opened-only", time.Hour, handoff.StatusNeedsReview, false},
	} {
		if _, err := items.Save(handoff.Entry{ID: tc.id, Status: tc.status, UpdatedAt: sent.Add(tc.after).UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		if _, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{ID: tc.id, Kind: "work_blocked", SubjectID: tc.id, DedupeKey: tc.id,
			CreatedAt: sent.Add(-time.Minute).UnixMilli(), State: storage.EngagementStateSent, Channel: "push", SentAt: sent.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		if err := rig.svc.Receipt(ctx, tc.id, "desktop", storage.EngagementReceiptOpened, "open", sent.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		rig.svc.inferCompletedReceipts(ctx, rig.clock)
		rig.svc.inferCompletedReceipts(ctx, rig.clock)
		receipts, err := rig.store.ListEngagementReceipts(ctx, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		acted := 0
		for _, r := range receipts {
			if r.State == storage.EngagementReceiptActed {
				acted++
				if r.Action != "item_completed" || r.At != sent.Add(tc.after).UnixMilli() {
					t.Fatalf("wrong attribution: %+v", r)
				}
			}
		}
		want := 0
		if tc.acted {
			want = 1
		}
		if acted != want {
			t.Fatalf("%s: acted = %d, want %d", tc.id, acted, want)
		}
	}
	view, err := app.GetEngagementWeekSummary()
	if err != nil {
		t.Fatal(err)
	}
	if view.Sent != 5 || view.ActedOn != 2 {
		t.Fatalf("weekly summary = %+v", view)
	}
	for _, kind := range view.Kinds {
		if kind.Kind == "work_blocked" && (kind.ActRate != 0.4 || kind.Opened != 5 || kind.Acted != 2) {
			t.Fatalf("weekly kind = %+v", kind)
		}
	}
}

func TestEngagementFailedSwitchWriteDoesNotChangeEffectivePreference(t *testing.T) {
	rig := newEngagementTestRig(t)
	rig.store.Close()
	if err := rig.svc.SetKindEnabled(context.Background(), engagement.KindWorkBlocked, false); err == nil {
		t.Fatal("closed store accepted preference")
	}
	spec, _ := engagement.Spec(engagement.KindWorkBlocked)
	if !rig.svc.Prefs().KindEnabled(spec) {
		t.Fatal("failed save changed live preference")
	}
}

func TestEngagementCompletionReadsResolvedPurposeLifecycle(t *testing.T) {
	rig := newHandoffTestRig(t)
	store, err := rig.app.purposeProductStore()
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-3 * time.Hour)
	created, _, err := store.Create(purposeTestCreateInput("engagement:purpose", PurposeKindCommitment, "measurement",
		[]PurposeEvidenceInput{purposeTestEvidence("engagement:purpose", PurposeEvidenceExplicitUser, PurposeStanceSupport, base.UnixMilli())}, base.UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	row := storage.EngagementMomentRow{Kind: "commitment_slipping", SubjectID: created.OpenLoop.ID}
	if completed, err := rig.app.engagementItemCompletedAt(context.Background(), row); err != nil || !completed.IsZero() {
		t.Fatalf("active purpose counted: %v %v", completed, err)
	}
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	when := base.Add(time.Hour)
	if _, _, err := store.AppendEvent(purposeTestAppendInput(detail, "engagement:resolved", PurposeEventResolved, PurposeStanceResolve, PurposeEvidenceExplicitUser, "measurement", when.UnixMilli())); err != nil {
		t.Fatal(err)
	}
	completed, err := rig.app.engagementItemCompletedAt(context.Background(), row)
	if err != nil || completed.UnixMilli() != when.UnixMilli() {
		t.Fatalf("resolved purpose = %v %v", completed, err)
	}
}
