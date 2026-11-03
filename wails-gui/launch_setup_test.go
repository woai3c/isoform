package main

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLaunchSetupWorkWaitsForPaintAndEnvironment(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "cancel"}[cancelled], func(t *testing.T) {
			app, manager := setupVoiceFixture(t)
			if err := manager.SetChoice("voice", "melotts-zh-en"); err != nil {
				t.Fatal(err)
			}
			app.launch = newLaunchStartup()
			var probes atomic.Int32
			var finished sync.WaitGroup
			finished.Add(3)
			releaseProbe := make(chan struct{})
			app.setupAgents.init(filepath.Join(t.TempDir(), "probe.json"), func(ctx context.Context, id string) SetupAgent {
				defer finished.Done()
				probes.Add(1)
				<-releaseProbe
				return SetupAgent{ID: id, Installed: true}
			}, nil, nil)
			done := app.deferSetupStartup(manager)
			ctx, cancel := context.WithCancel(t.Context())
			environment, releaseEnvironment, startupDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				defer close(startupDone)
				if !app.launch.wait(ctx, time.Hour) {
					return
				}
				close(environment)
				select {
				case <-releaseEnvironment:
					app.launch.startWorker(ctx)
				case <-ctx.Done():
				}
			}()
			t.Cleanup(func() {
				close(releaseProbe)
				cancel()
				<-startupDone
				app.launch.stop()
				if probes.Load() != 0 {
					finished.Wait()
					// A returned probe still has a cache-file publication to join.
					deadline := time.Now().Add(time.Second)
					for {
						app.setupAgents.mu.Lock()
						refreshing := app.setupAgents.refreshing
						app.setupAgents.mu.Unlock()
						if !refreshing {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("probe cache publication did not finish before cleanup")
						}
						time.Sleep(time.Millisecond)
					}
				}
			})
			assertParked := func() {
				t.Helper()
				select {
				case <-done:
					t.Fatal("setup startup completed before the optional phase")
				default:
				}
				if probes.Load() != 0 || len(manager.Downloads()) != 0 || manager.Selected("voice") != "melotts-zh-en" {
					t.Fatal("parked setup probed, authorized downloads or changed the saved voice")
				}
			}
			assertParked()
			app.launch.firstScreenPainted()
			<-environment
			assertParked()
			if cancelled {
				app.launch.cancelPending()
				cancel()
			} else {
				close(releaseEnvironment)
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("setup work did not finish or cancel")
			}
			if cancelled && probes.Load() != 0 {
				t.Fatal("cancelled setup started a probe")
			}
			if !cancelled && probes.Load() != 3 {
				t.Fatal("optional startup lost the setup probe")
			}
			if len(manager.Downloads()) != 0 || manager.Selected("voice") != "melotts-zh-en" {
				t.Fatal("optional resumption authorized a new download or reset the voice")
			}
		})
	}
}
