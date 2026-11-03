package main

import "time"

// The brief store already persists both generated documents and the last-good
// local Today projection, atomically and with provenance beside the document.
// Do not introduce a second content cache or treat yesterday as today's brief.
func (s *mobileBriefService) storedLatestForLaunch() (MobileBriefV1, bool) {
	// The same current brief the Mac's display and the phone routes read; the
	// day's snapshot stands in only before any brief is stored.
	brief, ok := s.store.current()
	loc := s.location
	if loc == nil {
		loc = time.Local
	}
	day := s.now().In(loc).Format("2006-01-02")
	if !ok {
		brief, ok = s.store.currentToday()
		ok = ok && brief.LocalDate == day
	}
	if !ok {
		return MobileBriefV1{}, false
	}
	// Stored text is evidence, never authorization. Run the very same live
	// source/Closed Eyes/mail/memory/audio gates as the normal delivery path.
	return s.app.mobileBriefWithCards(s.app.mobileBriefForDelivery(brief)), true
}
