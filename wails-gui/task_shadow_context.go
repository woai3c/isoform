package main

import (
	"context"
)

type taskShadowExecutionParentKey struct{}

// Observer completion ends bookkeeping, not execution already dispatched from
// it. Both remain bounded by the original owner and their own timeouts.
func newTaskShadowObserverContext(parent context.Context) (context.Context, context.CancelFunc) {
	observer, cancel := context.WithTimeout(parent, taskShadowPipelineTimeout)
	return context.WithValue(observer, taskShadowExecutionParentKey{}, parent), cancel
}
func taskShadowExecutionParent(ctx context.Context) context.Context {
	if parent, ok := ctx.Value(taskShadowExecutionParentKey{}).(context.Context); ok {
		return parent
	}
	return ctx
}
