package native

import (
	"errors"
	"fmt"
	"testing"
)

func TestEmitErrorFromCarriesHTTPStatus(t *testing.T) {
	var got []LoopEvent
	cfg := &LoopConfig{Emit: func(ev LoopEvent) { got = append(got, ev) }}
	emitErrorFrom(cfg, "boom", fmt.Errorf("stream: %w", &HTTPError{Status: 401, Body: "x"}))
	emitErrorFrom(cfg, "boom", errors.New("anthropic: HTTP 401: unauthorized"))
	if len(got) != 2 || got[0].Type != "error" || got[0].Status != 401 || got[1].Status != 0 {
		t.Fatalf("events = %+v", got)
	}
}
