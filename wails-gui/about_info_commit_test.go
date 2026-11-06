package main

import "testing"

// ST85: a staging build names itself by its short commit; a release never does.
func TestAboutStagingCommitOnlyForStaging(t *testing.T) {
	saved := GitCommit
	t.Cleanup(func() { GitCommit = saved })
	GitCommit = "d1282d52e0123456789abcdef"
	if got := aboutStagingCommit("staging"); got != "d1282d52" {
		t.Fatalf("staging commit = %q, want the short commit", got)
	}
	if got := aboutStagingCommit("release"); got != "" {
		t.Fatalf("release build showed a commit: %q", got)
	}
	if got := aboutStagingCommit("development"); got != "" {
		t.Fatalf("development build showed a commit: %q", got)
	}
}
