package pii

import "testing"

func resetUserApps() {
	SetUserSensitiveApps(map[string]string{})
}

func TestIsCoveredByDefaults(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   bool
	}{
		{name: "exact default match", prefix: "com.apple.Passwords", want: true},
		{name: "case-insensitive match", prefix: "com.apple.passwords", want: true},
		{name: "longer bundle id under default prefix", prefix: "com.1password.desktop", want: true},
		{name: "unrelated app", prefix: "com.example.notes", want: false},
		{name: "shorter prefix than default is not covered", prefix: "com.apple", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsCoveredByDefaults(tt.prefix); got != tt.want {
				t.Errorf("IsCoveredByDefaults(%q) = %v, want %v", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestSetUserSensitiveAppsPrunesDefaultCoveredEntries(t *testing.T) {
	defer resetUserApps()

	// Simulates a legacy config saved before Apple Passwords became a
	// hardcoded default: the user-added entry must be pruned on load.
	SetUserSensitiveApps(map[string]string{
		"com.apple.Passwords": "Passwords",
		"com.example.notes":   "Notes",
	})

	got := GetUserSensitiveApps()
	if _, ok := got["com.apple.Passwords"]; ok {
		t.Error("default-covered entry com.apple.Passwords should be pruned")
	}
	if _, ok := got["com.example.notes"]; !ok {
		t.Error("non-default entry com.example.notes should be kept")
	}
}

func TestAddUserSensitiveAppIgnoresDefaultCoveredEntries(t *testing.T) {
	defer resetUserApps()
	resetUserApps()

	AddUserSensitiveApp("com.apple.passwords", "Passwords")
	if len(GetUserSensitiveApps()) != 0 {
		t.Error("adding a default-covered app should be a no-op")
	}

	AddUserSensitiveApp("com.example.notes", "Notes")
	if _, ok := GetUserSensitiveApps()["com.example.notes"]; !ok {
		t.Error("adding a non-default app should still work")
	}
}

func TestIsSensitiveAppName(t *testing.T) {
	defer resetUserApps()

	SetUserSensitiveApps(map[string]string{
		"com.apple.journal":     "手记",
		"com.tencent.xinWeChat": "微信",
		"com.example.diary":     "My Diary",
	})

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "cjk display name", in: "手记", want: true},
		{name: "second cjk entry", in: "微信", want: true},
		{name: "latin display name", in: "My Diary", want: true},
		{name: "case-insensitive", in: "my diary", want: true},
		{name: "surrounding whitespace", in: "  手记  ", want: true},
		{name: "unprotected app", in: "kitty", want: false},
		{name: "default password manager", in: "1Password", want: true},
		{name: "default system passwords", in: "Passwords", want: true},
		{name: "default localized keychain", in: "钥匙串访问", want: true},
		{name: "empty string", in: "", want: false},
		{name: "bundle id is not a name", in: "com.apple.journal", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSensitiveAppName(tt.in); got != tt.want {
				t.Errorf("IsSensitiveAppName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFilterSensitiveAppNames(t *testing.T) {
	defer resetUserApps()

	SetUserSensitiveApps(map[string]string{
		"com.apple.journal": "手记",
	})

	got := FilterSensitiveAppNames([]string{"kitty", "手记", "Google Chrome"})
	if len(got) != 2 || got[0] != "kitty" || got[1] != "Google Chrome" {
		t.Errorf("FilterSensitiveAppNames = %v, want [kitty Google Chrome]", got)
	}

	// Nothing filtered: order and content preserved.
	got = FilterSensitiveAppNames([]string{"kitty", "Figma"})
	if len(got) != 2 {
		t.Errorf("unexpected filtering of unprotected apps: %v", got)
	}

	// Empty input stays empty (not nil-panics).
	if got := FilterSensitiveAppNames(nil); len(got) != 0 {
		t.Errorf("FilterSensitiveAppNames(nil) = %v, want empty", got)
	}
}

func TestGetAllSensitiveAppsHasNoDuplicatePrefixes(t *testing.T) {
	defer resetUserApps()

	SetUserSensitiveApps(map[string]string{
		"com.apple.Passwords": "Passwords",
		"com.example.notes":   "Notes",
	})

	seen := map[string]bool{}
	for _, e := range GetAllSensitiveApps() {
		if seen[e.BundlePrefix] {
			t.Errorf("duplicate bundle prefix in list: %q", e.BundlePrefix)
		}
		seen[e.BundlePrefix] = true
	}
}
