// Package config loads the relay's process settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

const (
	// DefaultMaxConns bounds open client connections per process. A hub
	// socket and a phone's long-poll each hold one.
	DefaultMaxConns = 4096
	// DefaultClientIPHeader is the proxy header that names the client.
	DefaultClientIPHeader = "X-Forwarded-For"
)

var headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// Config holds listener and state settings. APNs settings live in apnspush.
type Config struct {
	Address  string
	StateDir string
	TLSCert  string
	TLSKey   string
	// TrustedProxies are the peers whose X-Forwarded-For names the client:
	// the proxy in front of the relay. Loopback by default (a proxy on the
	// same host, the documented setup); any other peer is the client itself.
	TrustedProxies []*net.IPNet
	// NoTrustedProxies disables forwarded-address handling entirely.
	NoTrustedProxies bool
	// BehindTLSProxy allows a plaintext listener off loopback because a
	// trusted proxy (Fly's edge, Caddy) terminates TLS. It requires
	// TrustedProxies to be set explicitly.
	BehindTLSProxy bool
	// ClientIPHeader is the header a trusted proxy puts the client address
	// in; its last value counts. X-Forwarded-For by default; Fly sets
	// Fly-Client-IP.
	ClientIPHeader string
	// MaxConns caps open connections; the listener refuses more.
	MaxConns int
	// LogFormat is "text" or "json".
	LogFormat string
}

// Load reads ISOFORM_RELAY_* settings. Non-loopback listeners require TLS.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Address:        strings.TrimSpace(getenv("ISOFORM_RELAY_ADDR")),
		StateDir:       strings.TrimSpace(getenv("ISOFORM_RELAY_STATE_DIR")),
		TLSCert:        strings.TrimSpace(getenv("ISOFORM_RELAY_TLS_CERT")),
		TLSKey:         strings.TrimSpace(getenv("ISOFORM_RELAY_TLS_KEY")),
		ClientIPHeader: strings.TrimSpace(getenv("ISOFORM_RELAY_CLIENT_IP_HEADER")),
		LogFormat:      strings.ToLower(strings.TrimSpace(getenv("ISOFORM_RELAY_LOG_FORMAT"))),
		MaxConns:       DefaultMaxConns,
	}
	switch strings.ToLower(strings.TrimSpace(getenv("ISOFORM_RELAY_BEHIND_TLS_PROXY"))) {
	case "", "0", "false", "no":
	case "1", "true", "yes":
		c.BehindTLSProxy = true
	default:
		return c, errors.New("ISOFORM_RELAY_BEHIND_TLS_PROXY must be 1 or 0")
	}
	if c.ClientIPHeader == "" {
		c.ClientIPHeader = DefaultClientIPHeader
	}
	if !headerNamePattern.MatchString(c.ClientIPHeader) {
		return c, errors.New("ISOFORM_RELAY_CLIENT_IP_HEADER must be a header name")
	}
	switch c.LogFormat {
	case "":
		c.LogFormat = "text"
	case "text", "json":
	default:
		return c, errors.New("ISOFORM_RELAY_LOG_FORMAT must be text or json")
	}
	if raw := strings.TrimSpace(getenv("ISOFORM_RELAY_MAX_CONNS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return c, errors.New("ISOFORM_RELAY_MAX_CONNS must be a positive count")
		}
		c.MaxConns = n
	}
	if c.Address == "" {
		c.Address = "127.0.0.1:8791"
	}
	if c.StateDir == "" {
		c.StateDir = "./data"
	}
	rawTrustedProxies := strings.TrimSpace(getenv("ISOFORM_RELAY_TRUSTED_PROXIES"))
	trustedProxies, noTrustedProxies, err := ParseTrustedProxies(rawTrustedProxies)
	if err != nil {
		return c, fmt.Errorf("ISOFORM_RELAY_TRUSTED_PROXIES %w", err)
	}
	c.TrustedProxies, c.NoTrustedProxies = trustedProxies, noTrustedProxies
	if c.NoTrustedProxies && c.BehindTLSProxy {
		return c, errors.New("ISOFORM_RELAY_TRUSTED_PROXIES=none cannot be used with ISOFORM_RELAY_BEHIND_TLS_PROXY")
	}
	if c.BehindTLSProxy && rawTrustedProxies == "" {
		return c, errors.New("ISOFORM_RELAY_BEHIND_TLS_PROXY requires ISOFORM_RELAY_TRUSTED_PROXIES to name the proxy")
	}
	host, _, err := net.SplitHostPort(c.Address)
	if err != nil {
		return c, errors.New("ISOFORM_RELAY_ADDR must be an IP address and port")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return c, errors.New("ISOFORM_RELAY_ADDR must use a literal IP address")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return c, errors.New("TLS certificate and key must be configured together")
	}
	if c.BehindTLSProxy && c.TLSCert != "" {
		return c, errors.New("ISOFORM_RELAY_BEHIND_TLS_PROXY and a TLS certificate are exclusive")
	}
	if !ip.IsLoopback() && c.TLSCert == "" && !c.BehindTLSProxy {
		return c, errors.New("non-loopback listening requires TLS; bind loopback behind a private HTTPS proxy, or set ISOFORM_RELAY_BEHIND_TLS_PROXY=1 with ISOFORM_RELAY_TRUSTED_PROXIES")
	}
	return c, nil
}

// ParseTrustedProxies parses the relay's trusted proxy list. Empty keeps the
// loopback-only default; "none" disables forwarded client addresses.
func ParseTrustedProxies(value string) ([]*net.IPNet, bool, error) {
	proxies := strings.TrimSpace(value)
	if strings.EqualFold(proxies, "none") {
		return nil, true, nil
	}
	if strings.Contains(strings.ToLower(proxies), "none") {
		return nil, false, errors.New("=none cannot be combined with proxy entries")
	}
	if proxies == "" {
		proxies = "127.0.0.0/8,::1/128"
	}
	var networks []*net.IPNet
	for _, item := range strings.Split(proxies, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			if ip := net.ParseIP(item); ip != nil && ip.To4() != nil {
				item += "/32"
			} else {
				item += "/128"
			}
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return nil, false, errors.New("must list IP addresses or CIDR ranges")
		}
		networks = append(networks, network)
	}
	return networks, false, nil
}

// TLS reports whether the relay terminates TLS itself.
func (c Config) TLS() bool { return c.TLSCert != "" }
