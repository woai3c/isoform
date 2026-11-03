//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObservationAppDeclaredCategory(t *testing.T) {
	for _, tc := range []struct{ declared, want string }{
		{"public.app-category.finance", "finance"},
		{"public.app-category.medical", "health"},
		{"public.app-category.healthcare-fitness", "health"},
		{"public.app-category.productivity", ""},
	} {
		t.Run(tc.declared, func(t *testing.T) {
			bundle := filepath.Join(t.TempDir(), "Fixture.app")
			if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0700); err != nil {
				t.Fatal(err)
			}
			data := `<plist version="1.0"><dict><key>LSApplicationCategoryType</key><string>` + tc.declared + `</string></dict></plist>`
			if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if got := readObservationAppCategory(SystemApp{bundlePath: bundle}); got != tc.want {
				t.Fatalf("%q != %q", got, tc.want)
			}
		})
	}
	if got := readObservationAppCategory(SystemApp{}); got != "" {
		t.Fatal(got)
	}
}
