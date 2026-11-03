package main

import (
	"context"
	"time"
)

// morningBriefPurposeSourceDigest binds the ledger record and the observed
// evidence used to derive last touch. The hash is local-only and conveys no
// authority to act or to retain withdrawn text.
func morningBriefPurposeSourceDigest(detail PurposeOpenLoopDetail, observation mobileBriefPurposeObservation) string {
	return mobileBriefModelDigest(struct {
		Detail      PurposeOpenLoopDetail
		Observation mobileBriefPurposeObservation
	}{detail, observation})
}

// morningBriefPurposeSourceReadable recomputes the same source at the brief's
// cutoff. sources must be the request's freshly privacy-filtered Today source
// snapshot. The caller separately binds related Purposes and all other inputs.
func (a *App) morningBriefPurposeSourceReadable(ctx context.Context, sources todayChatSources, id string, cutoff time.Time, expected string) bool {
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() && !pausedBriefReadAllowed(ctx) || ctx.Err() != nil || expected == "" || cutoff.IsZero() {
		return false
	}
	var detail PurposeOpenLoopDetail
	found := false
	for _, candidate := range sources.Purposes {
		if candidate.OpenLoop.ID == id {
			detail, found = candidate, true
			break
		}
	}
	if !found || !mobileBriefPurposeCurrent(detail) || detail.Current.OccurredAt > cutoff.UnixMilli() {
		return false
	}
	subjects, err := mobileBriefMailSubjects(a)
	if err != nil || mobileBriefValueEchoesMail(mobileGoalForeignText(detail), subjects) {
		return false
	}
	pauses, err := a.observationPausesContext(ctx)
	if err != nil {
		return false
	}
	reader := mobileBriefPurposeReader{app: a, store: a.activity.store, asOf: cutoff, pauses: pauses, pausesReadable: true, cache: map[string]mobileBriefPurposeObservation{}}
	observation := reader.purpose(detail)
	if observation.Sensitive || observation.Unavailable || observation.MissingEvidence || observation.RetentionExpired || ctx.Err() != nil {
		return false
	}
	return morningBriefPurposeSourceDigest(detail, observation) == expected
}
