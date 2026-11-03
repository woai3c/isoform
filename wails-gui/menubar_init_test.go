package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMenuBarInitStateMachine(t *testing.T) {
	t.Run("success stops after acknowledgement", func(t *testing.T) {
		results := make(chan menuBarInitResult, 3)
		var attempts []int
		err := runMenuBarInit(context.Background(), func(attempt int) {
			attempts = append(attempts, attempt)
			results <- menuBarInitResult{attempt: attempt}
		}, results, time.Second, []time.Duration{time.Millisecond, time.Millisecond})
		if err != nil || !reflect.DeepEqual(attempts, []int{1}) {
			t.Fatalf("attempts=%v error=%v", attempts, err)
		}
	})
	t.Run("failure retries and recovers", func(t *testing.T) {
		results := make(chan menuBarInitResult, 3)
		var attempts []int
		start := time.Now()
		err := runMenuBarInit(context.Background(), func(attempt int) {
			attempts = append(attempts, attempt)
			var failure error
			if attempt < 3 {
				failure = errors.New("button unavailable")
			}
			results <- menuBarInitResult{attempt: attempt, err: failure}
		}, results, time.Second, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond})
		if err != nil || !reflect.DeepEqual(attempts, []int{1, 2, 3}) || time.Since(start) < 30*time.Millisecond {
			t.Fatalf("attempts=%v elapsed=%v error=%v", attempts, time.Since(start), err)
		}
	})
	t.Run("unresponsive main queue is bounded", func(t *testing.T) {
		results := make(chan menuBarInitResult, 3)
		var attempts []int
		err := runMenuBarInit(context.Background(), func(attempt int) { attempts = append(attempts, attempt) }, results, time.Millisecond, []time.Duration{time.Millisecond, 2 * time.Millisecond})
		if err == nil || !reflect.DeepEqual(attempts, []int{1, 2, 3}) {
			t.Fatalf("attempts=%v error=%v", attempts, err)
		}
	})
	t.Run("late success cancels retries", func(t *testing.T) {
		results := make(chan menuBarInitResult, 3)
		var attempts []int
		err := runMenuBarInit(context.Background(), func(attempt int) {
			attempts = append(attempts, attempt)
			if attempt == 2 {
				results <- menuBarInitResult{attempt: 1}
			}
		}, results, time.Millisecond, []time.Duration{time.Millisecond, time.Millisecond})
		if err != nil || !reflect.DeepEqual(attempts, []int{1, 2}) {
			t.Fatalf("attempts=%v error=%v", attempts, err)
		}
	})
	t.Run("shutdown cancels a queued attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var attempts []int
		err := runMenuBarInit(ctx, func(attempt int) { attempts = append(attempts, attempt); cancel() }, make(chan menuBarInitResult), time.Second, []time.Duration{time.Second, time.Second})
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(attempts, []int{1}) {
			t.Fatalf("attempts=%v error=%v", attempts, err)
		}
	})
}
