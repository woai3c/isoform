package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMemoryUserEditRejectsActualObservedWriters(t *testing.T) {
	for _, writer := range []string{"native_create", "native_update", "activity_create", "activity_update"} {
		t.Run(writer, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			mc, triager := NewMemoryGenerator(a), &ActivityTriager{app: a}
			at := seedMCPReadActivity(t, a, "edited-observation", "Editor", "com.example.editor", "ordinary", "", "")
			session := ActivitySessionRow{ID: "edited-observation", StartTime: at, AppNames: `["Editor"]`}
			group := sessionGroup{Sessions: []ActivitySessionRow{session}}
			response := &mcMemoryGenerationResponse{Title: "Observed source", Dimension: "user_insight", MemoryPoints: []string{"observed original point"}}
			var id string
			var err error
			if strings.HasPrefix(writer, "activity") {
				id, err = triager.saveActivityMemory(session, group, response)
			} else {
				id, err = mc.saveMemoryFile(memorySourceConversation, "edited-thread", response)
			}
			if err != nil {
				t.Fatal(err)
			}
			edited, err := a.SaveMemoryEntry(id, "# User correction\n\n- **Activity Session**: edited-observation\n\n## Memory Points\n\n- Keep this explicit correction.")
			if err != nil {
				t.Fatal(err)
			}
			switch writer {
			case "native_create":
				_, err = mc.saveMemoryFile(memorySourceConversation, "edited-thread", response)
			case "native_update":
				err = mc.updateMemoryFile(a.ctx, id, memorySourceConversation, "another-thread", response, "")
			case "activity_create":
				_, err = triager.saveActivityMemory(session, group, response)
			case "activity_update":
				_, err = triager.updateActivityMemory(a.ctx, id, session, group, response)
			}
			if !errors.Is(err, errMemoryUserEdited) {
				t.Errorf("observed writer accepted explicit user correction: %v", err)
			}
			current, readErr := a.GetMemoryEntry(id)
			if readErr != nil || current.Source != "user_edit" || current.Content != edited.Content || !slices.Contains(current.ActivitySessionIDs, session.ID) {
				t.Fatalf("user edit ownership/content/provenance changed: %+v, %v", current, readErr)
			}
		})
	}
}

func TestMemoryUserEditPreservesWithheldEnvelopeAndClaimedSessions(t *testing.T) {
	a := newMCPReadTestApp(t)
	mc := NewMemoryGenerator(a)
	id, err := mc.saveMemoryFile(memorySourceTelegram, "private-source", &mcMemoryGenerationResponse{Title: "Private", Dimension: "user_insight", MemoryPoints: []string{"private point"}})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := a.SaveMemoryEntry(id, "# User correction\n\n- **Activity Session**: claimed-session\n\n## Memory Points\n\n- Corrected point")
	if err != nil {
		t.Fatal(err)
	}
	p := memoryReadProvenanceFromContent(entry.Content)
	if entry.Source != "user_edit" || !p.Withheld || !slices.Contains(p.Sessions, "claimed-session") || !slices.Contains(entry.ActivitySessionIDs, "claimed-session") {
		t.Fatalf("user ownership erased privacy or claimed evidence: %+v, %+v", entry, p)
	}
}

func TestMemoryReassignmentRetainsUserEditProtectionAndPrivacy(t *testing.T) {
	a := newMCPReadTestApp(t)
	id, err := NewMemoryGenerator(a).saveMemoryFile(memorySourceTelegram, "private-reassign", &mcMemoryGenerationResponse{Title: "Private source", Dimension: "user_insight", MemoryPoints: []string{"I prefer this option"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "correct-speaker", Kind: storage.PrincipalKindContact, DisplayName: "Speaker", CreatedFrom: "manual"}); err != nil {
		t.Fatal(err)
	}
	if err := a.ReassignMemoryAttribution(id, "correct-speaker", "The other speaker said this"); err != nil {
		t.Fatal(err)
	}
	entry, err := a.GetMemoryEntry(id)
	if err != nil || entry.Source != "user_edit" || !memoryReadProvenanceFromContent(entry.Content).Withheld {
		t.Fatalf("reassignment lost ownership/privacy: %+v, %v", entry, err)
	}
	if _, err := a.saveMemoryEntry(id, "# Re-observed\n\n- Overwritten", false); !errors.Is(err, errMemoryUserEdited) {
		t.Fatalf("observation overwrote attribution correction: %v", err)
	}
}

func TestMemoryUserEditPreservesExplicitScopedForgetSourceLinks(t *testing.T) {
	a := newMCPReadTestApp(t)
	mc := NewMemoryGenerator(a)
	response := &mcMemoryGenerationResponse{Title: "Observed fact", Dimension: "user_insight", MemoryPoints: []string{"initial observation"}}
	id, err := mc.saveMemoryFile(memorySourceConversation, "original-thread", response)
	if err != nil {
		t.Fatal(err)
	}
	if err := mc.updateMemoryFile(a.ctx, id, memorySourceConversation, "secondary-thread", response, ""); err != nil {
		t.Fatal(err)
	}
	original, err := a.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parseOKFDocument(original.Content)
	priorSourceIDs := okfStringSlice(doc.Frontmatter, "source_ids")
	if len(priorSourceIDs) != 2 {
		t.Fatalf("real writer did not record both sources: %v", priorSourceIDs)
	}
	doc.Frontmatter["merged_from"] = []string{"prior-merged-source"}
	writeMCPReadMarkdown(t, id, renderOKFDocument(doc.Frontmatter, doc.Body))
	// An explicit replacement defines its own forgetting boundary. Retain
	// the observation links in this submitted version when they still apply;
	// prior versions keep their own links for scoped erasure through history.
	claim := renderOKFDocument(map[string]any{"title": "Explicit correction", "source_ids": append(priorSourceIDs, "claimed-source"), "merged_from": []string{"prior-merged-source", "claimed-merged-source"}, "read_provenance": memoryReadProvenanceJSON(memoryReadSource("manual"))}, "## Memory Points\n\n- Unique corrected wording")
	edited, err := a.SaveMemoryEntry(id, claim)
	if err != nil {
		t.Fatal(err)
	}
	editedDoc, _ := parseOKFDocument(edited.Content)
	for _, sourceID := range append(priorSourceIDs, "claimed-source") {
		if !slices.Contains(okfStringSlice(editedDoc.Frontmatter, "source_ids"), sourceID) {
			t.Fatalf("edit dropped scoped forget source %q", sourceID)
		}
	}
	if !slices.Contains(okfStringSlice(editedDoc.Frontmatter, "merged_from"), "prior-merged-source") || !slices.Contains(okfStringSlice(editedDoc.Frontmatter, "merged_from"), "claimed-merged-source") {
		t.Fatalf("edit dropped explicitly retained source metadata: %+v", editedDoc.Frontmatter)
	}
	if p := memoryReadProvenanceFromContent(edited.Content); slices.Contains(p.Kinds, "manual") || !slices.Contains(p.Kinds, "conversation") {
		t.Fatalf("submitted metadata rewrote read authority: %+v", p)
	}
	preview, err := a.GetMemoryForgetPreview(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ForgetMemoryEntry(id, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	response.MemoryPoints = []string{"Entirely different re-observation text"}
	if _, err := mc.saveMemoryFile(memorySourceConversation, "original-thread", response); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("forget lost the corrected record identity: %v", err)
	}
	// Observation source IDs are parents, not descendants. Forgetting the
	// corrected record must not suppress a different fact from its source.
	if _, err := mc.saveMemoryFile(memorySourceConversation, "secondary-thread", response); err != nil {
		t.Fatalf("forget incorrectly suppressed an independent source observation: %v", err)
	}
}

func TestMemoryReassignmentCannotRemoveForgottenSourceScope(t *testing.T) {
	a := newMCPReadTestApp(t)
	id, err := NewMemoryGenerator(a).saveMemoryFile(memorySourceConversation, "forgotten-thread", &mcMemoryGenerationResponse{Title: "Forgotten source", Dimension: "user_insight", MemoryPoints: []string{"original source point"}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := a.GetMemoryForgetPreview(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ForgetMemoryEntry(id, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	content := buildOKFMemoryMarkdown("alias", "Different record", time.Now(), memorySourceConversation, "user_insight", []string{"I describe a different paraphrase"})
	doc, _ := parseOKFDocument(content)
	doc.Frontmatter["merged_from"] = []string{id}
	content = renderOKFDocument(doc.Frontmatter, doc.Body)
	writeMCPReadMarkdown(t, "alias", content)
	if _, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "speaker", Kind: storage.PrincipalKindContact, DisplayName: "Speaker", CreatedFrom: "manual"}); err != nil {
		t.Fatal(err)
	}
	if err := a.ReassignMemoryAttribution("alias", "speaker", "Correct attribution"); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("reassignment removed scoped forget protection: %v", err)
	}
	current, err := a.GetMemoryEntry("alias")
	if err != nil || current.Content != content {
		t.Fatalf("failed reassignment mutated forgotten source: %+v, %v", current, err)
	}
}

func TestMemoryGenerationRanksRelevantOlderSnapshotWithinBudget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		title  string
		points []string
	}{
		{
			name:   "title_and_first_point",
			title:  "Orchard release checklist",
			points: []string{"orchard release checklist " + strings.Repeat("relevant detail ", 120)},
		},
		{
			name:  "fourth_point_only",
			title: "Established delivery workflow",
			points: []string{
				strings.Repeat("baseline detail ", 50),
				strings.Repeat("routine procedure ", 50),
				strings.Repeat("established process ", 50),
				"Orchard release checklist",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			old := time.Now().Add(-24 * time.Hour)
			writeMCPReadMarkdown(t, "older-relevant", buildOKFMemoryMarkdown("older-relevant", tc.title, old, "manual", "user_insight", tc.points))
			path := filepath.Join(configDirOrFallback(), "memory", "older-relevant.md")
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 7; i++ {
				id := fmt.Sprintf("recent-unrelated-%d", i)
				writeMCPReadMarkdown(t, id, buildOKFMemoryMarkdown(id, "Unrelated recent note", time.Now(), "manual", "user_insight", []string{strings.Repeat("noise ", 1160)}))
			}
			seedMCPReadActivity(t, a, "hidden-relevant", "Safari", "com.apple.Safari", "Private Browsing", "", "")
			writeMCPReadMemory(t, "hidden-relevant", "hidden-relevant", "# orchard release checklist private title\n\n- orchard release checklist private point")
			text, p := loadMemoryGenerationContextForQuery(a.ctx, a, "orchard release checklist", 50_000)
			if !strings.Contains(text, "[id=older-relevant]") || strings.Contains(text, "hidden-relevant") || len(text) > 50_000 || p == nil {
				t.Fatalf("relevant older safe snapshot was lost or private text admitted: %q, %+v", text, p)
			}
			if !strings.HasPrefix(text, "- [id=older-relevant]") {
				t.Fatal("relevance did not outrank recency")
			}
			if tc.name == "fourth_point_only" && strings.Contains(text, "Orchard release checklist") {
				t.Fatal("ranking expanded the compact prompt beyond its leading three points")
			}
		})
	}
}

func TestMemoryGenerationProvenanceBoundedAcrossThreeGenerations(t *testing.T) {
	a := newMCPReadTestApp(t)
	allSessions := make([]string, 0, 270)
	for i := 0; i < 270; i++ {
		id := fmt.Sprintf("lineage-session-%03d", i)
		seedMCPReadActivity(t, a, id, "Editor", "com.example.editor", "ordinary", "", "")
		allSessions = append(allSessions, id)
		body := "# Source\n\n- lineage comparison"
		content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Source", "source": "activity", "dimension": "user_insight", "activity_sessions": []string{id, id}, "read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity", "activity"}, Sessions: []string{id, id}, PromptSafe: true})}, body)
		writeMCPReadMarkdown(t, fmt.Sprintf("lineage-memory-%03d", i), content)
	}
	// A valid input that exceeds the cap must be omitted whole, never have
	// its dependencies silently trimmed to make its text fit.
	writeMCPReadMarkdown(t, "oversized-lineage", withMemoryReadProvenance(buildOKFMemoryMarkdown("oversized-lineage", "Oversized lineage", time.Now(), "manual", "user_insight", []string{"oversized lineage marker"}), memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: allSessions, PromptSafe: true}))
	writeMCPReadMarkdown(t, "oversized-envelope", withMemoryReadProvenance(buildOKFMemoryMarkdown("oversized-envelope", "Oversized envelope", time.Now(), "manual", "user_insight", []string{"oversized envelope marker"}), memoryReadProvenance{Version: 2, Kinds: []string{"manual"}, Apps: []string{strings.Repeat("long-app-name", 4000)}}))
	for generation := 0; generation < 3; generation++ {
		inputs := loadMemoryGenerationPromptContextForQuery(a.ctx, a, "lineage comparison")
		if inputs.provenance == nil || len(inputs.provenance.Sessions) == 0 || !memoryGenerationProvenanceFits(*inputs.provenance) || strings.Contains(inputs.existing, "[id=oversized-") {
			t.Fatalf("generation %d admitted oversized dependencies: %+v", generation, inputs.provenance)
		}
		for _, line := range strings.Split(inputs.existing, "\n") {
			if !strings.HasPrefix(line, "- [id=") {
				continue
			}
			id := strings.SplitN(strings.TrimPrefix(line, "- [id="), "]", 2)[0]
			entry, err := a.GetMemoryEntry(id)
			if err != nil {
				t.Fatal(err)
			}
			for _, dependency := range append(memoryReadProvenanceFromContent(entry.Content).Sessions, entry.ActivitySessionIDs...) {
				if !slices.Contains(inputs.provenance.Sessions, dependency) {
					t.Fatalf("generation %d dropped admitted dependency %q from %q", generation, dependency, id)
				}
			}
		}
		id := fmt.Sprintf("generation-%d", generation)
		at := seedMCPReadActivity(t, a, id, "Editor", "com.example.editor", "ordinary", "", "")
		session := ActivitySessionRow{ID: id, StartTime: at, AppNames: `["Editor"]`}
		response := &mcMemoryGenerationResponse{Title: "Generated lineage", Dimension: "user_insight", MemoryPoints: []string{"lineage comparison generation"}}
		inputs.apply(response)
		memoryID, err := (&ActivityTriager{app: a}).saveActivityMemory(session, sessionGroup{Sessions: []ActivitySessionRow{session, session}}, response)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := a.GetMemoryEntry(memoryID)
		if err != nil {
			t.Fatal(err)
		}
		p := memoryReadProvenanceFromContent(entry.Content)
		if !slices.Equal(entry.ActivitySessionIDs, []string{id}) {
			t.Fatalf("comparison inputs became evidence: %v", entry.ActivitySessionIDs)
		}
		if len(p.Sessions) > 257 || len(p.Sessions) != len(memoryProvenanceUnique(p.Sessions)) || len(parseMemoryFrontmatter(entry.Content).ActivitySessionIDs) != len(p.Sessions) || len(entry.ActivitySessionIDs) != len(memoryProvenanceUnique(entry.ActivitySessionIDs)) {
			t.Fatalf("generation %d grew duplicate/unbounded stored metadata: p=%d, metadata=%d", generation, len(p.Sessions), len(entry.ActivitySessionIDs))
		}
		for _, dependency := range append(inputs.provenance.Sessions, id) {
			if !slices.Contains(p.Sessions, dependency) || !slices.Contains(parseMemoryFrontmatter(entry.Content).ActivitySessionIDs, dependency) {
				t.Fatalf("writer dropped dependency %q", dependency)
			}
		}
	}
}

func TestMemoryGenerationLogsUnavailablePrivacyOncePerLoad(t *testing.T) {
	for _, state := range []string{"paused", "unavailable"} {
		t.Run(state, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			if state == "paused" {
				a.closedEyes.enabled.Store(true)
			} else {
				a.store = nil
			}
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(previous)
			text, p := loadMemoryGenerationContext(a.ctx, a)
			if text != "" || p != nil || strings.Count(output.String(), "dedup context unavailable:") != 1 {
				t.Fatalf("unavailable context did not log one useful reason: %q / %q / %+v", state, output.String(), p)
			}
		})
	}
}
