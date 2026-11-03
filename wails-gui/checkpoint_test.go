package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

type rewindFileState struct {
	Content string
	Mode    fs.FileMode
	ModTime time.Time
}

func rewindTreeState(t *testing.T, root string) map[string]rewindFileState {
	t.Helper()
	state := map[string]rewindFileState{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var content []byte
		if !entry.IsDir() {
			content, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		state[path] = rewindFileState{string(content), info.Mode(), info.ModTime()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

func newRewindTestApp(t *testing.T, model string) (*App, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("CODEX_HOME", filepath.Join(root, ".codex"))
	t.Setenv(agentHomeEnv, filepath.Join(root, "isoform"))
	store, err := storage.New(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.SaveProject(ctx, storage.ProjectRow{ID: "project", Name: "Rewind test", Path: root}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(ctx, storage.SessionRow{LocalID: "chat", ProjectID: "project", SessionID: "external-thread", Model: model}); err != nil {
		t.Fatal(err)
	}
	for i, item := range []storage.StreamItemRow{
		{Kind: "user", Content: "kept question"}, {Kind: "assistant", Content: "kept answer"},
		{Kind: "user", Content: "removed question"}, {Kind: "assistant", Content: "removed answer"},
	} {
		item.ID = fmt.Sprintf("item-%d", i)
		if _, err := store.AppendStreamItem(ctx, "chat", item); err != nil {
			t.Fatal(err)
		}
	}
	// The rollout matches the old discovery glob and contains the exact rewind
	// target, so the previous implementation would rewrite this fixture.
	for path, content := range map[string]string{
		".codex/sessions/2026/10/21/rollout-test-external-thread.jsonl": "{\"type\":\"session_meta\"}\n{\"payload\":{\"role\":\"user\",\"content\":\"removed question\"}}\n{\"payload\":{\"role\":\"assistant\",\"content\":\"removed answer\"}}\n",
		".codex/config.toml":                             "model = 'user-choice'\n",
		".codex/sessions/unrelated.jsonl":                "unrelated history\n",
		".claude/projects/project/external-thread.jsonl": "user Claude transcript\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &App{ctx: ctx, store: store}, root
}

func TestRewindCheckpointLeavesAssistantHomesUntouched(t *testing.T) {
	for _, model := range []string{"gpt-5.2-codex:xhigh", "claude-sonnet"} {
		for _, checkpoint := range []string{"none", "files", "git"} {
			t.Run(fmt.Sprintf("%s/checkpoint=%s", model, checkpoint), func(t *testing.T) {
				app, root := newRewindTestApp(t, model)
				projectFile := filepath.Join(root, "work", "file.txt")
				if checkpoint != "none" {
					if err := os.MkdirAll(filepath.Dir(projectFile), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(projectFile, []byte("before"), 0600); err != nil {
						t.Fatal(err)
					}
					if checkpoint == "git" {
						workDir := filepath.Dir(projectFile)
						for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=Rewind test", "-c", "user.email=rewind@example.invalid", "commit", "-m", "Initial"}} {
							if err := gitRun(workDir, args...); err != nil {
								t.Fatal(err)
							}
						}
						t.Setenv("GIT_AUTHOR_NAME", "Rewind test")
						t.Setenv("GIT_AUTHOR_EMAIL", "rewind@example.invalid")
						t.Setenv("GIT_COMMITTER_NAME", "Rewind test")
						t.Setenv("GIT_COMMITTER_EMAIL", "rewind@example.invalid")
						commit, head, untracked, err := createGitSnapshot(workDir)
						if err != nil {
							t.Fatal(err)
						}
						if err := app.store.SaveGitSnapshot(context.Background(), storage.GitSnapshotRow{SessionLocalID: "chat", SortOrder: 2, StashSHA: commit, HeadSHA: head, UntrackedFiles: untracked, WorkDir: workDir}); err != nil {
							t.Fatal(err)
						}
					} else {
						app.maybeBackupFile("chat", 2, "Write", map[string]any{"file_path": projectFile}, map[string]bool{})
					}
					if err := os.WriteFile(projectFile, []byte("after"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				codexBefore := rewindTreeState(t, filepath.Join(root, ".codex"))
				claudeBefore := rewindTreeState(t, filepath.Join(root, ".claude"))
				if err := app.RewindSession("chat", "external-thread", filepath.Dir(projectFile), 2, "removed question"); err != nil {
					t.Fatal(err)
				}
				rows, err := app.store.LoadStreamItems(context.Background(), "chat")
				if err != nil || len(rows) != 2 || rows[0].Content != "kept question" || rows[1].Content != "kept answer" {
					t.Fatalf("rewound chat = %+v, err=%v", rows, err)
				}
				if !reflect.DeepEqual(codexBefore, rewindTreeState(t, filepath.Join(root, ".codex"))) ||
					!reflect.DeepEqual(claudeBefore, rewindTreeState(t, filepath.Join(root, ".claude"))) {
					t.Fatal("rewind touched the user's assistant files or directories")
				}
				if checkpoint != "none" {
					got, err := os.ReadFile(projectFile)
					if err != nil || string(got) != "before" {
						t.Fatalf("checkpoint restore = %q, err=%v", got, err)
					}
					remaining, err := app.store.LoadCheckpointFilesFrom(context.Background(), "chat", 2)
					if err != nil || len(remaining) != 0 {
						t.Fatalf("checkpoint cleanup = %+v, err=%v", remaining, err)
					}
				}
			})
		}
	}
}

func TestRewindNextTurnUsesRetainedIsoformHistory(t *testing.T) {
	for _, rewindAt := range []int{0, 2} {
		t.Run(fmt.Sprintf("rewind-at-%d", rewindAt), func(t *testing.T) {
			app, root := newRewindTestApp(t, "gpt-5.2-codex")
			if err := app.RewindSession("chat", "external-thread", "", rewindAt, "removed question"); err != nil {
				t.Fatal(err)
			}
			runner := wireHandoffPromptCapture(t, app)
			if err := app.SendPromptWithAttachments("replacement question", root, "gpt-5.2-codex", "external-thread", "chat", nil, nil, false, false, "", "", "", "en", ""); err != nil {
				t.Fatal(err)
			}
			var input *agent.TaskInput
			select {
			case input = <-runner.inputs:
			case <-time.After(5 * time.Second):
				t.Fatal("next turn did not reach runner")
			}
			prompt, history := input.PromptFunc()
			waitForTagIdle(t, app, "chat")
			if !strings.Contains(prompt, "replacement question") || strings.Contains(history, "removed") {
				t.Fatalf("next turn prompt=%q history=%q", prompt, history)
			}
			if rewindAt == 0 && history != "" {
				t.Fatalf("full rewind retained history: %q", history)
			}
			if rewindAt == 2 && (!strings.Contains(history, "kept question") || !strings.Contains(history, "kept answer")) {
				t.Fatalf("partial rewind lost retained turns: %q", history)
			}
		})
	}
}
