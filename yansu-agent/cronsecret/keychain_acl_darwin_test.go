//go:build darwin && cgo

package cronsecret

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"yansu-agent/cronsecret/keychaintest"
)

func aclProbe(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, "trusted-yansu")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "clang", "-Wno-deprecated-declarations",
		"-framework", "Security", "-framework", "CoreFoundation", "-I", ".", "-o", path,
		"testdata/keychain_acl_probe.c")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile native Keychain probe: %v: %s", err, output)
	}
	cmd = exec.CommandContext(ctx, "/usr/bin/codesign", "--force", "--sign", "-", "--identifier", "com.isoform.cronsecret-test.probe", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sign native Keychain probe: %v: %s", err, output)
	}
	return path
}

type aclProbeExit int

func (code aclProbeExit) Error() string { return fmt.Sprintf("Keychain probe exited %d", code) }

func runACLProbe(t *testing.T, probe string, args ...string) (string, error) {
	t.Helper()
	// launchd can create the separate audit session without granting this
	// unprivileged test process permission to alter its own session. Every job
	// is transient, namespaced, and removed before the fixture HOME is cleaned.
	dir := t.TempDir()
	label := "com.isoform.cronsecret-test." + filepath.Base(dir) + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
	job := "user/" + strconv.Itoa(os.Getuid()) + "/" + label
	outPath, errPath := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
	var plist strings.Builder
	plist.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>`)
	fmt.Fprintf(&plist, `<key>Label</key><string>%s</string><key>ProgramArguments</key><array>`, label)
	for _, arg := range append([]string{probe}, args...) {
		fmt.Fprintf(&plist, `<string>%s</string>`, html.EscapeString(arg))
	}
	fmt.Fprintf(&plist, `</array><key>RunAtLoad</key><true/><key>SessionCreate</key><true/>
<key>LimitLoadToSessionType</key><string>Background</string><key>ProcessType</key><string>Background</string>
<key>EnvironmentVariables</key><dict><key>HOME</key><string>%s</string></dict>
<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string></dict></plist>`,
		html.EscapeString(os.Getenv("HOME")), html.EscapeString(outPath), html.EscapeString(errPath))
	path := filepath.Join(dir, "probe.plist")
	if err := os.WriteFile(path, []byte(plist.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	}
	if output, err := run("bootstrap", "user/"+strconv.Itoa(os.Getuid()), path); err != nil {
		if os.Getenv("CI") != "" {
			t.Skipf("CI isolated Keychain session unavailable: %v: %s", err, output)
		}
		t.Fatalf("start isolated Keychain probe session: %v: %s", err, output)
	}
	defer func() {
		if output, err := run("bootout", job); err != nil {
			t.Errorf("remove Keychain probe session: %v: %s", err, output)
		}
	}()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		state, err := run("print", job)
		if err != nil {
			t.Fatalf("inspect Keychain probe session: %v: %s", err, state)
		}
		for _, line := range strings.Split(string(state), "\n") {
			const prefix = "last exit code = "
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			code, err := strconv.Atoi(strings.TrimPrefix(line, prefix))
			if err != nil {
				continue
			}
			stdout, _ := os.ReadFile(outPath)
			stderr, _ := os.ReadFile(errPath)
			output := string(stdout) + string(stderr)
			if code >= 90 {
				t.Fatalf("Keychain probe setup failed (%d): %s", code, output)
			}
			if code != 0 {
				return output, aclProbeExit(code)
			}
			return output, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Keychain probe did not finish without interaction")
	return "", nil
}

func requireSecurityCannotRead(t *testing.T, probe, keychain, account, secret string) {
	t.Helper()
	service, err := runACLProbe(t, probe, "service", keychain, account)
	if err != nil {
		t.Fatalf("locate current Keychain service: %v", err)
	}
	for _, flag := range []string{"-g", "-w", "-ga"} {
		t.Run(flag, func(t *testing.T) {
			args := []string{"security", keychain, "/usr/bin/security", "find-generic-password", "-s", service}
			switch flag {
			case "-ga":
				args = append(args, flag, account)
			default:
				args = append(args, "-a", account, flag)
			}
			args = append(args, keychain)
			output, probeErr := runACLProbe(t, probe, args...)
			if strings.Contains(output, secret) {
				t.Fatal("untrusted security command read the Keychain credential without interaction")
			}
			// security returns the low byte of errSecInteractionNotAllowed
			// (-25308). Require the actual no-interaction denial, not a timeout,
			// missing-item error, or merely the absence of password output.
			if probeErr != aclProbeExit(36) {
				t.Fatalf("security command did not refuse the password read without interaction: %v", probeErr)
			}
		})
	}
}

func TestKeychainTrustsOnlyCreatorAndExplicitCLI(t *testing.T) {
	home, keychain := keychainHome(t)
	checkKeychainTrustedCLI(t, home, keychain)
}

func TestModernKeychainTrustsOnlyCreatorAndExplicitCLI(t *testing.T) {
	home := isolatedHome(t)
	dir := filepath.Join(home, "Library", "Keychains")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// macOS enables partition ACLs for Keychains in this directory. A fixture
	// directly below HOME uses the legacy format and misses this boundary.
	keychain := keychaintest.New(t, dir)
	probe := aclProbe(t, home)
	account, err := canonicalDir(home)
	if err != nil {
		t.Fatal(err)
	}
	secret := "fixture-only-modern-credential"
	requireKeychain(t, Store(home, secret, WithKeychain(keychain), WithTrustedCLI(probe)))
	output, err := runACLProbe(t, probe, "read", keychain, account)
	if err != aclProbeExit(1) || !strings.Contains(output, "native read refused: -25293") || strings.Contains(output, secret) {
		t.Fatalf("modern Keychain did not refuse the unapproved ad-hoc partition: %v: %s", err, output)
	}
	keychaintest.GrantCLI(t, keychain, account, probe)
	output, err = runACLProbe(t, probe, "read", keychain, account)
	if err != nil || output != secret {
		t.Fatalf("explicitly approved native CLI could not read its modern Keychain credential: %v: %s", err, output)
	}
	requireSecurityCannotRead(t, probe, keychain, account, secret)
	got, err := Load(home, WithKeychain(keychain))
	if err != nil || got != secret {
		t.Fatalf("CLI partition approval changed creator access: %v", err)
	}
}

func checkKeychainTrustedCLI(t *testing.T, home, keychain string) {
	t.Helper()
	probe := aclProbe(t, home)
	account, err := canonicalDir(home)
	if err != nil {
		t.Fatal(err)
	}
	secret := "fixture-only-creator-credential"
	requireKeychain(t, Store(home, secret, WithKeychain(keychain)))
	output, err := runACLProbe(t, probe, "read", keychain, account)
	if err == nil || strings.Contains(output, secret) {
		t.Fatal("an executable outside the ACL read the creator-only item")
	}
	requireSecurityCannotRead(t, probe, keychain, account, secret)

	requireKeychain(t, Store(home, secret, WithKeychain(keychain), WithTrustedCLI(probe)))
	output, err = runACLProbe(t, probe, "read", keychain, account)
	if err != nil || output != secret {
		t.Fatalf("explicit native CLI identity could not read its credential: %v", err)
	}
	requireSecurityCannotRead(t, probe, keychain, account, secret)

	// The desktop installs a byte-for-byte copy of its bundled CLI into its
	// tools directory. Trust follows the signature, not just a pathname.
	copyPath := filepath.Join(home, "installed-yansu")
	data, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, data, 0o700); err != nil {
		t.Fatal(err)
	}
	output, err = runACLProbe(t, copyPath, "read", keychain, account)
	if err != nil || output != secret {
		t.Fatalf("unchanged installed CLI identity could not read its credential: %v", err)
	}
}

func TestKeychainStoreRevokesLegacySecurityTrust(t *testing.T) {
	home, keychain := keychainHome(t)
	probe := aclProbe(t, home)
	account, err := canonicalDir(home)
	if err != nil {
		t.Fatal(err)
	}
	oldSecret := "fixture-only-insecure-credential"
	// Reproduce the previous release's ACL with a real item. This internal
	// fixture bypasses the desktop's fixed bundled-CLI path selection.
	requireKeychain(t, keychainWrite(account, oldSecret, keychain, "/usr/bin/security"))
	service, err := runACLProbe(t, probe, "service", keychain, account)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runACLProbe(t, probe, "security", keychain, "/usr/bin/security",
		"find-generic-password", "-s", service, "-a", account, "-w", keychain)
	if err != nil || !strings.Contains(output, oldSecret) {
		t.Fatalf("legacy ACL positive control did not allow security: %v", err)
	}
	secret := "fixture-only-upgraded-credential"
	requireKeychain(t, Store(home, secret, WithKeychain(keychain)))
	got, err := Load(home, WithKeychain(keychain))
	if err != nil || got != secret {
		t.Fatalf("creator could not read upgraded item: %v", err)
	}
	requireSecurityCannotRead(t, probe, keychain, account, secret)
}

func TestKeychainRejectsSymlinkCLIWithoutChangingCredential(t *testing.T) {
	home, keychain := keychainHome(t)
	requireKeychain(t, Store(home, "original-credential", WithKeychain(keychain)))
	link := filepath.Join(home, "yansu")
	if err := os.Symlink("/usr/bin/security", link); err != nil {
		t.Fatal(err)
	}
	if err := Store(home, "replacement", WithKeychain(keychain), WithTrustedCLI(link)); err == nil {
		t.Fatal("symlink CLI was accepted")
	}
	got, err := Load(home, WithKeychain(keychain))
	if err != nil || got != "original-credential" {
		t.Fatalf("rejected CLI changed the credential: %v", err)
	}
}

func TestKeychainAddFailurePreservesCurrentItem(t *testing.T) {
	home, keychain := keychainHome(t)
	requireKeychain(t, Store(home, "retained-secret", WithKeychain(keychain)))
	probe := aclProbe(t, home)
	account, err := canonicalDir(home)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture locks its real disposable Keychain at the native Add call,
	// then unlocks it. The original item must survive that actual Add refusal.
	output, err := runACLProbe(t, probe, "write-locked-add", keychain, account, "replacement")
	if err == nil || (!strings.Contains(output, "add_status=-25308") && !strings.Contains(output, "add_status=-25293")) {
		t.Fatalf("fixture did not reach an actual SecItemAdd refusal: %v: %s", err, output)
	}
	got, err := Load(home, WithKeychain(keychain))
	if err != nil || got != "retained-secret" {
		t.Fatalf("failed replacement lost the old credential: %v", err)
	}
}

func TestKeychainNewBinaryReplacesOlderCreatorAtLaunch(t *testing.T) {
	home, keychain := keychainHome(t)
	oldBinary := aclProbe(t, home)
	account, err := canonicalDir(home)
	if err != nil {
		t.Fatal(err)
	}
	oldSecret := "old-binary-secret"
	if output, err := runACLProbe(t, oldBinary, "seed", keychain, account, "0", "legacy", oldSecret, ""); err != nil {
		t.Fatalf("older signed binary could not create its item: %v: %s", err, output)
	}
	if output, err := runACLProbe(t, oldBinary, "read", keychain, account); err != nil || output != oldSecret {
		t.Fatalf("older signed creator could not read its own item: %v", err)
	}
	newSecret := "new-binary-secret"
	requireKeychain(t, Store(home, newSecret, WithKeychain(keychain)))
	if got, err := Load(home, WithKeychain(keychain)); err != nil || got != newSecret {
		t.Fatalf("new executable could not read its launch credential: %v", err)
	}
	if output, err := runACLProbe(t, oldBinary, "read", keychain, account); err == nil || strings.Contains(output, newSecret) {
		t.Fatal("old creator retained trust after the new executable rotated its credential")
	}
	requireSecurityCannotRead(t, oldBinary, keychain, account, newSecret)
	// A retained, inactive item owned by the old identity must not deadlock
	// the next startup. Rotate again after the ownership transition.
	requireKeychain(t, Store(home, "second-launch-secret", WithKeychain(keychain)))
	if got, err := Load(home, WithKeychain(keychain)); err != nil || got != "second-launch-secret" {
		t.Fatalf("later launch was blocked by the old owner: %v", err)
	}
}

func seedKeychainSlot(t *testing.T, probe, keychain, account string, slot int, generation, secret string) {
	t.Helper()
	creator, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if output, err := runACLProbe(t, probe, "seed", keychain, account, strconv.Itoa(slot), generation, secret, creator); err != nil {
		t.Fatalf("seed real Keychain slot: %v: %s", err, output)
	}
}

func TestKeychainRecoversInterruptedRetirement(t *testing.T) {
	home, keychain := keychainHome(t)
	probe := aclProbe(t, home)
	account, err := canonicalDir(home)
	if err != nil {
		t.Fatal(err)
	}
	seedKeychainSlot(t, probe, keychain, account, 0, "0000000000000001", "old-secret")
	seedKeychainSlot(t, probe, keychain, account, 1, "0000000000000002", "current-secret")
	if got, err := Load(home, WithKeychain(keychain)); err != nil || got != "current-secret" {
		t.Fatalf("interrupted rotation did not select newest credential: %v", err)
	}
	requireKeychain(t, Store(home, "next-secret", WithKeychain(keychain)))
	if got, err := Load(home, WithKeychain(keychain)); err != nil || got != "next-secret" {
		t.Fatalf("rotation could not reuse the stale slot: %v", err)
	}
}

func TestKeychainRejectsCorruptOrExhaustedGeneration(t *testing.T) {
	for _, fixture := range []struct {
		name, generation string
		both             bool
	}{
		{"malformed", "01", false},
		{"equal", "0000000000000001", true},
		{"exhausted", "ffffffffffffffff", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			home, keychain := keychainHome(t)
			probe := aclProbe(t, home)
			account, err := canonicalDir(home)
			if err != nil {
				t.Fatal(err)
			}
			seedKeychainSlot(t, probe, keychain, account, 0, fixture.generation, "preserved-secret")
			if fixture.both {
				seedKeychainSlot(t, probe, keychain, account, 1, fixture.generation, "other-secret")
			}
			if err := Store(home, "replacement", WithKeychain(keychain)); err == nil {
				t.Fatal("invalid or exhausted generation was overwritten")
			}
			got, err := Load(home, WithKeychain(keychain))
			switch fixture.name {
			case "exhausted":
				if err != nil || got != "preserved-secret" {
					t.Fatalf("exhausted generation lost its credential: %v", err)
				}
			default:
				if err == nil {
					t.Fatal("malformed generation did not fail closed")
				}
			}
		})
	}
}

func TestKeychainReadsLegacyNextAndIgnoresUnrelatedServices(t *testing.T) {
	home, keychain := keychainHome(t)
	probe := aclProbe(t, home)
	account, err := canonicalDir(home)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const unrelated = "com.example.unrelated"
	if output, err := runACLProbe(t, probe, "seed", keychain, account, unrelated, "01", "unrelated-secret", creator); err != nil {
		t.Fatalf("seed unrelated item: %v: %s", err, output)
	}
	seedKeychainSlot(t, probe, keychain, account, 1, "0000000000000003", "legacy-next-secret")
	if got, err := Load(home, WithKeychain(keychain)); err != nil || got != "legacy-next-secret" {
		t.Fatalf("legacy next service did not load independently of unrelated metadata: %v", err)
	}
	requireKeychain(t, Store(home, "replacement", WithKeychain(keychain)))
	if output, err := runACLProbe(t, probe, "read-service", keychain, account, unrelated); err != nil || output != "unrelated-secret" {
		t.Fatalf("rotation changed unrelated Keychain data: %v", err)
	}
}
