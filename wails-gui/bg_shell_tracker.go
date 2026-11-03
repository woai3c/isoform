package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
	"yansu-agent/agent"

	"github.com/google/uuid"
)

// bgShellSessionInfo tracks the association between a background shell
// and the chat session that spawned it.
type bgShellSessionInfo struct {
	Tag       string // session local_id
	HandoffID string
	AttemptID string
	SessionID string
	WorkDir   string
	Model     string
	ShellID   string
	Command   string
	StartedAt time.Time
}

// bgShellFinish is a finished background script whose chat has not been
// resumed yet.
type bgShellFinish struct {
	info   bgShellSessionInfo
	exit   int
	status string
	// lineStored is set once the chat has the finished line, so a retried
	// resume does not store it twice.
	lineStored bool
}

// bgShellTracker keeps a mapping of active background shells to their
// owning chat sessions. This lets the App layer know which session to
// notify (and optionally auto-resume) when a background script finishes.
type bgShellTracker struct {
	mu     sync.Mutex
	shells map[string]*bgShellSessionInfo // shellID → info
	// pending holds finishes per chat (tag) that wait for the chat's run
	// to end before they resume it.
	pending map[string][]bgShellFinish
}

func newBgShellTracker() *bgShellTracker {
	return &bgShellTracker{
		shells:  make(map[string]*bgShellSessionInfo),
		pending: make(map[string][]bgShellFinish),
	}
}

// AddPending queues a finish for its chat.
func (t *bgShellTracker) AddPending(f bgShellFinish) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil {
		t.pending = make(map[string][]bgShellFinish)
	}
	t.pending[f.info.Tag] = append(t.pending[f.info.Tag], f)
}

// TakePending removes and returns the chat's queued finishes, oldest first.
func (t *bgShellTracker) TakePending(tag string) []bgShellFinish {
	t.mu.Lock()
	defer t.mu.Unlock()
	fs := t.pending[tag]
	delete(t.pending, tag)
	return fs
}

// RestorePending puts finishes back ahead of any queued since they were
// taken.
func (t *bgShellTracker) RestorePending(tag string, fs []bgShellFinish) {
	if len(fs) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil {
		t.pending = make(map[string][]bgShellFinish)
	}
	t.pending[tag] = append(append([]bgShellFinish{}, fs...), t.pending[tag]...)
}

// RemoveForSession forgets a deleted chat's shells and queued finishes, so a
// script that finishes later neither records nor resumes anything there.
func (t *bgShellTracker) RemoveForSession(tag string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, info := range t.shells {
		if info.Tag == tag {
			delete(t.shells, id)
		}
	}
	delete(t.pending, tag)
}

// Register associates a shellID with a session. Called from the
// executePrompt callback when the agent emits a Bash tool_use with
// run_in_background=true.
func (t *bgShellTracker) Register(shellID string, info bgShellSessionInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	info.ShellID = shellID
	info.StartedAt = time.Now()
	t.shells[shellID] = &info
	log.Printf("[bg-shell-tracker] registered shell=%s tag=%s cmd=%q", shellID, info.Tag, truncateCmd(info.Command, 80))
}

// Lookup returns the session info for a shell, or nil if unknown.
func (t *bgShellTracker) Lookup(shellID string) *bgShellSessionInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.shells[shellID]
}

// Remove deletes tracking for a shell.
func (t *bgShellTracker) Remove(shellID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.shells, shellID)
}

// Clear removes all tracked shells and queued finishes. Called on agent
// restart so stale shell entries from the previous runner do not linger into
// the new session.
func (t *bgShellTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shells = make(map[string]*bgShellSessionInfo)
	t.pending = make(map[string][]bgShellFinish)
}

// ActiveForSession returns all shells currently tracked for a given tag.
func (t *bgShellTracker) ActiveForSession(tag string) []bgShellSessionInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	var result []bgShellSessionInfo
	for _, info := range t.shells {
		if info.Tag == tag {
			result = append(result, *info)
		}
	}
	return result
}

// BgShellStatus is a frontend-facing struct for active background shells.
type BgShellStatus struct {
	ShellID   string `json:"shell_id"`
	Tag       string `json:"tag"`
	Command   string `json:"command"`
	StartedAt int64  `json:"started_at"` // unix millis
	Running   bool   `json:"running"`
}

// GetBackgroundShells returns the status of all tracked background shells.
// Exposed to the frontend via Wails binding.
func (a *App) GetBackgroundShells(tag string) []BgShellStatus {
	if a.bgShells == nil {
		return nil
	}
	active := a.bgShells.ActiveForSession(tag)
	result := make([]BgShellStatus, 0, len(active))
	for _, info := range active {
		result = append(result, BgShellStatus{
			ShellID:   info.ShellID,
			Tag:       info.Tag,
			Command:   truncateCmd(info.Command, 80),
			StartedAt: info.StartedAt.UnixMilli(),
			Running:   true,
		})
	}
	return result
}

// wireBgShellCallback sets up the BashTool.OnDone callback so the app
// is notified when background scripts finish. Should be called after
// StartAgent creates the runner.
func (a *App) wireBgShellCallback() {
	a.mu.Lock()
	runner := a.runner
	a.mu.Unlock()

	nr, ok := agent.NativeRunnerOf(runner)
	if !ok || nr == nil {
		return
	}

	nr.BashTool().SetOnDone(func(shellID string, exit int, status string, command string) {
		a.onBgShellDone(shellID, exit, status, command)
	})
}

// onBgShellDone is invoked (on a goroutine) when a background shell exits.
func (a *App) onBgShellDone(shellID string, exit int, status string, command string) {
	log.Printf("[bg-shell-done] shell=%s exit=%d status=%s cmd=%q", shellID, exit, status, truncateCmd(command, 80))

	info := a.bgShells.Lookup(shellID)
	if info == nil {
		log.Printf("[bg-shell-done] shell=%s not tracked, skipping", shellID)
		return
	}
	a.bgShells.Remove(shellID)
	a.onBgShellFinish(*info, exit, status, command)
}

// A captured callback can outlive Stop and a new attempt of the same chat.
func (a *App) onBgShellFinish(info bgShellSessionInfo, exit int, status, command string) {
	tag, shellID := info.Tag, info.ShellID
	a.handoffActionMu.Lock()
	if !a.bgShellAttemptCurrentLocked(info) {
		a.handoffActionMu.Unlock()
		return
	}

	// Emit a frontend event so the UI can update the banner.
	a.emitBgShellEvent(tag, shellID, exit, status, command)

	// A chat deleted while its script ran gets nothing: no record, and no
	// turn with tools in its folder.
	if !a.bgShellChatExists(tag) {
		log.Printf("[bg-shell-done] shell=%s tag=%s chat is gone, dropping", shellID, tag)
		a.handoffActionMu.Unlock()
		return
	}

	// Record the finish in the chat. History loading skips system rows, so
	// the model learns of it from the follow-up turn's prompt.
	if a.store != nil {
		a.PersistStreamItem(tag, StreamItemData{
			ID:      uuid.NewString(),
			Kind:    "system",
			Content: fmt.Sprintf("Background script finished (exit=%d). Command: %s", exit, truncateCmd(command, 80)),
		})
	}

	// Queue the finish and resume now, or when the chat's run ends.
	a.bgShells.AddPending(bgShellFinish{info: info, exit: exit, status: status})
	a.handoffActionMu.Unlock()
	a.resumeBgShells(tag)
}

// bgShellChatExists reports whether the chat is still stored. Without a store
// (tests, headless) there is nothing to check; a failed read is not taken as
// a deleted chat.
func (a *App) bgShellChatExists(tag string) bool {
	if a.store == nil {
		return true
	}
	_, err := a.store.LoadSession(context.Background(), tag)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		log.Printf("[bg-shell-done] tag=%s could not read the chat: %v", tag, err)
	}
	return true
}

// afterRunBgShells runs when a chat's run ends without being stopped
// (executePrompt): scripts that finished during the run resume the chat now.
// A stopped run (Stop, quitting or an agent restart) drops them in its defer;
// the system line stays as the record, and nothing resumes them after a
// relaunch.
func (a *App) afterRunBgShells(tag string) {
	if a.bgShells == nil {
		return
	}
	a.resumeBgShells(tag)
}

// bgShellResumeLines is the user line a finished background script leaves in
// its chat before the follow-up turn, per UI language. %d is the exit code.
var bgShellResumeLines = map[string]struct{ done, failed string }{
	"en":    {done: "Background script finished.", failed: "Background script finished with errors (exit=%d)."},
	"zh-CN": {done: "后台脚本已完成。", failed: "后台脚本已完成，但出现错误（exit=%d）。"},
}

// bgShellResumeRun replaces the follow-up turn in tests.
var bgShellResumeRun func(a *App, info bgShellSessionInfo, prompt string) error

// startBgShellResume starts the follow-up turn.
// Caller holds handoffActionMu, shared with Stop/timeout and attempt restart.
func (a *App) bgShellAttemptCurrentLocked(info bgShellSessionInfo) bool {
	if info.HandoffID == "" {
		return true
	}
	if a.handoffStore == nil || info.AttemptID == "" {
		return false
	}
	entry, err := a.handoffStore.Get(info.HandoffID)
	return err == nil && entry.ChatSessionID == info.Tag && entry.AttemptID == info.AttemptID && entry.Status != handoff.StatusAborted && entry.Status != handoff.StatusBlocked
}

func (a *App) startBgShellResume(info bgShellSessionInfo, prompt string) error {
	a.handoffActionMu.Lock()
	current := a.bgShellAttemptCurrentLocked(info)
	a.handoffActionMu.Unlock()
	if !current {
		return errors.New("background script attempt is no longer current")
	}

	if bgShellResumeRun != nil {
		return bgShellResumeRun(a, info, prompt)
	}
	return a.executePromptOwned(prompt, prompt, info.WorkDir, info.Model, info.SessionID, info.Tag, nil, false, false, "", nil, nil, "", "", promptOriginResume, &info)
}

// resumeBgShells stores the finished line of each queued script in the chat,
// shows it in every open window and starts one follow-up turn, once. It used
// to run in the renderer on a bg:shell:auto-resume event, and each open
// frontend (the app window and a browser preview) stored its own line and
// started its own turn. While the chat has a run going, the finishes wait
// for afterRunBgShells.
func (a *App) resumeBgShells(tag string) {
	if a.bgShells == nil {
		return
	}
	if a.handoffSessionStopped(tag) {
		a.bgShells.RemoveForSession(tag)
		return
	}
	if a.handoffSessionBusy(tag) {
		return
	}
	// Claim the chat before storing the lines, so a message sent meanwhile
	// cannot start a turn between the lines and their follow-up. A chat
	// deleted by now gets nothing; executePrompt refuses it too.
	a.mu.Lock()
	if a.chatDeleteGuardedLocked(tag) {
		a.mu.Unlock()
		a.bgShells.TakePending(tag)
		return
	}
	if a.activeTags[tag] {
		a.mu.Unlock()
		log.Printf("[bg-shell-done] tag=%s has a run going, resuming when it ends", tag)
		return
	}
	if a.activeTags == nil {
		a.activeTags = map[string]bool{}
	}
	a.activeTags[tag] = true
	a.mu.Unlock()
	release := func() {
		a.mu.Lock()
		delete(a.activeTags, tag)
		a.mu.Unlock()
	}

	finishes := a.bgShells.TakePending(tag)
	if len(finishes) == 0 {
		release()
		return
	}
	if !a.bgShellChatExists(tag) {
		release()
		log.Printf("[bg-shell-done] tag=%s chat is gone, dropping %d finished script(s)", tag, len(finishes))
		return
	}
	a.handoffActionMu.Lock()
	current := finishes[:0]
	for _, finish := range finishes {
		if a.bgShellAttemptCurrentLocked(finish.info) {
			current = append(current, finish)
		}
	}
	finishes = current
	if len(finishes) == 0 {
		a.handoffActionMu.Unlock()
		release()
		return
	}
	for i := range finishes {
		if finishes[i].lineStored {
			continue
		}
		if err := a.storeBgShellLine(finishes[i]); err != nil {
			// No turn without its line; the next run's end tries again.
			a.bgShells.RestorePending(tag, finishes)
			a.handoffActionMu.Unlock()
			release()
			log.Printf("[bg-shell-done] tag=%s could not store the finished line, not resuming: %v", tag, err)
			return
		}
		finishes[i].lineStored = true
	}
	a.handoffActionMu.Unlock()
	release()

	last := finishes[len(finishes)-1]
	err := a.startBgShellResume(last.info, bgShellResumePrompt(finishes))
	if errors.Is(err, errHandoffRunBusy) {
		a.bgShells.RestorePending(tag, finishes)
		// Verification owns the next wakeup. Recheck after restoring to cover
		// a check that finished just before the pending work was put back.
		if !a.handoffSessionBusy(tag) {
			go a.resumeBgShells(tag)
		}
		return
	}
	if errors.Is(err, errPromptAlreadyRunning) {
		// A message got in after the claim was released; its run resumes
		// these when it ends. If that run already ended before they went
		// back, nothing would, so resume now.
		a.bgShells.RestorePending(tag, finishes)
		a.mu.Lock()
		idle := !a.activeTags[tag]
		a.mu.Unlock()
		if idle {
			go a.resumeBgShells(tag)
		}
		return
	}
	if err != nil {
		log.Printf("[bg-shell-done] tag=%s resume failed: %v", tag, err)
	}
}

// storeBgShellLine stores a finish's user line and tells open windows.
func (a *App) storeBgShellLine(f bgShellFinish) error {
	if a.store == nil {
		return nil
	}
	lines := bgShellResumeLines[a.currentUILanguage()]
	line := lines.done
	if f.exit != 0 {
		line = fmt.Sprintf(lines.failed, f.exit)
	}
	row := storage.StreamItemRow{ID: uuid.NewString(), Kind: "user", Content: line}
	sortOrder, err := a.store.AppendStreamItem(context.Background(), f.info.Tag, row)
	if err != nil {
		return err
	}
	a.mainChatRowsChanged(f.info.Tag)
	a.emitBrowserEvent(mainChatItemsAppendedEvent, map[string]any{
		"sessionId": f.info.Tag,
		"itemId":    row.ID,
		"sortOrder": sortOrder,
		"kind":      row.Kind,
		"source":    "bg_shell",
	})
	return nil
}

// bgShellResumePrompt asks the model to pick up the finished scripts.
func bgShellResumePrompt(finishes []bgShellFinish) string {
	if len(finishes) == 1 {
		f := finishes[0]
		return fmt.Sprintf("Background script %s has finished (exit=%d, status=%s). Review the results and summarize what was accomplished.", f.info.ShellID, f.exit, f.status)
	}
	parts := make([]string, len(finishes))
	for i, f := range finishes {
		parts[i] = fmt.Sprintf("%s (exit=%d, status=%s)", f.info.ShellID, f.exit, f.status)
	}
	return fmt.Sprintf("Background scripts %s have finished. Review the results and summarize what was accomplished.", strings.Join(parts, ", "))
}

// emitBgShellEvent sends a bg_shell_done event to the frontend.
func (a *App) emitBgShellEvent(tag, shellID string, exit int, status, command string) {
	if a.ctx == nil {
		return
	}
	a.emitBrowserEvent("bg:shell:done", map[string]any{
		"tag":      tag,
		"shell_id": shellID,
		"exit":     exit,
		"status":   status,
		"command":  truncateCmd(command, 80),
	})
}

// registerBgShellFromToolUse extracts shell_id from a Bash tool_use
// event that used run_in_background and registers it in the tracker.
func (a *App) registerBgShellFromToolResult(tag, sessionID, workDir, model string, toolName string, result string, bashCmd string, owners ...*handoff.Entry) {
	if toolName != "Bash" {
		return
	}
	if !strings.Contains(result, "shell_id=") {
		return
	}
	shellID := extractShellID(result)
	if shellID == "" {
		return
	}
	cmd := bashCmd

	if a.bgShells == nil {
		return
	}
	info := bgShellSessionInfo{
		Tag:       tag,
		SessionID: sessionID,
		WorkDir:   workDir,
		Model:     model,
		Command:   cmd,
	}
	if len(owners) > 0 && owners[0] != nil {
		info.HandoffID, info.AttemptID = owners[0].ID, owners[0].AttemptID
	}
	a.bgShells.Register(shellID, info)

	if a.ctx != nil {
		a.emitBrowserEvent("bg:shell:registered", map[string]any{
			"tag":      tag,
			"shell_id": shellID,
		})
	}
}

func extractShellID(s string) string {
	idx := strings.Index(s, "shell_id=")
	if idx < 0 {
		return ""
	}
	rest := s[idx+len("shell_id="):]
	end := strings.IndexAny(rest, " \t\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
