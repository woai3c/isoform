package main

import (
	"testing"
)

func TestParseGitRemoteURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantHost  string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "SSH SCP-style github",
			url:       "git@github.com:org/repo.git",
			wantHost:  "github.com",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "HTTPS github with .git",
			url:       "https://github.com/org/repo.git",
			wantHost:  "github.com",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "HTTPS github without .git",
			url:       "https://github.com/org/repo",
			wantHost:  "github.com",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "SSH SCP-style gitlab",
			url:       "git@gitlab.com:org/repo.git",
			wantHost:  "gitlab.com",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "SSH URL with port",
			url:       "ssh://git@github.com:22/org/repo.git",
			wantHost:  "github.com",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "HTTPS gitlab",
			url:       "https://gitlab.com/mygroup/myproject.git",
			wantHost:  "gitlab.com",
			wantOwner: "mygroup",
			wantRepo:  "myproject",
		},
		{
			name:      "git protocol",
			url:       "git://github.com/org/repo.git",
			wantHost:  "github.com",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "trailing whitespace",
			url:       "  git@github.com:org/repo.git  ",
			wantHost:  "github.com",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:    "empty string",
			url:     "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			url:     "   ",
			wantErr: true,
		},
		{
			name:    "no owner/repo path",
			url:     "https://github.com/",
			wantErr: true,
		},
		{
			name:    "malformed SCP no colon",
			url:     "git@github.com/org/repo.git",
			wantErr: true,
		},
		{
			name:      "SSH URL without port",
			url:       "ssh://git@github.com/org/repo.git",
			wantHost:  "github.com",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "custom host SCP-style",
			url:       "git@git.example.com:team/project.git",
			wantHost:  "git.example.com",
			wantOwner: "team",
			wantRepo:  "project",
		},
		{
			name:      "HTTPS bitbucket",
			url:       "https://bitbucket.org/team/project.git",
			wantHost:  "bitbucket.org",
			wantOwner: "team",
			wantRepo:  "project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, owner, repo, err := ParseGitRemoteURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseGitRemoteURL(%q) expected error, got host=%q owner=%q repo=%q", tt.url, host, owner, repo)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseGitRemoteURL(%q) unexpected error: %v", tt.url, err)
			}
			if host != tt.wantHost {
				t.Errorf("host = %q, want %q", host, tt.wantHost)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

func TestBuildRepoURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{
			name: "SSH to HTTPS",
			url:  "git@github.com:org/repo.git",
			want: "https://github.com/org/repo",
		},
		{
			name: "HTTPS with .git stripped",
			url:  "https://github.com/org/repo.git",
			want: "https://github.com/org/repo",
		},
		{
			name: "HTTPS already clean",
			url:  "https://github.com/org/repo",
			want: "https://github.com/org/repo",
		},
		{
			name: "gitlab SSH",
			url:  "git@gitlab.com:group/project.git",
			want: "https://gitlab.com/group/project",
		},
		{
			name: "SSH URL with port",
			url:  "ssh://git@github.com:22/org/repo.git",
			want: "https://github.com/org/repo",
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildRepoURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("BuildRepoURL(%q) expected error, got %q", tt.url, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildRepoURL(%q) unexpected error: %v", tt.url, err)
			}
			if got != tt.want {
				t.Fatalf("BuildRepoURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func Test_buildPRCreateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		head    string
		base    string
		want    string
		wantErr bool
	}{
		{
			name: "GitHub SSH",
			url:  "git@github.com:org/repo.git",
			head: "feature-branch",
			base: "main",
			want: "https://github.com/org/repo/compare/main...feature-branch?expand=1",
		},
		{
			name: "GitHub HTTPS",
			url:  "https://github.com/org/repo.git",
			head: "fix/bug-123",
			base: "develop",
			want: "https://github.com/org/repo/compare/develop...fix%2Fbug-123?expand=1",
		},
		{
			name: "GitLab SSH",
			url:  "git@gitlab.com:group/project.git",
			head: "feature",
			base: "main",
			want: "https://gitlab.com/group/project/-/merge_requests/new?merge_request[source_branch]=feature&merge_request[target_branch]=main",
		},
		{
			name: "GitLab HTTPS",
			url:  "https://gitlab.com/group/project.git",
			head: "fix/issue",
			base: "main",
			want: "https://gitlab.com/group/project/-/merge_requests/new?merge_request[source_branch]=fix%2Fissue&merge_request[target_branch]=main",
		},
		{
			name: "GitLab self-hosted",
			url:  "git@gitlab.mycompany.com:team/app.git",
			head: "feature",
			base: "main",
			want: "https://gitlab.mycompany.com/team/app/-/merge_requests/new?merge_request[source_branch]=feature&merge_request[target_branch]=main",
		},
		{
			name: "Bitbucket falls back to GitHub-style",
			url:  "git@bitbucket.org:team/repo.git",
			head: "feature",
			base: "main",
			want: "https://bitbucket.org/team/repo/compare/main...feature?expand=1",
		},
		{
			name:    "empty URL",
			url:     "",
			head:    "feature",
			base:    "main",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildPRCreateURL(tt.url, tt.head, tt.base)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("buildPRCreateURL(%q, %q, %q) expected error, got %q", tt.url, tt.head, tt.base, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildPRCreateURL(%q, %q, %q) unexpected error: %v", tt.url, tt.head, tt.base, err)
			}
			if got != tt.want {
				t.Fatalf("buildPRCreateURL(%q, %q, %q) = %q, want %q", tt.url, tt.head, tt.base, got, tt.want)
			}
		})
	}
}

func TestParseGitRemoteURLConsistency(t *testing.T) {
	// The same repo via different URL formats should produce identical host/owner/repo.
	urls := []string{
		"git@github.com:myorg/myrepo.git",
		"https://github.com/myorg/myrepo.git",
		"https://github.com/myorg/myrepo",
		"ssh://git@github.com/myorg/myrepo.git",
	}

	var firstHost, firstOwner, firstRepo string
	for i, u := range urls {
		host, owner, repo, err := ParseGitRemoteURL(u)
		if err != nil {
			t.Fatalf("ParseGitRemoteURL(%q) unexpected error: %v", u, err)
		}
		if i == 0 {
			firstHost, firstOwner, firstRepo = host, owner, repo
			continue
		}
		if host != firstHost || owner != firstOwner || repo != firstRepo {
			t.Fatalf("inconsistent parse for %q: got (%q,%q,%q), want (%q,%q,%q)",
				u, host, owner, repo, firstHost, firstOwner, firstRepo)
		}
	}
}
