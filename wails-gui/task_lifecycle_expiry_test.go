package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func TestTaskLifecycleExpiryUsesStatusSpecificObservationHorizons(t *testing.T) {
	for _, test := range []struct {
		status  string
		horizon time.Duration
	}{
		{TaskEpisodeStatusCandidate, taskLifecycleCandidateHorizon},
		{TaskEpisodeStatusActive, taskLifecycleActiveHorizon},
		{TaskEpisodeStatusPaused, taskLifecyclePausedHorizon},
	} {
		t.Run(test.status, func(t *testing.T) {
			store := newTestStore(t)
			episode, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
				"expiry-"+test.status,
				1_000,
				2_000,
				test.status,
			))
			if err != nil || !added {
				t.Fatalf("create: added=%v err=%v", added, err)
			}
			before := expireStaleTaskEpisodes(
				store,
				episode.DetectedAt+test.horizon.Milliseconds()-1,
				episode.DetectedAt+test.horizon.Milliseconds(),
			)
			if len(before.ExpiredIDs) != 0 || len(before.Errors) != 0 {
				t.Fatalf("expired before boundary: %+v", before)
			}
			at := episode.DetectedAt + test.horizon.Milliseconds()
			result := expireStaleTaskEpisodes(store, at, at+1)
			if len(result.Errors) != 0 ||
				len(result.ExpiredIDs) != 1 ||
				result.ExpiredIDs[0] != episode.ID {
				t.Fatalf("expiry=%+v", result)
			}
			got, err := store.GetTaskEpisodeDetail(episode.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Episode.CurrentStatus != TaskEpisodeStatusExpired ||
				!strings.Contains(
					got.Transitions[len(got.Transitions)-1].Reason,
					taskLifecycleExpiryPolicyVersion,
				) {
				t.Fatalf("expired detail=%+v", got)
			}
			retry := expireStaleTaskEpisodes(store, at, at+2)
			if len(retry.ExpiredIDs) != 0 || len(retry.Errors) != 0 {
				t.Fatalf("idempotent retry=%+v", retry)
			}
		})
	}
}

func TestTaskLifecycleExpiryIgnoresAttachedEvidenceForLeaseRenewal(t *testing.T) {
	store := newTestStore(t)
	episode, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"expiry-state-bound-lease",
		1_000,
		2_000,
		TaskEpisodeStatusActive,
	))
	if err != nil || !added {
		t.Fatalf("create: added=%v err=%v", added, err)
	}
	latestSignalAt := episode.DetectedAt + taskLifecycleActiveHorizon.Milliseconds() - 1
	if _, _, err := store.AppendTaskEpisodeEvidence(TaskEpisodeEvidence{
		TaskEpisodeID: episode.ID,
		Role:          TaskEpisodeEvidenceAction,
		SourceKind:    "activity_event",
		SourceID:      "continued-work",
		Summary:       "The user continued the observed task.",
		ObservedAt:    latestSignalAt,
		CreatedAt:     latestSignalAt + 1,
	}); err != nil {
		t.Fatal(err)
	}
	result := expireStaleTaskEpisodes(
		store,
		episode.DetectedAt+taskLifecycleActiveHorizon.Milliseconds(),
		episode.DetectedAt+taskLifecycleActiveHorizon.Milliseconds()+1,
	)
	if len(result.Errors) != 0 ||
		len(result.ExpiredIDs) != 1 ||
		result.ExpiredIDs[0] != episode.ID {
		t.Fatalf("attached evidence renewed the lease: %+v", result)
	}
	detail, err := store.GetTaskEpisodeDetail(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Episode.CurrentStatus != TaskEpisodeStatusExpired ||
		len(detail.Evidence) != 2 {
		t.Fatalf("expired detail=%+v", detail)
	}
}

func TestTaskLifecycleStateTransitionEstablishesNewLease(t *testing.T) {
	store := newTestStore(t)
	episode, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"expiry-transition-boundary",
		1_000,
		2_000,
		TaskEpisodeStatusActive,
	))
	if err != nil || !added {
		t.Fatalf("create: added=%v err=%v", added, err)
	}
	pausedAt := int64(10_000)
	transition, added, err := store.AppendTaskEpisodeTransition(TaskEpisodeTransition{
		TaskEpisodeID: episode.ID,
		FromStatus:    TaskEpisodeStatusActive,
		ToStatus:      TaskEpisodeStatusPaused,
		Reason:        "The user moved to different work.",
		OccurredAt:    pausedAt,
		CreatedAt:     pausedAt + 1,
	})
	if err != nil || !added {
		t.Fatalf("pause: added=%v err=%v", added, err)
	}
	detail, err := store.GetTaskEpisodeDetail(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease := taskLifecycleObservationLease(
		detail,
		pausedAt+taskLifecyclePausedHorizon.Milliseconds()-1,
	)
	if lease.AnchorAt != pausedAt ||
		lease.AnchorKind != "state_transition" ||
		lease.AnchorTransitionID != transition.ID ||
		lease.Due || lease.HorizonMs != taskLifecyclePausedHorizon.Milliseconds() {
		t.Fatalf("paused lease=%+v", lease)
	}
	resumedAt := int64(20_000)
	resume, added, err := store.AppendTaskEpisodeTransition(TaskEpisodeTransition{
		TaskEpisodeID: episode.ID,
		FromStatus:    TaskEpisodeStatusPaused,
		ToStatus:      TaskEpisodeStatusActive,
		Reason:        "The user returned to the observed task.",
		OccurredAt:    resumedAt,
		CreatedAt:     resumedAt + 1,
	})
	if err != nil || !added {
		t.Fatalf("resume: added=%v err=%v", added, err)
	}
	detail, err = store.GetTaskEpisodeDetail(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease = taskLifecycleObservationLease(
		detail,
		resumedAt+taskLifecycleActiveHorizon.Milliseconds()-1,
	)
	if lease.AnchorAt != resumedAt ||
		lease.AnchorTransitionID != resume.ID ||
		lease.Due || lease.HorizonMs != taskLifecycleActiveHorizon.Milliseconds() {
		t.Fatalf("resumed lease=%+v", lease)
	}
	result := expireStaleTaskEpisodes(
		store,
		resumedAt+taskLifecycleActiveHorizon.Milliseconds(),
		resumedAt+taskLifecycleActiveHorizon.Milliseconds()+1,
	)
	if len(result.Errors) != 0 ||
		len(result.ExpiredIDs) != 1 ||
		result.ExpiredIDs[0] != episode.ID {
		t.Fatalf("state-bound expiry=%+v", result)
	}
}

func TestTaskLifecycleInputExcludesLeaseDueEpisodeWithoutExpiryWrite(t *testing.T) {
	store := newTestStore(t)
	episode, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"lifecycle-due-before-expiry-write",
		1_000,
		2_000,
		TaskEpisodeStatusActive,
	))
	if err != nil || !added {
		t.Fatalf("create: added=%v err=%v", added, err)
	}
	prefixCutoffAt := episode.DetectedAt + taskLifecycleActiveHorizon.Milliseconds()
	if _, _, err := store.AppendTaskEpisodeEvidence(TaskEpisodeEvidence{
		TaskEpisodeID: episode.ID,
		Role:          TaskEpisodeEvidenceAction,
		SourceKind:    "activity_event",
		SourceID:      "recent-but-non-renewing-action",
		Summary:       "A later generic activity was associated with this old task.",
		ObservedAt:    prefixCutoffAt - 1,
		CreatedAt:     prefixCutoffAt,
	}); err != nil {
		t.Fatal(err)
	}
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"state-bound-filter",
		prefixCutoffAt,
		prefixCutoffAt+1,
		[]activitymon.ActivityEvent{{
			Timestamp: prefixCutoffAt + 1,
			Kind:      activitymon.EventKeyboard,
			TypedText: "Start unrelated current work.",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Episodes) != 0 || len(input.PrefixEvidence) != 0 {
		t.Fatalf("due episode entered lifecycle input: %+v", input)
	}
	unchanged, err := store.GetTaskEpisode(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.CurrentStatus != TaskEpisodeStatusActive {
		t.Fatalf("read-only filter mutated status=%s", unchanged.CurrentStatus)
	}
}

func TestTaskLifecycleExpiryRemovesStaleEpisodesFromLaterPrompt(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < taskLifecycleMaxEpisodes; i++ {
		_, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
			fmt.Sprintf("stale-%02d", i),
			1_000+int64(i),
			2_000+int64(i),
			TaskEpisodeStatusActive,
		))
		if err != nil || !added {
			t.Fatalf("create stale %d: added=%v err=%v", i, added, err)
		}
	}
	fresh, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"fresh-current-work",
		100_000_000,
		100_000_100,
		TaskEpisodeStatusCandidate,
	))
	if err != nil || !added {
		t.Fatalf("create fresh: added=%v err=%v", added, err)
	}
	cutoffAt := int64(100_100_000)
	expiry := expireStaleTaskEpisodes(store, cutoffAt, cutoffAt+1)
	if len(expiry.Errors) != 0 || len(expiry.ExpiredIDs) != taskLifecycleMaxEpisodes {
		t.Fatalf("expiry=%+v", expiry)
	}
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"later-group",
		cutoffAt-1_000,
		cutoffAt,
		[]activitymon.ActivityEvent{{
			Timestamp: cutoffAt,
			Kind:      activitymon.EventKeyboard,
			TypedText: "Continuing current work.",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Episodes) != 1 || input.Episodes[0].EpisodeID != fresh.ID {
		t.Fatalf("later lifecycle episodes=%+v", input.Episodes)
	}
}
