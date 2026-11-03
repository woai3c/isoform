package ocr

import (
	"reflect"
	"testing"
)

func TestResolveLanguages(t *testing.T) {
	tests := []struct {
		name      string
		preferred []string
		supported []string
		want      []string
	}{
		{
			name:      "nil supported returns preferred",
			preferred: []string{"zh-Hans", "en-US"},
			supported: nil,
			want:      []string{"zh-Hans", "en-US"},
		},
		{
			name:      "empty supported returns preferred",
			preferred: []string{"zh-Hans", "en-US"},
			supported: []string{},
			want:      []string{"zh-Hans", "en-US"},
		},
		{
			name:      "filters unsupported languages",
			preferred: []string{"zh-Hans", "zh-Hant", "en-US"},
			supported: []string{"en-US", "zh-Hans", "fr-FR"},
			want:      []string{"zh-Hans", "en-US"},
		},
		{
			name:      "preserves preferred ordering",
			preferred: []string{"zh-Hans", "en-US"},
			supported: []string{"en-US", "zh-Hans", "de-DE"},
			want:      []string{"zh-Hans", "en-US"},
		},
		{
			name:      "appends discovered CJK languages",
			preferred: []string{"zh-Hans", "en-US"},
			supported: []string{"en-US", "zh-Hans", "ja-JP", "ko-KR"},
			want:      []string{"zh-Hans", "en-US", "ja-JP", "ko-KR"},
		},
		{
			name:      "appends zh-Hant when discovered",
			preferred: []string{"zh-Hans", "en-US"},
			supported: []string{"en-US", "zh-Hans", "zh-Hant"},
			want:      []string{"zh-Hans", "en-US", "zh-Hant"},
		},
		{
			name:      "no duplicates",
			preferred: []string{"zh-Hans", "zh-Hant", "en-US"},
			supported: []string{"zh-Hans", "zh-Hant", "en-US", "ja-JP"},
			want:      []string{"zh-Hans", "zh-Hant", "en-US", "ja-JP"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveLanguages(tt.preferred, tt.supported)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ResolveLanguages(%v, %v) = %v, want %v",
					tt.preferred, tt.supported, got, tt.want)
			}
		})
	}
}
