package storage

import (
	"context"
	"testing"
	"time"
)

func TestMobileReceiptsReplayAndKeepFirstAnswer(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := "mobile:device-1:send-1"

	if _, found, err := store.MobileReceipt(ctx, key); err != nil || found {
		t.Fatalf("empty store: found=%v err=%v", found, err)
	}
	first := MobileReceipt{DeviceID: "device-1", RequestKey: key, Route: "POST /chats/c1/messages", Status: 202, Body: `{"ok":true,"data":{"turnId":"t1"}}`, At: time.Now().UnixMilli()}
	if err := store.SaveMobileReceipt(ctx, first); err != nil {
		t.Fatal(err)
	}
	// A racing duplicate must not replace the answer the phone already has.
	second := first
	second.Status, second.Body = 202, `{"ok":true,"data":{"turnId":"t2"}}`
	if err := store.SaveMobileReceipt(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.MobileReceipt(ctx, key)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if got.Status != 202 || got.Body != first.Body || got.Route != first.Route || got.DeviceID != "device-1" {
		t.Fatalf("receipt was overwritten: %+v", got)
	}
}

func TestMobileReceiptsAreNamespacedPerKey(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.Now().UnixMilli()
	for _, key := range []string{"mobile:device-1:k", "mobile:device-2:k"} {
		if err := store.SaveMobileReceipt(ctx, MobileReceipt{DeviceID: key, RequestKey: key, Route: "POST /chats", Status: 201, Body: "{}", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"mobile:device-1:k", "mobile:device-2:k"} {
		if _, found, err := store.MobileReceipt(ctx, key); err != nil || !found {
			t.Fatalf("%s: found=%v err=%v", key, found, err)
		}
	}
}

func TestPruneMobileReceiptsKeepsTheRetentionWindow(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	old := now.Add(-MobileReceiptRetention - time.Hour)
	recent := now.Add(-MobileReceiptRetention + time.Hour)
	for _, at := range []time.Time{old, recent} {
		key := "mobile:d:" + at.Format(time.RFC3339)
		if err := store.SaveMobileReceipt(ctx, MobileReceipt{DeviceID: "d", RequestKey: key, Route: "POST /chats", Status: 201, Body: "{}", At: at.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.PruneMobileReceipts(ctx, now)
	if err != nil || removed != 1 {
		t.Fatalf("prune removed %d (err %v), want 1", removed, err)
	}
	if _, found, _ := store.MobileReceipt(ctx, "mobile:d:"+old.Format(time.RFC3339)); found {
		t.Fatal("receipt older than the retention window survived")
	}
	if _, found, _ := store.MobileReceipt(ctx, "mobile:d:"+recent.Format(time.RFC3339)); !found {
		t.Fatal("receipt inside the retention window was pruned")
	}
}
