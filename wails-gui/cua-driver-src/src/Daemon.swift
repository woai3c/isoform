import AppKit
import Darwin
import Foundation
import ScreenCaptureKit

/// Long-running daemon: accepts connections on a Unix domain socket, reads
/// newline-delimited JSON requests, dispatches to handlers, writes JSON back.
///
/// The v1 concurrency model is intentionally simple: one client at a time is
/// typical (the Electron main process is the only caller). We run accept() on a
/// background queue so the main RunLoop stays free for AX callbacks.
final class Daemon {
    private let socketPath: String
    private let store = ElementStore()
    private var listenFd: Int32 = -1
    private var isShuttingDown = false
    private let acceptQueue = DispatchQueue(label: "com.yetone.alma.cu.accept")
    private let handleQueue = DispatchQueue(label: "com.yetone.alma.cu.handle")
    private let idleTimeoutSec: TimeInterval
    private var lastActivity: Date = Date()
    private var idleTimer: DispatchSourceTimer?

    /// Records the window bounds (in points) and screenshot dimensions (in
    /// pixels) for the last screenshot taken of each target app. Used to
    /// translate coord_space="screenshot" click coordinates to the screen
    /// points CGEvent expects. Keyed by pid.
    ///
    /// Codex's equivalent is `cursorPositionInScaledCoordinates` /
    /// `pointPixelScale` — the helper does the translation so the model
    /// can stop worrying about Retina factors or sips downsampling.
    private struct ScreenshotMapping {
        let windowOrigin: CGPoint   // screen points
        let windowSize: CGSize       // screen points
        let imageSize: CGSize         // screenshot pixels
        let windowId: CGWindowID      // so the lens can orderWindow:relativeTo:
    }
    private var lastMapping: [pid_t: ScreenshotMapping] = [:]
    private let mappingLock = NSLock()

    /// Fallback-indexing (P8): when AX tree is empty, get_app_state runs
    /// Vision OCR on the screenshot and registers each text region as a
    /// `v*` visual-ref. Clicks can target these refs the same way they
    /// target AX `e*` refs — the click handler resolves the bounds and
    /// converts the centre to screen points.
    private var visualRefs: [pid_t: [String: CGRect]] = [:]
    private let visualLock = NSLock()

    private struct WindowTarget {
        let pid: pid_t
        let windowId: CGWindowID
        let frame: CGRect
        let minimized: Bool
    }
    private var lastWindowTargets: [pid_t: WindowTarget] = [:]
    private let windowTargetLock = NSLock()

    private var activeBackgroundSession: BackgroundActivationSession?
    private var activeBackgroundPID: pid_t = 0
    private var activeBackgroundWindowID: CGWindowID = 0
    private let backgroundSessionLock = NSLock()
    private var frontmostObserver: NSObjectProtocol?
    private var terminationObserver: NSObjectProtocol?

    init(socketPath: String, idleTimeoutSec: TimeInterval = 600) {
        self.socketPath = socketPath
        self.idleTimeoutSec = idleTimeoutSec
    }

    func run() throws {
        try bindListener()
        startIdleTimer()
        startFrontmostMonitor()
        acceptQueue.async { [weak self] in self?.acceptLoop() }
        setupSignalHandlers()
        log("computer-use daemon listening on \(socketPath)")
        RunLoop.main.run()
    }

    // MARK: - Socket

    private func bindListener() throws {
        // Create the socket directory if needed.
        let dir = (socketPath as NSString).deletingLastPathComponent
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        // Remove stale socket file.
        unlink(socketPath)

        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw CUError.internalError("socket() failed: \(errnoMsg())") }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = Array(socketPath.utf8)
        if pathBytes.count >= MemoryLayout.size(ofValue: addr.sun_path) {
            close(fd)
            throw CUError.internalError("Socket path too long (macOS limit ~104 chars): \(socketPath)")
        }
        withUnsafeMutablePointer(to: &addr.sun_path) { ptr in
            ptr.withMemoryRebound(to: CChar.self, capacity: pathBytes.count + 1) { p in
                for i in 0..<pathBytes.count { p[i] = CChar(pathBytes[i]) }
                p[pathBytes.count] = 0
            }
        }
        let bindResult = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bindResult == 0 else {
            close(fd)
            throw CUError.internalError("bind() failed: \(errnoMsg())")
        }
        // Socket file only readable by owner.
        chmod(socketPath, 0o600)
        guard listen(fd, 4) == 0 else {
            close(fd)
            throw CUError.internalError("listen() failed: \(errnoMsg())")
        }
        self.listenFd = fd
    }

    private func acceptLoop() {
        while listenFd >= 0 {
            var caddr = sockaddr_un()
            var clen = socklen_t(MemoryLayout<sockaddr_un>.size)
            let cfd: Int32 = withUnsafeMutablePointer(to: &caddr) {
                $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { sp in
                    accept(listenFd, sp, &clen)
                }
            }
            if cfd < 0 {
                if errno == EINTR { continue }
                if !isShuttingDown { log("accept() failed: \(errnoMsg())") }
                break
            }
            handleQueue.async { [weak self] in self?.serve(clientFd: cfd) }
        }
    }

    // MARK: - Per-connection loop

    private func serve(clientFd fd: Int32) {
        defer { close(fd) }
        var buffer = Data()
        let chunkSize = 8192
        var tmp = [UInt8](repeating: 0, count: chunkSize)

        while true {
            let n = tmp.withUnsafeMutableBufferPointer { recv(fd, $0.baseAddress, chunkSize, 0) }
            if n == 0 { return }              // peer closed
            if n < 0 {
                if errno == EINTR { continue }
                log("recv() err: \(errnoMsg())")
                return
            }
            buffer.append(tmp, count: n)
            // Process any complete lines.
            while let nl = buffer.firstIndex(of: 0x0A) {
                let line = buffer.subdata(in: 0..<nl)
                buffer.removeSubrange(0...nl)
                if line.isEmpty { continue }
                let respData = processLine(line)
                var toSend = respData
                toSend.append(0x0A)
                if writeAll(fd, toSend) < 0 { return }
            }
        }
    }

    private func writeAll(_ fd: Int32, _ data: Data) -> Int {
        var written = 0
        data.withUnsafeBytes { raw in
            guard let base = raw.baseAddress else { return }
            while written < data.count {
                let n = send(fd, base.advanced(by: written), data.count - written, 0)
                if n < 0 {
                    if errno == EINTR { continue }
                    written = -1; return
                }
                written += n
            }
        }
        return written
    }

    // MARK: - Dispatch

    private func processLine(_ line: Data) -> Data {
        lastActivity = Date()
        let decoder = JSONDecoder()
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.withoutEscapingSlashes]

        let req: Request
        do {
            req = try decoder.decode(Request.self, from: line)
        } catch {
            let err = CUError.invalidArg("malformed JSON: \(error.localizedDescription)")
            return encodeResp(Response.failure("?", err), encoder: encoder)
        }

        let resp: Response
        do {
            let data = try dispatch(req)
            resp = Response.success(req.id, data)
        } catch let e as CUError {
            resp = Response.failure(req.id, e)
        } catch {
            resp = Response.failure(req.id, CUError.internalError(error.localizedDescription))
        }
        return encodeResp(resp, encoder: encoder)
    }

    private func encodeResp(_ r: Response, encoder: JSONEncoder) -> Data {
        (try? encoder.encode(r)) ?? Data("{\"ok\":false,\"error\":{\"code\":\"internal\",\"message\":\"encode failure\"}}".utf8)
    }

    /// Package-internal so `MCPServer` in the same binary can tools/call
    /// directly into the dispatcher without round-tripping through the
    /// unix socket. External callers still go through the socket.
    func dispatch(_ req: Request) throws -> Any? {
        // V2: for any action that targets a pid, prime the lens overlay
        // with the last-known window id so it orders above that specific
        // window rather than floating globally-topmost. Read-only commands
        // are exempt (they don't show the lens).
        let actionCmds: Set<String> = [
            "click", "perform_secondary_action", "drag",
            "type", "type_text", "press", "press_key",
            "scroll", "set_value",
        ]
        // Layer-1 cmds that target a bundle directly — include in the
        // forbidden-target gate too, so an agent can't route around AX
        // blocks by using `intent` to LSOpen a URL into System Settings.
        let layer1Cmds: Set<String> = ["intent", "open_url"]
        if actionCmds.contains(req.cmd) {
            primeLensTargetWindow(pid: resolveActionPid(req))
        }

        // Forbidden-target guard: refuse to drive privacy UI, login
        // prompts, Dock/LaunchPad, etc. `force_forbidden: true` in
        // args is the explicit opt-out. Runs BEFORE any handler so
        // there's no partial-state risk.
        let forceForbidden = req.args.bool("force_forbidden") ?? false
        if actionCmds.contains(req.cmd) {
            try ForbiddenTargets.check(
                pid: resolveActionPid(req),
                allowOverride: forceForbidden
            )
        } else if layer1Cmds.contains(req.cmd) {
            try ForbiddenTargets.check(
                bundleId: req.args.str("bundle"),
                allowOverride: forceForbidden
            )
        }

        switch req.cmd {
        case "ping":
            return ["pong": true, "version": version, "uptime_s": Int(ProcessInfo.processInfo.systemUptime)]

        case "permissions":
            return [
                "accessibility": AXBridge.isTrusted(prompt: false),
                // Best-effort probe: try listing shareable content; any failure
                // is assumed to mean screen recording is not granted.
                "screen_recording": SCPermissionProbe.check(),
            ]

        case "grant":
            // Fire the system AX prompt. Returns current state immediately; the
            // dialog is async so the agent should poll `permissions` after.
            let current = AXBridge.isTrusted(prompt: true)
            return ["prompted": true, "accessibility": current]

        case "apps":
            let apps = AXBridge.runningApps()
            return [
                "apps": apps.map { app in
                    [
                        "bundle": app.bundleId as Any?,
                        "name": app.name,
                        "pid": Int(app.pid),
                        "active": app.active,
                    ] as [String: Any?]
                }
            ]

        case "list_apps":
            // Codex-compatible: running + recently-used (14 days default) with
            // usage frequency. Agents are expected to use this instead of
            // `apps` when they need to pick between several candidate apps.
            //
            // We also attach each app's registered Layer-1 `intents` (from
            // AppCommandRegistry) so the agent can pick a flicker-free
            // URL-scheme dispatch over a Layer-2 click when one exists —
            // e.g. "launch" NetEase via `orpheus://` instead of clicking
            // its Dock icon.
            let days = req.args.int("days") ?? 14
            let apps = AppUsage.listApps(days: days)
            return [
                "apps": apps.map { a -> [String: Any?] in
                    let intents = a.bundleId.map { AppCommandRegistry.intents(for: $0) } ?? []
                    var entry: [String: Any?] = [
                        "bundle": a.bundleId as Any?,
                        "name": a.name,
                        "pid": a.pid.map { Int($0) } as Any?,
                        "active": a.active,
                        "running": a.running,
                        "path": a.path as Any?,
                        "use_count": a.useCount as Any?,
                        "last_used_at": a.lastUsedAt.map { ISO8601DateFormatter().string(from: $0) } as Any?,
                        "intents": intents.map { intent in
                            [
                                "name": intent.name,
                                "description": intent.description,
                                "required_params": intent.requiredParams,
                                "optional_params": intent.optionalParams,
                            ] as [String: Any?]
                        },
                    ]
                    // Inventory prefetch (OpenBridge startup-inventory pattern):
                    // for running apps, attach their open window titles so ONE
                    // list_apps call reveals whether the target content is
                    // ALREADY open (e.g. a window titled "收件箱 - Gmail"),
                    // turning "navigate/launch there" into "read the window
                    // that is already there". Best-effort and per-app isolated:
                    // a missing AX grant or a single app's AX failure is
                    // swallowed by try?, never breaking the list_apps result.
                    // AXBridge.windows sets a short AX messaging timeout so a
                    // hung app cannot stall the whole list_apps response.
                    if a.running,
                       let app = try? AXBridge.resolveApp(bundle: a.bundleId, pid: a.pid),
                       let wins = try? AXBridge.windows(of: app), !wins.isEmpty {
                        entry["windows"] = wins.map { w -> [String: Any?] in
                            [
                                "title": w.title,
                                "focused": w.focused,
                                "minimized": w.minimized,
                            ]
                        }
                    }
                    return entry
                }
            ]

        case "windows":
            let bundle = req.args.str("bundle")
            let pid = req.args.int("pid").map { pid_t($0) }
            let app = try AXBridge.resolveApp(bundle: bundle, pid: pid)
            let wins = try AXBridge.windows(of: app)
            return [
                "app": ["bundle": app.bundleIdentifier as Any?, "pid": Int(app.processIdentifier)],
                "windows": wins.map {
                    [
                        "id": Int($0.id),
                        "title": $0.title,
                        "bounds": ["x": $0.bounds.origin.x, "y": $0.bounds.origin.y,
                                   "w": $0.bounds.size.width, "h": $0.bounds.size.height],
                        "minimized": $0.minimized,
                        "focused": $0.focused,
                    ] as [String: Any?]
                },
            ]

        case "snap":
            let bundle = req.args.str("bundle")
            let pid = req.args.int("pid").map { pid_t($0) }
            let windowId = req.args.int("window_id").map { CGWindowID($0) }
            // depth default raised from 6 → 40 to match get_app_state.
            // See the get_app_state case below for the full rationale —
            // both code paths feed AXBridge.snapshot which truncates at
            // `depth`, and shallow walks miss Electron / CEF chat / IDE
            // content. interactiveOnly stays true here to preserve the
            // legacy lean snap payload for automation callers; consumers
            // who want full content should call get_app_state instead
            // (semantic split: snap = "find a click target", get_app_state
            // = "what does the app currently show").
            let depth = req.args.int("depth") ?? 40
            let interactiveOnly = req.args.bool("interactive_only") ?? true
            // Wake-up budget: how long to wait for an initially-empty AX
            // tree to populate after the AXEnhancedUserInterface flip.
            // Default 0 here (opt-in for `snap`); `get_app_state` is where
            // it defaults to 1.5s for agent convenience.
            let wakeup = req.args.double("wakeup_seconds") ?? 0.0
            let snap = try AXBridge.snapshot(
                bundle: bundle, pid: pid, windowId: windowId,
                depth: depth, interactiveOnly: interactiveOnly, store: store,
                wakeupSeconds: wakeup
            )
            return snapToDict(snap)

        case "launch_app":
            // Launch an app without bringing it to the foreground — matches
            // Codex's behaviour. If already running, returns the existing
            // instance. By default, the user's frontmost app is preserved.
            let bundle = try requireStr(req, "bundle")
            let activates = req.args.bool("activates") ?? false
            let app = try runAsync {
                try await AXBridge.launchApp(bundle: bundle, activates: activates)
            }
            // Give the app a brief moment to register an AX tree before we
            // return, so the next get_app_state call doesn't race.
            Thread.sleep(forTimeInterval: 0.2)
            return [
                "launched": true,
                "activates": activates,
                "app": [
                    "bundle": app.bundleIdentifier as Any?,
                    "name": app.localizedName as Any?,
                    "pid": Int(app.processIdentifier),
                ] as [String: Any?],
            ]

        case "open_url":
            // Layer-1 primitive: open any URL via LaunchServices without
            // activating the handling app. Use this when you already know
            // the exact URL to send (mailto, https, app-scheme). For
            // known-app shortcuts, prefer `intent` which looks the URL
            // template up from the registry.
            //
            // Optional `bundle` forces the URL to a specific handling app
            // (useful when e.g. you want an https URL opened in Safari
            // specifically, not the user's default browser).
            let urlStr = try requireStr(req, "url")
            guard let u = URL(string: urlStr) else {
                throw CUError.invalidArg("invalid url: \(urlStr)")
            }
            let bundle = req.args.str("bundle")
            try AppCommandRegistry.openQuietly(url: u, preferredBundleId: bundle)
            return [
                "opened": urlStr,
                "via": bundle as Any?,
            ] as [String: Any?]

        case "intent":
            // Layer-1 primary: look up a registered intent for a given
            // bundle and dispatch it. Zero flicker, zero synthesized
            // input — the target app receives the URL via its own URL
            // scheme handler. Call `list_apps` to see which intents are
            // registered for which bundles.
            //
            // Args:
            //   bundle: string (required) — bundle id, e.g. "com.netease.163music"
            //   intent: string (required) — name from list_apps, e.g. "play_song"
            //   params: object (optional) — per-intent parameter map;
            //           required keys are listed in `list_apps`.
            let targetBundle = try requireStr(req, "bundle")
            let intentName = try requireStr(req, "intent")
            // `params` is a nested dict. AnyCodable decodes nested objects
            // as `[String: Any?]`; we flatten to `[String: String]` since
            // URL templates only need string params.
            var params: [String: String] = [:]
            if let raw = req.args?["params"]?.value as? [String: Any?] {
                for (k, v) in raw {
                    switch v {
                    case let s as String: params[k] = s
                    case let n as Int: params[k] = String(n)
                    case let n as Double: params[k] = String(n)
                    case let b as Bool: params[k] = b ? "true" : "false"
                    case .none: continue
                    case .some(let any): params[k] = String(describing: any)
                    }
                }
            }
            let opened = try AppCommandRegistry.dispatch(
                bundleId: targetBundle,
                intent: intentName,
                params: params
            )
            return [
                "bundle": targetBundle,
                "intent": intentName,
                "url": opened.absoluteString,
            ]

        case "get_app_state":
            // Codex-compatible combo: AX snapshot + window screenshot in one
            // round trip. This is what agents should call at the start of
            // every turn. On apps with no AX tree (Qt / custom-drawn) the
            // elements list is empty but the screenshot is still captured so
            // the model can pick coordinates.
            //
            // Auto-launch: if `bundle` is given and the app isn't running,
            // launch it in the BACKGROUND (activates=false). This mirrors
            // Codex's "Start an app use session if needed" behaviour and is
            // the key reason Alma previously foregrounded apps — the skill
            // had no launch path, so models resorted to `open -b` which
            // activates by default.
            let bundle = req.args.str("bundle")
            let pid = req.args.int("pid").map { pid_t($0) }
            let windowId = req.args.int("window_id").map { CGWindowID($0) }
            // depth default raised from 6 → 40. Modern Electron / CEF
            // apps nest content far below the original limit: Feishu /
            // Lark wraps each rendered chat message ~28 levels under
            // AXWebArea (window → BrowserView → MultiWebView → AXWebArea
            // → chat-container × ~15 → message-group → sender/body →
            // text leaf); VSCode / Cursor / Notion / Slack are in the
            // same depth class. depth=6 hit only the WebView outer
            // shell, ax_status came back "populated" because chrome
            // existed, but the caller saw no content and concluded the
            // tree was empty. depth=40 mirrors what
            // activitymon/axtree_tree_darwin.go now uses (TREE_MAX_DEPTH
            // bumped from 25 → 40 for the same Lark-reading reason).
            let depth = req.args.int("depth") ?? 40
            // interactive_only default flipped from true → false for
            // get_app_state specifically (not for `snap`). The natural
            // semantic of "get app state" is "what does this app
            // currently show," which has to include AXStaticText nodes
            // (every chat message, every email body, every code line).
            // The original true default was inherited from cua-driver's
            // automation-first design — fine when the caller wants a
            // short list of clickable element_index values, wrong when
            // the caller asks "who did the user last chat with in
            // <group>?". Callers wanting the legacy lean payload can
            // still pass interactive_only=true explicitly. The
            // companion `snap` case above keeps the old default for
            // backwards compatibility with automation workflows.
            let interactiveOnly = req.args.bool("interactive_only") ?? false
            let maxW = req.args.int("screenshot_max_width") ?? 1600
            let wantShot = req.args.bool("include_screenshot") ?? false
            let includeElementURLs = req.args.bool("include_element_urls") ?? false
            if includeElementURLs {
                let expected = ProcessInfo.processInfo.environment["YANSU_CUA_SOURCE_LOCATOR_SECRET"] ?? ""
                let supplied = req.args.str("source_locator_secret") ?? ""
                guard expected.count >= 32, supplied == expected else {
                    throw CUError.invalidArg("element URL extraction requires a private source-locator capability")
                }
            }
            let autoLaunch = req.args.bool("auto_launch") ?? true
            // Default wake-up budget for CEF/Electron apps whose AX tree
            // streams 100-500ms after the AXEnhancedUserInterface flip.
            // 1.5s covers the common case (Slack, Discord, VS Code,
            // Notion, 飞书, 钉钉, 微信); agents can override with
            // `wakeup_seconds` — pass 0 to skip and go straight to OCR
            // fallback for apps known to be AX-empty (网易云音乐).
            let wakeup = req.args.double("wakeup_seconds") ?? 1.5

            if autoLaunch, let b = bundle, pid == nil,
               NSRunningApplication.runningApplications(withBundleIdentifier: b).isEmpty,
               NSWorkspace.shared.runningApplications.first(where: { $0.localizedName == b }) == nil
            {
                // Launch in background WITH a PreserveFrontmostWatcher so
                // the CEF/Electron startup's self-activation
                // ([NSApp activateIgnoringOtherApps:] in its
                // applicationDidFinishLaunching) doesn't visibly bring
                // it forward. The watcher snaps the user's real
                // frontmost back the instant the target tries to take
                // focus — before Dock / menu bar / window chrome
                // redraw. Then waitUntilReady polls content stability
                // in its normal way.
                if let launched = launchWithFrontmostPreserved(bundle: b) {
                    waitUntilReady(app: launched, timeout: 10.0)
                }
            }

            var snap = try AXBridge.snapshot(
                bundle: bundle, pid: pid, windowId: windowId,
                depth: depth, interactiveOnly: interactiveOnly, store: store,
                wakeupSeconds: wakeup,
                includeElementURLs: includeElementURLs
            )
            if prepareBackgroundActivationIfNeeded(for: snap) {
                snap = (try? AXBridge.snapshot(
                    bundle: bundle, pid: pid, windowId: snap.windowId ?? windowId,
                    depth: depth, interactiveOnly: interactiveOnly, store: store,
                    wakeupSeconds: wakeup,
                    includeElementURLs: includeElementURLs
                )) ?? snap
            }
            recordWindowTarget(from: snap)
            var out = snapToDict(snap)

            // Enumerate menu-bar keyboard shortcuts (Codex-parity):
            // the model can prefer press_key over clicking menus when a
            // shortcut exists. Cheap — bounded at 200 items.
            let shortcuts = MenuShortcuts.collect(for: snap.app)
            if !shortcuts.isEmpty {
                out["menu_shortcuts"] = shortcuts.map {
                    ["path": $0.path, "combo": $0.combo] as [String: Any?]
                }
            }
            if wantShot, let wid = snap.windowId {
                let outPath = req.args.str("screenshot_path")
                    ?? "/tmp/alma-cu-state-\(Int(Date().timeIntervalSince1970 * 1000)).jpg"
                do {
                    let minimized = ((try? AXBridge.windows(of: snap.app)) ?? [])
                        .first(where: { $0.id == wid })?.minimized ?? false
                    let shot = try captureWindowForState(
                        windowId: wid,
                        outputPath: outPath,
                        maxWidth: maxW,
                        preferBackingStore: minimized
                    )
                    out["screenshot"] = [
                        "path": shot.path,
                        "width": shot.width,
                        "height": shot.height,
                    ] as [String: Any?]
                    // Record the window-point / image-pixel mapping so
                    // subsequent actions can accept coord_space="screenshot".
                    if let bounds = snap.windowBounds, let wid = snap.windowId {
                        mappingLock.lock()
                        lastMapping[snap.app.processIdentifier] = ScreenshotMapping(
                            windowOrigin: bounds.origin,
                            windowSize: bounds.size,
                            imageSize: CGSize(width: shot.width, height: shot.height),
                            windowId: wid
                        )
                        mappingLock.unlock()
                    }
                    // Fallback visual indexing: if the AX tree is empty /
                    // useless, run OCR and expose each text region as a
                    // `v*` ref the model can click by. Only triggered when
                    // AX yielded 0 or 1 (just the window shell) elements,
                    // to avoid double-paying cost on rich AX apps.
                    if snap.elements.count <= 1 {
                        let regions = VisualIndex.index(imagePath: shot.path, maxResults: 80)
                        if !regions.isEmpty {
                            visualLock.lock()
                            var bounds: [String: CGRect] = [:]
                            for r in regions { bounds[r.ref] = r.bounds }
                            visualRefs[snap.app.processIdentifier] = bounds
                            visualLock.unlock()
                            out["visual_refs"] = regions.map { r in
                                [
                                    "ref": r.ref,
                                    "text": r.text,
                                    "bounds": [
                                        "x": Double(r.bounds.origin.x),
                                        "y": Double(r.bounds.origin.y),
                                        "w": Double(r.bounds.size.width),
                                        "h": Double(r.bounds.size.height),
                                    ] as [String: Any?],
                                    "confidence": Double(r.confidence),
                                ] as [String: Any?]
                            }
                        }
                    }
                } catch {
                    // Screenshot failure shouldn't break the whole call —
                    // surface it alongside the tree so the agent can decide.
                    out["screenshot_error"] = String(describing: error)
                }
            }
            return out

        case "click":
            // Three modes:
            //   • by ref (existing behaviour)
            //   • by pixel (x, y) with optional pid — for apps with no usable
            //     AX tree, matches Codex's `click` coordinate form
            let button = EventDispatch.MouseButton(rawValue: req.args.str("button") ?? "left") ?? .left
            let clickCount = req.args.int("click_count") ?? 1
            let show = req.args.bool("show_cursor") ?? true
            let strategy = AXBridge.ClickStrategy(rawValue: req.args.str("strategy") ?? "auto") ?? .auto
            let settleMs = req.args.int("settle_ms") ?? 600

            // Opt-in foreground activation for actions that need the
            // target's Chromium/Electron renderer to be active to
            // commit (channel switch, modal open, page nav). VISIBLE
            // flicker — target flashes frontmost for ~settleMs+150ms
            // then user's prior app comes back. Default false; agents
            // should only set true after a bgclick provably failed to
            // commit (e.g. live_title still says the old context).
            let activate = req.args.bool("activate") ?? false
            let wantsProcessActivation = shouldUseProcessActivation(req, explicitActivate: activate)

            // Daemon-level guard: refuse clicks on known Chromium/
            // Electron bundles unless activate:true is set. The
            // bgclick path (see EventDispatch.click) ORs maskCommand
            // into the CGEvent flags when the target isn't frontmost
            // — that bit lets the event bypass WindowServer's filter,
            // but Slack/Discord/Notion's click handlers ALSO read it
            // and interpret cmd+click on a sidebar item as "open this
            // in a new window." Concrete user-visible symptom: we
            // tried to switch channels in Slack and ended up with a
            // second Slack window stranded on the user's desktop. The
            // guard forces the caller into the activate:true path,
            // where targetIsActive=true skips the maskCommand
            // insertion in makeClickEvent and the click lands cleanly.
            //
            // Escape hatch: pass `force_electron_shortcut: true` to
            // bypass (same flag press_key uses; intentionally not
            // documented in the public schema so the LLM can't
            // route around the protection).
            if !activate, !wantsProcessActivation, !(req.args.bool("force_electron_shortcut") ?? false),
               let pid = resolveActionPid(req),
               let bundleId = bundleIdentifier(for: pid),
               bundleIsElectron(bundleId),
               (NSRunningApplication(processIdentifier: pid)?.isActive ?? false) == false
            {
                throw CUError.electronShortcutGated(
                    bundle: bundleId,
                    op: "click",
                    detail: "the bgclick path inserts the cmd modifier into the event to bypass WindowServer's frontmost filter, but Chromium-shell apps interpret cmd+click on sidebar items as \"open in new window\" — would strand a duplicate window on the user's desktop."
                )
            }

            var savedFront: pid_t = 0
            if activate, let pid = resolveActionPid(req) {
                savedFront = activateForeground(pid: pid)
            }
            defer { if savedFront > 0 { restoreForeground(pid: savedFront) } }
            if req.args.double("x") != nil {
                guard let pid = resolveActionPid(req) else {
                    throw CUError.invalidArg("click by pixel requires either `pid` or `bundle` of a running app — without a target we'd have to post globally and move the real cursor.")
                }
                guard let p = resolvePoint(req, pid: pid) else {
                    throw CUError.invalidArg("click --pixel requires both x and y")
                }
                let settleTok = settlePrelude(pid: pid)
                if wantsProcessActivation,
                   let target = try prepareBackgroundActivationForAction(pid: pid, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
                {
                    AXBridge.clickAtPointProcessActivated(
                        at: p, pid: pid,
                        windowNumber: Int(target.windowId),
                        windowFrame: target.frame,
                        button: button,
                        clickCount: clickCount,
                        showCursor: show
                    )
                    holdBackgroundActivation()
                } else {
                    try AXBridge.clickAtPoint(
                        at: p, pid: pid,
                        button: button, clickCount: clickCount, showCursor: show
                    )
                }
                let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
                return mergeSettleResult(settleResult, into: [
                    "clicked_at": ["x": p.x, "y": p.y],
                    "button": button.rawValue,
                    "clicks": clickCount,
                    "coord_space": req.args.str("coord_space") ?? "screenshot",
                ])
            }
            let ref = try requireStr(req, "ref")
            // Visual refs (v*) resolve to screenshot-pixel bounds from the
            // last get_app_state. Route through clickAtPoint so the focus
            // stealth + coord translation apply consistently.
            if ref.hasPrefix("v") {
                guard let p = resolveActionPid(req) else {
                    throw CUError.invalidArg("visual refs (v*) require `pid` or `bundle`")
                }
                visualLock.lock()
                let bounds = visualRefs[p]?[ref]
                visualLock.unlock()
                guard let b = bounds else {
                    throw CUError.elementNotFound(ref)
                }
                guard let translated = translateScreenshotCoord(pid: p, x: Double(b.midX), y: Double(b.midY)) else {
                    throw CUError.internalError("no screenshot mapping for pid \(p); run get_app_state first")
                }
                let settleTok = settlePrelude(pid: p)
                if wantsProcessActivation,
                   let target = try prepareBackgroundActivationForAction(pid: p, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
                {
                    AXBridge.clickAtPointProcessActivated(
                        at: CGPoint(x: translated.0, y: translated.1), pid: p,
                        windowNumber: Int(target.windowId),
                        windowFrame: target.frame,
                        button: button,
                        clickCount: clickCount,
                        showCursor: show
                    )
                    holdBackgroundActivation()
                } else {
                    try AXBridge.clickAtPoint(
                        at: CGPoint(x: translated.0, y: translated.1), pid: p,
                        button: button, clickCount: clickCount, showCursor: show
                    )
                }
                let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
                return mergeSettleResult(settleResult, into: [
                    "clicked": ref,
                    "clicked_at": ["x": translated.0, "y": translated.1],
                    "button": button.rawValue,
                    "clicks": clickCount,
                ])
            }
            let settleTok = settlePrelude(pid: resolveSettlePid(req, ref: ref))
            if let pid = resolveSettlePid(req, ref: ref),
               shouldUseProcessActivation(req, explicitActivate: activate, pid: pid),
               let target = try prepareBackgroundActivationForAction(pid: pid, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
            {
                try AXBridge.clickProcessActivated(
                    ref: ref, store: store,
                    windowNumber: Int(target.windowId),
                    windowFrame: target.frame,
                    strategy: strategy,
                    button: button,
                    clickCount: clickCount,
                    preferPhysical: bundleIsElectron(bundleIdentifier(for: pid)),
                    showCursor: show
                )
                holdBackgroundActivation()
            } else {
                try AXBridge.click(
                    ref: ref, store: store, strategy: strategy,
                    button: button, clickCount: clickCount, showCursor: show
                )
            }
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: [
                "clicked": ref, "strategy": strategy.rawValue,
                "button": button.rawValue, "clicks": clickCount,
            ])

        case "type":
            // Legacy "type into a ref" — kept for backward compat.
            let ref = try requireStr(req, "ref")
            let text = try requireStr(req, "text")
            let replace = req.args.bool("replace") ?? true
            let show = req.args.bool("show_cursor") ?? true
            let settleMs = req.args.int("settle_ms") ?? 600
            let settleTok = settlePrelude(pid: resolveSettlePid(req, ref: ref))
            if let pid = resolveSettlePid(req, ref: ref),
               shouldUseProcessActivation(req, explicitActivate: false, pid: pid)
            {
                _ = try prepareBackgroundActivationForAction(pid: pid, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
            }
            try AXBridge.type(ref: ref, text: text, replace: replace, store: store, showCursor: show)
            holdBackgroundActivation()
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: [
                "typed": ref,
                "length": text.count,
            ])

        case "type_text":
            // Codex-compatible: keyboard text entry without targeting a ref.
            // The text goes to whatever has keyboard focus in the target app
            // (caller is responsible for focusing first via click or AX).
            let text = try requireStr(req, "text")
            let inputMethod = (req.args.str("input_method") ?? "auto").lowercased()
            guard ["auto", "physical", "keyboard", "unicode", "cg_event", "ax"].contains(inputMethod) else {
                throw CUError.invalidArg("input_method must be auto|physical|unicode|ax")
            }
            let settleMs = req.args.int("settle_ms") ?? 600
            let show = req.args.bool("show_cursor") ?? true
            // Opt-in foreground activation — see "click" case rationale.
            // For type_text the common use is filling a Quick Switcher
            // / Spotlight-style search input: if the upstream cmd+K
            // (press_key) used activate:true, this type_text inherits
            // the active state and savedFront=0 here.
            let activate = req.args.bool("activate") ?? false
            let wantsProcessActivation = shouldUseProcessActivation(req, explicitActivate: activate)
            var savedFront: pid_t = 0
            if activate, let pid = resolveActionPid(req) {
                savedFront = activateForeground(pid: pid)
            }
            defer { if savedFront > 0 { restoreForeground(pid: savedFront) } }

            if let ref = req.args.str("ref"), inputMethod == "auto" || inputMethod == "ax" {
                let refPid = resolveSettlePid(req, ref: ref)
                // Why this branch is not a simple AX write: AX text fields on
                // Chromium/CEF apps (Feishu, Slack, Discord, VS Code, Chrome-
                // shell, Tauri/WKWebView wrappers, …) are READ-ONLY mirrors of
                // the DOM. Setting AXValue/AXSelectedText never reaches the
                // renderer, so type_text(ref) silently no-ops while reporting
                // success. We handle this in two layers so it generalises
                // beyond a hardcoded app list:
                //   1. Known-Electron fast path: skip the doomed AX write and
                //      go straight to focus + synthesized keys.
                //   2. write-then-verify fallback for everything else: do the
                //      AX write, read the value back, and if it didn't land
                //      (the read-only-mirror signal) fall back to synth keys.
                //      This catches CEF/WebView apps not on the Electron list.
                // input_method=ax is an explicit caller override: raw AX write,
                // no verification, no fallback.
                let forceAXOnly = inputMethod == "ax"
                let isElectron = refPid.map { bundleIsElectron(bundleIdentifier(for: $0)) } ?? false
                let settleTok = settlePrelude(pid: refPid)
                let useProcessActivation = refPid.map {
                    shouldUseProcessActivation(req, explicitActivate: activate, pid: $0)
                } ?? false
                if useProcessActivation, let pid = refPid {
                    _ = try prepareBackgroundActivationForAction(pid: pid, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
                }

                // Focus the element, then deliver synthesized key events via
                // the process-activated or window-level path as appropriate.
                func deliverSynthKeys(pid: pid_t) throws {
                    try AXBridge.focusElement(ref: ref, store: store, showCursor: show)
                    if useProcessActivation {
                        try EventDispatch.typeTextProcessActivated(text, pid: pid)
                    } else {
                        try EventDispatch.typeText(text, pid: pid)
                    }
                }

                var method = "ax_ref"
                if forceAXOnly {
                    try AXBridge.type(ref: ref, text: text, replace: false, store: store, showCursor: show)
                } else if isElectron, let pid = refPid {
                    try deliverSynthKeys(pid: pid)
                    method = "synth_keys_electron"
                } else {
                    // write-then-verify: try the AX write, then confirm the
                    // value actually changed to include our text. If not (a
                    // read-only AX mirror) and we have a pid, retry via keys.
                    let before = AXBridge.currentValue(ref: ref, store: store) ?? ""
                    try AXBridge.type(ref: ref, text: text, replace: false, store: store, showCursor: show)
                    usleep(30_000)
                    let after = AXBridge.currentValue(ref: ref, store: store) ?? ""
                    if after != before && after.contains(text) {
                        method = "ax_ref"
                    } else if let pid = refPid {
                        try deliverSynthKeys(pid: pid)
                        method = "ax_ref_verify_fallback"
                    }
                }
                holdBackgroundActivation()
                let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
                return mergeSettleResult(settleResult, into: [
                    "typed": true, "length": text.count, "method": method,
                ])
            }

            guard let pid = resolveActionPid(req) else {
                throw CUError.invalidArg("type_text requires `pid`, `bundle`, or `ref`.")
            }

            let settleTok = settlePrelude(pid: pid)
            if inputMethod == "ax" {
                throw CUError.invalidArg("input_method=ax requires `ref`.")
            }
            if ["unicode", "cg_event"].contains(inputMethod) {
                if wantsProcessActivation {
                    _ = try prepareBackgroundActivationForAction(pid: pid, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
                    try EventDispatch.typeUnicodeTextProcessActivated(text, pid: pid)
                    holdBackgroundActivation()
                } else {
                    try EventDispatch.typeUnicodeText(text, pid: pid)
                }
                let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
                return mergeSettleResult(settleResult, into: [
                    "typed": true, "length": text.count, "method": "unicode_events",
                ])
            }
            if ["physical", "keyboard"].contains(inputMethod) {
                if wantsProcessActivation {
                    _ = try prepareBackgroundActivationForAction(pid: pid, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
                    try EventDispatch.typePhysicalTextProcessActivated(text, pid: pid)
                    holdBackgroundActivation()
                } else {
                    try EventDispatch.typePhysicalText(text, pid: pid)
                }
                let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
                return mergeSettleResult(settleResult, into: [
                    "typed": true, "length": text.count, "method": "physical_key_events",
                ])
            }

            if wantsProcessActivation {
                _ = try prepareBackgroundActivationForAction(pid: pid, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
                try EventDispatch.typeTextProcessActivated(text, pid: pid)
                holdBackgroundActivation()
            } else {
                try EventDispatch.typeText(text, pid: pid)
            }
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: [
                "typed": true, "length": text.count, "method": "auto_key_events",
            ])

        case "press":
            // Legacy ref-scoped AX "press Enter/Escape/Increment" — unchanged.
            let ref = try requireStr(req, "ref")
            let key = try requireStr(req, "key")
            let show = req.args.bool("show_cursor") ?? true
            let settleMs = req.args.int("settle_ms") ?? 600
            let settleTok = settlePrelude(pid: resolveSettlePid(req, ref: ref))
            try AXBridge.press(ref: ref, key: key, store: store, showCursor: show)
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: ["pressed": key])

        case "press_key":
            // Codex-compatible: xdotool-style combo ("cmd+s", "ctrl+shift+t",
            // "Return", "Escape"). Dispatched via CGEvent; supports any
            // modifier combination — the thing Alma's v1 explicitly didn't.
            let combo = try requireStr(req, "key")
            guard let pid = resolveActionPid(req) else {
                throw CUError.invalidArg("press_key requires `pid` or `bundle` of a running app.")
            }
            let settleMs = req.args.int("settle_ms") ?? 600
            // Opt-in foreground activation — see "click" case for the
            // full rationale. The press_key path is most often paired
            // with click on Electron apps (click toolbar/switcher →
            // press cmd+K → type → press Enter); if the upstream click
            // already activated, this press_key inherits the active
            // state and `activate:true` here is a no-op (savedFront=0).
            let activate = req.args.bool("activate") ?? false
            let wantsProcessActivation = shouldUseProcessActivation(req, explicitActivate: activate)

            // Daemon-level guard: refuse cmd-modified hotkeys on
            // known Chromium/Electron bundles unless activate:true is
            // set. Chromium strips the cmd modifier on backgrounded
            // targets, polluting the focused composer with the bare
            // letter (cmd+K → "k" in Slack's draft box). The hint in
            // the error tells the caller exactly how to recover
            // (`activate: true` or click the toolbar Switcher button).
            //
            // Escape hatch: pass `force_electron_shortcut: true` to
            // bypass this guard. Reserved for legitimate uses we
            // haven't anticipated; not advertised in the public
            // schema.
            if !activate, !wantsProcessActivation, comboHasCmdModifier(combo),
               !(req.args.bool("force_electron_shortcut") ?? false)
            {
                if let bundleId = bundleIdentifier(for: pid),
                   bundleIsElectron(bundleId)
                {
                    throw CUError.electronShortcutGated(
                        bundle: bundleId,
                        op: "press_key \"\(combo)\"",
                        detail: "Chromium strips the cmd modifier on backgrounded targets; the bare key (e.g. \"k\" from cmd+K) lands in the focused text composer as draft text — user-visible data pollution."
                    )
                }
            }

            var savedFront: pid_t = 0
            if activate {
                savedFront = activateForeground(pid: pid)
            }
            defer { if savedFront > 0 { restoreForeground(pid: savedFront) } }
            let settleTok = settlePrelude(pid: pid)
            if wantsProcessActivation {
                _ = try prepareBackgroundActivationForAction(pid: pid, requestedWindowId: req.args.int("window_id").map { CGWindowID($0) })
                try EventDispatch.pressKeyProcessActivated(combo, pid: pid)
                holdBackgroundActivation()
            } else {
                try EventDispatch.pressKey(combo, pid: pid)
            }
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: ["pressed": combo])

        case "perform_secondary_action":
            // Codex: right-click / AXShowMenu. Accepts either a ref or a point.
            let show = req.args.bool("show_cursor") ?? true
            let settleMs = req.args.int("settle_ms") ?? 600
            if req.args.double("x") != nil {
                guard let pid = resolveActionPid(req) else {
                    throw CUError.invalidArg("perform_secondary_action by pixel requires `pid` or `bundle`.")
                }
                guard let p = resolvePoint(req, pid: pid) else {
                    throw CUError.invalidArg("perform_secondary_action --pixel requires both x and y")
                }
                let settleTok = settlePrelude(pid: pid)
                try AXBridge.clickAtPoint(
                    at: p, pid: pid,
                    button: .right, clickCount: 1, showCursor: show
                )
                let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
                return mergeSettleResult(settleResult, into: [
                    "opened_menu_at": ["x": p.x, "y": p.y],
                ])
            }
            let ref = try requireStr(req, "ref")
            let settleTok = settlePrelude(pid: resolveSettlePid(req, ref: ref))
            try AXBridge.performSecondaryAction(ref: ref, store: store, showCursor: show)
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: ["opened_menu": ref])

        case "drag":
            let fx = try requireDouble(req, "start_x")
            let fy = try requireDouble(req, "start_y")
            let tx = try requireDouble(req, "end_x")
            let ty = try requireDouble(req, "end_y")
            guard let pid = resolveActionPid(req) else {
                throw CUError.invalidArg("drag requires `pid` or `bundle` of a running app.")
            }
            let button = EventDispatch.MouseButton(rawValue: req.args.str("button") ?? "left") ?? .left
            let show = req.args.bool("show_cursor") ?? true
            // Apply coord_space translation to BOTH endpoints consistently.
            let space = req.args.str("coord_space")
            let fromP: CGPoint = {
                if space == "screen" { return CGPoint(x: fx, y: fy) }
                if let t = translateScreenshotCoord(pid: pid, x: fx, y: fy) {
                    return CGPoint(x: t.0, y: t.1)
                }
                return CGPoint(x: fx, y: fy)
            }()
            let toP: CGPoint = {
                if space == "screen" { return CGPoint(x: tx, y: ty) }
                if let t = translateScreenshotCoord(pid: pid, x: tx, y: ty) {
                    return CGPoint(x: t.0, y: t.1)
                }
                return CGPoint(x: tx, y: ty)
            }()
            let settleMs = req.args.int("settle_ms") ?? 600
            let settleTok = settlePrelude(pid: pid)
            try AXBridge.drag(
                from: fromP, to: toP,
                pid: pid, button: button, showCursor: show
            )
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: [
                "dragged": [
                    "from": ["x": fromP.x, "y": fromP.y],
                    "to": ["x": toP.x, "y": toP.y],
                    "button": button.rawValue,
                    "coord_space": space ?? "screenshot",
                ],
            ])

        case "set_value":
            let ref = try requireStr(req, "ref")
            guard let value = req.args.any("value") else {
                throw CUError.invalidArg("missing \"value\"")
            }
            let settleMs = req.args.int("settle_ms") ?? 600
            let settleTok = settlePrelude(pid: resolveSettlePid(req, ref: ref))
            try AXBridge.setValue(ref: ref, value: value, store: store)
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: ["set": ref])

        case "scroll":
            let ref = try requireStr(req, "ref")
            let dir = try requireStr(req, "direction")
            let pages = req.args.int("pages") ?? 1
            let show = req.args.bool("show_cursor") ?? true
            let settleMs = req.args.int("settle_ms") ?? 600
            let settleTok = settlePrelude(pid: resolveSettlePid(req, ref: ref))
            try AXBridge.scroll(ref: ref, direction: dir, pages: pages, store: store, showCursor: show)
            let settleResult = settlePostlude(settleTok, budgetMs: settleMs)
            return mergeSettleResult(settleResult, into: [
                "scrolled": dir, "pages": pages,
            ])

        case "lens":
            // Runtime toggles — useful for the CLI and scripted runs.
            // Optional `enabled`: master on/off for the whole lens overlay.
            // Optional `fog`: toggle the V5 radial-vignette fog backdrop.
            if let on = req.args.bool("enabled") {
                LensOverlay.shared.setEnabled(on)
            }
            if let fog = req.args.bool("fog") {
                LensOverlay.shared.setFogEnabled(fog)
            }
            return [
                "enabled": LensOverlay.shared.isEnabled,
                "fog": LensOverlay.shared.isFogEnabled,
            ]

        case "pip_open":
            // Resolve to a single CGWindowID. Three input shapes:
            //   { window_id: N }                 — explicit
            //   { pid: N, title?: "…" }          — pick window by pid (+ optional title match)
            //   { bundle: "com.…", title?: "…" } — resolve bundle → pid → window
            let explicitWid = req.args.int("window_id").map { CGWindowID($0) }
            let wantedTitle = req.args.str("title")
            let resolved: (wid: CGWindowID, pid: pid_t?, title: String) = try {
                if let wid = explicitWid {
                    // Best-effort enrichment so status reports are useful.
                    let providedPid = req.args.int("pid").map { pid_t($0) }
                    return (wid, providedPid, wantedTitle ?? "")
                }
                let bundle = req.args.str("bundle")
                let pid = req.args.int("pid").map { pid_t($0) }
                let app = try AXBridge.resolveApp(bundle: bundle, pid: pid)
                let wins = (try? AXBridge.windows(of: app)) ?? []
                guard !wins.isEmpty else {
                    throw CUError.internalError("App has no windows to mirror.")
                }
                let pick: AXBridge.WindowInfo = {
                    if let t = wantedTitle, !t.isEmpty,
                       let m = wins.first(where: { $0.title == t })
                        ?? wins.first(where: { $0.title.contains(t) })
                    {
                        return m
                    }
                    return wins.first(where: { $0.focused }) ?? wins[0]
                }()
                return (pick.id, app.processIdentifier, pick.title)
            }()
            // Optional preferred size.
            var preferred: NSSize?
            if let w = req.args.double("width"), let h = req.args.double("height"), w > 0, h > 0 {
                preferred = NSSize(width: w, height: h)
            }
            let status = try PIPManager.shared.open(
                windowId: resolved.wid, pid: resolved.pid,
                title: resolved.title.isEmpty ? nil : resolved.title,
                preferredSize: preferred
            )
            return ["opened": true, "session": status]

        case "pip_close":
            // Either { window_id: N } to close one, or { all: true } to close them all.
            if req.args.bool("all") == true {
                PIPManager.shared.closeAll()
                return ["closed": "all"]
            }
            guard let widInt = req.args.int("window_id") else {
                throw CUError.invalidArg("missing \"window_id\" (or pass \"all\": true)")
            }
            let wid = CGWindowID(widInt)
            let did = PIPManager.shared.close(windowId: wid)
            return ["closed": did, "window_id": Int(wid)]

        case "pip_status":
            return [
                "sessions": PIPManager.shared.statusList(),
                "armed": PIPMinimizeArmer.shared.statusList(),
            ]

        case "pip_arm":
            // Subsystem-3 minimise-hijack: register an AX observer on the
            // given window so that when the user clicks its minimise
            // button, Alma auto-opens a PIP mirror. Same argument shape
            // as pip_open: either { window_id, pid? } or
            // { bundle | pid, title? } to resolve.
            let explicitWid = req.args.int("window_id").map { CGWindowID($0) }
            let wantedTitle = req.args.str("title")
            let resolved: (wid: CGWindowID, pid: pid_t, title: String) = try {
                if let wid = explicitWid, let p = req.args.int("pid").map({ pid_t($0) }) {
                    return (wid, p, wantedTitle ?? "")
                }
                let bundle = req.args.str("bundle")
                let pid = req.args.int("pid").map { pid_t($0) }
                let app = try AXBridge.resolveApp(bundle: bundle, pid: pid)
                let wins = (try? AXBridge.windows(of: app)) ?? []
                guard !wins.isEmpty else {
                    throw CUError.internalError("App has no windows to arm.")
                }
                let pick: AXBridge.WindowInfo = {
                    if let t = wantedTitle, !t.isEmpty,
                       let m = wins.first(where: { $0.title == t })
                        ?? wins.first(where: { $0.title.contains(t) })
                    {
                        return m
                    }
                    return wins.first(where: { $0.focused }) ?? wins[0]
                }()
                return (pick.id, app.processIdentifier, pick.title)
            }()
            try PIPMinimizeArmer.shared.arm(
                windowId: resolved.wid, pid: resolved.pid,
                title: resolved.title.isEmpty ? nil : resolved.title
            )
            return ["armed": ["window_id": Int(resolved.wid), "pid": Int(resolved.pid), "title": resolved.title]]

        case "pip_disarm":
            if req.args.bool("all") == true {
                PIPMinimizeArmer.shared.disarmAll()
                return ["disarmed": "all"]
            }
            guard let widInt = req.args.int("window_id") else {
                throw CUError.invalidArg("missing \"window_id\" (or pass \"all\": true)")
            }
            let did = PIPMinimizeArmer.shared.disarm(windowId: CGWindowID(widInt))
            return ["disarmed": did, "window_id": widInt]

        case "mcp_start":
            // Optional `port` — when absent the OS assigns an ephemeral
            // free port. Optional `token_path` — useful for CI or when
            // the caller wants to place the token under a specific
            // sandbox-readable path. See MCPServer.swift for the v1/v2
            // auth story.
            let port = req.args.int("port").flatMap { UInt16(exactly: $0) }
            let tokenPath = req.args.str("token_path")
            return try MCPServer.shared.start(daemon: self, port: port, tokenFilePath: tokenPath)

        case "mcp_stop":
            MCPServer.shared.stop()
            return ["stopped": true]

        case "mcp_status":
            return MCPServer.shared.statusSnapshot()

        case "mcp_allow":
            // Whitelist an MCP peer by any of: team_id, bundle_id, pid.
            // Optional ttl_seconds makes the entry ephemeral (auto-drops
            // after expiry). An entry with NO fields set is rejected so
            // we never accidentally create a match-anything rule.
            let teamId = req.args.str("team_id")
            let bundleId = req.args.str("bundle_id")
            let pid = req.args.int("pid").map { pid_t($0) }
            let ttl = req.args.double("ttl_seconds")
            if teamId == nil && bundleId == nil && pid == nil {
                throw CUError.invalidArg("need at least one of team_id / bundle_id / pid")
            }
            MCPServer.shared.addWhitelist(teamId: teamId, bundleId: bundleId, pid: pid, ttlSeconds: ttl)
            return [
                "allowed": true,
                "team_id": teamId as Any?,
                "bundle_id": bundleId as Any?,
                "pid": pid.map { Int($0) } as Any?,
                "ttl_seconds": ttl as Any?,
            ] as [String: Any?]

        case "mcp_deny_all":
            // Blunt instrument: wipe the whole whitelist.
            MCPServer.shared.clearWhitelist()
            return ["cleared": true]

        case "mcp_whitelist":
            return ["entries": MCPServer.shared.whitelistList()]

        case "mcp_arm_peer":
            // Convenience shorthand for the common "let THIS specific
            // pid complete the OAuth flow" case. Equivalent to
            // `mcp_allow { pid: P, ttl_seconds: T }`.
            guard let pidInt = req.args.int("pid") else {
                throw CUError.invalidArg("missing `pid`")
            }
            let ttl = req.args.double("ttl_seconds") ?? 120
            MCPServer.shared.addWhitelist(teamId: nil, bundleId: nil, pid: pid_t(pidInt), ttlSeconds: ttl)
            return ["armed": true, "pid": pidInt, "ttl_seconds": ttl]

        case "raise":
            let bundle = req.args.str("bundle")
            let pid = req.args.int("pid").map { pid_t($0) }
            let windowId = req.args.int("window_id").map { CGWindowID($0) }
            try AXBridge.raise(bundle: bundle, pid: pid, windowId: windowId)
            return ["raised": true]

        case "shot":
            let out = try requireStr(req, "out")
            let maxW = req.args.int("max_width")
            let windowId: CGWindowID
            if let wid = req.args.int("window_id") {
                windowId = CGWindowID(wid)
            } else {
                // Derive from bundle/pid: first window of the app.
                let bundle = req.args.str("bundle")
                let pid = req.args.int("pid").map { pid_t($0) }
                let app = try AXBridge.resolveApp(bundle: bundle, pid: pid)
                guard let first = (try AXBridge.windows(of: app)).first else {
                    throw CUError.internalError("App has no windows to capture.")
                }
                windowId = first.id
            }
            let result = try captureWindowForState(
                windowId: windowId,
                outputPath: out,
                maxWidth: maxW ?? 1600,
                preferBackingStore: false
            )
            return ["path": result.path, "width": result.width, "height": result.height]

        case "stealth_stats":
            // Forensic counters from VictimObserver. Callers can use
            // this to verify a protected click caused zero observable
            // intrusions, and to watch for tap-disable recoveries which
            // indicate the callback was too slow (usually "main thread
            // blocked during click window").
            let reset = req.args.bool("reset") ?? false
            let s = VictimObserver.stats()
            if reset { VictimObserver.resetStats() }
            return ["stats": s, "reset": reset]

        case "turn_ended":
            // Called by the agent loop at the end of every turn. Mirrors
            // Codex's `ComputerUseIPCCodexTurnEndedRequest` — all
            // focus-suppression state that was set up for the turn's
            // actions is released HERE, not 5s-after-each-action. This
            // is the ONLY safe scope: if the agent hangs or crashes
            // mid-turn, the process-level watchdog / user cancellation
            // sends a `turn_ended` and the user's focus stops being
            // pinned.
            finishBackgroundActivation()
            let released = FocusStealPreventer.releaseAll()
            return [
                "turn_ended": true,
                "released_disallowed": released.disallowedCount,
                "released_only_allow": released.hadOnlyAllow,
            ]

        case "shutdown":
            DispatchQueue.main.async { self.shutdown() }
            return ["shutting_down": true]

        default:
            throw CUError.invalidArg("unknown cmd: \(req.cmd)")
        }
    }

    @discardableResult
    private func prepareBackgroundActivationIfNeeded(for snap: AXBridge.SnapshotResult) -> Bool {
        guard let windowId = snap.windowId, let frame = snap.windowBounds else { return false }
        if snap.windowMinimized {
            // get_app_state is a read-only observation. Keep minimized
            // windows minimized; screenshot capture below will try the
            // backing store when a caller asks for an image.
            return false
        }
        guard NSWorkspace.shared.frontmostApplication?.processIdentifier != snap.app.processIdentifier else {
            releaseBackgroundActivationIfMatches(pid: snap.app.processIdentifier)
            BackgroundActivationSession.activateWindow(
                targetPID: snap.app.processIdentifier,
                windowNumber: Int(windowId),
                windowFrame: frame
            )
            return true
        }
        do {
            _ = try prepareBackgroundActivation(pid: snap.app.processIdentifier, windowId: windowId, frame: frame)
            holdBackgroundActivation()
            return true
        } catch {
            log("background activation prepare failed: \(error)")
            return false
        }
    }

    private func prepareBackgroundActivationForAction(
        pid: pid_t,
        requestedWindowId: CGWindowID?
    ) throws -> WindowTarget? {
        guard let target = resolveWindowTarget(pid: pid, requestedWindowId: requestedWindowId) else {
            return nil
        }
        if target.minimized,
           let app = NSRunningApplication(processIdentifier: pid)
        {
            guard shouldRestoreMinimizedForBackgroundWork(app: app) else {
                return nil
            }
            if let restored = restoreMinimizedWindowInBackground(app: app, windowId: target.windowId) {
                _ = try prepareBackgroundActivation(pid: pid, windowId: restored.windowId, frame: restored.frame)
                return restored
            }
        }
        _ = try prepareBackgroundActivation(pid: pid, windowId: target.windowId, frame: target.frame)
        return target
    }

    private func restoreMinimizedWindowInBackground(app: NSRunningApplication, windowId: CGWindowID) -> WindowTarget? {
        guard let targetWin = ((try? AXBridge.windows(of: app)) ?? []).first(where: { $0.id == windowId }),
              targetWin.minimized
        else {
            return nil
        }

        let preservePid = NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0
        let watcher = preservePid > 0 ? PreserveFrontmostWatcher(preservePid: preservePid) : nil
        watcher?.begin()
        FocusStealPreventer.addDisallowed(app.processIdentifier)
        defer {
            FocusStealPreventer.removeDisallowed(app.processIdentifier)
            if preservePid > 0 {
                restoreForeground(pid: preservePid)
            }
            watcher?.end()
        }

        if AXBridge.unminimizeIfNeeded(targetWin.element) {
            Thread.sleep(forTimeInterval: 0.12)
        }
        guard let restoredWin = ((try? AXBridge.windows(of: app)) ?? []).first(where: { $0.id == windowId }) else {
            return nil
        }
        let target = WindowTarget(
            pid: app.processIdentifier,
            windowId: restoredWin.id,
            frame: restoredWin.bounds,
            minimized: restoredWin.minimized
        )
        windowTargetLock.lock()
        lastWindowTargets[app.processIdentifier] = target
        windowTargetLock.unlock()
        return target
    }

    private func prepareBackgroundActivation(pid: pid_t, windowId: CGWindowID, frame: CGRect) throws -> BackgroundActivationSession? {
        if NSWorkspace.shared.frontmostApplication?.processIdentifier == pid {
            releaseBackgroundActivationIfMatches(pid: pid)
            BackgroundActivationSession.activateWindow(
                targetPID: pid,
                windowNumber: Int(windowId),
                windowFrame: frame
            )
            return nil
        }

        backgroundSessionLock.lock()
        let existing = activeBackgroundSession
        let sameTarget = activeBackgroundPID == pid && activeBackgroundWindowID == windowId
        backgroundSessionLock.unlock()

        let session: BackgroundActivationSession
        if let existing, sameTarget {
            session = existing
            session.beginTargetDelivery()
        } else {
            finishBackgroundActivation()
            session = try BackgroundActivationSession.start(targetPID: pid)
            backgroundSessionLock.lock()
            activeBackgroundSession = session
            activeBackgroundPID = pid
            activeBackgroundWindowID = windowId
            backgroundSessionLock.unlock()
        }
        session.activateWindow(windowNumber: Int(windowId), windowFrame: frame)
        return session
    }

    private func holdBackgroundActivation() {
        backgroundSessionLock.lock()
        let session = activeBackgroundSession
        backgroundSessionLock.unlock()
        session?.holdFocusSuppressionUntilFinish()
    }

    private func finishBackgroundActivation() {
        backgroundSessionLock.lock()
        let session = activeBackgroundSession
        activeBackgroundSession = nil
        activeBackgroundPID = 0
        activeBackgroundWindowID = 0
        backgroundSessionLock.unlock()
        session?.finish()
    }

    private func releaseBackgroundActivationIfMatches(pid: pid_t) {
        backgroundSessionLock.lock()
        let shouldRelease = activeBackgroundPID == pid
        backgroundSessionLock.unlock()
        if shouldRelease {
            finishBackgroundActivation()
        }
    }

    private func startFrontmostMonitor() {
        frontmostObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didActivateApplicationNotification,
            object: nil,
            queue: .main
        ) { [weak self] notif in
            guard let self else { return }
            guard let app = notif.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication else { return }
            self.releaseBackgroundActivationIfMatches(pid: app.processIdentifier)
        }
        // Reap Chromium AX observers when their target process exits, so
        // dead AXObservers + run loop sources don't accumulate over the
        // daemon's (long-lived) lifetime.
        terminationObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didTerminateApplicationNotification,
            object: nil,
            queue: .main
        ) { notif in
            guard let app = notif.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication else { return }
            ChromiumAccessibilityActivation.shared.deactivate(pid: app.processIdentifier)
        }
    }

    private func shouldUseProcessActivation(_ req: Request, explicitActivate: Bool) -> Bool {
        if explicitActivate { return false }
        if req.args.str("activation") == "process" { return true }
        if req.args.str("activation") == "window" { return false }
        guard let pid = resolveActionPid(req) else { return false }
        return shouldUseProcessActivation(req, explicitActivate: explicitActivate, pid: pid)
    }

    private func shouldUseProcessActivation(_ req: Request, explicitActivate: Bool, pid: pid_t) -> Bool {
        if explicitActivate { return false }
        if req.args.str("activation") == "process" { return true }
        if req.args.str("activation") == "window" { return false }
        if NSWorkspace.shared.frontmostApplication?.processIdentifier == pid { return false }
        return true
    }

    private func recordWindowTarget(from snap: AXBridge.SnapshotResult) {
        guard let windowId = snap.windowId, let frame = snap.windowBounds else { return }
        let target = WindowTarget(
            pid: snap.app.processIdentifier,
            windowId: windowId,
            frame: frame,
            minimized: snap.windowMinimized
        )
        windowTargetLock.lock()
        lastWindowTargets[snap.app.processIdentifier] = target
        windowTargetLock.unlock()
    }

    private func resolveWindowTarget(pid: pid_t, requestedWindowId: CGWindowID?) -> WindowTarget? {
        windowTargetLock.lock()
        let cached = lastWindowTargets[pid]
        windowTargetLock.unlock()
        if let cached, requestedWindowId == nil || cached.windowId == requestedWindowId {
            return cached
        }
        if let app = NSRunningApplication(processIdentifier: pid),
           let wins = try? AXBridge.windows(of: app),
           !wins.isEmpty
        {
            let win: AXBridge.WindowInfo = {
                if let requestedWindowId, let found = wins.first(where: { $0.id == requestedWindowId }) { return found }
                return wins.first(where: { $0.focused }) ?? wins[0]
            }()
            let target = WindowTarget(pid: pid, windowId: win.id, frame: win.bounds, minimized: win.minimized)
            windowTargetLock.lock()
            lastWindowTargets[pid] = target
            windowTargetLock.unlock()
            return target
        }
        if let entry = WindowList.topWindow(for: pid) {
            return WindowTarget(pid: pid, windowId: entry.id, frame: entry.bounds, minimized: false)
        }
        return nil
    }

    private func captureWindowForState(
        windowId: CGWindowID,
        outputPath: String,
        maxWidth: Int,
        preferBackingStore: Bool
    ) throws -> Capture.Result {
        if preferBackingStore {
            return try Capture.captureWindowBackingStore(windowId: windowId, outputPath: outputPath, maxWidth: maxWidth)
        }
        do {
            return try runAsync {
                try await Capture.captureWindow(windowId: windowId, outputPath: outputPath, maxWidth: maxWidth)
            }
        } catch {
            return try Capture.captureWindowBackingStore(windowId: windowId, outputPath: outputPath, maxWidth: maxWidth)
        }
    }

    /// Wait for the target app's UI to stabilise after launch. Two-phase:
    ///   Phase A — wait for any usable window (AX or CGWindowList), up to 3s.
    ///   Phase B — wait for the AX element count OR screenshot pixel hash
    ///             to be equal across two consecutive samples, up to the
    ///             remaining budget. Samples every 350ms.
    /// Returns immediately when both phases pass or the timeout elapses.
    private func waitUntilReady(app: NSRunningApplication, timeout: TimeInterval) {
        let start = Date()
        let pid = app.processIdentifier

        // Phase A: any window
        while Date().timeIntervalSince(start) < min(3.0, timeout) {
            if let wins = try? AXBridge.windows(of: app), !wins.isEmpty { break }
            if WindowList.topWindow(for: pid) != nil { break }
            Thread.sleep(forTimeInterval: 0.15)
        }

        // Phase B: content stable (two consecutive equal signatures)
        var lastSig: UInt64 = 0
        var matchCount = 0
        while Date().timeIntervalSince(start) < timeout {
            let sig = signature(for: app)
            if sig != 0, sig == lastSig {
                matchCount += 1
                if matchCount >= 1 {
                    // Two consecutive equal → stable enough.
                    return
                }
            } else {
                matchCount = 0
            }
            lastSig = sig
            Thread.sleep(forTimeInterval: 0.35)
        }
    }

    /// Cheap scalar signature of the app's current UI state: combines AX
    /// element count with a quick CGImage pixel digest. Identical values
    /// across samples mean "content is no longer changing".
    private func signature(for app: NSRunningApplication) -> UInt64 {
        var elementCount: UInt64 = 0
        if let wins = try? AXBridge.windows(of: app), let first = wins.first {
            elementCount = UInt64(axSubtreeCount(first.element, cap: 200))
        }
        var pixelHash: UInt64 = 0
        if let wid = WindowList.topWindow(for: app.processIdentifier)?.id {
            pixelHash = UInt64(cheapWindowPixelHash(windowId: wid))
        }
        return (elementCount << 32) ^ pixelHash
    }

    /// Walk `el` and return a count of its descendants, bounded by `cap`
    /// so a deep tree doesn't cost more than a few ms per sample.
    private func axSubtreeCount(_ el: AXUIElement, cap: Int) -> Int {
        var stack: [AXUIElement] = [el]
        var count = 0
        while let next = stack.popLast() {
            count += 1
            if count >= cap { break }
            var raw: CFTypeRef?
            let err = AXUIElementCopyAttributeValue(next, kAXChildrenAttribute as CFString, &raw)
            if err == .success, let kids = raw as? [AXUIElement] {
                stack.append(contentsOf: kids)
            }
        }
        return count
    }

    /// A very cheap pixel hash of the given window — sample ~64 pixels
    /// across the captured CGImage. Good enough to spot "still loading"
    /// shimmer vs "content painted".
    private func cheapWindowPixelHash(windowId: CGWindowID) -> UInt32 {
        let opts: CGWindowListOption = [.optionIncludingWindow]
        guard let img = CGWindowListCreateImage(
            .null, opts, windowId,
            [.boundsIgnoreFraming, .nominalResolution]
        ) else { return 0 }
        guard let data = img.dataProvider?.data as Data? else { return 0 }
        var h: UInt32 = 0xDEAD_BEEF
        let count = data.count
        let step = max(1, count / 64)
        data.withUnsafeBytes { buf in
            var i = 0
            while i < count {
                h = (h &* 16777619) ^ UInt32(buf[i])
                i += step
            }
        }
        return h
    }

    // MARK: - Post-action settle
    //
    // The MCP wrapper's `respond(summary, target)` takes a post-action
    // screenshot by calling get_app_state again. That call races the
    // target's own redraw — AXPerformAction / CGEventPostToPid return
    // as soon as the target's runloop has *accepted* the event, but
    // the visible effect (button depressed, menu opened, text echoed)
    // lands one vsync or more later. Without any settle, ~60% of post-
    // action screenshots capture the pre-click frame.
    //
    // Pattern: snap a cheap pixel hash of the target window BEFORE the
    // action; after the action returns, poll the same hash every 40ms
    // until it differs (content visibly changed), or a budget expires.
    // Best-effort — if we can't resolve a window id for the target pid,
    // or the hash fails, we skip the wait entirely.

    /// Opaque handle paired between `settlePrelude` and `settlePostlude`.
    /// Empty (`windowId == nil`) when the pid couldn't be resolved to a
    /// CGWindow, in which case the postlude is a no-op.
    fileprivate struct SettleToken {
        let windowId: CGWindowID?
        let baselineHash: UInt32
    }

    /// Outcome of a settle wait — surfaced to action handlers so they
    /// can report visible-change observation back to the agent. The
    /// agent uses `observed=false` as a signal that its action did
    /// not produce a UI delta within the wait budget (e.g. cmd+K
    /// reached the wrong window, or the target view wasn't focused),
    /// letting it pivot on the next turn instead of brute-retrying
    /// the same call until loop-guard kicks in.
    fileprivate struct SettleResult {
        /// true when the post-action pixel hash differed from the
        /// pre-action baseline within the budget. false when the
        /// budget elapsed with no observable change, OR when the
        /// prelude couldn't establish a baseline (no resolvable
        /// window). In the second case the result is best-effort
        /// and the agent should treat it as "unknown" rather than
        /// "definitely no change" — `observable=false` carries that
        /// distinction.
        let observed: Bool
        /// true when prelude established a window+baseline. Lets
        /// callers distinguish "we watched and saw nothing" from
        /// "we couldn't watch at all".
        let observable: Bool
        /// Milliseconds spent waiting. Equal to the budget on miss,
        /// less on a hit (early-return as soon as the hash flips).
        let waitedMs: Int
    }

    /// Capture a pre-action baseline of the target window's content
    /// hash. Call BEFORE running the mutating action; pair with
    /// `settlePostlude` after.
    fileprivate func settlePrelude(pid: pid_t?) -> SettleToken {
        guard let p = pid else { return SettleToken(windowId: nil, baselineHash: 0) }
        let wid: CGWindowID? = {
            mappingLock.lock(); defer { mappingLock.unlock() }
            return lastMapping[p]?.windowId ?? WindowList.topWindow(for: p)?.id
        }()
        guard let w = wid else { return SettleToken(windowId: nil, baselineHash: 0) }
        return SettleToken(windowId: w, baselineHash: cheapWindowPixelHash(windowId: w))
    }

    /// Block until the target window's content has visibly changed
    /// versus the prelude baseline, or `budgetMs` elapses. Returns a
    /// SettleResult so action handlers can include observation in
    /// their response. No-op (returns observable=false) when the
    /// token is empty (prelude couldn't resolve a window) or the
    /// budget is zero.
    ///
    /// @discardableResult so existing callsites that don't need the
    /// observation signal still compile cleanly — only handlers that
    /// surface the result to the agent need to consume it.
    @discardableResult
    fileprivate func settlePostlude(_ token: SettleToken, budgetMs: Int = 600) -> SettleResult {
        guard let w = token.windowId, token.baselineHash != 0, budgetMs > 0 else {
            return SettleResult(observed: false, observable: false, waitedMs: 0)
        }
        let start = Date()
        let deadline = start.addingTimeInterval(Double(budgetMs) / 1000.0)
        while Date() < deadline {
            let cur = cheapWindowPixelHash(windowId: w)
            if cur != 0 && cur != token.baselineHash {
                let waited = Int(Date().timeIntervalSince(start) * 1000)
                return SettleResult(observed: true, observable: true, waitedMs: waited)
            }
            Thread.sleep(forTimeInterval: 0.04)
        }
        let waited = Int(Date().timeIntervalSince(start) * 1000)
        return SettleResult(observed: false, observable: true, waitedMs: waited)
    }

    /// Merge a SettleResult into the action handler's response dict.
    /// Adds three fields the agent reads:
    ///   • `ui_changed`: bool — true when a visible UI delta was
    ///     observed within the budget. The single most useful signal:
    ///     `false` means "your action did not produce a change",
    ///     which lets the agent pivot immediately instead of retrying.
    ///   • `ui_change_observable`: bool — false when we couldn't
    ///     resolve a window/baseline (best-effort). Distinguishes
    ///     "watched, saw nothing" from "couldn't watch".
    ///   • `settle_waited_ms`: int — actual wait time. Useful for the
    ///     agent to gauge how slow the target app is responding.
    ///
    /// Centralised here so every action handler reports the same
    /// fields with the same names.
    fileprivate func mergeSettleResult(_ result: SettleResult, into payload: [String: Any]) -> [String: Any] {
        var out = payload
        out["ui_changed"] = result.observed
        out["ui_change_observable"] = result.observable
        out["settle_waited_ms"] = result.waitedMs
        return out
    }

    /// Like `resolveActionPid` but falls back to looking up the pid
    /// recorded for a ref in the ElementStore. Used only for the settle
    /// path so ref-only calls (no explicit pid/bundle) still settle.
    fileprivate func resolveSettlePid(_ req: Request, ref: String? = nil) -> pid_t? {
        if let p = resolveActionPid(req) { return p }
        guard let r = ref else { return nil }
        return store.resolve(r)?.pid
    }

    /// Resolve the target pid from either `pid` or `bundle` in the
    /// request args. Action handlers should call this instead of
    /// reading `pid` directly — it lets the model pass EITHER:
    ///   • `pid: <int>` — explicit
    ///   • `bundle: <string>` — we look up the running app's pid; if
    ///     the app isn't running, we launch it in the background so
    ///     the caller doesn't have to orchestrate get_app_state + click
    ///     separately.
    /// Auto-launch path is WRAPPED IN A FRONTMOST-PRESERVE WATCHER so
    /// that if the launching CEF/Cocoa app calls
    /// `[NSApp activateIgnoringOtherApps:]` in its own startup code
    /// (very common), the activation notification is caught and the
    /// user's real frontmost is snapped back immediately — no visible
    /// flicker during cold-launch.
    private func resolveActionPid(_ req: Request) -> pid_t? {
        if let p = req.args.int("pid").map({ pid_t($0) }), p > 0 { return p }
        guard let bundle = req.args.str("bundle") else { return nil }
        if let app = try? AXBridge.resolveApp(bundle: bundle, pid: nil) {
            return app.processIdentifier
        }
        return launchWithFrontmostPreserved(bundle: bundle)?.processIdentifier
    }

    /// Launch a bundle in the background while a PreserveFrontmostWatcher
    /// is active. The watcher catches any app that tries to become
    /// frontmost during the launch (including the new app itself on its
    /// first NSApp.finishLaunching) and re-asserts the user's original
    /// frontmost via SetFrontProcessWithOptions. All in the same main-
    /// loop cycle, so Dock / menu bar / window chrome don't have time to
    /// redraw.
    fileprivate func launchWithFrontmostPreserved(bundle: String) -> NSRunningApplication? {
        // Capture the user's original frontmost app BEFORE we do anything.
        // We'll lock the tap to allow ONLY this pid's activations during
        // the launch window — everyone else is silenced. This is critical
        // because NSWorkspace.openApplication's completion fires AFTER
        // the new app's applicationDidFinishLaunching, so a reactive
        // pid-blacklist-after-launch is always too late to catch the
        // very first kCPSNotifyNewFront.
        let originalFrontPid: pid_t = NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0

        // Install the CPS-notification tap + reactive snap-back watcher
        // BEFORE doing anything. The tap drops activation notifications
        // targeting anyone other than originalFrontPid; the watcher is
        // a belt-and-braces snap-back for activations that bypass the
        // tap (Electron's AppKit-layer self-activation sometimes does).
        let preventer = FocusStealPreventer()
        preventer.begin()
        var watcher: PreserveFrontmostWatcher?
        if originalFrontPid > 0 {
            FocusStealPreventer.setOnlyAllow(originalFrontPid)
            watcher = PreserveFrontmostWatcher(preservePid: originalFrontPid)
            watcher?.begin()
        }

        let app = try? runAsync({ try await AXBridge.launchApp(bundle: bundle, activates: false) })

        // Also mark the new pid as permanently disallowed for a short
        // window so trailing delayed self-activations (e.g. from async
        // startup work) are caught even after we release the only-allow
        // lock.
        let newPid: pid_t? = app?.processIdentifier
        if let p = newPid {
            FocusStealPreventer.addDisallowed(p)
        }

        // Give the app ~800ms to run its finishLaunching (where it typically
        // calls activateIgnoringOtherApps:). Any activation events it
        // triggers are swallowed by the tap + watcher.
        Thread.sleep(forTimeInterval: 0.8)

        // Hold guards for 8s post-launch — Electron's late self-activation
        // window (renderer → main IPC → activateIgnoringOtherApps) can
        // easily span several seconds. After that we tear everything down.
        let w = watcher
        DispatchQueue.main.asyncAfter(deadline: .now() + 8.0) {
            FocusStealPreventer.setOnlyAllow(nil)
            if let p = newPid {
                FocusStealPreventer.removeDisallowed(p)
            }
            preventer.end()
            w?.end()
        }

        return app
    }

    /// Before dispatching an action that targets `pid`, prime the lens
    /// overlay with the target's window id so it can order itself above
    /// that specific window (Codex V2 `.aboveTarget` mode). When the
    /// target moves to another Space / gets lowered, the lens goes with
    /// it — staying visually bound to the thing Alma is acting on,
    /// rather than floating on top of everything.
    ///
    /// No-op if we don't have a screenshot mapping yet (e.g. pure coord
    /// click without a prior get_app_state) — the lens falls back to
    /// plain overlay-level.
    private func primeLensTargetWindow(pid: pid_t?) {
        guard let p = pid else {
            LensOverlay.shared.setTargetWindow(id: nil)
            return
        }
        mappingLock.lock()
        let wid = lastMapping[p]?.windowId
        mappingLock.unlock()
        LensOverlay.shared.setTargetWindow(id: wid)
    }

    /// Translate a screenshot-pixel coordinate into a screen-point
    /// coordinate using the last recorded screenshot mapping for `pid`.
    /// Returns nil when no mapping is known.
    private func translateScreenshotCoord(pid: pid_t, x: Double, y: Double) -> (Double, Double)? {
        mappingLock.lock(); defer { mappingLock.unlock() }
        guard let m = lastMapping[pid] else { return nil }
        let sx = m.windowOrigin.x + CGFloat(x) * (m.windowSize.width / m.imageSize.width)
        let sy = m.windowOrigin.y + CGFloat(y) * (m.windowSize.height / m.imageSize.height)
        return (Double(sx), Double(sy))
    }

    /// Resolve `x`/`y` from a request, honouring `coord_space`:
    ///   "screenshot" (default when last mapping exists) — input is in
    ///     screenshot pixels; we map to screen points.
    ///   "screen" — input is already in screen points.
    private func resolvePoint(_ req: Request, pid: pid_t?) -> CGPoint? {
        guard let x = req.args.double("x"), let y = req.args.double("y") else { return nil }
        let space = req.args.str("coord_space")
        let resolved: (Double, Double)
        if space == "screen" {
            resolved = (x, y)
        } else if let p = pid, let translated = translateScreenshotCoord(pid: p, x: x, y: y) {
            resolved = translated
        } else {
            // No mapping available and no explicit "screen" hint — assume
            // screen points to match legacy behaviour.
            resolved = (x, y)
        }
        return CGPoint(x: resolved.0, y: resolved.1)
    }

    private func snapToDict(_ snap: AXBridge.SnapshotResult) -> [String: Any?] {
        // The AX-side title (kAXTitleAttribute) can lag a real
        // channel/page/tab switch on Electron/CEF apps by several
        // seconds while the renderer reflushes its tree. The
        // WindowServer-side title (kCGWindowName, surfaced via
        // WindowList.entry) updates near-instantly when the
        // underlying NSWindow.title changes — surface both so the
        // agent has a fresh signal even when the AX tree is mid-
        // transition. Concrete example: Slack right after a channel
        // switch — `title` still reads "pd-playground (Channel)" but
        // `live_title` already reads "isoform-platform (Channel)".
        // When the two disagree, `live_title` is the truth and AX is
        // catching up.
        let liveTitle: String? = snap.windowId.flatMap { WindowList.entry(id: $0)?.title }
        return [
            "snapshot_id": snap.snapshotId,
            // AX tree status — agents should branch on this:
            //   "populated"          → use AX refs (e*) for clicking, Layer 2, no flicker.
            //   "woke_up"            → ditto; just shows it took a moment.
            //   "empty_after_wakeup" → AX tree really is empty (e.g. 网易云音乐);
            //                          use v* OCR refs or intent command.
            //   "no_wakeup_budget"   → caller didn't ask us to wait; retry with
            //                          wakeup_seconds>0 if AX matters.
            "ax_status": snap.axStatus,
            "wakeup_attempts": snap.wakeupAttempts,
            "wakeup_duration_ms": snap.wakeupDurationMs,
            "app": [
                "bundle": snap.app.bundleIdentifier as Any?,
                "name": snap.app.localizedName as Any?,
                "pid": Int(snap.app.processIdentifier),
            ] as [String: Any?],
            "window": [
                "id": snap.windowId.map { Int($0) } as Any?,
                "title": snap.windowTitle as Any?,
                "live_title": liveTitle as Any?,
                "minimized": snap.windowMinimized,
                "bounds": snap.windowBounds.map {
                    ["x": $0.origin.x, "y": $0.origin.y, "w": $0.size.width, "h": $0.size.height]
                } as Any?,
            ] as [String: Any?],
            "elements": snap.elements.map { $0.toDict() },
        ]
    }

    private func requireStr(_ req: Request, _ key: String) throws -> String {
        guard let v = req.args.str(key), !v.isEmpty else { throw CUError.invalidArg("missing \"\(key)\"") }
        return v
    }

    // MARK: - Electron-app cmd-shortcut guard
    //
    // Chromium-shell apps (Slack, Discord, VSCode, Cursor, Notion,
    // Linear, Figma, MS Teams …) silently strip the cmd modifier from
    // keyboard events when the target window isn't frontmost. The bare
    // letter then lands in whichever text composer has focus —
    // user-visible data pollution that survives across our process'
    // lifetime (Slack saves drafts on every keystroke).
    //
    // We refuse cmd-modified press_key calls to known Electron bundles
    // unless the caller opts in via `activate: true`. The list is
    // best-effort — a new Electron app missing from the set simply
    // doesn't get the protection, which is the safe failure direction
    // (false negatives are tolerable; false positives would block real
    // shortcuts).
    //
    // Why prefixes: Slack's bundleID ends in "...slackmacgap"; some
    // apps version-suffix their bundles (Notion variants, Cursor ID is
    // a UUID prefix). hasPrefix gives us tolerance for those without
    // a regex.
    private static let electronBundlePrefixes: [String] = [
        "com.tinyspeck.slackmacgap",      // Slack
        "com.hnc.Discord",                 // Discord
        "com.microsoft.VSCode",            // VSCode
        "com.todesktop.230313mzl4w4u92",   // Cursor (todesktop deploy id)
        "com.exafunction.windsurf",        // Windsurf
        "notion.id",                       // Notion
        "com.linear",                      // Linear
        "com.figma.Desktop",               // Figma
        "com.microsoft.teams2",            // MS Teams (new)
        "com.microsoft.teams",             // MS Teams (classic)
        "com.electron",                    // generic Electron passthrough
    ]

    private func bundleIsElectron(_ bundle: String?) -> Bool {
        guard let b = bundle else { return false }
        return Daemon.electronBundlePrefixes.contains { b.hasPrefix($0) }
    }

    private func shouldRestoreMinimizedForBackgroundWork(app: NSRunningApplication) -> Bool {
        if bundleIsElectron(app.bundleIdentifier) { return true }
        let bundle = app.bundleIdentifier?.lowercased() ?? ""
        if bundle.contains("lark") || bundle.contains("feishu") || bundle.contains("bytedance") {
            return true
        }
        let name = app.localizedName?.lowercased() ?? ""
        return name.contains("slack")
            || name.contains("lark")
            || name.contains("feishu")
            || name.contains("飞书")
            || name.contains("飛書")
    }

    /// True when `combo` carries the cmd / command / meta modifier.
    /// xdotool-style strings normalise via KeyParser; here we do a
    /// case-insensitive substring scan against the modifier names the
    /// parser accepts, which is cheaper than re-parsing and reliable
    /// for the gate's purpose (we only need to detect cmd presence,
    /// not validate the combo's full structure — the parser does that
    /// downstream).
    private func comboHasCmdModifier(_ combo: String) -> Bool {
        let lower = combo.lowercased()
        // Token-bounded match so substrings like "cmdlet" or
        // "command-palette" don't trip the guard. The xdotool format
        // uses + or - as separators; we also tolerate plain " ".
        let separators: Set<Character> = ["+", "-", " "]
        var tokenStart = lower.startIndex
        for i in lower.indices {
            if separators.contains(lower[i]) {
                let token = lower[tokenStart..<i]
                if token == "cmd" || token == "command" || token == "meta" {
                    return true
                }
                tokenStart = lower.index(after: i)
            }
        }
        let last = lower[tokenStart..<lower.endIndex]
        return last == "cmd" || last == "command" || last == "meta"
    }

    /// Returns the bundle identifier for a pid, or nil if it can't be
    /// resolved (process gone, sandbox restriction, etc.). Used by the
    /// Electron-shortcut guard.
    private func bundleIdentifier(for pid: pid_t) -> String? {
        return NSRunningApplication(processIdentifier: pid)?.bundleIdentifier
    }

    private func requireDouble(_ req: Request, _ key: String) throws -> Double {
        guard let v = req.args.double(key) else { throw CUError.invalidArg("missing \"\(key)\"") }
        return v
    }

    // MARK: - Idle timeout + signals

    private func startIdleTimer() {
        let t = DispatchSource.makeTimerSource(queue: .main)
        t.schedule(deadline: .now() + 30, repeating: 30)
        t.setEventHandler { [weak self] in
            guard let self = self else { return }
            if Date().timeIntervalSince(self.lastActivity) > self.idleTimeoutSec {
                self.log("idle timeout reached; exiting")
                self.shutdown()
            }
        }
        t.resume()
        idleTimer = t
    }

    private func setupSignalHandlers() {
        for sig in [SIGINT, SIGTERM, SIGHUP] {
            signal(sig, SIG_IGN)
            let src = DispatchSource.makeSignalSource(signal: sig, queue: .main)
            src.setEventHandler { [weak self] in self?.shutdown() }
            src.resume()
            // Retain by leaking; we never undo this — daemon lifecycle == process lifecycle.
            _ = Unmanaged.passRetained(src)
        }
    }

    private func shutdown() {
        if isShuttingDown { return }
        isShuttingDown = true
        log("shutting down")
        if let observer = frontmostObserver {
            NSWorkspace.shared.notificationCenter.removeObserver(observer)
            frontmostObserver = nil
        }
        if let observer = terminationObserver {
            NSWorkspace.shared.notificationCenter.removeObserver(observer)
            terminationObserver = nil
        }
        finishBackgroundActivation()
        ChromiumAccessibilityActivation.shared.cleanupAll()
        AXBridge.restoreAllEnhancedUI()
        if listenFd >= 0 { close(listenFd); listenFd = -1 }
        unlink(socketPath)
        exit(0)
    }

    // MARK: - Misc

    private var version: String { "0.1.0" }

    private func log(_ msg: String) {
        FileHandle.standardError.write(Data("[alma-cu] \(msg)\n".utf8))
    }

    private func errnoMsg() -> String {
        String(cString: strerror(errno))
    }
}

/// Tiny bridge: synchronously wait on a Swift async function from a non-async
/// caller. The daemon's dispatch loop is sync (easier to reason about per-req
/// lifetimes), so we hop to a Task and block.
func runAsync<T>(_ body: @escaping () async throws -> T) throws -> T {
    let sem = DispatchSemaphore(value: 0)
    var result: Result<T, Error>!
    Task {
        do { result = .success(try await body()) }
        catch { result = .failure(error) }
        sem.signal()
    }
    sem.wait()
    switch result! {
    case .success(let v): return v
    case .failure(let e):
        if let cu = e as? CUError { throw cu }
        throw CUError.internalError(e.localizedDescription)
    }
}

/// Probe Screen Recording permission without stealing focus. Cheapest reliable
/// signal: ask SCShareableContent for the current list of windows; failure
/// means not authorized.
enum SCPermissionProbe {
    static func check() -> Bool {
        var ok = false
        let sem = DispatchSemaphore(value: 0)
        Task {
            do {
                _ = try await SCShareableContent.current
                ok = true
            } catch {
                ok = false
            }
            sem.signal()
        }
        sem.wait()
        return ok
    }
}
