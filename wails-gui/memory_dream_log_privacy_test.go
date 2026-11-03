package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// These are the failure boundaries that can receive provider output or paths.
// Keep their routing checked as well as the formatter: removing redaction from
// one call site must fail even when the common formatter is still correct.
type privateDiagnosticLogSite struct{ file, function, stage string }

var dreamPrivateLogSites = []privateDiagnosticLogSite{
	{"memory_dream.go", "scheduleTick", "scheduled_cycle"},
	{"memory_dream.go", "executeCycle", "archive_exact"},
	{"memory_dream.go", "executeCycle", "archive_expired"},
	{"memory_dream.go", "executeCycle", "purge_archive"},
	{"memory_dream.go", "gatherCandidates", "load_embeddings"},
	{"memory_dream.go", "runSimilarityLayers", "archive_similar"},
	{"memory_dream.go", "runL3Merge", "merge_model"},
	{"memory_dream.go", "runL3Merge", "write_synthesis"},
	{"memory_dream.go", "runL3Merge", "archive_merge"},
	{"memory_dream_archive.go", "restoreArchivedMemory", "restore_remove_archive"},
	{"memory_dream_archive.go", "undoDreamRun", "undo_restore"},
}

func TestMemoryDreamFailureSitesUsePrivacyBoundary(t *testing.T) {
	assertPrivateDiagnosticLogSites(t, "logMemoryDreamError", dreamPrivateLogSites)
}

func assertPrivateDiagnosticLogSites(t *testing.T, boundary string, sites []privateDiagnosticLogSite) {
	t.Helper()
	files := make(map[string]*ast.File)
	for _, site := range sites {
		t.Run(site.stage, func(t *testing.T) {
			file := files[site.file]
			if file == nil {
				var err error
				file, err = parser.ParseFile(token.NewFileSet(), site.file, nil, 0)
				if err != nil {
					t.Fatal(err)
				}
				files[site.file] = file
			}
			matches := 0
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != site.function {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || len(call.Args) != 3 {
						return true
					}
					name, ok := call.Fun.(*ast.Ident)
					if !ok || name.Name != boundary {
						return true
					}
					literal, ok := call.Args[0].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Error("Diagnostic failure kind must be a fixed literal, not source text")
						return true
					}
					stage, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatal(err)
					}
					if stage == site.stage {
						matches++
					}
					return true
				})
			}
			if matches != 1 {
				t.Fatalf("%s.%s must route %s through the private diagnostic boundary exactly once; got %d", site.file, site.function, site.stage, matches)
			}
		})
	}
}

func TestMemoryDreamFailureLogsKeepErrorContentPrivate(t *testing.T) {
	// Every Dream failure site uses this common diagnostic boundary. Exercise
	// its fixed operation kinds separately so field and preview regressions
	// identify the affected stage, including archive restore and undo.
	for _, flag := range []string{"unset", "0", "1"} {
		for _, site := range dreamPrivateLogSites {
			stage := site.stage
			t.Run(stage+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const secret = "private-dream-error-amber"
				const tail = "private-dream-unbounded-tail"
				err := errors.New("i/o timeout: " + secret + strings.Repeat("x", 300) + tail)
				logMemoryDreamError(stage, "memory-id", err)
				line := contentLogLine(t, output.String(), "[dream] stage="+stage)
				assertActivityLogContentVisibility(t, line, flag == "1", secret)
				if !strings.Contains(line, "id=memory-id") || !strings.Contains(line, "error_kind=timeout") || !strings.Contains(line, "error_bytes=") {
					t.Fatalf("missing operational metadata: %s", line)
				}
				if strings.Contains(line, tail) {
					t.Fatalf("debug preview is unbounded: %s", line)
				}
			})
		}
	}
}

func TestMemoryDreamMergeProviderFailureKeepsResponsePrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			const secret = "private-dream-provider-response-amber"
			app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{})
			app.runner = &suggestionErrorPrivacyRunner{err: errors.New("provider timeout: " + secret)}
			service := NewMemoryDreamService(app)
			preview := &DreamPreview{}
			run := &DreamRun{ID: "dream-log-test"}
			cluster := []DreamMemoryCandidate{{ID: "memory-one", Content: "source one"}, {ID: "memory-two", Content: "source two"}}
			// Current Dream revalidates the captured inputs against files before dispatch.
			for _, candidate := range cluster {
				writePrivacyMemory(t, candidate.ID, candidate.Content)
			}
			if err := service.runL3Merge(run, cluster, true, preview); err != nil {
				t.Fatal(err)
			}
			line := contentLogLine(t, output.String(), "[dream] stage=merge_model")
			assertActivityLogContentVisibility(t, line, flag == "1", secret)
			if !strings.Contains(line, "error_kind=timeout") || !strings.Contains(line, "error_bytes=") {
				t.Fatalf("missing provider error metadata: %s", line)
			}
			if len(preview.ArchiveProposed) != 0 || len(preview.SynthesisProposed) != 0 || run.SynthesizedCreated != 0 {
				t.Fatal("failed provider response produced memory changes")
			}
		})
	}
}
