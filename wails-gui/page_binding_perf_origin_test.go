package main

import (
	"log"
	"strings"
	"testing"
	"time"
)

// Direct Go calls are tagged internal; the bridge frame name is what marks a window call.
func TestBindingOriginSeparatesWindowFromInternalCalls(t *testing.T) {
	if got := bindingOrigin(); got != "internal" {
		t.Fatalf("a direct Go call origin = %q, want internal", got)
	}
	if !isBridgeFrame("github.com/wailsapp/wails/v2/internal/binding.(*BoundMethod).Call") {
		t.Fatal("the Wails bound-method frame is not recognised as the window bridge")
	}
	if isBridgeFrame("main.(*App).GetDayNoticed") {
		t.Fatal("an application frame was taken for the window bridge")
	}
}

// The perf line keeps its existing prefix, so parsers of binding/ms/rows still match,
// and gains an origin at the end.
func TestPageBindingLogKeepsPrefixAndAddsOrigin(t *testing.T) {
	var buf strings.Builder
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	logPageBinding("GetExample", time.Now(), []int{1, 2})
	line := buf.String()
	if !strings.Contains(line, "[perf] binding=GetExample ms=") || !strings.Contains(line, " rows=2 origin=internal") {
		t.Fatalf("perf line = %q", line)
	}
}
