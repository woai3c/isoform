package main

import "testing"

func TestTodayPermissionStatesDistinguishFirstRequestAndDenial(t *testing.T) {
	for _, test := range []struct {
		granted, asked bool
		setup, want    string
	}{
		{false, false, "off", "not_determined"},
		{false, true, "off", "denied"},
		{false, false, "denied", "denied"},
		{true, true, "denied", "granted"},
	} {
		if got := todayScreenPermissionState(test.granted, test.asked, test.setup); got != test.want {
			t.Fatalf("screen state = %q, want %q", got, test.want)
		}
	}
	for _, test := range []struct{ native, want string }{{"not_determined", "not_determined"}, {"granted", "granted"}, {"denied", "denied"}, {"restricted", "denied"}, {"write_only", "denied"}} {
		if got := todayCalendarPermissionState(test.native); got != test.want {
			t.Fatalf("calendar %q = %q, want %q", test.native, got, test.want)
		}
	}
}
