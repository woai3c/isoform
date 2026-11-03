//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"

	"github.com/energye/systray"
	"golang.org/x/sys/windows/registry"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// menuBarApp is set during startup() so systray callbacks can reach the App.
var (
	menuBarApp         *App
	trayStartOnce      sync.Once
	trayQuitCh         = make(chan struct{})
	trayClosedEyesItem *systray.MenuItem
)

// initMenuBar starts the Windows system tray icon with Open/Quit menu items.
// Runs in a goroutine so it does not block Wails' main loop.
//
// The systray goroutine MUST stay locked to a single OS thread for its whole
// lifetime. energye/systray creates a hidden Win32 window inside Register()
// and then drives GetMessage in nativeLoop(); Windows binds the window's
// message queue to the OS thread that created it, so GetMessage only sees
// clicks from that thread's queue. Without LockOSThread, Go's sysmon can
// hand the goroutine off to a different OS thread mid-syscall, after which
// the pump silently reads an empty queue and every left/right click on the
// tray icon is dropped. Symptom: icon visible, completely unresponsive,
// only fixable by killing the process. This is independent of the
// handler-blocking issue fixed in #1879 — that one made the pump stall,
// this one makes the pump read from the wrong queue.
func initMenuBar(app *App) {
	menuBarApp = app

	trayStartOnce.Do(func() {
		go func() {
			runtime.LockOSThread()
			// Intentionally no UnlockOSThread: when systray.Quit() returns
			// from Run at shutdown, letting the locked OS thread exit with
			// the goroutine is the desired outcome.
			systray.Run(onTrayReady, onTrayExit)
		}()
	})
}

// cleanupMenuBar removes the tray icon. Called from App.shutdown.
func cleanupMenuBar() {
	select {
	case <-trayQuitCh:
		// already closed
	default:
		close(trayQuitCh)
	}
	systray.Quit()
}

func onTrayReady() {
	if len(embeddedAppIconICO) > 0 {
		systray.SetIcon(embeddedAppIconICO)
	}
	systray.SetTitle(productName())
	systray.SetTooltip(productName())

	mOpen := systray.AddMenuItem("Open Isoform", "Show the Isoform window")

	trayClosedEyesItem = systray.AddMenuItemCheckbox(
		menuBarObservationTitle(false, menuBarLanguage(menuBarApp)),
		menuBarObservationTitle(false, menuBarLanguage(menuBarApp)),
		false,
	)
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit Isoform", "Quit Isoform")

	// All tray callbacks dispatch their real work to a goroutine. The
	// energye/systray library runs handlers synchronously on its Win32
	// message-pump thread; the moment a handler blocks (e.g. waiting for
	// Wails to round-trip a runtime.WindowShow through a busy WebView2
	// UI thread during heavy chat streaming), the entire pump stalls and
	// both left- and right-click events stop being delivered until the
	// blocked call returns. Returning from the handler immediately keeps
	// the pump responsive even when the underlying runtime call hangs.
	systray.SetOnClick(func(_ systray.IMenu) {
		log.Printf("[tray] icon clicked")
		go showYansuWindow("tray_click")
	})

	mOpen.Click(func() {
		log.Printf("[tray] Open Isoform menu item clicked")
		go showYansuWindow("tray_menu_open")
	})

	trayClosedEyesItem.Click(func() {
		if menuBarApp == nil {
			return
		}
		go menuBarApp.toggleMenuBarObservation()
	})

	mQuit.Click(func() {
		log.Printf("[tray] quit requested from tray menu")
		go func() {
			if menuBarApp != nil && menuBarApp.ctx != nil {
				menuBarApp.requestQuit()
				return
			}
			systray.Quit()
		}()
	})
}

func onTrayExit() {
	log.Printf("[tray] tray exited")
}

// showYansuWindow brings the main window to the foreground. Called from
// tray clicks. Logs every step so a future "I clicked tray and nothing
// happened" report can be diagnosed from the log alone.
//
// Wails v2 on Windows has a known issue where runtime.WindowShow after
// HideWindowOnClose does not always foreground the window — it may show
// but stay behind other windows, or remain in the not-foregrounded state.
// The workaround is to briefly toggle AlwaysOnTop to force a Z-order
// reshuffle; the user perceives the window jumping to the front. The
// flag is cleared immediately so normal stacking resumes.
func showYansuWindow(source string) {
	if menuBarApp == nil || menuBarApp.ctx == nil {
		log.Printf("[tray] showYansuWindow(%s) skipped: app or ctx not ready", source)
		return
	}
	log.Printf("[tray] showYansuWindow(%s) -> WindowShow", source)
	wailsruntime.WindowShow(menuBarApp.ctx)
	wailsruntime.WindowUnminimise(menuBarApp.ctx)
	// Force Z-order to put us on top. Without this, Wails v2 + WebView2 on
	// Windows sometimes leaves the restored window behind the previously
	// focused app.
	wailsruntime.WindowSetAlwaysOnTop(menuBarApp.ctx, true)
	wailsruntime.WindowSetAlwaysOnTop(menuBarApp.ctx, false)
	log.Printf("[tray] showYansuWindow(%s) done", source)
}

// The following functions are required by callers in the shared package but
// have no Windows-specific behavior beyond the tray handling above.

func updateMenuBarStatus(_ int)            {}
func updateMenuBarLoginItemChecked(_ bool) {}

// updateMenuBarDoingNow has no tray line on Windows yet; the sidebar shows
// the same words.
func updateMenuBarDoingNow(_ string) {}

func updateMenuBarClosedEyesMode(enabled bool) {
	if trayClosedEyesItem == nil {
		return
	}
	title := menuBarObservationTitle(enabled, menuBarLanguage(menuBarApp))
	trayClosedEyesItem.SetTitle(title)
	trayClosedEyesItem.SetTooltip(title)
	if enabled {
		trayClosedEyesItem.Check()
		return
	}
	trayClosedEyesItem.Uncheck()
}
func switchToRegularApp()     {}
func switchToAccessoryApp()   {}
func shouldStartHidden() bool { return false }

// ── Windows auto-start via Registry ──────────────────────────────────

const registryRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const registryValueName = "Isoform"

func registerAsLoginItem() bool {
	exePath, err := os.Executable()
	if err != nil {
		log.Printf("[loginitem] failed to get executable path: %v", err)
		return false
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, registryRunKey, registry.SET_VALUE)
	if err != nil {
		log.Printf("[loginitem] failed to open registry key: %v", err)
		return false
	}
	defer key.Close()

	if err := key.SetStringValue(registryValueName, fmt.Sprintf(`"%s"`, exePath)); err != nil {
		log.Printf("[loginitem] failed to set registry value: %v", err)
		return false
	}
	log.Printf("[loginitem] registered at login: %s", exePath)
	return true
}

func unregisterLoginItem() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.SET_VALUE)
	if err != nil {
		log.Printf("[loginitem] failed to open registry key: %v", err)
		return false
	}
	defer key.Close()

	if err := key.DeleteValue(registryValueName); err != nil {
		log.Printf("[loginitem] failed to delete registry value: %v", err)
		return false
	}
	log.Printf("[loginitem] unregistered from login")
	return true
}

func readStartAtLogin() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.QUERY_VALUE)
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer key.Close()
	_, _, err = key.GetStringValue(registryValueName)
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func isLoginItemRegistered() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, registryRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()

	_, _, err = key.GetStringValue(registryValueName)
	return err == nil
}

func ensureStartAtLoginEnabled() {
	if loadConfig().StartAtLoginDisabled {
		updateMenuBarLoginItemChecked(false)
		return
	}
	if isLoginItemRegistered() {
		updateMenuBarLoginItemChecked(true)
		return
	}
	if registerAsLoginItem() {
		log.Printf("[loginitem] enabled by default")
		updateMenuBarLoginItemChecked(true)
		return
	}
	log.Printf("[loginitem] default enable requested but registration did not report success")
	updateMenuBarLoginItemChecked(isLoginItemRegistered())
}

// performRealQuit is kept for API parity with darwin; on Windows the tray
// "Quit" handler records an explicit quit, so this is only invoked if
// some other code path (e.g. future CLI flag) needs a hard shutdown.
func performRealQuit() {
	if menuBarApp != nil && menuBarApp.ctx != nil {
		menuBarApp.requestQuit()
	}
}

func initMenuBarEarly(_ *App)       {}
func menuBarServicesReady(app *App) { initMenuBar(app) }
