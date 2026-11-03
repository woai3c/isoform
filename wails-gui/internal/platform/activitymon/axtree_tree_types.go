package activitymon

// AXNode is a structured representation of a platform accessibility element.
// Produced by CollectFocusedWindowTree() and consumed by app-specific parsers
// in the appparser sub-package.
//
// On macOS the JSON is emitted by the C tree walker in axtree_tree_darwin.go
// and unmarshalled here. On Windows the same shape is produced by the
// UI Automation subprocess in axtree_tree_windows.go (see uia_windows.ps1).
// On unsupported platforms the struct exists but no walker populates it.
type AXNode struct {
	Role         string    `json:"role"`
	Subrole      string    `json:"subrole,omitempty"`
	Title        string    `json:"title,omitempty"`
	Description  string    `json:"description,omitempty"`
	Value        string    `json:"value,omitempty"`
	Identifier   string    `json:"identifier,omitempty"`
	Selected     bool      `json:"selected,omitempty"`
	DOMClassList []string  `json:"domClassList,omitempty"`
	Frame        *AXFrame  `json:"frame,omitempty"`
	Children     []*AXNode `json:"children,omitempty"`
}

// AXFrame holds the on-screen bounding box of an AXNode.
//
// macOS reports CGRect screen coordinates in device-independent points.
// Windows reports BoundingRectangle in physical pixels and the UIA bridge
// divides by the focused window's DPI scale so the values are comparable
// (device-independent pixels relative to the virtual screen origin).
type AXFrame struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}
