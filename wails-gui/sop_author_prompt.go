package main

import (
	"fmt"
	"strings"
)

// sopAuthorSystemPrompt scopes the unattended loop to distilling observed
// activity evidence into a reusable, executable process document. Unlike the
// handoff author (one task, shallow pass), the SOP author synthesizes a
// recurring end-to-end process across days of evidence — but it is still an
// explore-and-draft pass, never an execution pass.
const sopAuthorSystemPrompt = `You are Yansu's SOP author. You run UNATTENDED in the background to freeze a recurring end-to-end work process observed in the user's activity into a Standard Operating Procedure: a reusable, executable process document.

Your deliverables are a file named SOP.md in your working directory, plus optional helper scripts under ./scripts/. Nothing else you do is persisted.

An SOP is bigger than a single task: it is the COMPLETE repeatable process (multiple ordered steps, each with concrete instructions an agent can follow, and an observable checkpoint). Write it so that a capable agent — or a colleague who has never seen this work — can execute the whole process end-to-end.

Use tools only to orient:
- Read the seed files under ./context (brief, group summary, activities, timeline, memory, knowledge, existing-sops) when present.
- When ./context/existing-sops.md exists, check it BEFORE drafting: if the process you are describing is the same process as a listed SOP, add the line ` + "`updateExisting: <slug>`" + ` to your SOP.md frontmatter (using that SOP's slug verbatim) so your draft replaces it instead of creating a duplicate. Only do this for the SAME process — a genuinely different process must not name updateExisting.
- When updating an existing SOP, read ./existing/SOP.md first and revise it — preserve steps that are still correct, refine or extend where the new evidence shows changes. Do not rewrite from scratch.
- Use at most a few cheap local reads/searches. Do NOT browse the web, call integrations, run the process itself, install packages, or do broad recursive exploration.

Hard rules (do not violate):
- EXPLORE AND DRAFT ONLY. Never modify, create, move, or delete the user's files. Never commit, push, or run destructive shell commands. The ONLY files you write are SOP.md and files under ./scripts/ in your working directory.
- Ground every step in observed evidence. If the evidence shows only part of the process, write the observed part concretely and mark unobserved gaps explicitly as "not yet observed" — never invent steps.
- Scripts are optional helpers, never requirements: create a script under ./scripts/ ONLY when the evidence contains a concrete, verbatim command sequence worth capturing. Scripts must be non-destructive and referenced from the step that uses them.
- Steps must be written as instructions/prompts an agent can execute, each ending with an observable checkpoint.
- Do not include secrets, tokens, passwords, or personal data in SOP.md or scripts even if they appear in the evidence — reference them as placeholders like <API_KEY>.`

// sopAuthorRecoveryNudge replaces the loop's default "stop and summarise" nudge
// — for this flow the work IS the tool calls plus the final Write.
const sopAuthorRecoveryNudge = "Stop exploring and write SOP.md now. Distill the steps you can ground in the seed evidence; mark anything unobserved explicitly rather than inventing it."

// buildSOPAuthorPrompt is the user turn: the candidate process plus the exact
// SOP.md shape to emit. The author writes only title/description/trigger into
// the frontmatter; the Go side owns every machine field (id, status, source,
// version, timestamps).
func buildSOPAuthorPrompt(spec sopAuthorSpec) string {
	var task strings.Builder
	if t := strings.TrimSpace(spec.Name); t != "" {
		fmt.Fprintf(&task, "Candidate process name: %s\n", t)
	} else {
		task.WriteString("Candidate process name: (derive it from the evidence — pick the dominant recurring end-to-end process in the window)\n")
	}
	if d := strings.TrimSpace(spec.Description); d != "" {
		fmt.Fprintf(&task, "Candidate description: %s\n", d)
	}
	if tr := strings.TrimSpace(spec.Trigger); tr != "" {
		fmt.Fprintf(&task, "Candidate trigger: %s\n", tr)
	}
	if o := strings.TrimSpace(spec.Outline); o != "" {
		fmt.Fprintf(&task, "Step outline observed by triage:\n%s\n", o)
	}
	if spec.Action == "update" {
		task.WriteString("Action: UPDATE — revise ./existing/SOP.md with the new evidence instead of starting over.\n")
	} else {
		task.WriteString("Action: CREATE — this is a new SOP.\n")
	}

	return fmt.Sprintf(`Author an SOP (Standard Operating Procedure) for this recurring process:

%s
Seed evidence (when present) is under ./context — read brief.md first, then activities.md and timeline.md. Then write ./SOP.md with EXACTLY this structure:

---
title: <concise process name>
description: <one sentence: what running this process produces>
trigger: <when to run this — the situation that starts the process>
updateExisting: <slug of the matching SOP from ./context/existing-sops.md; OMIT this line entirely for a new process>
---
# Goal
<what a successful run of this process delivers, concretely>

# Prerequisites
<tools, access, files, or state that must exist before starting; "none" is okay>

# Steps
## 1. <step name>
<concrete instructions / the exact prompt an executing agent should follow for this step>
**Checkpoint:** <observable success criterion for this step>

## 2. <step name>
...

# Success criteria
<how to verify the whole process completed correctly>

# Evidence
<which observed activities/time windows this SOP was distilled from, and any gaps marked "not yet observed">

Write the file with the Write tool. Only add files under ./scripts/ when a step captures a concrete observed command sequence. Do not print the SOP in chat.`, task.String())
}
