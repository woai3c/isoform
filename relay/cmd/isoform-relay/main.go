// Command isoform-relay is the store-and-forward broker between iPhones and a
// Mac hub. It stores credential digests, ciphertext and routing metadata only.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/netutil"
	"isoform-relay/internal/apnspush"
	"isoform-relay/internal/config"
	"isoform-relay/internal/email"
	"isoform-relay/internal/httpapi"
	"isoform-relay/internal/hub"
	"isoform-relay/internal/notify"
	"isoform-relay/internal/push"
	"isoform-relay/internal/sms"
	"isoform-relay/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func environmentError(env *config.Environment, cause error) error {
	if err := env.Err(); err != nil {
		return err
	}
	return cause
}

func run() error {
	env := config.NewEnvironment(os.Getenv, log.Printf)
	cfg, err := config.Load(env.Get)
	if err != nil {
		return environmentError(env, err)
	}
	if cfg.LogFormat == "json" {
		// log.Printf calls land in the JSON handler too, so every line is
		// one object with time, level and msg.
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	}
	gateway, err := apnspush.LoadGatewayConfig(env.Get)
	if err != nil {
		return environmentError(env, err)
	}
	apns, err := apnspush.LoadConfig(env.Get)
	if err != nil {
		return environmentError(env, err)
	}
	smsConfig, err := sms.LoadConfig(env.Get)
	if err != nil {
		return environmentError(env, err)
	}
	emailConfig, err := email.LoadConfig(env.Get)
	if err != nil {
		return environmentError(env, err)
	}
	if err := env.Err(); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.StateDir, "relay.sqlite"))
	if err != nil {
		return errors.New("unable to open relay state")
	}
	defer st.Close()
	var pushSender apnspush.Sender
	pushConfiguration := push.Configuration{}
	var pushClient *apnspush.Client
	if gateway.Configured() {
		pushSender = apnspush.NewGatewayClient(gateway)
		pushConfiguration.Environment = gateway.Environment
		pushConfiguration.Gateway = true
	} else if apns.Configured() {
		pushClient, err = apnspush.NewClient(apns)
		if err != nil {
			return err
		}
		defer pushClient.Close()
		pushSender = pushClient
		pushConfiguration = apns.Configuration()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.NoTrustedProxies {
		httpapi.DisableForwardedClientIP()
	} else {
		httpapi.SetTrustedProxies(cfg.TrustedProxies)
	}
	httpapi.SetClientIPHeader(cfg.ClientIPHeader)
	notifier := notify.New()
	hubs := hub.NewManager(st, notifier)
	handler := httpapi.New(st, hubs, notifier, pushConfiguration)
	if smsConfig.Enabled() {
		provider, err := sms.NewProvider(smsConfig)
		if err != nil {
			return err
		}
		vault, err := sms.NewVault(smsConfig.VaultKeys())
		if err != nil {
			return err
		}
		texts := sms.NewService(st, provider, vault, smsConfig)
		// Values sealed under a previous key move to the current one, so a
		// rotation needs one start with both keys (docs/sms-channel.md).
		moved, stuck, err := texts.Reseal(ctx)
		if err != nil {
			return errors.New("unable to reseal the stored text message values")
		}
		if moved > 0 || stuck > 0 {
			log.Printf("sms: resealed %d stored values under the current key; %d open under no key", moved, stuck)
		}
		texts.OnReply = hubs.Kick
		texts.OnState = hubs.SMSChanged
		hubs.SMS = texts
		handler.SMS = texts
	}
	if emailConfig.Enabled() {
		provider, err := email.NewProvider(emailConfig)
		if err != nil {
			return err
		}
		vault, err := email.NewVault(emailConfig.Key)
		if err != nil {
			return err
		}
		handler.Email = email.NewService(st, provider, vault, emailConfig)
	}
	listener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return errors.New("unable to listen on relay address")
	}
	defer listener.Close()
	// Past MaxConns new connections wait in the accept queue rather than
	// adding goroutines; the kernel drops them when that fills.
	listener = netutil.LimitListener(listener, cfg.MaxConns)
	// requestCtx is every request's parent; cancelling it on shutdown ends
	// the long-polls at once so Shutdown does not wait up to longPollMax.
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	server := &http.Server{
		Handler:           handler,
		BaseContext:       func(net.Listener) context.Context { return requestCtx },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8192,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
	}

	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		st.Maintain(ctx, time.Minute)
	}()
	if pushSender != nil {
		background.Add(1)
		go func() {
			defer background.Done()
			(&apnspush.Worker{Outbox: st, Sender: pushSender}).Run(ctx)
		}()
	}

	serveDone := make(chan error, 1)
	go func() {
		if cfg.TLS() {
			serveDone <- server.ServeTLS(listener, cfg.TLSCert, cfg.TLSKey)
		} else {
			serveDone <- server.Serve(listener)
		}
	}()
	log.Printf("Isoform relay listening on %s (tls: %t, behind proxy: %t, max conns: %d, push: %t, sms: %t, email: %t)", listener.Addr(), cfg.TLS(), cfg.BehindTLSProxy, cfg.MaxConns, pushSender != nil, smsConfig.Enabled(), emailConfig.Enabled())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serveDone:
	}
	stop()
	log.Print("Isoform relay shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cancelRequests()
	_ = server.Shutdown(shutdownCtx)
	hubs.CloseAll()
	workersDone := make(chan struct{})
	go func() {
		background.Wait()
		close(workersDone)
	}()
	select {
	case <-workersDone:
	case <-shutdownCtx.Done():
		return errors.New("background shutdown timed out")
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return errors.New("relay listener failed")
	}
	return nil
}
