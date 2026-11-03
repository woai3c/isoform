//go:build !darwin && !windows

package activitymon

// CollectFocusedWindowText is a no-op on non-darwin platforms.
func CollectFocusedWindowText() string {
	return ""
}
