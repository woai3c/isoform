package worktree

import (
	"os"
	"testing"
)

// TestMain points HOME at a fresh temporary directory so the service's data
// folder resolution (and any other home-relative path) can never touch the
// user's real folders. The git subprocesses the tests spawn inherit HOME and
// would lose the developer's ~/.gitconfig identity, so the committer identity
// comes from the environment instead.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("/tmp", "worktree-test-home-")
	if err != nil {
		home, err = os.MkdirTemp("", "worktree-test-home-")
	}
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	_ = os.Unsetenv("YANSU_AGENT_HOME")
	for _, pair := range [][2]string{
		{"GIT_AUTHOR_NAME", "Isoform Tests"},
		{"GIT_AUTHOR_EMAIL", "isoform-tests@localhost"},
		{"GIT_COMMITTER_NAME", "Isoform Tests"},
		{"GIT_COMMITTER_EMAIL", "isoform-tests@localhost"},
	} {
		if os.Getenv(pair[0]) == "" {
			_ = os.Setenv(pair[0], pair[1])
		}
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
