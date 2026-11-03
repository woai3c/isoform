package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"yansu-agent/cronsecret"
)

func TestCronAPIPublicationRecoversAfterKeychainUnlock(t *testing.T) {
	app, server, home := newCronMcpTestServer(t)
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	portFile := filepath.Join(dir, "cron-port")
	var attempts atomic.Int32
	store := func(_ string, secret string, _ ...cronsecret.Option) error {
		if secret != server.Secret() {
			t.Error("retry changed the running server's credential")
		}
		if attempts.Add(1) <= 2 {
			return fmt.Errorf("%w: Keychain status -25308", cronsecret.ErrUnavailable)
		}
		return nil
	}
	waits := make(chan time.Duration, 3)
	resume := make(chan struct{})
	wait := func(ctx context.Context, delay time.Duration) error {
		waits <- delay
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-resume:
			return nil
		}
	}
	// Mirrors startup's existing ownership path: only a nil return retains API.
	if err := publishCronAPIFiles(dir, server.Port(), server.Secret(), store, wait); err != nil {
		server.Stop()
		t.Fatal(err)
	} else {
		app.cronAPI = server
	}
	for _, want := range []time.Duration{5 * time.Second, 30 * time.Second} {
		select {
		case got := <-waits:
			if got != want {
				t.Fatalf("retry delay = %s, want %s", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("retry did not start")
		}
		if _, err := os.Stat(portFile); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("published before verified storage: %v", err)
		}
		resume <- struct{}{}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if port, err := os.ReadFile(portFile); err == nil && string(port) == fmt.Sprint(server.Port()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("port not published after Keychain recovered")
		}
		time.Sleep(time.Millisecond)
	}
	if attempts.Load() != 3 || app.cronAPI != server {
		t.Fatal("API was not retained through two failed attempts")
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/cron/jobs", server.Port()), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+server.Secret())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recovered API status = %d", resp.StatusCode)
	}
}

func TestCronAPIPublicationStopsRetryOnShutdown(t *testing.T) {
	_, server, home := newCronMcpTestServer(t)
	var attempts atomic.Int32
	store := func(string, string, ...cronsecret.Option) error {
		attempts.Add(1)
		return fmt.Errorf("%w: Keychain status -25308", cronsecret.ErrUnavailable)
	}
	started, stopped := make(chan struct{}), make(chan struct{})
	wait := func(ctx context.Context, _ time.Duration) error {
		close(started)
		err := waitCronAPIRetry(ctx, time.Hour)
		close(stopped)
		return err
	}
	if err := publishCronAPIFiles(home, server.Port(), server.Secret(), store, wait); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not start")
	}
	server.Stop()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel retry")
	}
	if attempts.Load() != 1 {
		t.Fatal("storage was retried after shutdown")
	}
	if _, err := os.Stat(filepath.Join(home, "cron-port")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stopped API published its port: %v", err)
	}
	if _, ok := cronAPIPublications.Load(server.publication.key); ok {
		t.Fatal("stopped API retained its publication state")
	}
}

func TestCronAPIPublicationReturnsPermanentFailure(t *testing.T) {
	_, server, home := newCronMcpTestServer(t)
	want := errors.New("invalid trusted CLI")
	store := func(string, string, ...cronsecret.Option) error { return want }
	wait := func(context.Context, time.Duration) error {
		t.Error("permanent failure must not retry")
		return context.Canceled
	}
	if err := publishCronAPIFiles(home, server.Port(), server.Secret(), store, wait); !errors.Is(err, want) {
		t.Fatalf("publication error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "cron-port")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed API published its port: %v", err)
	}
}

func TestCronAPIRetryBackoff(t *testing.T) {
	var got []time.Duration
	for retry := 0; retry < 6; retry++ {
		got = append(got, cronAPIRetryDelay(retry))
	}
	want := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retry backoff = %v, want %v", got, want)
	}
}

func TestCronAPIPublicationCancellationDuringStore(t *testing.T) {
	_, server, home := newCronMcpTestServer(t)
	store := func(string, string, ...cronsecret.Option) error {
		server.publication.cancel()
		return nil
	}
	if err := publishCronAPIFiles(home, server.Port(), server.Secret(), store, waitCronAPIRetry); !errors.Is(err, context.Canceled) {
		t.Fatalf("publication error = %v, want canceled", err)
	}
	if _, err := os.Stat(filepath.Join(home, "cron-port")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled API published its port: %v", err)
	}
}

func TestCronAPIPublicationRefusesStoppedServer(t *testing.T) {
	_, server, home := newCronMcpTestServer(t)
	server.Stop()
	store := func(string, string, ...cronsecret.Option) error {
		t.Error("stopped server must not store credentials")
		return nil
	}
	if err := publishCronAPIFiles(home, server.Port(), server.Secret(), store, waitCronAPIRetry); err == nil {
		t.Fatal("publication accepted a stopped server")
	}
	if _, err := os.Stat(filepath.Join(home, "cron-port")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stopped API published its port: %v", err)
	}
}
