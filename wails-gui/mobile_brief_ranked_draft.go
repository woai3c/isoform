package main

import (
	"encoding/json"
	"strings"
)

// A writer may refer only to the capped selection. The client receives the
// existing evidence refs, never the scorer's private source identities.
type morningBriefDraftParagraph struct {
	Text  string                `json:"text"`
	Items []string              `json:"items"`
	Chip  *mobileBriefCardWords `json:"chip,omitempty"`
}

type morningBriefDraftReply struct {
	Paragraphs []morningBriefDraftParagraph `json:"paragraphs"`
	Answers    []mobileBriefCardWords       `json:"answers,omitempty"`
}

func parseMorningBriefDraft(raw string, selection morningBriefSelection) (morningBriefDraft, error) {
	decoder := json.NewDecoder(strings.NewReader(stripMobileBriefCodeFence(raw)))
	decoder.DisallowUnknownFields()
	var reply morningBriefDraftReply
	if err := decoder.Decode(&reply); err != nil {
		return morningBriefDraft{}, err
	}
	if selection.Lead == nil || len(reply.Paragraphs) == 0 || len(reply.Paragraphs) > mobileBriefMaxWords {
		return morningBriefDraft{}, codeError("invalid draft structure")
	}
	items := map[string]morningBriefCandidate{selection.Lead.ID: *selection.Lead}
	for _, item := range selection.Needs {
		items[item.ID] = item
	}
	if selection.Slip != nil {
		items[selection.Slip.ID] = *selection.Slip
	}
	if selection.Handled != nil {
		items[selection.Handled.ID] = *selection.Handled
	}
	var draft morningBriefDraft
	for index, paragraph := range reply.Paragraphs {
		text := strings.TrimSpace(paragraph.Text)
		if text == "" || len(text) > mobileBriefNarrativeMaxBytes || !mobileBriefCopySafe(text) || redactThreadOutcomeText(text) != text || len(paragraph.Items) == 0 {
			return morningBriefDraft{}, codeError("invalid draft paragraph")
		}
		var refs []string
		lead := false
		seen := map[string]bool{}
		for _, id := range paragraph.Items {
			item, ok := items[id]
			if !ok || id == "" || seen[id] || len(item.Refs) == 0 {
				return morningBriefDraft{}, codeError("draft cites an unavailable item")
			}
			seen[id] = true
			lead = lead || id == selection.Lead.ID
			for _, ref := range item.Refs {
				if ref == "" {
					return morningBriefDraft{}, codeError("draft cites an unavailable item")
				}
				if !containsString(refs, ref) {
					refs = append(refs, ref)
				}
			}
		}
		if index == 0 && !lead {
			return morningBriefDraft{}, codeError("draft does not lead with the selected item")
		}
		draft.Paragraphs = append(draft.Paragraphs, MobileBriefPara{Text: text, Refs: refs, chipWords: paragraph.Chip})
	}
	if mobileBriefNarrativeWords(draft.Paragraphs) > mobileBriefMaxWords {
		return morningBriefDraft{}, codeError("draft exceeds word ceiling")
	}
	if len(reply.Answers) > 2 {
		return morningBriefDraft{}, codeError("too many closing answers")
	}
	draft.Answers = reply.Answers
	return draft, nil
}
