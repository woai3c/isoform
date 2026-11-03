//go:build darwin

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"wails-gui/internal/platform/ocr"
)

const maxRequestBytes = 64 * 1024 * 1024

type request struct {
	ID       uint64   `json:"id"`
	Mode     string   `json:"mode"`
	JPEGData []byte   `json:"jpegData"`
	Langs    []string `json:"langs,omitempty"`
	Priority int      `json:"priority"`
}

type response struct {
	ID           uint64                `json:"id"`
	Text         string                `json:"text,omitempty"`
	Confidence   float64               `json:"confidence,omitempty"`
	Observations []ocr.TextObservation `json:"observations,omitempty"`
	Error        string                `json:"error,omitempty"`
}

func main() {
	_ = os.Setenv("YANSU_OCR_HELPER_CHILD", "1")
	// A wedged native request must not orphan this persistent helper if the
	// desktop crashes before its normal shutdown closes the helper pipes.
	parentPID := os.Getppid()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if os.Getppid() != parentPID {
				os.Exit(0)
			}
		}
	}()
	if err := serve(os.Stdin, os.Stdout, ocr.New()); err != nil {
		fmt.Fprintf(os.Stderr, "ocr helper: %v\n", err)
	}
}

// serve retains the engine between requests. A malformed or oversized frame
// ends the stream, so the parent cannot consume an out-of-sync response.
func serve(input io.Reader, output io.Writer, engine ocr.Engine) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxRequestBytes)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			return fmt.Errorf("decode request: %w", err)
		}
		resp := recognize(engine, req)
		resp.ID = req.ID
		if err := encoder.Encode(resp); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
	}
	return scanner.Err()
}

func recognize(engine ocr.Engine, req request) response {
	if !engine.IsSupported() {
		return response{Error: "OCR not supported"}
	}
	prio := ocr.Priority(req.Priority)
	switch req.Mode {
	case "text":
		result, err := engine.RecognizeJPEG(req.JPEGData, req.Langs, prio)
		if err != nil {
			return response{Error: err.Error()}
		}
		return response{Text: result.Text, Confidence: result.Confidence}
	case "boxes":
		observations, err := engine.RecognizeJPEGWithBoxes(req.JPEGData, req.Langs, prio)
		if err != nil {
			return response{Error: err.Error()}
		}
		return response{Observations: observations}
	default:
		return response{Error: "unknown OCR mode"}
	}
}
