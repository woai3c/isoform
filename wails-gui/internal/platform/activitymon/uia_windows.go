//go:build windows

package activitymon

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

//go:embed uia_windows.ps1
var uiaScript string

// uiaRequest is one JSON line written to the PowerShell subprocess stdin.
// See uia_windows.ps1 for protocol details.
type uiaRequest struct {
	Mode   string     `json:"mode"`             // "text" | "tree" | "tree_by_hwnd"
	HWND   int64      `json:"hwnd,omitempty"`   // 0 = foreground
	Budget *uiaBudget `json:"budget,omitempty"` // optional walk limits
}

type uiaBudget struct {
	MaxNodes int `json:"maxNodes,omitempty"`
	MaxDepth int `json:"maxDepth,omitempty"`
	MaxBytes int `json:"maxBytes,omitempty"`
}

type uiaTextResponse struct {
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

type uiaTreeResponse struct {
	Tree      json.RawMessage `json:"tree"`
	NodeCount int             `json:"nodeCount"`
	Truncated bool            `json:"truncated"`
	Error     string          `json:"error,omitempty"`
}

// uiaEngine wraps a persistent PowerShell subprocess that exposes the
// Windows UI Automation tree. Mirrors winRTEngine (ocr_windows.go) in
// shape so failures, restarts, and concurrency follow the same model.
type uiaEngine struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	scanner    *bufio.Scanner
	scriptPath string
	disabled   bool      // permanently disabled after a fatal init error
	lastErrLog time.Time // throttles the "subprocess failed" log
}

// global engine. UIA walks are infrequent (~once per capture) and reading
// the foreground window's tree must serialize against concurrent walks
// anyway — System.Windows.Automation's underlying COM object is reentrant
// but Slack-class trees saturate one core per walk so parallel calls just
// hurt latency.
var (
	globalUIA  *uiaEngine
	globalUIAM sync.Mutex
)

func getUIA() *uiaEngine {
	globalUIAM.Lock()
	defer globalUIAM.Unlock()
	if globalUIA == nil {
		globalUIA = &uiaEngine{}
	}
	return globalUIA
}

// startProcess writes the embedded PowerShell script to a temp file (so
// `-File` works) and spawns powershell.exe with the same flags as the OCR
// engine.
func (e *uiaEngine) startProcess() error {
	if e.disabled {
		return fmt.Errorf("uia: engine permanently disabled")
	}
	if e.scriptPath == "" {
		f, err := os.CreateTemp("", "yansu-uia-*.ps1")
		if err != nil {
			return fmt.Errorf("uia: create script file: %w", err)
		}
		if _, err := f.WriteString(uiaScript); err != nil {
			f.Close()
			os.Remove(f.Name())
			return fmt.Errorf("uia: write script: %w", err)
		}
		f.Close()
		e.scriptPath = f.Name()
	}

	cmd := exec.Command("powershell.exe",
		"-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-File", e.scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("uia: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("uia: stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("uia: start powershell: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	// Allow up to 4 MB per response — large Electron trees can serialize
	// to ~1 MB and we leave headroom for any future cache additions.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	if !scanner.Scan() {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("uia: powershell init failed (no output)")
	}
	if line := scanner.Text(); line != "READY" {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("uia: init failed: %s", line)
	}

	e.cmd = cmd
	e.stdin = stdin
	e.scanner = scanner
	log.Printf("[uia] PowerShell UIA process started (pid %d)", cmd.Process.Pid)
	return nil
}

func (e *uiaEngine) killProcess() {
	if e.cmd != nil {
		if e.stdin != nil {
			e.stdin.Close()
		}
		_ = e.cmd.Process.Kill()
		_ = e.cmd.Wait()
		e.cmd = nil
		e.stdin = nil
		e.scanner = nil
	}
}

func (e *uiaEngine) ensureProcess() error {
	if e.cmd != nil {
		return nil
	}
	return e.startProcess()
}

// uiaCallTimeout caps how long we wait for a single PowerShell response.
// A capture pipeline call that hangs longer than this is treated as a
// dead subprocess: we kill it, the next call starts a fresh one, and
// the activity monitor (which calls UIA from a goroutine, not the main
// thread) absorbs the miss as an empty tree. Without this, a stuck
// PowerShell would pile up goroutines on e.mu indefinitely — and once
// dozens of capture goroutines accumulate, OCR + screenshot encoding
// thrash, which on Windows has historically taken the WebView2 message
// pump (and therefore the tray) down with it.
//
// 8s budget rationale: empirically a cold first call on a huge Cursor
// / Slack tree takes ~3s; warm calls are <500ms. 8s catches genuine
// hangs without firing on legitimately slow tree walks.
const uiaCallTimeout = 8 * time.Second

// callRaw runs one request/response round trip. The caller must hold e.mu.
// On any read/write/timeout error the subprocess is killed; the next call
// will transparently restart it (one retry per call).
func (e *uiaEngine) callRaw(req uiaRequest) ([]byte, error) {
	if err := e.ensureProcess(); err != nil {
		return nil, err
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("uia: marshal request: %w", err)
	}
	if _, err := fmt.Fprintf(e.stdin, "%s\n", reqJSON); err != nil {
		e.killProcess()
		if err := e.startProcess(); err != nil {
			return nil, err
		}
		if _, err := fmt.Fprintf(e.stdin, "%s\n", reqJSON); err != nil {
			e.killProcess()
			return nil, fmt.Errorf("uia: write after restart: %w", err)
		}
	}
	return e.readResponseWithTimeout()
}

// readResponseWithTimeout reads one line from the PowerShell stdout
// scanner, treating a uiaCallTimeout-second silence as a hung subprocess
// (kill + return error). bufio.Scanner has no native deadline, so we
// move the Scan call to a worker goroutine and race it against a timer.
// On timeout the subprocess is killed, which unblocks the goroutine's
// Scan via EOF — leaking it would be safe (one-shot) but killing first
// keeps the leak window <1s.
func (e *uiaEngine) readResponseWithTimeout() ([]byte, error) {
	type scanResult struct {
		ok   bool
		text string
		err  error
	}
	done := make(chan scanResult, 1)
	scanner := e.scanner
	go func() {
		ok := scanner.Scan()
		done <- scanResult{ok: ok, text: scanner.Text(), err: scanner.Err()}
	}()

	select {
	case res := <-done:
		if !res.ok {
			e.killProcess()
			if res.err != nil {
				return nil, fmt.Errorf("uia: read response: %w", res.err)
			}
			return nil, fmt.Errorf("uia: process exited unexpectedly")
		}
		return []byte(res.text), nil
	case <-time.After(uiaCallTimeout):
		e.killProcess()
		// Drain the scan goroutine so it doesn't outlive the call —
		// killProcess closed stdout, so Scan should return immediately.
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
		return nil, fmt.Errorf("uia: response timed out after %s", uiaCallTimeout)
	}
}

func (e *uiaEngine) logErrorThrottled(format string, args ...interface{}) {
	e.mu.Lock()
	since := time.Since(e.lastErrLog)
	e.lastErrLog = time.Now()
	e.mu.Unlock()
	if since > 30*time.Second {
		log.Printf(format, args...)
	}
}

// CollectText reads the flat text of a UIA element subtree.
func (e *uiaEngine) CollectText(hwnd int64) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	respBytes, err := e.callRaw(uiaRequest{Mode: "text", HWND: hwnd})
	if err != nil {
		e.logErrorThrottled("[uia] text walk failed: %v", err)
		return ""
	}
	var resp uiaTextResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		e.logErrorThrottled("[uia] text parse failed: %v (raw: %s)", err, truncForLog(respBytes))
		return ""
	}
	if resp.Error != "" {
		return ""
	}
	return resp.Text
}

// CollectTree returns the raw AXNode JSON of a UIA element subtree, plus
// the node count and a truncation flag. Empty JSON on error.
func (e *uiaEngine) CollectTree(hwnd int64) (rawJSON []byte, nodeCount int, truncated bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	mode := "tree"
	if hwnd != 0 {
		mode = "tree_by_hwnd"
	}
	respBytes, err := e.callRaw(uiaRequest{Mode: mode, HWND: hwnd})
	if err != nil {
		e.logErrorThrottled("[uia] tree walk failed: %v", err)
		return nil, 0, false
	}
	var resp uiaTreeResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		e.logErrorThrottled("[uia] tree parse failed: %v (raw: %s)", err, truncForLog(respBytes))
		return nil, 0, false
	}
	if resp.Error != "" {
		return nil, 0, false
	}
	return resp.Tree, resp.NodeCount, resp.Truncated
}

func truncForLog(b []byte) string {
	const lim = 256
	if len(b) <= lim {
		return string(b)
	}
	return string(b[:lim]) + "...(truncated)"
}
