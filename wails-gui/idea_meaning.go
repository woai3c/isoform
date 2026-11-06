package main

import (
	"context"
	"crypto/sha256"
	"math"
	"sync"
	"time"
)

// One card per idea by meaning (design review v1.0.8 W40, Y33). The words of
// two titles often differ while the idea is the same ("Approves disk cleanup
// only after…" and "You give explicit scope… when approving destructive
// actions"), so titles are compared with the memory store's embedder when
// the app has one. Without an embedder it returns nothing and the page falls
// back to its word matcher.

// ideaMeaningThreshold is the cosine similarity at which two titles mean the
// same idea. It is tuned on the local MiniLM embedder against the belief titles
// of one user (see the PR for the pairs it was read against).
const ideaMeaningThreshold = 0.8

// ideaMeaningMaxCache bounds the in-memory vector cache; it is cleared when full.
const ideaMeaningMaxCache = 20000

var ideaVectors = struct {
	sync.Mutex
	byHash map[[sha256.Size]byte][]float64
}{byHash: map[[sha256.Size]byte][]float64{}}

// IdeaMeaningGroups returns, for each title in the given order, the index of
// the meaning group it joins. Earlier titles head their groups, so pass the
// newest first. It returns nil when the memory store has no embedder.
func (a *App) IdeaMeaningGroups(titles []string) (perfValue []int, perfErr error) {
	defer logPageBinding("IdeaMeaningGroups", time.Now(), &perfValue)
	if a == nil || a.memoryStore == nil || a.memoryStore.embedder == nil || len(titles) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vectors, err := embedIdeaTitles(ctx, a.memoryStore.embedder, titles)
	if err != nil {
		return nil, err
	}
	return clusterIdeaMeanings(vectors), nil
}

// embedIdeaTitles returns one unit-length vector per title, embedding only the
// titles it has not seen in this process.
func embedIdeaTitles(ctx context.Context, embedder EmbeddingProvider, titles []string) ([][]float64, error) {
	out := make([][]float64, len(titles))
	var missing []int
	ideaVectors.Lock()
	for i, title := range titles {
		if vec, ok := ideaVectors.byHash[sha256.Sum256([]byte(title))]; ok {
			out[i] = vec
		} else {
			missing = append(missing, i)
		}
	}
	ideaVectors.Unlock()
	for start := 0; start < len(missing); start += 64 {
		end := min(start+64, len(missing))
		texts := make([]string, 0, end-start)
		for _, i := range missing[start:end] {
			texts = append(texts, titles[i])
		}
		vecs, err := embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(texts) {
			return nil, errIdeaMeaningShape
		}
		ideaVectors.Lock()
		if len(ideaVectors.byHash) >= ideaMeaningMaxCache {
			clear(ideaVectors.byHash)
		}
		for k, i := range missing[start:end] {
			unit := normalizeVector(vecs[k])
			out[i] = unit
			ideaVectors.byHash[sha256.Sum256([]byte(titles[i]))] = unit
		}
		ideaVectors.Unlock()
	}
	return out, nil
}

var errIdeaMeaningShape = errorString("idea meaning: embedder returned a different count")

type errorString string

func (e errorString) Error() string { return string(e) }

func normalizeVector(v []float64) []float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return v
	}
	scale := 1 / math.Sqrt(sum)
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x * scale
	}
	return out
}

// clusterIdeaMeanings assigns each vector to the first group whose head it
// matches, else starts a group. Vectors are unit length, so a dot product is
// the cosine similarity.
func clusterIdeaMeanings(vectors [][]float64) []int {
	out := make([]int, len(vectors))
	var heads []int
	for i, v := range vectors {
		group := -1
		for g, h := range heads {
			if dot(vectors[h], v) >= ideaMeaningThreshold {
				group = g
				break
			}
		}
		if group < 0 {
			heads = append(heads, i)
			group = len(heads) - 1
		}
		out[i] = group
	}
	return out
}

func dot(a, b []float64) float64 {
	var sum float64
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}
