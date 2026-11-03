package activitymon

// BrowserURLRead distinguishes unavailable native work from a successful empty tab.
type BrowserURLRead struct {
	URL      string
	TimedOut bool
	InFlight bool
}
