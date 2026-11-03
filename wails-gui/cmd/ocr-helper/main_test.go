//go:build darwin

package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"wails-gui/internal/platform/ocr"
)

// Protocol tests use an engine fixture so they do not compile Vision's model
// or depend on the host's OCR languages and permissions.
type protocolEngine struct {
	textCalls  int
	boxesCalls int
}

func (e *protocolEngine) RecognizeJPEG(_ []byte, _ []string, _ ocr.Priority) (*ocr.Result, error) {
	e.textCalls++
	return &ocr.Result{Text: "recognized"}, nil
}

func (e *protocolEngine) RecognizeJPEGWithBoxes(_ []byte, _ []string, _ ocr.Priority) ([]ocr.TextObservation, error) {
	e.boxesCalls++
	return []ocr.TextObservation{{Text: "warmup"}}, nil
}

func (e *protocolEngine) SupportedLanguages() []string { return nil }
func (e *protocolEngine) IsSupported() bool            { return true }

func TestServeRetainsEngineAndEchoesRequestIDs(t *testing.T) {
	var input, output bytes.Buffer
	enc := json.NewEncoder(&input)
	for _, req := range []request{{ID: 7, Mode: "boxes"}, {ID: 8, Mode: "unknown"}, {ID: 9, Mode: "text"}} {
		if err := enc.Encode(req); err != nil {
			t.Fatal(err)
		}
	}
	engine := &protocolEngine{}
	if err := serve(&input, &output, engine); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for _, id := range []uint64{7, 8, 9} {
		var resp response
		if err := decoder.Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.ID != id {
			t.Fatalf("response ID = %d, want %d", resp.ID, id)
		}
		if id == 8 && resp.Error == "" {
			t.Fatal("unknown mode must report an error without ending the stream")
		}
	}
	if engine.boxesCalls != 1 || engine.textCalls != 1 {
		t.Fatalf("wrong engine calls: boxes=%d text=%d", engine.boxesCalls, engine.textCalls)
	}
}

func TestServeRejectsMalformedRequest(t *testing.T) {
	var output bytes.Buffer
	if err := serve(bytes.NewBufferString("invalid\n"), &output, &protocolEngine{}); err == nil || output.Len() != 0 {
		t.Fatalf("malformed request must terminate without an unmatched response: err=%v output=%q", err, output.String())
	}
}
