package main

import "testing"

func TestResolveBackgroundListeningEnabled_Defaults(t *testing.T) {
	tests := []struct {
		name         string
		cfg          yansuConfig
		configExists bool
		want         bool
	}{
		{
			name:         "fresh install defaults on",
			cfg:          yansuConfig{},
			configExists: false,
			want:         true,
		},
		{
			name:         "upgrade stays off until configured",
			cfg:          yansuConfig{},
			configExists: true,
			want:         false,
		},
		{
			name: "explicit enable wins",
			cfg: yansuConfig{
				BackgroundListeningConfigured: true,
				BackgroundListeningDisabled:   false,
			},
			configExists: true,
			want:         true,
		},
		{
			name: "explicit disable wins",
			cfg: yansuConfig{
				BackgroundListeningConfigured: true,
				BackgroundListeningDisabled:   true,
			},
			configExists: false,
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveBackgroundListeningEnabled(tt.cfg, tt.configExists); got != tt.want {
				t.Fatalf("resolveBackgroundListeningEnabled(%+v, %t) = %t, want %t", tt.cfg, tt.configExists, got, tt.want)
			}
		})
	}
}

func TestEnsureBackgroundListeningDefaultForFreshInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ensureBackgroundListeningDefaultForFreshInstall()

	cfg := loadConfig()
	if !cfg.BackgroundListeningConfigured {
		t.Fatal("expected background listening to be marked configured for fresh installs")
	}
	if cfg.BackgroundListeningDisabled {
		t.Fatal("expected fresh-install background listening default to be enabled")
	}
}

func TestResolveBackgroundListeningSource(t *testing.T) {
	got := resolveBackgroundListeningSource(yansuConfig{})
	want := backgroundListeningSourceMicrophone
	if isBackgroundListeningSourceSupported(backgroundListeningSourceBoth) {
		want = backgroundListeningSourceBoth
	}
	if got != want {
		t.Fatalf("resolveBackgroundListeningSource() = %q, want %q", got, want)
	}

	t.Run("legacy source config no longer overrides combined mode", func(t *testing.T) {
		for _, cfg := range []yansuConfig{
			{BackgroundListeningSource: string(backgroundListeningSourceMicrophone)},
			{BackgroundListeningSource: string(backgroundListeningSourceSystem)},
			{BackgroundListeningSource: "speakerphone"},
		} {
			if got := resolveBackgroundListeningSource(cfg); got != want {
				t.Fatalf("resolveBackgroundListeningSource(%+v) = %q, want %q", cfg, got, want)
			}
		}
	})
}
