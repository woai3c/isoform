package tools

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Keep the exclusion boundary covered even when ripgrep integration tests
// cannot run. These cases call the real builder with no executable on PATH.
func TestProtectedGrepExclusionsWithoutRipgrep(t *testing.T) {
	t.Setenv("PATH", "")
	t.Run("home secrets and source text", func(t *testing.T) {
		home, project := hardBlockHome(t)
		writeFile(t, filepath.Join(project, "safe.go"), "cron-secret is a protected filename\n")
		writeFile(t, filepath.Join(project, "logins.json"), "project fixture\n")
		assertProtectedGrepExclusions(t, home, home, []string{
			"!**/.ssh/id_rsa",
			"!**/Library/Keychains",
			"!**/Library/Application Support/Google/Chrome/Default/Cookies",
			"!**/.isoform-agent/cron-secret",
		}, []string{
			"!**/.ssh", "!**/.ssh/id_rsa.pub", "!**/.ssh/known_hosts", "!**/.ssh/config",
			"!**/project", "!**/project/safe.go", "!**/project/logins.json",
		})
	})
	t.Run("literal metacharacters", func(t *testing.T) {
		home, _ := hardBlockHome(t)
		data := filepath.Join(home, "data[1]{x}?")
		t.Setenv("YANSU_AGENT_HOME", data)
		writeFile(t, filepath.Join(data, "cron-secret"), "PRIVATE_SENTINEL")
		writeFile(t, filepath.Join(home, "safe.txt"), "safe line")
		assertProtectedGrepExclusions(t, home, home, []string{
			`!**/data\[1\]\{x\}\?/cron-secret`,
		}, []string{"!**/data[1]{x}?/cron-secret", "!**/safe.txt"})
	})
	t.Run("symlinked search root", func(t *testing.T) {
		home, project := hardBlockHome(t)
		alias := filepath.Join(project, "home-alias")
		if err := os.Symlink(home, alias); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(project, "safe.txt"), "safe line")
		assertProtectedGrepExclusions(t, alias, home, []string{
			"!**/Library/Keychains", "!**/.ssh/id_rsa", "!**/.isoform-agent/cron-secret",
		}, []string{"!**/project", "!**/project/safe.txt"})
	})
	t.Run("credential subroots", func(t *testing.T) {
		home, _ := hardBlockHome(t)
		for _, tc := range []struct{ root, exclusion string }{
			{".config", "!**/gh/hosts.yml"},
			{".aws", "!**/credentials"},
			{".mozilla/firefox/p", "!**/logins.json"},
			{".docker", "!**/config.json"},
			{".kube", "!**/config"},
		} {
			t.Run(tc.root, func(t *testing.T) {
				root := filepath.Join(home, filepath.FromSlash(tc.root))
				assertProtectedGrepExclusions(t, root, home, []string{tc.exclusion}, nil)
			})
		}
	})
}

func assertProtectedGrepExclusions(t *testing.T, root, workDir string, want, allowed []string) {
	t.Helper()
	globs, err := protectedGrepExclusions(context.Background(), root, workDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, exclusion := range want {
		if !slices.Contains(globs, exclusion) {
			t.Errorf("missing protected exclusion %q: %q", exclusion, globs)
		}
	}
	for _, exclusion := range allowed {
		if slices.Contains(globs, exclusion) {
			t.Errorf("unexpected exclusion %q: %q", exclusion, globs)
		}
	}
}
