package main

import "testing"

func TestIsOrphanedSpotlightHelperRequiresDetachedExactBinary(t *testing.T) {
	const binary = "/Applications/Yansu.app/Contents/MacOS/quick-chat-spotlight"
	tests := []struct {
		name       string
		currentPID int
		pid        int
		parentPID  int
		command    string
		want       bool
	}{
		{
			name:       "detached exact helper",
			currentPID: 100,
			pid:        200,
			parentPID:  1,
			command:    binary + " --listen-port=1234",
			want:       true,
		},
		{
			name:       "live child",
			currentPID: 100,
			pid:        200,
			parentPID:  100,
			command:    binary + " --listen-port=1234",
		},
		{
			name:       "different binary",
			currentPID: 100,
			pid:        200,
			parentPID:  1,
			command:    "/tmp/quick-chat-spotlight --listen-port=1234",
		},
		{
			name:       "current process",
			currentPID: 100,
			pid:        100,
			parentPID:  1,
			command:    binary,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isOrphanedSpotlightHelper(
				test.currentPID,
				test.pid,
				test.parentPID,
				test.command,
				binary,
			); got != test.want {
				t.Fatalf("orphan=%v, want %v", got, test.want)
			}
		})
	}
}
