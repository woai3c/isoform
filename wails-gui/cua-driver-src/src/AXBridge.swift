import AppKit
import ApplicationServices
import Foundation

/// All interactions with the macOS Accessibility tree live here.
///
/// Design notes mirrored from Codex's `SkyComputerUseService`:
/// - Clicks are `AXUIElementPerformAction(kAXPressAction)`, NEVER `CGEventPost`.
///   This is why the user's cursor never moves.
/// - Window raising is `kAXRaiseAction` on the window element — raises the
///   window in the app's window list without activating the app, so keyboard
///   focus stays with the user's frontmost app.
/// - Electron/Chromium apps default to a reduced AX tree. We toggle
///   `AXEnhancedUserInterface` when inspecting them and restore it on exit.
enum AXBridge {

    // MARK: - Permission

    /// True if the current process can read/drive the AX tree.
    /// Pass `prompt: true` once when the user first asks Alma to do something;
    /// macOS will show the system TCC dialog.
    static func isTrusted(prompt: Bool) -> Bool {
        let key = "AXTrustedCheckOptionPrompt" as CFString
        let opts: [CFString: CFBoolean] = [key: prompt ? kCFBooleanTrue : kCFBooleanFalse]
        return AXIsProcessTrustedWithOptions(opts as CFDictionary)
    }

    // MARK: - App / window discovery

    struct AppInfo {
        let bundleId: String?
        let name: String
        let pid: pid_t
        let active: Bool
    }

    static func runningApps() -> [AppInfo] {
        let active = NSWorkspace.shared.frontmostApplication?.processIdentifier
        return NSWorkspace.shared.runningApplications
            .filter { $0.activationPolicy == .regular }
            .map {
                AppInfo(
                    bundleId: $0.bundleIdentifier,
                    name: $0.localizedName ?? "(unknown)",
                    pid: $0.processIdentifier,
                    active: $0.processIdentifier == active
                )
            }
            .sorted { $0.name.lowercased() < $1.name.lowercased() }
    }

    static func resolveApp(bundle: String?, pid: pid_t?) throws -> NSRunningApplication {
        if let p = pid, let app = NSRunningApplication(processIdentifier: p) {
            return app
        }
        if let b = bundle {
            let matches = NSRunningApplication.runningApplications(withBundleIdentifier: b)
            if let first = matches.first { return first }
            // Fallback: exact localized name.
            if let byName = NSWorkspace.shared.runningApplications.first(where: { $0.localizedName == b }) {
                return byName
            }
        }
        throw CUError.appNotFound(bundle ?? "pid=\(pid ?? 0)")
    }

    /// Restore a minimized window so ScreenCaptureKit can capture it.
    /// A minimized window draws nothing, so screenshots fail with an
    /// SCStream error. Setting `kAXMinimizedAttribute = false` un-hides
    /// the window WITHOUT activating its app — the window reappears in
    /// its prior position but does not come to front. Returns true if a
    /// restoration was performed, false otherwise (already visible).
    static func unminimizeIfNeeded(_ window: AXUIElement) -> Bool {
        var raw: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(window, kAXMinimizedAttribute as CFString, &raw)
        guard err == .success, let minimized = raw as? Bool, minimized else { return false }
        _ = AXUIElementSetAttributeValue(window, kAXMinimizedAttribute as CFString, kCFBooleanFalse)
        // Give the window server a beat to redraw it so the next screenshot
        // catches content rather than a half-drawn frame.
        Thread.sleep(forTimeInterval: 0.2)
        return true
    }

    /// Launch `bundle` without bringing it to the foreground — the critical
    /// difference from `open -b <bundle>` (which defaults to activates=true).
    /// Codex's computer-use does the same via
    /// `openApplicationAtURL:configuration:` with `setActivates:NO` and
    /// `setAddsToRecentItems:NO`; we match that so the user's frontmost
    /// app and current Space are undisturbed while Alma drives the target
    /// in the background.
    ///
    /// If the app is already running, this is a no-op that returns the
    /// existing instance (including cases where the app would otherwise
    /// be brought forward).
    static func launchApp(bundle: String, activates: Bool = false) async throws -> NSRunningApplication {
        if let existing = NSRunningApplication.runningApplications(withBundleIdentifier: bundle).first {
            return existing
        }
        guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundle) else {
            throw CUError.appNotFound(bundle)
        }
        let config = NSWorkspace.OpenConfiguration()
        config.activates = activates
        config.addsToRecentItems = false
        config.hides = false // don't hide it — we need to screenshot the window
        return try await NSWorkspace.shared.openApplication(at: url, configuration: config)
    }

    struct WindowInfo {
        let element: AXUIElement
        let id: CGWindowID
        let title: String
        let bounds: CGRect
        let minimized: Bool
        let focused: Bool
    }

    static func windows(of app: NSRunningApplication) throws -> [WindowInfo] {
        let appEl = AXUIElementCreateApplication(app.processIdentifier)
        AXUIElementSetMessagingTimeout(appEl, 0.5)
        let windows = (try copyAttr(appEl, kAXWindowsAttribute) as? [AXUIElement]) ?? []
        var focusedRaw: CFTypeRef?
        let fwErr = AXUIElementCopyAttributeValue(appEl, kAXFocusedWindowAttribute as CFString, &focusedRaw)
        let focusedWin: AXUIElement? = (fwErr == .success) ? (focusedRaw as! AXUIElement) : nil
        return windows.compactMap { win -> WindowInfo? in
            AXUIElementSetMessagingTimeout(win, 0.5)
            let title = ((try? copyAttr(win, kAXTitleAttribute)) as? String) ?? ""
            let pos = axPoint(win) ?? .zero
            let size = axSize(win) ?? .zero
            let minimized = ((try? copyAttr(win, kAXMinimizedAttribute)) as? Bool) ?? false
            let focused = focusedWin.map { CFEqual($0, win) } ?? false
            var wid: CGWindowID = 0
            _ = _AXUIElementGetWindow(win, &wid)
            return WindowInfo(
                element: win,
                id: wid,
                title: title,
                bounds: CGRect(origin: pos, size: size),
                minimized: minimized,
                focused: focused
            )
        }
    }

    // MARK: - Snapshot

    struct SerializedElement {
        let ref: String
        let role: String
        let subrole: String?
        let roleDescription: String?
        let title: String?
        let value: String?
        let description: String?
        let url: String?
        let bounds: CGRect?
        let actions: [String]
        let enabled: Bool
        let focused: Bool
        let selected: Bool?
        let childrenCount: Int
        /// Index in the snapshot's element list of the nearest listed
        /// ancestor (nil for a top-level element). Lets a caller keep only
        /// what lies under one element, e.g. a browser's web area.
        let parentIndex: Int?

        func toDict() -> [String: Any?] {
            var out: [String: Any?] = [
                "ref": ref,
                "role": role,
                "enabled": enabled,
                "focused": focused,
                "actions": actions,
                "children_count": childrenCount,
            ]
            if let v = subrole { out["subrole"] = v }
            if let v = roleDescription { out["role_description"] = Self.truncateField(v, limit: Self.labelLimit) }
            if let v = title { out["title"] = Self.truncateField(v, limit: Self.labelLimit) }
            if let v = value { out["value"] = Self.truncateField(v, limit: Self.valueLimit) }
            if let v = description { out["description"] = Self.truncateField(v, limit: Self.valueLimit) }
            if let v = url, v.count <= Self.urlLimit { out["url"] = v }
            if let s = selected { out["selected"] = s }
            if let p = parentIndex { out["parent_index"] = p }
            if let b = bounds {
                out["bounds"] = [
                    "x": Double(b.origin.x),
                    "y": Double(b.origin.y),
                    "w": Double(b.size.width),
                    "h": Double(b.size.height),
                ]
            }
            return out
        }

        // Per-field character caps for the LLM-facing snapshot. Mirrors
        // kwwk-computer-use-core's StateFormatter (primaryLabelLimit 240 /
        // detailValueLimit 500): a full window AX dump of a rich web app
        // (e.g. Gmail) runs to tens of thousands of tokens, and the agent
        // re-snapshots every turn, so repeated full dumps blow the upstream
        // context window. Capping the long free-text fields here bounds the
        // per-element token cost without dropping any element or its ref.
        private static let labelLimit = 240
        private static let valueLimit = 500
        private static let urlLimit = 2_048

        // truncateField caps a field at `limit` characters, appending a
        // marker that tells the model how much was elided. Truncation is
        // OUTPUT-ONLY: element refetch uses the untruncated descriptor title,
        // and actions (set_value / type_text) operate by ref, so capping the
        // serialized text changes nothing about addressing or dispatch.
        private static func truncateField(_ s: String, limit: Int) -> String {
            if s.count <= limit { return s }
            let end = s.index(s.startIndex, offsetBy: limit)
            return String(s[..<end]) + "…[+\(s.count - limit) chars]"
        }
    }

    struct SnapshotResult {
        let snapshotId: String
        let app: NSRunningApplication
        let windowId: CGWindowID?
        let windowTitle: String?
        let windowBounds: CGRect?
        let windowMinimized: Bool
        let elements: [SerializedElement]
        /// Number of walk attempts performed. 1 = populated on first try,
        /// ≥2 = had to wait for AX tree to wake up on an Electron/CEF app.
        let wakeupAttempts: Int
        /// Total ms spent sleeping between wake-up retries. Zero for apps
        /// that populated on first try or when no wake-up budget was given.
        let wakeupDurationMs: Int
        /// Classification of the final tree state:
        ///   "populated"         — has interactive elements, ready for AX click.
        ///   "woke_up"           — was empty initially, populated after wait.
        ///   "empty_after_wakeup"— still empty after full wake-up budget.
        ///   "no_wakeup_budget"  — empty, but caller didn't ask us to wait.
        let axStatus: String
    }

    static func snapshot(
        bundle: String?,
        pid: pid_t?,
        windowId: CGWindowID?,
        depth: Int,
        interactiveOnly: Bool,
        store: ElementStore,
        wakeupSeconds: Double = 0.0,
        includeElementURLs: Bool = false
    ) throws -> SnapshotResult {
        guard isTrusted(prompt: false) else { throw CUError.axDenied() }

        let app = try resolveApp(bundle: bundle, pid: pid)
        setEnhancedUI(app, on: true)
        let appEl = AXUIElementCreateApplication(app.processIdentifier)
        ChromiumAccessibilityActivation.shared.activateIfNeeded(pid: app.processIdentifier, root: appEl)

        // Walking the tree is the read; it populates `out`. We do one
        // pass, then if the result is empty AND the caller gave us a
        // wake-up budget, we wait + re-flip AX attrs + re-walk. This
        // catches Electron/CEF/Chromium apps that stream their AX tree
        // lazily (100-500ms after the `AXEnhancedUserInterface` flip).
        // Without this retry, those apps (Slack, Discord, VS Code, Notion,
        // 飞书, 钉钉, 微信, ...) would always appear AX-empty on the first
        // get_app_state and force Layer 3 synthesized clicks — which
        // flicker on exactly the apps we care about.
        //
        // Apps that genuinely disable AX (网易云音乐) stay empty through
        // the full budget, and the caller falls back to OCR + synth.

        let deadline = Date().addingTimeInterval(wakeupSeconds)
        let snapId = store.beginSnapshot()

        var out: [SerializedElement] = []
        var targetId: CGWindowID?
        var targetTitle: String?
        var targetBounds: CGRect?
        var targetMinimized = false
        var attempts = 0
        var waitedNs: UInt64 = 0

        func doWalk() {
            out.removeAll(keepingCapacity: true)
            targetId = nil; targetTitle = nil; targetBounds = nil; targetMinimized = false
            let wins = (try? windows(of: app)) ?? []
            if !wins.isEmpty {
                let target: WindowInfo = {
                    if let wid = windowId, let m = wins.first(where: { $0.id == wid }) { return m }
                    return wins.first(where: { $0.focused }) ?? wins[0]
                }()
                targetId = target.id
                targetTitle = target.title
                targetBounds = target.bounds
                targetMinimized = target.minimized
                walk(target.element, depth: depth, maxDepth: depth,
                     interactiveOnly: interactiveOnly,
                     pid: app.processIdentifier, snapId: snapId,
                     store: store, out: &out,
                     includeElementURLs: includeElementURLs)
            } else if let wlist = WindowList.topWindow(for: app.processIdentifier) {
                targetId = wlist.id
                targetTitle = wlist.title
                targetBounds = wlist.bounds
            }
        }

        // "Effectively empty" = nothing the agent can act on. A lone window
        // shell (out.count == 1, typically the AXWindow itself with an
        // AXRaise action) tells us nothing about what's inside — it's the
        // same as having no AX tree at all for targeting purposes. This
        // matches the heuristic `get_app_state` uses to trigger OCR fallback.
        func isEffectivelyEmpty(_ elems: [SerializedElement]) -> Bool {
            elems.count <= 1
        }

        doWalk()
        attempts = 1
        let startedEmpty = isEffectivelyEmpty(out)

        // Back-off schedule: 150, 250, 400ms. Typical Electron wake-up
        // happens on the first retry (150ms); the escalation gives slow
        // apps (Teams, some Qt wrappers) a fair chance without burning
        // too much wall-clock.
        let backoff: [TimeInterval] = [0.15, 0.25, 0.40]
        var i = 0
        while isEffectivelyEmpty(out), Date() < deadline, i < backoff.count {
            let sleepFor = min(backoff[i], max(0, deadline.timeIntervalSinceNow))
            if sleepFor <= 0 { break }
            let t0 = DispatchTime.now().uptimeNanoseconds
            Thread.sleep(forTimeInterval: sleepFor)
            waitedNs &+= DispatchTime.now().uptimeNanoseconds - t0
            // Re-assert the AX flags. Some CEF builds reset them silently.
            setEnhancedUI(app, on: true)
            doWalk()
            attempts += 1
            i += 1
        }

        let empty = isEffectivelyEmpty(out)
        let axStatus: String = {
            if !empty && !startedEmpty { return "populated" }
            if !empty && startedEmpty { return "woke_up" }
            if wakeupSeconds > 0 { return "empty_after_wakeup" }
            return "no_wakeup_budget"
        }()

        return SnapshotResult(
            snapshotId: snapId,
            app: app,
            windowId: targetId,
            windowTitle: targetTitle,
            windowBounds: targetBounds,
            windowMinimized: targetMinimized,
            elements: out,
            wakeupAttempts: attempts,
            wakeupDurationMs: Int(waitedNs / 1_000_000),
            axStatus: axStatus
        )
    }

    private static func walk(
        _ el: AXUIElement,
        depth: Int,
        maxDepth: Int,
        interactiveOnly: Bool,
        pid: pid_t,
        snapId: String,
        store: ElementStore,
        out: inout [SerializedElement],
        includeElementURLs: Bool = false,
        parentRoles: [String] = [],
        parentIndex: Int? = nil
    ) {
        let role = (try? copyAttr(el, kAXRoleAttribute) as? String) ?? ""
        let actions = copyActionNames(el)
        let isInteractive = !actions.isEmpty || interactiveRoles.contains(role)

        let children = (try? copyAttr(el, kAXChildrenAttribute) as? [AXUIElement]) ?? []
        // Set when this element is listed: its children name it as parent;
        // a skipped element passes its own parent on.
        var listedIndex: Int? = nil

        // A web area is listed whenever addresses are asked for, so a caller
        // checking pages finds every page and what lies under it.
        let listedWebArea = includeElementURLs && role == "AXWebArea"
        if !interactiveOnly || isInteractive || listedWebArea {
            let subrole = (try? copyAttr(el, kAXSubroleAttribute)) as? String
            let roleDesc = (try? copyAttr(el, kAXRoleDescriptionAttribute)) as? String
            // Many apps (Calculator, toolbar buttons) leave kAXTitleAttribute
            // empty and put the label in kAXLabelValueAttribute or kAXTitleUIElement.
            // Try title → titleUIElement → label → description → help, in order,
            // and collapse them into one visible `title` field so LLMs have a
            // single stable name.
            let title: String? = {
                if let s = (try? copyAttr(el, kAXTitleAttribute)) as? String, !s.isEmpty { return s }
                if let t = (try? copyAttr(el, kAXTitleUIElementAttribute)) {
                    let titleEl = t as! AXUIElement
                    if let s = (try? copyAttr(titleEl, kAXValueAttribute)) as? String, !s.isEmpty { return s }
                }
                if let s = (try? copyAttr(el, "AXLabelValue")) as? String, !s.isEmpty { return s }
                return nil
            }()
            let value = readValueString(el)
            let description = ((try? copyAttr(el, kAXDescriptionAttribute)) as? String)
                ?? ((try? copyAttr(el, kAXHelpAttribute)) as? String)
            let url = includeElementURLs ? readURLString(el) : nil
            let enabled = ((try? copyAttr(el, kAXEnabledAttribute)) as? Bool) ?? true
            let focused = ((try? copyAttr(el, kAXFocusedAttribute)) as? Bool) ?? false
            let selected = ((try? copyAttr(el, kAXSelectedAttribute)) as? Bool)
            let identifier = (try? copyAttr(el, kAXIdentifierAttribute)) as? String

            let bounds = boundsRect(el)

            // Build the refetch descriptor from attrs we already read.
            // Keep the last 8 parent roles only — deeper chains don't
            // buy additional uniqueness in practice.
            let chain = Array(parentRoles.suffix(8))
            let descriptor = ElementDescriptor(
                role: role, subrole: subrole,
                title: title.flatMap(nonEmpty),
                identifier: identifier.flatMap(nonEmpty),
                roleDescription: roleDesc.flatMap(nonEmpty),
                parentRoles: chain
            )

            let ref = store.register(el, snapshotId: snapId, pid: pid, descriptor: descriptor)
            out.append(SerializedElement(
                ref: ref, role: role, subrole: subrole, roleDescription: roleDesc,
                title: title.flatMap(nonEmpty),
                value: value.flatMap(nonEmpty),
                description: description.flatMap(nonEmpty),
                url: url.flatMap(nonEmpty),
                bounds: bounds,
                actions: actions, enabled: enabled, focused: focused, selected: selected,
                childrenCount: children.count,
                parentIndex: parentIndex
            ))
            listedIndex = out.count - 1
        }

        if depth > 0 {
            // `el`'s role becomes part of the chain for its children.
            // Even skipped (non-interactive) nodes contribute to the
            // chain, so the descriptor's chain matches the live tree's
            // chain regardless of filtering.
            let childChain = parentRoles + [role]
            for child in children {
                walk(child, depth: depth - 1, maxDepth: maxDepth,
                     interactiveOnly: interactiveOnly, pid: pid, snapId: snapId,
                     store: store, out: &out,
                     includeElementURLs: includeElementURLs,
                     parentRoles: childChain,
                     parentIndex: listedIndex ?? parentIndex)
            }
        }
    }

    private static let interactiveRoles: Set<String> = [
        kAXButtonRole, kAXCheckBoxRole, kAXRadioButtonRole, kAXPopUpButtonRole,
        kAXMenuButtonRole, kAXMenuItemRole,
        // "AXTab" / "AXLink" have no header constants on macOS — use string literals.
        "AXTab", "AXLink",
        kAXTextFieldRole, kAXTextAreaRole, kAXComboBoxRole, kAXSliderRole,
        kAXIncrementorRole, kAXDisclosureTriangleRole, kAXCellRole, kAXRowRole,
    ]

    // MARK: - Actions (click / type / press / scroll / raise)

    /// Click strategy.
    /// `.auto` (default): AX action (AXPress/AXPick/AXConfirm) when the
    ///   element exposes one — this path routes through the accessibility
    ///   server, doesn't require the target app to be active, and doesn't
    ///   trigger the target's mouse-click handler (which many Cocoa / CEF
    ///   apps use as a cue to self-activate via `[NSApp activateIgnoringOtherApps:]`).
    ///   Falls back to CGEvent only when the element has NO AX action.
    /// `.physical`: always CGEvent (use when a specific click cannot be
    ///   done via AX — e.g. double-click to play a track in Music.app).
    /// `.ax`: force AX only; fail if no AX action exists.
    enum ClickStrategy: String { case auto, physical, ax }

    static func click(
        ref: String,
        store: ElementStore,
        strategy: ClickStrategy = .auto,
        button: EventDispatch.MouseButton = .left,
        clickCount: Int = 1,
        showCursor: Bool = true
    ) throws {
        let entry = try resolveRef(ref, store: store)
        let bounds = boundsRect(entry.element)
        let center = bounds.map { CGPoint(x: $0.midX, y: $0.midY) }
        let shouldRenderLens = showCursor
        if shouldRenderLens, let c = center { LensOverlay.shared.showAt(axPoint: c) }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }

        // Try AX first on `.auto` and `.ax`. AXPress via AXUIElementPerformAction
        // is routed through the accessibility server — it doesn't deliver a
        // mouse event to the app's event loop, so the app's click-handler
        // self-activation cue (NSApp.activateIgnoringOtherApps:) doesn't fire.
        // Net: target handles the click, stays backgrounded.
        //
        // `ALMA_CU_FORCE_SYNTH=1` forces the synthetic path unconditionally
        // so audit-sensitive deployments can prove the dlsym CGEventPost
        // path actually fires end-to-end.
        let effectiveStrategy: ClickStrategy = FeatureFlags.forceSynth ? .physical : strategy
        let wantAX = (effectiveStrategy != .physical) && button == .left && clickCount <= 1
        if wantAX {
            let actions = copyActionNames(entry.element)
            for a in [kAXPressAction, kAXPickAction, kAXConfirmAction] where actions.contains(a as String) {
                let err = AXUIElementPerformAction(entry.element, a as CFString)
                try throwIfAXError(err, context: "performing \(a)", ref: ref)
                if shouldRenderLens { LensOverlay.shared.clickPulse() }
                return
            }
            if effectiveStrategy == .ax {
                throw CUError.actionUnsupported("click with strategy=ax: no AXPress/AXPick/AXConfirm on element")
            }
            // .auto with no AX action → fall through to CGEvent below.
        }

        // CGEvent path. Used for:
        //   • `.physical` explicitly (double/triple click, right click)
        //   • `.auto` where the element has no AX action
        guard let c = center else {
            throw CUError.actionUnsupported("click: element has no AX action and no screen bounds for a physical click")
        }
        try EventDispatch.click(at: c, button: button, clickCount: clickCount, pid: entry.pid)
        if shouldRenderLens { LensOverlay.shared.clickPulse() }
    }

    static func clickProcessActivated(
        ref: String,
        store: ElementStore,
        windowNumber: Int,
        windowFrame: CGRect,
        strategy: ClickStrategy = .auto,
        button: EventDispatch.MouseButton = .left,
        clickCount: Int = 1,
        preferPhysical: Bool = false,
        showCursor: Bool = true
    ) throws {
        let entry = try resolveRef(ref, store: store)
        let bounds = boundsRect(entry.element)
        let center = bounds.map { CGPoint(x: $0.midX, y: $0.midY) }
        let shouldRenderLens = showCursor
        if shouldRenderLens, let c = center { LensOverlay.shared.showAt(axPoint: c) }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }

        let effectiveStrategy: ClickStrategy = FeatureFlags.forceSynth ? .physical : strategy
        let skipAXForWebRenderer = preferPhysical && effectiveStrategy == .auto
        let wantAX = !skipAXForWebRenderer && (effectiveStrategy != .physical) && button == .left && clickCount <= 1
        if wantAX {
            let actions = copyActionNames(entry.element)
            for a in [kAXPressAction, kAXPickAction, kAXConfirmAction] where actions.contains(a as String) {
                let err = AXUIElementPerformAction(entry.element, a as CFString)
                try throwIfAXError(err, context: "performing \(a)", ref: ref)
                if shouldRenderLens { LensOverlay.shared.clickPulse() }
                return
            }
            if effectiveStrategy == .ax {
                throw CUError.actionUnsupported("click with strategy=ax: no AXPress/AXPick/AXConfirm on element")
            }
        }

        guard let c = center else {
            throw CUError.actionUnsupported("click: element has no AX action and no screen bounds for a physical click")
        }
        BackgroundActivationSession.click(
            targetPID: entry.pid,
            windowNumber: windowNumber,
            windowFrame: windowFrame,
            at: c,
            button: button,
            clickCount: clickCount
        )
        if shouldRenderLens { LensOverlay.shared.clickPulse() }
    }

    /// Pixel-coordinate click, no AX ref required. `pid` is required —
    /// without it we'd have no way to route the CGEvent without going
    /// through the global HID tap (which moves the real cursor).
    static func clickAtPoint(
        at point: CGPoint,
        pid: pid_t,
        button: EventDispatch.MouseButton = .left,
        clickCount: Int = 1,
        showCursor: Bool = true
    ) throws {
        let shouldRenderLens = showCursor
        if shouldRenderLens { LensOverlay.shared.showAt(axPoint: point) }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }

        try EventDispatch.click(at: point, button: button, clickCount: clickCount, pid: pid)
        if shouldRenderLens { LensOverlay.shared.clickPulse() }
    }

    static func clickAtPointProcessActivated(
        at point: CGPoint,
        pid: pid_t,
        windowNumber: Int,
        windowFrame: CGRect,
        button: EventDispatch.MouseButton = .left,
        clickCount: Int = 1,
        showCursor: Bool = true
    ) {
        let shouldRenderLens = showCursor
        if shouldRenderLens { LensOverlay.shared.showAt(axPoint: point) }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }

        BackgroundActivationSession.click(
            targetPID: pid,
            windowNumber: windowNumber,
            windowFrame: windowFrame,
            at: point,
            button: button,
            clickCount: clickCount
        )
        if shouldRenderLens { LensOverlay.shared.clickPulse() }
    }

    /// Secondary (right) click. Codex exposes this as a first-class verb
    /// because lots of apps (Music, Finder, Notes) hide menus behind the
    /// context menu rather than AX actions. Try AXShowMenu first so the
    /// keyboard focus stays put when possible; fall back to a physical
    /// right-click if AXShowMenu isn't exposed.
    static func performSecondaryAction(
        ref: String,
        store: ElementStore,
        showCursor: Bool = true
    ) throws {
        let entry = try resolveRef(ref, store: store)
        let actions = copyActionNames(entry.element)
        let center = boundsRect(entry.element).map { CGPoint(x: $0.midX, y: $0.midY) }
        let shouldRenderLens = showCursor
        if shouldRenderLens, let c = center { LensOverlay.shared.showAt(axPoint: c) }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }

        if actions.contains(kAXShowMenuAction as String) {
            let err = AXUIElementPerformAction(entry.element, kAXShowMenuAction as CFString)
            try throwIfAXError(err, context: "performing AXShowMenu", ref: ref)
            if shouldRenderLens { LensOverlay.shared.clickPulse() }
            return
        }
        guard let c = center else {
            throw CUError.actionUnsupported("perform_secondary_action (no AXShowMenu and no bounds)")
        }
        try EventDispatch.secondaryClick(at: c, pid: entry.pid)
        if shouldRenderLens { LensOverlay.shared.clickPulse() }
    }

    /// Drag between two pixel points. Currently coordinate-based only —
    /// Codex's `drag` is also coordinate-based; AX has no drag action to fall
    /// back to.
    static func drag(
        from start: CGPoint,
        to end: CGPoint,
        pid: pid_t,
        button: EventDispatch.MouseButton = .left,
        showCursor: Bool = true
    ) throws {
        let shouldRenderLens = showCursor
        if shouldRenderLens { LensOverlay.shared.showAt(axPoint: start) }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }
        try EventDispatch.drag(from: start, to: end, button: button, pid: pid)
        if shouldRenderLens { LensOverlay.shared.clickPulse() }
    }

    /// Directly set `AXValue` on an element (Codex's `set_value`). Unlike
    /// `type`, this skips the "focus first then set" dance and does not route
    /// through the keyboard simulator — good for sliders, steppers, and text
    /// fields where we want a clean overwrite rather than keystroke echo.
    static func setValue(ref: String, value: Any, store: ElementStore) throws {
        let entry = try resolveRef(ref, store: store)
        var settable: DarwinBoolean = false
        let q = AXUIElementIsAttributeSettable(entry.element, kAXValueAttribute as CFString, &settable)
        guard q == .success, settable.boolValue else {
            throw CUError.actionUnsupported("set_value (AXValue not settable on this element)")
        }
        let cfValue: CFTypeRef
        switch value {
        case let s as String: cfValue = s as CFString
        case let n as NSNumber: cfValue = n
        case let b as Bool: cfValue = b as CFBoolean
        default: cfValue = String(describing: value) as CFString
        }
        let err = AXUIElementSetAttributeValue(entry.element, kAXValueAttribute as CFString, cfValue)
        try throwIfAXError(err, context: "setting AXValue", ref: ref)
    }

    /// Focus an element over AX without writing any value. Used by the
    /// Electron/Chromium type_text fallback: AX text fields on those apps
    /// are read-only mirrors, so we focus via AX (best effort) and then
    /// deliver synthesized key events instead of writing AXValue.
    static func focusElement(ref: String, store: ElementStore, showCursor: Bool = true) throws {
        let entry = try resolveRef(ref, store: store)
        if showCursor, let c = boundsRect(entry.element).map({ CGPoint(x: $0.midX, y: $0.midY) }) {
            LensOverlay.shared.showAt(axPoint: c, slideMs: 140)
            LensOverlay.shared.typeBadge()
        }
        defer { if showCursor { LensOverlay.shared.hide() } }
        _ = AXUIElementSetAttributeValue(entry.element, kAXFocusedAttribute as CFString, kCFBooleanTrue)
    }

    /// Read the element's current AXValue as a string (nil if unset or not a
    /// string). Used to verify whether an AX write actually landed — on
    /// Chromium/CEF read-only AX mirrors the value never changes, which is
    /// the signal the type_text fallback uses to switch to synthesized keys.
    static func currentValue(ref: String, store: ElementStore) -> String? {
        guard let entry = try? resolveRef(ref, store: store) else { return nil }
        return (try? copyAttr(entry.element, kAXValueAttribute)) as? String
    }

    static func type(ref: String, text: String, replace: Bool, store: ElementStore, showCursor: Bool = true) throws {
        let entry = try resolveRef(ref, store: store)
        let center = boundsRect(entry.element).map { CGPoint(x: $0.midX, y: $0.midY) }
        let shouldRenderLens = showCursor
        if shouldRenderLens, let c = center {
            LensOverlay.shared.showAt(axPoint: c, slideMs: 140)
            LensOverlay.shared.typeBadge()
        }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }

        // Focus the element first so Cmd-Z etc. land correctly.
        _ = AXUIElementSetAttributeValue(entry.element, kAXFocusedAttribute as CFString, kCFBooleanTrue)

        if replace {
            let err = AXUIElementSetAttributeValue(entry.element, kAXValueAttribute as CFString, text as CFString)
            try throwIfAXError(err, context: "setting AXValue", ref: ref)
            return
        }

        // Insert at current selection using kAXSelectedTextAttribute if supported,
        // otherwise append to kAXValue.
        var settable: DarwinBoolean = false
        let q = AXUIElementIsAttributeSettable(entry.element, kAXSelectedTextAttribute as CFString, &settable)
        if q == .success, settable.boolValue {
            let err = AXUIElementSetAttributeValue(entry.element, kAXSelectedTextAttribute as CFString, text as CFString)
            try throwIfAXError(err, context: "inserting at selection", ref: ref)
            return
        }
        // Fallback: append.
        let existing = ((try? copyAttr(entry.element, kAXValueAttribute)) as? String) ?? ""
        let err = AXUIElementSetAttributeValue(entry.element, kAXValueAttribute as CFString, (existing + text) as CFString)
        try throwIfAXError(err, context: "appending to AXValue", ref: ref)
    }

    static func press(ref: String, key: String, store: ElementStore, showCursor: Bool = true) throws {
        let entry = try resolveRef(ref, store: store)
        let center = boundsRect(entry.element).map { CGPoint(x: $0.midX, y: $0.midY) }
        let shouldRenderLens = showCursor
        if shouldRenderLens, let c = center { LensOverlay.shared.showAt(axPoint: c) }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }
        let actions = copyActionNames(entry.element)
        let mapped: String
        switch key.lowercased() {
        case "enter", "return": mapped = kAXConfirmAction as String
        case "escape", "esc": mapped = kAXCancelAction as String
        case "space":
            // Press is the nearest AX equivalent of space for buttons.
            mapped = kAXPressAction as String
        case "increment", "up":
            mapped = kAXIncrementAction as String
        case "decrement", "down":
            mapped = kAXDecrementAction as String
        case "showmenu", "menu":
            mapped = kAXShowMenuAction as String
        default:
            throw CUError.actionUnsupported("press \(key) (only Enter/Escape/Space/Increment/Decrement/ShowMenu are supported over AX in v1)")
        }
        guard actions.contains(mapped) else {
            throw CUError.actionUnsupported("press \(key) → \(mapped) not on element")
        }
        let err = AXUIElementPerformAction(entry.element, mapped as CFString)
        try throwIfAXError(err, context: "performing \(mapped)", ref: ref)
        if shouldRenderLens { LensOverlay.shared.clickPulse() }
    }

    static func scroll(ref: String, direction: String, pages: Int, store: ElementStore, showCursor: Bool = true) throws {
        let entry = try resolveRef(ref, store: store)
        let center = boundsRect(entry.element).map { CGPoint(x: $0.midX, y: $0.midY) }
        let shouldRenderLens = showCursor
        if shouldRenderLens, let c = center {
            LensOverlay.shared.showAt(axPoint: c, slideMs: 140)
            LensOverlay.shared.scrollBadge(direction: direction)
        }
        defer { if shouldRenderLens { LensOverlay.shared.hide() } }
        let action: String
        switch direction.lowercased() {
        case "up": action = "AXScrollUpByPage"
        case "down": action = "AXScrollDownByPage"
        case "left": action = "AXScrollLeftByPage"
        case "right": action = "AXScrollRightByPage"
        default: throw CUError.invalidArg("direction must be up|down|left|right")
        }
        let actions = copyActionNames(entry.element)
        guard actions.contains(action) else {
            throw CUError.actionUnsupported("\(action) not available on element (ref points at an unscrollable element — snap the parent ScrollArea)")
        }
        for _ in 0..<max(1, pages) {
            let err = AXUIElementPerformAction(entry.element, action as CFString)
            try throwIfAXError(err, context: "performing \(action)", ref: ref)
            if shouldRenderLens { LensOverlay.shared.clickPulse() }
        }
    }

    static func raise(bundle: String?, pid: pid_t?, windowId: CGWindowID?) throws {
        let app = try resolveApp(bundle: bundle, pid: pid)
        let wins = try windows(of: app)
        let target: WindowInfo = {
            if let wid = windowId, let m = wins.first(where: { $0.id == wid }) { return m }
            return wins.first(where: { $0.focused }) ?? wins.first!
        }()
        let err = AXUIElementPerformAction(target.element, kAXRaiseAction as CFString)
        try throwIfAXError(err, context: "raising window", ref: "(window \(target.id))")
    }

    // MARK: - Visibility gating
    //
    // The lens overlay is a visual-feedback affordance. It must NEVER appear
    // when the user is working in another app — a floating ring over their
    // current window would be intrusive noise. So every action asks this
    // gate first; if the target isn't genuinely in front of the user, we
    // perform the AX action silently (matching Codex's background behaviour).
    //
    // "In front of the user" means:
    //   1. The target app is the frontmost application.
    //   2. The element lives under that app's focused window (not a minimised
    //      or background window of the same app).
    //   3. That window is on the user's current Space (the AX-reported bounds
    //      are on-screen).
    static func isTargetForeground(element: AXUIElement, pid: pid_t) -> Bool {
        guard NSWorkspace.shared.frontmostApplication?.processIdentifier == pid else { return false }

        // Walk up to the enclosing window element.
        var cursor: AXUIElement? = element
        var window: AXUIElement?
        for _ in 0..<32 { // depth guard against cycles
            guard let cur = cursor else { break }
            let role = (try? copyAttr(cur, kAXRoleAttribute)) as? String
            if role == (kAXWindowRole as String) { window = cur; break }
            var raw: CFTypeRef?
            let err = AXUIElementCopyAttributeValue(cur, kAXParentAttribute as CFString, &raw)
            if err == .success, let parent = raw { cursor = (parent as! AXUIElement) } else { break }
        }
        guard let win = window else { return false }

        // Compare against the app's focused window. Some apps (notably
        // browsers with side panels) may not set focus correctly; in that
        // case we accept any non-minimised window bound to a real Space.
        let appEl = AXUIElementCreateApplication(pid)
        var raw: CFTypeRef?
        let ferr = AXUIElementCopyAttributeValue(appEl, kAXFocusedWindowAttribute as CFString, &raw)
        if ferr == .success, let focusedAny = raw {
            let focused = focusedAny as! AXUIElement
            if !CFEqual(win, focused) { return false }
        }

        // Minimised / off-screen guard.
        if let minimised = ((try? copyAttr(win, kAXMinimizedAttribute)) as? Bool), minimised { return false }

        // Space check: CGWindowID of zero means the window is not drawable on
        // the current Space (e.g. collapsed Finder desktop). Skip.
        var wid: CGWindowID = 0
        _ = _AXUIElementGetWindow(win, &wid)
        if wid == 0 { return false }

        return true
    }

    // MARK: - Enhanced UI toggle
    //
    // Two levers, both set when we first touch an app:
    //   • AXEnhancedUserInterface — Electron/Chromium heuristic. Setting true
    //     forces the renderer to stream the full AX tree to the platform.
    //   • AXManualAccessibility — what Codex also flips. Some apps (including
    //     certain Qt builds and Xcode) respond to this second toggle where the
    //     first is a no-op. Harmless on apps that ignore it.
    //
    // We restore both to their pre-daemon values on shutdown.
    private static var enhancedOriginal: [pid_t: (Bool, Bool)] = [:]
    private static let enhancedLock = NSLock()
    private static let axEnhanced = "AXEnhancedUserInterface"
    private static let axManual = "AXManualAccessibility"

    /// Convenience overload that resolves an NSRunningApplication
    /// from a pid_t. Used by EventDispatch.withFocusPreserved, which
    /// works in raw pids — without this wrapper, that path would have
    /// to re-implement the AXUIElement creation and the
    /// enhancedOriginal tracking, splitting the cleanup contract.
    /// No-op when the pid is no longer running.
    static func setEnhancedUI(pid: pid_t, on: Bool) {
        guard let app = NSRunningApplication(processIdentifier: pid) else { return }
        setEnhancedUI(app, on: on)
    }

    static func setEnhancedUI(_ app: NSRunningApplication, on: Bool) {
        enhancedLock.lock(); defer { enhancedLock.unlock() }
        let pid = app.processIdentifier
        let el = AXUIElementCreateApplication(pid)
        if on {
            if enhancedOriginal[pid] == nil {
                let prevE = (try? copyAttr(el, axEnhanced) as? Bool) ?? false
                let prevM = (try? copyAttr(el, axManual) as? Bool) ?? false
                enhancedOriginal[pid] = (prevE, prevM)
            }
            _ = AXUIElementSetAttributeValue(el, axEnhanced as CFString, kCFBooleanTrue)
            _ = AXUIElementSetAttributeValue(el, axManual as CFString, kCFBooleanTrue)
        } else {
            let (prevE, prevM) = enhancedOriginal[pid] ?? (false, false)
            _ = AXUIElementSetAttributeValue(el, axEnhanced as CFString,
                                             prevE ? kCFBooleanTrue : kCFBooleanFalse)
            _ = AXUIElementSetAttributeValue(el, axManual as CFString,
                                             prevM ? kCFBooleanTrue : kCFBooleanFalse)
            enhancedOriginal.removeValue(forKey: pid)
        }
    }

    static func restoreAllEnhancedUI() {
        enhancedLock.lock()
        let snapshot = enhancedOriginal
        enhancedOriginal.removeAll()
        enhancedLock.unlock()
        for (pid, pair) in snapshot {
            let el = AXUIElementCreateApplication(pid)
            _ = AXUIElementSetAttributeValue(el, axEnhanced as CFString,
                                             pair.0 ? kCFBooleanTrue : kCFBooleanFalse)
            _ = AXUIElementSetAttributeValue(el, axManual as CFString,
                                             pair.1 ? kCFBooleanTrue : kCFBooleanFalse)
        }
    }

    // MARK: - Helpers

    private static func resolveRef(_ ref: String, store: ElementStore) throws -> ElementStore.Entry {
        guard let entry = store.resolve(ref) else { throw CUError.elementNotFound(ref) }
        // Ambient fast-path: if the AXObserver already told us this
        // element is destroyed, skip the attribute probe and go
        // straight to descriptor refetch. Saves one AX round-trip
        // per stale ref and avoids the spurious `invalidUIElement`
        // error the probe would otherwise log.
        if entry.invalidated {
            if let fresh = store.refetchByDescriptor(ref) { return fresh }
            throw CUError.refStale(ref)
        }
        // Cheap liveness check — catches the window where the
        // AXObserver hasn't delivered the destroyed notification yet
        // (real but small: notifications are coalesced per run-loop
        // cycle, so between a destroy and the next cycle we'd read
        // `invalidated == false`).
        var raw: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(entry.element, kAXRoleAttribute as CFString, &raw)
        if err == .invalidUIElement {
            // Original element destroyed. Try to find an equivalent via
            // the identity descriptor captured at register time. Three
            // possible outcomes, mirroring Codex's RefetchableSkyshot
            // AXTree:
            //   • exactly 1 match  → silently rebind, agent never notices
            //   • 0 matches        → element is truly gone → ref_stale
            //   • ≥2 matches       → ambiguous → refuse, agent must re-snap
            //
            // The middle case (ambiguous) is safety-first: better to force
            // a re-snap than accidentally click the wrong button. Codex's
            // string `"Multiple existing equivalent elements; cannot
            // guarantee uniqueness"` encodes the same stance.
            if let fresh = store.refetchByDescriptor(ref) {
                return fresh
            }
            throw CUError.refStale(ref)
        }
        return entry
    }

    static func copyAttr(_ el: AXUIElement, _ name: String) throws -> Any? {
        var raw: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(el, name as CFString, &raw)
        switch err {
        case .success: return raw
        case .noValue, .attributeUnsupported: return nil
        case .apiDisabled: throw CUError.axDenied()
        default: return nil
        }
    }

    static func copyActionNames(_ el: AXUIElement) -> [String] {
        var raw: CFArray?
        let err = AXUIElementCopyActionNames(el, &raw)
        guard err == .success, let arr = raw as? [String] else { return [] }
        return arr
    }

    static func axPoint(_ el: AXUIElement) -> CGPoint? {
        guard let v = try? copyAttr(el, kAXPositionAttribute) else { return nil }
        let val = v as! AXValue
        var p = CGPoint.zero
        if AXValueGetValue(val, .cgPoint, &p) { return p }
        return nil
    }

    static func axSize(_ el: AXUIElement) -> CGSize? {
        guard let v = try? copyAttr(el, kAXSizeAttribute) else { return nil }
        let val = v as! AXValue
        var s = CGSize.zero
        if AXValueGetValue(val, .cgSize, &s) { return s }
        return nil
    }

    static func boundsRect(_ el: AXUIElement) -> CGRect? {
        if let p = axPoint(el), let s = axSize(el) {
            return CGRect(origin: p, size: s)
        }
        return nil
    }

    private static func readValueString(_ el: AXUIElement) -> String? {
        if let s = try? copyAttr(el, kAXValueAttribute) as? String { return s }
        if let n = try? copyAttr(el, kAXValueAttribute) as? NSNumber { return n.stringValue }
        return nil
    }

    private static func readURLString(_ el: AXUIElement) -> String? {
        guard let raw = try? copyAttr(el, kAXURLAttribute) else { return nil }
        if let url = raw as? URL { return url.absoluteString }
        if let url = raw as? NSURL { return url.absoluteString }
        if let value = raw as? String { return value }
        return nil
    }

    private static func nonEmpty(_ s: String) -> String? { s.isEmpty ? nil : s }

    private static func throwIfAXError(_ err: AXError, context: String, ref: String) throws {
        switch err {
        case .success: return
        case .invalidUIElement: throw CUError.refStale(ref)
        case .actionUnsupported, .attributeUnsupported: throw CUError.actionUnsupported(context)
        case .apiDisabled: throw CUError.axDenied()
        default: throw CUError.axFailure(err, context: context)
        }
    }
}

// Private CoreGraphics SPI present since 10.0; provides the CGWindowID for
// an AXUIElement window so we can cross-reference with ScreenCaptureKit / CGWindowList.
@_silgen_name("_AXUIElementGetWindow")
func _AXUIElementGetWindow(_ element: AXUIElement, _ idOut: UnsafeMutablePointer<CGWindowID>) -> AXError
