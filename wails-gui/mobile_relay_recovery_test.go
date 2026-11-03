package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestMobileRelayRetryTracksHealthDuringAnOutage(t *testing.T) {
	for _, test := range []struct {
		name      string
		healthyOn int32
		wantNext  time.Duration
		minProbes int32
	}{
		{"healthy", 1, 20 * time.Millisecond, 1},
		{"unreachable", 0, 160 * time.Millisecond, 2},
		{"recovers during wait", 2, 20 * time.Millisecond, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var probes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/health" {
					t.Errorf("unexpected health request: %s %s", r.Method, r.URL.Path)
				}
				if calls := probes.Add(1); test.healthyOn == 0 || calls < test.healthyOn {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			client := &mobileRelayClient{origin: server.URL, retryNow: make(chan struct{}, 1), timing: mobileRelayTiming{
				InitialBackoff: 2 * time.Millisecond,
				MaxBackoff:     200 * time.Millisecond,
				HealthyBackoff: 20 * time.Millisecond,
			}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			next, retry := client.waitRetry(ctx, 80*time.Millisecond)
			if !retry || next != test.wantNext || probes.Load() < test.minProbes {
				t.Fatalf("next=%s retry=%v probes=%d; want next=%s probes >= %d", next, retry, probes.Load(), test.wantNext, test.minProbes)
			}
		})
	}
}

func TestMobileRelayWakeResetsBackoffAndCancelsTheAttempt(t *testing.T) {
	client := &mobileRelayClient{retryNow: make(chan struct{}, 1), timing: mobileRelayTimes}
	attempt, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()
	client.cancelDial = cancelAttempt
	app := &App{mobileBridge: &mobileBridge{client: client}}
	app.mobileRelayRecovered()
	app.mobileRelayRecovered() // Bursts collapse to one retry and never block.
	if !errors.Is(attempt.Err(), context.Canceled) {
		t.Fatal("wake left the current connection attempt running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, retry := client.waitRetry(ctx, 20*time.Second)
	if !retry || next != mobileRelayTimes.InitialBackoff {
		t.Fatalf("wake reset: next=%s retry=%v", next, retry)
	}
	select {
	case <-client.retryNow:
		t.Fatal("wake burst queued more than one retry")
	default:
	}
}

func TestMobileRelayWakeInterruptsRegistration(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	first := relay.nextConn(t)
	relay.nextFrame(t, "register")
	bridge.app.mobileBridgeMu.Lock()
	bridge.app.mobileBridge = bridge
	bridge.app.mobileBridgeMu.Unlock()
	bridge.app.mobileRelayRecovered()
	second := relay.nextConn(t)
	if first == second {
		t.Fatal("wake did not start a new connection attempt")
	}
}

func TestMobileRelayNetworkChangeResetsBackoff(t *testing.T) {
	client := &mobileRelayClient{retryNow: make(chan struct{}, 1), timing: mobileRelayTimes}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var samples atomic.Int32
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		client.watchNetwork(ctx, func() (string, error) {
			switch samples.Add(1) {
			case 1, 2:
				return "en0:192.168.1.2", nil
			case 3:
				return "", errors.New("interface snapshot unavailable")
			default:
				return "en1:192.168.2.2", nil
			}
		}, 5*time.Millisecond)
	}()
	next, retry := client.waitRetry(ctx, 20*time.Second)
	cancel()
	<-stopped
	if !retry || next != mobileRelayTimes.InitialBackoff || samples.Load() < 4 {
		t.Fatalf("network reset: next=%s retry=%v samples=%d", next, retry, samples.Load())
	}
}

func TestMobileRelayUnchangedNetworkDoesNotRetry(t *testing.T) {
	client := &mobileRelayClient{retryNow: make(chan struct{}, 1), timing: mobileRelayTimes}
	ctx, cancel := context.WithCancel(context.Background())
	var samples atomic.Int32
	client.watchNetwork(ctx, func() (string, error) {
		if samples.Add(1) == 4 {
			cancel()
		}
		return "en0:192.168.1.2", nil
	}, 5*time.Millisecond)
	select {
	case <-client.retryNow:
		t.Fatal("an unchanged network requested a reconnect")
	default:
	}
}

func TestMobileRelayInterfaceChangeKeepsAHealthySocket(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	first := relay.acceptRegistration(t, mobileTestListed(phone))
	mobileTestWaitFor(t, "connected before recovery", func() bool {
		return bridge.connectionStatus().RelayState == mobileRelayConnected
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	samples := 0
	bridge.client.watchNetwork(ctx, func() (string, error) {
		samples++
		switch samples {
		case 1:
			return "en0:192.168.1.2", nil
		case 3:
			cancel()
		}
		return "en0:192.168.1.2\nutun0:fd00::1", nil
	}, 5*time.Millisecond)
	select {
	case <-relay.conns:
		t.Fatal("interface recovery replaced a healthy registered socket")
	case <-time.After(100 * time.Millisecond):
	}
	brief := MobileBriefV1{Version: 1, BriefID: "2026-10-09-morning", Slot: "morning", AsOf: "2026-10-09T07:00:00Z"}
	if err := bridge.publishBrief(brief, false); err != nil {
		t.Fatal(err)
	}
	if frame := relay.nextFrame(t, "brief"); frame.conn != first {
		t.Fatal("the brief was not delivered over the original healthy socket")
	}
}
