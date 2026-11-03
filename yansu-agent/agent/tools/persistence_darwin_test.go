package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"yansu-agent/agent/authority"
)

func opaquePersistenceWrite(path string) string {
	return `python3 -c 'import base64;open(base64.b64decode("` + base64.StdEncoding.EncodeToString([]byte(path)) + `"),"w").write("changed")'`
}

func TestBashSeatbeltPersistenceWrites(t *testing.T) {
	home, project := sandboxFixture(t)
	paths := append([]string(nil), persistenceHomeFiles...)
	for _, dir := range persistenceHomeDirectories {
		paths = append(paths, dir+"/fixture")
	}
	paths = append(paths, "outside/.git/hooks/pre-commit", "outside/.git/config", "outside/bare.git/hooks/pre-receive")
	for _, relative := range paths {
		path := filepath.Join(home, relative)
		sandboxWrite(t, path, "original")
		result, err := NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": opaquePersistenceWrite(path)}})
		if err != nil || result.Metadata["exit_code"] == 0 || !strings.Contains(result.Content, "Operation not permitted") {
			t.Errorf("write did not reach kernel denial for %s: %v %+v", relative, err, result)
		}
		if body, err := os.ReadFile(path); err != nil || string(body) != "original" {
			t.Errorf("persistence file changed %s: %q %v", relative, body, err)
		}
	}
	// A startup file's existing symlink target gets the same write policy.
	outside := filepath.Join(project, "linked-startup")
	sandboxWrite(t, outside, "original")
	if err := os.Remove(filepath.Join(home, ".profile")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".profile")); err != nil {
		t.Fatal(err)
	}
	result, err := NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": opaquePersistenceWrite(outside)}})
	if err != nil || result.Metadata["exit_code"] == 0 {
		t.Fatalf("startup symlink target writable: %v %+v", err, result)
	}
	// Protect the symlink directory entry as well as its target: an opaque
	// process must not replace the link and create a new startup file there.
	startupLink := filepath.Join(home, ".profile")
	encoded := base64.StdEncoding.EncodeToString([]byte(startupLink))
	replace := `python3 -c 'import base64,os;p=base64.b64decode("` + encoded + `");os.unlink(p);open(p,"w").write("changed")'`
	result, err = NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": replace}})
	if err != nil || result.Metadata["exit_code"] == 0 || !strings.Contains(result.Content, "Operation not permitted") {
		t.Fatalf("startup symlink replacement did not reach kernel denial: %v %+v", err, result)
	}
	if target, err := os.Readlink(startupLink); err != nil || target != outside {
		t.Fatalf("startup symlink replaced: %q %v", target, err)
	}
	localHook := filepath.Join(project, ".git/hooks/pre-commit")
	sandboxWrite(t, localHook, "original")
	result, err = NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": opaquePersistenceWrite(localHook)}})
	if err != nil || result.Metadata["exit_code"] != 0 {
		t.Fatalf("work-folder hook should remain writable: %v %+v", err, result)
	}
}

func TestBashPersistenceApprovalIsPerCallAndTarget(t *testing.T) {
	home, project := sandboxFixture(t)
	startup := filepath.Join(home, ".zshrc")
	sandboxWrite(t, startup, "original")
	command := "printf approved > " + strconv.Quote(startup)
	call := ToolCall{WorkDir: project, Input: map[string]any{"command": command}, Authority: &AuthorityRuntime{Approved: true}}
	result, err := NewBashTool().Execute(context.Background(), call)
	if err != nil || result.Metadata["exit_code"] != 0 {
		t.Fatalf("approved startup write failed: %v %+v", err, result)
	}
	// Approval of the visible target must not open other persistence files
	// hidden inside an interpreter in the same shell invocation.
	gitConfig := filepath.Join(home, ".gitconfig")
	sandboxWrite(t, gitConfig, "original")
	call.Input = map[string]any{"command": command + "; " + opaquePersistenceWrite(gitConfig)}
	result, err = NewBashTool().Execute(context.Background(), call)
	if err != nil || result.Metadata["exit_code"] == 0 {
		t.Fatalf("approval escaped its named target: %v %+v", err, result)
	}
	call.Input = map[string]any{"command": opaquePersistenceWrite(startup)}
	call.Authority = nil
	result, err = NewBashTool().Execute(context.Background(), call)
	if err != nil || result.Metadata["exit_code"] == 0 {
		t.Fatalf("prior call persisted its approval: %v %+v", err, result)
	}
	call.Input = map[string]any{"command": command}
	call.Authority = &AuthorityRuntime{Approved: true, Context: authority.Context{Unattended: true}}
	if _, err := NewBashTool().Execute(context.Background(), call); err == nil {
		t.Fatal("unattended persistence write accepted an approval flag")
	}
	// An approved startup symlink cannot override an absolute secret denial.
	if err := os.Remove(startup); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".ssh/id_rsa"), startup); err != nil {
		t.Fatal(err)
	}
	call.Authority = &AuthorityRuntime{Approved: true}
	if _, err := NewBashTool().Execute(context.Background(), call); err == nil {
		t.Fatal("approved persistence write bypassed the private-key hard block")
	}
}
