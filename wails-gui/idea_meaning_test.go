package main

import (
	"context"
	"testing"
	"time"
)

// fixtureEmbedder places each invented title on a fixed axis, so the test
// controls exactly which titles are alike. It counts the texts it was asked for.
type fixtureEmbedder struct {
	axes   map[string][]float64
	embedN int
}

func (f *fixtureEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, text := range texts {
		f.embedN++
		out[i] = f.axes[text]
	}
	return out, nil
}
func (f *fixtureEmbedder) EmbedQuery(ctx context.Context, text string) ([]float64, error) {
	v, _ := f.EmbedBatch(ctx, []string{text})
	return v[0], nil
}
func (f *fixtureEmbedder) Provider() string    { return "fixture" }
func (f *fixtureEmbedder) Model() string       { return "axes" }
func (f *fixtureEmbedder) ProviderKey() string { return "fixture-axes" }

func TestClusterIdeaMeaningsJoinsAlikeTitlesAtTheThreshold(t *testing.T) {
	vectors := [][]float64{normalizeVector([]float64{1, 0}), normalizeVector([]float64{0.9, 0.3}), normalizeVector([]float64{0, 1})}
	got := clusterIdeaMeanings(vectors, 0.65)
	if got[0] != got[1] || got[2] == got[0] {
		t.Fatalf("groups = %v, want the first two joined and the third apart", got)
	}
	if strict := clusterIdeaMeanings(vectors, 0.99); strict[0] == strict[1] {
		t.Fatalf("a 0.99 threshold still joined %v", strict)
	}
}

func TestIdeaMeaningGroupsPendingThenCachedAndFromDisk(t *testing.T) {
	cache := t.TempDir()
	previous := ideaMeaningCacheRoot
	ideaMeaningCacheRoot = func() (string, error) { return cache, nil }
	t.Cleanup(func() { ideaMeaningCacheRoot = previous })
	titles := []string{"Date-sensitive tests fail on base", "Known base failures", "Writes short replies"}
	embedder := &fixtureEmbedder{axes: map[string][]float64{
		titles[0]: {1, 0}, titles[1]: {0.95, 0.2}, titles[2]: {0, 1},
	}}
	app := &App{memoryStore: &MemoryStore{embedder: embedder}}

	ideaMeanings = &ideaMeaningStore{vectors: map[[32]byte][]float64{}, results: map[[32]byte][]int{}}
	first, err := app.IdeaMeaningGroups(titles, 0.65)
	if err != nil || first == nil || !first.Pending {
		t.Fatalf("first call = %+v, %v; want pending", first, err)
	}
	waitForGroups(t, app, titles, 0.65)
	if embedder.embedN != len(titles) {
		t.Fatalf("embedded %d titles, want %d", embedder.embedN, len(titles))
	}
	again := waitForGroups(t, app, titles, 0.65)
	if again[0] != again[1] || again[2] == again[0] {
		t.Fatalf("groups = %v", again)
	}
	before := embedder.embedN
	start := time.Now()
	_, _ = app.IdeaMeaningGroups(titles, 0.65)
	if time.Since(start) > 200*time.Millisecond || embedder.embedN != before {
		t.Fatalf("cached call took %v and embedded again", time.Since(start))
	}

	// A restart keeps the disk cache: no title is embedded again.
	ideaMeanings = &ideaMeaningStore{vectors: map[[32]byte][]float64{}, results: map[[32]byte][]int{}}
	restarted := waitForGroups(t, app, titles, 0.65)
	if embedder.embedN != before || restarted[0] != again[0] {
		t.Fatalf("after restart embedded %d more, groups %v", embedder.embedN-before, restarted)
	}
}

func TestIdeaMeaningGroupsRejectsAnOutOfRangeThreshold(t *testing.T) {
	app := &App{memoryStore: &MemoryStore{embedder: &fixtureEmbedder{}}}
	if r, _ := app.IdeaMeaningGroups([]string{"a"}, 0.2); r != nil {
		t.Fatalf("threshold 0.2 answered %+v", r)
	}
}

func waitForGroups(t *testing.T, app *App, titles []string, threshold float64) []int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r, err := app.IdeaMeaningGroups(titles, threshold)
		if err != nil {
			t.Fatal(err)
		}
		if r != nil && r.Groups != nil {
			return r.Groups
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("groups never arrived")
	return nil
}
