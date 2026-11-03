package main

import (
	"context"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"wails-gui/internal/remoteaccess"
)

var DefaultMobilePushGatewayURL string
var DefaultMobilePushGatewayEnvironment string
var DefaultMobilePushGatewayPublicKey string

var errMobileRemoteAccessNotReady = errors.New("remote access is not ready; try again when the relay is available")

type mobileRemoteAccessSupervisor interface {
	Start(context.Context) error
	WaitReady(context.Context) (remoteaccess.Snapshot, error)
	Snapshot() remoteaccess.Snapshot
	Reset(context.Context) (remoteaccess.Snapshot, error)
	Stop()
}

func mobilePushGatewayConfig() (string, string, string, error) {
	raw := strings.TrimSpace(DefaultMobilePushGatewayURL)
	if raw == "" {
		return "", "", "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", "", "", errors.New("the push gateway must be an HTTPS origin")
	}
	environment := strings.TrimSpace(DefaultMobilePushGatewayEnvironment)
	if environment == "" {
		environment = "production"
	}
	if environment != "development" && environment != "production" {
		return "", "", "", errors.New("the push gateway environment must be development or production")
	}
	publicKey := strings.TrimSpace(DefaultMobilePushGatewayPublicKey)
	rawPublicKey, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil || len(rawPublicKey) != 32 || base64.RawURLEncoding.EncodeToString(rawPublicKey) != publicKey {
		return "", "", "", errors.New("the push gateway public key must be a base64url X25519 key")
	}
	if _, err := ecdh.X25519().NewPublicKey(rawPublicKey); err != nil {
		return "", "", "", errors.New("the push gateway public key must be a base64url X25519 key")
	}
	return strings.TrimRight(raw, "/"), environment, publicKey, nil
}

func mobilePushGatewayHubConfig(hubPrivate *ecdh.PrivateKey, gatewayPublic string) (string, string, error) {
	if hubPrivate == nil {
		return "", "", errors.New("the hub identity is unavailable for push gateway authentication")
	}
	rawGatewayPublic, err := base64.RawURLEncoding.DecodeString(gatewayPublic)
	if err != nil || len(rawGatewayPublic) != 32 || base64.RawURLEncoding.EncodeToString(rawGatewayPublic) != gatewayPublic {
		return "", "", errors.New("the push gateway public key is invalid")
	}
	publicKey, err := ecdh.X25519().NewPublicKey(rawGatewayPublic)
	if err != nil {
		return "", "", errors.New("the push gateway public key is invalid")
	}
	shared, err := hubPrivate.ECDH(publicKey)
	if err != nil {
		return "", "", errors.New("unable to derive push gateway authentication")
	}
	hubPublic := hubPrivate.PublicKey().Bytes()
	salt := append(append([]byte(nil), hubPublic...), rawGatewayPublic...)
	key, err := hkdf.Key(sha256.New, shared, salt, "isoform-push-gateway hub v1", 32)
	if err != nil {
		return "", "", errors.New("unable to derive push gateway authentication")
	}
	return base64.RawURLEncoding.EncodeToString(hubPublic), base64.RawURLEncoding.EncodeToString(key), nil
}

func (bridge *mobileBridge) ensureRemoteAccess(ctx context.Context) (remoteaccess.Snapshot, error) {
	bridge.mu.Lock()
	if bridge.stopped {
		bridge.mu.Unlock()
		return remoteaccess.Snapshot{State: remoteaccess.StateOff}, errMobileBridgeStopping
	}
	if bridge.remoteAccess == nil {
		if bridge.app != nil && bridge.app.remoteAccessFactory != nil {
			bridge.remoteAccess = bridge.app.remoteAccessFactory()
		} else {
			relayBin, err := LocateBundledRelay()
			if err != nil {
				bridge.mu.Unlock()
				return remoteaccess.Snapshot{State: remoteaccess.StateFailed, LastError: err.Error()}, err
			}
			dumbpipeBin, err := LocateBundledDumbpipe()
			if err != nil {
				bridge.mu.Unlock()
				return remoteaccess.Snapshot{State: remoteaccess.StateFailed, LastError: err.Error()}, err
			}
			gatewayURL, gatewayEnvironment, gatewayPublicKey, err := mobilePushGatewayConfig()
			if err != nil {
				bridge.mu.Unlock()
				return remoteaccess.Snapshot{State: remoteaccess.StateFailed, LastError: err.Error()}, err
			}
			hubID, hubPublicKey, hubKey := "", "", ""
			if gatewayURL != "" {
				if bridge.identity == nil {
					bridge.mu.Unlock()
					return remoteaccess.Snapshot{State: remoteaccess.StateFailed, LastError: "hub identity is unavailable"}, errors.New("hub identity is unavailable")
				}
				hubID = bridge.identity.HubID
				hubPublicKey, hubKey, err = mobilePushGatewayHubConfig(bridge.identity.Private, gatewayPublicKey)
				if err != nil {
					bridge.mu.Unlock()
					return remoteaccess.Snapshot{State: remoteaccess.StateFailed, LastError: err.Error()}, err
				}
			}
			bridge.remoteAccess = remoteaccess.New(remoteaccess.Config{
				RelayBin: relayBin, DumbpipeBin: dumbpipeBin,
				StateDir:       filepath.Join(bridge.stateDir, "remote-access"),
				PushGatewayURL: gatewayURL, PushGatewayEnvironment: gatewayEnvironment,
				PushGatewayHubID: hubID, PushGatewayHubPub: hubPublicKey, PushGatewayHubKey: hubKey,
				OnChange: func() { go bridge.notify() },
				Logf:     func(format string, args ...any) { log.Printf("[mobile-remote-access] "+format, args...) },
			})
		}
	}
	supervisor := bridge.remoteAccess
	bridge.mu.Unlock()
	if supervisor.Snapshot().State == remoteaccess.StateFailed {
		supervisor.Stop()
	}
	if err := supervisor.Start(bridge.ctx); err != nil {
		return supervisor.Snapshot(), err
	}
	bridge.mu.Lock()
	if bridge.stopped {
		if bridge.remoteAccess == supervisor {
			bridge.remoteAccess = nil
		}
		bridge.mu.Unlock()
		supervisor.Stop()
		return supervisor.Snapshot(), errMobileBridgeStopping
	}
	bridge.mu.Unlock()
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	snapshot, err := supervisor.WaitReady(waitCtx)
	if err != nil {
		return snapshot, err
	}
	bridge.mu.Lock()
	if bridge.stopped {
		if bridge.remoteAccess == supervisor {
			bridge.remoteAccess = nil
		}
		bridge.remoteRelayURL = ""
		bridge.mu.Unlock()
		supervisor.Stop()
		return snapshot, errMobileBridgeStopping
	}
	bridge.remoteRelayURL = snapshot.RelayURL
	bridge.mu.Unlock()
	return snapshot, nil
}
