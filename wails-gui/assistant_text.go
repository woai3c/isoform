package main

import (
	"strconv"
	"strings"
)

// Settings no longer edits Isoform's own text (2026-10-19): the persona, about
// you, instructions and Self statement editors were removed with their Wails
// bindings, and saved edits no longer reach any prompt. The edit rows stay in
// assistant_text_edits_v1 (Forget still erases them), so restoring the editors
// would bring them back. Self statement edits were stored as ordinary Self
// revisions and stay part of the Self model; these guards keep Self Dream from
// overwriting them.

func selfModelIsUserDirective(entry SelfModelEntry) bool {
	for _, e := range entry.Evidence {
		if e.SourceKind == selfDirectiveEvidenceKind {
			return true
		}
	}
	return false
}

// A reset clears protection only for its own field. Retained source evidence
// still enforces the original privacy gates after either operation.
func selfModelHasUserEdits(entry SelfModelEntry) bool {
	if selfModelIsUserDirective(entry) || entry.ReasoningStatus == "self_dream_user_correction" {
		return true
	}
	type fieldEvent struct {
		revision int
		kind     string
	}
	latest := make(map[string]fieldEvent)
	for _, e := range entry.Evidence {
		if e.SourceKind != "assistant_field_edit" && e.SourceKind != "assistant_field_reset" {
			continue
		}
		// Canonical revisions order decisions even within one millisecond,
		// without inventing future timestamps that frozen reads would omit.
		revision, err := strconv.Atoi(strings.SplitN(e.Statement, ":", 2)[0])
		if err != nil {
			return true
		}
		if old, ok := latest[e.SourceID]; !ok || old.revision < revision {
			latest[e.SourceID] = fieldEvent{revision: revision, kind: e.SourceKind}
		}
	}
	for _, e := range latest {
		if e.kind == "assistant_field_edit" {
			return true
		}
	}
	return false
}
