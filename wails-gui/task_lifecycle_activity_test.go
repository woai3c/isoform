package main

import (
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func seedLifecycleTaskEpisode(t *testing.T, store *ActivityStore, startedAt int64) TaskEpisode {
	t.Helper()
	input := TaskOnsetInput{
		SourceKind:  "activity",
		SourceID:    "session-onset",
		WindowStart: startedAt,
		CutoffAt:    startedAt + 100,
		Evidence: []TaskOnsetEvidence{{
			Ref:        "event:onset",
			Kind:       "focus",
			SourceKind: "activity_event",
			SourceID:   "event-onset",
			OccurredAt: startedAt,
			Summary:    "Focused the implementation workspace",
		}},
	}
	response := TaskOnsetResponse{Episodes: []TaskOnsetPrediction{{
		LocalKey:           "implementation",
		Task:               "Implement the requested change",
		DesiredResult:      "A tested code change",
		InferredStartedAt:  startedAt,
		TaskConfidence:     0.9,
		BoundaryConfidence: 0.8,
		ResultConfidence:   0.75,
		EvidenceRefs:       []string{"event:onset"},
	}}}
	results, err := PersistTaskOnsetEpisodes(store, input, response, startedAt+200, nil)
	if err != nil {
		t.Fatalf("PersistTaskOnsetEpisodes: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("capture count = %d, want 1", len(results))
	}
	return results[0].Episode
}

func TestBuildTaskLifecycleInputFromActivitySeparatesPrefixAndSuffix(t *testing.T) {
	store := newTestStore(t)
	episode := seedLifecycleTaskEpisode(t, store, 1_000)
	verified := true
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"session-lifecycle",
		1_100,
		2_000,
		[]activitymon.ActivityEvent{
			{Timestamp: 1_050, Kind: activitymon.EventClick, ClickLabel: "before cutoff"},
			{Timestamp: 1_200, Kind: activitymon.EventClick, ClickLabel: "Save"},
			{
				Timestamp:             1_400,
				Kind:                  activitymon.EventSpeech,
				SpeechSource:          "microphone",
				SpeechSpeakerVerified: &verified,
				SpeechText:            "I'm done with this task",
			},
		},
	)
	if err != nil {
		t.Fatalf("BuildTaskLifecycleInputFromActivity: %v", err)
	}
	if len(input.Episodes) != 1 || input.Episodes[0].EpisodeID != episode.ID {
		t.Fatalf("episodes = %#v", input.Episodes)
	}
	if len(input.PrefixEvidence) == 0 {
		t.Fatal("expected immutable prefix evidence")
	}
	if len(input.SuffixEvidence) != 2 {
		t.Fatalf("suffix evidence = %d, want 2", len(input.SuffixEvidence))
	}
	if input.SuffixEvidence[0].Kind != "click" {
		t.Fatalf("save click kind = %q, want conservative click", input.SuffixEvidence[0].Kind)
	}
	if input.SuffixEvidence[1].Kind != "explicit_completion" {
		t.Fatalf("completion kind = %q", input.SuffixEvidence[1].Kind)
	}
	if input.SuffixEvidence[0].Actor != TaskLifecycleActorSystem ||
		input.SuffixEvidence[1].Actor != TaskLifecycleActorUser {
		t.Fatalf("suffix actors=%+v", input.SuffixEvidence)
	}
	for _, item := range input.SuffixEvidence {
		if item.OccurredAt <= input.PrefixCutoffAt {
			t.Fatalf("suffix item leaked across prefix boundary: %#v", item)
		}
	}
}

func TestBuildTaskLifecycleInputCanReobserveDetectionWindowAfterInferredStart(
	t *testing.T,
) {
	store := newTestStore(t)
	episodeInput := taskEpisodeFixture(
		"same-window-result",
		1_000,
		1_500,
		TaskEpisodeStatusCandidate,
	)
	episodeInput.InitialEvidence[0].ObservedAt = 1_200
	episode, added, err := store.CreateTaskEpisode(episodeInput)
	if err != nil || !added {
		t.Fatalf("create episode: added=%v err=%v", added, err)
	}
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"same-window",
		episode.InferredStartAt,
		1_600,
		[]activitymon.ActivityEvent{
			{
				Timestamp: 1_200,
				Kind:      activitymon.EventKeyboard,
				TypedText: "A complete update.",
			},
			{
				Timestamp:  1_300,
				Kind:       activitymon.EventClick,
				ClickLabel: "Send",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Episodes) != 1 ||
		len(input.Episodes[0].EvidenceRefs) == 0 ||
		len(input.SuffixEvidence) != 2 {
		t.Fatalf("same-window lifecycle input=%+v", input)
	}
	var hypothesisFound bool
	for _, evidence := range input.PrefixEvidence {
		if evidence.Kind == "task_hypothesis" {
			hypothesisFound = true
		}
		if evidence.SourceKind == "activity_session" {
			t.Fatalf("post-boundary onset evidence leaked into prefix: %+v", evidence)
		}
	}
	if !hypothesisFound {
		t.Fatal("persisted task hypothesis was not available as prefix context")
	}
	if input.SuffixEvidence[1].Kind != "sent" {
		t.Fatalf("terminal result was not reobserved: %+v", input.SuffixEvidence)
	}
}

func TestBuildTaskLifecycleSuffixPreservesRedactedCommittedText(t *testing.T) {
	evidence := buildTaskLifecycleSuffixEvidence(
		[]activitymon.ActivityEvent{
			{
				Timestamp:              1_200,
				Kind:                   activitymon.EventKeyboard,
				AppBundleID:            "com.apple.mail",
				AppName:                "Mail",
				WindowTitle:            "Customer update",
				TypedText:              "Hello, this is the completed draft.",
				TypedTextCaptureMethod: activitymon.TypedTextCaptureMethodAXFieldDiff,
			},
			{
				Timestamp:  1_300,
				Kind:       activitymon.EventClick,
				AppName:    "Mail",
				ClickLabel: "Send",
			},
		},
		1_100,
		1_400,
	)
	if len(evidence) != 2 {
		t.Fatalf("evidence=%+v, want keyboard and send", evidence)
	}
	if evidence[0].ContentKind != TaskLifecycleContentCommittedText ||
		evidence[0].Content != "Hello, this is the completed draft." ||
		evidence[0].ContentTruncated {
		t.Fatalf("committed text evidence=%+v", evidence[0])
	}
	if evidence[0].Actor != TaskLifecycleActorUser ||
		evidence[1].Actor != TaskLifecycleActorSystem {
		t.Fatalf("evidence actors=%+v", evidence)
	}
	if evidence[0].Destination == nil ||
		evidence[0].Destination.SemanticRole !=
			TaskLifecycleDestinationUserResultCandidate ||
		evidence[0].Destination.CaptureMethod !=
			activitymon.TypedTextCaptureMethodAXFieldDiff {
		t.Fatalf("committed text destination=%+v", evidence[0].Destination)
	}
	if evidence[1].Kind != "sent" || evidence[1].Content != "" {
		t.Fatalf("terminal evidence=%+v", evidence[1])
	}
}

func TestBuildTaskLifecycleSuffixUsesStableActivitySourceIdentity(t *testing.T) {
	target := activitymon.ActivityEvent{
		Timestamp:   1_300,
		Kind:        activitymon.EventClick,
		AppBundleID: "com.openai.chat",
		AppName:     "ChatGPT",
		WindowTitle: "Customer update",
		ClickRole:   "AXButton",
		ClickLabel:  "Send",
	}
	first := buildTaskLifecycleSuffixEvidence(
		[]activitymon.ActivityEvent{target},
		1_000,
		1_400,
	)
	second := buildTaskLifecycleSuffixEvidence(
		[]activitymon.ActivityEvent{
			{Timestamp: 1_100, Kind: activitymon.EventAppSwitch, AppName: "Mail"},
			target,
		},
		1_000,
		1_400,
	)
	if len(first) != 1 || len(second) != 2 {
		t.Fatalf("stable source fixtures first=%+v second=%+v", first, second)
	}
	if first[0].SourceID == "" ||
		first[0].SourceID != second[1].SourceID ||
		first[0].Ref != second[1].Ref {
		t.Fatalf("activity source identity changed with slice position: first=%+v second=%+v", first[0], second[1])
	}
}

func TestBuildTaskLifecycleInputConsumesPersistedActivitySourceOnce(t *testing.T) {
	store := newTestStore(t)
	episode := seedLifecycleTaskEpisode(t, store, 1_000)
	event := activitymon.ActivityEvent{
		Timestamp:  1_300,
		Kind:       activitymon.EventClick,
		AppName:    "Editor",
		ClickRole:  "AXButton",
		ClickLabel: "Open details",
	}
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"replayed-group",
		1_100,
		1_400,
		[]activitymon.ActivityEvent{event},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.SuffixEvidence) != 1 {
		t.Fatalf("first source input=%+v", input.SuffixEvidence)
	}
	if _, err := PersistTaskLifecycleObservations(
		store,
		input,
		TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
			LocalKey:     "unclear-open-details",
			Kind:         TaskLifecycleKindUnknown,
			EpisodeIDs:   []string{episode.ID},
			EvidenceRefs: []string{input.SuffixEvidence[0].Ref},
		}}},
		1_500,
	); err != nil {
		t.Fatal(err)
	}
	replayed, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"replayed-group",
		1_100,
		1_400,
		[]activitymon.ActivityEvent{event},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.SuffixEvidence) != 0 {
		t.Fatalf("persisted activity source was replayed: %+v", replayed.SuffixEvidence)
	}
}

func TestTaskLifecycleActivityReplayCannotCreateSecondStateBoundary(t *testing.T) {
	store := newTestStore(t)
	episode, added, err := store.CreateTaskEpisode(taskEpisodeFixture(
		"lifecycle-replayed-state-boundary",
		1_000,
		1_100,
		TaskEpisodeStatusActive,
	))
	if err != nil || !added {
		t.Fatalf("create: added=%v err=%v", added, err)
	}
	event := activitymon.ActivityEvent{
		Timestamp:   1_300,
		Kind:        activitymon.EventAppSwitch,
		AppBundleID: "com.openai.chat",
		AppName:     "ChatGPT",
		WindowTitle: "Usage remaining",
	}
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"replayed-state-group",
		1_200,
		1_400,
		[]activitymon.ActivityEvent{event},
	)
	if err != nil || len(input.SuffixEvidence) != 1 {
		t.Fatalf("first lifecycle input=%+v err=%v", input, err)
	}
	if _, err := PersistTaskLifecycleObservations(
		store,
		input,
		TaskLifecycleResponse{Observations: []TaskLifecycleObservation{{
			LocalKey:           "pause-for-usage-check",
			Kind:               TaskLifecycleKindPaused,
			EpisodeIDs:         []string{episode.ID},
			Confidence:         0.8,
			BoundaryConfidence: 0.7,
			EvidenceRefs:       []string{input.SuffixEvidence[0].Ref},
		}}},
		1_500,
	); err != nil {
		t.Fatal(err)
	}
	paused, err := store.GetTaskEpisodeDetail(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Episode.CurrentStatus != TaskEpisodeStatusPaused ||
		len(paused.Transitions) != 2 {
		t.Fatalf("first state boundary=%+v", paused)
	}
	replayed, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"replayed-state-group",
		1_200,
		1_400,
		[]activitymon.ActivityEvent{event},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.SuffixEvidence) != 0 {
		t.Fatalf("replayed source could create another boundary: %+v", replayed.SuffixEvidence)
	}
	after, err := store.GetTaskEpisodeDetail(episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Episode.CurrentStatus != TaskEpisodeStatusPaused ||
		len(after.Transitions) != len(paused.Transitions) {
		t.Fatalf("replay changed lifecycle: before=%+v after=%+v", paused.Transitions, after.Transitions)
	}
}

func TestBuildTaskLifecycleInputSuppressesExactLegacyActivityReplay(t *testing.T) {
	store := newTestStore(t)
	episode := seedLifecycleTaskEpisode(t, store, 1_000)
	event := activitymon.ActivityEvent{
		Timestamp: 1_300,
		Kind:      activitymon.EventKeyboard,
		AppName:   "Yansu",
		TypedText: "Continue the current task.",
	}
	summary := summarizeTaskLifecycleEvent(event)
	if _, _, err := store.AppendTaskEpisodeEvidence(TaskEpisodeEvidence{
		TaskEpisodeID: episode.ID,
		Role:          TaskEpisodeEvidenceAction,
		SourceKind:    "activity_event",
		SourceID:      "0",
		Summary:       summary,
		ObservedAt:    event.Timestamp,
		CreatedAt:     1_400,
	}); err != nil {
		t.Fatal(err)
	}
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"legacy-replayed-group",
		1_100,
		1_500,
		[]activitymon.ActivityEvent{event},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.SuffixEvidence) != 0 {
		t.Fatalf("legacy activity source was replayed: %+v", input.SuffixEvidence)
	}
}

func TestBuildTaskLifecycleSuffixClassifiesOnlyCommitReturnGestures(t *testing.T) {
	evidence := buildTaskLifecycleSuffixEvidence(
		[]activitymon.ActivityEvent{
			{
				Timestamp:              1_200,
				Kind:                   activitymon.EventKeyboard,
				AppBundleID:            "com.openai.chat",
				AppName:                "ChatGPT",
				KeyChars:               []string{"h", "i", "Return"},
				TypedText:              "First submitted draft.",
				TypedTextCaptureMethod: activitymon.TypedTextCaptureMethodAXFieldDiff,
			},
			{
				Timestamp:              1_300,
				Kind:                   activitymon.EventKeyboard,
				AppBundleID:            "com.apple.mail",
				AppName:                "Mail",
				KeyChars:               []string{"⌘Return"},
				TypedText:              "Second submitted draft.",
				TypedTextCaptureMethod: activitymon.TypedTextCaptureMethodAXFieldDiff,
			},
			{
				Timestamp: 1_400,
				Kind:      activitymon.EventKeyboard,
				AppName:   "ChatGPT",
				KeyChars:  []string{"⇧Return"},
				TypedText: "A newline, not a submission.",
			},
		},
		1_100,
		1_500,
	)
	if len(evidence) != 3 {
		t.Fatalf("evidence=%+v", evidence)
	}
	if evidence[0].Kind != TaskLifecycleEvidenceTextCommit ||
		evidence[1].Kind != TaskLifecycleEvidenceTextCommit {
		t.Fatalf("commit gestures=%+v", evidence[:2])
	}
	if evidence[2].Kind != string(activitymon.EventKeyboard) {
		t.Fatalf("Shift+Return kind=%q, want keyboard", evidence[2].Kind)
	}
	for _, index := range []int{0, 1} {
		if evidence[index].ContentKind != TaskLifecycleContentCommittedText ||
			!strings.Contains(evidence[index].Summary, "possible text commit") {
			t.Fatalf("commit evidence=%+v", evidence[index])
		}
	}
	if evidence[0].Destination.SemanticRole !=
		TaskLifecycleDestinationDelegatedAIPrompt ||
		evidence[1].Destination.SemanticRole !=
			TaskLifecycleDestinationUserResultCandidate {
		t.Fatalf("destination roles=%+v", evidence[:2])
	}
}

func TestBuildTaskLifecycleInputFromActivityRejectsUnverifiedCompletionSpeech(t *testing.T) {
	store := newTestStore(t)
	seedLifecycleTaskEpisode(t, store, 1_000)
	unverified := false
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"session-lifecycle",
		1_100,
		2_000,
		[]activitymon.ActivityEvent{{
			Timestamp:             1_400,
			Kind:                  activitymon.EventSpeech,
			SpeechSource:          "microphone",
			SpeechSpeakerVerified: &unverified,
			SpeechText:            "I'm done with this task",
		}},
	)
	if err != nil {
		t.Fatalf("BuildTaskLifecycleInputFromActivity: %v", err)
	}
	if got := input.SuffixEvidence[0].Kind; got == "explicit_completion" {
		t.Fatalf("unverified speaker was treated as completion: %q", got)
	}
	if got := input.SuffixEvidence[0].Actor; got != TaskLifecycleActorUnknown {
		t.Fatalf("unverified speaker actor=%q, want unknown", got)
	}
}

func TestBuildTaskLifecycleSuffixMarksSystemAudioAsOtherParty(t *testing.T) {
	evidence := buildTaskLifecycleSuffixEvidence(
		[]activitymon.ActivityEvent{{
			Timestamp:    1_200,
			Kind:         activitymon.EventSpeech,
			SpeechSource: "system_audio",
			SpeechText:   "We need this because it is urgent.",
		}},
		1_100,
		1_300,
	)
	if len(evidence) != 1 ||
		evidence[0].Actor != TaskLifecycleActorOtherParty {
		t.Fatalf("system audio evidence=%+v", evidence)
	}
}

func TestBuildTaskLifecycleInputFromActivityKeepsOverlappingEpisodes(t *testing.T) {
	store := newTestStore(t)
	first := seedLifecycleTaskEpisode(t, store, 1_000)
	second := seedLifecycleTaskEpisode(t, store, 1_001)
	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"session-lifecycle",
		1_200,
		2_000,
		[]activitymon.ActivityEvent{{
			Timestamp:  1_300,
			Kind:       activitymon.EventClick,
			ClickLabel: "Merge pull request",
		}},
	)
	if err != nil {
		t.Fatalf("BuildTaskLifecycleInputFromActivity: %v", err)
	}
	if len(input.Episodes) != 2 {
		t.Fatalf("episodes = %d, want 2", len(input.Episodes))
	}
	ids := map[string]bool{}
	for _, episode := range input.Episodes {
		ids[episode.EpisodeID] = true
	}
	if !ids[first.ID] || !ids[second.ID] {
		t.Fatalf("overlapping episode IDs missing: %#v", ids)
	}
	if got := input.SuffixEvidence[0].Kind; got != "merged" {
		t.Fatalf("merge event kind = %q", got)
	}
}

func TestBuildTaskLifecycleInputFromActivityExcludesEpisodesStartedInSuffix(t *testing.T) {
	store := newTestStore(t)
	prefixEpisode := seedLifecycleTaskEpisode(t, store, 1_000)
	suffixEpisode := seedLifecycleTaskEpisode(t, store, 1_300)
	if suffixEpisode.InferredStartAt <= 1_200 {
		t.Fatalf("suffix episode fixture started at %d", suffixEpisode.InferredStartAt)
	}

	input, err := BuildTaskLifecycleInputFromActivity(
		store,
		"activity",
		"session-lifecycle",
		1_200,
		2_000,
		[]activitymon.ActivityEvent{{
			Timestamp:  1_400,
			Kind:       activitymon.EventClick,
			ClickLabel: "Save",
		}},
	)
	if err != nil {
		t.Fatalf("BuildTaskLifecycleInputFromActivity: %v", err)
	}
	if len(input.Episodes) != 1 || input.Episodes[0].EpisodeID != prefixEpisode.ID {
		t.Fatalf("prefix episodes = %#v, want only %q", input.Episodes, prefixEpisode.ID)
	}
}
