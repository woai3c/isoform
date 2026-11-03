package main

import (
	"errors"
	"net/http"
	"testing"
)

func TestIsAuthRejected(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"401 unauthorized", &apiHTTPError{StatusCode: http.StatusUnauthorized}, true},
		{"403 forbidden", &apiHTTPError{StatusCode: http.StatusForbidden}, true},
		{"404 not found", &apiHTTPError{StatusCode: http.StatusNotFound}, false},
		{"500 server error", &apiHTTPError{StatusCode: http.StatusInternalServerError}, false},
		{"wrapped 401", errors.Join(errors.New("ctx"), &apiHTTPError{StatusCode: http.StatusUnauthorized}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAuthRejected(tt.err); got != tt.want {
				t.Fatalf("isAuthRejected(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestApiGetClassifiesAuthFailures is the contract CheckAuth relies on: only a
// genuine credential rejection (401/403, including the HTML login-page redirect)
// must be reported as auth-rejected. Transient 5xx / network failures must not,
