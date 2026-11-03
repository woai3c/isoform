package appparser

import "wails-gui/internal/platform/activitymon"

// AXNode is re-exported from activitymon so parser files can reference it
// without importing the activitymon package everywhere. This also lets the
// package stay self-contained for unit tests that construct fake trees.
type AXNode = activitymon.AXNode

// AXFrame is the bounding-box type re-exported from activitymon.
type AXFrame = activitymon.AXFrame

// FindFirst performs a depth-first walk of the tree and returns the first
// node where predicate returns true. Returns nil if no node matches.
func FindFirst(root *AXNode, predicate func(*AXNode) bool) *AXNode {
	if root == nil {
		return nil
	}
	if predicate(root) {
		return root
	}
	for _, c := range root.Children {
		if hit := FindFirst(c, predicate); hit != nil {
			return hit
		}
	}
	return nil
}

// FindAll performs a depth-first walk and returns every node where predicate
// returns true. Matched subtrees are NOT descended into (to avoid double-
// counting container matches). Swap to TraverseAll if you need full traversal.
func FindAll(root *AXNode, predicate func(*AXNode) bool) []*AXNode {
	var out []*AXNode
	if root == nil {
		return out
	}
	var walk func(n *AXNode)
	walk = func(n *AXNode) {
		if n == nil {
			return
		}
		if predicate(n) {
			out = append(out, n)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// Traverse walks every node depth-first, invoking visit on each. Return false
// from visit to stop further descent into that subtree.
func Traverse(root *AXNode, visit func(*AXNode) bool) {
	if root == nil {
		return
	}
	if !visit(root) {
		return
	}
	for _, c := range root.Children {
		Traverse(c, visit)
	}
}

// ChildrenByRole returns direct children of `parent` whose Role equals role.
func ChildrenByRole(parent *AXNode, role string) []*AXNode {
	if parent == nil {
		return nil
	}
	var out []*AXNode
	for _, c := range parent.Children {
		if c != nil && c.Role == role {
			out = append(out, c)
		}
	}
	return out
}

// IsSelected reports whether the node is currently marked selected by the
// app's accessibility tree. This is the "user choice" signal — Finder file
// rows, NSTableView selections, segmented control buttons, and similar
// surfaces expose user intent only through kAXSelectedAttribute and would
// otherwise be indistinguishable from non-selected siblings on role/value
// alone.
func IsSelected(n *AXNode) bool {
	if n == nil {
		return false
	}
	return n.Selected
}

// HasClass reports whether the node's domClassList contains any class name
// equal to the given value.
func HasClass(n *AXNode, className string) bool {
	if n == nil {
		return false
	}
	for _, c := range n.DOMClassList {
		if c == className {
			return true
		}
	}
	return false
}

// HasClassContaining reports whether any class name contains the given
// substring. Useful for matching dynamic CSS-in-JS class suffixes.
func HasClassContaining(n *AXNode, substr string) bool {
	if n == nil {
		return false
	}
	for _, c := range n.DOMClassList {
		if containsSubstring(c, substr) {
			return true
		}
	}
	return false
}

func containsSubstring(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Text returns a representative text for a node: description > value > title.
// Matches LittleBird's precedence in its runtime `val()` / `textish()` helpers.
func Text(n *AXNode) string {
	if n == nil {
		return ""
	}
	if n.Description != "" {
		return n.Description
	}
	if n.Value != "" {
		return n.Value
	}
	return n.Title
}

// CollectStaticTexts gathers all AXStaticText values (and their descriptions)
// from the subtree in traversal order.
func CollectStaticTexts(root *AXNode) []string {
	var out []string
	Traverse(root, func(n *AXNode) bool {
		if n.Role == "AXScrollBar" {
			return false
		}
		if n.Role == "AXStaticText" {
			t := n.Description
			if t == "" {
				t = n.Value
			}
			if t != "" {
				out = append(out, t)
			}
		}
		return true
	})
	return out
}
