package main

// The current brief (PROTOCOL.md section 8.1) is chosen by one rule and kept
// in memory. It is the newest stored brief whose written text is there; with
// none, the newest stored brief stands in. A quiet slot is never current, and
// a template-only brief never replaces a newer written one. Today, the menu
// bar, Chat's brief cards, the launch read and every phone route all read this
// choice, so they cannot disagree. The choice is recomputed only when a brief
// is stored or removed (store.save and the retention sweep clear it).

// mobileBriefCurrenter is the optional part of a brief source that answers
// the current brief from the in-memory choice.
type mobileBriefCurrenter interface {
	CurrentMobileBrief() (MobileBriefV1, bool)
}

// mobileBriefHasWrittenText reports a brief whose words were written.
func mobileBriefHasWrittenText(brief MobileBriefV1) bool {
	return brief.NarrativeStatus == "ok" && len(brief.Narrative) > 0
}

// mobileBriefChooseCurrent applies the rule to briefs listed newest first.
func mobileBriefChooseCurrent(briefs []MobileBriefV1) (MobileBriefV1, bool) {
	var newest MobileBriefV1
	found := false
	for _, brief := range briefs {
		if brief.NarrativeStatus == "quiet" {
			continue
		}
		if mobileBriefHasWrittenText(brief) {
			return brief, true
		}
		if !found {
			newest, found = brief, true
		}
	}
	return newest, found
}

// current returns the brief the rule chooses from the stored briefs. The
// choice is computed on the first read after a store or removal, then kept.
func (s *mobileBriefStore) current() (MobileBriefV1, bool) {
	if s.dir == "" {
		return MobileBriefV1{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.currentFresh {
		s.currentBrief, s.currentOK = mobileBriefChooseCurrent(s.briefs())
		s.currentFresh = true
	}
	return s.currentBrief, s.currentOK
}

// CurrentMobileBrief is the stored current brief as a delivered read applies
// the gates to it. Every phone route names this brief; the Mac's display reads
// it through LatestMobileBrief. When the gates now withhold the chosen brief's
// words, the newest brief whose words may still be read is current instead,
// so a withdrawal never leaves a template standing in for a written brief.
func (s *mobileBriefService) CurrentMobileBrief() (MobileBriefV1, bool) {
	stored, ok := s.store.current()
	if !ok {
		return MobileBriefV1{}, false
	}
	deliver := s.deliveredWithAudio
	chosen := deliver(stored)
	if !mobileBriefHasWrittenText(stored) || mobileBriefHasWrittenText(chosen) {
		return chosen, true
	}
	for _, brief := range s.store.briefs() {
		if brief.BriefID == stored.BriefID || !mobileBriefHasWrittenText(brief) {
			continue
		}
		if delivered := deliver(brief); mobileBriefHasWrittenText(delivered) {
			return delivered, true
		}
	}
	return chosen, true
}
