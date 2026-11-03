package native

import (
	"context"
	"testing"
	"time"
)

func TestPermissionManager_ResolveAccept(t *testing.T) {
	p := NewPermissionManager()
	go func() {
		time.Sleep(20 * time.Millisecond)
		if err := p.Respond("sess1", "acceptEdits"); err != nil {
			t.Errorf("respond: %v", err)
		}
	}()
	res, err := p.Await(context.Background(), "sess1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Granted || res.OptionID != "acceptEdits" {
		t.Errorf("res = %+v", res)
	}
}

func TestPermissionManager_Reject(t *testing.T) {
	p := NewPermissionManager()
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = p.Respond("s", "reject")
	}()
	res, _ := p.Await(context.Background(), "s", 0)
	if res.Granted {
		t.Errorf("reject should not grant: %+v", res)
	}
}

func TestPermissionManager_Timeout(t *testing.T) {
	p := NewPermissionManager()
	res, err := p.Await(context.Background(), "s", 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res.Granted {
		t.Errorf("timeout should reject: %+v", res)
	}
}

func TestPermissionManager_CtxCancel(t *testing.T) {
	p := NewPermissionManager()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := p.Await(ctx, "s", time.Minute)
	if err == nil {
		t.Error("expected ctx.Err()")
	}
}

func TestPermissionManager_ConcurrentAwaitSameSession(t *testing.T) {
	p := NewPermissionManager()
	done := make(chan error, 1)
	go func() {
		_, err := p.Await(context.Background(), "s", 100*time.Millisecond)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	_, err := p.Await(context.Background(), "s", 100*time.Millisecond)
	if err == nil {
		t.Error("second concurrent Await should error")
	}
	<-done
}

func TestPermissionManager_RespondUnknownSession(t *testing.T) {
	p := NewPermissionManager()
	if err := p.Respond("nope", "accept"); err == nil {
		t.Error("expected error for unknown session")
	}
}

func TestPermissionManager_AlwaysFlag(t *testing.T) {
	p := NewPermissionManager()
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = p.Respond("s", "allow_always")
	}()
	res, _ := p.Await(context.Background(), "s", 0)
	if !res.Always {
		t.Errorf("always flag not set: %+v", res)
	}
}
