package main

import (
	"errors"
	"sync"
	"sync/atomic"

	"wails-gui/internal/platform/nativeprompt"
)

// macOS shows one dialog per permission request, and several at once are
// confusing. Two rules keep them calm: the app asks only while the user is
// asking (a Grant button, the onboarding checklist, the listening switch),
// never on its own at launch or when work resumes; and requests from the UI
// run one at a time, so the next prompt waits until the previous one is
// answered.
//
// Code that starts on its own (background listening at launch, resume after
// unlock) checks permissionPromptsAllowed before touching an API that would
// prompt, and reports errPermissionNotRequested instead; the status then shows
// as "not granted" with a Grant button in Settings and onboarding.
var (
	permissionPromptMu    sync.Mutex
	permissionPromptDepth atomic.Int32
)

// errPermissionNotRequested means a permission is undetermined and the
// current code path is not allowed to prompt for it.
var errPermissionNotRequested = errors.New("permission not requested yet; grant it from Settings or onboarding")

// allowPermissionPrompts marks the calling user action as allowed to show
// macOS permission dialogs until the returned release function runs.
func allowPermissionPrompts() (release func()) {
	permissionPromptDepth.Add(1)
	var once sync.Once
	return func() { once.Do(func() { permissionPromptDepth.Add(-1) }) }
}

// permissionPromptsAllowed reports whether a user-driven permission request is
// in flight, so an OS prompt may be shown. Test binaries never show OS prompts.
func permissionPromptsAllowed() bool {
	return nativeprompt.Allowed() && permissionPromptDepth.Load() > 0
}

// runPermissionRequest serializes user-driven permission requests and allows
// prompts for the duration of fn.
func runPermissionRequest(fn func()) {
	permissionPromptMu.Lock()
	defer permissionPromptMu.Unlock()
	release := allowPermissionPrompts()
	defer release()
	fn()
}
