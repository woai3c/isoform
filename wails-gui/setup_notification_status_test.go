package main

import "testing"

func TestSetupNotificationGrantedUsesLiveProbe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []PermissionItem
		want  *bool
	}{
		{"granted", []PermissionItem{{Key: "notifications", Granted: true}}, boolPtr(true)},
		{"denied", []PermissionItem{{Key: "notifications", Granted: false}}, boolPtr(false)},
		{"unavailable", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := setupNotificationGranted(PlatformPermissions{Items: tc.items})
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("status = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetupNotificationStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   *bool
	}{
		{-1, nil}, {0, boolPtr(false)}, {1, boolPtr(false)}, {2, boolPtr(true)}, {3, boolPtr(true)},
	} {
		got := setupNotificationStatus(tc.status)
		if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Fatalf("status %d = %v, want %v", tc.status, got, tc.want)
		}
	}
}
