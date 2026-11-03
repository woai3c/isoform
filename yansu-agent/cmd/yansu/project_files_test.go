package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/internal/config"
	"yansu-agent/projectcontext"
)

// These unit fixtures exercise the real init/clone filesystem paths, using a
// local HTTP fixture and local Git repository; no signed-in service is used.
func TestCloneAndInitPreserveProjectAgentFiles(t *testing.T) {
	for _, operation := range []string{"clone", "init"} {
		for _, state := range []string{"absent", "user", "legacy"} {
			t.Run(fmt.Sprintf("%s/%s", operation, state), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv(config.AgentHomeEnv, filepath.Join(home, "state"))
				t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/orgs/acme/products/product/projects/project":
						io.WriteString(w, `{"uuid":"fixture","name":"project"}`)
					case "/api/orgs/acme/products/product/projects/project/stages":
						io.WriteString(w, `[{"name":"Requirements","type":"prd","content":"Project requirements"}]`)
					case "/api/orgs/acme/products/product/knowledge":
						io.WriteString(w, `[]`)
					default:
						t.Errorf("unexpected request: %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				if err := config.Save(&config.Config{BaseURL: server.URL}); err != nil {
					t.Fatal(err)
				}
				if err := config.SaveToken("fixture-token"); err != nil {
					t.Fatal(err)
				}

				project := t.TempDir()
				paths := []string{"CLAUDE.md", "AGENTS.md", ".cursorrules", ".windsurfrules", ".github/copilot-instructions.md"}
				original := "User instructions.\n"
				if state == "legacy" {
					original += "# Isoform Project Context\nLegacy text must stay.\n"
				}
				for _, path := range append(append([]string{}, paths...), ".gitignore") {
					if state == "absent" && path != ".gitignore" {
						continue
					}
					fullPath := filepath.Join(project, path)
					if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(fullPath, []byte(original), 0644); err != nil {
						t.Fatal(err)
					}
				}
				for _, args := range [][]string{{"init", project}, {"-C", project, "add", "."}, {"-C", project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "commit", "-m", "fixture"}} {
					if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
						t.Fatalf("git %v: %v: %s", args, err, out)
					}
				}

				if operation == "clone" {
					// Rewrite just this fixture's proxy URL to the local repository.
					t.Setenv("GIT_CONFIG_COUNT", "1")
					t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+project+".insteadOf")
					t.Setenv("GIT_CONFIG_VALUE_0", buildGitProxyURL(server.URL, "fixture-token", "acme", "product", "project"))
					parent := t.TempDir()
					projectTestChdir(t, parent)
					if err := runClone("acme/product/project"); err != nil {
						t.Fatal(err)
					}
					project = filepath.Join(parent, "project")
				} else {
					projectTestChdir(t, project)
					if err := runProjectInit("acme/product/project"); err != nil {
						t.Fatal(err)
					}
				}
				for _, path := range append(paths, ".gitignore") {
					data, err := os.ReadFile(filepath.Join(project, path))
					if state != "absent" || path == ".gitignore" {
						if err != nil || string(data) != original {
							t.Errorf("%s changed: %q, %v", path, data, err)
						}
					} else if !os.IsNotExist(err) {
						t.Errorf("%s created: %q, %v", path, data, err)
					}
				}
				if _, err := os.Stat(filepath.Join(project, ".something/project.json")); err != nil {
					t.Fatal(err)
				}
				hook, err := os.ReadFile(filepath.Join(project, ".git/hooks/post-commit"))
				if err != nil || !strings.Contains(string(hook), "hook run") || !strings.Contains(string(hook), "push --knowledge-only") {
					t.Fatalf("knowledge hook lost: %q, %v", hook, err)
				}
				prompt := projectcontext.WithPrompt("Fix the project", project)
				for _, want := range []string{"# Isoform Project Context", ".something/context.md", ".something/knowledge/_index.md", ".something/new-knowledge/", "Fix the project"} {
					if !strings.Contains(prompt, want) {
						t.Errorf("project prompt lost %q", want)
					}
				}
			})
		}
	}
}

func projectTestChdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Error(err)
		}
	})
}
