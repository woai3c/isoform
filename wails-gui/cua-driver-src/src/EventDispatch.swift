import AppKit
import Carbon
import CoreGraphics
import Foundation

/// Physical-HID-style event dispatch — the "simulate physical clicks" side of
/// the Codex hybrid. Used when:
///   • the target element has no AX Press/Pick/Confirm action,
///   • or the target app exposes no AX tree at all (Qt / custom-drawn / Electron
///     with AX disabled), so the model gave us pixel coordinates,
///   • or the caller explicitly wants a real mouse/keyboard event (drag,
///     double-click, Cmd-S, etc.).
///
/// Focus-steal suppression: every dispatched event is sandwiched in
/// `withFocusPreserved`. If the frontmost app changes as a side effect
/// (which does happen for left-clicks on a background window's content area),
/// we reactivate the original app.  This mirrors
/// `AccessibilitySupport.SystemFocusStealPreventer` in Codex's helper.
enum EventDispatch {

    // MARK: - Mouse

    enum MouseButton: String {
        case left, right, middle
        var cgButton: CGMouseButton {
            switch self {
            case .left: return .left
            case .right: return .right
            case .middle: return .center
            }
        }
        /// Value written into CGEvent field 3 (`kCGMouseEventButtonNumber`)
        /// per bgclick-rev invariant #3: 0 left, 1 right, 2 other.
        var fieldValue: Int {
            switch self {
            case .left: return 0
            case .right: return 1
            case .middle: return 2
            }
        }
        // Down/up event pairs differ between left/right/middle.
        var downType: CGEventType {
            switch self {
            case .left: return .leftMouseDown
            case .right: return .rightMouseDown
            case .middle: return .otherMouseDown
            }
        }
        var upType: CGEventType {
            switch self {
            case .left: return .leftMouseUp
            case .right: return .rightMouseUp
            case .middle: return .otherMouseUp
            }
        }
        var draggedType: CGEventType {
            switch self {
            case .left: return .leftMouseDragged
            case .right: return .rightMouseDragged
            case .middle: return .otherMouseDragged
            }
        }
        // NSEvent variants, used by the bgclick-rev synthesis path (click/drag).
        var nsDownType: NSEvent.EventType {
            switch self {
            case .left: return .leftMouseDown
            case .right: return .rightMouseDown
            case .middle: return .otherMouseDown
            }
        }
        var nsUpType: NSEvent.EventType {
            switch self {
            case .left: return .leftMouseUp
            case .right: return .rightMouseUp
            case .middle: return .otherMouseUp
            }
        }
        var nsDraggedType: NSEvent.EventType {
            switch self {
            case .left: return .leftMouseDragged
            case .right: return .rightMouseDragged
            case .middle: return .otherMouseDragged
            }
        }
    }

    /// Click at a screen point, routed to a specific pid via `CGEventPostToPid`.
    ///
    /// Implementation follows Lakr233's bgclick-rev skill (9 invariants). The
    /// CGEvent is constructed via `NSEvent.mouseEvent(...) → .cgEvent`, then
    /// four fields are written explicitly (3 button, 7 subtype=3 magic,
    /// 91/92 CGWindowID) and the location pipeline runs screen→windowLocal
    /// via the private `CGEventSetWindowLocation`. When the target app is
    /// backgrounded, the WindowServer filter is bypassed by OR-ing
    /// `kCGEventFlagMaskCommand` (0x00100000) into the flags — this is the
    /// "特殊比特位". NSEvent.cgEvent auto-fills 12 other fields (0, 1, 2, 41,
    /// 43, 44, 50, 51, 55, 59, 102, 108); we never touch those.
    static func click(
        at point: CGPoint,
        button: MouseButton = .left,
        clickCount: Int = 1,
        pid: pid_t,
        modifiers: CGEventFlags = []
    ) throws {
        try withFocusPreserved(activating: pid, style: .bgclick) {
            let cgWindowId = resolveClickWindowID(forPid: pid, screenPoint: point)
            let winBounds = cgWindowId.flatMap { windowBounds(cgWindowId: $0) }
            let targetIsActive = NSRunningApplication(processIdentifier: pid)?.isActive ?? false

            for i in 1...max(1, clickCount) {
                let down = try makeClickEvent(
                    type: button.nsDownType, button: button, clickIndex: i,
                    screenPoint: point, cgWindowId: cgWindowId, winBounds: winBounds,
                    modifiers: modifiers, targetIsActive: targetIsActive, isDown: true
                )
                let up = try makeClickEvent(
                    type: button.nsUpType, button: button, clickIndex: i,
                    screenPoint: point, cgWindowId: cgWindowId, winBounds: winBounds,
                    modifiers: modifiers, targetIsActive: targetIsActive, isDown: false
                )
                post(down, toPid: pid)
                post(up, toPid: pid)
                if i < clickCount {
                    // Typical OS double-click threshold is ~500ms; 40ms between
                    // presses is well under that and feels snappy.
                    usleep(40_000)
                }
            }
        }
    }

    /// Build one mouse CGEvent per bgclick-rev invariants #2/#3/#4/#5/#7.
    private static func makeClickEvent(
        type: NSEvent.EventType,
        button: MouseButton,
        clickIndex: Int,
        screenPoint: CGPoint,
        cgWindowId: CGWindowID?,
        winBounds: CGRect?,
        modifiers: CGEventFlags,
        targetIsActive: Bool,
        isDown: Bool
    ) throws -> CGEvent {
        // NSEvent.mouseEvent location is overwritten below by the CGEvent
        // location pipeline, so `.zero` is fine here. windowNumber=cgWindowId
        // feeds field 51 (the private NSEvent.windowNumber mirror), which
        // together with fields 91/92 gives WindowServer three routing hints.
        guard let ns = NSEvent.mouseEvent(
            with: type,
            location: .zero,
            modifierFlags: [],
            timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: Int(cgWindowId ?? 0),
            context: nil,
            eventNumber: Int(nextSyntheticEventNumber()),
            clickCount: clickIndex,
            pressure: isDown ? 1.0 : 0.0
        ), let ev = ns.cgEvent else {
            throw CUError.internalError("NSEvent→CGEvent failed (click)")
        }

        // Invariant #3: four explicit CGEventSetIntegerValueField writes.
        ev.setIntegerValueField(CGEventField(rawValue: 3)!, value: Int64(button.fieldValue))
        ev.setIntegerValueField(CGEventField(rawValue: 7)!, value: 3) // magic non-standard subtype
        if let wid = cgWindowId {
            ev.setIntegerValueField(CGEventField(rawValue: 91)!, value: Int64(wid))
            ev.setIntegerValueField(CGEventField(rawValue: 92)!, value: Int64(wid))
        }

        // Invariant #4: location pipeline. Public setter takes screen
        // coords; read back; translate to window-local; private setter
        // stores the window-local point. CGEventSetWindowLocation is
        // resolved via dlsym — the symbol exists on current macOS but
        // isn't in the public SDK headers.
        ev.location = screenPoint
        if let bounds = winBounds, cgWindowId != nil {
            let readBack = ev.location
            let winLocal = CGPoint(
                x: readBack.x - bounds.origin.x,
                y: readBack.y - bounds.origin.y
            )
            SetWindowLocation.fn(ev, winLocal)
        }

        // Invariant #5: the "特殊比特位". When target isn't frontmost,
        // the ⌘ modifier bit acts as a WindowServer filter bypass, letting
        // the event reach the target window without activation. OR-ed
        // with caller's modifiers — not a straight assignment — so a
        // caller-requested shift-click still carries shift.
        var flags: CGEventFlags = modifiers
        if !targetIsActive {
            flags.insert(.maskCommand)
        }
        if !flags.isEmpty {
            ev.flags = flags
        }

        return ev
    }

    /// Right-click (a.k.a. secondary click). Distinct from "hold control and
    /// left-click" — this dispatches a real `rightMouseDown/Up` pair, which is
    /// what context-menu-sensitive apps listen for.
    static func secondaryClick(at point: CGPoint, pid: pid_t) throws {
        try click(at: point, button: .right, clickCount: 1, pid: pid)
    }

    /// Scroll at a point. Vertical only in v1 (Codex exposes a richer surface,
    /// but up/down covers 99% of real use.) `pages` is integer multiples of a
    /// typical wheel "click"; we translate one page to ~-120/+120 like stock
    /// wheel drivers.
    static func scroll(
        at point: CGPoint,
        direction: String,
        pages: Int = 1,
        pid: pid_t
    ) throws {
        try withFocusPreserved(activating: pid) {
            let (dy, dx) = scrollDeltas(direction: direction, pages: max(1, pages))
            guard let src = CGEventSource(stateID: .combinedSessionState) else {
                throw CUError.internalError("CGEventSource creation failed")
            }
            // Move the cursor-equivalent focus point first so the scroll lands
            // on the right view. We don't warp the real cursor — we just set
            // the event's location field.
            guard let ev = CGEvent(
                scrollWheelEvent2Source: src,
                units: .line,
                wheelCount: 2,
                wheel1: Int32(dy),
                wheel2: Int32(dx),
                wheel3: 0
            ) else {
                throw CUError.internalError("CGEvent creation failed (scroll)")
            }
            ev.location = point
            post(ev, toPid: pid)
        }
    }

    private static func scrollDeltas(direction: String, pages: Int) -> (Int, Int) {
        // Positive wheel1 = scroll up (content moves down). Line units — one
        // "line" is roughly 3 pixels, so `pages × 10` approximates a page.
        let step = 10 * pages
        switch direction.lowercased() {
        case "up":    return (step, 0)
        case "down":  return (-step, 0)
        case "left":  return (0, step)
        case "right": return (0, -step)
        default:      return (-step, 0)
        }
    }

    /// Drag from `start` to `end` using the bgclick-rev synthesis path
    /// (same NSEvent→cgEvent + 4 explicit fields as `click`). Intermediate
    /// dragged events are posted at a constant CGWindowID so the WindowServer
    /// keeps routing them to the starting window even when the cursor passes
    /// over others.
    static func drag(
        from start: CGPoint,
        to end: CGPoint,
        button: MouseButton = .left,
        pid: pid_t,
        steps: Int = 12
    ) throws {
        try withFocusPreserved(activating: pid, style: .bgclick) {
            let cgWindowId = resolveClickWindowID(forPid: pid, screenPoint: start)
            let winBounds = cgWindowId.flatMap { windowBounds(cgWindowId: $0) }
            let targetIsActive = NSRunningApplication(processIdentifier: pid)?.isActive ?? false

            let down = try makeClickEvent(
                type: button.nsDownType, button: button, clickIndex: 1,
                screenPoint: start, cgWindowId: cgWindowId, winBounds: winBounds,
                modifiers: [], targetIsActive: targetIsActive, isDown: true
            )
            post(down, toPid: pid)

            let n = max(2, steps)
            for i in 1..<n {
                let t = Double(i) / Double(n)
                let p = CGPoint(
                    x: start.x + (end.x - start.x) * CGFloat(t),
                    y: start.y + (end.y - start.y) * CGFloat(t)
                )
                let mv: CGEvent
                do {
                    mv = try makeClickEvent(
                        type: button.nsDraggedType, button: button, clickIndex: 1,
                        screenPoint: p, cgWindowId: cgWindowId, winBounds: winBounds,
                        modifiers: [], targetIsActive: targetIsActive, isDown: true
                    )
                } catch {
                    continue
                }
                post(mv, toPid: pid)
                usleep(8_000)
            }

            let up = try makeClickEvent(
                type: button.nsUpType, button: button, clickIndex: 1,
                screenPoint: end, cgWindowId: cgWindowId, winBounds: winBounds,
                modifiers: [], targetIsActive: targetIsActive, isDown: false
            )
            post(up, toPid: pid)
        }
    }

    // MARK: - Keyboard

    /// Dispatch a key combo expressed in xdotool-style syntax.
    /// Examples: "cmd+s", "cmd+shift+t", "Return", "Escape", "ctrl+Left".
    /// Modifiers (case-insensitive): cmd|command|meta, ctrl|control,
    /// shift, opt|option|alt.
    static func pressKey(_ combo: String, pid: pid_t) throws {
        let parsed = try KeyParser.parse(combo)
        try withFocusPreserved(activating: pid) {
            guard let src = CGEventSource(stateID: .combinedSessionState) else {
                throw CUError.internalError("CGEventSource creation failed")
            }

            // CEF / Electron / Chromium apps (Slack, Discord, VS Code,
            // Notion, Linear, …) route window-scoped keyboard shortcuts
            // through Chromium's renderer keyboard dispatcher, which
            // drops events whose target window is not the system key
            // window — even when delivered via CGEventPostToPid.
            // AXBridge.setEnhancedUI (installed by withFocusPreserved's
            // .axAction style) reifies the AX tree but does NOT make
            // the window key; Chromium's shortcut path checks key
            // status separately. typeKeyPresses already pairs CGEvent
            // posts with makeWindowKeyWithoutActivating for the same
            // reason; pressKey was missing this step, causing cmd+K
            // and similar Slack/Discord shortcuts to silently no-op
            // for background-driven sessions. The 1ms RunLoop spin lets
            // the WindowServer process the key-window flip before the
            // first key event fires.
            FocusStealPreventer.allowActivation(pid: pid, for: 2.0)
            _ = makeWindowKeyWithoutActivating(pid: pid)
            RunLoop.main.run(until: Date().addingTimeInterval(0.001))

            guard
                let down = CGEvent(keyboardEventSource: src, virtualKey: parsed.keyCode, keyDown: true),
                let up = CGEvent(keyboardEventSource: src, virtualKey: parsed.keyCode, keyDown: false)
            else {
                throw CUError.internalError("CGEvent creation failed (key)")
            }
            if !parsed.flags.isEmpty {
                down.flags = parsed.flags
                up.flags = parsed.flags
            }
            post(down, toPid: pid)
            post(up, toPid: pid)
        }
    }

    /// Make the target window the system key window without foreground
    /// activation, then let WindowServer settle. Chromium/CEF renderers
    /// (Electron apps: Feishu, Slack, Discord, VS Code, …) drop synthetic
    /// keyboard events whose target window is not key — even when delivered
    /// via CGEventPostToPid. The window-level keyboard helpers (pressKey,
    /// typeKeyPresses, typeUnicodeText) already run this sequence before
    /// posting; their *ProcessActivated clones dropped it, which made
    /// type_text / press_key silently no-op into Electron text inputs in the
    /// background-activated path while click — delivered through the full
    /// BackgroundActivation mouse pipeline — kept working.
    private static func establishKeyWindowForBackgroundKeyboard(pid: pid_t) {
        FocusStealPreventer.allowActivation(pid: pid, for: 2.0)
        _ = makeWindowKeyWithoutActivating(pid: pid)
        RunLoop.main.run(until: Date().addingTimeInterval(0.001))
    }

    static func pressKeyProcessActivated(_ combo: String, pid: pid_t) throws {
        let parsed = try KeyParser.parse(combo)
        guard let src = CGEventSource(stateID: .combinedSessionState) else {
            throw CUError.internalError("CGEventSource creation failed")
        }
        establishKeyWindowForBackgroundKeyboard(pid: pid)
        guard
            let down = CGEvent(keyboardEventSource: src, virtualKey: parsed.keyCode, keyDown: true),
            let up = CGEvent(keyboardEventSource: src, virtualKey: parsed.keyCode, keyDown: false)
        else {
            throw CUError.internalError("CGEvent creation failed (key)")
        }
        if !parsed.flags.isEmpty {
            down.flags = parsed.flags
            up.flags = parsed.flags
        }
        post(down, toPid: pid)
        post(up, toPid: pid)
    }

    /// Type literal text using Codex-style physical key events where possible:
    /// translate each character through the current keyboard layout, briefly
    /// make the target window key, then post keyCode down/up pairs to the pid.
    /// If the layout cannot produce a character, fall back to Unicode payload
    /// events so existing non-ASCII desktop-app input keeps working.
    static func typeText(_ text: String, pid: pid_t) throws {
        if let presses = try? KeyboardLayoutTranslator.keyPresses(for: text) {
            try typeKeyPresses(presses, pid: pid)
            return
        }
        try typeUnicodeText(text, pid: pid)
    }

    static func typePhysicalText(_ text: String, pid: pid_t) throws {
        let presses = try KeyboardLayoutTranslator.keyPresses(for: text)
        try typeKeyPresses(presses, pid: pid)
    }

    static func typeTextProcessActivated(_ text: String, pid: pid_t) throws {
        if let presses = try? KeyboardLayoutTranslator.keyPresses(for: text) {
            try typeKeyPressesProcessActivated(presses, pid: pid)
            return
        }
        try typeUnicodeTextProcessActivated(text, pid: pid)
    }

    static func typePhysicalTextProcessActivated(_ text: String, pid: pid_t) throws {
        let presses = try KeyboardLayoutTranslator.keyPresses(for: text)
        try typeKeyPressesProcessActivated(presses, pid: pid)
    }

    static func typeUnicodeText(_ text: String, pid: pid_t) throws {
        try withFocusPreserved(activating: pid) {
            guard let src = CGEventSource(stateID: .combinedSessionState) else {
                throw CUError.internalError("CGEventSource creation failed")
            }

            // WebKit-backed text fields (Apple Notes body, Slack composer,
            // VS Code, Linear, Notion, …) require the target window to be
            // the system key window before they will accept synthetic
            // CGEvents — including ones carrying a kCGKeyboardEventUnicodeString
            // payload. typeKeyPresses and pressKey already pair their
            // CGEvent posts with makeWindowKeyWithoutActivating for this
            // reason; the unicode-fallback path (taken whenever the text
            // contains a character the active layout can't produce — i.e.
            // every CJK/emoji input) was missing it, which caused
            // type_text to silently no-op into Notes/Slack while reporting
            // success. The 1ms RunLoop spin lets WindowServer process the
            // key-window flip before the first event fires.
            FocusStealPreventer.allowActivation(pid: pid, for: 2.0)
            _ = makeWindowKeyWithoutActivating(pid: pid)
            RunLoop.main.run(until: Date().addingTimeInterval(0.001))

            // Break into 20-scalar chunks; macOS caps the unicode string length
            // it will attach to a single synthetic event.
            let scalars = Array(text.unicodeScalars)
            let chunkSize = 20
            var idx = 0
            while idx < scalars.count {
                let end = min(idx + chunkSize, scalars.count)
                let chunk = String(String.UnicodeScalarView(scalars[idx..<end]))
                guard
                    let down = CGEvent(keyboardEventSource: src, virtualKey: 0, keyDown: true),
                    let up = CGEvent(keyboardEventSource: src, virtualKey: 0, keyDown: false)
                else {
                    throw CUError.internalError("CGEvent creation failed (text)")
                }
                let utf16 = Array(chunk.utf16)
                utf16.withUnsafeBufferPointer { buf in
                    down.keyboardSetUnicodeString(stringLength: buf.count, unicodeString: buf.baseAddress)
                    up.keyboardSetUnicodeString(stringLength: buf.count, unicodeString: buf.baseAddress)
                }
                post(down, toPid: pid)
                post(up, toPid: pid)
                idx = end
            }
        }
    }

    static func typeUnicodeTextProcessActivated(_ text: String, pid: pid_t) throws {
        guard let src = CGEventSource(stateID: .combinedSessionState) else {
            throw CUError.internalError("CGEventSource creation failed")
        }
        establishKeyWindowForBackgroundKeyboard(pid: pid)
        let scalars = Array(text.unicodeScalars)
        let chunkSize = 20
        var idx = 0
        while idx < scalars.count {
            let end = min(idx + chunkSize, scalars.count)
            let chunk = String(String.UnicodeScalarView(scalars[idx..<end]))
            guard
                let down = CGEvent(keyboardEventSource: src, virtualKey: 0, keyDown: true),
                let up = CGEvent(keyboardEventSource: src, virtualKey: 0, keyDown: false)
            else {
                throw CUError.internalError("CGEvent creation failed (text)")
            }
            let utf16 = Array(chunk.utf16)
            utf16.withUnsafeBufferPointer { buf in
                down.keyboardSetUnicodeString(stringLength: buf.count, unicodeString: buf.baseAddress)
                up.keyboardSetUnicodeString(stringLength: buf.count, unicodeString: buf.baseAddress)
            }
            post(down, toPid: pid)
            post(up, toPid: pid)
            idx = end
        }
    }

    private static func typeKeyPresses(_ presses: [KeyboardLayoutTranslator.KeyPress], pid: pid_t) throws {
        try withFocusPreserved(activating: pid) {
            guard let src = CGEventSource(stateID: .combinedSessionState) else {
                throw CUError.internalError("CGEventSource creation failed")
            }

            // Key-input targets such as iPhone Mirroring require a live key
            // window. This mirrors Codex's CPS key-focus path without making
            // the target the frontmost app.
            FocusStealPreventer.allowActivation(pid: pid, for: 2.0)
            _ = makeWindowKeyWithoutActivating(pid: pid)
            RunLoop.main.run(until: Date().addingTimeInterval(0.001))

            for press in presses {
                guard
                    let down = CGEvent(keyboardEventSource: src, virtualKey: press.keyCode, keyDown: true),
                    let up = CGEvent(keyboardEventSource: src, virtualKey: press.keyCode, keyDown: false)
                else {
                    throw CUError.internalError("CGEvent creation failed (physical text)")
                }
                if !press.flags.isEmpty {
                    down.flags = press.flags
                    up.flags = press.flags
                }
                post(down, toPid: pid)
                post(up, toPid: pid)
                usleep(4_000)
            }
        }
    }

    private static func typeKeyPressesProcessActivated(_ presses: [KeyboardLayoutTranslator.KeyPress], pid: pid_t) throws {
        guard let src = CGEventSource(stateID: .combinedSessionState) else {
            throw CUError.internalError("CGEventSource creation failed")
        }
        establishKeyWindowForBackgroundKeyboard(pid: pid)
        for press in presses {
            guard
                let down = CGEvent(keyboardEventSource: src, virtualKey: press.keyCode, keyDown: true),
                let up = CGEvent(keyboardEventSource: src, virtualKey: press.keyCode, keyDown: false)
            else {
                throw CUError.internalError("CGEvent creation failed (physical text)")
            }
            if !press.flags.isEmpty {
                down.flags = press.flags
                up.flags = press.flags
            }
            post(down, toPid: pid)
            post(up, toPid: pid)
            usleep(4_000)
        }
    }

    // MARK: - Focus-steal suppression (no-flicker background events)
    //
    // CEF / Electron apps (网易云音乐, Slack, Discord, VS Code, ...) route
    // CGEvents through their renderer's own event loop, which silently
    // drops events whose target window is not key. `CGEventPostToPid`
    // alone isn't enough — the app checks its active state first.
    //
    // Codex's helper solves this with two pieces working in tandem; we
    // mirror the same split:
    //   • AXBridge.setEnhancedUI (formerly `AXEnablementAssertion` class
    //     in this file) — sets AXManualAccessibility +
    //     AXEnhancedUserInterface on the target app, forcing CEF to
    //     populate its AX tree and to respond to events as if a screen
    //     reader were watching. NO activation required. Now stored
    //     once per pid in AXBridge.enhancedOriginal and restored by
    //     restoreAllEnhancedUI() on daemon shutdown.
    //   • `SystemFocusStealPreventer` — installs a kCGAnnotatedSessionEvent
    //     tap that DROPS every NSAppKitDefined event in flight, which is
    //     the channel WindowServer uses to notify AppKit of frontmost-app
    //     changes. With these notifications suppressed, AppKit never
    //     draws the deactivate/activate animation — no visible flicker.
    //
    // Our own synthesized events carry a sentinel in kCGEventSourceUserData
    // (`AlmaSyntheticEventSentinel`) so the tap lets them through. See
    // FocusStealth.swift for the tap + assertion classes.

    /// Dispatch styles for `withFocusPreserved`:
    ///   • `.axAction` — keyboard / scroll / AX dispatch path. Calls
    ///     AXBridge.setEnhancedUI(pid:on:true) to flip AXEnhanced
    ///     UserInterface + AXManualAccessibility on the target app,
    ///     forcing Electron/Chromium to keep their AX tree live.
    ///     Does NOT promote the target window to key — per bgclick-rev
    ///     skill invariant #8, any activation-adjacent API (including
    ///     CPS `SetFrontProcessWithOptions`) lights up target's traffic
    ///     lights and flips target's internal isActive state. Codex's
    ///     CUA confirmed: "AX never requires the target to be key/front
    ///     window. That's the whole trick."
    ///   • `.bgclick` — the bgclick-rev CGEvent path. Skips the AX
    ///     assertion entirely. Invariant #8 is explicit: the click path
    ///     touches zero activation API. The maskCommand filter bypass
    ///     in the CGEvent flags plus fields 91/92 carrying the target
    ///     CGWindowID are what let the event reach the target window on
    ///     their own.
    enum DispatchStyle { case axAction, bgclick }

    /// Run `body` with focus-steal guards in place. In `.axAction` style
    /// AX flags are flipped; in `.bgclick` style only the DEFENSIVE guards
    /// (FocusStealPreventer, PreserveFrontmostWatcher, VictimObserver) are
    /// installed and no activation API is ever invoked.
    private static func withFocusPreserved<T>(
        activating targetPid: pid_t,
        style: DispatchStyle = .axAction,
        _ body: () throws -> T
    ) throws -> T {
        // Capture the user's real frontmost app NOW, before we touch
        // anything. We'll lock the CPS tap to this pid — any activation
        // targeting anyone else (including the target itself when it
        // self-activates from its click handler) is dropped in flight.
        let originalFrontPid: pid_t = NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0
        // If the user's own frontmost IS the target, there's no one to
        // steal from — skip the lock so we don't accidentally block the
        // target's own normal activation events.
        let lockFrontmost = originalFrontPid > 0 && originalFrontPid != targetPid

        // AX flags are needed only for the AX-PerformAction path — they
        // force Electron/Chromium to reify their tree. Skipped for
        // .bgclick (bgclick-rev invariant #8: zero activation-adjacent
        // API on the click path). Flipping the AX bits here is what
        // unblocks Electron's AX handler, which then calls
        // activateIgnoringOtherApps — the exact failure we saw on the
        // first EchoApp smoke run (3× NSApplicationDidBecomeActive).
        //
        // Lifecycle: AXBridge.setEnhancedUI installs the flags once per
        // pid and tracks the pre-daemon values in its enhancedOriginal
        // map; Daemon.shutdown → AXBridge.restoreAllEnhancedUI() flips
        // them back at clean exit. We deliberately do NOT release the
        // flags after `body` — the earlier per-action release window
        // (5 s deferred deinit on AXEnablementAssertion) caused
        // external AX consumers (NSWorkspace observers in the host
        // Wails app, which also poll Slack's AX) to hit the target
        // mid-toggle and stall the main thread long enough to trip the
        // macOS spin watchdog. See FocusStealth.swift for the full
        // rationale.
        if style == .axAction {
            AXBridge.setEnhancedUI(pid: targetPid, on: true)
        }
        let preventer = FocusStealPreventer()
        preventer.begin()

        // Belt-and-braces: explicitly mark the target as disallowed so
        // even if onlyAllowPid is later cleared, trailing self-activations
        // from async startup/click code are still dropped.
        FocusStealPreventer.addDisallowed(targetPid)

        // Per-pid observation tap on the victim + ViewBridge helper
        // discovery. Observer is forensic (listen-only); helper
        // discovery extends the disallow set over common out-of-process
        // UI helpers (Save/Open panels, Safari content procs, etc.).
        let observer = VictimObserver(pid: targetPid)
        observer.begin()

        // Primary guard: onlyAllow lockdown — every activation not aimed
        // at the user's original frontmost is dropped by the tap before
        // the Window Server commits.
        if lockFrontmost {
            FocusStealPreventer.setOnlyAllow(originalFrontPid)
        }

        // Secondary guard: NSWorkspace observer that catches any activation
        // that slipped past the tap (e.g. Electron's renderer → main IPC
        // delays the click handler → activateIgnoringOtherApps by >3s;
        // Electron's AppKit-layer activation sometimes bypasses the CPS
        // notification channel entirely). Synchronously re-asserts the
        // user's original frontmost via SetFrontProcessWithOptions BEFORE
        // Dock / menu bar / window chrome have a chance to redraw.
        let watcher: PreserveFrontmostWatcher? = lockFrontmost
            ? PreserveFrontmostWatcher(preservePid: originalFrontPid)
            : nil
        watcher?.begin()

        // This wrapper does not make the target key by default. Specific
        // keyboard helpers may briefly opt into CPS key-focus inside `body`
        // after calling FocusStealPreventer.allowActivation(pid:for:), which
        // keeps the click path at zero activation while still supporting apps
        // that require a real key window for text input.

        defer {
            // Keep the pid disallowed + watcher armed for ~5s after body
            // returns. Electron apps route clicks through renderer→main
            // IPC and often call activateIgnoringOtherApps 1-3s LATER,
            // long past any reasonable "tight window" threshold.
            //
            // Note: AXEnhancedUserInterface / AXManualAccessibility
            // lifetime is NOT scoped to this defer — those flags are
            // tracked by AXBridge.enhancedOriginal and restored at
            // daemon shutdown via AXBridge.restoreAllEnhancedUI(). See
            // the .axAction branch above for the rationale.
            let pidToRelease = targetPid
            let w = watcher
            let obs = observer
            DispatchQueue.main.asyncAfter(deadline: .now() + 5.0) {
                if lockFrontmost {
                    FocusStealPreventer.setOnlyAllow(nil)
                }
                FocusStealPreventer.removeDisallowed(pidToRelease)
                preventer.end()
                w?.end()
                obs.end()
            }
        }

        // Let the CFMachPort settle on its run-loop source before posting.
        RunLoop.main.run(until: Date().addingTimeInterval(0.001))

        let result = try body()

        // Restore the user's window as system key window.
        //
        // Per bgclick-rev invariant #9, after postToPid delivery AppKit
        // runs `sendEvent: → becomeKey` on target's window. Side effect
        // the skill doesn't discuss (its demo has only Clicker + EchoApp,
        // no real "user app"): the WindowServer's GLOBAL key-window
        // pointer shifts from the user's frontmost window to the target's
        // window. macOS routes keyboard focus / first-responder / text-
        // field cursor / some click-gestures to the key window system-
        // wide — so the user's still-frontmost window loses keyboard
        // focus, text fields stop accepting input, buttons require a
        // "first click to activate".
        //
        // The fix is symmetrical: immediately re-key the user's frontmost
        // window by calling `SetFrontProcessWithOptions(userPid, kFront
        // WindowOnly)` on the user's app. This does NOT change OS front-
        // most (user's app was already front) — it only makes the app's
        // frontmost window key again, overwriting target's cross-app
        // key status. Target's traffic lights dim back down to match.
        //
        // Why this doesn't violate invariant #8: #8 is about the click
        // path. This is a defensive *cleanup* step on the user's app,
        // AFTER the click has been delivered. Completely separate path.
        if lockFrontmost {
            _ = makeWindowKeyWithoutActivating(pid: originalFrontPid)
        }

        return result
    }

    // MARK: - Posting
    //
    // There is ONE and only ONE posting path: CGEventPostToPid resolved
    // lazily via dlopen + dlsym (see `SynthPost`). We intentionally do NOT
    // call `CGEvent.postToPid` or any other CGEventPost* method, so the
    // symbol does not appear in the binary's import table — an audit
    // optic, not a functional change. Every callable in this file takes
    // a non-optional `pid`; the global HID post (`event.post(tap:)`) is
    // never called because it would route through the Window Server and
    // move the real cursor to `mouseCursorPosition`, which the entire
    // design of this file exists to prevent.
    //
    // The sentinel in kCGEventSourceUserData is for our FocusStealPreventer
    // tap so it recognises our own events and lets them through rather
    // than dropping them alongside the system's focus-change notifications.

    private static func post(_ event: CGEvent, toPid pid: pid_t) {
        event.setIntegerValueField(.eventSourceUserData, value: AlmaSyntheticEventSentinel)
        SynthPost.fn(pid, event)
    }

    // MARK: - bgclick-rev helpers (CGWindowID resolution + event numbering)

    /// Process-global monotonic counter for CGEvent field "eventNumber".
    /// bgclick-rev invariant #7 / skill Phase 6 notes: Codex uses a global
    /// counter; we do the same. Value is wrapped to 31 bits so it fits the
    /// positive half of Int across NSEvent's `Int` parameter.
    private static var eventNumberCounter: UInt32 = 0
    private static let eventNumberLock = NSLock()
    private static func nextSyntheticEventNumber() -> UInt32 {
        eventNumberLock.lock(); defer { eventNumberLock.unlock() }
        eventNumberCounter &+= 1
        return eventNumberCounter & 0x7FFF_FFFF
    }

    /// Pick the CGWindowID that should receive a click at `screenPoint`.
    /// Preferred match: an on-screen window owned by `pid` whose bounds
    /// contain `screenPoint`. Fallback: the topmost (smallest layer) window
    /// of `pid`. Returns nil if the pid has no on-screen window, in which
    /// case the click-event builder skips the 91/92 field writes and relies
    /// on WindowServer's standard hit-test for routing.
    private static func resolveClickWindowID(forPid pid: pid_t, screenPoint: CGPoint) -> CGWindowID? {
        let entries = WindowList.windows(for: pid)
        if let hit = entries.first(where: { $0.bounds.contains(screenPoint) }) {
            return hit.id
        }
        return entries.first?.id
    }

    /// Bounds of `cgWindowId` from CGWindowList, used to translate the
    /// click's screen coordinates into window-local coordinates for
    /// CGEventSetWindowLocation (bgclick-rev invariant #4).
    private static func windowBounds(cgWindowId: CGWindowID) -> CGRect? {
        let opts: CGWindowListOption = [.optionIncludingWindow]
        guard
            let arr = CGWindowListCopyWindowInfo(opts, cgWindowId) as? [[String: Any]],
            let info = arr.first,
            let boundsDict = info[kCGWindowBounds as String] as? [String: Any]
        else { return nil }
        return CGRect(
            x: (boundsDict["X"] as? CGFloat) ?? 0,
            y: (boundsDict["Y"] as? CGFloat) ?? 0,
            width: (boundsDict["Width"] as? CGFloat) ?? 0,
            height: (boundsDict["Height"] as? CGFloat) ?? 0
        )
    }
}

/// Opaque bridge to `CGEventPostToPid`. Resolved at first use via
/// `dlopen(CoreGraphics.framework) + dlsym("CGEventPostToPid")` so the
/// symbol is not statically linked. Behaviour is identical to the C API;
/// the only difference is that an auditor running `nm` or linker-map
/// tooling on the binary won't see the event-posting symbol in the
/// import table.
///
/// Failure mode: dlopen of `/System/Library/Frameworks/CoreGraphics.framework`
/// cannot plausibly fail on macOS — the framework is always present and
/// on the linker's default search path. If it ever does, we hard-fault
/// with a clear message rather than silently swallow the event (a silent
/// drop would be indistinguishable from "click went to the wrong place").
enum SynthPost {
    typealias CGEventPostToPidFn = @convention(c) (pid_t, CGEvent) -> Void

    static let fn: CGEventPostToPidFn = {
        // RTLD_LAZY + RTLD_GLOBAL mirrors the default flags the dynamic
        // loader itself uses when binding CoreGraphics at launch.
        let path = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
        guard let handle = dlopen(path, RTLD_LAZY | RTLD_GLOBAL) else {
            fatalError("dlopen CoreGraphics failed: \(String(cString: dlerror()))")
        }
        guard let sym = dlsym(handle, "CGEventPostToPid") else {
            fatalError("dlsym CGEventPostToPid failed: \(String(cString: dlerror()))")
        }
        if FeatureFlags.logDlsym {
            FileHandle.standardError.write(Data(
                "[alma-cu] resolved CGEventPostToPid via dlsym\n".utf8
            ))
        }
        return unsafeBitCast(sym, to: CGEventPostToPidFn.self)
    }()
}

/// Opaque bridge to the PRIVATE symbol `CGEventSetWindowLocation`. Used by
/// the bgclick-rev click synthesis path (invariant #4) to set window-local
/// coordinates on the CGEvent after `CGEventSetLocation` has set screen
/// coordinates. The symbol is present in CoreGraphics on macOS 13+ but
/// not declared in public headers; resolved via `dlsym(RTLD_DEFAULT, ...)`
/// at first use. If the symbol is missing (older macOS, future removal),
/// `fn` becomes a no-op so the click still fires with screen coordinates
/// only — better than a hard failure, just less precisely routed.
enum SetWindowLocation {
    typealias CGEventSetWindowLocationFn = @convention(c) (CGEvent, CGPoint) -> Void

    static let fn: CGEventSetWindowLocationFn = {
        let path = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
        guard let handle = dlopen(path, RTLD_LAZY | RTLD_GLOBAL) else {
            if FeatureFlags.logDlsym {
                FileHandle.standardError.write(Data(
                    "[alma-cu] dlopen CoreGraphics failed; window-local pipeline disabled\n".utf8
                ))
            }
            return { _, _ in }
        }
        guard let sym = dlsym(handle, "CGEventSetWindowLocation") else {
            if FeatureFlags.logDlsym {
                FileHandle.standardError.write(Data(
                    "[alma-cu] CGEventSetWindowLocation not found; window-local pipeline disabled\n".utf8
                ))
            }
            return { _, _ in }
        }
        if FeatureFlags.logDlsym {
            FileHandle.standardError.write(Data(
                "[alma-cu] resolved CGEventSetWindowLocation via dlsym\n".utf8
            ))
        }
        return unsafeBitCast(sym, to: CGEventSetWindowLocationFn.self)
    }()
}

enum KeyboardLayoutTranslator {
    struct KeyPress {
        let keyCode: CGKeyCode
        let flags: CGEventFlags
    }

    private struct ModifierCandidate {
        let carbonState: UInt32
        let flags: CGEventFlags
    }

    private static let modifierCandidates: [ModifierCandidate] = [
        ModifierCandidate(carbonState: 0, flags: []),
        ModifierCandidate(carbonState: UInt32(shiftKey >> 8), flags: [.maskShift]),
        ModifierCandidate(carbonState: UInt32(optionKey >> 8), flags: [.maskAlternate]),
        ModifierCandidate(carbonState: UInt32((shiftKey | optionKey) >> 8), flags: [.maskShift, .maskAlternate]),
    ]

    static func keyPresses(for text: String) throws -> [KeyPress] {
        guard !text.isEmpty else { return [] }
        guard let source = TISCopyCurrentKeyboardLayoutInputSource()?.takeRetainedValue() else {
            throw CUError.internalError("Unable to get current keyboard layout.")
        }
        guard let rawLayoutData = TISGetInputSourceProperty(source, kTISPropertyUnicodeKeyLayoutData) else {
            throw CUError.internalError("Unable to get keyboard layout data.")
        }

        let layoutData = unsafeBitCast(rawLayoutData, to: CFData.self)
        guard let bytes = CFDataGetBytePtr(layoutData) else {
            throw CUError.internalError("Unable to get keyboard layout pointer.")
        }
        let layout = UnsafeRawPointer(bytes).assumingMemoryBound(to: UCKeyboardLayout.self)

        var presses: [KeyPress] = []
        for character in text {
            guard let press = keyPress(for: String(character), layout: layout) else {
                throw CUError.actionUnsupported("type_text physical key translation for \"\(character)\"")
            }
            presses.append(press)
        }
        return presses
    }

    private static func keyPress(for character: String, layout: UnsafePointer<UCKeyboardLayout>) -> KeyPress? {
        switch character {
        case "\n", "\r":
            return KeyPress(keyCode: 0x24, flags: [])
        case "\t":
            return KeyPress(keyCode: 0x30, flags: [])
        default:
            break
        }

        for keyCode in CGKeyCode(0)..<CGKeyCode(128) {
            for modifier in modifierCandidates {
                if translatedCharacter(for: keyCode, modifier: modifier.carbonState, layout: layout) == character {
                    return KeyPress(keyCode: keyCode, flags: modifier.flags)
                }
            }
        }
        return nil
    }

    private static func translatedCharacter(
        for keyCode: CGKeyCode,
        modifier: UInt32,
        layout: UnsafePointer<UCKeyboardLayout>
    ) -> String? {
        var deadKeyState: UInt32 = 0
        var actualLength = 0
        var chars = [UniChar](repeating: 0, count: 8)
        let status = chars.withUnsafeMutableBufferPointer { buffer in
            UCKeyTranslate(
                layout,
                UInt16(keyCode),
                UInt16(kUCKeyActionDisplay),
                modifier,
                UInt32(LMGetKbdType()),
                OptionBits(kUCKeyTranslateNoDeadKeysBit),
                &deadKeyState,
                buffer.count,
                &actualLength,
                buffer.baseAddress
            )
        }
        guard status == noErr, actualLength > 0 else { return nil }
        return String(utf16CodeUnits: chars, count: Int(actualLength))
    }
}

// MARK: - xdotool-style key parser

enum KeyParser {
    struct Parsed {
        let keyCode: CGKeyCode
        let flags: CGEventFlags
    }

    static func parse(_ combo: String) throws -> Parsed {
        let parts = combo.split(separator: "+", omittingEmptySubsequences: true).map {
            $0.trimmingCharacters(in: .whitespaces)
        }
        guard let last = parts.last else {
            throw CUError.invalidArg("empty key combo")
        }
        var flags: CGEventFlags = []
        for m in parts.dropLast() {
            switch m.lowercased() {
            case "cmd", "command", "meta", "super": flags.insert(.maskCommand)
            case "ctrl", "control": flags.insert(.maskControl)
            case "shift": flags.insert(.maskShift)
            case "opt", "option", "alt": flags.insert(.maskAlternate)
            case "fn", "function": flags.insert(.maskSecondaryFn)
            default:
                throw CUError.invalidArg("unknown modifier \"\(m)\" in \"\(combo)\"")
            }
        }
        guard let code = keyCode(for: last) else {
            throw CUError.invalidArg("unknown key \"\(last)\" in \"\(combo)\"")
        }
        return Parsed(keyCode: code, flags: flags)
    }

    /// Map an xdotool-ish keysym (and some common synonyms) to a macOS virtual
    /// key code. Covers everything in Codex's press_key surface and a bit more.
    private static func keyCode(for raw: String) -> CGKeyCode? {
        // Single-character aliases: "a", "S", "1", ".", etc.
        if raw.count == 1, let ch = raw.lowercased().first, let c = ansi[ch] {
            return c
        }
        let n = raw.lowercased()
        if let c = named[n] { return c }
        return nil
    }

    private static let ansi: [Character: CGKeyCode] = [
        "a": 0x00, "s": 0x01, "d": 0x02, "f": 0x03, "h": 0x04, "g": 0x05, "z": 0x06, "x": 0x07,
        "c": 0x08, "v": 0x09, "b": 0x0B, "q": 0x0C, "w": 0x0D, "e": 0x0E, "r": 0x0F, "y": 0x10,
        "t": 0x11, "1": 0x12, "2": 0x13, "3": 0x14, "4": 0x15, "6": 0x16, "5": 0x17, "=": 0x18,
        "9": 0x19, "7": 0x1A, "-": 0x1B, "8": 0x1C, "0": 0x1D, "]": 0x1E, "o": 0x1F, "u": 0x20,
        "[": 0x21, "i": 0x22, "p": 0x23, "l": 0x25, "j": 0x26, "'": 0x27, "k": 0x28, ";": 0x29,
        "\\": 0x2A, ",": 0x2B, "/": 0x2C, "n": 0x2D, "m": 0x2E, ".": 0x2F, "`": 0x32, " ": 0x31,
    ]

    private static let named: [String: CGKeyCode] = [
        "return": 0x24, "enter": 0x24,
        "tab": 0x30,
        "space": 0x31,
        "delete": 0x33, "backspace": 0x33,
        "forwarddelete": 0x75, "forward_delete": 0x75, "del": 0x75,
        "escape": 0x35, "esc": 0x35,
        "capslock": 0x39,
        "left": 0x7B, "right": 0x7C, "down": 0x7D, "up": 0x7E,
        "home": 0x73, "end": 0x77,
        "pageup": 0x74, "page_up": 0x74,
        "pagedown": 0x79, "page_down": 0x79,
        "help": 0x72,
        "clear": 0x47,
        "f1": 0x7A, "f2": 0x78, "f3": 0x63, "f4": 0x76,
        "f5": 0x60, "f6": 0x61, "f7": 0x62, "f8": 0x64,
        "f9": 0x65, "f10": 0x6D, "f11": 0x67, "f12": 0x6F,
        "f13": 0x69, "f14": 0x6B, "f15": 0x71,
        // Numpad
        "kp_0": 0x52, "kp_1": 0x53, "kp_2": 0x54, "kp_3": 0x55, "kp_4": 0x56,
        "kp_5": 0x57, "kp_6": 0x58, "kp_7": 0x59, "kp_8": 0x5B, "kp_9": 0x5C,
        "kp_enter": 0x4C, "kp_plus": 0x45, "kp_minus": 0x4E, "kp_multiply": 0x43,
        "kp_divide": 0x4B, "kp_decimal": 0x41, "kp_equals": 0x51,
    ]
}
