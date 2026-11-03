package activitymon

import (
	"runtime"
	"testing"
)

func TestBrowserIdentityIsAvailableWithoutNativeCapture(t *testing.T) {
	browser := "com.google.Chrome"
	if runtime.GOOS == "windows" {
		browser = "chrome.exe"
	}
	if !IsBrowser(browser) {
		t.Fatalf("known browser %q was not recognized", browser)
	}
	if IsBrowser("com.example.editor") {
		t.Fatal("ordinary editor recognized as browser")
	}
}
