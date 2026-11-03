//go:build darwin

package main

import (
	"errors"
	"testing"
)

// This exercises the Go prompt gate only. The cgo bridge requires a manual
// check in the signed app and must never ask the test runner for Contacts.
func TestRequestContactsAuthorizationRequiresUserAction(t *testing.T) {
	if permissionPromptsAllowed() {
		t.Fatal("permission prompts unexpectedly enabled before user action")
	}
	granted, err := requestContactsAuthorization()
	if granted || !errors.Is(err, errPermissionNotRequested) {
		t.Fatalf("background contacts request = (%v, %v), want denied before native call", granted, err)
	}
}
