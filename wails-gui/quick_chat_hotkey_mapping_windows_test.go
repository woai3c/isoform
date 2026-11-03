package main

import "testing"

func TestQuickChatWindowsPresetKeyMapping(t *testing.T) {
	// Every enabled Windows preset uses Space; sentinels are not virtual keys.
	for _, tc := range []struct {
		key     string
		vk      uint16
		matched bool
	}{
		{" ", 0x20, true},
		{"space", 0x20, true},
		{"opt-opt", 0, false},
		{"off", 0, false},
	} {
		vk, matched := vkCodeForKey(tc.key)
		if vk != tc.vk || matched != tc.matched {
			t.Errorf("key %q: got (%#x, %v), want (%#x, %v)", tc.key, vk, matched, tc.vk, tc.matched)
		}
	}
}
