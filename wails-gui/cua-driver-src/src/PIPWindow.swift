import AppKit
import ApplicationServices
import AVFoundation
import CoreMedia
import CoreVideo
import Foundation
import ScreenCaptureKit

// MARK: - PIPManager

/// Picture-in-picture floating window that continuously mirrors a single
/// foreign window via ScreenCaptureKit. Codex parity: their
/// `feature/computerUsePIP` surfaces a persistent floater so the user can
/// still watch what the agent is doing to a background / minimised app
/// without raising it to frontmost.
///
/// This file owns three P3 subsystems:
///   1. Floating NSWindow + SCStream composite (`PIPSession`, `PIPFloaterWindow`,
///      `PIPContentView`).
///   2. Virtual AI cursor inside the PIP (`PIPLens`), driven by
///      NotificationCenter broadcasts from `LensOverlay` in Overlay.swift.
///   3. Minimise-button hijack (`PIPMinimizeArmer`): AXObserver on a
///      pre-armed window's minimise event auto-opens a PIP when the user
///      clicks minimise.
///
/// Thread model: all AppKit + SCStream lifecycle on the main thread.
/// SCStream output callbacks arrive on an internal queue; they hop to main
/// before touching UI state. The manager holds strong refs to every live
/// session keyed by `CGWindowID` — SCStream does not retain its delegate,
/// so if we dropped a session the stream would silently stop delivering.
final class PIPManager {
    static let shared = PIPManager()

    private var sessions: [CGWindowID: PIPSession] = [:]
    private let lock = NSLock()

    private init() {}

    /// Open (or front-raise) a PIP for the given window. If a session for
    /// this window already exists, the existing floater is brought forward
    /// and returned unchanged. Returns the session status snapshot.
    @discardableResult
    func open(
        windowId: CGWindowID,
        pid: pid_t?,
        title: String?,
        preferredSize: NSSize?
    ) throws -> [String: Any?] {
        lock.lock()
        if let existing = sessions[windowId] {
            lock.unlock()
            DispatchQueue.main.async { existing.bringToFront() }
            return existing.statusSnapshot()
        }
        lock.unlock()

        // SCShareableContent lookup happens off-main; hop back after.
        let scWindow = try runAsync { () async throws -> SCWindow in
            let content: SCShareableContent
            do { content = try await SCShareableContent.current }
            catch { throw CUError.scDenied() }
            guard let w = content.windows.first(where: { $0.windowID == windowId }) else {
                throw CUError.internalError(
                    "No SCWindow with id \(windowId) (window closed or off-screen?)"
                )
            }
            return w
        }

        let session = try runMainBlocking { () throws -> PIPSession in
            try PIPSession(
                windowId: windowId,
                pid: pid,
                title: title ?? scWindow.title ?? "",
                scWindow: scWindow,
                preferredSize: preferredSize,
                onClose: { [weak self] wid in self?.onSessionClosed(windowId: wid) }
            )
        }

        lock.lock()
        sessions[windowId] = session
        lock.unlock()

        return session.statusSnapshot()
    }

    /// Close the PIP for a specific window. No-op if not open.
    @discardableResult
    func close(windowId: CGWindowID) -> Bool {
        lock.lock()
        let session = sessions.removeValue(forKey: windowId)
        lock.unlock()
        guard let s = session else { return false }
        DispatchQueue.main.async { s.shutdown() }
        return true
    }

    /// Close every open PIP.
    func closeAll() {
        lock.lock()
        let all = Array(sessions.values)
        sessions.removeAll()
        lock.unlock()
        DispatchQueue.main.async { all.forEach { $0.shutdown() } }
    }

    /// Snapshot of every live PIP — used by `pip_status`.
    func statusList() -> [[String: Any?]] {
        lock.lock(); defer { lock.unlock() }
        return sessions.values.map { $0.statusSnapshot() }
    }

    /// Look up a live session by target window id — used by the minimise
    /// armer (subsystem 3) so a second minimise while the PIP is already
    /// open is a no-op.
    func hasSession(windowId: CGWindowID) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return sessions[windowId] != nil
    }

    private func onSessionClosed(windowId: CGWindowID) {
        lock.lock()
        sessions.removeValue(forKey: windowId)
        lock.unlock()
    }

    // MARK: - Utilities

    /// Run a closure on the main queue, blocking the caller for its result.
    /// Needed because Daemon.dispatch() runs off-main but NSWindow init must
    /// happen on main.
    private func runMainBlocking<T>(_ body: @escaping () throws -> T) throws -> T {
        if Thread.isMainThread { return try body() }
        var result: Result<T, Error>!
        let sem = DispatchSemaphore(value: 0)
        DispatchQueue.main.async {
            do { result = .success(try body()) }
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
}

// MARK: - Session

/// One PIP floater + one SCStream + one output sink. Lifetime: from `open`
/// to explicit `close` OR target-window-closed signal (AXObserver or
/// SCStream error).
final class PIPSession: NSObject, SCStreamDelegate, SCStreamOutput {
    let windowId: CGWindowID
    let pid: pid_t?
    let title: String

    /// Source window frame in screen points (AX top-left origin). Cached
    /// at session open; refreshed on each lens event via CGWindowList so
    /// the cursor mapping stays correct when the user moves/resizes the
    /// source window.
    private var cachedSrcFrame: CGRect

    private let floater: PIPFloaterWindow
    private let contentView: PIPContentView
    private var stream: SCStream!
    private let outputQueue = DispatchQueue(label: "com.yetone.alma.cu.pip.output", qos: .userInteractive)
    private let onClose: (CGWindowID) -> Void

    private var lastFrameAt: CFTimeInterval = 0
    private var frameCount: Int = 0
    private var framesWindowStart: CFTimeInterval = CACurrentMediaTime()

    /// `true` once the stream has delivered at least one frame. Before that
    /// the floater shows a placeholder so the user doesn't see a black box.
    private var hasFirstFrame = false
    private var isShuttingDown = false

    /// AXObserver tracking the target window's lifecycle so the PIP can
    /// self-close when the source goes away. Registered on init, torn down
    /// in `shutdown`.
    private var axObserver: AXObserver?
    private let axWinElement: AXUIElement?

    /// Observers we added to NotificationCenter for the virtual-cursor
    /// mirror. Removed on shutdown.
    private var ncTokens: [NSObjectProtocol] = []

    init(
        windowId: CGWindowID,
        pid: pid_t?,
        title: String,
        scWindow: SCWindow,
        preferredSize: NSSize?,
        onClose: @escaping (CGWindowID) -> Void
    ) throws {
        self.windowId = windowId
        self.pid = pid
        self.title = title
        self.onClose = onClose
        self.cachedSrcFrame = scWindow.frame

        // Default: 1/3 of the source window, clamped between 240x160 and
        // 960x720. Aspect ratio locked to the source so the mirror never
        // looks stretched.
        let srcW = max(1, scWindow.frame.width)
        let srcH = max(1, scWindow.frame.height)
        let aspect = srcW / srcH
        let initialSize: NSSize = {
            if let s = preferredSize { return s }
            let target = NSSize(width: srcW / 3, height: srcH / 3)
            let minS = NSSize(width: 240, height: 240 / aspect)
            let maxS = NSSize(width: 960, height: 960 / aspect)
            let w = min(max(target.width, minS.width), maxS.width)
            let h = w / aspect
            return NSSize(width: w, height: h)
        }()

        let initialFrame = NSRect(origin: PIPFloaterWindow.nextCascadeOrigin(size: initialSize),
                                  size: initialSize)
        let win = PIPFloaterWindow(contentRect: initialFrame)
        win.title = title.isEmpty ? "Alma PIP" : "Alma PIP — \(title)"
        win.contentAspectRatio = NSSize(width: aspect, height: 1.0)

        let cv = PIPContentView(frame: NSRect(origin: .zero, size: initialFrame.size),
                                sourceAspect: aspect)
        win.contentView = cv
        self.floater = win
        self.contentView = cv

        // Resolve the matching AX window element (optional — not every
        // window has a usable AX mapping, especially private ViewBridge
        // helpers). Used for the destroy observer below.
        if let p = pid {
            self.axWinElement = PIPSession.resolveAXWindow(pid: p, windowId: windowId)
        } else {
            self.axWinElement = nil
        }

        // SCStream config — continuous at 30fps is plenty for visual feedback
        // and keeps CPU reasonable.
        let filter = SCContentFilter(desktopIndependentWindow: scWindow)
        let config = SCStreamConfiguration()
        // Pixel dims: cap at 2× the initial PIP size so we don't pay for
        // rendering a 4K source into a 300pt floater. Uses point-to-pixel
        // conversion via the main screen's backing scale.
        let scale = NSScreen.main?.backingScaleFactor ?? 2.0
        let pixelCap = Int(max(initialSize.width, initialSize.height) * scale * 2)
        var pixW = Int(srcW * scale)
        var pixH = Int(srcH * scale)
        if max(pixW, pixH) > pixelCap {
            let ratio = Double(pixelCap) / Double(max(pixW, pixH))
            pixW = max(1, Int(Double(pixW) * ratio))
            pixH = max(1, Int(Double(pixH) * ratio))
        }
        config.width = pixW
        config.height = pixH
        config.minimumFrameInterval = CMTime(value: 1, timescale: 30)
        config.showsCursor = true
        config.capturesAudio = false
        config.pixelFormat = kCVPixelFormatType_32BGRA
        config.queueDepth = 3
        if #available(macOS 14.0, *) {
            config.ignoreShadowsSingleWindow = true
        }

        super.init()
        // SCStream's delegate is passed only at init time (no settable
        // `delegate` property), so we create the stream after `super.init`
        // when `self` is available.
        self.stream = SCStream(filter: filter, configuration: config, delegate: self)
        try stream.addStreamOutput(self, type: .screen, sampleHandlerQueue: outputQueue)

        // Close button → clean shutdown.
        win.onUserClose = { [weak self] in self?.shutdown() }
        win.makeKeyAndOrderFront(nil)
        win.orderFrontRegardless()

        stream.startCapture { [weak self] error in
            if let e = error { self?.handleStreamError(e) }
        }

        subscribeToLensBroadcasts()
        installDestroyObserver()
    }

    // MARK: - SCStreamOutput

    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen, sampleBuffer.isValid, CMSampleBufferIsValid(sampleBuffer) else { return }
        // SCStreamFrameInfo `status`: .complete means the frame has new
        // contents. .idle / .blank / .suspended / .started are delivered
        // between frames and carry no pixels — skip them.
        if let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
           let info = attachments.first,
           let statusRaw = info[.status] as? Int,
           let status = SCFrameStatus(rawValue: statusRaw),
           status != .complete
        {
            return
        }
        DispatchQueue.main.async { [weak self] in
            self?.render(sampleBuffer: sampleBuffer)
        }
    }

    // MARK: - SCStreamDelegate

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        handleStreamError(error)
    }

    private func handleStreamError(_ error: Error) {
        // Target window closed, or permission revoked mid-session. Either
        // way the PIP has nothing to show — tear down gracefully.
        log("PIP stream stopped for windowId=\(windowId): \(error.localizedDescription)")
        DispatchQueue.main.async { [weak self] in self?.shutdown() }
    }

    // MARK: - Render + stats

    private func render(sampleBuffer: CMSampleBuffer) {
        if isShuttingDown { return }
        contentView.enqueue(sampleBuffer: sampleBuffer)
        if !hasFirstFrame {
            hasFirstFrame = true
            contentView.hidePlaceholder()
        }
        let now = CACurrentMediaTime()
        lastFrameAt = now
        frameCount += 1
        if now - framesWindowStart >= 1.0 {
            framesWindowStart = now
            frameCount = 0
        }
    }

    // MARK: - Public

    func bringToFront() {
        floater.makeKeyAndOrderFront(nil)
        floater.orderFrontRegardless()
    }

    func shutdown() {
        if isShuttingDown { return }
        isShuttingDown = true
        for token in ncTokens { NotificationCenter.default.removeObserver(token) }
        ncTokens.removeAll()
        tearDownDestroyObserver()
        stream.stopCapture { _ in }
        floater.orderOut(nil)
        floater.onUserClose = nil
        onClose(windowId)
    }

    func statusSnapshot() -> [String: Any?] {
        let f = floater.frame
        return [
            "window_id": Int(windowId),
            "pid": pid.map { Int($0) } as Any?,
            "title": title,
            "has_first_frame": hasFirstFrame,
            "last_frame_age_ms": lastFrameAt == 0 ? nil : Int((CACurrentMediaTime() - lastFrameAt) * 1000),
            "floater_frame": ["x": f.origin.x, "y": f.origin.y, "w": f.size.width, "h": f.size.height],
        ]
    }

    // MARK: - Subsystem 2: virtual cursor mirror

    /// Observe the four `alma.lens.*` broadcasts from `LensOverlay` and
    /// forward each to `PIPLens` with coords mapped into the PIP's local
    /// view space. Any event whose `screenPoint` falls outside the source
    /// window's current bounds is ignored — another PIP (or none) is the
    /// intended recipient.
    private func subscribeToLensBroadcasts() {
        let nc = NotificationCenter.default
        // Broadcasts happen on the posting thread — LensOverlay.showAt may
        // run off-main. We only touch UI in the dispatched closure below.
        let move = nc.addObserver(forName: .almaLensMove, object: nil, queue: nil) { [weak self] note in
            guard let self = self else { return }
            guard let v = note.userInfo?["screenPoint"] as? NSValue else { return }
            let screenPoint = v.pointValue
            let slideMs = note.userInfo?["slideMs"] as? Int ?? 180
            DispatchQueue.main.async { self.handleLensMove(screenPoint: screenPoint, slideMs: slideMs) }
        }
        let click = nc.addObserver(forName: .almaLensClick, object: nil, queue: .main) { [weak self] _ in
            self?.contentView.lens.triggerClick()
        }
        let type = nc.addObserver(forName: .almaLensTypeBadge, object: nil, queue: .main) { [weak self] _ in
            self?.contentView.lens.showBadge(symbol: "keyboard")
        }
        let scroll = nc.addObserver(forName: .almaLensScrollBadge, object: nil, queue: .main) { [weak self] note in
            guard let sym = note.userInfo?["symbol"] as? String else { return }
            self?.contentView.lens.showBadge(symbol: sym)
        }
        let hide = nc.addObserver(forName: .almaLensHide, object: nil, queue: .main) { [weak self] _ in
            self?.contentView.lens.hide()
        }
        ncTokens.append(contentsOf: [move, click, type, scroll, hide])
    }

    private func handleLensMove(screenPoint: CGPoint, slideMs: Int) {
        refreshSrcFrame()
        // Only mirror events targeting THIS window — prevents cross-talk
        // when multiple PIPs are open.
        guard cachedSrcFrame.contains(screenPoint) else {
            contentView.lens.hide()
            return
        }
        let local = mapScreenToView(screenPoint: screenPoint)
        contentView.lens.moveTo(local, slideMs: slideMs)
    }

    /// Re-read the source window's current on-screen bounds. CGWindowList
    /// is cheap (a single CFCopy of window metadata) — fine to call on
    /// every cursor event, which are at most ~30 Hz.
    private func refreshSrcFrame() {
        let opts: CGWindowListOption = [.optionIncludingWindow]
        guard let list = CGWindowListCopyWindowInfo(opts, windowId) as? [[String: Any]],
              let entry = list.first,
              let b = entry[kCGWindowBounds as String] as? [String: Any],
              let x = b["X"] as? CGFloat, let y = b["Y"] as? CGFloat,
              let w = b["Width"] as? CGFloat, let h = b["Height"] as? CGFloat,
              w > 0, h > 0
        else {
            return // keep last known
        }
        cachedSrcFrame = CGRect(x: x, y: y, width: w, height: h)
    }

    /// Convert AX screen-point → PIPContentView coord (Cocoa bottom-left
    /// origin). Two transforms: first normalise inside the source window,
    /// then map into the aspect-fit video rect inside the view, then flip
    /// y to match Cocoa convention.
    private func mapScreenToView(screenPoint: CGPoint) -> CGPoint {
        let src = cachedSrcFrame
        let viewBounds = contentView.bounds
        let aspect = src.width / src.height
        let videoRect = aspectFitRect(aspect: aspect, within: viewBounds)
        let nx = (screenPoint.x - src.minX) / src.width
        let nyTop = (screenPoint.y - src.minY) / src.height // AX y is top-down
        let vx = videoRect.minX + nx * videoRect.width
        // Cocoa bottom-left: bottom has larger distance from top → invert.
        let vy = videoRect.minY + (1 - nyTop) * videoRect.height
        return CGPoint(x: vx, y: vy)
    }

    // MARK: - Subsystem 3 primitive: window destroy observer

    /// Auto-close when the source window goes away. The AX observer must
    /// run on the main thread's CFRunLoop; matches the pattern in
    /// ElementStore.swift.
    private func installDestroyObserver() {
        guard let p = pid, let el = axWinElement else { return }
        var obs: AXObserver?
        let createErr = AXObserverCreate(p, { (_, _, notif, userInfo) in
            let name = notif as String
            guard let info = userInfo else { return }
            let session = Unmanaged<PIPSession>.fromOpaque(info).takeUnretainedValue()
            if name == (kAXUIElementDestroyedNotification as String) {
                DispatchQueue.main.async { session.shutdown() }
            }
        }, &obs)
        guard createErr == .success, let observer = obs else { return }
        let ptr = Unmanaged.passUnretained(self).toOpaque()
        _ = AXObserverAddNotification(observer, el, kAXUIElementDestroyedNotification as CFString, ptr)
        CFRunLoopAddSource(
            CFRunLoopGetMain(),
            AXObserverGetRunLoopSource(observer),
            .defaultMode
        )
        self.axObserver = observer
    }

    private func tearDownDestroyObserver() {
        guard let observer = axObserver else { return }
        CFRunLoopRemoveSource(
            CFRunLoopGetMain(),
            AXObserverGetRunLoopSource(observer),
            .defaultMode
        )
        if let el = axWinElement {
            _ = AXObserverRemoveNotification(observer, el, kAXUIElementDestroyedNotification as CFString)
        }
        axObserver = nil
    }

    /// Walk the app's AX windows and return the element whose CGWindowID
    /// matches. `_AXUIElementGetWindow` is the SPI (already used elsewhere
    /// in AXBridge) that returns the window's quartz id.
    private static func resolveAXWindow(pid: pid_t, windowId: CGWindowID) -> AXUIElement? {
        let appEl = AXUIElementCreateApplication(pid)
        var raw: CFTypeRef?
        guard AXUIElementCopyAttributeValue(appEl, kAXWindowsAttribute as CFString, &raw) == .success,
              let wins = raw as? [AXUIElement]
        else { return nil }
        for w in wins {
            var wid: CGWindowID = 0
            _ = _AXUIElementGetWindow(w, &wid)
            if wid == windowId { return w }
        }
        return nil
    }
}

// MARK: - Floater window

/// Borderless-ish floating window with minimal chrome: a draggable title bar
/// + close button, resizable from corners, aspect-locked. Level `.floating`
/// keeps it above normal windows but BELOW the menu bar (Codex parity —
/// users should always be able to reach the menu bar / Dock). Joins all
/// Spaces + full-screen apps so it stays visible wherever the user is.
final class PIPFloaterWindow: NSWindow {
    var onUserClose: (() -> Void)?

    /// Cascading origin so multiple PIPs don't stack on top of each other.
    private static var cascadeIndex = 0
    private static let cascadeLock = NSLock()

    static func nextCascadeOrigin(size: NSSize) -> NSPoint {
        cascadeLock.lock(); defer { cascadeLock.unlock() }
        let idx = cascadeIndex; cascadeIndex += 1
        guard let screen = NSScreen.main else { return NSPoint(x: 100, y: 100) }
        let vf = screen.visibleFrame
        let step: CGFloat = 28
        // Anchor near the top-right of the visible frame, step down-left with
        // each new PIP. Keeps them clustered yet distinguishable.
        let x = vf.maxX - size.width - 24 - CGFloat(idx) * step
        let y = vf.maxY - size.height - 24 - CGFloat(idx) * step
        return NSPoint(x: max(vf.minX + 12, x), y: max(vf.minY + 12, y))
    }

    init(contentRect: NSRect) {
        super.init(
            contentRect: contentRect,
            styleMask: [.titled, .closable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        isReleasedWhenClosed = false
        level = .floating
        // All-spaces + full-screen-aux: the PIP follows the user, matching
        // LensOverlay's rationale.
        collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary]
        hidesOnDeactivate = false
        isMovableByWindowBackground = true
        titlebarAppearsTransparent = true
        titleVisibility = .visible
        backgroundColor = NSColor.windowBackgroundColor
        hasShadow = true
        minSize = NSSize(width: 200, height: 150)
    }

    override func close() {
        onUserClose?()
        super.close()
    }

    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }
}

// MARK: - Content view

/// Hosts an `AVSampleBufferDisplayLayer` — the canonical path for
/// consuming SCStream CMSampleBuffers directly without per-frame CGImage
/// conversion. A placeholder label is shown until the first frame lands so
/// the user sees "Connecting…" instead of a black rectangle.
final class PIPContentView: NSView {
    private let displayLayer = AVSampleBufferDisplayLayer()
    private let placeholder = NSTextField(labelWithString: "Connecting to window…")
    let lens: PIPLens

    private let sourceAspect: CGFloat

    init(frame frameRect: NSRect, sourceAspect: CGFloat) {
        self.sourceAspect = sourceAspect
        self.lens = PIPLens()
        super.init(frame: frameRect)
        wantsLayer = true
        let root = CALayer()
        root.backgroundColor = NSColor.black.cgColor
        layer = root

        displayLayer.frame = bounds
        displayLayer.videoGravity = .resizeAspect
        displayLayer.backgroundColor = NSColor.black.cgColor
        displayLayer.autoresizingMask = [.layerWidthSizable, .layerHeightSizable]
        root.addSublayer(displayLayer)

        // PIP-local lens on top of the video. The lens owns one parent
        // CALayer; we host it inside the view so autoresizing keeps it
        // tracking the content view's bounds.
        let lensHost = lens.hostLayer
        lensHost.frame = bounds
        lensHost.autoresizingMask = [.layerWidthSizable, .layerHeightSizable]
        root.addSublayer(lensHost)

        placeholder.translatesAutoresizingMaskIntoConstraints = false
        placeholder.textColor = .secondaryLabelColor
        placeholder.font = .systemFont(ofSize: 12, weight: .medium)
        placeholder.alignment = .center
        addSubview(placeholder)
        NSLayoutConstraint.activate([
            placeholder.centerXAnchor.constraint(equalTo: centerXAnchor),
            placeholder.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) { fatalError() }

    override func layout() {
        super.layout()
        displayLayer.frame = bounds
    }

    func enqueue(sampleBuffer: CMSampleBuffer) {
        if displayLayer.status == .failed {
            displayLayer.flush()
        }
        if displayLayer.isReadyForMoreMediaData {
            displayLayer.enqueue(sampleBuffer)
        }
    }

    func hidePlaceholder() {
        placeholder.isHidden = true
    }
}

// MARK: - PIPLens (subsystem 2)

/// Miniature cursor renderer scoped to a single PIP. Intentionally simpler
/// than `LensView` in Overlay.swift: no display-link arrival semaphores,
/// no multi-screen / proximity-fade, no fog — the PIP doesn't need any of
/// those. Just the cursor glyph, a click pulse, and a short-lived badge.
///
/// Renders into a single host `CALayer` that PIPContentView places above
/// the video layer. All coords are Cocoa view coords (bottom-left origin,
/// same as PIPContentView).
final class PIPLens {
    let hostLayer = CALayer()

    private let cursorLayer = CALayer()
    private let pulseLayer = CALayer()
    private let badgeLayer = CALayer()

    private var currentPoint: CGPoint = .zero
    private var visible = false

    init() {
        hostLayer.masksToBounds = false

        cursorLayer.bounds = CGRect(x: 0, y: 0, width: 22, height: 22)
        // Top-left anchor matches the real Mac cursor hotspot.
        cursorLayer.anchorPoint = CGPoint(x: 0, y: 1)
        cursorLayer.contentsGravity = .resizeAspect
        cursorLayer.contentsScale = NSScreen.main?.backingScaleFactor ?? 2.0
        cursorLayer.contents = PIPLens.makeTintedCursorImage(size: cursorLayer.bounds.size)
        cursorLayer.opacity = 0
        hostLayer.addSublayer(cursorLayer)

        // Click pulse: a ring that expands + fades on triggerClick.
        pulseLayer.bounds = CGRect(x: 0, y: 0, width: 30, height: 30)
        pulseLayer.anchorPoint = CGPoint(x: 0.5, y: 0.5)
        pulseLayer.cornerRadius = 15
        pulseLayer.borderWidth = 2
        pulseLayer.borderColor = NSColor.controlAccentColor.cgColor
        pulseLayer.backgroundColor = NSColor.clear.cgColor
        pulseLayer.opacity = 0
        hostLayer.addSublayer(pulseLayer)

        // Badge: a small accent pill with an SF Symbol glyph.
        badgeLayer.bounds = CGRect(x: 0, y: 0, width: 18, height: 18)
        badgeLayer.anchorPoint = CGPoint(x: 0.5, y: 0.5)
        badgeLayer.contentsGravity = .resizeAspect
        badgeLayer.contentsScale = cursorLayer.contentsScale
        badgeLayer.opacity = 0
        hostLayer.addSublayer(badgeLayer)
    }

    func moveTo(_ point: CGPoint, slideMs: Int) {
        currentPoint = point
        let wasVisible = visible
        visible = true
        CATransaction.begin()
        if wasVisible {
            CATransaction.setAnimationDuration(min(0.4, max(0.08, Double(slideMs) / 1000.0)))
            CATransaction.setAnimationTimingFunction(CAMediaTimingFunction(name: .easeInEaseOut))
        } else {
            CATransaction.setDisableActions(true)
        }
        cursorLayer.position = point
        pulseLayer.position = point
        // Badge sits below-right of the cursor hotspot (cursor body extends
        // down-right, so the badge lands clear of it).
        badgeLayer.position = CGPoint(x: point.x + 26, y: point.y - 26)
        cursorLayer.opacity = 1
        CATransaction.commit()
    }

    func triggerClick() {
        visible = true
        cursorLayer.opacity = 1
        let scale = CAKeyframeAnimation(keyPath: "transform.scale")
        scale.values = [1.0, 0.82, 1.04, 1.0]
        scale.keyTimes = [0.0, 0.28, 0.68, 1.0]
        scale.duration = 0.24
        cursorLayer.add(scale, forKey: "press")

        let pulseScale = CAKeyframeAnimation(keyPath: "transform.scale")
        pulseScale.values = [0.6, 1.8]
        pulseScale.duration = 0.42
        pulseScale.timingFunction = CAMediaTimingFunction(name: .easeOut)
        let pulseOp = CAKeyframeAnimation(keyPath: "opacity")
        pulseOp.values = [0.0, 0.85, 0.0]
        pulseOp.keyTimes = [0.0, 0.25, 1.0]
        pulseOp.duration = 0.42
        pulseLayer.add(pulseScale, forKey: "pulseScale")
        pulseLayer.add(pulseOp, forKey: "pulseOp")
    }

    func showBadge(symbol: String) {
        guard let img = PIPLens.makeBadgeImage(symbol: symbol, size: badgeLayer.bounds.size) else { return }
        badgeLayer.contents = img
        let fade = CAKeyframeAnimation(keyPath: "opacity")
        fade.values = [0.0, 1.0, 1.0, 0.0]
        fade.keyTimes = [0.0, 0.15, 0.85, 1.0]
        fade.duration = 0.55
        badgeLayer.add(fade, forKey: "fade")
    }

    func hide() {
        visible = false
        CATransaction.begin()
        CATransaction.setAnimationDuration(0.12)
        cursorLayer.opacity = 0
        CATransaction.commit()
    }

    // MARK: - Images

    /// Same source as `LensView.makeCursorImage` — NSCursor.arrow — but
    /// tinted with an accent-blue overlay so the PIP cursor is obviously
    /// "Alma-driven" and not mistaken for the user's real pointer. (Inside
    /// a PIP the user's real cursor can never appear, so ambiguity is
    /// lower than in Overlay.swift — but consistency is worth the tint.)
    static func makeTintedCursorImage(size: CGSize) -> CGImage? {
        let scale: CGFloat = 2.0
        let pixelW = Int(size.width * scale)
        let pixelH = Int(size.height * scale)
        guard let rep = NSBitmapImageRep(
            bitmapDataPlanes: nil,
            pixelsWide: pixelW, pixelsHigh: pixelH,
            bitsPerSample: 8, samplesPerPixel: 4,
            hasAlpha: true, isPlanar: false,
            colorSpaceName: .deviceRGB,
            bitmapFormat: .alphaFirst,
            bytesPerRow: 0, bitsPerPixel: 32
        ) else { return nil }
        rep.size = size
        guard let ctx = NSGraphicsContext(bitmapImageRep: rep) else { return nil }
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = ctx
        let arrow = NSCursor.arrow.image
        let r = NSRect(origin: .zero, size: size)
        arrow.draw(in: r, from: .zero, operation: .sourceOver, fraction: 1.0)
        // Accent overlay masked by the arrow alpha.
        NSColor.controlAccentColor.set()
        r.fill(using: .sourceAtop)
        NSGraphicsContext.restoreGraphicsState()
        return rep.cgImage
    }

    static func makeBadgeImage(symbol: String, size: CGSize) -> CGImage? {
        let cfg = NSImage.SymbolConfiguration(pointSize: size.width * 0.7, weight: .bold, scale: .medium)
        guard let base = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)?
                .withSymbolConfiguration(cfg) else { return nil }
        let scale: CGFloat = 2.0
        guard let rep = NSBitmapImageRep(
            bitmapDataPlanes: nil,
            pixelsWide: Int(size.width * scale), pixelsHigh: Int(size.height * scale),
            bitsPerSample: 8, samplesPerPixel: 4,
            hasAlpha: true, isPlanar: false,
            colorSpaceName: .deviceRGB,
            bitmapFormat: .alphaFirst,
            bytesPerRow: 0, bitsPerPixel: 32
        ) else { return nil }
        rep.size = size
        guard let ctx = NSGraphicsContext(bitmapImageRep: rep) else { return nil }
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = ctx
        let pill = NSBezierPath(roundedRect: NSRect(origin: .zero, size: size), xRadius: 4, yRadius: 4)
        NSColor.controlAccentColor.setFill()
        pill.fill()
        let tinted = NSImage(size: base.size)
        tinted.lockFocus()
        NSColor.white.set()
        let br = NSRect(origin: .zero, size: base.size)
        base.draw(in: br, from: .zero, operation: .sourceOver, fraction: 1.0)
        br.fill(using: .sourceIn)
        tinted.unlockFocus()
        tinted.draw(in: NSRect(origin: .zero, size: size), from: .zero, operation: .sourceOver, fraction: 1.0)
        NSGraphicsContext.restoreGraphicsState()
        return rep.cgImage
    }
}

// MARK: - Subsystem 3: minimise-hijack armer

/// Registers an AX observer on a specified window so that when the user
/// clicks the minimise button, Alma auto-opens a PIP mirror. One armer
/// per armed window id; destroyed on explicit disarm, on the target
/// window being destroyed, or on the PIP being successfully opened (the
/// arm has served its purpose — the PIP itself owns the lifecycle from
/// there on).
///
/// Armers do NOT fight the minimise: the system animation runs normally,
/// the window lands in the Dock, and the PIP floater simultaneously pops
/// up. SCStream on a minimised window continues to deliver frames (the
/// capture target is the window's off-screen buffer, not its on-screen
/// presentation), so the user gets seamless visibility.
final class PIPMinimizeArmer {
    static let shared = PIPMinimizeArmer()

    private struct Armed {
        let pid: pid_t
        let element: AXUIElement
        let title: String
        let observer: AXObserver
    }

    private var armed: [CGWindowID: Armed] = [:]
    private let lock = NSLock()

    private init() {}

    func arm(windowId: CGWindowID, pid: pid_t, title: String?) throws {
        lock.lock()
        if armed[windowId] != nil {
            lock.unlock()
            return
        }
        lock.unlock()

        let el = PIPSession_resolveAXWindow(pid: pid, windowId: windowId)
        guard let element = el else {
            throw CUError.internalError("No AX window element for windowId=\(windowId) pid=\(pid)")
        }

        var obs: AXObserver?
        let createErr = AXObserverCreate(pid, { (_, axEl, notif, userInfo) in
            let name = notif as String
            guard let info = userInfo else { return }
            let armer = Unmanaged<PIPMinimizeArmer>.fromOpaque(info).takeUnretainedValue()
            // Pull the window_id from the element each fire — keeps the
            // callback stateless so it survives re-arming.
            var wid: CGWindowID = 0
            _ = _AXUIElementGetWindow(axEl, &wid)
            if wid == 0 { return }
            if name == (kAXWindowMiniaturizedNotification as String) {
                DispatchQueue.main.async { armer.onMinimise(windowId: wid) }
            } else if name == (kAXUIElementDestroyedNotification as String) {
                DispatchQueue.main.async { armer.disarm(windowId: wid) }
            }
        }, &obs)
        guard createErr == .success, let observer = obs else {
            throw CUError.internalError("AXObserverCreate failed: \(createErr.rawValue)")
        }
        let ptr = Unmanaged.passUnretained(self).toOpaque()
        _ = AXObserverAddNotification(observer, element, kAXWindowMiniaturizedNotification as CFString, ptr)
        _ = AXObserverAddNotification(observer, element, kAXUIElementDestroyedNotification as CFString, ptr)
        CFRunLoopAddSource(
            CFRunLoopGetMain(),
            AXObserverGetRunLoopSource(observer),
            .defaultMode
        )
        lock.lock()
        armed[windowId] = Armed(pid: pid, element: element, title: title ?? "", observer: observer)
        lock.unlock()
    }

    @discardableResult
    func disarm(windowId: CGWindowID) -> Bool {
        lock.lock()
        let a = armed.removeValue(forKey: windowId)
        lock.unlock()
        guard let entry = a else { return false }
        CFRunLoopRemoveSource(
            CFRunLoopGetMain(),
            AXObserverGetRunLoopSource(entry.observer),
            .defaultMode
        )
        _ = AXObserverRemoveNotification(entry.observer, entry.element, kAXWindowMiniaturizedNotification as CFString)
        _ = AXObserverRemoveNotification(entry.observer, entry.element, kAXUIElementDestroyedNotification as CFString)
        return true
    }

    func disarmAll() {
        lock.lock()
        let all = Array(armed.keys)
        lock.unlock()
        for wid in all { _ = disarm(windowId: wid) }
    }

    func statusList() -> [[String: Any?]] {
        lock.lock(); defer { lock.unlock() }
        return armed.map { (wid, a) in
            [
                "window_id": Int(wid),
                "pid": Int(a.pid),
                "title": a.title,
            ]
        }
    }

    private func onMinimise(windowId: CGWindowID) {
        if PIPManager.shared.hasSession(windowId: windowId) {
            // Already mirrored — the arm has served its purpose.
            disarm(windowId: windowId)
            return
        }
        let entry: Armed? = {
            lock.lock(); defer { lock.unlock() }
            return armed[windowId]
        }()
        guard let a = entry else { return }
        do {
            _ = try PIPManager.shared.open(
                windowId: windowId, pid: a.pid,
                title: a.title.isEmpty ? nil : a.title,
                preferredSize: nil
            )
            disarm(windowId: windowId)
        } catch {
            log("PIP auto-open on minimise failed for wid=\(windowId): \(error)")
        }
    }
}

/// File-scope helper that mirrors `PIPSession.resolveAXWindow` — pulled
/// out so the armer can call it without going through a session. Keeping
/// the private static version in PIPSession preserves readability at the
/// call site inside the session initialiser.
private func PIPSession_resolveAXWindow(pid: pid_t, windowId: CGWindowID) -> AXUIElement? {
    let appEl = AXUIElementCreateApplication(pid)
    var raw: CFTypeRef?
    guard AXUIElementCopyAttributeValue(appEl, kAXWindowsAttribute as CFString, &raw) == .success,
          let wins = raw as? [AXUIElement]
    else { return nil }
    for w in wins {
        var wid: CGWindowID = 0
        _ = _AXUIElementGetWindow(w, &wid)
        if wid == windowId { return w }
    }
    return nil
}

// MARK: - Aspect-fit helper

/// Compute the sub-rect inside `bounds` that preserves `aspect`
/// (width/height). Matches `videoGravity = .resizeAspect` semantics on
/// AVSampleBufferDisplayLayer.
private func aspectFitRect(aspect: CGFloat, within: CGRect) -> CGRect {
    guard aspect > 0, within.width > 0, within.height > 0 else { return within }
    let boundsAspect = within.width / within.height
    if aspect > boundsAspect {
        // Source is wider — letterbox top/bottom.
        let h = within.width / aspect
        let y = within.minY + (within.height - h) / 2
        return CGRect(x: within.minX, y: y, width: within.width, height: h)
    } else {
        // Source is taller — pillarbox left/right.
        let w = within.height * aspect
        let x = within.minX + (within.width - w) / 2
        return CGRect(x: x, y: within.minY, width: w, height: within.height)
    }
}

// MARK: - Logging

private func log(_ s: @autoclosure () -> String) {
    FileHandle.standardError.write(Data((s() + "\n").utf8))
}
