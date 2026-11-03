package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The frontend verifies its actual edit/removal payloads against these same
// fixtures. Valid source IDs establish the cascade; refs are also preserved.
func TestInlineMemoryPointPayloadRetainsSourceForgetCascade(t *testing.T) {
	data, err := os.ReadFile("testdata/memory-point-envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, Content, Edited, Removed string
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		for _, action := range []struct{ name, payload string }{{"edit", fixture.Edited}, {"remove", fixture.Removed}} {
			t.Run(fixture.Name+"/"+action.name, func(t *testing.T) {
				app, dir := newForgetTestApp(t)
				app.activity = &activityMonitorState{store: newTestStore(t)}
				for id, content := range map[string]string{
					"point-source": "# Source\n\nAn observed writing preference.\n",
					"derived":      fixture.Content,
					"unrelated":    "# Independent\n\nAn independent preference.\n",
				} {
					if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
				}
				assertDerivedInPreview := func() MemoryForgetPreview {
					t.Helper()
					preview, err := app.GetMemoryForgetPreview("point-source")
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range preview.Items {
						if item.Kind == "memory" && item.ID == "derived" {
							return preview
						}
					}
					t.Fatalf("source Forget omitted derived memory: %+v", preview)
					return preview
				}
				assertDerivedInPreview() // Positive control before the edit.
				saved, err := app.SaveMemoryEntry("derived", action.payload)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(memoryDerivedSourceIDs(saved.Content), []string{"point-source"}) ||
					!reflect.DeepEqual(memoryDerivedSourceRefs(saved.Content), memoryDerivedSourceRefs(fixture.Content)) {
					t.Fatalf("point mutation lost its source envelope: %s", saved.Content)
				}
				preview := assertDerivedInPreview()
				if err := app.ForgetMemoryEntry("point-source", preview.ConfirmationToken); err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{"point-source", "derived"} {
					if _, err := os.Stat(filepath.Join(dir, id+".md")); !os.IsNotExist(err) {
						t.Errorf("forgotten %s retained: %v", id, err)
					}
				}
				if _, err := os.Stat(filepath.Join(dir, "unrelated.md")); err != nil {
					t.Fatalf("unrelated control removed: %v", err)
				}
			})
		}
	}
}
