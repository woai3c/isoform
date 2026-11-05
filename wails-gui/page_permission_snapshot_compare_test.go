package main

import "testing"

// A probe that returns an empty list where the stored view has none is the
// same answer: it must not emit permissions:changed (the storm that re-read
// every settings page on each refresh).
func TestPermissionViewChangedIgnoresNilVersusEmptyItems(t *testing.T) {
	stored := clonePlatformPermissions(PlatformPermissions{})
	probe := PlatformPermissions{Items: []PermissionItem{}}
	if permissionViewChanged(stored, probe) {
		t.Fatal("an empty probe against a nil stored list counted as a change")
	}
	if permissionViewChanged(stored, PlatformPermissions{}) {
		t.Fatal("identical views counted as a change")
	}
}
