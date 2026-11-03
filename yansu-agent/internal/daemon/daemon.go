package daemon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"yansu-agent/internal/client"
	"yansu-agent/internal/config"
)

// Run starts the relay daemon and blocks until ctx is cancelled or a signal is received.
// It auto-discovers the Slack team_id and slack_user_id from the backend API so no
// manual setup-relay step is required.
func Run(ctx context.Context, cfg *config.Config, daemonCfg *config.DaemonConfig) error {
	if cfg.Token == "" || cfg.BaseURL == "" {
		return fmt.Errorf("not logged in — run 'yansu login' first")
	}

	// Write PID file so daemon management commands can find the process.
	if err := config.WritePIDFile(); err != nil {
		fmt.Printf("warning: could not write PID file: %v\n", err)
	}
	defer config.RemovePIDFile()

	// Auto-discover Slack identity from the backend.
	apiClient := client.New(cfg.BaseURL, cfg.Token)
	binding, err := apiClient.GetMySlackBinding(ctx)
	if err != nil {
		return fmt.Errorf("failed to get Slack binding: %w\n\nVisit %s/settings/slack to connect your Slack workspace first.", err, cfg.BaseURL)
	}

	fmt.Printf("[%s] Slack binding: team=%s slack_user=%s\n",
		time.Now().Format("2006-01-02 15:04:05"), binding.TeamID, binding.SlackUserID)

	registry := NewProjectRegistry()
	if err := registry.Load(); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("load project registry: %w", err)
		}
	}

	sessions := NewSessionManager()
	defer sessions.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down yansu daemon...")
		cancel()
	}()

	adapter := NewRelayAdapter(cfg.BaseURL, cfg.Token, binding.TeamID, binding.SlackUserID)

	fmt.Printf("[%s] yansu daemon started (relay mode). Registered projects: %d\n",
		time.Now().Format("2006-01-02 15:04:05"), len(registry.List()))

	router := NewRouter(cfg, daemonCfg, registry, sessions, adapter)
	return adapter.Listen(ctx, func(event Event) {
		// Defense in depth: the relay adapter already drops passive channel
		// messages, but no adapter must ever route ambient channel chatter
		// (messages between other people, no @mention) into a session.
		if IsPassiveChannelEvent(event) {
			return
		}
		fmt.Printf("[%s] Received event from %s user %s: %q\n",
			time.Now().Format("2006-01-02 15:04:05"), event.Platform, event.UserID, event.Text)
		// Session key includes platform so parallel queues are independent.
		session := sessions.GetOrCreate(event.UserID + ":" + event.Platform)
		if router.IsAITaskEvent(event, session) {
			// AI path: bypass serial queue. Slot management is entirely inside runAITask.
			go router.handleAI(ctx, event)
		} else {
			// Fast path: serial session queue, one at a time per user.
			if !session.CanEnqueue() {
				adapter.Reply(event, "⏳ Too many pending tasks. Please wait for current tasks to complete.")
				return
			}
			session.Enqueue(func() {
				router.Handle(ctx, event)
			})
		}
	})
}
