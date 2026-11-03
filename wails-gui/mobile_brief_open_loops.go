package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

const mobileBriefOpenLoopCap = 4
const mobileBriefWaitingReplySource = "brief_waiting_reply"

func mobileBriefLoopIdentity(kind, id string) string {
	if kind == mobileBriefRecapSource && strings.HasPrefix(id, threadOutcomeMomentPrefix) {
		if cut := strings.LastIndex(id, ":"); cut > 0 {
			id = id[:cut]
		}
	}
	return mobileBriefSourceKey(kind, id)
}

func mobileBriefCandidateLoopKey(item mobileBriefCandidate) string {
	if item.LoopKey != "" {
		return item.LoopKey
	}
	return mobileBriefLoopIdentity(item.SourceKind, item.SourceID)
}

// Due dates lead; older untouched work comes next. An explicit counterparty
// breaks equal-age ties. No sighting creates an obligation or a person score.
func mobileBriefOpenLoopFirst(a, b mobileBriefCandidate) bool {
	if a.DueAt.IsZero() != b.DueAt.IsZero() {
		return !a.DueAt.IsZero()
	}
	if !a.DueAt.Equal(b.DueAt) {
		return a.DueAt.Before(b.DueAt)
	}
	at, bt := a.LastTouch, b.LastTouch
	if at.IsZero() {
		at = a.At
		if at.IsZero() {
			at = a.OpenedAt
		}
	}
	if bt.IsZero() {
		bt = b.At
		if bt.IsZero() {
			bt = b.OpenedAt
		}
	}
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	if (a.Person != "") != (b.Person != "") {
		return a.Person != ""
	}
	if a.Person != b.Person {
		return a.Person < b.Person
	}
	return mobileBriefSourceKey(a.SourceKind, a.SourceID) < mobileBriefSourceKey(b.SourceKind, b.SourceID)
}

// Work can survive any number of days. The last two briefs supply identities
// only, never cached text or evidence. Apply the cap across aliases so a rich
// Purpose card or today's Moved row cannot bypass suppression.
func selectMobileBriefOpenLoops(in mobileBriefInput) mobileBriefInput {
	recent := map[string]bool{}
	count := 0
	history := append([]MobileBriefV1{}, in.PreviousBriefs...)
	sort.SliceStable(history, func(i, j int) bool { return mobileBriefAsOfTime(history[i]).After(mobileBriefAsOfTime(history[j])) })
	for _, brief := range history {
		if !mobileBriefAsOfTime(brief).Before(in.AsOf) {
			continue
		}
		raised := map[string]bool{}
		for _, p := range brief.Narrative {
			for _, ref := range p.Refs {
				raised[ref] = true
			}
		}
		for _, section := range mobileBriefSectionLines(brief.Sections) {
			for _, item := range section.items {
				for _, ref := range brief.Evidence {
					if len(item.Refs) > 0 && item.Refs[0] == ref.Ref && (raised[ref.Ref] || len(brief.Narrative) == 0 && section.name == mobileBriefSectionOpenLoops) {
						recent[mobileBriefLoopIdentity(ref.SourceKind, ref.SourceID)] = true
					}
				}
			}
		}
		count++
		if count == 2 {
			break
		}
	}
	// Carry family identity onto today's Moved alias before suppression.
	identities := map[string]mobileBriefCandidate{}
	for _, item := range in.OpenLoops {
		if item.LoopKey != "" {
			identities[mobileBriefSourceKey(item.SourceKind, item.SourceID)] = item
		}
	}
	for _, items := range [][]mobileBriefCandidate{in.Moved, in.Doing} {
		for i, item := range items {
			if canonical, ok := identities[mobileBriefSourceKey(item.SourceKind, item.SourceID)]; ok {
				items[i].LoopKey = canonical.LoopKey
				items[i].LoopAliases = canonical.LoopAliases
			}
		}
	}
	// Keep old-phone source records intact. Rich cards enrich ranking metadata
	// only; their bodies, report markers and evidence must never replace aliases.
	byID := map[string]mobileBriefCandidate{}
	for _, item := range prepareMobileBriefCandidates(in.OpenLoops, mobileBriefOpenLoopFirst) {
		if !item.At.After(in.AsOf) {
			byID[mobileBriefCandidateLoopKey(item)] = item
		}
	}
	for _, items := range [][]mobileBriefCandidate{in.WorkingToward, in.Commitments, in.WaitingOnYou} {
		for _, item := range prepareMobileBriefPurposeCandidates(items, mobileBriefPurposeFirst) {
			key := mobileBriefCandidateLoopKey(item)
			if prior, ok := byID[key]; ok {
				if prior.DueAt.IsZero() {
					prior.DueAt = item.DueAt
				}
				if prior.LastTouch.IsZero() {
					prior.LastTouch = item.LastTouch
				}
				if prior.OpenedAt.IsZero() {
					prior.OpenedAt = item.OpenedAt
				}
				if prior.Person == "" {
					prior.Person = item.Person
				}
				byID[key] = prior
			}
		}
	}
	in.OpenLoopCounts = map[string]int{}
	for _, item := range byID {
		kind := item.Kind
		if kind == "" {
			kind = "other"
		}
		in.OpenLoopCounts[kind]++
	}
	var candidates []mobileBriefCandidate
	for key, item := range byID {
		dueToday := !item.DueAt.IsZero() && item.DueAt.In(mobileBriefInputLocation(in)).Format("2006-01-02") == in.AsOf.In(mobileBriefInputLocation(in)).Format("2006-01-02")
		raised := recent[key]
		for _, alias := range item.LoopAliases {
			raised = raised || recent[alias]
		}
		if raised && !dueToday {
			continue
		}
		candidates = append(candidates, item)
	}
	candidates = prepareMobileBriefCandidates(candidates, mobileBriefOpenLoopFirst)
	if len(candidates) > mobileBriefOpenLoopCap {
		in.OpenLoopsTruncated = true
		candidates = candidates[:mobileBriefOpenLoopCap]
	}
	selected := map[string]bool{}
	for _, item := range candidates {
		selected[mobileBriefCandidateLoopKey(item)] = true
	}
	filter := func(items []mobileBriefCandidate) []mobileBriefCandidate {
		var out []mobileBriefCandidate
		for _, item := range items {
			key := mobileBriefCandidateLoopKey(item)
			if _, loop := byID[key]; !loop || selected[key] {
				out = append(out, item)
			}
		}
		return out
	}
	in.OpenLoops = candidates
	// Goal progress cards retain their independent five-row context budget.
	// Suppress a repeated mention across every richer alias, without rewriting
	// old-phone rows with execution metadata or counterparties from those cards.
	var progress []mobileBriefCandidate
	for _, item := range in.WorkingToward {
		key := mobileBriefCandidateLoopKey(item)
		dueToday := !item.DueAt.IsZero() && item.DueAt.In(mobileBriefInputLocation(in)).Format("2006-01-02") == in.AsOf.In(mobileBriefInputLocation(in)).Format("2006-01-02")
		if !recent[key] || dueToday {
			progress = append(progress, item)
		}
	}
	in.WorkingToward = progress
	in.Commitments = filter(in.Commitments)
	var people []mobileBriefCandidate
	for _, item := range filter(in.WaitingOnYou) {
		dueToday := !item.DueAt.IsZero() && item.DueAt.In(mobileBriefInputLocation(in)).Format("2006-01-02") == in.AsOf.In(mobileBriefInputLocation(in)).Format("2006-01-02")
		if !recent[mobileBriefCandidateLoopKey(item)] || dueToday {
			people = append(people, item)
		}
	}
	in.WaitingOnYou = people
	in.Moved = filter(in.Moved)
	in.Doing = filter(in.Doing)
	return in
}

func mobileBriefInputLocation(in mobileBriefInput) *time.Location {
	if in.Location != nil {
		return in.Location
	}
	return time.Local
}

// Query persisted derived outcomes from any day, then recheck the exact source
// closure through #50's reader. No additional OCR or mail body enters the brief.
func collectMobileBriefOlderThreads(ctx context.Context, a *App, in *mobileBriefInput) {
	collectMobileBriefThreadHeads(ctx, a, in)
}

func (a *App) mobileBriefWaitingReply(ctx context.Context, id string, now time.Time) (mobileBriefCandidate, bool) {
	if a == nil || a.store == nil || a.IsClosedEyesModeEnabled() && !pausedBriefReadAllowed(ctx) {
		return mobileBriefCandidate{}, false
	}
	row, err := a.store.GetEngagementMoment(ctx, id)
	if err != nil || row == nil || row.Kind != string(engagement.KindWaitingOnReply) || row.CreatedAt > now.UnixMilli() {
		return mobileBriefCandidate{}, false
	}
	switch row.State {
	case storage.EngagementStatePending, storage.EngagementStateScheduled, storage.EngagementStateSent:
	default:
		return mobileBriefCandidate{}, false
	}
	if row.ExpiresAt > 0 && row.ExpiresAt <= now.UnixMilli() {
		return mobileBriefCandidate{}, false
	}
	m, allowed := sanitizeEngagementMoment(momentFromRow(*row))
	if !allowed {
		return mobileBriefCandidate{}, false
	}
	var source proactiveVoiceSource
	if a.loadProactiveVoiceMail(ctx, m, now, "en", &source) != "" || len(source.People) == 0 || source.People[0].Name == nil {
		return mobileBriefCandidate{}, false
	}
	name := *source.People[0].Name
	candidate := mobileBriefCandidate{SourceKind: mobileBriefWaitingReplySource, SourceID: id, Title: name, Person: name, Status: "waiting", Kind: "waiting_on_reply", At: m.CreatedAt, LastTouch: m.CreatedAt, OpenedAt: m.CreatedAt, DueAt: m.DueAt}
	candidate.rankedSourceDigest = mobileBriefModelDigest(candidate)
	return candidate, true
}

// morningBriefWaitingSourceReadable repeats the mail and consent checks and
// rejects changes to any projected fact consumed by scoring.
func (a *App) morningBriefWaitingSourceReadable(ctx context.Context, id, digest string, now time.Time) bool {
	candidate, ok := a.mobileBriefWaitingReply(ctx, id, now)
	return ok && digest != "" && candidate.rankedSourceDigest == digest
}

func collectMobileBriefWaitingReplies(ctx context.Context, a *App, in *mobileBriefInput) {
	if a == nil || a.store == nil {
		return
	}
	rows, err := a.store.BriefWaitingReplyMoments(ctx, in.AsOf.UnixMilli())
	if err != nil {
		return
	}
	for _, row := range rows {
		if item, ok := a.mobileBriefWaitingReply(ctx, row.ID, in.AsOf); ok {
			in.OpenLoops = append(in.OpenLoops, item)
		}
	}
}

// Reuse chat's source holdbacks for one-time work. No handoff body, execution
// claim or messenger payload is a source for this brief.
func (a *App) mobileBriefHandoffReadable(ctx context.Context, item handoff.Entry) bool {
	if a == nil || a.IsClosedEyesModeEnabled() && !pausedBriefReadAllowed(ctx) || !a.todayChatDerivedAllowedContext(ctx, []string{item.AppName}, nil, string(item.Source)) {
		return false
	}
	switch item.Source {
	case handoff.SourceSlack, handoff.SourceDiscord, handoff.SourceTelegram:
		return false
	case handoff.SourceTriage, handoff.SourceAgency, handoff.SourceFallback:
		if item.SourceID == "" || a.activity == nil || a.activity.store == nil {
			return false
		}
		pauses, err := a.observationPausesContext(ctx)
		if err != nil {
			return false
		}
		reader := mobileBriefPurposeReader{app: a, store: a.activity.store, asOf: time.Now(), pauses: pauses, pausesReadable: true, cache: map[string]mobileBriefPurposeObservation{}}
		for _, kind := range []string{"activity", "activity_session"} {
			observation := reader.source(kind, item.SourceID, 0, false)
			if !observation.Sensitive && !observation.Unavailable && !observation.MissingEvidence {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func (a *App) mobileBriefHandoffSourceReadable(ctx context.Context, id string) bool {
	if a == nil || a.handoffStore == nil {
		return false
	}
	entries, err := a.handoffEntries(ctx)
	if err != nil {
		return false
	}
	for _, item := range entries {
		if item.ID == id {
			return a.mobileBriefHandoffReadable(ctx, item)
		}
	}
	return false
}

// morningBriefHandoffSourceReadable rechecks the original work record as well
// as the current privacy policy before its projected facts can be reused.
func (a *App) morningBriefHandoffSourceReadable(ctx context.Context, id, digest string) bool {
	if a == nil || a.handoffStore == nil || digest == "" {
		return false
	}
	entries, err := a.handoffEntries(ctx)
	if err != nil {
		return false
	}
	for _, item := range entries {
		if item.ID == id {
			return a.mobileBriefHandoffReadable(ctx, item) && mobileBriefModelDigest(item) == digest
		}
	}
	return false
}

// morningBriefHandoffOriginDigest identifies the source and its original
// policy context, not the mutable work title or status. It is local-only.
func morningBriefHandoffOriginDigest(item handoff.Entry) string {
	return mobileBriefModelDigest(struct {
		ID, Source, SourceID, SourceLink, AppName string
		CreatedAt                                 int64
	}{item.ID, string(item.Source), item.SourceID, item.SourceLink, item.AppName, item.CreatedAt})
}

// morningBriefHandoffOriginReadable is a policy check for a historical
// comparison. A true result does not authorize sending old text to a model.
func (a *App) morningBriefHandoffOriginReadable(ctx context.Context, id, digest string) bool {
	if a == nil || a.handoffStore == nil || id == "" || digest == "" {
		return false
	}
	entries, err := a.handoffEntries(ctx)
	if err != nil {
		return false
	}
	for _, item := range entries {
		if item.ID == id {
			return a.mobileBriefHandoffReadable(ctx, item) && morningBriefHandoffOriginDigest(item) == digest
		}
	}
	return false
}
