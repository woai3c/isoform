//go:build !darwin && !windows

package activitymon

// On platforms that have no accessibility-tree implementation we expose the
// same surface as darwin / windows so callers can compile and rely on a nil
// return. AXNode / AXFrame types live in axtree_tree_types.go.

// CollectFocusedWindowTree is a no-op on unsupported platforms.
func CollectFocusedWindowTree() *AXNode {
	return nil
}

// CollectFocusedWindowTreeJSON is a no-op on unsupported platforms.
func CollectFocusedWindowTreeJSON() string {
	return ""
}

// CollectWindowTreeByBundleJSON is a no-op on unsupported platforms.
func CollectWindowTreeByBundleJSON(_ string) string {
	return ""
}

// CollectWindowTreeByBundle is a no-op on unsupported platforms.
func CollectWindowTreeByBundle(_ string) *AXNode {
	return nil
}

// CollectWindowTreeByBundleIndexJSON is a no-op on unsupported platforms.
func CollectWindowTreeByBundleIndexJSON(_ string, _ int) string {
	return ""
}

// CollectWindowTreeByBundleIndex is a no-op on unsupported platforms.
func CollectWindowTreeByBundleIndex(_ string, _ int) *AXNode {
	return nil
}

// ListWindowsByBundleJSON is a no-op on unsupported platforms.
func ListWindowsByBundleJSON(_ string) string {
	return ""
}
