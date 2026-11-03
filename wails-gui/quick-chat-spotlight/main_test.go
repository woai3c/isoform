package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSpotlightConfiguresAccessoryBeforeRun(t *testing.T) {
	contentBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	content := string(contentBytes)
	mainFunc := strings.Index(content, "func main() {")
	if mainFunc < 0 {
		t.Fatalf("main.go does not define main")
	}
	mainContent := content[mainFunc:]

	configureCall := strings.Index(mainContent, "configureSpotlightAppPresentation()")
	if configureCall < 0 {
		t.Fatalf("main.go does not call configureSpotlightAppPresentation")
	}

	// The platform shell now lives behind run(a) — Wails on Windows/Linux
	// (run_other.go), a native NSPanel run loop on macOS (run_darwin.go).
	// Dock suppression must still happen before the GUI shell starts, so
	// assert configureSpotlightAppPresentation precedes the run(a) call.
	runCall := strings.Index(mainContent, "run(a)")
	if runCall < 0 {
		t.Fatalf("main.go does not call run(a)")
	}

	if configureCall > runCall {
		t.Fatalf("configureSpotlightAppPresentation runs after run(a); macOS Dock suppression must happen before the GUI shell starts")
	}
}

func TestDarwinSpotlightPresentationCreatesSharedApplication(t *testing.T) {
	contentBytes, err := os.ReadFile("clear_window_bg_darwin.go")
	if err != nil {
		t.Fatalf("read clear_window_bg_darwin.go: %v", err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "[NSApplication sharedApplication]") {
		t.Fatalf("darwin spotlight presentation does not initialise NSApplication before setting accessory policy")
	}
}

func TestYansuHostHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, portString, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: time.Second}
	if !yansuHostHealthy(client, port) {
		t.Fatal("expected healthy host bridge")
	}
}

func TestYansuHostHealthyRejectsNonOKResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, portString, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: time.Second}
	if yansuHostHealthy(client, port) {
		t.Fatal("expected unhealthy host bridge for non-200 response")
	}
}

// Isoform's host bridge refuses a POST without the helper's token or a JSON
// Content-Type, so the disarm call must carry both.
func TestDisarmDismissRequestSendsTokenAndJSON(t *testing.T) {
	var gotToken, gotType, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get(tokenHeader)
		gotType = r.Header.Get("Content-Type")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	req, err := newDisarmDismissRequest(port, "helper-token")
	if err != nil {
		t.Fatalf("newDisarmDismissRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if req.Method != http.MethodPost || gotPath != "/api/spotlight/disarm-dismiss-on-key" {
		t.Fatalf("request = %s %s", req.Method, gotPath)
	}
	if gotToken != "helper-token" {
		t.Fatalf("token header = %q", gotToken)
	}
	if gotType != "application/json" {
		t.Fatalf("Content-Type = %q", gotType)
	}
}
