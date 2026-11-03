package main

import "testing"

func TestTelemetryHostAcceptsOnlyIngestOrigins(t *testing.T) {
	for _, host := range []string{
		"https://us.i.posthog.com",
		"https://eu.i.posthog.com:443",
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	} {
		t.Run("valid/"+host, func(t *testing.T) {
			if !validTelemetryHost(host) {
				t.Fatalf("ingest origin %q rejected", host)
			}
		})
	}
	for _, host := range []string{
		"",
		"https://",
		"http://ingest.example",
		"https://ingest.example/path",
		"https://user@ingest.example",
		"https://ingest.example?key=value",
		"https://ingest.example?",
		"https://ingest.example#fragment",
		"https://ingest.example#",
	} {
		t.Run("invalid/"+host, func(t *testing.T) {
			if validTelemetryHost(host) {
				t.Fatalf("non-origin %q accepted", host)
			}
		})
	}
}
