package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureEntry(remote string) Entry {
	data := []byte("a real fixture model\n")
	n := int64(len(data))
	sha := sha256Bytes(data)
	return Entry{ID: "fixture", Job: "live", DisplayName: "Fixture", Licence: Licence{Name: "MIT"}, Recommended: true, SizeBytes: &n, Files: []File{{Path: "fixture/model.bin", SHA256: &sha, Bytes: &n, URLs: []string{remote}}}, Sources: []Source{{Kind: "mirror", URL: "https://model-assets.yansu.app/"}}}
}
func newFixtureManager(t *testing.T, e Entry, free int64) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := New(filepath.Join(dir, "models"), dir, []Entry{e}, func(string) (int64, error) { return free, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}
func waitState(t *testing.T, m *Manager, state string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		jobs := m.Downloads()
		if len(jobs) > 0 && jobs[0].State == state {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("wanted %s: %+v", state, jobs)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func TestShippedManifestSchemaAndEvidence(t *testing.T) {
	entries := Catalog()
	if len(entries) < 12 {
		t.Fatalf("catalog missing models: %d", len(entries))
	}
	source, err := os.ReadFile("../../asr-eval/RESULTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(source) != Evaluation() {
		t.Fatal("embedded evaluation drifted from source")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.ID] = true
		if strings.Contains(e.Evidence, "asr-eval/RESULTS.md") {
			for _, line := range strings.Split(e.Evidence, "\n")[1:] {
				_, row, ok := strings.Cut(line, ": ")
				if ok && !strings.Contains(string(source), row) {
					t.Fatalf("retyped evidence: %s", e.ID)
				}
			}
		}
	}
	for _, id := range []string{"sensevoice", "speaker-pack", "qwen3-asr-0.6b", "qwen3-asr-1.7b", "kokoro-en", "melotts-zh-en", "gliner-pii", "minilm", "openai-privacy-filter", "parakeet-tdt-v2", "whisper-large-v3-turbo", "mimo-v2.5-asr", "none"} {
		if !seen[id] {
			t.Errorf("missing %s", id)
		}
	}
}
func TestFixtureChecksumAndSchema(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	if err := Validate([]Entry{e}); err != nil {
		t.Fatal(err)
	}
	sum := sha256Bytes([]byte("a real fixture model\n"))
	if *e.Files[0].SHA256 != sum {
		t.Fatal("fixture checksum")
	}
	e.Files[0].Path = "../escape"
	if Validate([]Entry{e}) == nil {
		t.Fatal("path traversal allowed")
	}
}
func TestPlanTotalsSpaceAndConfirmation(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	m := newFixtureManager(t, e, *e.SizeBytes-1)
	p, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalBytes != *e.SizeBytes || p.EnoughSpace || len(p.Items) != 1 || p.Items[0].FileCount != 1 {
		t.Fatalf("bad plan %+v", p)
	}
	if err := m.Start(p); err == nil {
		t.Fatal("started without disk space")
	}
	if _, err := m.BuildPlan(map[string]string{"live": "not-in-manifest"}, nil); err == nil {
		t.Fatal("unlisted choice")
	}
	if err := m.Start(Plan{Items: []PlanItem{{ID: e.ID}}}); err == nil {
		t.Fatal("unreviewed plan")
	}
	m.free = func(string) (int64, error) { return 1000, nil }
	p, err = m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	copy := p
	raw, _ := json.Marshal(p)
	_ = json.Unmarshal(raw, &copy)
	copy.Items[0].Host = "other.example"
	if m.Start(copy) == nil {
		t.Fatal("mutated plan accepted")
	}
	if len(m.Downloads()) != 0 {
		t.Fatal("planning queued downloads")
	}
}
func TestDownloadChecksumFailureAndRetry(t *testing.T) {
	var corrupt atomic.Bool
	corrupt.Store(true)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if corrupt.Load() {
			fmt.Fprint(w, "x real fixture model\n")
		} else {
			fmt.Fprint(w, "a real fixture model\n")
		}
	}))
	defer server.Close()
	m := newFixtureManager(t, fixtureEntry(server.URL), 1000)
	p, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(p); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "failed")
	if !strings.Contains(m.Downloads()[0].Error, "checksum") {
		t.Fatalf("%+v", m.Downloads())
	}
	if _, err := os.Stat(filepath.Join(m.folder, "fixture/model.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published corrupt bytes")
	}
	corrupt.Store(false)
	if err := m.Retry("fixture"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "done")
	if requests.Load() != 2 {
		t.Fatalf("requests=%d", requests.Load())
	}
	if err := m.Retry("unlisted"); err == nil {
		t.Fatal("unlisted retry allowed")
	}
	choices, err := m.GetChoices()
	if err != nil || !choices.Jobs[0].Alternatives[0].Downloaded {
		t.Fatalf("not downloaded: %+v %v", choices, err)
	}
}
func TestDownloadResume(t *testing.T) {
	data := "a real fixture model\n"
	var ranges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=7-" || r.Header.Get("If-Range") != "\"v1\"" {
			t.Errorf("resume headers: %+v", r.Header)
		}
		ranges.Add(1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 7-%d/%d", len(data)-1, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		fmt.Fprint(w, data[7:])
	}))
	defer server.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(dest+".partial", []byte(data[:7]), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(dest+".partial.validator", []byte("\"v1\""), 0600)
	if err := transfer(context.Background(), server.URL, dest, sha256Bytes([]byte(data)), int64(len(data)), func(int64) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(dest)
	if string(raw) != data || ranges.Load() != 1 {
		t.Fatalf("resume failed: %q", raw)
	}
}
func TestExistingInstallAndRemovalNeverDownloads(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	e := fixtureEntry(server.URL)
	e.Files[0].Path = "sherpa/fixture/model.bin"
	m := newFixtureManager(t, e, 1000)
	p := filepath.Join(m.legacy, e.Files[0].Path)
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	_ = os.WriteFile(p, []byte("a real fixture model\n"), 0644)
	c, err := m.GetChoices()
	if err != nil || !c.Jobs[0].Alternatives[0].Downloaded {
		t.Fatal("legacy install missed", err)
	}
	plan, err := m.BuildPlan(nil, nil)
	if err != nil || len(plan.Items) != 0 || plan.TotalBytes != 0 {
		t.Fatal("legacy re-download", err)
	}
	if err := m.Remove(e.ID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("recognition or remove accessed network")
	}
}
func TestConfirmedJobsSurviveRestart(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	m := newFixtureManager(t, e, 1000)
	m.mu.Lock()
	m.jobs[e.ID] = &savedJob{Download: Download{ID: e.ID, State: "downloading", BytesTotal: *e.SizeBytes}, Source: e.Sources[0], Fingerprint: fingerprint(e)}
	m.order = []string{e.ID}
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next, err := New(m.folder, m.legacy, []Entry{e}, m.free, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Downloads()[0].State != "queued" {
		t.Fatal("confirmed interrupted job not resumable")
	}
	e.Line = "new manifest"
	changed, err := New(m.folder, m.legacy, []Entry{e}, m.free, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer changed.Close()
	if len(changed.Downloads()) != 0 {
		t.Fatal("old confirmation authorized changed manifest")
	}
}

func TestArchiveInstallChecksEveryFileAndReplacesIncompleteDirectory(t *testing.T) {
	raw, err := os.ReadFile("testdata/model.tar.bz2")
	if err != nil {
		t.Fatal(err)
	}
	if sha256Bytes(raw) != "737906497b5ae242cff8d4a6646b491c727340d189c9383479b13bfc7e0365cf" {
		t.Fatal("archive fixture checksum changed")
	}
	data := []byte("a real fixture model\n")
	archive := &Archive{Format: "tar.bz2", Root: "fixture", Destination: "fixture", Files: []InstalledFile{{Path: "model.bin", Bytes: int64(len(data)), SHA256: sha256Bytes(data)}}}
	folder := t.TempDir()
	_ = os.MkdirAll(filepath.Join(folder, "fixture"), 0755)
	_ = os.WriteFile(filepath.Join(folder, "fixture", "incomplete"), []byte("old"), 0644)
	if err := installArchive(context.Background(), "testdata/model.tar.bz2", folder, archive); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(folder, "fixture", "model.bin"))
	if string(got) != string(data) {
		t.Fatal("archive not installed")
	}
	archive.Files[0].Path = "../escape"
	if installArchive(context.Background(), "testdata/model.tar.bz2", t.TempDir(), archive) == nil {
		t.Fatal("unlisted archive member accepted")
	}
	archive.Files[0].Path = "model.bin"
	archive.Files[0].SHA256 = strings.Repeat("0", 64)
	if installArchive(context.Background(), "testdata/model.tar.bz2", t.TempDir(), archive) == nil {
		t.Fatal("bad member checksum accepted")
	}
}
func TestDownloadEventsGloballyThrottled(t *testing.T) {
	m := newFixtureManager(t, fixtureEntry("https://model-assets.yansu.app/fixture"), 1000)
	times := make(chan time.Time, 100)
	m.emit = func([]Download) { times <- time.Now() }
	m.mu.Lock()
	m.changedLocked()
	m.mu.Unlock()
	select {
	case <-times:
	case <-time.After(time.Second):
		t.Fatal("missing first event")
	}
	for i := 0; i < 100; i++ {
		m.mu.Lock()
		m.changedLocked()
		m.mu.Unlock()
	}
	select {
	case <-times:
		t.Fatal("progress event exceeded two per second")
	case <-time.After(350 * time.Millisecond):
	}
	select {
	case <-times:
	case <-time.After(time.Second):
		t.Fatal("trailing event lost")
	}
}

func TestCancelConfirmedDownloadKeepsPartialAndRequiresRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "21")
		w.Header().Set("ETag", "\"fixture\"")
		fmt.Fprint(w, "a real ")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	m := newFixtureManager(t, fixtureEntry(server.URL), 1000)
	plan, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(plan); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.folder, "fixture/model.bin.partial")
	deadline := time.After(time.Second)
	for {
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no partial bytes")
		case <-time.After(time.Millisecond):
		}
	}
	m.Cancel("fixture")
	waitState(t, m, "failed")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("cancel discarded resumable bytes")
	}
}

func TestArchivePlanCountsTransferFilesAndExtractionSpace(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	e.Files[0].Path = ".archives/fixture.tar.bz2"
	e.Files[0].Archive = &Archive{Format: "tar.bz2", Root: "fixture", Destination: "fixture", Files: []InstalledFile{
		{Path: "a.bin", Bytes: 21, SHA256: *e.Files[0].SHA256},
		{Path: "b.bin", Bytes: 21, SHA256: *e.Files[0].SHA256},
	}}
	m := newFixtureManager(t, e, 1000)
	plan, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].FileCount != 1 || plan.RequiredBytes != plan.TotalBytes+42 {
		t.Fatalf("archive accounting: %+v", plan)
	}
}
