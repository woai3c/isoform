package daemon

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/internal/config"
)

func TestServicePathsIsolateAgentHomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YANSU_APP_FLAVOR", "")
	for _, tc := range []struct {
		name, root, label, unit string
	}{
		{"published", filepath.Join(home, ".yansu-agent"), "ai.yansu.daemon", "yansu-daemon.service"},
		{"isoform", filepath.Join(home, ".isoform-agent"), "ai.isoform.daemon", "isoform-daemon.service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.AgentHomeEnv, tc.root)
			plist, err := launchdPlistPath()
			if want := filepath.Join(home, launchdPlistDir, tc.label+".plist"); err != nil || plist != want {
				t.Fatalf("plist = %q, %v; want %q", plist, err, want)
			}
			unit, err := systemdServicePath()
			if want := filepath.Join(home, systemdDir, tc.unit); err != nil || unit != want {
				t.Fatalf("unit = %q, %v; want %q", unit, err, want)
			}
			logs, err := ensureLogDir()
			if want := filepath.Join(tc.root, "logs"); err != nil || logs != want {
				t.Fatalf("logs = %q, %v; want %q", logs, err, want)
			}
		})
	}
}

func TestServiceIdentityCustomRootsAndAliases(t *testing.T) {
	home := t.TempDir()
	first := filepath.Join(home, "first")
	second := filepath.Join(home, "second")
	firstLabel, firstUnit := daemonIdentity(first, home)
	secondLabel, secondUnit := daemonIdentity(second, home)
	if firstLabel == secondLabel || firstUnit == secondUnit || firstLabel == "ai.yansu.daemon" || firstUnit == "yansu-daemon.service" {
		t.Fatalf("custom roots share service identity: %q %q / %q %q", firstLabel, firstUnit, secondLabel, secondUnit)
	}
	if label, unit := daemonIdentity(first, home); label != firstLabel || unit != firstUnit {
		t.Fatal("custom service identity is not stable")
	}
	published := filepath.Join(home, config.ConfigDirName)
	if err := os.MkdirAll(published, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(published, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if label, unit := daemonIdentity(alias, home); label != "ai.yansu.daemon" || unit != "yansu-daemon.service" {
		t.Fatalf("published alias acquired a separate service: %q %q", label, unit)
	}
}

func TestServiceDefinitionsPersistSelectedHome(t *testing.T) {
	root := filepath.Join(t.TempDir(), `Isoform & "custom" 100%`)
	t.Setenv(config.AgentHomeEnv, "") // The inferred home must also be persisted.
	t.Setenv(config.EnvBaseURL, "https://example.test/?a=1&b=2")
	plist := launchdServiceContents("/Applications/Isoform.app/bin/yansu", "relay", root, "ai.isoform.daemon")
	decoder := xml.NewDecoder(strings.NewReader(plist))
	values := map[string]string{}
	key := ""
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid launchd XML: %v", err)
		}
		if start, ok := token.(xml.StartElement); ok {
			switch start.Name.Local {
			case "key":
				if err := decoder.DecodeElement(&key, &start); err != nil {
					t.Fatal(err)
				}
			case "string":
				var value string
				if err := decoder.DecodeElement(&value, &start); err != nil {
					t.Fatal(err)
				}
				values[key] = value
			}
		}
	}
	if values[config.AgentHomeEnv] != root || values[config.EnvBaseURL] != os.Getenv(config.EnvBaseURL) {
		t.Fatalf("launchd lost environment: %v", values)
	}
	if values["StandardOutPath"] != filepath.Join(root, "logs", "daemon.log") {
		t.Fatalf("launchd log path = %q", values["StandardOutPath"])
	}
	unit := systemdServiceContents("/opt/Isoform & tools/yansu", "relay", root)
	for _, entry := range []string{config.AgentHomeEnv + "=" + root, config.EnvBaseURL + "=" + os.Getenv(config.EnvBaseURL)} {
		if want := "Environment=" + systemdQuote(entry) + "\n"; !strings.Contains(unit, want) {
			t.Fatalf("systemd missing persisted environment %q:\n%s", want, unit)
		}
	}
	if !strings.Contains(unit, "100%%") || strings.Contains(unit, "100%\"") {
		t.Fatalf("systemd failed to escape literal percent:\n%s", unit)
	}
}

func TestServicePathsRejectInvalidHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDir); err != nil {
			t.Error(err)
		}
	})
	t.Setenv(config.AgentHomeEnv, "relative")
	for name, resolve := range map[string]func() (string, error){
		"launchd": launchdPlistPath,
		"systemd": systemdServicePath,
		"logs":    ensureLogDir,
	} {
		t.Run(name, func(t *testing.T) {
			if path, err := resolve(); err == nil || path != "" {
				t.Fatalf("invalid home resolved to %q, %v", path, err)
			}
		})
	}
	t.Setenv(config.AgentHomeEnv, filepath.Join(home, "safe"))
	t.Setenv("HOME", "relative")
	t.Setenv("USERPROFILE", "relative")
	for _, resolve := range []func() (string, error){launchdPlistPath, systemdServicePath} {
		if path, err := resolve(); err == nil || path != "" {
			t.Fatalf("relative user home resolved service path %q, %v", path, err)
		}
	}
}
