package main

import (
	"net/http/httptest"
	"testing"
)

func TestIsLoopbackRemoteAddr(t *testing.T) {
	tests := []struct {
		name   string
		addr   string
		expect bool
	}{
		{name: "localhost hostname", addr: "localhost:8080", expect: true},
		{name: "ipv4 loopback", addr: "127.0.0.1:8080", expect: true},
		{name: "ipv6 loopback", addr: "[::1]:8080", expect: true},
		{name: "ipv6 loopback with zone", addr: "[::1%lo0]:8080", expect: true},
		{name: "remote", addr: "10.0.0.1:8080", expect: false},
		{name: "empty", addr: "", expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLoopbackRemoteAddr(tt.addr)
			if got != tt.expect {
				t.Fatalf("isLoopbackRemoteAddr(%q) = %v, want %v", tt.addr, got, tt.expect)
			}
		})
	}
}

func TestIsAllowedTerminalOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		expect bool
	}{
		{name: "empty origin", origin: "", expect: true},
		{name: "localhost", origin: "http://localhost:3000", expect: true},
		{name: "loopback ipv4", origin: "http://127.0.0.1:3000", expect: true},
		{name: "loopback ipv6", origin: "http://[::1]:3000", expect: true},
		{name: "wails localhost", origin: "http://wails.localhost", expect: true},
		{name: "wails scheme", origin: "wails://wails.localhost", expect: true},
		{name: "wails macOS production", origin: "wails://wails", expect: true},
		{name: "remote https", origin: "https://evil.example", expect: false},
		{name: "unsupported scheme", origin: "file://localhost/tmp", expect: false},
		{name: "malformed", origin: "://bad", expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowedTerminalOrigin(tt.origin)
			if got != tt.expect {
				t.Fatalf("isAllowedTerminalOrigin(%q) = %v, want %v", tt.origin, got, tt.expect)
			}
		})
	}
}

func TestAllowTerminalWSRequest(t *testing.T) {
	t.Run("allow loopback + allowed origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://127.0.0.1/terminal/x", nil)
		req.RemoteAddr = "127.0.0.1:52000"
		req.Header.Set("Origin", "http://localhost:3000")
		if !allowTerminalWSRequest(req) {
			t.Fatal("expected request to be allowed")
		}
	})

	t.Run("deny non-loopback even with allowed origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://127.0.0.1/terminal/x", nil)
		req.RemoteAddr = "10.10.10.10:52000"
		req.Header.Set("Origin", "http://localhost:3000")
		if allowTerminalWSRequest(req) {
			t.Fatal("expected request to be denied")
		}
	})

	t.Run("deny loopback with remote origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://127.0.0.1/terminal/x", nil)
		req.RemoteAddr = "127.0.0.1:52000"
		req.Header.Set("Origin", "https://evil.example")
		if allowTerminalWSRequest(req) {
			t.Fatal("expected request to be denied")
		}
	})
}
