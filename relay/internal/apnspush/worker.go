package apnspush

import (
	"context"
	"errors"
	"log"
	"time"

	"isoform-relay/internal/push"
)

// Outbox is the durable queue the worker leases from. The store implements it.
type Outbox interface {
	ClaimPush(ctx context.Context, now time.Time, lease time.Duration) (*push.Claim, error)
	ValidatePush(ctx context.Context, claim push.Claim, now time.Time) error
	FinishPush(ctx context.Context, claim push.Claim, outcome push.Outcome, now time.Time) error
}

// Sender performs one provider attempt for a leased claim.
type Sender interface {
	Send(context.Context, push.Claim) (push.Outcome, error)
}

// Worker drains the outbox one claim at a time.
type Worker struct {
	Outbox Outbox
	Sender Sender
}

// Run processes the outbox until ctx ends. Provider configuration errors pause
// dispatch until restart so bad credentials do not drain the queue.
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		worked, err := w.processOne(ctx)
		var configErr *configurationError
		if errors.As(err, &configErr) {
			log.Print("APNs rejected provider configuration; repair credentials or topic and restart to resume push dispatch")
			<-ctx.Done()
			return
		}
		if err != nil && ctx.Err() == nil {
			log.Print("push dispatch could not finish; durable retry state retained")
		}
		delay := 5 * time.Second
		if worked && err == nil {
			delay = 100 * time.Millisecond
		}
		var cooldown *providerCooldownError
		if errors.As(err, &cooldown) {
			delay = 20 * time.Minute
		}
		timer.Reset(delay)
	}
}

func (w *Worker) processOne(ctx context.Context) (bool, error) {
	claim, err := w.Outbox.ClaimPush(ctx, time.Now(), time.Minute)
	if err != nil || claim == nil {
		return false, err
	}
	if err = w.Outbox.ValidatePush(ctx, *claim, time.Now()); err != nil {
		if errors.Is(err, push.ErrLeaseLost) {
			return true, nil
		}
		return true, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	outcome, sendErr := w.Sender.Send(requestCtx, *claim)
	cancel()
	if ctx.Err() != nil {
		// Leave an uncertain in-flight send leased for recovery after shutdown.
		return true, ctx.Err()
	}
	var configErr *configurationError
	if errors.As(sendErr, &configErr) {
		return true, sendErr
	}
	if err = w.Outbox.FinishPush(ctx, *claim, outcome, time.Now()); errors.Is(err, push.ErrLeaseLost) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if sendErr != nil {
		log.Print("APNs did not confirm this attempt; its bounded delivery policy was recorded")
	}
	var cooldown *providerCooldownError
	if errors.As(sendErr, &cooldown) {
		return true, sendErr
	}
	return true, nil
}
