package handoff

import "errors"

// What the record proves about a step whose effect left the run (v1.0.2 §1).
const (
	ProofDone    = "done"     // proof it happened: skipped on Start again
	ProofNotDone = "not_done" // proof it didn't: done again with no question
	ProofUnknown = "unknown"  // no proof either way: the person answers
)

// The person's answer to an unknown step.
const (
	StepSkip = "skip"
	StepRedo = "redo"
)

// Step kinds the runner records.
const (
	StepWriteFile  = "write_file"  // a file written outside the task's folder
	StepRunCommand = "run_command" // a command that changes something elsewhere
)

// EffectStep is one step of a stopped run whose effect left the run. The store
// keeps the step's own record (its plain name, target and where to check); the
// app owns the wording.
type EffectStep struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Name is the step's plain name when the record has one.
	Name string `json:"name,omitempty"`
	// Target is the file or command the step acted on.
	Target string `json:"target,omitempty"`
	At     int64  `json:"at"` // unix ms the step ran
	// App and Link say where to check: an app and, when known, a link.
	App    string `json:"app,omitempty"`
	Link   string `json:"link,omitempty"`
	Proof  string `json:"proof"`
	Answer string `json:"answer,omitempty"`
}

// ErrStepUnavailable is returned for an unknown step or answer.
var ErrStepUnavailable = errors.New("handoff: that step is no longer open")

// StepsNeedingAnswer counts unknown steps without the person's answer.
func (t *Thread) StepsNeedingAnswer() int {
	if t == nil {
		return 0
	}
	n := 0
	for _, step := range t.Steps {
		if step.Proof == ProofUnknown && step.Answer == "" {
			n++
		}
	}
	return n
}

// StepsNeedingCheck counts unknown steps, answered or not: the row says Review
// while any of them is on the stopped task.
func (t *Thread) StepsNeedingCheck() int {
	if t == nil {
		return 0
	}
	n := 0
	for _, step := range t.Steps {
		if step.Proof == ProofUnknown {
			n++
		}
	}
	return n
}

// AnswerStep records Skip it or Do it again on an unknown step; an empty
// answer clears it.
func (t *Thread) AnswerStep(id, answer string) error {
	switch answer {
	case "", StepSkip, StepRedo:
	default:
		return ErrStepUnavailable
	}
	for i := range t.Steps {
		if t.Steps[i].ID == id && t.Steps[i].Proof == ProofUnknown {
			t.Steps[i].Answer = answer
			return nil
		}
	}
	return ErrStepUnavailable
}

// RestartSteps splits the stopped run's steps for Start again: proven-done and
// skipped steps never run again; proven-not-done and redo steps run again.
func (t *Thread) RestartSteps() (skip, redo []EffectStep) {
	for _, step := range t.Steps {
		switch {
		case step.Proof == ProofDone || step.Proof == ProofUnknown && step.Answer == StepSkip:
			skip = append(skip, step)
		case step.Proof == ProofNotDone || step.Proof == ProofUnknown && step.Answer == StepRedo:
			redo = append(redo, step)
		}
	}
	return skip, redo
}
