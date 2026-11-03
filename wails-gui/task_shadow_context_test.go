package main

import (
	"context"
	"testing"
)

func TestShadowExecutionOutlivesObserverButNotOwner(t *testing.T) {
	for _, ownerFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "observer_finishes_while_execution_queued", true: "owner_stops_before_observer"}[ownerFirst], func(t *testing.T) {
			owner, stop := context.WithCancel(context.Background())
			defer stop()
			observer, finish := newTaskShadowObserverContext(owner)
			defer finish()
			execution := taskShadowExecutionParent(observer)
			if ownerFirst {
				stop()
				if execution.Err() == nil || observer.Err() == nil {
					t.Fatal("owner cancellation did not reach both children")
				}
				return
			}
			finish()
			if observer.Err() == nil {
				t.Fatal("observer still active")
			}
			if execution.Err() != nil {
				t.Fatal("observer completion canceled queued execution")
			}
			stop()
			if execution.Err() == nil {
				t.Fatal("execution escaped owner cancellation")
			}
		})
	}
}
