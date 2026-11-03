package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// These documentation contracts guard the two privacy promises that must
// survive release edits and merges. They do not verify a remote PostHog project.
func TestTelemetryIPDiscardReleaseGateDocumented(t *testing.T) {
	doc := readTelemetryPrivacyContract(t, "../docs/product-telemetry.md")
	for _, promise := range []string{
		"$geoip_disable does not discard the source IP",
		"the ingest request still exposes the client's source IP",
		"## Release gate: verify IP discard before adding a key",
		"keep ISOFORM_DESKTOP_POSTHOG_KEY unset until every item below is verified",
		"- [ ] Enable **Discard client IP data**",
		"project ID, ingest host, verification time (UTC), reviewer",
		"- [ ] Inspect a stored test event",
		"$ip and ip",
		"event UUID and a redacted screenshot or query result",
		"- [ ] Record release sign-off",
	} {
		requireTelemetryPrivacyText(t, doc, promise)
	}
	release := readTelemetryPrivacyContract(t, "../.claude/skills/wails-gui-release/SKILL.md")
	requireTelemetryPrivacyText(t, release, "docs/product-telemetry.md#release-gate-verify-ip-discard-before-adding-a-key")
}

// The release key may ship only for the project and host this record signs
// off. A signed-off record must carry every piece of evidence, not placeholders.
func TestTelemetryReleaseSignOffRecordsRemoteEvidence(t *testing.T) {
	record := readTelemetryPrivacyContract(t, "../docs/hub-reviews/release-telemetry-sign-off.md")
	for _, promise := range []string{
		"SIGNED OFF", "no client code path that discards the network source IP",
		"- [x] Enable **Discard client IP data**", "Project ID: 221015", "Ingest host: https://us.i.posthog.com",
		"Verified at (UTC): 2026-10-28T", "Reviewer: isoform hub (for Bo)",
		"Event UUID: 009aaf1c-788c-4fd9-ba19-33103c4b2824",
		"$ip and ip contain no source address: confirmed", "after ingestion",
		"access-log and export retention", "Build commit/version: 1.0.1",
		"release-telemetry-evidence/discard-client-ip-data.jpg",
		"release-telemetry-evidence/stored-test-event.jpg",
		"Reopen this gate whenever the project, ingest host, proxy",
	} {
		requireTelemetryPrivacyText(t, record, promise)
	}
	if strings.Contains(record, "pending") || strings.Contains(record, "- [ ]") {
		t.Fatal("a signed-off release record must not contain pending evidence")
	}
	for _, evidence := range []string{"discard-client-ip-data.jpg", "stored-test-event.jpg"} {
		info, err := os.Stat("../docs/hub-reviews/release-telemetry-evidence/" + evidence)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() >= 1<<20 {
			t.Errorf("%s is %d bytes; evidence screenshots stay under 1 MB", evidence, info.Size())
		}
	}
}

func TestTelemetryTierCommentPreservesPrivacyContracts(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "telemetry_tier_pref.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, specification := range group.Specs {
			value := specification.(*ast.ValueSpec)
			for _, name := range value.Names {
				if name.Name != "TelemetryTierOff" {
					continue
				}
				for _, promise := range []string{
					"off prevents all telemetry network calls and enqueueing",
					"desktop PostHog sink only accepts the content-free product events",
					"only the status code, a stable error class and body length",
					"upstream error bodies are never sent",
				} {
					requireTelemetryPrivacyText(t, group.Doc.Text(), promise)
				}
				return
			}
		}
	}
	t.Fatal("telemetry tier privacy contract comment not found")
}

func readTelemetryPrivacyContract(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireTelemetryPrivacyText(t *testing.T, text, promise string) {
	t.Helper()
	normalize := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(value, "`", "")), " "))
	}
	if !strings.Contains(normalize(text), normalize(promise)) {
		t.Errorf("privacy contract is missing %q", promise)
	}
}
