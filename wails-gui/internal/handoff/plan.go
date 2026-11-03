package handoff

import (
	"strings"
	"unicode/utf8"
)

// planPartRunes bounds one part of a waiting task's plan message.
const planPartRunes = 320

// Plan is the one message a task waiting to start shows (Tasks v1.0.0
// section 2): What I'll do, What I won't do and What I need, read from the
// brief at projection time. No model call makes it.
type Plan struct {
	Will string `json:"will"`
	Wont string `json:"wont,omitempty"`
	Need string `json:"need,omitempty"`
}

// PlanFromBody reads the plan out of a brief's sections: Desired result and
// Included for What I'll do; Isoform will not, Boundaries and Risk and
// control for What I won't do; Access needed for What I need. ok is false
// when the brief has no Desired result.
func PlanFromBody(body string) (Plan, bool) {
	sections := map[string][]string{}
	current := ""
	for _, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			current = planHeading(line)
			continue
		}
		if current == "" || line == "" {
			continue
		}
		if text := strings.TrimSpace(criterionMarker.ReplaceAllString(line, "")); text != "" {
			sections[current] = append(sections[current], text)
		}
	}
	plan := Plan{
		Will: PlanPart(append(sections["desired result"], sections["included"]...)),
		Wont: PlanPart(append(append(sections["isoform will not"], sections["boundaries"]...), sections["risk and control"]...)),
		Need: PlanPart(sections["access needed"]),
	}
	return plan, len(sections["desired result"]) > 0
}

func planHeading(line string) string {
	title := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
	title = strings.TrimSpace(strings.TrimSuffix(title, ":"))
	switch title {
	case "desired result", "included", "isoform will not", "yansu will not", "boundaries", "risk and control", "access needed":
		if title == "yansu will not" {
			return "isoform will not"
		}
		return title
	}
	return ""
}

// PlanPart joins a plan part's lines into one short passage. A line that
// says nothing is needed ("None") leaves the part out.
func PlanPart(lines []string) string {
	var parts []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch strings.ToLower(strings.TrimSuffix(line, ".")) {
		case "", "none", "nothing", "n/a", "无":
			continue
		}
		if last := line[len(line)-1:]; !strings.ContainsAny(last, ".!?。！？") {
			line += "."
		}
		parts = append(parts, line)
	}
	text := strings.Join(parts, " ")
	if utf8.RuneCountInString(text) > planPartRunes {
		runes := []rune(text)
		text = strings.TrimSpace(string(runes[:planPartRunes-1])) + "…"
	}
	return text
}
