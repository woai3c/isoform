package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
	"wails-gui/internal/pii"
)

func TestDreamSynthesisPreservesVersionRefsAndReadProvenance(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		name := "live source"
		if frozen {
			name = "frozen source"
		}
		t.Run(name, func(t *testing.T) {
			app := newMCPReadTestApp(t)
			seedMCPReadActivity(t, app, "dream-source", "Editor", "com.example.editor", "ordinary", "", "")
			writeMCPReadMemory(t, "dream-input", "dream-source", "versioned source detail")
			entry, err := app.GetMemoryEntry("dream-input")
			if err != nil {
				t.Fatal(err)
			}
			refs := []string{taskContextSourceRef(taskContextSourceMemoryKind, entry.ID, entry.ModTime, entry.Content)}
			service := &MemoryDreamService{app: app}
			var id string
			switch frozen {
			case true:
				provenance := memoryReadProvenanceForFile(filepath.Join(configDirOrFallback(), "memory"), entry.ID, entry.Content)
				id, err = service.writeSynthesizedMemoryWithProvenance("Merged detail", "mergeprivacyneedle versioned detail", "user_insight", []string{entry.ID}, []memoryReadProvenance{provenance}, refs...)
			case false:
				id, err = service.writeSynthesizedMemory("Merged detail", "mergeprivacyneedle versioned detail", "user_insight", []string{entry.ID}, refs...)
			}
			if err != nil {
				t.Fatal(err)
			}
			merged, err := app.GetMemoryEntry(id)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := parseOKFDocument(merged.Content); !ok {
				t.Fatal("synthesis lost the OKF document")
			}
			if got := memoryDerivedSourceRefs(merged.Content); !reflect.DeepEqual(got, refs) {
				t.Fatalf("versioned source refs=%v, want %v", got, refs)
			}
			provenance := memoryReadProvenanceFromContent(merged.Content)
			if provenance.Withheld || !reflect.DeepEqual(provenance.Sessions, []string{"dream-source"}) {
				t.Fatalf("synthesis lost read provenance: %+v", provenance)
			}
			if got := callMCPReadTest(t, app, "memory_search", map[string]any{"query": "mergeprivacyneedle"}); len(got.Items) != 1 {
				t.Fatalf("readable synthesis withheld: %+v", got)
			}
			if err := app.store.SetUIState(app.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
				t.Fatal(err)
			}
			if got := callMCPReadTest(t, app, "memory_search", map[string]any{"query": "mergeprivacyneedle"}); len(got.Items) != 0 {
				t.Fatalf("synthesis lost its live source gate: %+v", got)
			}
		})
	}
}

func TestObservedMemoryWritesComposeCallerContextAndReadProvenance(t *testing.T) {
	for _, writer := range []string{"generator", "activity"} {
		for _, cancelled := range []bool{false, true} {
			name := writer + "/active context"
			if cancelled {
				name = writer + "/cancelled optional probe"
			}
			t.Run(name, func(t *testing.T) {
				app := newMCPReadTestApp(t)
				at := seedMCPReadActivity(t, app, "merge-observation", "Editor", "com.example.editor", "ordinary", "", "")
				provenance := memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"merge-observation"}}
				input := &mcMemoryGenerationResponse{Title: "Observed planning", Dimension: "user_insight", MemoryPoints: []string{"contextprivacyneedle tracks planning"}, inputProvenance: &provenance}
				var calls atomic.Int32
				app.memoryCanonicalizer = func(string, string) (string, error) {
					calls.Add(1)
					return `{"version":"isoform-memory-semantics-v1","status":"no_facts","facts":[]}`, nil
				}
				ctx, cancel := context.WithCancel(app.ctx)
				defer cancel()
				if cancelled {
					cancel()
				}
				write := func() (string, error) {
					switch writer {
					case "generator":
						return (&MemoryGenerator{app: app}).saveMemoryFileContext(ctx, memorySourceConversation, "merge-thread", input)
					default:
						session := ActivitySessionRow{ID: "merge-observation", StartTime: at, AppNames: `["Editor"]`}
						_, err := (&ActivityTriager{app: app}).saveActivityMemoryContext(ctx, session, sessionGroup{Sessions: []ActivitySessionRow{session}}, input)
						return activityMemoryEntryID(session.ID), err
					}
				}
				id, err := write()
				if err != nil {
					t.Fatal(err)
				}
				wantCalls := int32(1)
				if cancelled {
					wantCalls = 0
				}
				if calls.Load() != wantCalls {
					t.Fatalf("optional probe calls=%d, want %d", calls.Load(), wantCalls)
				}
				entry, err := app.GetMemoryEntry(id)
				if err != nil {
					t.Fatal(err)
				}
				gotProvenance := memoryReadProvenanceFromContent(entry.Content)
				if gotProvenance.Withheld || !reflect.DeepEqual(gotProvenance.Sessions, provenance.Sessions) {
					t.Fatalf("context-aware writer lost read provenance: %+v", gotProvenance)
				}
				if err := app.store.SetUIState(app.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
				if got := callMCPReadTest(t, app, "memory_list", map[string]any{}); len(got.Items) != 0 {
					t.Fatalf("context-aware writer lost the source gate: %+v", got)
				}
				if err := app.store.SetUIState(app.ctx, "sensitive_apps", `{}`); err != nil {
					t.Fatal(err)
				}
				if got := callMCPReadTest(t, app, "memory_list", map[string]any{}); len(got.Items) != 1 {
					t.Fatalf("fixture lineage was not readable before forgetting: %+v", got)
				}
				dir, err := memoryDir()
				if err != nil {
					t.Fatal(err)
				}
				if err := recordMemoryTombstone(dir, id, []string{entry.Content}, time.Now()); err != nil {
					t.Fatal(err)
				}
				if _, err := write(); !errors.Is(err, errMemoryForgotten) {
					t.Fatalf("readable provenance bypassed observed-write suppression: %v", err)
				}
				if calls.Load() != wantCalls {
					t.Fatal("forgotten observation reached optional semantic preparation")
				}
			})
		}
	}
}

func TestShadowLearningMaterializationPreservesMergedReadProvenance(t *testing.T) {
	app := newMCPReadTestApp(t)
	at := seedMCPReadActivity(t, app, "materialized-source", "Editor", "com.example.editor", "ordinary", "", "")
	input := taskEpisodeFixture("materialized-episode", at, at+1000, TaskEpisodeStatusCandidate)
	input.FrozenContext.Manifest = []TaskContextManifestEntry{{SourceKind: "activity_session", SourceID: "materialized-source", Included: true}}
	input.InitialEvidence[0].SourceID = "materialized-source"
	if _, _, err := app.activity.store.CreateTaskEpisode(input); err != nil {
		t.Fatal(err)
	}
	candidate := ShadowTaskLearningCandidate{TargetKind: ShadowTaskLearningTargetMemory, ProjectionKey: "merged-materialization", Statement: "shadowmergeprivacyneedle planning", Evidence: []ShadowTaskLearningProvenance{{TaskEpisodeID: input.ID}}}
	id, errText := app.applyQualifiedShadowTaskLearningCandidate(candidate, at)
	if errText != "" {
		t.Fatal(errText)
	}
	entry, err := app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	baseline, ok := parseOKFDocument(shadowTaskLearningMemoryContent(candidate, at))
	if !ok {
		t.Fatal("feat content helper did not render OKF")
	}
	actual, ok := parseOKFDocument(entry.Content)
	if !ok || actual.Body != baseline.Body {
		t.Fatal("materialization changed the shared content helper's body")
	}
	for key, value := range baseline.Frontmatter {
		if key == "read_provenance" {
			// Materialization replaces the helper's unverified placeholder with
			// resolved activity lineage, asserted below.
			continue
		}
		if !reflect.DeepEqual(actual.Frontmatter[key], value) {
			t.Errorf("materialization changed shared metadata %s", key)
		}
	}
	provenance := memoryReadProvenanceFromContent(entry.Content)
	if provenance.Withheld || !reflect.DeepEqual(provenance.Sessions, []string{"materialized-source"}) {
		t.Fatalf("materialization omitted resolved activity lineage: %+v", provenance)
	}
	if got := callMCPReadTest(t, app, "memory_search", map[string]any{"query": "shadowmergeprivacyneedle"}); len(got.Items) != 1 {
		t.Fatalf("readable materialization withheld: %+v", got)
	}
	if err := app.store.SetUIState(app.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
		t.Fatal(err)
	}
	if got := callMCPReadTest(t, app, "memory_search", map[string]any{"query": "shadowmergeprivacyneedle"}); len(got.Items) != 0 {
		t.Fatalf("materialized source privacy was lost: %+v", got)
	}
}

func TestMemoryUpdateFallbackLogsUsePrivateFields(t *testing.T) {
	for _, flag := range []string{"", "1"} {
		for _, failure := range []string{"invalid", "missing", "unreadable"} {
			t.Run(failure+"/debug="+flag, func(t *testing.T) {
				app := newMCPReadTestApp(t)
				t.Setenv(backgroundTranscriptDebugEnv, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const secret = "private-update-target-marker"
				id := secret
				switch failure {
				case "invalid":
					id = "../" + secret
				case "unreadable":
					dir, err := memoryDir()
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(filepath.Join(dir, id+".md"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				response := &mcMemoryGenerationResponse{Title: "Updated preference", Dimension: "user_insight", MemoryPoints: []string{"Keeps the original preference"}}
				if _, action, err := (&MemoryGenerator{app: app}).updateMemoryFileDetailed(app.ctx, id, memorySourceConversation, "fallback-source", response, ""); err != nil || action != BackgroundMaterializationCreated {
					t.Fatalf("fallback changed: action=%s err=%v", action, err)
				}
				assertActivityLogContentVisibility(t, output.String(), flag == "1", secret)
			})
		}
	}
}

// The preparation pass runs before the final write. It must see the same read
// provenance as the write, or its optional semantic probe sends the statements
// and evidence summaries of an app that is now sensitive to the model.
func TestShadowLearningPreparationWithholdsSensitiveEvidenceFromTheProbe(t *testing.T) {
	for _, sensitive := range []bool{false, true} {
		name := "readable source reaches the probe"
		if sensitive {
			name = "sensitive source stays local"
		}
		t.Run(name, func(t *testing.T) {
			app := newMCPReadTestApp(t)
			at := seedMCPReadActivity(t, app, "prepared-source", "Editor", "com.example.editor", "ordinary", "", "")
			input := taskEpisodeFixture("prepared-episode", at, at+1000, TaskEpisodeStatusCandidate)
			input.FrozenContext.Manifest = []TaskContextManifestEntry{{SourceKind: "activity_session", SourceID: "prepared-source", Included: true}}
			input.InitialEvidence[0].SourceID = "prepared-source"
			if _, _, err := app.activity.store.CreateTaskEpisode(input); err != nil {
				t.Fatal(err)
			}
			candidate := ShadowTaskLearningCandidate{TargetKind: ShadowTaskLearningTargetMemory, ProjectionKey: "prepared-memory", Statement: "shadowpreparationneedle planning", Evidence: []ShadowTaskLearningProvenance{{TaskEpisodeID: input.ID}}}
			var calls atomic.Int32
			app.memoryCanonicalizer = func(string, string) (string, error) {
				calls.Add(1)
				return `{"version":"isoform-memory-semantics-v1","status":"no_facts","facts":[]}`, nil
			}
			if sensitive {
				previous := pii.GetUserSensitiveApps()
				t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
				pii.SetUserSensitiveApps(map[string]string{"com.example.editor": "Editor"})
			}
			_ = app.prepareShadowTaskLearningMemory(candidate, at)
			if sensitive && calls.Load() != 0 {
				t.Fatalf("a sensitive source reached the semantic probe %d times", calls.Load())
			}
			if !sensitive && calls.Load() != 1 {
				t.Fatalf("the readable fixture never reached the probe (%d calls), so the test proves nothing", calls.Load())
			}
		})
	}
}
