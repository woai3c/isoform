package main

import "testing"

func TestForgottenMemoryEmbeddingCountMatchesRemoval(t *testing.T) {
	for _, tc := range []struct {
		name             string
		sharedPath       string
		sharedFrom       MemorySource
		shareOld         bool
		batch            bool
		batchIndexedOnly bool
		wantCount        int
	}{
		{name: "unshared cache", wantCount: 4},
		{name: "another memory shares current cache", sharedPath: "retained.md", sharedFrom: MemorySourceMemory, wantCount: 2},
		{name: "another source shares current cache at same path", sharedPath: "forgotten.md", sharedFrom: MemorySourceSessions, wantCount: 2},
		{name: "another memory shares history cache", sharedPath: "retained.md", sharedFrom: MemorySourceMemory, shareOld: true, wantCount: 3},
		{name: "batch removes internally shared cache once", batch: true, wantCount: 5},
		{name: "batch retains externally shared cache", batch: true, sharedPath: "retained.md", sharedFrom: MemorySourceMemory, wantCount: 3},
		{name: "batch retains cache shared by another source", batch: true, sharedPath: "derived.md", sharedFrom: MemorySourceSessions, wantCount: 3},
		{name: "batch includes older index-only hashes", batch: true, batchIndexedOnly: true, wantCount: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := newForgetTestApp(t)
			db := app.memoryStore.db
			const path = "forgotten.md"
			contents := []string{"# Current\n\nA current preference.", "# Previous\n\nA previous preference."}
			hashes := make([]string, len(contents))
			for i, content := range contents {
				chunks := chunkMarkdown(content, memoryChunkTokens, memoryChunkOverlap)
				if len(chunks) != 1 {
					t.Fatalf("fixture has %d chunks, want 1", len(chunks))
				}
				hashes[i] = chunks[0].Hash
				if _, err := db.Exec(`INSERT INTO memory_embedding_cache(provider, model, provider_key, hash, embedding)
					VALUES('test', 'test', 'test', ?, '[1,0]')`, hashes[i]); err != nil {
					t.Fatal(err)
				}
			}
			// A second provider is a separate cache row for the same current chunk.
			if _, err := db.Exec(`INSERT INTO memory_embedding_cache(provider, model, provider_key, hash, embedding)
				VALUES('other', 'test', 'other', ?, '[1,0]')`, hashes[0]); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO memory_chunks(id, path, source, start_line, end_line, hash, text, embedding)
				VALUES('forgotten', ?, ?, 1, 3, ?, ?, '[1,0]')`, path, string(MemorySourceMemory), hashes[0], contents[0]); err != nil {
				t.Fatal(err)
			}
			var additionalPaths []string
			if tc.batch {
				// Duplicate scope paths must not inflate either count.
				additionalPaths = []string{"derived.md", path, "derived.md"}
				derivedHash := hashes[0]
				if tc.batchIndexedOnly {
					derivedHash = "old-index-hash"
					if _, err := db.Exec(`INSERT INTO memory_embedding_cache(provider, model, provider_key, hash, embedding)
						VALUES('test', 'test', 'test', ?, '[1,0]')`, derivedHash); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := db.Exec(`INSERT INTO memory_chunks(id, path, source, start_line, end_line, hash, text, embedding)
					VALUES('derived', 'derived.md', ?, 1, 3, ?, ?, '[1,0]')`, string(MemorySourceMemory), derivedHash, contents[0]); err != nil {
					t.Fatal(err)
				}
			}
			if tc.sharedPath != "" {
				sharedIndex := 0
				if tc.shareOld {
					sharedIndex = 1
				}
				if _, err := db.Exec(`INSERT INTO memory_chunks(id, path, source, start_line, end_line, hash, text, embedding)
					VALUES('retained', ?, ?, 1, 3, ?, ?, '[1,0]')`, tc.sharedPath, string(tc.sharedFrom), hashes[sharedIndex], contents[sharedIndex]); err != nil {
					t.Fatal(err)
				}
			}
			totalEmbeddings := func() int {
				t.Helper()
				var total int
				if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM memory_chunks WHERE embedding != '') +
					(SELECT COUNT(*) FROM memory_embedding_cache)`).Scan(&total); err != nil {
					t.Fatal(err)
				}
				return total
			}
			before := totalEmbeddings()
			previewCount, err := countForgottenMemoryEmbeddings(db, path, contents, additionalPaths...)
			if err != nil {
				t.Fatal(err)
			}
			if previewCount != tc.wantCount {
				t.Errorf("preview count = %d, want %d for the complete removal scope", previewCount, tc.wantCount)
			}
			if err := removeForgottenMemoryIndex(db, path, contents, additionalPaths...); err != nil {
				t.Fatal(err)
			}
			if removed := before - totalEmbeddings(); removed != tc.wantCount || removed != previewCount {
				t.Errorf("removed %d embeddings, want %d and preview count %d", removed, tc.wantCount, previewCount)
			}
			if tc.sharedPath != "" {
				var retained int
				if err := db.QueryRow(`SELECT COUNT(*) FROM memory_chunks WHERE id = 'retained'`).Scan(&retained); err != nil || retained != 1 {
					t.Errorf("retained chunk count = %d, err = %v", retained, err)
				}
			}
		})
	}
}
