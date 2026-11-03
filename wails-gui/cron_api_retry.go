package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"yansu-agent/cronsecret"
)

// The existing startup call publishes by port and secret. Resolve that pair to
// its server's lifetime so publication can retry without blocking app startup.
var cronAPIPublications sync.Map

type cronAPIPublicationKey struct {
	port   int
	secret string
}

type cronAPIPublication struct {
	key    cronAPIPublicationKey
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex // serialize publication with Stop
}

func registerCronAPIPublication(parent context.Context, port int, secret string) *cronAPIPublication {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p := &cronAPIPublication{key: cronAPIPublicationKey{port, secret}, ctx: ctx, cancel: cancel}
	cronAPIPublications.Store(p.key, p)
	return p
}

func (p *cronAPIPublication) stop() {
	p.cancel()
	// Wait for an in-flight store/publication before the listener is closed.
	// A sleeping retry wakes on cancellation and cannot publish a stale port.
	p.mu.Lock()
	defer p.mu.Unlock()
	cronAPIPublications.Delete(p.key)
}

type cronAPISecretStore func(string, string, ...cronsecret.Option) error
type cronAPIRetryWait func(context.Context, time.Duration) error

func publishCronAPIFiles(dir string, port int, secret string, store cronAPISecretStore, wait cronAPIRetryWait, opts ...cronsecret.Option) error {
	write := func(ctx context.Context) error {
		if err := store(dir, secret, opts...); err != nil {
			return fmt.Errorf("store API secret: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "cron-port"), []byte(fmt.Sprint(port)), 0o644); err != nil {
			return fmt.Errorf("write API port: %w", err)
		}
		return nil
	}
	value, ok := cronAPIPublications.Load(cronAPIPublicationKey{port, secret})
	if !ok {
		return errors.New("cron API server is not running")
	}
	p := value.(*cronAPIPublication)
	attempt := func() error {
		p.mu.Lock()
		defer p.mu.Unlock()
		if err := p.ctx.Err(); err != nil {
			return err
		}
		return write(p.ctx)
	}
	err := attempt()
	if !errors.Is(err, cronsecret.ErrUnavailable) {
		return err
	}
	log.Printf("[cron-api] credential store unavailable; retrying in background: %v", err)
	go func() {
		for retry := 0; ; retry++ {
			if err := wait(p.ctx, cronAPIRetryDelay(retry)); err != nil {
				return
			}
			err := attempt()
			if err == nil {
				log.Printf("[cron-api] credentials published after retry")
				return
			}
			if p.ctx.Err() != nil {
				return
			}
			if !errors.Is(err, cronsecret.ErrUnavailable) {
				log.Printf("[cron-api] credential publication failed: %v", err)
				return
			}
			log.Printf("[cron-api] credential store still unavailable: %v", err)
		}
	}()
	// Startup must retain the API and continue while Keychain is locked.
	return nil
}

func cronAPIRetryDelay(retry int) time.Duration {
	switch retry {
	case 0:
		return 5 * time.Second
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func waitCronAPIRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
