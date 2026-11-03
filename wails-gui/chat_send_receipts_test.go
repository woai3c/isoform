package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestOrdinarySendReceiptRetriesWithoutDuplicateAdmission(t *testing.T) {
	var receipts sync.Map
	var calls atomic.Int32
	admitted := make(chan struct{})
	release := make(chan struct{})
	send := func() error { calls.Add(1); close(admitted); <-release; return nil }
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := onceChatSend(&receipts, "chat", "turn", send); err != nil {
			t.Error(err)
		}
	}()
	<-admitted
	go func() {
		defer wg.Done()
		if err := onceChatSend(&receipts, "chat", "turn", send); err != nil {
			t.Error(err)
		}
	}()
	close(release)
	wg.Wait()
	if err := onceChatSend(&receipts, "chat", "turn", send); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("admissions = %d, want 1", got)
	}
}

func TestOrdinarySendReceiptAllowsRetryAfterRejection(t *testing.T) {
	var receipts sync.Map
	rejected := errors.New("not admitted")
	if err := onceChatSend(&receipts, "chat", "turn", func() error { return rejected }); !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	called := false
	if err := onceChatSend(&receipts, "chat", "turn", func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("retry: called=%v err=%v", called, err)
	}
}
