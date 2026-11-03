package main

import (
	"errors"
	"path/filepath"
	"strings"

	"wails-gui/internal/handoff"
)

// errDesktopTaskGone is a link to a task that was deleted: the board says
// "That task isn't in Tasks anymore."
var errDesktopTaskGone = errors.New("task_gone")

// DesktopTaskPlan is a waiting task's one plan message. Unavailable is set
// when the plan can't be read; Do it waits then.
type DesktopTaskPlan struct {
	handoff.Plan
	Unavailable bool `json:"unavailable,omitempty"`
}

// DesktopTaskChecks is shown only when a check wasn't met, or nothing could
// be checked.
type DesktopTaskChecks struct {
	Met          int      `json:"met"`
	Total        int      `json:"total"`
	Criteria     []string `json:"criteria"`
	Unmet        []bool   `json:"unmet"`
	Unverifiable bool     `json:"unverifiable,omitempty"`
}

// DesktopTaskDetail is the task's one view beside the Tasks board: the
// phone's thread plus the plan, file chips, project and checks.
type DesktopTaskDetail struct {
	MobileTaskDetail
	Plan    *DesktopTaskPlan   `json:"plan,omitempty"`
	Files   []string           `json:"files,omitempty"`
	Project string             `json:"project,omitempty"`
	Checks  *DesktopTaskChecks `json:"checks,omitempty"`
}

func (a *App) desktopTaskDetail(id string, detail MobileTaskDetail, err error) (DesktopTaskDetail, error) {
	if err != nil {
		if err.Error() == "this task is no longer on your computer" {
			return DesktopTaskDetail{}, errDesktopTaskGone
		}
		return DesktopTaskDetail{}, err
	}
	out := DesktopTaskDetail{MobileTaskDetail: detail}
	if a == nil || a.handoffStore == nil || strings.HasPrefix(id, mobileDraftTaskPrefix) {
		return out, nil
	}
	entry, gerr := a.handoffStore.Get(id)
	if gerr != nil {
		return out, nil
	}
	for _, file := range entry.KeyFiles {
		if file = strings.TrimSpace(file); file != "" && len(out.Files) < 8 {
			out.Files = append(out.Files, file)
		}
	}
	if workdir := strings.TrimSpace(entry.Workdir); workdir != "" {
		out.Project = filepath.Base(workdir)
	}
	if entry.Status == handoff.StatusActive {
		out.Plan = a.desktopTaskPlan(entry)
	}
	out.Checks = desktopTaskChecks(entry)
	return out, nil
}

// desktopTaskPlan builds the plan from the handoff's own fields at projection
// time; it never asks a model.
func (a *App) desktopTaskPlan(entry handoff.Entry) *DesktopTaskPlan {
	if entry.Source == handoff.SourceAgency {
		view := a.productHandoffEntryView(entry).Plan
		if view == nil {
			return &DesktopTaskPlan{Unavailable: true}
		}
		return &DesktopTaskPlan{Plan: handoff.Plan{
			Will: handoff.PlanPart(append([]string{view.DesiredResult}, view.Includes...)),
			Wont: handoff.PlanPart(append(append([]string{}, view.Excludes...), view.RiskSummary)),
			Need: handoff.PlanPart(view.Access),
		}}
	}
	if plan, ok := handoff.PlanFromBody(entry.Body); ok {
		return &DesktopTaskPlan{Plan: plan}
	}
	// A brief without sections: its first paragraph is what Isoform will do.
	var lines []string
	for _, raw := range strings.Split(entry.Body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			if len(lines) > 0 {
				break
			}
			continue
		}
		if line == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		lines = append(lines, line)
	}
	if will := handoff.PlanPart(lines); will != "" {
		return &DesktopTaskPlan{Plan: handoff.Plan{Will: will}}
	}
	return nil
}

func desktopTaskChecks(entry handoff.Entry) *DesktopTaskChecks {
	if entry.Status != handoff.StatusNeedsReview && entry.Status != handoff.StatusCompleted {
		return nil
	}
	checks := &DesktopTaskChecks{Criteria: []string{}, Unmet: []bool{}}
	switch {
	case entry.Result != nil && len(entry.Result.Criteria) > 0:
		for _, criterion := range entry.Result.Criteria {
			checks.Criteria = append(checks.Criteria, mobileOfferText(criterion.Text, 200))
			checks.Unmet = append(checks.Unmet, !criterion.Met)
			if criterion.Met {
				checks.Met++
			}
		}
		checks.Total = len(entry.Result.Criteria)
	case entry.Verification != nil && entry.Verification.Total > 0:
		checks.Met, checks.Total = entry.Verification.Met, entry.Verification.Total
		for i, criterion := range entry.Criteria {
			checks.Criteria = append(checks.Criteria, mobileOfferText(criterion, 200))
			checks.Unmet = append(checks.Unmet, i >= entry.Verification.Met)
		}
	case entry.Status == handoff.StatusNeedsReview && entry.Verification != nil:
		checks.Unverifiable = true
		return checks
	default:
		return nil
	}
	if checks.Met >= checks.Total {
		return nil
	}
	return checks
}
