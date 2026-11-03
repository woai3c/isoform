package engagement

// NotificationTag is a localization token, never a sentence.
type NotificationTag string

const (
	NotificationTagNone     NotificationTag = ""
	NotificationTagNoisy    NotificationTag = "noisy"
	NotificationTagIgnored  NotificationTag = "ignored"
	NotificationTagNeedsYou NotificationTag = "needs_you"
	NotificationTagRare     NotificationTag = "rare"
	NotificationTagQuiet    NotificationTag = "quiet"
)

// NotificationNeedsYou is the Settings attention policy. Keep it separate from
// NeedsYou, which controls the text fallback for blocked work in the router.
func NotificationNeedsYou(kind Kind) bool {
	switch kind {
	case KindDecisionDue, KindCommitmentSlipping, KindWaitingOnReply, KindWorkBlocked:
		return true
	default:
		return false
	}
}

// NotificationStatsTag applies the ranked-page thresholds in priority order.
// All counts are from the interruption receipt cohort, excluding quiet-only
// lines. OpenedOrActed is the distinct union of opened/completed moments, so
// one notice never contributes twice to the attention rate.
func NotificationStatsTag(kind Kind, sent, openedOrActed, acted int, quiet bool) NotificationTag {
	if quiet {
		return NotificationTagQuiet
	}
	if sent == 0 {
		return NotificationTagNone
	}
	// Permission requests must never be labelled as noise to quiet away.
	if kind == KindDecisionDue && acted*2 < sent {
		return NotificationTagNeedsYou
	}
	if sent >= 20 && openedOrActed == 0 && acted == 0 {
		return NotificationTagNoisy
	}
	if sent >= 5 && openedOrActed*10 < sent {
		return NotificationTagIgnored
	}
	if NotificationNeedsYou(kind) && acted*2 < sent {
		return NotificationTagNeedsYou
	}
	if sent <= 4 {
		return NotificationTagRare
	}
	return NotificationTagNone
}

// NotificationSampleActivityThread is the generic push-copy variant used by
// decision_due notices about an unfinished Activity thread, not permissions.
const NotificationSampleActivityThread = "decision_due.activity_thread"

// NotificationSampleCopyKey preserves only the code-owned copy variant.
func NotificationSampleCopyKey(kind Kind, activityThread bool) string {
	if kind == KindDecisionDue && activityThread {
		return NotificationSampleActivityThread
	}
	return string(kind)
}

// NotificationSampleCopy resolves a sample key in either catalog language
// without any personal notice parameters. It uses the same copy as delivery.
func NotificationSampleCopy(key, lang string) (CopySet, bool) {
	if key == NotificationSampleActivityThread {
		return Copy(KindDecisionDue, lang, map[string]string{"_activity_thread": "sample"})
	}
	return Copy(Kind(key), lang)
}
