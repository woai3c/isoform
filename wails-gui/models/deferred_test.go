package models

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlanSkippingLeavesTheJobOutButKeepsItsChoice(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	m := newFixtureManager(t, e, 1000)
	p, err := m.BuildPlanSkipping(nil, nil, []string{"live"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 0 || p.TotalBytes != 0 || p.Choices["live"] != e.ID {
		t.Fatalf("skipped job planned: %+v", p)
	}
	full, err := m.BuildPlanSkipping(nil, nil, []string{"voice"})
	if err != nil || len(full.Items) != 1 || full.TotalBytes != *e.SizeBytes {
		t.Fatalf("other skip changed the plan: %+v %v", full, err)
	}
}

func TestDeferredJobsWaitUntilReleased(t *testing.T) {
	var requests atomic.Int32
	data := []byte("a real fixture model\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	e := fixtureEntry(server.URL)
	// Too little space: Later still works, and the job waits.
	m := newFixtureManager(t, e, 1)
	m.SetNetworkState(NetworkState{Known: true})
	p, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Defer(p); err != nil {
		t.Fatal(err)
	}
	if jobs := m.Downloads(); len(jobs) != 1 || jobs[0].State != "queued" || !jobs[0].Deferred {
		t.Fatalf("deferred job: %+v", jobs)
	}
	if !m.HasDeferred() {
		t.Fatal("HasDeferred")
	}
	if _, active := m.Progress(); active {
		t.Fatal("a deferred job is not downloading")
	}
	// Neither a restart, a network change nor Resume starts it.
	m.Resume()
	m.SetNetworkState(NetworkState{Known: true})
	next, err := New(m.folder, m.legacy, []Entry{e}, m.free, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	next.SetNetworkState(NetworkState{Known: true})
	next.Resume()
	time.Sleep(50 * time.Millisecond)
	if requests.Load() != 0 {
		t.Fatal("deferred job downloaded before release")
	}
	if !next.HasDeferred() {
		t.Fatal("deferral lost on restart")
	}
	next.free = func(string) (int64, error) { return 1000, nil }
	next.ReleaseDeferred()
	if next.HasDeferred() {
		t.Fatal("still deferred after release")
	}
	waitState(t, next, "done")
	if requests.Load() != 1 {
		t.Fatalf("requests %d", requests.Load())
	}
	if _, active := next.Progress(); active {
		t.Fatal("finished batch still active")
	}
}

func TestResumeStartsADeferredJobNow(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	e := fixtureEntry(server.URL)
	m := newFixtureManager(t, e, 1000)
	m.SetNetworkState(NetworkState{Known: true})
	p, _ := m.BuildPlan(nil, nil)
	if err := m.Defer(p); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeDownload(e.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("resume did not start the deferred job")
	}
	if percent, active := m.Progress(); !active || percent != 0 {
		t.Fatalf("progress %d %v", percent, active)
	}
}

func TestDeferNeedsAReviewedPlan(t *testing.T) {
	e := fixtureEntry("https://model-assets.yansu.app/fixture")
	dir := t.TempDir()
	m, err := New(filepath.Join(dir, "models"), dir, []Entry{e}, func(string) (int64, error) { return 1000, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if err := m.Defer(Plan{Items: []PlanItem{{ID: e.ID}}}); err == nil {
		t.Fatal("unreviewed plan deferred")
	}
	if len(m.Downloads()) != 0 {
		t.Fatal("job created")
	}
}
