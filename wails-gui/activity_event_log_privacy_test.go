package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var semanticPrivateLogSites = []privateDiagnosticLogSite{
	{"activity_event_stitch.go", "runSemanticPass", "load_pending"},
	{"activity_event_stitch.go", "runSemanticPass", "mark_disabled_done"},
	{"activity_event_stitch.go", "markGroupSemanticDone", "mark_group_done"},
	{"activity_event_stitch.go", "runSemanticEventExtraction", "panic"},
	{"activity_event_stitch.go", "runSemanticEventExtraction", "extraction_rejected"},
	{"activity_event_stitch.go", "runSemanticEventExtraction", "extraction_retry"},
	{"activity_event_stitch.go", "runSemanticEventExtraction", "coherence_rejected"},
	{"activity_event_stitch.go", "runSemanticEventExtraction", "coherence_retry"},
	{"activity_event_stitch.go", "buildGroupObservationSequence", "load_snapshots"},
	{"activity_event_stitch.go", "stitchSemanticEvents", "load_open"},
	{"activity_event_stitch.go", "stitchSemanticEvents", "update"},
	{"activity_event_stitch.go", "stitchSemanticEvents", "insert"},
	{"activity_event_stitch.go", "settleStaleSemanticEvents", "settle"},
}

func TestSemanticExtractionFailureSitesUsePrivacyBoundary(t *testing.T) {
	// Legacy semantic extraction is off in production, but its retained
	// extractor/coherence sinks must remain safe if used by a future caller.
	// People vision still reaches the pending/group/snapshot boundaries.
	assertPrivateDiagnosticLogSites(t, "logSemanticEventError", semanticPrivateLogSites)
}

func TestSemanticExtractionFailureLogsKeepContentPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		for _, site := range semanticPrivateLogSites {
			t.Run(site.stage+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const secret = "private-extraction-error-amber"
				const tail = "private-extraction-tail-amber"
				logSemanticEventError(site.stage, "source-id", errors.New("i/o timeout: "+secret+strings.Repeat("x", 300)+tail))
				line := contentLogLine(t, output.String(), "[semantic-events] stage="+site.stage)
				assertActivityLogContentVisibility(t, line, flag == "1", secret)
				if !strings.Contains(line, "id=source-id") || !strings.Contains(line, "error_kind=timeout") || !strings.Contains(line, "error_bytes=") {
					t.Fatalf("missing operational metadata: %s", line)
				}
				if strings.Contains(line, tail) {
					t.Fatalf("unbounded error preview: %s", line)
				}
			})
		}
	}
}

func TestSemanticExtractionModelErrorsKeepResponsesPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		for _, operation := range []string{"extraction", "coherence"} {
			for _, failure := range []string{"parse", "timeout", "rejected"} {
				t.Run(operation+"/"+failure+"/debug="+flag, func(t *testing.T) {
					setContentLogDebugFlag(t, flag)
					output, _ := isolateActivityPrivacyLogs(t)
					const secret = "private-semantic-model-response-amber"
					app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{response: `{"events":[{"title":"` + secret + `"`})
					kind, suffix := "parse", "retry"
					switch failure {
					case "timeout":
						app.runner = &suggestionErrorPrivacyRunner{err: errors.New("provider timeout: " + secret)}
						kind = "timeout"
					case "rejected":
						app.runner = &suggestionErrorPrivacyRunner{err: errors.New("HTTP 400 invalid_request: " + secret)}
						kind, suffix = "invalid", "rejected"
					}
					triager := NewActivityTriager(app, nil)
					seq := ObservationSequence{Observations: []Observation{{Index: 0, RealApp: "test", Body: "test source"}}}
					var err error
					switch operation {
					case "extraction":
						_, err = triager.extractSemanticEvents(context.Background(), seq)
					case "coherence":
						_, err = triager.coherenceVerify(context.Background(), seq)
					}
					if err == nil || !strings.Contains(err.Error(), secret) {
						t.Fatalf("fixture lost canonical model error: %v", err)
					}
					if isPermanentExtractionFailure(err) != (failure == "rejected") {
						t.Fatalf("error classification changed: %v", err)
					}
					// The top-level legacy orchestration is compile-time disabled.
					// Its individual routing sites are checked above; exercise the
					// same sink with errors returned by both real model parsers here.
					stage := operation + "_" + suffix
					logSemanticEventError(stage, "source-id", err)
					line := contentLogLine(t, output.String(), "[semantic-events] stage="+stage)
					assertActivityLogContentVisibility(t, line, flag == "1", secret)
					if !strings.Contains(line, "error_kind="+kind) {
						t.Fatalf("missing error kind %s: %s", kind, line)
					}
				})
			}
		}
	}
}

func TestSemanticCoherenceLogsKeepTitlesPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		for _, site := range []string{"budget", "unusable", "verified", "restored"} {
			t.Run(site+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const title = "private-coherence-title-amber"
				const detail = "private-coherence-error-amber"
				seq := annotated(coObs(0, "cmux", "github.com/Isoform/first work", ""), coObs(1, "Chrome", "", "https://github.com/Isoform/second"))
				event := extractedEvent{Title: title, Category: "build", SourceIndices: []int{0, 1}}
				budget := &coherenceBudget{remaining: 1}
				verify := func(context.Context, ObservationSequence) ([]extractedEvent, error) {
					if site == "unusable" {
						return nil, errors.New("HTTP 400 invalid_request: " + detail)
					}
					if site == "restored" {
						return []extractedEvent{{Title: "first task", SourceIndices: []int{0}}}, nil
					}
					return []extractedEvent{{Title: "first task", SourceIndices: []int{0}}, {Title: "second task", SourceIndices: []int{1}}}, nil
				}
				prefix := "[semantic-events] coherence verified"
				secrets := []string{}
				switch site {
				case "budget":
					budget.remaining = 0
					prefix = "[semantic-events] coherence verify budget exhausted"
				case "unusable":
					prefix = "[semantic-events] coherence verify unusable"
					secrets = append(secrets, detail)
				case "restored":
					prefix = "[semantic-events] coherence safety net"
				}
				result, err := applyCoherence(context.Background(), []extractedEvent{event}, seq, budget, verify)
				if err != nil || len(result) != 2 {
					t.Fatalf("coherence lost observations: result=%+v error=%v", result, err)
				}
				line := contentLogLine(t, output.String(), prefix)
				assertActivityLogContentVisibility(t, line, false, title)
				assertActivityLogContentVisibility(t, line, flag == "1", secrets...)
				if !strings.Contains(line, "title_bytes=") {
					t.Fatalf("missing title size: %s", line)
				}
			})
		}
	}
}
