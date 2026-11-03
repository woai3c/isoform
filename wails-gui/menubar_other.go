//go:build !darwin && !windows

package main

func initMenuBar(_ *App)                   {}
func cleanupMenuBar()                      {}
func updateMenuBarStatus(_ int)            {}
func updateMenuBarLoginItemChecked(_ bool) {}
func updateMenuBarClosedEyesMode(_ bool)   {}
func updateMenuBarDoingNow(_ string)       {}
func switchToRegularApp()                  {}
func switchToAccessoryApp()                {}
func shouldStartHidden() bool              { return false }
func performRealQuit()                     {}
func ensureStartAtLoginEnabled()           {}
func registerAsLoginItem() bool            { return false }
func unregisterLoginItem() bool            { return false }
func readStartAtLogin() (bool, error)      { return false, nil }
func isLoginItemRegistered() bool          { return false }

func initMenuBarEarly(_ *App)       {}
func menuBarServicesReady(app *App) { initMenuBar(app) }
