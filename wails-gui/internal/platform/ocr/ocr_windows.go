//go:build windows

package ocr

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
)

//go:embed ocr_windows.ps1
var psScript string

type ocrRequest struct {
	Mode  string   `json:"mode"`  // "text" or "boxes"
	Path  string   `json:"path"`  // path to JPEG file
	Langs []string `json:"langs"` // recognition languages (e.g. ["en-US", "zh-Hans"])
}

type ocrTextResponse struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Error      string  `json:"error,omitempty"`
}

type ocrBoxObservation struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
}

type ocrBoxResponse struct {
	Observations []ocrBoxObservation `json:"observations"`
	Error        string              `json:"error,omitempty"`
}

// winRTEngine implements Engine using a persistent PowerShell subprocess
// that calls the Windows.Media.Ocr WinRT API.
//
// Constructed via newWinRTEngine. Callers should obtain an Engine via New()
// in ocr_windows_dispatch.go, which selects between this and the PaddleOCR
// backend at runtime.
type winRTEngine struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	scanner    *bufio.Scanner
	scriptPath string
	tmpDir     string // directory for temporary JPEG files
}

func newWinRTEngine() Engine {
	return &winRTEngine{}
}

func (e *winRTEngine) IsSupported() bool {
	// Windows.Media.Ocr is available on Windows 10 and later.
	return true
}

func (e *winRTEngine) startProcess() error {
	if e.scriptPath == "" {
		f, err := os.CreateTemp("", "yansu-ocr-*.ps1")
		if err != nil {
			return fmt.Errorf("ocr: create script file: %w", err)
		}
		if _, err := f.WriteString(psScript); err != nil {
			f.Close()
			os.Remove(f.Name())
			return fmt.Errorf("ocr: write script: %w", err)
		}
		f.Close()
		e.scriptPath = f.Name()
	}

	if e.tmpDir == "" {
		dir, err := os.MkdirTemp("", "yansu-ocr-img-*")
		if err != nil {
			return fmt.Errorf("ocr: create temp dir: %w", err)
		}
		e.tmpDir = dir
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
		return fmt.Errorf("ocr: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("ocr: stdout pipe: %w", err)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("ocr: start powershell: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1*1024*1024), 1*1024*1024) // 1 MB buffer for responses

	if !scanner.Scan() {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("ocr: powershell init failed (no output)")
	}

	readyLine := scanner.Text()
	if readyLine != "READY" {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("ocr: init failed: %s", readyLine)
	}

	e.cmd = cmd
	e.stdin = stdin
	e.scanner = scanner
	log.Printf("[ocr] Windows OCR process started (pid %d)", cmd.Process.Pid)
	return nil
}

func (e *winRTEngine) killProcess() {
	if e.cmd != nil {
		e.stdin.Close()
		e.cmd.Process.Kill()
		e.cmd.Wait()
		e.cmd = nil
		e.stdin = nil
		e.scanner = nil
	}
}

func (e *winRTEngine) ensureProcess() error {
	if e.cmd != nil {
		return nil
	}
	return e.startProcess()
}

func (e *winRTEngine) callOCR(jpegData []byte, mode string, langs []string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ensureProcess(); err != nil {
		return nil, err
	}

	// Write JPEG to a temp file to avoid base64/JSON size limits.
	tmpFile := filepath.Join(e.tmpDir, "ocr-input.jpg")
	if err := os.WriteFile(tmpFile, jpegData, 0600); err != nil {
		return nil, fmt.Errorf("ocr: write temp file: %w", err)
	}
	defer removeOCRTempFile(tmpFile)

	req := ocrRequest{
		Mode:  mode,
		Path:  tmpFile,
		Langs: langs,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("ocr: marshal request: %w", err)
	}

	if _, err := fmt.Fprintf(e.stdin, "%s\n", reqJSON); err != nil {
		log.Printf("[ocr] write failed, restarting process: %v", err)
		e.killProcess()
		if err := e.startProcess(); err != nil {
			return nil, err
		}
		if _, err := fmt.Fprintf(e.stdin, "%s\n", reqJSON); err != nil {
			e.killProcess()
			return nil, fmt.Errorf("ocr: write after restart: %w", err)
		}
	}

	if !e.scanner.Scan() {
		scanErr := e.scanner.Err()
		e.killProcess()
		if scanErr != nil {
			return nil, fmt.Errorf("ocr: read response: %w", scanErr)
		}
		return nil, fmt.Errorf("ocr: process exited unexpectedly")
	}

	return []byte(e.scanner.Text()), nil
}

func (e *winRTEngine) RecognizeJPEG(jpegData []byte, langs []string, _ Priority) (*Result, error) {
	if len(jpegData) == 0 {
		return nil, fmt.Errorf("empty JPEG data")
	}

	respBytes, err := e.callOCR(jpegData, "text", langs)
	if err != nil {
		return nil, err
	}

	var resp ocrTextResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("ocr: parse response: %w (raw: %s)", err, string(respBytes))
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("ocr: %s", resp.Error)
	}

	return &Result{
		Text:       resp.Text,
		Confidence: resp.Confidence,
	}, nil
}

func (e *winRTEngine) RecognizeJPEGWithBoxes(jpegData []byte, langs []string, _ Priority) ([]TextObservation, error) {
	if len(jpegData) == 0 {
		return nil, fmt.Errorf("empty JPEG data")
	}

	respBytes, err := e.callOCR(jpegData, "boxes", langs)
	if err != nil {
		return nil, err
	}

	var resp ocrBoxResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("ocr: parse response: %w (raw: %s)", err, string(respBytes))
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("ocr: %s", resp.Error)
	}

	if len(resp.Observations) == 0 {
		return nil, nil
	}

	observations := make([]TextObservation, len(resp.Observations))
	for i, obs := range resp.Observations {
		observations[i] = TextObservation{
			Text:       obs.Text,
			Confidence: obs.Confidence,
			X:          obs.X,
			Y:          obs.Y,
			Width:      obs.Width,
			Height:     obs.Height,
		}
	}

	return observations, nil
}

func (e *winRTEngine) SupportedLanguages() []string {
	return nil
}
