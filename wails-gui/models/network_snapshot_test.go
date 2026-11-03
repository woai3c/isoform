package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNetworkSnapshotDoesNotAuthorizeDownloads(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("a real fixture model\n"))
	}))
	defer server.Close()
	e := fixtureEntry(server.URL)
	m := newFixtureManager(t, e, 1000)
	state := NetworkState{Known: true, Offline: true, Constrained: true}
	m.SetNetworkState(state)
	m.networkProbe = func() NetworkState { return state }
	if got := m.NetworkSnapshot(); got != state {
		t.Fatalf("snapshot = %+v", got)
	}
	raw, err := json.Marshal(m.NetworkSnapshot())
	if err != nil || string(raw) != `{"known":true,"offline":true,"expensive":false,"constrained":true}` {
		t.Fatalf("wire snapshot = %s, %v", raw, err)
	}
	if len(m.Downloads()) != 0 || requests.Load() != 0 {
		t.Fatal("reading network status created work")
	}
	plan, err := m.BuildPlan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(plan); err != nil {
		t.Fatal(err)
	}
	job := m.Downloads()[0]
	if job.State != "paused" || job.PauseReason != "Paused while offline" {
		t.Fatalf("offline job = %+v", job)
	}
	if err := m.ResumeDownload(e.ID); err != nil || requests.Load() != 0 {
		t.Fatal("resume bypassed offline admission", err)
	}
	state = NetworkState{Known: true}
	m.SetNetworkState(state)
	waitState(t, m, "done")
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestNetworkSnapshotUsesCurrentNativeProbe(t *testing.T) {
	m := newFixtureManager(t, fixtureEntry("https://example.invalid"), 1000)
	m.networkProbe = func() NetworkState { return NetworkState{Known: true, Offline: true} }
	if !m.NetworkSnapshot().Offline {
		t.Fatal("stale network snapshot")
	}
	if len(m.Downloads()) != 0 {
		t.Fatal("snapshot started a job")
	}
}

func TestNetworkSnapshotWaitsForNativeObservation(t *testing.T) {
	m := newFixtureManager(t, fixtureEntry("https://example.invalid"), 1000)
	if got := m.NetworkSnapshot(); got.Known {
		t.Fatalf("unobserved path reported known: %+v", got)
	}
}
