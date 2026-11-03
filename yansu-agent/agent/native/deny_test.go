package native

import "testing"

func TestMatchDeniedBashPrefix(t *testing.T) {
	prefixes := []string{"git push", "gh pr", "gh api", "gh release"}

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{"bare git push", "git push", "git push"},
		{"git push with args", "git push origin main --force", "git push"},
		{"extra whitespace", "  git   push  origin main", "git push"},
		{"chained with cd", "cd /repo && git push origin main", "git push"},
		{"semicolon chain", "echo ok; gh pr create --draft", "gh pr"},
		{"piped", "cat notes | gh api /repos/x/y/pulls -X POST", "gh api"},
		{"gh pr review approve", "gh pr review 42 --approve", "gh pr"},
		{"gh release", "gh release create v1.0.0", "gh release"},
		{"git pull is allowed", "git pull origin main", ""},
		{"gh issue view is allowed", "gh issue view 12", ""},
		{"substring not prefix", "echo git push", ""},
		{"pushd is not git push", "pushd /tmp", ""},
		{"empty command", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchDeniedBashPrefix(tt.command, prefixes); got != tt.want {
				t.Errorf("matchDeniedBashPrefix(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}
