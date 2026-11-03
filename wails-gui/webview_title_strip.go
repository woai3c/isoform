package main

// TitleStripRect is one draggable area of the window's title strip, in page
// points from the window's top-left corner.
type TitleStripRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// SetTitleStripDragRegions takes the title strip's draggable areas from the
// page, so on macOS the first click on an inactive window drags it there
// and nowhere else (webview_title_strip_darwin.go).
func (a *App) SetTitleStripDragRegions(rects []TitleStripRect) {
	setTitleStripDragRegions(rects)
}
