//go:build darwin && !bindings

package main

/*
 */
import "C"

import "log"

//export goClosedEyesOverlayClicked
func goClosedEyesOverlayClicked() {
	app := closedEyesOverlayApp.Load()
	if app == nil {
		return
	}

	go func() {
		if _, err := app.setClosedEyesModeEnabled(false); err != nil {
			log.Printf("[closed-eyes] overlay click failed: %v", err)
			return
		}
		log.Printf("[closed-eyes] disabled from overlay eye click")
	}()
}
