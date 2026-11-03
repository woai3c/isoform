package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitHubSkillRef(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantOwner   string
		wantRepo    string
		wantRef     string
		wantSubdir  string
		wantInvalid bool
	}{
		{name: "shorthand", input: "anthropics/skills", wantOwner: "anthropics", wantRepo: "skills"},
		{name: "shorthand with ref", input: "anthropics/skills@v1.2.0", wantOwner: "anthropics", wantRepo: "skills", wantRef: "v1.2.0"},
		{name: "shorthand with subdir", input: "anthropics/skills#calendar", wantOwner: "anthropics", wantRepo: "skills", wantSubdir: "calendar"},
		{name: "shorthand with ref and subdir", input: "anthropics/skills@main#calendar/agent", wantOwner: "anthropics", wantRepo: "skills", wantRef: "main", wantSubdir: "calendar/agent"},
		{name: "https url", input: "https://github.com/anthropics/skills", wantOwner: "anthropics", wantRepo: "skills"},
		{name: "https tree url", input: "https://github.com/anthropics/skills/tree/main", wantOwner: "anthropics", wantRepo: "skills", wantRef: "main"},
		{name: "https tree url with subdir", input: "https://github.com/anthropics/skills/tree/main/calendar", wantOwner: "anthropics", wantRepo: "skills", wantRef: "main", wantSubdir: "calendar"},
		{name: "https url with .git", input: "https://github.com/anthropics/skills.git", wantOwner: "anthropics", wantRepo: "skills"},
		{name: "git ssh url", input: "git@github.com:anthropics/skills.git", wantOwner: "anthropics", wantRepo: "skills"},
		{name: "fragment subdir wins", input: "https://github.com/anthropics/skills#path=hello/world", wantOwner: "anthropics", wantRepo: "skills", wantSubdir: "hello/world"},

		{name: "missing repo", input: "anthropics", wantInvalid: true},
		{name: "wrong host", input: "https://gitlab.com/anthropics/skills", wantInvalid: true},
		{name: "path traversal subdir", input: "anthropics/skills#../etc", wantInvalid: true},
		{name: "invalid owner", input: "/skills", wantInvalid: true},
		{name: "ref with whitespace", input: "anthropics/skills@bad ref", wantInvalid: true},
		{name: "empty", input: "   ", wantInvalid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGitHubSkillRef(tt.input)
			if tt.wantInvalid {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("owner=%q, want %q", got.Owner, tt.wantOwner)
			}
			if got.Repo != tt.wantRepo {
				t.Errorf("repo=%q, want %q", got.Repo, tt.wantRepo)
			}
			if got.Ref != tt.wantRef {
				t.Errorf("ref=%q, want %q", got.Ref, tt.wantRef)
			}
			if got.Subdir != tt.wantSubdir {
				t.Errorf("subdir=%q, want %q", got.Subdir, tt.wantSubdir)
			}
		})
	}
}

func TestGitHubSkillSlug(t *testing.T) {
	tests := []struct {
		name string
		ref  githubSkillRef
		want string
	}{
		{name: "owner repo", ref: githubSkillRef{Owner: "Anthropics", Repo: "Skills"}, want: "gh-anthropics-skills"},
		{name: "with subdir", ref: githubSkillRef{Owner: "anthropics", Repo: "skills", Subdir: "calendar/agent"}, want: "gh-anthropics-skills-calendar-agent"},
		{name: "underscores collapsed", ref: githubSkillRef{Owner: "acme", Repo: "skill_pack", Subdir: "my_skill"}, want: "gh-acme-skill-pack-my-skill"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := githubSkillSlug(tt.ref)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if !clawHubSlugPattern.MatchString(got) {
				t.Fatalf("slug %q does not match skill slug pattern", got)
			}
		})
	}
}

func TestResolveGitHubSkillRoot(t *testing.T) {
	repoRoot := t.TempDir()
	subdir := filepath.Join(repoRoot, "calendar", "agent")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "SKILL.md"), []byte("# Calendar"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveGitHubSkillRoot(repoRoot, "calendar/agent")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != subdir {
		t.Errorf("got %q, want %q", got, subdir)
	}

	if _, err := resolveGitHubSkillRoot(repoRoot, "missing"); err == nil {
		t.Errorf("expected error for missing subdir")
	}
	if _, err := resolveGitHubSkillRoot(repoRoot, "../escape"); err == nil {
		t.Errorf("expected error for traversal")
	}
}

func TestFetchGitHubRepoInfoUsesEnvOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/foo/bar" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"default_branch":"trunk","description":"hello","html_url":"https://github.com/foo/bar"}`))
	}))
	defer server.Close()

	t.Setenv("YANSU_GITHUB_API_URL", server.URL)

	info, err := fetchGitHubRepoInfo(t.Context(), "foo", "bar")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if info.DefaultBranch != "trunk" {
		t.Errorf("default branch=%q, want trunk", info.DefaultBranch)
	}
	if !strings.Contains(info.Description, "hello") {
		t.Errorf("description=%q", info.Description)
	}
}

func TestSkillNameFromGitHub(t *testing.T) {
	got := skillNameFromGitHub(githubSkillRef{Owner: "anthropics", Repo: "skills", Subdir: "calendar"})
	if got != "anthropics/skills · calendar" {
		t.Fatalf("got %q", got)
	}
	got = skillNameFromGitHub(githubSkillRef{Owner: "anthropics", Repo: "skills"})
	if got != "anthropics/skills" {
		t.Fatalf("got %q", got)
	}
}
