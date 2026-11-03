package main

import (
	"strings"
	"time"

	"wails-gui/internal/openanswer"
	"wails-gui/internal/pii"
)

// openAnswerStream removes structured material before either renderer or the
// backend's segment writer sees it. Promise admission is intentionally not
// enabled until there is a checkable source and a durable check.
type openAnswerStream struct {
	filter                  openanswer.BlockFilter
	changeFilter            openanswer.BlockFilter
	changeFilterInitialized bool
	last                    AgentEvent
	question                string
	candidate               *openanswer.Candidate
	change                  *openAnswerChangeBlock
	blocks                  int
	changeBlocks            int
	changeStandalone        bool
	completed               bool
	invalid                 bool
}

func (s *openAnswerStream) emit(evt AgentEvent, send func(AgentEvent)) {
	s.initChangeFilter()
	switch evt.Type {
	case "assistant":
		if s.completed {
			s.invalid = true
			s.candidate = nil
			s.change = nil
		}
		s.last = evt
		visible, blocks := s.filter.Feed(evt.Content)
		s.blocks = s.filter.BlocksStarted()
		for _, raw := range blocks {
			if s.blocks != 1 || strings.TrimSpace(s.question) == "" {
				s.candidate = nil
				continue
			}
			candidate, err := openanswer.ParseBlock(raw)
			if err == nil && candidate.Question == strings.TrimSpace(s.question) &&
				openanswer.CheckCandidate(candidate, time.Now()) == openanswer.Eligible && openAnswerCandidateTextAllowed(candidate) {
				s.candidate = &candidate
			}
		}
		if s.blocks > 1 {
			s.candidate = nil
		}
		visible, changeBlocks := s.changeFilter.Feed(visible)
		s.changeBlocks = s.changeFilter.BlocksStarted()
		for _, raw := range changeBlocks {
			if s.changeBlocks != 1 {
				s.change = nil
				continue
			}
			change, err := parseOpenAnswerChangeBlock(raw)
			if err == nil {
				s.change = &change
			} else {
				s.change = nil
			}
		}
		if s.changeBlocks > 1 {
			s.change = nil
		}
		if visible != "" {
			evt.Content = visible
			send(evt)
		}
	case "tool_use_start", "tool_use", "tool_result", "run_created", "permission_request", "plan_complete":
		if s.filter.Interrupted() || s.changeFilter.Interrupted() {
			// Preserve the filter until the closing delimiter or end of run.
			// A block crossing rows is never eligible for admission.
			s.invalid = true
		} else {
			s.finish(send)
		}
		s.candidate = nil
		s.change = nil
		s.blocks = 0
		s.changeBlocks = 0
		s.changeStandalone = false
		s.completed = false
		send(evt)
	case "done":
		s.finish(send)
		s.completed = true
		send(evt)
	case "error", "close":
		s.finish(send)
		if evt.Type == "error" {
			s.candidate = nil
			s.change = nil
			s.completed = false
		}
		send(evt)
	default:
		send(evt)
	}
}

func (s *openAnswerStream) initChangeFilter() {
	if !s.changeFilterInitialized {
		s.changeFilter = openanswer.NewBlockFilter("open_answer_change")
		s.changeFilterInitialized = true
	}
}

// This is a preliminary text filter, not proof that the source is readable or
// that a check exists. The eventual admission and delivery paths must check
// current privacy and source provenance again before persisting or posting.
func openAnswerCandidateTextAllowed(candidate openanswer.Candidate) bool {
	if _, sensitive := pii.IsSensitiveApp(candidate.Source); sensitive || pii.IsSensitiveAppName(candidate.Source) {
		return false
	}
	for _, value := range []string{candidate.Question, candidate.Topic, candidate.Source, candidate.WatchedEvent} {
		if pii.HasRedaction(value) || pii.RedactCodes(value) != value || proactiveVoiceRestrictedText(value) {
			return false
		}
	}
	return true
}

// completedCandidate is untrusted model output. Recheck preliminary privacy
// and timing because either may change while the answer is streaming. The
// question match does not establish source access or an actionable check.
func (s *openAnswerStream) completedCandidate() *openanswer.Candidate {
	return s.completedCandidateAt(time.Now())
}

func (s *openAnswerStream) completedCandidateAt(now time.Time) *openanswer.Candidate {
	if !s.completed || s.invalid || s.blocks != 1 || s.candidate == nil ||
		openanswer.CheckCandidate(*s.candidate, now) != openanswer.Eligible ||
		!openAnswerCandidateTextAllowed(*s.candidate) {
		return nil
	}
	candidate := *s.candidate
	return &candidate
}

func (s *openAnswerStream) completedChange() *openAnswerChangeBlock {
	if !s.completed || s.invalid || s.blocks != 0 || s.changeBlocks != 1 || !s.changeStandalone || s.change == nil {
		return nil
	}
	change := *s.change
	return &change
}

func (s *openAnswerStream) finish(send func(AgentEvent)) {
	if !s.completed && !s.filter.CandidateComplete() {
		s.candidate = nil
	}
	tail := s.filter.Finish()
	visible, changeBlocks := s.changeFilter.Feed(tail)
	if started := s.changeFilter.BlocksStarted(); started > s.changeBlocks {
		s.changeBlocks = started
	}
	for _, raw := range changeBlocks {
		if s.changeBlocks != 1 {
			s.change = nil
			continue
		}
		change, err := parseOpenAnswerChangeBlock(raw)
		if err == nil {
			s.change = &change
		} else {
			s.change = nil
		}
	}
	if s.changeBlocks > 1 {
		s.change = nil
	}
	if s.changeFilter.BlocksStarted() > 0 || s.changeFilter.Interrupted() {
		s.changeStandalone = s.changeFilter.StandaloneBlockComplete()
	}
	visible += s.changeFilter.Finish()
	if visible != "" {
		evt := s.last
		evt.Content = visible
		send(evt)
	}
	s.last = AgentEvent{}
}
