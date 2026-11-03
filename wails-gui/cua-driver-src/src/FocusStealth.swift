import AppKit
import CoreGraphics
import Foundation

// Private Carbon CPS (Core Process Services) APIs. These are marked
// deprecated since 10.9 but still function in macOS 14 and what Codex
// uses to make a target window key WITHOUT making the process frontmost.
//
// SetFrontProcessWithOptions with kSetFrontProcessFrontWindowOnly:
//   "If this option is set, the process's frontmost window is made key.
//    If not set, the process is made the front process."
// The critical difference: the former activates ONLY the window for key
// input — Dock, menu bar, and frontmost-app state are untouched. No
// flicker. The target app then processes the subsequent CGEvent as if
// it were the key app, because its window IS the key window.
private let kSetFrontProcessFrontWindowOnly: UInt32 = 1 << 0

// Build a ProcessSerialNumber by pid. GetProcessForPID lives in the
// Process Manager (HIServices/ApplicationServices). It's also deprecated
// but is what Codex uses (their strings show "_GetProcessForPID" in the
// import table).
private struct PSN {
    var highLongOfPSN: UInt32
    var lowLongOfPSN: UInt32
}

@_silgen_name("GetProcessForPID")
private func _GetProcessForPID(_ pid: pid_t, _ psn: UnsafeMutablePointer<PSN>) -> OSStatus

@_silgen_name("SetFrontProcessWithOptions")
private func _SetFrontProcessWithOptions(_ psn: UnsafePointer<PSN>, _ options: UInt32) -> OSStatus

/// Sentinel written to `kCGEventSourceUserData` of every event we synthesize.
/// The focus-steal preventer's event tap matches this and lets our own
/// events through instead of dropping them alongside the system focus-change
/// notifications it's filtering out.
///
/// 8 ASCII chars → 64-bit int: "ALMANOFL" (ALMa NO FLicker).
let AlmaSyntheticEventSentinel: Int64 = 0x414C_4D41_4E4F_464C

/// Make `pid`'s front window the system key window WITHOUT making the
/// process itself frontmost. This is Codex's actual mechanism (reverse-
/// engineered from SkyComputerUseService's use of `SetFrontProcessWith
/// Options` + `kCPSNotifyKeyFocusTaken`):
///
/// • The Window Server's "front process" stays whoever it was — the
///   Dock highlight, menu bar, and cmd-tab order are all unchanged.
/// • The target process's frontmost window gains key status — any
///   subsequent CGEventPostToPid lands in a live, event-processing
///   window instead of being silently discarded.
///
/// No restoration needed on exit: because the front PROCESS never
/// changed, there's nothing to restore. Only the target window's key
/// status toggled, and the system naturally returns key focus to the
/// user's app as soon as they move or click their own cursor.
///
/// Returns true on success, false if the private API returned an error
/// (caller can fall back to plain CGEventPostToPid with the caveat that
/// some apps will drop the event).
@discardableResult
func makeWindowKeyWithoutActivating(pid: pid_t) -> Bool {
    var psn = PSN(highLongOfPSN: 0, lowLongOfPSN: 0)
    guard _GetProcessForPID(pid, &psn) == 0 else { return false }
    let err = _SetFrontProcessWithOptions(&psn, kSetFrontProcessFrontWindowOnly)
    return err == 0
}

/// Force `pid` to be the front process at the CPS layer WITHOUT the
/// `kSetFrontProcessFrontWindowOnly` flag — i.e. the real system-wide
/// frontmost. Used to reassert the user's original app after a click
/// caused the target to self-activate (a common Cocoa behaviour: many
/// apps call `[NSApp activateIgnoringOtherApps:]` from their click
/// handler). This is Codex's `reenableKFocusSteal` — wrapping the click
/// with "make target key, click, restore original front" all inside
/// the FocusStealPreventer tap window so the user-visible flicker is
/// suppressed.
@discardableResult
func restoreFrontProcess(pid: pid_t) -> Bool {
    var psn = PSN(highLongOfPSN: 0, lowLongOfPSN: 0)
    guard _GetProcessForPID(pid, &psn) == 0 else { return false }
    let err = _SetFrontProcessWithOptions(&psn, 0)
    return err == 0
}

/// Bring `pid` to the system frontmost and pause briefly so its
/// Chromium/Electron renderer has time to come out of background
/// throttle. Returns the user's previous frontmost pid so the caller
/// can restore it via `restoreForeground(pid:)` once the action is
/// done — or 0 when the target was already frontmost (nothing to
/// restore).
///
/// VISIBLE: yes. The target's traffic lights brighten, Dock highlights,
/// menu bar swaps to the target's. This is the deliberate trade-off
/// for actions that genuinely require the target's renderer to be
/// active: Slack channel switching, Discord server switching, Notion
/// page navigation, Linear ticket open — Electron apps actively
/// defer the heavyweight side-effects of click events delivered to
/// background windows. The bgclick path updates internal Redux state
/// but the renderer never reconciles, so AX trees stay stale and
/// follow-up actions (cmd+K modal, type into search) see the wrong
/// context.
///
/// Only call this from action handlers when the agent explicitly
/// passed `activate: true`. Default action paths stay on bgclick.
@discardableResult
func activateForeground(pid: pid_t) -> pid_t {
    let originalFront: pid_t = NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0
    if originalFront == pid { return 0 }
    // Whitelist this activation through the FocusStealPreventer tap
    // for 5 s — long enough to cover the action + settle window. The
    // tap normally drops every activation; without this allowance,
    // restoreFrontProcess(pid:) is suppressed in flight.
    FocusStealPreventer.allowActivation(pid: pid, for: 5.0)
    _ = restoreFrontProcess(pid: pid)
    // Chromium's background-tab throttle releases on the next render
    // tick after the window becomes key/frontmost. 150 ms covers the
    // 60Hz tick plus a margin; shorter waits caused Slack to drop
    // the first cmd+K after activation in practice.
    Thread.sleep(forTimeInterval: 0.15)
    return originalFront
}

/// Restore `pid` as the system frontmost (typically the user's
/// pre-activation app). Pair with `activateForeground` via defer.
/// No-op when pid is 0 (caller passed back from activateForeground's
/// "no activation happened" return) or when pid is already frontmost.
func restoreForeground(pid: pid_t) {
    guard pid > 0 else { return }
    let current = NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0
    if current == pid { return }
    FocusStealPreventer.allowActivation(pid: pid, for: 5.0)
    _ = restoreFrontProcess(pid: pid)
}

/// Watch `NSWorkspace.didActivateApplicationNotification` while active.
/// If ANY process other than `preservePid` becomes frontmost, snap back
/// to `preservePid` immediately — no delay, no polling.
///
/// Why this exists: many Cocoa / CEF apps respond to a mouse click by
/// calling `[NSApp activateIgnoringOtherApps:]` in their click handler.
/// We can't prevent that (it's the target's own code). But we can
/// observe the activation notification the moment it fires and undo
/// it in the same main-loop cycle, so the user sees no menu-bar swap
/// or dock-highlight jump.
final class PreserveFrontmostWatcher {
    private let preservePid: pid_t
    private var observer: NSObjectProtocol?

    init(preservePid: pid_t) {
        self.preservePid = preservePid
    }

    func begin() {
        let center = NSWorkspace.shared.notificationCenter
        observer = center.addObserver(
            forName: NSWorkspace.didActivateApplicationNotification,
            object: nil,
            queue: .main
        ) { [weak self] notif in
            guard let self = self else { return }
            guard let app = notif.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication else { return }
            if FocusStealPreventer.isActivationTemporarilyAllowed(app.processIdentifier) { return }
            if app.processIdentifier != self.preservePid {
                // Someone else (likely the click target self-activating)
                // just became frontmost. Force the original back right now.
                _ = restoreFrontProcess(pid: self.preservePid)
            }
        }
    }

    func end() {
        if let o = observer {
            NSWorkspace.shared.notificationCenter.removeObserver(o)
        }
        observer = nil
    }
}

// AXManualAccessibility + AXEnhancedUserInterface management has moved
// to AXBridge.setEnhancedUI / restoreAllEnhancedUI. That path:
//   • tracks each target pid's pre-daemon AX flag values in a single
//     enhancedOriginal map (no duplicate state)
//   • flips both flags on once per pid; subsequent ensures are no-ops
//   • is restored on daemon shutdown via Daemon.shutdown →
//     AXBridge.restoreAllEnhancedUI()
//
// The previous AXEnablementAssertion class lived here and was created
// per-action with a 5 s deferred deinit that toggled the flags back
// off. That design caused external AX consumers (host Wails app's
// NSWorkspace observer, which also queries Slack's AX) to race against
// our toggles and hang on Mach IPC long enough for the macOS main-
// thread spin watchdog to SIGSEGV the host. AXBridge's tracking
// already implements the correct "set once per pid, restore on
// shutdown" lifecycle; nothing here was load-bearing beyond that. The
// class is removed to prevent re-introducing the per-action toggle
// pattern.

/// `SystemFocusStealPreventer`-equivalent.
///
/// Mechanism (reverse-engineered from Codex's helper binary):
///
///   Codex's flicker-free activation isn't "activate then restore" — it's
///   DROPPING the `kCPSNotifyNewFront` / `kCPSNotifyKeyFocusTaken` CPS
///   notification BEFORE the Window Server commits the frontmost change.
///   With the event dropped in flight, AppKit is never told, Dock is
///   never told, menu bar is never swapped, Cmd-Tab order doesn't shift.
///
///   The events are delivered as CGEventType 14 (`NSSystemDefined`) with
///   subtype 8 (`kCPSEventNotify`) — NOT type 13 (`NSAppKitDefined`) as
///   the first iteration of this file assumed. Type 14 is the earlier
///   channel through which the Window Server notifies the session of
///   process-lifecycle events; AppKit / CPS observers downstream listen
///   here. Dropping the event at this tap is like it never happened.
///
///   The tap checks whether the event's target pid is in the
///   `disallowedThiefPids` set we maintain. Only those pids have their
///   activation notifications swallowed; every other app on the system
///   activates normally.
final class FocusStealPreventer {
    private var tap: CFMachPort?
    private var src: CFRunLoopSource?
    /// Shared set of pids for which activation notifications should be
    /// swallowed. Members added before launching / clicking a target,
    /// removed after the operation settles.
    private static var disallowedThiefPids = Set<pid_t>()
    /// When set, EVERY activation targeting a pid different from this
    /// allowed pid is swallowed. Used during cold-launches where we
    /// don't yet know the target's pid — we lock frontmost to the
    /// user's original for the launch window.
    private static var onlyAllowPid: pid_t?
    private static var temporarilyAllowedActivations: [pid_t: Date] = [:]
    private static let lock = NSLock()

    static func addDisallowed(_ pid: pid_t) {
        lock.lock(); defer { lock.unlock() }
        disallowedThiefPids.insert(pid)
    }

    static func removeDisallowed(_ pid: pid_t) {
        lock.lock(); defer { lock.unlock() }
        disallowedThiefPids.remove(pid)
    }

    /// Lock frontmost to `pid` — drop activations targeting anyone else.
    /// Pass nil to release the lock.
    static func setOnlyAllow(_ pid: pid_t?) {
        lock.lock(); defer { lock.unlock() }
        onlyAllowPid = pid
    }

    static func allowActivation(pid: pid_t, for seconds: TimeInterval) {
        lock.lock(); defer { lock.unlock() }
        temporarilyAllowedActivations[pid] = Date().addingTimeInterval(seconds)
    }

    static func isActivationTemporarilyAllowed(_ pid: pid_t) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return hasTemporaryAllowanceLocked(pid, now: Date())
    }

    private static func hasTemporaryAllowanceLocked(_ pid: pid_t, now: Date) -> Bool {
        guard let expiry = temporarilyAllowedActivations[pid] else { return false }
        if expiry > now { return true }
        temporarilyAllowedActivations.removeValue(forKey: pid)
        return false
    }

    static func shouldDrop(targetPid pid: pid_t) -> Bool {
        lock.lock(); defer { lock.unlock() }
        if hasTemporaryAllowanceLocked(pid, now: Date()) { return false }
        if disallowedThiefPids.contains(pid) { return true }
        if let only = onlyAllowPid, pid != only { return true }
        return false
    }

    /// Drop all suppression state. Called from the `turn_ended` cmd so
    /// that per-turn activation locks don't leak across agent turns —
    /// matches Codex's `releasedTheft` at their `CodexTurnEndedNotification`
    /// handler, which per memory is the ONLY safe scope for focus
    /// suppression (anything longer risks pinning the user's real
    /// frontmost for seconds after the agent hangs or crashes).
    ///
    /// Returns a small summary for the caller to log.
    @discardableResult
    static func releaseAll() -> (disallowedCount: Int, hadOnlyAllow: Bool) {
        lock.lock(); defer { lock.unlock() }
        let count = disallowedThiefPids.count
        let hadOnly = onlyAllowPid != nil
        disallowedThiefPids.removeAll()
        onlyAllowPid = nil
        temporarilyAllowedActivations.removeAll()
        return (count, hadOnly)
    }

    func begin() {
        // Type 14 = NSEventType.systemDefined. This is the CPS notification
        // channel. We also keep type 13 in the mask to swallow any trailing
        // NSAppKitDefined focus-change that slips past (belt + braces).
        let mask: CGEventMask = (1 << 14) | (1 << 13)

        // Hold a weak-ish back-reference so the tap callback can re-enable
        // itself after a tap-disabled-by-timeout event. Without this, the
        // first time the system decides our callback was too slow, the
        // tap is silently suspended and EVERY activation from then on
        // leaks through — flicker city. See comment below in the callback.
        let selfPtr = Unmanaged.passUnretained(self).toOpaque()

        let cb: CGEventTapCallBack = { _, type, event, info in
            // Tap-disable recovery: the kernel disables an event tap if
            // the callback takes longer than its timeout threshold (or
            // an explicit user action). The tap stops firing silently
            // until re-enabled. Reassert immediately on both disable
            // event types — this is what keeps the whole suppression
            // system robust under load (e.g. when the daemon is doing a
            // ScreenCaptureKit grab on the main queue during a click).
            if type == .tapDisabledByTimeout || type == .tapDisabledByUserInput {
                if let info = info {
                    let this = Unmanaged<FocusStealPreventer>.fromOpaque(info).takeUnretainedValue()
                    if let t = this.tap { CGEvent.tapEnable(tap: t, enable: true) }
                    FileHandle.standardError.write(Data(
                        "[alma-cu] CGEventTap disabled (\(type == .tapDisabledByTimeout ? "timeout" : "user")); re-enabled\n".utf8
                    ))
                }
                return Unmanaged.passUnretained(event)
            }
            // Let Alma's own synthesized events through untouched.
            let userData = event.getIntegerValueField(.eventSourceUserData)
            if userData == AlmaSyntheticEventSentinel {
                return Unmanaged.passUnretained(event)
            }
            // Convert to NSEvent to read subtype + data1. For NSSystemDefined
            // (type 14), subtype 8 is kCPSEventNotify. data1 carries the
            // affected pid packed as a PSN.
            guard let nse = NSEvent(cgEvent: event) else {
                return Unmanaged.passUnretained(event)
            }
            // Subtype is reachable only for system/app-kit-defined types.
            // On other types, `subtype` throws — so branch by .type first.
            switch nse.type {
            case .systemDefined:
                // subtype 8 == kCPSEventNotify
                if nse.subtype.rawValue == 8 {
                    // data1's low word is typically the target pid for
                    // new-front / key-focus CPS notifications. Drop if
                    // the pid is one we're silencing OR if onlyAllowPid
                    // is set and this isn't it.
                    let pid = pid_t(Int32(truncatingIfNeeded: nse.data1))
                    if FocusStealPreventer.shouldDrop(targetPid: pid) {
                        return nil
                    }
                }
            case .appKitDefined:
                // AppKit-defined activation events (trailing confirmation
                // of CPS notifications) — drop for the same pid set.
                // data1 carries a window number / pid depending on subtype.
                let pid = pid_t(Int32(truncatingIfNeeded: nse.data1))
                if FocusStealPreventer.shouldDrop(targetPid: pid) {
                    return nil
                }
            default:
                break
            }
            return Unmanaged.passUnretained(event)
        }

        guard
            let t = CGEvent.tapCreate(
                tap: .cgAnnotatedSessionEventTap,
                place: .headInsertEventTap,
                options: .defaultTap,
                eventsOfInterest: mask,
                callback: cb,
                userInfo: selfPtr
            )
        else {
            return
        }
        tap = t
        src = CFMachPortCreateRunLoopSource(nil, t, 0)
        if let s = src {
            CFRunLoopAddSource(CFRunLoopGetCurrent(), s, .commonModes)
        }
        CGEvent.tapEnable(tap: t, enable: true)
    }

    func end() {
        if let t = tap {
            CGEvent.tapEnable(tap: t, enable: false)
            CFMachPortInvalidate(t)
        }
        if let s = src {
            CFRunLoopRemoveSource(CFRunLoopGetCurrent(), s, .commonModes)
        }
        tap = nil
        src = nil
    }
}
