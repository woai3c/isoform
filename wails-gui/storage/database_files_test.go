package storage

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDatabaseFilesIdentifyFixtureAndWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.db")
	store, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := store.DatabaseFiles(t.Context())
	if err != nil || !reflect.DeepEqual(files, []string{path, path + "-wal"}) {
		t.Fatalf("files=%v err=%v", files, err)
	}
}
