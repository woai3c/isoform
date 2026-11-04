package main

import "time"

// The brief store already persists both generated documents and the last-good
// local Today projection, atomically and with provenance beside the document.
// Do not introduce a second content cache or treat yesterday as today's brief.
func (s *mobileBriefService) storedLatestForLaunch() (MobileBriefV1, bool) {
	brief, ok, _ := s.finishedLatest()
	return brief, ok
}

// finishedLatest is the current brief as Today opens it: its finished read
// (mobileBriefFinishedRead). An error is a saved read that cannot be read.
func (s *mobileBriefService) finishedLatest() (MobileBriefV1, bool, error) {
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
		return MobileBriefV1{}, false, nil
	}
	// Stored text is evidence, never authorization: the finished read was
	// made by the normal delivery gates and is made again when what they
	// read changes.
	read, err := s.finishedRead(brief)
	return read, err == nil, err
}
