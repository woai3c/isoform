package config

import (
	"net"
	"strings"
	"testing"
)

func load(values map[string]string) (Config, error) {
	return Load(func(key string) string { return values[key] })
}

func TestDefaultsAreLoopbackPlaintextAndPrivateState(t *testing.T) {
	c, err := load(nil)
	if err != nil || c.Address != "127.0.0.1:8791" || c.StateDir != "./data" || c.TLS() {
		t.Fatalf("defaults %+v %v", c, err)
	}
}

func TestNonLoopbackRequiresTLS(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8791", "[::]:8791", "10.0.0.5:8791"} {
		if _, err := load(map[string]string{"ISOFORM_RELAY_ADDR": address}); err == nil || !strings.Contains(err.Error(), "requires TLS") {
			t.Errorf("plaintext non-loopback %s accepted: %v", address, err)
		}
	}
	c, err := load(map[string]string{"ISOFORM_RELAY_ADDR": "0.0.0.0:8791", "ISOFORM_RELAY_TLS_CERT": "cert.pem", "ISOFORM_RELAY_TLS_KEY": "key.pem"})
	if err != nil || !c.TLS() {
		t.Fatalf("TLS listener rejected: %v", err)
	}
}

func TestAddressMustBeLiteralIP(t *testing.T) {
	for _, address := range []string{"localhost:8791", "relay.example:8791", "bad", "127.0.0.1"} {
		if _, err := load(map[string]string{"ISOFORM_RELAY_ADDR": address}); err == nil {
			t.Errorf("accepted address %q", address)
		}
	}
}

func TestPartialTLSIsRejected(t *testing.T) {
	if _, err := load(map[string]string{"ISOFORM_RELAY_TLS_CERT": "cert.pem"}); err == nil {
		t.Fatal("certificate without key accepted")
	}
	if _, err := load(map[string]string{"ISOFORM_RELAY_TLS_KEY": "key.pem"}); err == nil {
		t.Fatal("key without certificate accepted")
	}
}

func TestTrustedProxies(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	c, err := Load(get)
	if err != nil || len(c.TrustedProxies) != 2 || !c.TrustedProxies[0].Contains(net.ParseIP("127.0.0.1")) {
		t.Fatalf("default = %v %v", c.TrustedProxies, err)
	}
	env["ISOFORM_RELAY_TRUSTED_PROXIES"] = "172.17.0.1, 10.0.0.0/8"
	c, err = Load(get)
	if err != nil || len(c.TrustedProxies) != 2 || !c.TrustedProxies[1].Contains(net.ParseIP("10.1.2.3")) || c.TrustedProxies[0].Contains(net.ParseIP("172.17.0.2")) {
		t.Fatalf("configured = %v %v", c.TrustedProxies, err)
	}
	env["ISOFORM_RELAY_TRUSTED_PROXIES"] = "proxy.example"
	if _, err := Load(get); err == nil {
		t.Fatal("a name is not an address")
	}
}

func TestTrustedProxiesNone(t *testing.T) {
	c, err := load(map[string]string{"ISOFORM_RELAY_TRUSTED_PROXIES": "none"})
	if err != nil || !c.NoTrustedProxies || len(c.TrustedProxies) != 0 {
		t.Fatalf("none = %+v, %v", c, err)
	}
	for _, proxies := range []string{"none,127.0.0.1", "127.0.0.1,none", "none,"} {
		if _, err := load(map[string]string{"ISOFORM_RELAY_TRUSTED_PROXIES": proxies}); err == nil {
			t.Errorf("accepted mixed no-proxy value %q", proxies)
		}
	}
	if _, err := load(map[string]string{"ISOFORM_RELAY_TRUSTED_PROXIES": "none", "ISOFORM_RELAY_BEHIND_TLS_PROXY": "1"}); err == nil {
		t.Fatal("accepted none with a TLS proxy")
	}
}

func TestBehindTLSProxyAllowsPlaintextOffLoopbackWithNamedProxies(t *testing.T) {
	env := map[string]string{"ISOFORM_RELAY_ADDR": "0.0.0.0:8080", "ISOFORM_RELAY_BEHIND_TLS_PROXY": "1"}
	if _, err := load(env); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Fatalf("proxy mode without named proxies accepted: %v", err)
	}
	env["ISOFORM_RELAY_TRUSTED_PROXIES"] = "fdaa::/16,172.16.0.0/12"
	c, err := load(env)
	if err != nil || !c.BehindTLSProxy || c.TLS() || len(c.TrustedProxies) != 2 {
		t.Fatalf("proxy mode = %+v %v", c, err)
	}
	env["ISOFORM_RELAY_TLS_CERT"], env["ISOFORM_RELAY_TLS_KEY"] = "cert.pem", "key.pem"
	if _, err := load(env); err == nil {
		t.Fatal("proxy mode together with a certificate accepted")
	}
	if _, err := load(map[string]string{"ISOFORM_RELAY_BEHIND_TLS_PROXY": "maybe"}); err == nil {
		t.Fatal("non-boolean proxy flag accepted")
	}
}

func TestClientIPHeaderLogFormatAndMaxConns(t *testing.T) {
	c, err := load(nil)
	if err != nil || c.ClientIPHeader != "X-Forwarded-For" || c.LogFormat != "text" || c.MaxConns != DefaultMaxConns {
		t.Fatalf("defaults = %+v %v", c, err)
	}
	c, err = load(map[string]string{"ISOFORM_RELAY_CLIENT_IP_HEADER": "Fly-Client-IP", "ISOFORM_RELAY_LOG_FORMAT": "JSON", "ISOFORM_RELAY_MAX_CONNS": "512"})
	if err != nil || c.ClientIPHeader != "Fly-Client-IP" || c.LogFormat != "json" || c.MaxConns != 512 {
		t.Fatalf("configured = %+v %v", c, err)
	}
	for key, bad := range map[string]string{
		"ISOFORM_RELAY_CLIENT_IP_HEADER": "Fly Client IP",
		"ISOFORM_RELAY_LOG_FORMAT":       "yaml",
		"ISOFORM_RELAY_MAX_CONNS":        "0",
	} {
		if _, err := load(map[string]string{key: bad}); err == nil {
			t.Errorf("%s=%q accepted", key, bad)
		}
	}
	if _, err := load(map[string]string{"ISOFORM_RELAY_MAX_CONNS": "many"}); err == nil {
		t.Error("non-numeric connection cap accepted")
	}
}
