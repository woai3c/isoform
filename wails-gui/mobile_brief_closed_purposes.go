package main

// A goal the user marks done or forgets leaves every read of a brief at once:
// Today, the phone's Feed and the spoken brief. Today's live projection
// already rebuilds its Purpose rows from the ledger (mobile_brief_today_refresh.go),
// but a stored brief read by the phone, its written text, offers and cards
// still name the goal. This is a view of the brief, never saved: Undo
// (userPurposeRestore) reopens the goal and the next read shows it again,
// with the text as written. Done and forgotten goals are not private, so
// nothing here counts as a sensitive item or withholds the text as a whole;
// only what cites the goal leaves.

// mobileBriefWithoutClosedPurposes leaves out the cards, paragraphs, offers
// and evidence of Purposes that are no longer open. A ledger that cannot be
// read changes nothing: the privacy pass before it has already run.
func (a *App) mobileBriefWithoutClosedPurposes(brief MobileBriefV1) MobileBriefV1 {
	store, err := a.purposeProductStore()
	if err != nil {
		return brief
	}
	details, err := store.ListDetailsAll()
	if err != nil {
		return brief
	}
	closed := map[string]bool{}
	for _, detail := range details {
		if !purposeStateOpen(detail.Current.LifecycleState) {
			closed[detail.OpenLoop.ID] = true
		}
	}
	return mobileBriefDropClosedPurposes(brief, closed)
}

func mobileBriefDropClosedPurposes(brief MobileBriefV1, closed map[string]bool) MobileBriefV1 {
	if len(closed) == 0 {
		return brief
	}
	closedRefs := map[string]bool{}
	for _, ref := range brief.Evidence {
		if ref.SourceKind == "purpose" && closed[ref.SourceID] {
			closedRefs[ref.Ref] = true
		}
	}
	cites := func(refs []string) bool {
		for _, ref := range refs {
			if closedRefs[ref] {
				return true
			}
		}
		return false
	}
	dropped := false
	var counts map[string]int
	if brief.Counts.OpenLoopsByKind != nil {
		counts = make(map[string]int, len(brief.Counts.OpenLoopsByKind))
		for kind, count := range brief.Counts.OpenLoopsByKind {
			counts[kind] = count
		}
		brief.Counts.OpenLoopsByKind = counts
	}
	keepItems := func(items []MobileBriefItem, openLoops bool) []MobileBriefItem {
		if items == nil {
			return nil
		}
		out := make([]MobileBriefItem, 0, len(items))
		for _, item := range items {
			if closed[item.PurposeID] || item.PurposeID == "" && len(item.Refs) > 0 && closedRefs[item.Refs[0]] {
				dropped = true
				if kind := mobileBriefFirstText(item.Kind, "other"); openLoops && counts[kind] > 0 {
					if counts[kind]--; counts[kind] == 0 {
						delete(counts, kind)
					}
				}
				continue
			}
			out = append(out, item)
		}
		return out
	}
	keep := func(items []MobileBriefItem) []MobileBriefItem { return keepItems(items, false) }
	sections := brief.Sections
	brief.Sections = MobileBriefSections{
		Noticed:    mobileBriefOptionalItems(keep(sections.Noticed)),
		Highlights: mobileBriefOptionalItems(keep(sections.Highlights)), DayAhead: mobileBriefOptionalItems(keep(sections.DayAhead)),
		Review: mobileBriefOptionalItems(keep(sections.Review)), Decide: mobileBriefOptionalItems(keep(sections.Decide)),
		Plan: mobileBriefOptionalItems(keep(sections.Plan)), Moved: keep(sections.Moved), Doing: keep(sections.Doing),
		Watching: keep(sections.Watching), Meetings: keep(sections.Meetings), People: keep(sections.People),
		OpenLoops: keepItems(sections.OpenLoops, true), Learned: keep(sections.Learned), Deliverables: keep(sections.Deliverables),
		Schedules: keep(sections.Schedules), WorkingToward: mobileBriefOptionalItems(keep(sections.WorkingToward)),
		Commitments: mobileBriefOptionalItems(keep(sections.Commitments)), WaitingOnYou: mobileBriefOptionalItems(keep(sections.WaitingOnYou)),
	}
	brief.Upcoming = keep(brief.Upcoming)
	if len(closedRefs) == 0 && !dropped {
		return brief
	}
	if brief.Narrative != nil {
		narrative := []MobileBriefPara{}
		for _, paragraph := range brief.Narrative {
			if cites(paragraph.Refs) {
				continue
			}
			narrative = append(narrative, paragraph)
		}
		if len(narrative) < len(brief.Narrative) {
			// The recording speaks every paragraph as written.
			brief.Audio = nil
			brief.Narrative = narrative
			if len(narrative) == 0 {
				brief.NarrativeStatus = "unavailable"
			}
		}
	}
	if brief.Offers != nil {
		offers := []MobileBriefOffer{}
		for _, offer := range brief.Offers {
			if !cites(offer.Refs) {
				offers = append(offers, offer)
			}
		}
		brief.Offers = offers
	}
	if brief.Cards != nil {
		cards := []MobileBriefCard{}
		for _, card := range brief.Cards {
			if !cites(card.Refs) {
				cards = append(cards, card)
			}
		}
		brief.Cards = cards
	}
	// A closed goal's label leaves with its last citation.
	used := map[string]bool{}
	for _, section := range mobileBriefSectionLines(brief.Sections) {
		for _, item := range section.items {
			for _, ref := range item.Refs {
				used[ref] = true
			}
		}
	}
	for _, item := range brief.Upcoming {
		for _, ref := range item.Refs {
			used[ref] = true
		}
	}
	evidence := make([]MobileBriefRef, 0, len(brief.Evidence))
	for _, ref := range brief.Evidence {
		if closedRefs[ref.Ref] && !used[ref.Ref] {
			continue
		}
		evidence = append(evidence, ref)
	}
	brief.Evidence = evidence
	// The key names the text, offers and evidence as read now. A stale one
	// fails every check of it: "Talk about this brief" said nothing once a
	// goal the brief cites was closed (2026-10-21).
	brief.ConversationKey = mobileBriefConversationKey(brief)
	return brief
}
