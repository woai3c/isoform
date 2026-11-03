//go:build darwin

package main

import "testing"

func TestSetupNativePermissionSnapshotReadsTheSetupKeysButMail(t *testing.T) {
	snapshot := setupNativePermissions()
	var want []string
	for _, key := range setupPermissionOrder {
		if key != mailPermissionKey {
			want = append(want, key)
		}
	}
	if len(snapshot.Items) != len(want) {
		t.Fatalf("setup read %d permissions", len(snapshot.Items))
	}
	for i, item := range snapshot.Items {
		if item.Key != want[i] {
			t.Fatalf("unexpected permission at %d: %s", i, item.Key)
		}
	}
}
