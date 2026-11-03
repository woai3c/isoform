package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// insertRetiredAssistantEdit writes a row as the removed Settings editor did.
func insertRetiredAssistantEdit(t *testing.T, store *ActivityStore, fieldID, value, action string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := store.db.Exec(`INSERT INTO assistant_text_edits_v1
		(id, field_id, value, learned_value, action, edited_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, fieldID, value, "learned "+fieldID, action, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return id
}

// seedRetiredSelfEdit stores a Self statement edit the way the removed editor
// did: an ordinary Self revision carrying assistant_field evidence.
func seedRetiredSelfEdit(t *testing.T, store *ActivityStore, entry SelfModelEntry, statement, action string) SelfModelEntry {
	t.Helper()
	fieldID := "self:" + entry.EntryKey + ":statement"
	editID := insertRetiredAssistantEdit(t, store, fieldID, statement, action)
	entry.Statement = statement
	entry.Evidence = append(entry.Evidence, SelfModelEvidence{
		SourceKind: "assistant_field_" + action, SourceID: fieldID,
		Statement: fmt.Sprintf("%d:%s", entry.Revision+1, editID), Confidence: 1, OccurredAt: time.Now().UnixMilli(),
	})
	entry.ReasoningStatus = "explicit_user_control"
	entry.CreatedAt = time.Now().UnixMilli()
	revised, _, err := store.AddSelfModelEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	return revised
}

func TestRetiredAssistantTextOverridesNoLongerApply(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	path, err := soulFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Learned tone"), 0600); err != nil {
		t.Fatal(err)
	}
	for id, value := range map[string]string{"persona": "My own tone", "about_you": "I run a bakery", "instructions": "Always answer in French"} {
		insertRetiredAssistantEdit(t, rig.store, id, value, "edit")
	}
	if got, err := rig.app.ReadSoulFile(); err != nil || got != "Learned tone" {
		t.Fatalf("persona = %q, %v; want the learned file", got, err)
	}
	prompt := rig.app.injectSoulContext("question")
	if !strings.Contains(prompt, "Learned tone") {
		t.Fatal("prompt lost the learned persona")
	}
	for _, retired := range []string{"My own tone", "I run a bakery", "Always answer in French", "Settings > Assistant"} {
		if strings.Contains(prompt, retired) {
			t.Fatalf("prompt still carries %q", retired)
		}
	}
	// The rows stay on disk, so the change can be rolled back.
	var rows int
	if err := rig.store.db.QueryRow(`SELECT COUNT(*) FROM assistant_text_edits_v1`).Scan(&rows); err != nil || rows != 3 {
		t.Fatalf("saved edits = %d, %v; want 3 kept", rows, err)
	}
}

func TestSelfModelHasUserEditsReadsRetainedEvidence(t *testing.T) {
	field := "self:k:statement"
	evidence := func(kind string, revision int) SelfModelEvidence {
		return SelfModelEvidence{SourceKind: kind, SourceID: field, Statement: fmt.Sprintf("%d:%s", revision, uuid.NewString())}
	}
	for _, tc := range []struct {
		name  string
		entry SelfModelEntry
		want  bool
	}{
		{"learned only", SelfModelEntry{Evidence: []SelfModelEvidence{{SourceKind: taskContextSourceMemoryKind}}}, false},
		{"edited", SelfModelEntry{Evidence: []SelfModelEvidence{evidence("assistant_field_edit", 2)}}, true},
		{"edited then reset", SelfModelEntry{Evidence: []SelfModelEvidence{evidence("assistant_field_edit", 2), evidence("assistant_field_reset", 3)}}, false},
		{"reset then edited", SelfModelEntry{Evidence: []SelfModelEvidence{evidence("assistant_field_reset", 2), evidence("assistant_field_edit", 3)}}, true},
		{"user correction", SelfModelEntry{ReasoningStatus: "self_dream_user_correction"}, true},
		{"directive", SelfModelEntry{Evidence: []SelfModelEvidence{{SourceKind: selfDirectiveEvidenceKind}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selfModelHasUserEdits(tc.entry); got != tc.want {
				t.Fatalf("selfModelHasUserEdits = %v, want %v", got, tc.want)
			}
		})
	}
}

// A Self statement the user edited before the editor was removed stays
// protected: Self Dream proposes, a person reviews.
func TestRetainedSelfEditStillNeedsSelfDreamReview(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	left, _ := seedSelfDreamDuplicateEntries(t, rig)
	seedRetiredSelfEdit(t, rig.store, left, left.Statement, "edit")
	detail, err := rig.app.runSelfDreamShadow(context.Background(), "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	var merge SelfDreamOperationRecord
	for _, op := range detail.Operations {
		if op.Operation == SelfDreamOperationMerge {
			merge = op
		}
	}
	if merge.ID == "" || merge.AutoEligible || merge.ValidationStatus != SelfDreamValidationReviewRequired {
		t.Fatalf("edit did not force review: %+v", merge)
	}
	if _, err := rig.store.compileSelfDreamOperation(merge, SelfDreamDecisionRecord{ActorKind: "rollout_gate", Decision: SelfDreamDecisionAutoPromote}); err == nil {
		t.Fatal("automatic compile overwrote a user edit")
	}
}

func TestAssistantMemoryForgetErasesSourceDerivedEditHistory(t *testing.T) {
	rig := newSelfDreamTestRig(t)
	entry := seedMemoryForgetSelf(t, rig.store, "forget-edited-self", "Source-derived preference")
	entry = seedRetiredSelfEdit(t, rig.store, entry, "An edited preference", "edit")
	insertRetiredAssistantEdit(t, rig.store, "self:"+entry.EntryKey+":scope", "Edited scope", "edit")
	seedRetiredSelfEdit(t, rig.store, entry, "Source-derived preference", "reset")
	insertRetiredAssistantEdit(t, rig.store, "instructions", "An independent explicit instruction", "edit")
	before := memoryForgetTriggerDefinitions(t, rig.store.db)
	tx, err := rig.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	preview, err := collectMemoryForgetDerivatives(tx, "forget-edited-self")
	if err != nil {
		t.Fatal(err)
	}
	var edits int
	for _, row := range preview {
		if row.RecordType == "revision" {
			edits++
		}
	}
	if edits != 3 {
		t.Fatalf("forget preview omitted edit history: %+v", preview)
	}
	if err := deleteMemoryForgetDerivatives(tx, "forget-edited-self"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var remaining, independent int
	if err := rig.store.db.QueryRow(`SELECT COUNT(*) FROM assistant_text_edits_v1 WHERE field_id LIKE ?`, "self:"+entry.EntryKey+":%").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("forgotten edit history remains: %d, %v", remaining, err)
	}
	if err := rig.store.db.QueryRow(`SELECT COUNT(*) FROM assistant_text_edits_v1 WHERE field_id = 'instructions'`).Scan(&independent); err != nil || independent != 1 {
		t.Fatalf("forget changed independent text: %d, %v", independent, err)
	}
	if after := memoryForgetTriggerDefinitions(t, rig.store.db); fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("forget did not restore immutable guards")
	}
}
