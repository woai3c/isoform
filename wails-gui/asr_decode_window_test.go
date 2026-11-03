package main

import "testing"

func TestOwnedDecodeWindowDoesNotReplayAdjacentSpeech(t *testing.T) {
	samples := make([]float32, 100)
	for i := range samples {
		samples[i] = float32(i + 1)
	}
	for _, tc := range []struct {
		name                string
		leftEnd, rightStart int
	}{
		{"pause with wide Chinese padding", 40, 50},
		{"forced split without silence", 45, 45},
	} {
		t.Run(tc.name, func(t *testing.T) {
			boundary := (tc.leftEnd + tc.rightStart) / 2
			left := ownedDecodeWindow(samples, 0, 10, tc.leftEnd-10, 30, 0, boundary)
			right := ownedDecodeWindow(samples, 0, tc.rightStart, 80-tc.rightStart, 30, boundary, 100)
			seen := map[float32]bool{}
			for _, v := range left {
				seen[v] = true
			}
			for _, v := range right {
				if seen[v] {
					t.Fatalf("sample %v decoded in both windows", v)
				}
			}
			for i := 10; i < tc.leftEnd; i++ {
				if !seen[samples[i]] {
					t.Fatalf("left speech sample %d lost", i)
				}
			}
			if len(right) == 0 || right[0] > samples[tc.rightStart] {
				t.Fatal("right speech clipped")
			}
		})
	}
}

func TestOwnedDecodeWindowWithRotatedHistory(t *testing.T) {
	samples := make([]float32, 100)
	for i := range samples {
		samples[i] = float32(i + 100)
	}
	got := ownedDecodeWindow(samples, 100, 120, 30, 10, 115, 160)
	if len(got) != 45 || got[0] != 115 || got[len(got)-1] != 159 {
		t.Fatalf("rotated offsets corrupted: %v", got)
	}
	if got := ownedDecodeWindow(samples, 100, 90, 30, 10, 0, 200); got != nil {
		t.Fatal("missing core audio must not look like a complete decode window")
	}
}
