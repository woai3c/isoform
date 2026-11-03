package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"isoform-relay/internal/apnspush"
	"isoform-relay/internal/config"
	"isoform-relay/internal/httpapi"
	"isoform-relay/internal/pushgateway"
	"isoform-relay/internal/pushgatewayauth"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("isoform-push-gateway", flag.ContinueOnError)
	printPublicKey := flags.Bool("print-public-key", false, "print the configured gateway public key and exit")
	generateKey := flags.String("generate-key", "", "write a new gateway private key to PATH")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*printPublicKey && *generateKey != "") {
		return errors.New("choose -print-public-key or -generate-key PATH")
	}
	if *generateKey != "" {
		return pushgatewayauth.GenerateGatewayPrivateKeyFile(*generateKey)
	}
	privateKey, err := pushgatewayauth.LoadGatewayPrivateKey(os.Getenv("ISOFORM_PUSH_GATEWAY_KEY_FILE"))
	if err != nil {
		return err
	}
	if *printPublicKey {
		_, err = fmt.Fprintln(os.Stdout, pushgatewayauth.PublicKeyString(privateKey))
		return err
	}
	env := config.NewEnvironment(os.Getenv, log.Printf)
	apns, err := apnspush.LoadConfig(env.Get)
	if err != nil {
		if envErr := env.Err(); envErr != nil {
			return envErr
		}
		return err
	}
	if err := env.Err(); err != nil {
		return err
	}
	if err := configureTrustedProxies(os.Getenv, log.Printf); err != nil {
		return err
	}
	if !apns.Configured() {
		return errors.New("push gateway requires complete ISOFORM_RELAY_APNS_* configuration")
	}
	client, err := apnspush.NewClient(apns)
	if err != nil {
		return err
	}
	defer client.Close()

	address := os.Getenv("ISOFORM_PUSH_GATEWAY_ADDR")
	if address == "" {
		address = "127.0.0.1:8792"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return errors.New("ISOFORM_PUSH_GATEWAY_ADDR must use a literal IP address and port")
	}
	cert, key := os.Getenv("ISOFORM_PUSH_GATEWAY_TLS_CERT"), os.Getenv("ISOFORM_PUSH_GATEWAY_TLS_KEY")
	if (cert == "") != (key == "") {
		return errors.New("push gateway TLS certificate and key must be set together")
	}
	behindProxy, err := strconv.ParseBool(os.Getenv("ISOFORM_PUSH_GATEWAY_BEHIND_TLS_PROXY"))
	if os.Getenv("ISOFORM_PUSH_GATEWAY_BEHIND_TLS_PROXY") == "" {
		behindProxy = false
		err = nil
	}
	if err != nil {
		return errors.New("ISOFORM_PUSH_GATEWAY_BEHIND_TLS_PROXY must be a boolean")
	}
	ip := net.ParseIP(host)
	if !ip.IsLoopback() && cert == "" && !behindProxy {
		return errors.New("non-loopback push gateway listeners require TLS or a trusted TLS proxy")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("unable to listen on push gateway address")
	}
	defer listener.Close()
	server := &http.Server{
		Handler:           pushgateway.New(apns.Environment, client, privateKey),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8192,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveDone := make(chan error, 1)
	go func() {
		if cert != "" {
			serveDone <- server.ServeTLS(listener, cert, key)
		} else {
			serveDone <- server.Serve(listener)
		}
	}()
	log.Printf("Isoform push gateway listening on %s (tls: %t, environment: %s)", listener.Addr(), cert != "", apns.Environment)
	select {
	case <-ctx.Done():
	case err := <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

func configureTrustedProxies(getenv func(string) string, logf func(string, ...any)) error {
	networks, noTrustedProxies, err := loadTrustedProxySettings(getenv, logf)
	if err != nil {
		return err
	}
	if noTrustedProxies {
		httpapi.DisableForwardedClientIP()
	} else {
		httpapi.SetTrustedProxies(networks)
	}
	httpapi.SetClientIPHeader(config.DefaultClientIPHeader)
	return nil
}

func loadTrustedProxySettings(getenv func(string) string, logf func(string, ...any)) ([]*net.IPNet, bool, error) {
	value := strings.TrimSpace(getenv("ISOFORM_PUSH_GATEWAY_TRUSTED_PROXIES"))
	if value == "" {
		env := config.NewEnvironment(getenv, logf)
		value = env.Get("ISOFORM_RELAY_TRUSTED_PROXIES")
		if err := env.Err(); err != nil {
			return nil, false, err
		}
	}
	networks, noTrustedProxies, err := config.ParseTrustedProxies(value)
	if err != nil {
		return nil, false, fmt.Errorf("ISOFORM_PUSH_GATEWAY_TRUSTED_PROXIES %w", err)
	}
	return networks, noTrustedProxies, nil
}
