package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestMobileBriefRecallRechecksMemoryAndKnowledgeSources(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(nil)
	now := time.Now().Truncate(time.Second)
	insertTestSession(t, app.activity.store, "brief-source", "done", now.Add(-time.Hour).UnixMilli())
	content := "# Protected recall title\n\n- **Updated**: " + now.Format(time.RFC3339) + "\n- **Date**: " + now.Format(time.RFC3339) + "\n- **Activity Session**: brief-source\n\nprotected-recall-payload\n"
	writePrivacyMemory(t, "private-memory", content)
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private-knowledge.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	collect := func() MobileBriefV1 {
		t.Helper()
		in := mobileBriefInput{WindowStart: now.Add(-24 * time.Hour), AsOf: now.Add(time.Minute), Location: time.UTC}
		collectMobileBriefRecall(app, &in)
		return buildMobileBriefV1(in)
	}
	if before := collect(); len(before.Sections.Learned) != 2 {
		t.Fatalf("fixture did not include memory and knowledge before protection: %+v", before.Sections.Learned)
	}
	// Neither file declares the source app in frontmatter. The live source,
	// captured before this setting, still has triage_status=done.
	pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
	brief := collect()
	if len(brief.Sections.Learned) != 0 {
		t.Fatalf("protected-derived memory or knowledge entered phone brief: %+v", brief.Sections.Learned)
	}
	encoded, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Protected recall title", "private-memory", "private-knowledge", "protected-recall-payload"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("phone brief leaked %q", forbidden)
		}
	}
}
