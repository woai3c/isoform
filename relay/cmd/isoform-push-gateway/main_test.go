package main

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"isoform-relay/internal/pushgatewayauth"
)

func TestRunRequiresGatewayKeyFile(t *testing.T) {
	t.Setenv("ISOFORM_PUSH_GATEWAY_KEY_FILE", filepath.Join(t.TempDir(), "missing.key"))
	if err := run(nil); err == nil || !strings.Contains(err.Error(), "key file") {
		t.Fatalf("run without key file = %v", err)
	}
}

func TestGenerateKeyCommandCreatesPrivateFileAndRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.key")
	if err := run([]string{"-generate-key", path}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("key mode %o, want 0600", info.Mode().Perm())
	}
	if _, err := pushgatewayauth.LoadGatewayPrivateKey(path); err != nil {
		t.Fatalf("generated key rejected: %v", err)
	}
	if err := run([]string{"-generate-key", path}); err == nil {
		t.Fatal("existing key file overwritten")
	}
}

func TestPrintPublicKeyCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.key")
	if err := run([]string{"-generate-key", path}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISOFORM_PUSH_GATEWAY_KEY_FILE", path)
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	runErr := run([]string{"-print-public-key"})
	_ = writer.Close()
	os.Stdout = original
	if runErr != nil {
		t.Fatal(runErr)
	}
	output, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	key, err := pushgatewayauth.LoadGatewayPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(output)) != pushgatewayauth.PublicKeyString(key) {
		t.Fatalf("printed key %q", output)
	}
}

func TestGatewayTrustedProxySettingsAndLegacyFallback(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "gateway setting",
			env: map[string]string{
				"ISOFORM_PUSH_GATEWAY_TRUSTED_PROXIES": "172.17.0.1,10.0.0.0/8",
				"YANSU_RELAY_TRUSTED_PROXIES":          "192.0.2.1",
			},
			want: "172.17.0.1",
		},
		{
			name: "relay legacy fallback",
			env:  map[string]string{"YANSU_RELAY_TRUSTED_PROXIES": "192.0.2.1"},
			want: "192.0.2.1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			networks, noTrusted, err := gatewayTrustedProxySettings(test.env, func(string, ...any) {})
			if err != nil || noTrusted || len(networks) == 0 {
				t.Fatalf("trusted proxy settings = %v, %t, %v", networks, noTrusted, err)
			}
			parsedIP := net.ParseIP(test.want)
			if parsedIP == nil || !networks[0].Contains(parsedIP) {
				t.Fatalf("trusted proxy list %v does not contain %s", networks, test.want)
			}
		})
	}
	networks, noTrusted, err := gatewayTrustedProxySettings(map[string]string{"ISOFORM_PUSH_GATEWAY_TRUSTED_PROXIES": "none"}, nil)
	if err != nil || !noTrusted || len(networks) != 0 {
		t.Fatalf("disabled trusted proxies = %v, %t, %v", networks, noTrusted, err)
	}
	defaults, noTrusted, err := gatewayTrustedProxySettings(nil, nil)
	if err != nil || noTrusted || len(defaults) != 2 ||
		!defaults[0].Contains(net.ParseIP("127.0.0.1")) || !defaults[1].Contains(net.ParseIP("::1")) {
		t.Fatalf("default trusted proxies = %v, %t, %v", defaults, noTrusted, err)
	}
}

func gatewayTrustedProxySettings(values map[string]string, logf func(string, ...any)) ([]*net.IPNet, bool, error) {
	getenv := func(name string) string { return values[name] }
	if values == nil {
		getenv = func(string) string { return "" }
	}
	return loadTrustedProxySettings(getenv, logf)
}
