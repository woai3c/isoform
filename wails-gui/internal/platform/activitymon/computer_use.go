package activitymon

// UIElement represents a single UI element from the accessibility tree.
type UIElement struct {
	ID              string  `json:"id"`
	Role            string  `json:"role"`
	Title           string  `json:"title,omitempty"`
	Label           string  `json:"label,omitempty"`
	Value           string  `json:"value,omitempty"`
	Description     string  `json:"description,omitempty"`
	RoleDescription string  `json:"role_description,omitempty"`
	Help            string  `json:"help,omitempty"`
	Identifier      string  `json:"identifier,omitempty"`
	IsActionable    bool    `json:"is_actionable"`
	X               float64 `json:"x"`
	Y               float64 `json:"y"`
	Width           float64 `json:"width"`
	Height          float64 `json:"height"`
}

// RunningApp represents a running application.
type RunningApp struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
	PID      int    `json:"pid"`
	IsActive bool   `json:"isActive"`
}

// AppWindow represents a window of an application.
type AppWindow struct {
	Title  string  `json:"title"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	IsMain bool    `json:"isMain"`
}
