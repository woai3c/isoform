package agent

import (
	"context"
	"time"
)

type nativeFallbackBudgetKey struct{}

// WithNativeFallbackBudget reserves a fresh duration for the native attempt.
// The router caps that attempt to this budget without extending the caller's
// deadline; controlled assistants use it to preserve enough fallback time.
func WithNativeFallbackBudget(ctx context.Context, budget time.Duration) context.Context {
	return context.WithValue(ctx, nativeFallbackBudgetKey{}, budget)
}

// NativeFallbackBudget returns zero when the caller has no explicit reservation.
func NativeFallbackBudget(ctx context.Context) time.Duration {
	budget, _ := ctx.Value(nativeFallbackBudgetKey{}).(time.Duration)
	if budget < 0 {
		return 0
	}
	return budget
}
