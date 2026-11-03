package main

import "wails-gui/internal/openanswer"

type completedOpenAnswerAdmission struct {
	tag                string
	userID             string
	runID              string
	assistantSegmentID string
	candidate          openanswer.Candidate
}

func resolveCompletedOpenAnswerAdmission(userID, runID string, segment *finishedSegment, candidate *openanswer.Candidate) (completedOpenAnswerAdmission, bool) {
	if userID == "" || runID == "" || segment == nil || segment.tag == "" || segment.id == "" || candidate == nil {
		return completedOpenAnswerAdmission{}, false
	}
	return completedOpenAnswerAdmission{
		tag: segment.tag, userID: userID, runID: runID, assistantSegmentID: segment.id, candidate: *candidate,
	}, true
}
