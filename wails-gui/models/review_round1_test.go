package models

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRightSizedCorruptInstallIsNeverReadyAndCanBeRepaired(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("a real fixture model\n"))
	}))
	t.Cleanup(server.Close)
	e := fixtureEntry(server.URL)
	m := newFixtureManager(t, e, 1000)
	dest := filepath.Join(m.folder, e.Files[0].Path)
	os.MkdirAll(filepath.Dir(dest), 0755)
	os.WriteFile(dest, []byte("a real fixture model\n"), 0600)
	if !presentAt(m.folder, m.legacy, e) {
		t.Fatal("valid file missed")
	}
	before, _ := os.Stat(dest)
	os.WriteFile(dest, []byte("x real fixture model\n"), 0600)
	// A caller restoring mtime cannot preserve the kernel change time.
	os.Chtimes(dest, before.ModTime(), before.ModTime())
	choices, err := m.GetChoices()
	if err != nil || choices.Jobs[0].Alternatives[0].Downloaded || recognized(dest, e.Files[0].Bytes, e.Files[0].SHA256) {
		t.Fatal("right-sized corruption accepted", err)
	}
	plan, err := m.BuildPlan(nil, nil)
	if err != nil || len(plan.Items) != 1 {
		t.Fatal("corrupt model cannot be repaired", err)
	}
	if err := m.Start(plan); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "done")
	if !presentAt(m.folder, m.legacy, e) || requests.Load() != 1 {
		t.Fatal("repair did not verify bytes")
	}
}

func TestLegacyVerifiedArchiveNeedsOnlyItsPinnedRuntimeFiles(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	runtime := []byte("runtime weights")
	archive := &Archive{Format: "tar.bz2", Destination: "sherpa/qwen/fixture", Root: "fixture", Legacy: &LegacyInstall{Marker: ".isoform-verified", Required: []string{"encoder.onnx"}}, Files: []InstalledFile{
		{Path: "encoder.onnx", Bytes: int64(len(runtime)), SHA256: sha256Bytes(runtime)},
		{Path: "README.md", Bytes: 4, SHA256: sha256Bytes([]byte("docs"))},
		{Path: "test_wavs/example.wav", Bytes: 4, SHA256: sha256Bytes([]byte("RIFF"))},
	}}
	e.Files[0].Archive = archive
	m := newFixtureManager(t, e, 1000)
	root := filepath.Join(m.legacy, archive.Destination)
	os.MkdirAll(root, 0755)
	os.WriteFile(filepath.Join(root, "encoder.onnx"), runtime, 0600)
	marker := filepath.Join(root, archive.Legacy.Marker)
	os.WriteFile(marker, []byte(*e.Files[0].SHA256+"\n"), 0600)
	if !presentAt(m.folder, m.legacy, e) {
		t.Fatal("valid legacy marker and required files ignored")
	}
	relative := archive.Destination + "/encoder.onnx"
	if got := pathForEntries(m.legacy, relative, []Entry{e}); got != filepath.Join(root, "encoder.onnx") {
		t.Fatalf("legacy loader redirected to %s", got)
	}
	plan, err := m.BuildPlan(nil, nil)
	if err != nil || len(plan.Items) != 0 {
		t.Fatal("legacy install would download again", err)
	}
	os.WriteFile(marker, []byte("wrong marker"), 0600)
	if presentAt(m.folder, m.legacy, e) {
		t.Fatal("unverified incomplete legacy archive accepted")
	}
	os.WriteFile(marker, []byte(*e.Files[0].SHA256), 0600)
	os.WriteFile(filepath.Join(root, "encoder.onnx"), []byte(strings.Repeat("x", len(runtime))), 0600)
	if presentAt(m.folder, m.legacy, e) {
		t.Fatal("marker hid corrupt runtime weights")
	}
}

func persistFixtureJob(t *testing.T, m *Manager, e Entry, state string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[e.ID] = &savedJob{Download: Download{ID: e.ID, State: state, BytesTotal: *e.SizeBytes}, Source: e.Sources[0], Fingerprint: fingerprint(e)}
	m.order = append(m.order, e.ID)
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
}
func TestRetryAndRestartRecheckSpaceBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("a real fixture model\n"))
	}))
	t.Cleanup(server.Close)
	e := fixtureEntry(server.URL)
	for _, state := range []string{"failed", "downloading"} {
		t.Run(state, func(t *testing.T) {
			m := newFixtureManager(t, e, 0)
			persistFixtureJob(t, m, e, state)
			if state == "failed" {
				if err := m.Retry(e.ID); !errors.Is(err, ErrNoSpace) {
					t.Fatalf("retry: %v", err)
				}
			} else {
				next, err := New(m.folder, m.legacy, []Entry{e}, m.free, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(next.Close)
				next.Resume()
				m = next
			}
			if jobs := m.Downloads(); jobs[0].State != "paused" || jobs[0].PauseReason != "not enough free disk space" {
				t.Fatalf("space guard: %+v", jobs)
			}
			if requests.Load() != 0 {
				t.Fatal("no-space launch issued HTTP")
			}
		})
	}
}
func TestOverlappingPlansShareReservations(t *testing.T) {
	requested := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested <- struct{}{}
		select {
		case <-release:
			w.Write([]byte("a real fixture model\n"))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	first := fixtureEntry(server.URL)
	second := fixtureEntry(server.URL)
	second.ID = "second"
	second.Files[0].Path = "second/model.bin"
	second.Recommended = false
	dir := t.TempDir()
	m, err := New(filepath.Join(dir, "models"), dir, []Entry{first, second}, func(string) (int64, error) { return *first.SizeBytes, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	t.Cleanup(func() { close(release) })
	p1, _ := m.BuildPlan(nil, nil)
	p2, _ := m.BuildPlan(map[string]string{"live": second.ID}, nil)
	if err := m.Start(p1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("first did not launch")
	}
	if err := m.Start(p2); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("overcommitted space: %v", err)
	}
	if len(m.Downloads()) != 1 {
		t.Fatal("unadmitted job was persisted")
	}
}
func TestRemainingSpaceIncludesExtractionAndResumablePrefix(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	e.Files[0].Archive = &Archive{Format: "tar.bz2", Destination: "runtime", Root: "runtime", Files: []InstalledFile{{Path: "weights", Bytes: 30, SHA256: sha256Bytes([]byte("weights"))}}}
	m := newFixtureManager(t, e, 1000)
	dest := filepath.Join(m.folder, e.Files[0].Path)
	os.MkdirAll(filepath.Dir(dest), 0755)
	os.WriteFile(dest+".partial", []byte("a real "), 0600)
	os.WriteFile(dest+".partial.validator", []byte("\"version\""), 0600)
	if got, want := m.remainingSpace(e), *e.SizeBytes-7+30; got != want {
		t.Fatalf("remaining=%d want=%d", got, want)
	}
}
func TestMeteredStartRestartChangeAndResume(t *testing.T) {
	var requests atomic.Int32
	started := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	e := fixtureEntry(server.URL)
	m := newFixtureManager(t, e, 1000)
	m.SetNetworkState(NetworkState{Known: true, Expensive: true})
	plan, _ := m.BuildPlan(nil, nil)
	if err := m.Start(plan); err != nil {
		t.Fatal(err)
	}
	if jobs := m.Downloads(); jobs[0].State != "paused" || jobs[0].PauseReason != "Paused on a metered connection" {
		t.Fatalf("metered: %+v", jobs)
	}
	if err := m.ResumeDownload(e.ID); err != nil || requests.Load() != 0 {
		t.Fatal("resume bypassed metered path", err)
	}
	next, err := New(m.folder, m.legacy, []Entry{e}, m.free, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	next.SetNetworkState(NetworkState{Known: true, Constrained: true})
	next.Resume()
	if requests.Load() != 0 {
		t.Fatal("restart bypassed constrained path")
	}
	next.SetNetworkState(NetworkState{Known: true})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("safe path did not resume confirmed job")
	}
	next.SetNetworkState(NetworkState{Known: true, Expensive: true})
	waitState(t, next, "paused")
	deadline := time.After(time.Second)
	for {
		next.mu.Lock()
		running := next.running[e.ID]
		next.mu.Unlock()
		if !running {
			break
		}
		select {
		case <-deadline:
			t.Fatal("metered transition did not cancel transfer")
		case <-time.After(time.Millisecond):
		}
	}
	if requests.Load() != 1 {
		t.Fatal("metered transition retried HTTP")
	}
	// None of these transitions can create a job without confirmation.
	fresh := newFixtureManager(t, e, 1000)
	fresh.SetNetworkState(NetworkState{Known: true})
	if err := fresh.ResumeDownload(e.ID); err == nil {
		t.Fatal("resume granted new authority")
	}
}
func TestLegacyMarkerMetadataSurvivesCatalogRefresh(t *testing.T) {
	for _, e := range Catalog() {
		if e.ID == "qwen3-asr-0.6b" {
			legacy := e.Files[0].Archive.Legacy
			if legacy == nil || len(legacy.Required) != 5 {
				t.Fatal("legacy runtime manifest missing")
			}
			raw, _ := json.Marshal(e)
			if !strings.Contains(string(raw), ".isoform-verified") {
				t.Fatal("marker lost")
			}
			return
		}
	}
	t.Fatal("Qwen absent")
}

func TestRestartSharesSpaceAcrossAllConfirmedJobs(t *testing.T) {
	var requests atomic.Int32
	requested := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		requested <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	first := fixtureEntry(server.URL)
	second := fixtureEntry(server.URL)
	second.ID = "second"
	second.Files[0].Path = "second/model.bin"
	second.Recommended = false
	dir := t.TempDir()
	m, err := New(filepath.Join(dir, "models"), dir, []Entry{first, second}, func(string) (int64, error) { return *first.SizeBytes, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	persistFixtureJob(t, m, first, "queued")
	persistFixtureJob(t, m, second, "queued")
	m.Resume()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("first confirmed job did not resume")
	}
	jobs := m.Downloads()
	if requests.Load() != 1 || jobs[1].State != "paused" || jobs[1].PauseReason != ErrNoSpace.Error() {
		t.Fatalf("restart overcommitted: %+v requests=%d", jobs, requests.Load())
	}
}
func TestUnknownPathAndCompletedPartialNeedNoUnconfirmedHTTP(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	m := newFixtureManager(t, e, 0)
	dest := filepath.Join(m.folder, e.Files[0].Path)
	os.MkdirAll(filepath.Dir(dest), 0755)
	os.WriteFile(dest+".partial", []byte("a real fixture model\n"), 0600)
	plan, err := m.BuildPlan(nil, nil)
	if err != nil || plan.RequiredBytes != 0 || !plan.EnoughSpace {
		t.Fatalf("complete verified partial needs no transfer: %+v %v", plan, err)
	}
	m.SetNetworkState(NetworkState{})
	if err := m.Start(plan); err != nil {
		t.Fatal(err)
	}
	if m.Downloads()[0].State != "paused" {
		t.Fatal("unknown path was admitted")
	}
	m.SetNetworkState(NetworkState{Known: true})
	waitState(t, m, "done")
	if !presentAt(m.folder, m.legacy, e) {
		t.Fatal("verified partial not published")
	}
}

func TestFailedConfirmationDoesNotLeaveLaunchableJobs(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("a real fixture model\n"))
	}))
	t.Cleanup(server.Close)
	e := fixtureEntry(server.URL)
	m := newFixtureManager(t, e, 1000)
	plan, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.folder, []byte("blocks state persistence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(plan); err == nil {
		t.Fatal("failed confirmation unexpectedly persisted")
	}
	m.SetNetworkState(NetworkState{Known: true})
	if len(m.Downloads()) != 0 || requests.Load() != 0 {
		t.Fatal("failed confirmation left launchable work")
	}
	if err := os.Remove(m.folder); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(plan); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "done")
}

func TestEveryLaunchReadsTheFreshNativeSnapshot(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	m := newFixtureManager(t, e, 1000)
	m.network = NetworkState{Known: true}
	var reads int
	m.networkProbe = func() NetworkState { reads++; return NetworkState{Known: true, Expensive: true} }
	plan, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(plan); err != nil {
		t.Fatal(err)
	}
	if m.Downloads()[0].State != "paused" || reads != 1 {
		t.Fatal("start trusted the old Go snapshot")
	}
	if err := m.ResumeDownload(e.ID); err != nil {
		t.Fatal(err)
	}
	m.Resume()
	if reads != 3 || m.Downloads()[0].State != "paused" {
		t.Fatal("resume skipped the fresh path read")
	}
}

func TestUnknownPathPauseChangesToTheMeteredReason(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	m := newFixtureManager(t, e, 1000)
	m.SetNetworkState(NetworkState{})
	plan, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(plan); err != nil {
		t.Fatal(err)
	}
	if m.Downloads()[0].PauseReason != "Waiting for network status" {
		t.Fatal("missing initial wait")
	}
	m.SetNetworkState(NetworkState{Known: true, Expensive: true})
	job := m.Downloads()[0]
	if job.State != "paused" || job.PauseReason != "Paused on a metered connection" {
		t.Fatalf("stale metered reason: %+v", job)
	}
	raw, err := os.ReadFile(m.statePath())
	if err != nil || !strings.Contains(string(raw), "Paused on a metered connection") {
		t.Fatal("metered reason not persisted", err)
	}
}

func TestStopCancelsBeforeWaitingForTheAdmissionMutex(t *testing.T) {
	m := newFixtureManager(t, fixtureEntry("https://model-assets.yansu.app/fixture"), 1000)
	m.mu.Lock()
	returned := make(chan (<-chan struct{}), 1)
	go func() { returned <- m.Stop() }()
	var done <-chan struct{}
	select {
	case done = <-returned:
	case <-time.After(time.Second):
		m.mu.Unlock()
		t.Fatal("cancellation waited on model disk IO")
	}
	select {
	case <-m.ctx.Done():
	default:
		m.mu.Unlock()
		t.Fatal("Stop returned before canceling model work")
	}
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("model join did not finish after disk IO released")
	}
}
