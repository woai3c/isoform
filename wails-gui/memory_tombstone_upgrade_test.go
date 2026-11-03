package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This is the shape written by feat/iphone-companion: no version property,
// and keys use the old id/body/point/source namespaces.
func writeFeatMemoryTombstone(t *testing.T, dir string, expiry time.Time) []byte {
	t.Helper()
	data, err := json.Marshal([]struct {
		Keys      []string `json:"keys"`
		ExpiresAt int64    `json:"expiresAt"`
	}{{Keys: []string{
		memoryForgetHash("id:legacy-memory"),
		memoryForgetHash("body:" + strings.Join(NormalizeForMatch("A private legacy paragraph."), " ")),
		memoryForgetHash("point:" + strings.Join(NormalizeForMatch("Prefers dark mode."), " ")),
		memoryForgetHash("source:shared-legacy-session"),
	}, ExpiresAt: expiry.UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "version") {
		t.Fatal("upgrade fixture must match the unversioned feat format")
	}
	if err := os.WriteFile(filepath.Join(dir, memoryTombstonesFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMemoryTombstoneUpgradeReadsFeatMarkers(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	expiry := now.Add(24 * time.Hour)
	writeFeatMemoryTombstone(t, dir, expiry)
	for _, tt := range []struct {
		name, id, content string
		forgotten         bool
	}{
		{"deleted identity", "legacy-memory", "# Changed wording\n\nA different body.", true},
		{"legacy body", "new-observation", "# A different heading\n\nA private legacy paragraph.", true},
		{"legacy point in larger record", "new-observation", "# Display settings\n\n## Memory Points\n\n- Prefers dark mode.\n- Uses a second monitor.", true},
		{"unrelated shared session", "unrelated", "# Calendar\n\n- **Activity Session**: shared-legacy-session\n\nReviews the calendar each morning.", false},
		{"explicit edit", "user-choice", "# Display\n\n- **Source**: user_edit\n\n## Memory Points\n\n- Prefers dark mode.", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			forgotten, err := isMemoryForgotten(dir, tt.id, tt.content, now)
			if err != nil || forgotten != tt.forgotten {
				t.Fatalf("feat marker match = %v, %v; want %v without upgrade error", forgotten, err, tt.forgotten)
			}
		})
	}
	forgotten, err := isMemoryForgotten(dir, "legacy-memory", "# Changed wording", expiry.Add(time.Millisecond))
	if err != nil || forgotten {
		t.Fatalf("legacy expiry changed: forgotten=%v err=%v", forgotten, err)
	}
}

func TestMemoryTombstoneUpgradeAllowsAnotherForgetAndRetainsLegacyProtection(t *testing.T) {
	app, dir := newForgetTestApp(t)
	if _, err := app.saveMemoryEntry("new-forget", "# New memory\n\nI prefer quiet meeting rooms.", false); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(24 * time.Hour)
	before := writeFeatMemoryTombstone(t, dir, expiry)
	preview, err := app.GetMemoryForgetPreview("new-forget")
	if err != nil {
		t.Fatalf("legacy file prevents preview: %v", err)
	}
	if err := app.ForgetMemoryEntry("new-forget", preview.ConfirmationToken); err != nil {
		t.Fatalf("legacy file prevents forgetting another record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new-forget.md")); !os.IsNotExist(err) {
		t.Fatalf("selected record still exists: %v", err)
	}
	var previous []memoryTombstone
	if err := json.Unmarshal(before, &previous); err != nil {
		t.Fatal(err)
	}
	after, err := loadMemoryTombstones(dir)
	if err != nil || len(after) != 2 {
		t.Fatalf("migrated markers = %#v, %v; want old and new", after, err)
	}
	var legacy *memoryTombstone
	for i := range after {
		if memoryTombstoneHasKey(after[i], memoryForgetIDKey("legacy-memory")) {
			legacy = &after[i]
		}
	}
	if legacy == nil || legacy.Version != 1 || legacy.ExpiresAt != previous[0].ExpiresAt || strings.Join(legacy.Keys, ",") != strings.Join(previous[0].Keys, ",") {
		t.Fatalf("migration lost or extended legacy protection: %#v", legacy)
	}
	stored, err := os.ReadFile(filepath.Join(dir, memoryTombstonesFile))
	if err != nil {
		t.Fatal(err)
	}
	var persisted []struct {
		Version *int     `json:"version"`
		Keys    []string `json:"keys"`
	}
	if err := json.Unmarshal(stored, &persisted); err != nil {
		t.Fatal(err)
	}
	migrated := false
	for _, marker := range persisted {
		for _, key := range marker.Keys {
			if key == memoryForgetIDKey("legacy-memory") && marker.Version != nil && *marker.Version == 1 {
				migrated = true
			}
		}
	}
	if !migrated {
		t.Fatalf("next marker write did not persist the version migration: %s, %v", stored, err)
	}
	if err := checkMemoryWriteAllowed(dir, "legacy-memory", "# A rewritten observation", true); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("legacy deleted identity lost suppression: %v", err)
	}
}
