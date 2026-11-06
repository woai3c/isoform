package main

// The brief store already persists both generated documents and the last-good
// local Today projection, atomically and with provenance beside the document.
// Do not introduce a second content cache or treat yesterday as today's brief.
func (s *mobileBriefService) storedLatestForLaunch() (MobileBriefV1, bool) {
	brief, ok, _ := s.finishedLatest()
	return brief, ok
}

// finishedLatest is the latest brief as every surface reads it (latestShown).
// Before any brief is stored, today's snapshot stands in. An error is a saved
// read that cannot be read.
func (s *mobileBriefService) finishedLatest() (MobileBriefV1, bool, error) {
	brief, ok, err := s.latestShown()
	if err != nil || ok {
		return brief, ok, err
	}
	snapshot, ok := s.store.currentToday()
	if !ok || snapshot.LocalDate != s.readDay() {
		return MobileBriefV1{}, false, nil
	}
	read, err := s.finishedRead(snapshot)
	return read, err == nil, err
}
