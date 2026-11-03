package main

import (
	"sync"
	"testing"
)

func TestPermissionPromptsStayBlockedUnderGoTest(t *testing.T) {
	if permissionPromptsAllowed() {
		t.Fatal("prompts must be off outside a request")
	}
	release := allowPermissionPrompts()
	if permissionPromptsAllowed() {
		t.Fatal("prompts must stay off inside a request under go test")
	}
	release()
	release() // a second release is a no-op
	if permissionPromptsAllowed() {
		t.Fatal("prompts must be off again after release")
	}
}

// Requests from the UI run one at a time.
func TestRunPermissionRequestSerializes(t *testing.T) {
	var (
		mu      sync.Mutex
		active  int
		maxSeen int
		wg      sync.WaitGroup
	)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runPermissionRequest(func() {
				mu.Lock()
				active++
				if active > maxSeen {
					maxSeen = active
				}
				mu.Unlock()
				if permissionPromptsAllowed() {
					t.Error("prompts must stay blocked inside the request under go test")
				}
				mu.Lock()
				active--
				mu.Unlock()
			})
		}()
	}
	wg.Wait()
	if maxSeen != 1 {
		t.Fatalf("requests overlapped: max concurrent = %d", maxSeen)
	}
	if permissionPromptsAllowed() {
		t.Fatal("prompts must be off after every request returned")
	}
}
