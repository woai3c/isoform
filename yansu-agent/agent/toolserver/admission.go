package toolserver

import "context"

type actionAdmissionKey struct{}

// WithActionAdmission lets the attempt timer and the first authorized action
// agree on which one wins before any action is written to the durable ledger.
// The callback must hold its admission lock while checking expiry and calling
// start, and disarm its pre-action timer only when start succeeds. It returns
// before the receipt callback or tool execution. Caller deadlines stay active.
func WithActionAdmission(ctx context.Context, admit func(start func() error) error) context.Context {
	return context.WithValue(ctx, actionAdmissionKey{}, admit)
}

func admitAction(ctx context.Context, start func() error) error {
	if admit, ok := ctx.Value(actionAdmissionKey{}).(func(func() error) error); ok && admit != nil {
		return admit(start)
	}
	return start()
}
