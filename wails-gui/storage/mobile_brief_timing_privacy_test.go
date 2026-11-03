package storage

import (
	"context"
	"sync"
	"testing"
)

func TestMobileBriefTimingPrivacyCoverageInitializesOnce(t *testing.T) {
	s := newTestStore(t)
	var wg sync.WaitGroup
	for index := 0; index < 16; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if since, err := s.EnsureMobileBriefTimingPrivacySince(ctx, 1000); err != nil || since != 1000 {
				t.Errorf("concurrent initialization = %d, %v", since, err)
			}
		}()
	}
	wg.Wait()
	if since, err := s.EnsureMobileBriefTimingPrivacySince(ctx, 2000); err != nil || since != 1000 {
		t.Fatalf("later read moved coverage: %d, %v", since, err)
	}
}

func TestMobileBriefTimingPrivacyCoverageFailsClosed(t *testing.T) {
	for _, value := range []string{"", "not-json", "0", "-1", "2000"} {
		t.Run(value, func(t *testing.T) {
			s := newTestStore(t)
			if err := s.SetUIState(ctx, mobileBriefTimingPrivacySinceKey, value); err != nil {
				t.Fatal(err)
			}
			if _, err := s.EnsureMobileBriefTimingPrivacySince(ctx, 1000); err == nil {
				t.Fatal("invalid coverage was accepted")
			}
			if raw, _ := s.GetUIState(ctx, mobileBriefTimingPrivacySinceKey); raw != value {
				t.Fatal("invalid coverage was overwritten")
			}
		})
	}
	t.Run("write failure", func(t *testing.T) {
		s := newTestStore(t)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.EnsureMobileBriefTimingPrivacySince(cancelled, 1000); err == nil {
			t.Fatal("failed initialization authorized timing")
		}
		if raw, _ := s.GetUIState(ctx, mobileBriefTimingPrivacySinceKey); raw != "" {
			t.Fatal("failed initialization persisted coverage")
		}
	})
}
