//go:build windows

package ocr

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.DecodeConfig
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"wails-gui/internal/apphome"
)

// paddleEngine implements Engine by talking to a bundled PaddleOCR-json
// subprocess. We manage the process ourselves instead of using
// github.com/doraemonkeys/paddleocr because that wrapper's exec.Command call
// does not set SysProcAttr.HideWindow / CREATE_NO_WINDOW, which makes
// PaddleOCR-json.exe pop a black console window every time Yansu.exe (a
// Wails GUI app) launches it. Mirroring the WinRT PowerShell setup in
// ocr_windows.go avoids that.
//
// Protocol (newline-delimited JSON over stdin/stdout, defined by
// PaddleOCR-json: https://github.com/hiroi-sora/PaddleOCR-json):
//
//   stderr (during init): many lines of MKLDNN kernel-fusion logs ending
//                         with "OCR init completed." — used as the ready marker.
//   stdin  request:       {"image_path":"C:\\...\\frame.jpg"}\n
//   stdout response:      {"code":100,"data":[{"box":[[x,y]x4],"score":0.99,"text":"..."}]}\n
//   no text response:     {"code":101,"data":"..."}\n
//   other error:          {"code":<n>,"data":"<msg>"}\n

const (
	paddleInitTimeout = 60 * time.Second
	// paddleCallTimeout bounds one OCR request. The model is loaded once at
	// init, so a call that takes this long means the sidecar is hung.
	paddleCallTimeout = 60 * time.Second
	// paddleStaleTempDirAge: temp folders from older builds (no pid in the
	// name) untouched this long are removed when an engine starts; folders
	// named with a pid go as soon as that process is gone.
	paddleStaleTempDirAge = time.Hour
	paddleTempDirPrefix   = "yansu-paddle-ocr-"
	paddleInitMarker      = "OCR init completed."
	paddleRestartInterval = 20 * time.Minute // upstream memory-leak workaround
	paddleStdoutMaxLine   = 4 * 1024 * 1024  // 4 MiB; large pages can produce thousands of detected lines
)

type paddleEngine struct {
	exe        string // absolute path to PaddleOCR-json.exe
	configPath string // absolute path to models/config_chinese.txt

	mu          sync.Mutex
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdoutPipe  io.ReadCloser // retained so we can close on kill
	scanner     *bufio.Scanner
	stderrBuf   *boundedBuffer // captures subprocess stderr for diagnostics
	tmpDir      string
	nextRestart time.Time
}

type paddleResponse struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data,omitempty"`
}

type paddleObservation struct {
	Box   [][]int `json:"box"`
	Score float32 `json:"score"`
	Text  string  `json:"text"`
}

func newPaddleEngine() (*paddleEngine, error) {
	exe, err := locatePaddleExe()
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(filepath.Dir(exe), "models", "config_chinese.txt")
	if _, err := os.Stat(configPath); err != nil {
		return nil, fmt.Errorf("paddle config_chinese.txt missing at %s: %w", configPath, err)
	}

	if n := sweepStaleTempDirs(os.TempDir(), paddleTempDirPrefix, time.Now().Add(-paddleStaleTempDirAge)); n > 0 {
		log.Printf("[ocr] removed %d stale paddle temp folder(s)", n)
	}
	tmpDir, err := os.MkdirTemp("", ownedTempDirPattern(paddleTempDirPrefix))
	if err != nil {
		return nil, fmt.Errorf("paddle temp dir: %w", err)
	}

	e := &paddleEngine{exe: exe, configPath: configPath, tmpDir: tmpDir}
	if err := e.startProcess(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, err
	}
	log.Printf("[ocr] paddle engine initialized (exe=%s)", exe)
	return e, nil
}

func (e *paddleEngine) IsSupported() bool { return true }

// Close terminates the PaddleOCR subprocess and removes the temp dir. Calling
// this on a singleton obtained via New() will break every other caller, so
// only the test code that constructs engines directly via newPaddleEngine
// should invoke it. The interface contract (ocr.Engine) intentionally omits
// Close — at runtime the subprocess is bound to a Job Object (see
// bindPaddleToParentLifecycle) so it dies with Yansu, which is more robust
// than wiring this into OnShutdown (handles os.Exit and crashes too).
func (e *paddleEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.killProcess()
	return os.RemoveAll(e.tmpDir)
}

func (e *paddleEngine) startProcess() error {
	// -ensure_ascii=0 makes PaddleOCR emit UTF-8 directly instead of \uXXXX
	// escapes, which keeps the stdout line ~2× smaller for CJK-heavy pages.
	cmd := exec.Command(e.exe,
		"-config_path="+e.configPath,
		"-ensure_ascii=0",
	)
	// PaddleOCR-json resolves dependent DLLs and model dirs relative to its
	// own location, so cd into that dir.
	cmd.Dir = filepath.Dir(e.exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW — keeps the console window hidden when launched from a GUI app
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("paddle stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("paddle stdout pipe: %w", err)
	}
	// Capture stderr to a bounded ring buffer for the lifetime of the
	// subprocess. Both init failures (missing model / DLL / GPU driver
	// crash) and runtime crashes (process exited mid-call) include the
	// recent stderr tail in their error messages — without this, the
	// caller only sees "EOF" / "process exited unexpectedly" with no
	// context. The bound keeps memory usage flat under chatty subprocess
	// logging.
	stderrBuf := &boundedBuffer{cap: 16 * 1024}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("paddle start: %w", err)
	}
	// Bind to a Job Object so the subprocess is killed when Yansu exits.
	// Windows does NOT auto-reap child processes on parent exit, and the
	// shutdown watchdog's os.Exit(0) bypasses any deferred cleanup, so
	// without this PaddleOCR-json.exe is left orphaned in Task Manager.
	bindPaddleToParentLifecycle(cmd)

	// The init phase emits dozens of kernel-fusion lines before "OCR init
	// completed." A line-oriented Scanner would buffer too aggressively
	// here, so raw-read until the marker appears.
	var initBuf bytes.Buffer
	chunk := make([]byte, 4096)
	deadline := time.Now().Add(paddleInitTimeout)
	for !bytes.Contains(initBuf.Bytes(), []byte(paddleInitMarker)) {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("paddle init timed out after %v; stdout tail: %q; stderr tail: %q",
				paddleInitTimeout, tailBytes(initBuf.Bytes(), 200), stderrBuf.tail(400))
		}
		n, err := stdout.Read(chunk)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("paddle init read failed: %w; stderr tail: %q", err, stderrBuf.tail(400))
		}
		initBuf.Write(chunk[:n])
		if initBuf.Len() > 1024*1024 {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("paddle init output exceeded 1 MiB without ready marker")
		}
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), paddleStdoutMaxLine)

	e.cmd = cmd
	e.stdin = stdin
	e.stdoutPipe = stdout
	e.scanner = sc
	e.stderrBuf = stderrBuf
	e.nextRestart = time.Now().Add(paddleRestartInterval)
	log.Printf("[ocr] paddle subprocess started (pid %d)", cmd.Process.Pid)
	return nil
}

func (e *paddleEngine) killProcess() {
	if reap := e.detachProcess(); reap != nil {
		reap()
	}
}

// killProcessAsync kills the subprocess now but waits for it in the
// background. Used when a call times out: Wait can block on a grandchild
// that still holds stderr, and the caller holds e.mu.
func (e *paddleEngine) killProcessAsync() {
	if reap := e.detachProcess(); reap != nil {
		go reap()
	}
}

// detachProcess closes the pipes and kills the subprocess, clears it from
// the engine (caller holds e.mu) and returns the Wait that reaps it, or nil
// when there is no subprocess.
func (e *paddleEngine) detachProcess() func() {
	if e.cmd == nil {
		return nil
	}
	cmd := e.cmd
	if e.stdin != nil {
		_ = e.stdin.Close()
	}
	if e.stdoutPipe != nil {
		_ = e.stdoutPipe.Close()
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	e.cmd = nil
	e.stdin = nil
	e.stdoutPipe = nil
	e.scanner = nil
	e.stderrBuf = nil
	return func() { _ = cmd.Wait() }
}

// ensureProcess starts the subprocess on first use and also handles the
// scheduled restart that mitigates PaddleOCR's known memory leak. Caller
// must hold e.mu.
func (e *paddleEngine) ensureProcess() error {
	if e.cmd == nil {
		return e.startProcess()
	}
	if time.Now().After(e.nextRestart) {
		log.Printf("[ocr] paddle restarting subprocess after %v (memory-leak workaround)", paddleRestartInterval)
		e.killProcess()
		return e.startProcess()
	}
	return nil
}

func (e *paddleEngine) callOCR(jpegData []byte) (paddleResponse, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ensureProcess(); err != nil {
		return paddleResponse{}, err
	}

	// Write the JPEG to a stable temp path. Sending bytes over stdin via
	// base64 would inflate the per-call transfer by ~33% with no quality
	// gain. The path is rewritten on every call (single file, locked above).
	// The frame may not be PII-masked yet (the PII mask OCRs it first), so
	// remove it once the response is read instead of leaving it on disk.
	tmp := filepath.Join(e.tmpDir, "ocr-input.jpg")
	if err := os.MkdirAll(e.tmpDir, 0o700); err != nil {
		return paddleResponse{}, fmt.Errorf("paddle temp dir: %w", err)
	}
	if err := os.WriteFile(tmp, jpegData, 0o600); err != nil {
		removeOCRTempFile(tmp)
		return paddleResponse{}, fmt.Errorf("paddle write tmp jpeg: %w", err)
	}
	defer removeOCRTempFile(tmp)

	reqBytes, err := json.Marshal(struct {
		ImagePath string `json:"image_path"`
	}{ImagePath: tmp})
	if err != nil {
		return paddleResponse{}, fmt.Errorf("paddle marshal request: %w", err)
	}

	if _, err := e.writeRequest(reqBytes); err != nil {
		log.Printf("[ocr] paddle write failed, restarting subprocess: %v", err)
		e.killProcess()
		if rerr := e.startProcess(); rerr != nil {
			return paddleResponse{}, rerr
		}
		if _, err := e.writeRequest(reqBytes); err != nil {
			e.killProcess()
			return paddleResponse{}, fmt.Errorf("paddle write after restart: %w", err)
		}
	}

	var timeoutStderr string
	ok, timedOut := scanLineWithin(e.scanner, paddleCallTimeout, func() {
		timeoutStderr = e.stderrBuf.tail(400)
		e.killProcessAsync()
	})
	if timedOut {
		return paddleResponse{}, fmt.Errorf("paddle OCR call timed out after %v; subprocess restarted on the next call; stderr tail: %q",
			paddleCallTimeout, timeoutStderr)
	}
	if !ok {
		scanErr := e.scanner.Err()
		stderrTail := e.stderrBuf.tail(400)
		e.killProcess()
		if scanErr != nil {
			return paddleResponse{}, fmt.Errorf("paddle read response: %w; stderr tail: %q", scanErr, stderrTail)
		}
		return paddleResponse{}, fmt.Errorf("paddle subprocess exited unexpectedly; stderr tail: %q", stderrTail)
	}

	line := e.scanner.Bytes()
	var resp paddleResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return paddleResponse{}, fmt.Errorf("paddle parse response: %w (raw: %s)", err, tailBytes(line, 200))
	}
	return resp, nil
}

func (e *paddleEngine) writeRequest(reqBytes []byte) (int, error) {
	if _, err := e.stdin.Write(reqBytes); err != nil {
		return 0, err
	}
	return e.stdin.Write([]byte{'\n'})
}

func (e *paddleEngine) decodeObservations(resp paddleResponse) ([]paddleObservation, error) {
	switch resp.Code {
	case CodePaddleSuccess:
		if len(resp.Data) == 0 {
			return nil, nil
		}
		var obs []paddleObservation
		if err := json.Unmarshal(resp.Data, &obs); err != nil {
			return nil, fmt.Errorf("paddle parse data array: %w", err)
		}
		return obs, nil
	case CodePaddleNoText:
		return nil, nil
	default:
		// Non-100/101 codes carry a string message in `data`.
		var msg string
		_ = json.Unmarshal(resp.Data, &msg)
		return nil, fmt.Errorf("paddle code=%d msg=%s", resp.Code, msg)
	}
}

// PaddleOCR-json status codes — only the two we actually act on.
const (
	CodePaddleSuccess = 100
	CodePaddleNoText  = 101
)

// Priority is ignored: the per-engine mutex serialises calls FIFO and there's
// only one subprocess. Plumbing the global priority gate here is a separate
// cleanup (the winRT backend doesn't use it either).
func (e *paddleEngine) RecognizeJPEG(jpegData []byte, _ []string, _ Priority) (*Result, error) {
	if len(jpegData) == 0 {
		return nil, fmt.Errorf("empty JPEG data")
	}
	resp, err := e.callOCR(jpegData)
	if err != nil {
		return nil, err
	}
	obs, err := e.decodeObservations(resp)
	if err != nil {
		return nil, err
	}
	if len(obs) == 0 {
		return &Result{}, nil
	}

	var sb strings.Builder
	var sumScore float32
	n := 0
	for _, d := range obs {
		if d.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(d.Text)
		sumScore += d.Score
		n++
	}
	conf := 0.0
	if n > 0 {
		conf = float64(sumScore) / float64(n)
	}
	return &Result{Text: sb.String(), Confidence: conf}, nil
}

func (e *paddleEngine) RecognizeJPEGWithBoxes(jpegData []byte, _ []string, _ Priority) ([]TextObservation, error) {
	if len(jpegData) == 0 {
		return nil, fmt.Errorf("empty JPEG data")
	}

	// PaddleOCR returns box coords in pixel space; we need image dims to
	// normalise into [0,1] top-left-origin layout the Engine interface
	// promises. DecodeConfig reads just the JPEG header, no pixel decode.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(jpegData))
	if err != nil {
		return nil, fmt.Errorf("paddle: decode jpeg dims: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("paddle: invalid image dims %dx%d", cfg.Width, cfg.Height)
	}

	resp, err := e.callOCR(jpegData)
	if err != nil {
		return nil, err
	}
	pobs, err := e.decodeObservations(resp)
	if err != nil {
		return nil, err
	}
	if len(pobs) == 0 {
		return nil, nil
	}

	fW, fH := float64(cfg.Width), float64(cfg.Height)
	out := make([]TextObservation, 0, len(pobs))
	for _, d := range pobs {
		if d.Text == "" || len(d.Box) == 0 {
			continue
		}
		b, ok := quadToAABB(d.Box)
		if !ok {
			continue
		}
		out = append(out, TextObservation{
			Text:       d.Text,
			Confidence: float64(d.Score),
			X:          clamp01(b.minX / fW),
			Y:          clamp01(b.minY / fH),
			Width:      clamp01((b.maxX - b.minX) / fW),
			Height:     clamp01((b.maxY - b.minY) / fH),
		})
	}
	return out, nil
}

type aabb struct {
	minX, minY, maxX, maxY float64
}

// quadToAABB collapses PaddleOCR's 4-point polygon (potentially rotated) into
// an axis-aligned bounding rectangle. Callers that need rotation should switch
// off this AABB approximation, but the Engine interface's TextObservation is
// AABB-only so we lose tilt information here by design.
func quadToAABB(quad [][]int) (aabb, bool) {
	if len(quad) == 0 {
		return aabb{}, false
	}
	minX, minY := math.MaxFloat64, math.MaxFloat64
	maxX, maxY := -math.MaxFloat64, -math.MaxFloat64
	for _, pt := range quad {
		if len(pt) < 2 {
			continue
		}
		x, y := float64(pt[0]), float64(pt[1])
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
	}
	if minX > maxX || minY > maxY {
		return aabb{}, false
	}
	return aabb{minX: minX, minY: minY, maxX: maxX, maxY: maxY}, true
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func tailBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}

// boundedBuffer is an io.Writer that retains only the last cap bytes written
// to it. Used for stderr capture so we can surface PaddleOCR's upstream
// diagnostic output in error messages without letting a chatty subprocess
// (MKLDNN warnings, periodic logs) bloat memory unboundedly.
type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.cap {
		b.buf = b.buf[len(b.buf)-b.cap:]
	}
	return len(p), nil
}

func (b *boundedBuffer) tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.buf) <= n {
		return string(b.buf)
	}
	return string(b.buf[len(b.buf)-n:])
}

// locatePaddleExe resolves the path to PaddleOCR-json.exe. Search order:
//
//  1. YANSU_PADDLE_OCR_EXE env var — explicit override, used by CI and tests.
//  2. <executable-dir>/paddleocr/PaddleOCR-json.exe — production bundle layout
//     populated by scripts/build-wails.sh.
//  3. ~/.yansu-agent/paddleocr/PaddleOCR-json.exe — dev convenience: matches
//     the path scripts/download-paddleocr.sh extracts into when run locally.
//
// Returns an error if no candidate exists. The dispatch logic in
// initSharedEngine then falls back to WinRT, so a missing binary degrades
// gracefully instead of disabling OCR.
const paddleOCRExeEnvVar = "YANSU_PADDLE_OCR_EXE"

func locatePaddleExe() (string, error) {
	if v := strings.TrimSpace(os.Getenv(paddleOCRExeEnvVar)); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, nil
		}
		return "", fmt.Errorf("%s=%q does not exist", paddleOCRExeEnvVar, v)
	}

	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "paddleocr", "PaddleOCR-json.exe")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	if dataDir, err := apphome.Dir(""); err == nil {
		candidate := filepath.Join(dataDir, "paddleocr", "PaddleOCR-json.exe")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("PaddleOCR-json.exe not found (set %s or bundle paddleocr/ next to the executable)", paddleOCRExeEnvVar)
}

func (e *paddleEngine) SupportedLanguages() []string {
	return nil
}
