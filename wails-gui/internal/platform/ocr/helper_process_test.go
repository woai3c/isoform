package ocr

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This subprocess fixture exercises real pipes, process reuse, timeouts and
// cleanup without invoking Vision or reading any user data.
func TestOCRHelperProcess(t *testing.T) {
	if os.Getenv("YANSU_TEST_OCR_PROCESS") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req helperRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		switch req.Mode {
		case "slow":
			time.Sleep(300 * time.Millisecond)
		case "hang":
			time.Sleep(time.Minute)
		case "bad-id":
			req.ID++
		case "boxes":
			img, err := jpeg.Decode(bytes.NewReader(req.JPEGData))
			if err != nil || img.Bounds().Dx() < 100 || img.Bounds().Dy() < 40 {
				os.Exit(4)
			}
			// The warmup must carry visible marks through the real recognition
			// mode, rather than warming just the process or image decoder.
			foundInk := false
			for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
				for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
					r, g, b, _ := img.At(x, y).RGBA()
					if r+g+b < 3*32000 {
						foundInk = true
					}
				}
			}
			if !foundInk {
				os.Exit(5)
			}
		}
		resp := helperResponse{ID: req.ID, Text: fmt.Sprintf("%d:%s", os.Getpid(), string(req.JPEGData))}
		if req.Mode == "error" {
			resp.Error = "recognition unavailable"
		}
		if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
			os.Exit(3)
		}
	}
	os.Exit(0)
}

func testHelperProcess(t *testing.T) *helperProcess {
	t.Helper()
	h := newHelperProcess(func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOCRHelperProcess$")
		cmd.Env = append(os.Environ(), "YANSU_TEST_OCR_PROCESS=1")
		return cmd
	}, 100*time.Millisecond, 3*time.Second)
	t.Cleanup(h.close)
	return h
}

func runHelper(t *testing.T, h *helperProcess, mode, text string) *helperResponse {
	t.Helper()
	resp, err := h.run(helperRequest{Mode: mode, JPEGData: []byte(text)}, false)
	if err != nil {
		t.Fatalf("helper %s: %v", mode, err)
	}
	return resp
}

func expectHelperTimeout(t *testing.T, h *helperProcess, mode string) *HelperTimeoutError {
	t.Helper()
	_, err := h.run(helperRequest{Mode: mode}, false)
	var timeoutErr *HelperTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected timeout, got %v", err)
	}
	return timeoutErr
}

func TestHelperProcessColdStartAndReuse(t *testing.T) {
	h := testHelperProcess(t)
	first := runHelper(t, h, "slow", "first")
	if !h.warm {
		t.Fatal("successful cold request did not warm helper")
	}
	pid := h.cmd.Process.Pid
	second := runHelper(t, h, "text", "second")
	if h.cmd.Process.Pid != pid || !strings.HasPrefix(first.Text, strconv.Itoa(pid)+":") || second.Text != strconv.Itoa(pid)+":second" {
		t.Fatalf("helper not reused: first=%q second=%q pid=%d", first.Text, second.Text, pid)
	}
}

func TestHelperProcessFirstTimeoutPreservesProcessAndCountsDrop(t *testing.T) {
	h := testHelperProcess(t)
	runHelper(t, h, "text", "warm")
	cmd := h.cmd
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	h.lastSummary = time.Now().Add(-time.Minute)
	timeoutErr := expectHelperTimeout(t, h, "hang")
	if timeoutErr.Cold || h.cmd != cmd || cmd.ProcessState != nil || !processAlive(cmd.Process.Pid) {
		t.Fatal("first warm timeout must leave the same helper running")
	}
	if h.droppedFrames != 1 || h.timeouts != 1 || h.restarts != 0 || h.timeoutStreak != 1 {
		t.Fatalf("first timeout counters: dropped=%d timeouts=%d restarts=%d streak=%d", h.droppedFrames, h.timeouts, h.restarts, h.timeoutStreak)
	}
	if !strings.Contains(logs.String(), "dropped_frames=1") {
		t.Fatalf("missing dropped frame summary: %s", logs.String())
	}
}

func TestHelperProcessTwoTimeoutsRestartAndRecover(t *testing.T) {
	h := testHelperProcess(t)
	runHelper(t, h, "text", "warm")
	oldCmd := h.cmd
	expectHelperTimeout(t, h, "hang")
	expectHelperTimeout(t, h, "text")
	if h.cmd != nil || oldCmd.ProcessState == nil || processAlive(oldCmd.Process.Pid) {
		t.Fatal("second timeout did not stop and reap the hung helper")
	}
	if h.droppedFrames != 2 || h.restarts != 1 || h.pending != nil {
		t.Fatalf("second timeout counters: drops=%d restarts=%d pending=%v", h.droppedFrames, h.restarts, h.pending)
	}
	resp := runHelper(t, h, "text", "recovered")
	if h.cmd.Process.Pid == oldCmd.Process.Pid || !strings.HasSuffix(resp.Text, ":recovered") || !h.warm {
		t.Fatalf("did not recover in new helper: %q", resp.Text)
	}
}

func TestHelperProcessLateReplyIsDiscardedAndSuccessResetsStreak(t *testing.T) {
	h := testHelperProcess(t)
	runHelper(t, h, "text", "warm")
	pid := h.cmd.Process.Pid
	expectHelperTimeout(t, h, "slow")
	// Give the late request time to finish; its result remains on the pending
	// channel until the next call explicitly drains it.
	time.Sleep(300 * time.Millisecond)
	resp := runHelper(t, h, "text", "new-frame")
	if resp.Text != strconv.Itoa(pid)+":new-frame" || h.timeoutStreak != 0 || h.restarts != 0 {
		t.Fatalf("late response leaked or streak not reset: response=%q streak=%d restarts=%d", resp.Text, h.timeoutStreak, h.restarts)
	}
	expectHelperTimeout(t, h, "hang")
	if h.cmd == nil || h.restarts != 0 {
		t.Fatal("success did not reset the consecutive timeout count")
	}
}

func TestHelperProcessRejectsMismatchedResponseID(t *testing.T) {
	h := testHelperProcess(t)
	_, err := h.run(helperRequest{Mode: "bad-id"}, false)
	if err == nil || !strings.Contains(err.Error(), "does not match") || h.cmd != nil || h.droppedFrames != 1 {
		t.Fatalf("mismatched result accepted: err=%v drops=%d", err, h.droppedFrames)
	}
}

func TestHelperProcessColdTimeoutAndPrewarmAreNotFrameDrops(t *testing.T) {
	h := testHelperProcess(t)
	h.coldTimeout = 100 * time.Millisecond
	_, err := h.run(helperRequest{Mode: "hang"}, true)
	var timeoutErr *HelperTimeoutError
	if !errors.As(err, &timeoutErr) || !timeoutErr.Cold || h.droppedFrames != 0 || h.requests != 0 || h.restarts != 0 {
		t.Fatalf("prewarm timeout: err=%v drops=%d requests=%d restarts=%d", err, h.droppedFrames, h.requests, h.restarts)
	}
}

func TestHelperProcessCloseInterruptsPendingRequest(t *testing.T) {
	h := testHelperProcess(t)
	runHelper(t, h, "text", "warm")
	h.warmTimeout = time.Minute
	result := make(chan error, 1)
	go func() {
		_, err := h.run(helperRequest{Mode: "hang"}, false)
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	h.close()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed request unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not interrupt pending request")
	}
	if h.cmd != nil {
		t.Fatal("shutdown left helper alive")
	}
}

func TestHelperProcessFailureSummariesAreThrottledAndFlushOnClose(t *testing.T) {
	h := testHelperProcess(t)
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	failRequest := func() {
		t.Helper()
		if _, err := h.run(helperRequest{Mode: "error"}, false); err == nil {
			t.Fatal("expected a failed recognition request")
		}
	}
	const burstSize = 40
	for range burstSize {
		failRequest()
	}
	if h.requests != burstSize || h.droppedFrames != burstSize || h.completed != 0 {
		t.Fatalf("failure burst lost counters: requests=%d dropped=%d completed=%d", h.requests, h.droppedFrames, h.completed)
	}
	if got := strings.Count(logs.String(), "[ocr] helper summary:"); got != 1 {
		t.Fatalf("failure burst emitted %d summaries, want one within a minute", got)
	}

	// Advance the summary window without sleeping or changing request timing.
	h.lastSummary = time.Now().Add(-time.Minute)
	failRequest()
	if got := strings.Count(logs.String(), "[ocr] helper summary:"); got != 2 {
		t.Fatalf("expired minute emitted %d total summaries, want two", got)
	}
	if !strings.Contains(logs.String(), "requests=41 completed=0 dropped_frames=41") {
		t.Fatalf("minute summary omitted accumulated failures: %s", logs.String())
	}
	failRequest()
	if got := strings.Count(logs.String(), "[ocr] helper summary:"); got != 2 {
		t.Fatalf("failure after summary bypassed the new window: summaries=%d", got)
	}

	h.close()
	if got := strings.Count(logs.String(), "[ocr] helper summary:"); got != 3 {
		t.Fatalf("shutdown did not flush a final summary: summaries=%d", got)
	}
	if !strings.Contains(logs.String(), "requests=42 completed=0 dropped_frames=42") {
		t.Fatalf("shutdown summary omitted final failures: %s", logs.String())
	}
}
