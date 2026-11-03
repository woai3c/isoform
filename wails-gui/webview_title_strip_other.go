//go:build !darwin

package main

func patchWebViewTitleStripFirstMouse() {}

func setTitleStripDragRegions([]TitleStripRect) {}
