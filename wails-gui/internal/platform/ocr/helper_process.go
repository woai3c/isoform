package ocr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

const helperMaxMessageBytes = 64 * 1024 * 1024

// The helper serves newline-delimited requests in one process. IDs ensure a
// delayed result can never be mistaken for a later screenshot's OCR result.
type helperRequest struct {
	ID       uint64   `json:"id"`
	Mode     string   `json:"mode"`
	JPEGData []byte   `json:"jpegData"`
	Langs    []string `json:"langs,omitempty"`
	Priority int      `json:"priority"`
}

type helperResponse struct {
	ID           uint64            `json:"id"`
	Text         string            `json:"text,omitempty"`
	Confidence   float64           `json:"confidence,omitempty"`
	Observations []TextObservation `json:"observations,omitempty"`
	Error        string            `json:"error,omitempty"`
}

type helperReply struct {
	response *helperResponse
	err      error
}

type helperProcess struct {
	mu            sync.Mutex
	command       func() *exec.Cmd
	warmTimeout   time.Duration
	coldTimeout   time.Duration
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	scanner       *bufio.Scanner
	pending       <-chan helperReply
	nextID        uint64
	warm          bool
	timeoutStreak int
	closed        chan struct{}
	closeOnce     sync.Once

	requests      uint64
	completed     uint64
	droppedFrames uint64
	timeouts      uint64
	restarts      uint64
	lastSummary   time.Time
}

func newHelperProcess(command func() *exec.Cmd, warmTimeout, coldTimeout time.Duration) *helperProcess {
	return &helperProcess{
		command: command, warmTimeout: warmTimeout, coldTimeout: coldTimeout,
		closed: make(chan struct{}),
	}
}

func (h *helperProcess) start() error {
	cmd := h.command()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("ocr helper stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("ocr helper stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("ocr helper start: %w", err)
	}
	h.cmd, h.stdin, h.stdout = cmd, stdin, stdout
	h.scanner = bufio.NewScanner(stdout)
	h.scanner.Buffer(make([]byte, 4096), helperMaxMessageBytes)
	return nil
}

// stop is called with mu held. Closing both pipes unblocks an outstanding
// request before Wait reaps the child; no detached request goroutine remains.
func (h *helperProcess) stop() {
	if h.cmd == nil {
		return
	}
	_ = h.cmd.Process.Kill()
	_ = h.stdin.Close()
	_ = h.stdout.Close()
	if h.pending != nil {
		<-h.pending
		h.pending = nil
	}
	_ = h.cmd.Wait()
	h.cmd, h.stdin, h.stdout, h.scanner = nil, nil, nil, nil
	h.warm = false
	h.timeoutStreak = 0
}

func (h *helperProcess) close() {
	h.closeOnce.Do(func() { close(h.closed) })
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stop()
	h.logSummary(true)
}

func (h *helperProcess) logSummary(force bool) {
	if !force && time.Since(h.lastSummary) < time.Minute {
		return
	}
	log.Printf("[ocr] helper summary: requests=%d completed=%d dropped_frames=%d timeouts=%d restarts=%d",
		h.requests, h.completed, h.droppedFrames, h.timeouts, h.restarts)
	h.lastSummary = time.Now()
}

func (h *helperProcess) run(req helperRequest, prewarm bool) (resp *helperResponse, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.closed:
		return nil, errors.New("ocr helper is closed")
	default:
	}
	if !prewarm {
		h.requests++
		defer func() {
			if err != nil {
				h.droppedFrames++
			} else {
				h.completed++
			}
			h.logSummary(false)
		}()
	}
	if h.cmd == nil {
		if err := h.start(); err != nil {
			return nil, err
		}
	}

	cold := !h.warm
	timeout := h.warmTimeout
	if cold {
		timeout = h.coldTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	started := time.Now()

	// After one timeout, let Vision finish the previous request. Do not enqueue
	// another full screenshot behind it; only one request is ever in flight.
	if h.pending != nil {
		if _, err := h.await(timer.C, timeout, cold); err != nil {
			return nil, err
		}
	}

	h.nextID++
	req.ID = h.nextID
	reply := make(chan helperReply, 1)
	h.pending = reply
	stdin, scanner := h.stdin, h.scanner
	go func() {
		if err := json.NewEncoder(stdin).Encode(req); err != nil {
			reply <- helperReply{err: fmt.Errorf("ocr helper request: %w", err)}
			return
		}
		if !scanner.Scan() {
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			reply <- helperReply{err: fmt.Errorf("ocr helper response: %w", err)}
			return
		}
		var resp helperResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			reply <- helperReply{err: fmt.Errorf("ocr helper response: %w", err)}
			return
		}
		if resp.ID != req.ID {
			reply <- helperReply{err: fmt.Errorf("ocr helper response ID %d does not match request %d", resp.ID, req.ID)}
			return
		}
		reply <- helperReply{response: &resp}
	}()

	resp, err = h.await(timer.C, timeout, cold)
	if err != nil {
		return nil, err
	}
	h.timeoutStreak = 0
	if resp.Error != "" {
		return nil, fmt.Errorf("ocr: %s", resp.Error)
	}
	if !h.warm {
		h.warm = true
		log.Printf("[ocr] helper warm; recognition took %s", time.Since(started).Round(time.Millisecond))
	}
	return resp, nil
}

func (h *helperProcess) await(deadline <-chan time.Time, timeout time.Duration, cold bool) (*helperResponse, error) {
	select {
	case reply := <-h.pending:
		h.pending = nil
		if reply.err != nil {
			h.restarts++
			h.stop()
			return nil, reply.err
		}
		return reply.response, nil
	case <-deadline:
		h.timeouts++
		h.timeoutStreak++
		if h.timeoutStreak >= 2 {
			h.restarts++
			h.stop()
		}
		return nil, &HelperTimeoutError{Timeout: timeout, Cold: cold}
	case <-h.closed:
		h.stop()
		return nil, errors.New("ocr helper is closed")
	}
}
