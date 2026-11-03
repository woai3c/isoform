package pii

import (
	"errors"
	"testing"

	"wails-gui/models"
)

func TestCachedPIIPathsCannotBypassPinnedModelReadiness(t *testing.T) {
	dir := t.TempDir()
	ensurePIIModelMu.Lock()
	ensurePIIModelCache[dir] = &piiModelPaths{ModelFile: "cached weights", TokenizerFile: "cached tokenizer", RuntimeLib: "cached runtime"}
	ensurePIIModelMu.Unlock()
	t.Cleanup(func() { ensurePIIModelMu.Lock(); delete(ensurePIIModelCache, dir); ensurePIIModelMu.Unlock() })
	paths, err := ensurePIIModel(dir, nil)
	if paths != nil || !errors.Is(err, models.ErrNotReady) {
		t.Fatalf("cached paths bypassed verification: %+v %v", paths, err)
	}
}
