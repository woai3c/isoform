// Package notify provides cross-platform desktop notifications.
// Platform-specific implementations live in build-tagged files.
package notify

import "sync"

var (
	clickMu      sync.Mutex
	clickHandler ClickHandler
)

// SetClickHandler registers a function called when the user clicks a
// delivered desktop notification. Only one handler can be active; calling
// this again replaces the previous handler.
func SetClickHandler(handler ClickHandler) {
	clickMu.Lock()
	defer clickMu.Unlock()
	clickHandler = handler
}

// invokeClickHandler dispatches the click event to the registered handler.
// Called from platform-specific code; safe to call from any goroutine.
func invokeClickHandler(clickData string) {
	clickMu.Lock()
	handler := clickHandler
	clickMu.Unlock()
	if handler != nil && clickData != "" {
		go handler(clickData)
	}
}

// ClickHandler is called when the user clicks a delivered desktop notification.
// The clickData argument is the same string that was passed in Options.ClickData.
type ClickHandler func(clickData string)

// Options configures optional notification behaviour.
type Options struct {
	// IconPath is the absolute path to a .png or .ico file shown beside the
	// notification. Leave empty to use the OS default icon.
	IconPath string

	// ClickData is an opaque string attached to the notification and returned
	// via the registered ClickHandler when the user clicks the notification.
	// Leave empty if no click handling is needed.
	ClickData string
}
