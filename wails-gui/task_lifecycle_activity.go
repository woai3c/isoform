package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

const (
	taskLifecycleRecentCompletedHorizonMs = int64(24 * 60 * 60 * 1000)
	taskLifecycleEvidenceContentLimit     = 2_048
	taskLifecycleActivitySourcePolicy     = "task-lifecycle-activity-source-v1"
)

func taskLifecycleCommittedText(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	value, _ = pii.RedactText(value)
	if len(value) <= taskLifecycleEvidenceContentLimit {
		return value, false
	}
	return truncateAtRuneBoundary(
		value,
		taskLifecycleEvidenceContentLimit,
	) + "...", true
}

func taskLifecycleEpisodeEligible(episode TaskEpisode, cutoffAt int64) bool {
	if episode.InferredStartAt > cutoffAt {
		return false
	}
	switch episode.CurrentStatus {
	case TaskEpisodeStatusCandidate, TaskEpisodeStatusActive, TaskEpisodeStatusPaused:
		return true
	case TaskEpisodeStatusCompleted:
		return cutoffAt-episode.InferredStartAt <= taskLifecycleRecentCompletedHorizonMs
	default:
		return false
	}
}

func taskLifecycleEpisodePriority(status string) int {
	switch status {
	case TaskEpisodeStatusActive:
		return 0
	case TaskEpisodeStatusCandidate:
		return 1
	case TaskEpisodeStatusPaused:
		return 2
	case TaskEpisodeStatusCompleted:
		return 3
	default:
		return 4
	}
}

func taskLifecycleEvidenceRef(item TaskEpisodeEvidence) string {
	return "task-episode-evidence:" + item.ID
}

func taskLifecycleSnapshotAlternatives(
	hypothesis TaskEpisodeHypothesisRevision,
) []TaskLifecycleEpisodeAlternative {
	result := make([]TaskLifecycleEpisodeAlternative, 0, len(hypothesis.Alternatives))
	for _, alternative := range hypothesis.Alternatives {
		switch alternative.Kind {
		case "task":
			result = append(result, TaskLifecycleEpisodeAlternative{
				Task:          alternative.Statement,
				DesiredResult: hypothesis.DesiredResultHypothesis,
				Confidence:    alternative.Confidence,
			})
		case "desired_result":
			result = append(result, TaskLifecycleEpisodeAlternative{
				Task:          hypothesis.TaskHypothesis,
				DesiredResult: alternative.Statement,
				Confidence:    alternative.Confidence,
			})
		}
	}
	return result
}

func taskLifecyclePrefixEvidence(
	details []TaskEpisodeDetail,
	prefixCutoffAt int64,
) ([]TaskLifecycleEvidence, map[string][]string, map[string][]string) {
	type candidate struct {
		episodeID string
		evidence  TaskEpisodeEvidence
		required  bool
	}
	candidates := make([]candidate, 0)
	for _, detail := range details {
		requiredIDs := make(map[string]struct{}, len(detail.Episode.LatestHypothesis.EvidenceIDs))
		for _, id := range detail.Episode.LatestHypothesis.EvidenceIDs {
			requiredIDs[id] = struct{}{}
		}
		for _, item := range detail.Evidence {
			if item.ObservedAt > prefixCutoffAt {
				continue
			}
			_, required := requiredIDs[item.ID]
			if item.Role == TaskEpisodeEvidenceResult {
				required = true
			}
			candidates = append(candidates, candidate{
				episodeID: detail.Episode.ID,
				evidence:  item,
				required:  required,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].required != candidates[j].required {
			return candidates[i].required
		}
		if candidates[i].evidence.ObservedAt != candidates[j].evidence.ObservedAt {
			return candidates[i].evidence.ObservedAt > candidates[j].evidence.ObservedAt
		}
		return candidates[i].evidence.ID < candidates[j].evidence.ID
	})
	actualEvidenceLimit := max(
		0,
		taskLifecycleMaxPrefixEvidence-len(details),
	)
	if len(candidates) > actualEvidenceLimit {
		candidates = candidates[:actualEvidenceLimit]
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].evidence.ObservedAt != candidates[j].evidence.ObservedAt {
			return candidates[i].evidence.ObservedAt < candidates[j].evidence.ObservedAt
		}
		return candidates[i].evidence.ID < candidates[j].evidence.ID
	})

	evidence := make([]TaskLifecycleEvidence, 0, len(candidates))
	refsByEpisode := make(map[string][]string)
	resultRefsByEpisode := make(map[string][]string)
	for _, item := range candidates {
		ref := taskLifecycleEvidenceRef(item.evidence)
		evidence = append(evidence, TaskLifecycleEvidence{
			Ref:        ref,
			Kind:       item.evidence.Role,
			SourceKind: "task_episode_evidence",
			SourceID:   item.evidence.ID,
			OccurredAt: item.evidence.ObservedAt,
			Summary:    item.evidence.Summary,
		})
		refsByEpisode[item.episodeID] = append(refsByEpisode[item.episodeID], ref)
		if item.evidence.Role == TaskEpisodeEvidenceResult {
			resultRefsByEpisode[item.episodeID] = append(
				resultRefsByEpisode[item.episodeID],
				ref,
			)
		}
	}
	for _, detail := range details {
		episode := detail.Episode
		hypothesis := episode.LatestHypothesis
		sourceID := strings.TrimSpace(hypothesis.ID)
		if sourceID == "" {
			sourceID = fmt.Sprintf(
				"%s:revision:%d",
				episode.ID,
				hypothesis.Revision,
			)
		}
		ref := "task-episode-hypothesis:" + sourceID
		occurredAt := min(prefixCutoffAt, episode.InferredStartAt)
		evidence = append(evidence, TaskLifecycleEvidence{
			Ref:        ref,
			Kind:       "task_hypothesis",
			SourceKind: "task_episode_hypothesis",
			SourceID:   sourceID,
			OccurredAt: occurredAt,
			Summary: strings.Join([]string{
				"Task hypothesis: " + hypothesis.TaskHypothesis,
				"Desired result hypothesis: " +
					hypothesis.DesiredResultHypothesis,
			}, "\n"),
		})
		refsByEpisode[episode.ID] = append(
			refsByEpisode[episode.ID],
			ref,
		)
		sort.Strings(refsByEpisode[episode.ID])
	}
	sort.SliceStable(evidence, func(i, j int) bool {
		if evidence[i].OccurredAt != evidence[j].OccurredAt {
			return evidence[i].OccurredAt < evidence[j].OccurredAt
		}
		return evidence[i].Ref < evidence[j].Ref
	})
	return evidence, refsByEpisode, resultRefsByEpisode
}

func classifyTaskLifecycleEvent(event activitymon.ActivityEvent) string {
	if event.Kind == activitymon.EventSpeech &&
		event.SpeechSource == "microphone" &&
		(event.SpeechSpeakerVerified == nil || *event.SpeechSpeakerVerified) {
		speech := strings.ToLower(strings.Join(strings.Fields(event.SpeechText), " "))
		switch {
		case strings.Contains(speech, "i am done"),
			strings.Contains(speech, "i'm done"),
			strings.Contains(speech, "i finished"),
			strings.Contains(speech, "we are done"),
			strings.Contains(speech, "we're done"):
			return "explicit_completion"
		case strings.Contains(speech, "i give up"),
			strings.Contains(speech, "i'm abandoning"),
			strings.Contains(speech, "i am abandoning"):
			return "explicit_abandonment"
		}
	}
	if event.Kind == activitymon.EventKeyboard {
		if taskLifecycleTextCommitCandidate(event) {
			return TaskLifecycleEvidenceTextCommit
		}
		return string(event.Kind)
	}
	if event.Kind != activitymon.EventClick {
		return string(event.Kind)
	}
	label := strings.ToLower(strings.Join(strings.Fields(event.ClickLabel), " "))
	switch {
	case label == "send" || strings.HasPrefix(label, "send "):
		return "sent"
	case label == "submit" || strings.HasPrefix(label, "submit "):
		return "submission"
	case label == "publish" || strings.HasPrefix(label, "publish "):
		return "published"
	case label == "merge" || strings.HasPrefix(label, "merge "):
		return "merged"
	case label == "discard" || strings.HasPrefix(label, "discard "):
		return "discarded"
	case label == "cancel task" || label == "cancel job":
		return "cancelled"
	default:
		return string(event.Kind)
	}
}

func taskLifecycleEventActor(event activitymon.ActivityEvent) string {
	switch event.Kind {
	case activitymon.EventKeyboard:
		return TaskLifecycleActorUser
	case activitymon.EventSpeech:
		switch event.SpeechSource {
		case "microphone":
			if event.SpeechSpeakerVerified == nil ||
				*event.SpeechSpeakerVerified {
				return TaskLifecycleActorUser
			}
			return TaskLifecycleActorUnknown
		case "system_audio":
			return TaskLifecycleActorOtherParty
		default:
			return TaskLifecycleActorUnknown
		}
	default:
		return TaskLifecycleActorSystem
	}
}

func taskLifecycleTextCommitCandidate(event activitymon.ActivityEvent) bool {
	if event.Kind != activitymon.EventKeyboard {
		return false
	}
	for _, key := range event.GetKeyChars() {
		switch strings.TrimSpace(key) {
		case "Return", "⌘Return":
			return true
		}
	}
	return false
}

func summarizeTaskLifecycleEvent(event activitymon.ActivityEvent) string {
	summary := summarizeTaskOnsetEvent(event)
	if summary == "" || !taskLifecycleTextCommitCandidate(event) {
		return summary
	}
	return summary + ". The keyboard burst ended with Return, a possible text commit"
}

func taskLifecycleActivityEventSourceID(
	event activitymon.ActivityEvent,
) string {
	speakerVerified := "unknown"
	if event.SpeechSpeakerVerified != nil {
		speakerVerified = strconv.FormatBool(*event.SpeechSpeakerVerified)
	}
	payload, _ := json.Marshal(struct {
		Timestamp              int64    `json:"timestamp"`
		Kind                   string   `json:"kind"`
		AppBundleID            string   `json:"appBundleId"`
		AppName                string   `json:"appName"`
		WindowTitle            string   `json:"windowTitle"`
		ClickX                 string   `json:"clickX"`
		ClickY                 string   `json:"clickY"`
		ClickRole              string   `json:"clickRole"`
		ClickLabel             string   `json:"clickLabel"`
		KeyCode                uint16   `json:"keyCode"`
		Modifiers              uint64   `json:"modifiers"`
		KeyChar                string   `json:"keyChar"`
		KeyChars               []string `json:"keyChars"`
		TypedText              string   `json:"typedText"`
		TypedTextCaptureMethod string   `json:"typedTextCaptureMethod"`
		IMEComposing           bool     `json:"imeComposing"`
		FocusRole              string   `json:"focusRole"`
		FocusLabel             string   `json:"focusLabel"`
		SpeechText             string   `json:"speechText"`
		SpeechSource           string   `json:"speechSource"`
		SpeechLikelyEcho       bool     `json:"speechLikelyEcho"`
		SpeechSpeakerVerified  string   `json:"speechSpeakerVerified"`
		MeetingCandidate       bool     `json:"meetingCandidate"`
		MeetingCandidateApp    string   `json:"meetingCandidateApp"`
		MeetingCandidateReason string   `json:"meetingCandidateReason"`
	}{
		Timestamp:              event.Timestamp,
		Kind:                   string(event.Kind),
		AppBundleID:            event.AppBundleID,
		AppName:                event.AppName,
		WindowTitle:            event.WindowTitle,
		ClickX:                 strconv.FormatFloat(event.ClickX, 'g', -1, 64),
		ClickY:                 strconv.FormatFloat(event.ClickY, 'g', -1, 64),
		ClickRole:              event.ClickRole,
		ClickLabel:             event.ClickLabel,
		KeyCode:                event.KeyCode,
		Modifiers:              event.Modifiers,
		KeyChar:                event.KeyChar,
		KeyChars:               append([]string(nil), event.KeyChars...),
		TypedText:              event.TypedText,
		TypedTextCaptureMethod: event.TypedTextCaptureMethod,
		IMEComposing:           event.IMEComposing,
		FocusRole:              event.FocusRole,
		FocusLabel:             event.FocusLabel,
		SpeechText:             event.SpeechText,
		SpeechSource:           event.SpeechSource,
		SpeechLikelyEcho:       event.SpeechLikelyEcho,
		SpeechSpeakerVerified:  speakerVerified,
		MeetingCandidate:       event.MeetingCandidate,
		MeetingCandidateApp:    event.MeetingCandidateApp,
		MeetingCandidateReason: event.MeetingCandidateReason,
	})
	return deterministicTaskEpisodeID(
		taskLifecycleActivitySourcePolicy,
		taskEpisodePayloadHash(payload),
	)
}

func taskLifecycleLegacyActivityEvidenceKey(
	observedAt int64,
	summary string,
) string {
	return fmt.Sprintf("%d\x00%s", observedAt, strings.TrimSpace(summary))
}

func (s *ActivityStore) filterUnseenTaskLifecycleSuffixEvidence(
	items []TaskLifecycleEvidence,
) ([]TaskLifecycleEvidence, error) {
	if len(items) == 0 {
		return []TaskLifecycleEvidence{}, nil
	}
	if s == nil || s.db == nil {
		return nil, errors.New("activity store is unavailable")
	}
	windowStart := items[0].OccurredAt
	windowEnd := items[0].OccurredAt
	for _, item := range items[1:] {
		windowStart = min(windowStart, item.OccurredAt)
		windowEnd = max(windowEnd, item.OccurredAt)
	}
	rows, err := s.db.Query(`
		SELECT source_id, observed_at, summary
		FROM task_episode_evidence
		WHERE source_kind = 'activity_event'
			AND observed_at >= ? AND observed_at <= ?`,
		windowStart,
		windowEnd,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seenSourceIDs := make(map[string]struct{})
	seenLegacy := make(map[string]struct{})
	for rows.Next() {
		var sourceID, summary string
		var observedAt int64
		if err := rows.Scan(&sourceID, &observedAt, &summary); err != nil {
			return nil, err
		}
		seenSourceIDs[sourceID] = struct{}{}
		seenLegacy[taskLifecycleLegacyActivityEvidenceKey(
			observedAt,
			summary,
		)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]TaskLifecycleEvidence, 0, len(items))
	for _, item := range items {
		if _, seen := seenSourceIDs[item.SourceID]; seen {
			continue
		}
		if _, seen := seenLegacy[taskLifecycleLegacyActivityEvidenceKey(
			item.OccurredAt,
			item.Summary,
		)]; seen {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

func buildTaskLifecycleSuffixEvidence(
	events []activitymon.ActivityEvent,
	prefixCutoffAt, cutoffAt int64,
) []TaskLifecycleEvidence {
	sorted := append([]activitymon.ActivityEvent(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Timestamp != sorted[j].Timestamp {
			return sorted[i].Timestamp < sorted[j].Timestamp
		}
		return i < j
	})
	result := make([]TaskLifecycleEvidence, 0, min(len(sorted), taskLifecycleMaxSuffixEvidence))
	previous := ""
	seenSources := make(map[string]struct{}, len(sorted))
	for _, event := range sorted {
		if event.Timestamp <= prefixCutoffAt || event.Timestamp > cutoffAt {
			continue
		}
		summary := summarizeTaskLifecycleEvent(event)
		if summary == "" || summary == previous {
			continue
		}
		previous = summary
		sourceID := taskLifecycleActivityEventSourceID(event)
		if _, seen := seenSources[sourceID]; seen {
			continue
		}
		seenSources[sourceID] = struct{}{}
		contentKind := ""
		content := ""
		contentTruncated := false
		if event.Kind == activitymon.EventKeyboard {
			content, contentTruncated = taskLifecycleCommittedText(
				event.TypedText,
			)
			if content != "" {
				contentKind = TaskLifecycleContentCommittedText
			}
		}
		result = append(result, TaskLifecycleEvidence{
			Ref:              "lifecycle-event:" + sourceID,
			Kind:             classifyTaskLifecycleEvent(event),
			SourceKind:       "activity_event",
			SourceID:         sourceID,
			OccurredAt:       event.Timestamp,
			Summary:          summary,
			ContentKind:      contentKind,
			Content:          content,
			ContentTruncated: contentTruncated,
			Actor:            taskLifecycleEventActor(event),
			Destination:      taskLifecycleResultDestinationForEvent(event),
		})
		if len(result) == taskLifecycleMaxSuffixEvidence {
			break
		}
	}
	return result
}

// BuildTaskLifecycleInputFromActivity creates one production observation
// boundary. Prefix evidence is immutable TaskEpisode history at or before the
// boundary; suffix evidence is newly observed activity after it.
func BuildTaskLifecycleInputFromActivity(
	store *ActivityStore,
	sourceKind, sourceID string,
	prefixCutoffAt, cutoffAt int64,
	events []activitymon.ActivityEvent,
) (TaskLifecycleInput, error) {
	if store == nil || store.db == nil {
		return TaskLifecycleInput{}, errors.New("activity store is unavailable")
	}
	if prefixCutoffAt <= 0 || cutoffAt <= prefixCutoffAt {
		return TaskLifecycleInput{}, errors.New("task lifecycle activity window is invalid")
	}
	// Activity outcomes are a separate observational ledger, independent of
	// Shadow qualification and lifecycle model availability.
	if err := store.observeActivityOutcomesInWindow(prefixCutoffAt, cutoffAt); err != nil {
		return TaskLifecycleInput{}, err
	}
	episodes, err := store.ListTaskEpisodes("", taskEpisodeMaxListLimit)
	if err != nil {
		return TaskLifecycleInput{}, err
	}
	repeatedOnsets, err := store.taskEpisodeRepeatedOnsetSet()
	if err != nil {
		return TaskLifecycleInput{}, err
	}
	details := make([]TaskEpisodeDetail, 0, len(episodes))
	for _, episode := range episodes {
		if _, repeated := repeatedOnsets[episode.ID]; repeated {
			continue
		}
		if !taskLifecycleEpisodeEligible(episode, prefixCutoffAt) {
			continue
		}
		detail, err := store.GetTaskEpisodeDetail(episode.ID)
		if err != nil {
			return TaskLifecycleInput{}, err
		}
		if taskLifecycleObservationLease(detail, prefixCutoffAt).Due {
			continue
		}
		details = append(details, detail)
	}
	sort.SliceStable(details, func(i, j int) bool {
		left := details[i].Episode
		right := details[j].Episode
		leftPriority := taskLifecycleEpisodePriority(left.CurrentStatus)
		rightPriority := taskLifecycleEpisodePriority(right.CurrentStatus)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if left.InferredStartAt != right.InferredStartAt {
			return left.InferredStartAt > right.InferredStartAt
		}
		return left.ID < right.ID
	})
	if len(details) > taskLifecycleMaxEpisodes {
		details = details[:taskLifecycleMaxEpisodes]
	}
	prefix, refsByEpisode, resultRefsByEpisode := taskLifecyclePrefixEvidence(
		details,
		prefixCutoffAt,
	)
	snapshots := make([]TaskLifecycleEpisodeSnapshot, 0, len(details))
	for _, detail := range details {
		episode := detail.Episode
		snapshots = append(snapshots, TaskLifecycleEpisodeSnapshot{
			EpisodeID:               episode.ID,
			CurrentStatus:           episode.CurrentStatus,
			InferredStartedAt:       episode.InferredStartAt,
			Task:                    episode.LatestHypothesis.TaskHypothesis,
			DesiredResult:           episode.LatestHypothesis.DesiredResultHypothesis,
			TaskConfidence:          episode.LatestHypothesis.TaskConfidence,
			BoundaryConfidence:      episode.LatestHypothesis.BoundaryConfidence,
			EvidenceRefs:            refsByEpisode[episode.ID],
			KnownResultEvidenceRefs: resultRefsByEpisode[episode.ID],
			Alternatives:            taskLifecycleSnapshotAlternatives(episode.LatestHypothesis),
		})
	}
	suffix := buildTaskLifecycleSuffixEvidence(events, prefixCutoffAt, cutoffAt)
	suffix, err = store.filterUnseenTaskLifecycleSuffixEvidence(suffix)
	if err != nil {
		return TaskLifecycleInput{}, err
	}
	return normalizeTaskLifecycleInput(TaskLifecycleInput{
		SourceKind:     sourceKind,
		SourceID:       sourceID,
		PrefixCutoffAt: prefixCutoffAt,
		CutoffAt:       cutoffAt,
		Episodes:       snapshots,
		PrefixEvidence: prefix,
		SuffixEvidence: suffix,
	})
}
