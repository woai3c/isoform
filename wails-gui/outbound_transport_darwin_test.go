//go:build darwin

package main

import (
	"context"
	"errors"
	"testing"
)

func TestClassifyMainChatMailSendError(t *testing.T) {
	ctx := context.Background()
	if err := classifyMainChatMailSendError(ctx, ctx, errors.New("exit"), "Mail got an error: (-600)"); !errors.Is(err, errMailClosed) {
		t.Fatalf("procNotFound = %v", err)
	}
	if err := classifyMainChatMailSendError(ctx, ctx, errors.New("exit"), "Not authorized to send Apple events"); !errors.Is(err, errMailNotAllowed) {
		t.Fatalf("permission refusal = %v", err)
	}
	classified := classifyMainChatMailSendError(ctx, ctx, errors.New("exit"), "AppleEvent error")
	var transportErr *proactiveSendTransportError
	if !errors.As(classified, &transportErr) || !transportErr.unknown {
		t.Fatalf("other script error = %v", classified)
	}
}
