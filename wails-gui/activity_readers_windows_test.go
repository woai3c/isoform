//go:build windows

package main

import "testing"

func TestActivityStorageWindowsFileURIs(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{`C:\Users\Test Name\activity.db`, "file:///C:/Users/Test%20Name/activity.db"},
		{`\\server\share\Test Name\activity.db`, "file:////server/share/Test%20Name/activity.db"},
	} {
		uri := activityDatabaseURI(tc.path)
		if uri.String() != tc.want {
			t.Fatalf("URI %q = %q, want %q", tc.path, uri.String(), tc.want)
		}
	}
}
