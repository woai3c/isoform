package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func requireTrustedAnalyzeGit(t *testing.T) {
	t.Helper()
	info, err := os.Stat("/usr/bin/git")
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		t.Skip("Analyze Git fixtures require the trusted /usr/bin/git executable")
	}
}

func TestAnalyzeGitSnapshotDisablesSignatureHelpers(t *testing.T) {
	home, project := daemonAnalyzeFixture(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	createAnalyzeGitFixture(t, home, project)
	git := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command("/usr/bin/git", args...)
		cmd.Dir = project
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	tree := git("", "rev-parse", "HEAD^{tree}")
	commit := "tree " + tree + "\nauthor Isoform Test <test@example.invalid> 1700000000 +0000\ncommitter Isoform Test <test@example.invalid> 1700000000 +0000\ngpgsig -----BEGIN PGP SIGNATURE-----\n \n aGVsbG8=\n -----END PGP SIGNATURE-----\n\nsigned evidence fixture\n"
	hash := git(commit, "hash-object", "-t", "commit", "-w", "--stdin")
	git("", "update-ref", "HEAD", hash)
	marker := filepath.Join(home, "signature-helper-ran")
	helper := filepath.Join(project, "signature-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf invoked > "+strconv.Quote(marker)+"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	git("", "config", "--local", "gpg.program", helper)
	git("", "config", "--local", "log.showSignature", "true")
	// The fixture really invokes the verifier without our explicit override.
	git("", "--no-pager", "log", "-1", "--format=oneline")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("signature helper control did not execute: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snapshot, err := collectAnalyzeGitSnapshot(ctx, project)
	if err != nil || !strings.Contains(snapshot, hash) || !strings.Contains(snapshot, "signed evidence fixture") {
		t.Fatalf("guarded commit snapshot missing: %v %s", err, snapshot)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("signature helper escaped fixed snapshot config: %v", err)
	}
}

func TestAnalyzeGitSnapshotHonorsCancellation(t *testing.T) {
	home, project := daemonAnalyzeFixture(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	createAnalyzeGitFixture(t, home, project)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collectAnalyzeGitSnapshot(ctx, project); err == nil {
		t.Fatal("cancelled evidence collection succeeded")
	}
}

func TestAnalyzeGitSnapshotDoesNotFetchPromisedObjects(t *testing.T) {
	home, project := daemonAnalyzeFixture(t)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	createAnalyzeGitFixture(t, home, project)
	runGit := func(input string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/git", args...)
		cmd.Dir = project
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	git := func(input string, args ...string) string {
		t.Helper()
		out, err := runGit(input, args...)
		if err != nil {
			t.Fatalf("partial-clone fixture %v: %v: %s", args, err, out)
		}
		return out
	}
	missing := strings.Repeat("1", 40)
	tree := git("100644 blob "+missing+"\tmissing.txt\n", "mktree", "--missing")
	commit := "tree " + tree + "\nauthor Isoform Test <test@example.invalid> 1700000000 +0000\ncommitter Isoform Test <test@example.invalid> 1700000000 +0000\n\nmissing promised object fixture\n"
	hash := git(commit, "hash-object", "-t", "commit", "-w", "--stdin")
	git("", "update-ref", "HEAD", hash)
	// Mark the known commit/tree as promised while deliberately omitting the
	// blob. This is a real partial-clone object graph without any remote host.
	pack := git(hash+"\n"+tree+"\n", "pack-objects", ".git/objects/pack/pack")
	if err := os.WriteFile(filepath.Join(project, ".git/objects/pack/pack-"+pack+".promisor"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "transport-helper-ran")
	helper := filepath.Join(project, "transport-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf invoked > "+strconv.Quote(marker)+"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"core.repositoryformatversion", "1"}, {"extensions.partialClone", "origin"}, {"remote.origin.promisor", "true"}, {"remote.origin.url", "ext::" + helper}, {"protocol.ext.allow", "always"}} {
		git("", "config", "--local", pair[0], pair[1])
	}
	// The unguarded control must really try the configured helper. It only
	// writes a local marker and exits; no network service is started/contacted.
	_, _ = runGit("", "--no-pager", "log", "-1", "--patch")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("partial-clone control did not try its transport helper: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if _, err := collectAnalyzeGitSnapshot(context.Background(), project); err == nil {
		t.Fatal("missing promised object should make analysis unavailable")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("read-only analysis started a Git transport helper: %v", err)
	}
}
