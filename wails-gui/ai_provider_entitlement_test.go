package main

import "testing"

func TestIsByoaEntitledPlan(t *testing.T) {
	cases := []struct {
		name     string
		plan     string
		isActive bool
		want     bool
	}{
		// byoa / studio / max unlock BYOA.
		{"byoa active", "byoa", true, true},
		{"studio active", "studio", true, true},
		{"max active", "max", true, true},
		{"byoa uppercase", "BYOA", true, true},
		{"byoa padded", "  byoa  ", true, true},
		// pro is a paid plan but does NOT unlock BYOA (gateway-only tier).
		{"pro active not entitled", "pro", true, false},
		// free / empty / unknown / inactive never entitle.
		{"free active", "free", true, false},
		{"empty active", "", true, false},
		{"unknown plan", "enterprise", true, false},
		{"byoa inactive", "byoa", false, false},
		{"studio inactive", "studio", false, false},
		{"max inactive", "max", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isByoaEntitledPlan(c.plan, c.isActive); got != c.want {
				t.Errorf("isByoaEntitledPlan(%q, %v) = %v, want %v", c.plan, c.isActive, got, c.want)
			}
		})
	}
}
