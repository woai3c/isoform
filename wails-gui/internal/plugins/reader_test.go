package plugins

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"wails-gui/storage"
)

func TestStoredReaderPreservesValues(t *testing.T) {
	store, err := storage.New(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	values := map[string]string{
		"enabled": "true", "disabled": "false", "legacy": "1",
		"whitespace": " FALSE \n", "empty": "", "unknown": "future-format",
		"json": `{"enabled":true,"other":"preserve"}`,
	}
	keys := make([]string, 0, len(values)+1)
	for key, value := range values {
		if err := store.SetUIState(ctx, key, value); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	keys = append(keys, "missing")
	values["missing"] = ""
	got, err := ReadValues(NewStoredReader(ctx, store), keys)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, values) {
		t.Fatalf("read values = %#v, want %#v", got, values)
	}
	for key, want := range values {
		stored, err := store.GetUIState(ctx, key)
		if err != nil || stored != want {
			t.Fatalf("stored %q changed: %q, %v", key, stored, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStoredReader(ctx, store).Read("enabled"); err == nil {
		t.Fatal("storage error was swallowed")
	}
}
