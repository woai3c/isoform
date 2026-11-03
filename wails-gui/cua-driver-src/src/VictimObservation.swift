import AppKit
import CoreGraphics
import Darwin
import Foundation

/// Observation + ViewBridge coverage layer that sits on top of
/// `FocusStealPreventer`. Three responsibilities:
///
/// 1. **Per-pid observation tap** — a listen-only CGEventTap scoped to
///    the victim process via `CGEventTapCreateForPid`. Observes events
///    on their way into the target. Used for forensics: if any
///    activation / app-kit event reaches the victim during a protected
///    window, we log it with enough context to diagnose the leak.
///    Mirrors Codex's `SyntheticAppFocusEnforcer.clickEventTap`.
///
/// 2. **ViewBridge helper discovery** — targets that use out-of-process
///    UI (`NSSavePanel`, `NSOpenPanel`, `NSSharingService`, `CoreUI`
///    graphics workers, Safari XPC content processes, Finder's
///    `com.apple.finder.shares.helper`, etc.) spawn helper pids that
///    can self-activate independently. On entry we enumerate NS running
///    apps whose bundle-id ends in `.helper`, `.Helper`, `.xpc`, or
///    matches common per-vendor patterns rooted at the target's bundle-
///    id, and add those pids to the disallow set. Covers Codex's
///    `lastViewBridgeFocusStealWasSuppressed` surface.
///
/// 3. **Stats** — a small counter of how many observed-in-flight
///    activation events were seen on the victim. Queryable via the
///    `stealth_stats` daemon command so callers can prove post-hoc that
///    a protected click caused zero observable intrusions.
///
/// Lifetime: one `VictimObserver` per protected click, `begin` before
/// posting, `end` after the 5s hold window.
final class VictimObserver {
    let pid: pid_t
    private var tap: CFMachPort?
    private var src: CFRunLoopSource?
    private var addedHelperPids: [pid_t] = []

    /// Counters are atomic-ish via the shared `statsLock`. Read via
    /// `stats()` and reset via `resetStats()`. Kept static so the
    /// `stealth_stats` daemon verb can consult them across the global
    /// observer population.
    private static var statsLock = NSLock()
    private(set) static var totalObservedActivations: Int = 0
    private(set) static var totalTapDisablesRecovered: Int = 0
    private(set) static var totalViewBridgeHelpersCovered: Int = 0

    init(pid: pid_t) {
        self.pid = pid
    }

    func begin() {
        coverViewBridgeHelpers()
        installPidTap()
    }

    func end() {
        // Drop helper pids from the disallow set — but only those WE
        // added. If another caller independently protected the same pid,
        // their own disallow entry stays.
        for p in addedHelperPids {
            FocusStealPreventer.removeDisallowed(p)
        }
        addedHelperPids.removeAll()

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

    // MARK: - ViewBridge helper discovery

    private func coverViewBridgeHelpers() {
        let targetBundle = NSRunningApplication(processIdentifier: pid)?.bundleIdentifier ?? ""
        if targetBundle.isEmpty { return }
        for app in NSWorkspace.shared.runningApplications {
            guard let bid = app.bundleIdentifier else { continue }
            if app.processIdentifier == pid { continue }
            if Self.isLikelyHelper(bid: bid, of: targetBundle) {
                FocusStealPreventer.addDisallowed(app.processIdentifier)
                addedHelperPids.append(app.processIdentifier)
                Self.statsLock.lock()
                Self.totalViewBridgeHelpersCovered += 1
                Self.statsLock.unlock()
            }
        }
    }

    /// Heuristic: bundle identifier starts with the target's bundle id
    /// AND ends in `.helper`, `.Helper`, `.xpc`, or contains `Helper`
    /// after a dot. Catches the common patterns used by Safari
    /// (`com.apple.Safari.SafeBrowsing.Service`), Chrome
    /// (`com.google.Chrome.helper`), Electron apps
    /// (`<bundle>.Helper (Renderer)`), etc.
    fileprivate static func isLikelyHelper(bid: String, of target: String) -> Bool {
        guard target.count > 0, bid.hasPrefix(target) else { return false }
        let suffix = bid.dropFirst(target.count)
        if suffix.isEmpty { return false }
        if suffix.hasPrefix(".") {
            let lower = suffix.lowercased()
            return lower.contains("helper") || lower.contains(".xpc") || lower.contains("service")
        }
        return false
    }

    // MARK: - Per-pid tap

    /// `CGEventTapCreateForPid` is not in the Swift overlay — we load it
    /// via dlsym the same way we do `CGEventPostToPid` in EventDispatch,
    /// which conveniently also keeps it out of the static import table.
    private func installPidTap() {
        guard let fn = Self.tapForPidFn else { return }
        // Mouse + keyboard events — the ones a target would actually
        // receive and potentially self-activate from — plus the
        // systemDefined/appKitDefined activation channels we already
        // filter at the session level.
        let mask: CGEventMask = (1 << 14) | (1 << 13)
                              | (1 << CGEventType.leftMouseDown.rawValue)
                              | (1 << CGEventType.rightMouseDown.rawValue)
                              | (1 << CGEventType.keyDown.rawValue)

        let cb: CGEventTapCallBack = { _, type, event, info in
            if type == .tapDisabledByTimeout || type == .tapDisabledByUserInput {
                if let info = info {
                    let this = Unmanaged<VictimObserver>.fromOpaque(info).takeUnretainedValue()
                    if let t = this.tap { CGEvent.tapEnable(tap: t, enable: true) }
                    VictimObserver.statsLock.lock()
                    VictimObserver.totalTapDisablesRecovered += 1
                    VictimObserver.statsLock.unlock()
                }
                return Unmanaged.passUnretained(event)
            }
            // Filter out our own synthesised events so we don't
            // double-count them as "observed activations".
            let userData = event.getIntegerValueField(.eventSourceUserData)
            if userData == AlmaSyntheticEventSentinel {
                return Unmanaged.passUnretained(event)
            }
            // CGEventType has no .systemDefined / .appKitDefined enum
            // cases (those are NSEventType). Compare against the raw
            // values the Window Server uses: 14 = NSSystemDefined,
            // 13 = NSAppKitDefined — the two activation channels.
            let raw = type.rawValue
            if raw == 14 || raw == 13 {
                VictimObserver.statsLock.lock()
                VictimObserver.totalObservedActivations += 1
                VictimObserver.statsLock.unlock()
            }
            return Unmanaged.passUnretained(event)
        }

        let selfPtr = Unmanaged.passUnretained(self).toOpaque()
        // ListenOnly — we're observing, not intercepting. That makes
        // CGEventTapCreateForPid content with no auxiliary entitlement
        // beyond plain Accessibility.
        let place: UInt32 = 0 // kCGHeadInsertEventTap
        let options: UInt32 = 1 // kCGEventTapOptionListenOnly
        let raw = fn(pid, place, options, mask, cb, selfPtr)
        guard let t = raw else { return }
        self.tap = t
        src = CFMachPortCreateRunLoopSource(nil, t, 0)
        if let s = src {
            CFRunLoopAddSource(CFRunLoopGetCurrent(), s, .commonModes)
        }
        CGEvent.tapEnable(tap: t, enable: true)
    }

    typealias CGEventTapCreateForPidFn = @convention(c) (
        pid_t, UInt32, UInt32, CGEventMask, CGEventTapCallBack, UnsafeMutableRawPointer?
    ) -> CFMachPort?

    /// Resolved lazily via dlopen(CoreGraphics) + dlsym, same pattern as
    /// `SynthPost.fn` in EventDispatch.swift. Keeps the symbol out of
    /// the import table; no functional difference from a direct call.
    static let tapForPidFn: CGEventTapCreateForPidFn? = {
        let path = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
        guard let handle = dlopen(path, RTLD_LAZY | RTLD_GLOBAL) else { return nil }
        guard let sym = dlsym(handle, "CGEventTapCreateForPid") else { return nil }
        return unsafeBitCast(sym, to: CGEventTapCreateForPidFn.self)
    }()

    // MARK: - Stats

    static func stats() -> [String: Any] {
        statsLock.lock(); defer { statsLock.unlock() }
        return [
            "observed_activations": totalObservedActivations,
            "tap_disables_recovered": totalTapDisablesRecovered,
            "viewbridge_helpers_covered": totalViewBridgeHelpersCovered,
        ]
    }

    static func resetStats() {
        statsLock.lock(); defer { statsLock.unlock() }
        totalObservedActivations = 0
        totalTapDisablesRecovered = 0
        totalViewBridgeHelpersCovered = 0
    }
}
