package activitymon

import "testing"

func TestPrivateBrowsingRequiresModeMarker(t *testing.T) {
	for _, title := range []string{"How to use Incognito - Google Chrome", "Private Browsing explained - Safari", "InPrivate settings - Microsoft Edge", "了解无痕浏览 - Google Chrome", "无痕模式使用指南 - Google Chrome", "Incognito - Movie review", "无痕 - 电影"} {
		if IsPrivateBrowsing(title) {
			t.Errorf("ordinary page classified private: %q", title)
		}
	}
	for _, title := range []string{"Private Browsing", "New tab - Google Chrome (Incognito)", "Page - Mozilla Firefox (Private Browsing)", "Page - Microsoft Edge InPrivate", "Page - Safari [Private Browsing]", "Page - Google Chrome (无痕)", "新标签页 - 无痕模式"} {
		if !IsPrivateBrowsing(title) {
			t.Errorf("private marker missed: %q", title)
		}
	}
}

func TestPrivateBrowsingDelimitedMarkerPrefixes(t *testing.T) {
	for _, marker := range []string{"Private Browsing", "InPrivate", "无痕浏览", "无痕模式", "隐身模式"} {
		for _, separator := range []string{" - ", " — "} {
			title := marker + separator + "Health portal"
			t.Run(title, func(t *testing.T) {
				if !IsPrivateBrowsing(title) {
					t.Fatalf("private marker prefix missed: %q", title)
				}
			})
		}
	}
	if !IsPrivateBrowsing("Page — 无痕浏览") {
		t.Fatal("Chinese private-mode suffix missed")
	}
}
