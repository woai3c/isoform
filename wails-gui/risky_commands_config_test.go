package main

import (
	"context"
	"testing"
	"time"

	"yansu-agent/agent"
)

// riskyCommandsCaptureRunner records the TaskInput of every run and returns
// at once, so the test can read the flag the attended chat sent.
type riskyCommandsCaptureRunner struct {
	inputs chan *agent.TaskInput
}

func (r *riskyCommandsCaptureRunner) Init(context.Context) error { return nil }
func (r *riskyCommandsCaptureRunner) IsReady() bool              { return true }
func (r *riskyCommandsCaptureRunner) ExecuteTask(_ context.Context, input *agent.TaskInput, _ agent.StreamCallback) (string, error) {
	r.inputs <- input
	return "run", nil
}
func (r *riskyCommandsCaptureRunner) CancelRun(string) error                   { return nil }
func (r *riskyCommandsCaptureRunner) RespondToPermission(string, string) error { return nil }
func (r *riskyCommandsCaptureRunner) CancelPermission(string)                  {}
func (r *riskyCommandsCaptureRunner) Shutdown() error                          { return nil }

func TestAskBeforeRiskyCommandsDefaultsOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	if value, err := app.GetAskBeforeRiskyCommands(); err != nil || value {
		t.Fatal("ask before risky commands should default to off")
	}
}

func TestSetAskBeforeRiskyCommandsRoundTripsThroughConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}

	if err := app.SetAskBeforeRiskyCommands(true); err != nil {
		t.Fatalf("SetAskBeforeRiskyCommands(true): %v", err)
	}
	if value, err := app.GetAskBeforeRiskyCommands(); err != nil || !value {
		t.Fatal("getter stayed off after enabling")
	}
	if !loadConfig().AskBeforeRiskyCommands {
		t.Fatal("enabled value was not persisted to config")
	}

	if err := app.SetAskBeforeRiskyCommands(false); err != nil {
		t.Fatalf("SetAskBeforeRiskyCommands(false): %v", err)
	}
	if value, err := app.GetAskBeforeRiskyCommands(); err != nil || value {
		t.Fatal("getter stayed on after disabling")
	}
	if loadConfig().AskBeforeRiskyCommands {
		t.Fatal("disabled value was not persisted to config")
	}
}

// The attended chat turn carries the Settings value in both states.
func TestAttendedChatTaskInputCarriesAskBeforeRiskyCommands(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			app, _, _ := newBgShellTestApp(t)
			if err := app.SetAskBeforeRiskyCommands(enabled); err != nil {
				t.Fatal(err)
			}
			runner := &riskyCommandsCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			app.runner = runner
			app.tagCancels = map[string]context.CancelFunc{}
			app.sessionTempDirs = map[string]string{}
			app.streams = map[string]context.CancelFunc{}
			app.runIDByTag = map[string]string{}
			app.pendingTraceIDs = map[string]string{}

			if err := app.executePrompt("work", "", "m", "", "chat-1", nil, false, false, "", nil, nil, "", "", promptOriginMac); err != nil {
				t.Fatal(err)
			}
			select {
			case input := <-runner.inputs:
				if input.AutoApproveTools {
					t.Fatal("attended chat must not auto-approve tools")
				}
				if input.AskBeforeRiskyCommands != enabled {
					t.Fatalf("AskBeforeRiskyCommands = %v, want %v", input.AskBeforeRiskyCommands, enabled)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the attended run did not start")
			}
		})
	}
}
