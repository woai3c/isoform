package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/slack-go/slack"
)

func TestClassifySlackSendErrorProtectsUncertainOutcomes(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"server", slack.StatusCodeError{Code: http.StatusServiceUnavailable, Status: "503"}},
		{"rate limit status", slack.StatusCodeError{Code: http.StatusTooManyRequests, Status: "429"}},
		{"rate limit body", &slack.RateLimitedError{}},
		{"network", &net.DNSError{Err: "temporary", IsTemporary: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			classified := classifySlackSendError(context.Background(), test.err)
			var transportErr *proactiveSendTransportError
			if !errors.As(classified, &transportErr) || !transportErr.unknown {
				t.Fatalf("classified as definitive: %T %v", classified, classified)
			}
		})
	}
	for _, err := range []error{
		slack.StatusCodeError{Code: http.StatusBadRequest, Status: "400"},
		slack.SlackErrorResponse{Err: "invalid_auth"},
	} {
		classified := classifySlackSendError(context.Background(), err)
		var transportErr *proactiveSendTransportError
		if errors.As(classified, &transportErr) && transportErr.unknown {
			t.Fatalf("clear rejection became unknown: %v", classified)
		}
	}
}
