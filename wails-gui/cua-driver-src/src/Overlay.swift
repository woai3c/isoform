import AppKit
import Foundation
import QuartzCore

/// Broadcast channel so the PIP mirror lens (PIPWindow.swift) can shadow
/// every real-lens event without tight coupling. The object sender is the
/// LensOverlay singleton; observers hop to main as needed. Payloads use
/// NSValue(point:) for CGPoint because Notification userInfo can't carry
/// non-Hashable non-NSObject values directly.
extension Notification.Name {
    static let almaLensMove = Notification.Name("alma.lens.move")
    static let almaLensClick = Notification.Name("alma.lens.click")
    static let almaLensTypeBadge = Notification.Name("alma.lens.typeBadge")
    static let almaLensScrollBadge = Notification.Name("alma.lens.scrollBadge")
    static let almaLensHide = Notification.Name("alma.lens.hide")
}

/// A transparent, click-through overlay that renders a virtual cursor
/// wherever Alma is about to act. Visual style matches Lakr233's
/// bgclick-rev demo: flat blue pointer glyph with a white stroke, a red
/// "+" crosshair at the tip for precision, and a persistent red "+" mark
/// left at each click location.
///
/// Window ordering (Lakr233 trick): the overlay's NSWindow level is set
/// to match `kCGWindowLayer` of the target window, and the overlay is
/// ordered directly above the target via `order(.above, relativeTo:)`.
/// Side effect: when the user drags another window over the target, the
/// cursor is naturally occluded — feels glued to the target's z-stratum
/// instead of floating on a HUD layer.
///
/// Visibility gate: when the target window is minimised, closed, or its
/// app is hidden, `CGWindowListCopyWindowInfo(.optionOnScreenOnly)` stops
/// returning it — we detect this and hide the cursor. A lightweight timer
/// polls while the cursor is visible so we also react mid-action.
final class LensOverlay {
    static let shared = LensOverlay()

    /// One LensWindow per NSScreen. Keyed by screen identity. Lazily
    /// created when first needed on a given display; reconciled on
    /// `NSApplication.didChangeScreenParameters`.
    private struct Slot {
        let window: NSWindow
        let view: LensView
    }
    private var slots: [ObjectIdentifier: Slot] = [:]

    /// Screen currently hosting the visible virtual cursor. `nil` when
    /// the cursor is hidden.
    private var activeScreen: NSScreen?

    private var activeSlot: Slot? {
        guard let s = activeScreen else { return nil }
        return slots[ObjectIdentifier(s)]
    }

    /// Target window id. When non-nil, the active lens window matches
    /// the target's CGWindowLayer and is ordered directly above the
    /// target in the global z-stack. Set by `setTargetWindow(id:)` before
    /// each action.
    private var targetWindowID: CGWindowID?

    private var enabled = true
    private let lock = NSLock()

    /// Polls target-window visibility while the cursor is on screen.
    private var visibilityTimer: Timer?

    /// Last screen point (AX coords, top-left origin) we rendered at —
    /// used by the visibility timer to re-check point-level occlusion.
    private var lastShownPoint: CGPoint?

    private var reduceMotion: Bool = {
        NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
    }()

    private init() {
        NotificationCenter.default.addObserver(
            forName: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification,
            object: NSWorkspace.shared, queue: .main
        ) { [weak self] _ in
            self?.reduceMotion = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        }
        NotificationCenter.default.addObserver(
            forName: NSApplication.didChangeScreenParametersNotification,
            object: nil, queue: .main
        ) { [weak self] _ in
            self?.reconcileScreens()
        }
    }

    func setEnabled(_ v: Bool) {
        lock.lock(); defer { lock.unlock() }
        enabled = v
        if !v { hideImmediately() }
    }

    var isEnabled: Bool {
        lock.lock(); defer { lock.unlock() }
        return enabled
    }

    /// Kept for back-compat with the `lens` daemon cmd — the fog
    /// backdrop was removed when the visuals were simplified to match
    /// bgclick-rev. Calling this is a no-op.
    func setFogEnabled(_ v: Bool) {
        _ = v
    }

    var isFogEnabled: Bool { false }

    // MARK: - Public API

    /// Tell the overlay which foreign window is the current target. The
    /// overlay uses this to (a) match the target's z-stratum via
    /// NSWindow.level, (b) order above that window, and (c) hide itself
    /// when the target disappears from screen. Pass `nil` to return to
    /// a plain floating overlay.
    func setTargetWindow(id: CGWindowID?) {
        DispatchQueue.main.async {
            self.targetWindowID = id
            self.restackActive()
        }
    }

    /// Move the virtual cursor to a point given in AX screen coordinates
    /// (top-left origin). Blocks the caller for the animation duration
    /// so the subsequent action is visible rather than instantaneous.
    ///
    /// Visibility gate: the cursor is rendered only if the target window
    /// is the topmost non-desktop window at `point`. Three cases this
    /// rejects:
    ///   1. Target is minimised / hidden / closed (not in on-screen list).
    ///   2. Target is on another Space (same — not in on-screen list for
    ///      the current desktop).
    ///   3. Target is on-screen but another app's window covers it at
    ///      `point` (`.optionOnScreenOnly` returns it, but it's buried).
    /// Case 3 is the important one: without it, the cursor would end up
    /// drawn over a DIFFERENT app's content at the same canvas position,
    /// looking like Alma is clicking the wrong app.
    ///
    /// The PIP mirror still receives the broadcast — its window is an
    /// independent viewport and may be visible even when the real target
    /// isn't.
    func showAt(axPoint point: CGPoint, slideMs: Int = 180) {
        guard isEnabled else { return }
        NotificationCenter.default.post(
            name: .almaLensMove, object: self,
            userInfo: ["screenPoint": NSValue(point: point), "slideMs": slideMs]
        )
        if let wid = targetWindowID,
           !WindowList.isWindowVisibleAtPoint(windowId: wid, point: point)
        {
            DispatchQueue.main.async {
                self.lastShownPoint = nil
                self.fadeOutAndOrderOut()
            }
            return
        }
        lastShownPoint = point
        let (target, screen) = axPointToCocoa(point)
        let dur = runMainBlocking { [unowned self] in
            self.activate(on: screen)
            return self.activeSlot?.view.moveCursor(
                to: target, preferredMs: slideMs,
                reduceMotion: reduceMotion
            ) ?? 0
        }
        if dur > 0 { Thread.sleep(forTimeInterval: dur + 0.02) }
    }

    /// Play the click indicator (drops a persistent red "+" marker at
    /// the current cursor tip). Blocks briefly so the mark is visible.
    func clickPulse() {
        guard isEnabled else { return }
        NotificationCenter.default.post(name: .almaLensClick, object: self)
        _ = runMainBlocking { [unowned self] in
            self.activeSlot?.view.triggerClick(reduceMotion: reduceMotion)
            return 0
        }
        Thread.sleep(forTimeInterval: reduceMotion ? 0.06 : 0.18)
    }

    /// Show a "typing" badge next to the cursor (keyboard glyph).
    func typeBadge() {
        guard isEnabled else { return }
        NotificationCenter.default.post(name: .almaLensTypeBadge, object: self)
        DispatchQueue.main.async { self.activeSlot?.view.showBadge(symbol: "keyboard") }
    }

    /// Show a directional arrow badge indicating scroll direction.
    func scrollBadge(direction: String) {
        guard isEnabled else { return }
        let symbol: String
        switch direction.lowercased() {
        case "up": symbol = "arrow.up"
        case "down": symbol = "arrow.down"
        case "left": symbol = "arrow.left"
        case "right": symbol = "arrow.right"
        default: return
        }
        NotificationCenter.default.post(
            name: .almaLensScrollBadge, object: self,
            userInfo: ["symbol": symbol]
        )
        DispatchQueue.main.async { self.activeSlot?.view.showBadge(symbol: symbol) }
    }

    /// Fade out and hide. Also clears accumulated click marks so the
    /// next session starts with a clean canvas.
    func hide() {
        guard isEnabled else { return }
        NotificationCenter.default.post(name: .almaLensHide, object: self)
        DispatchQueue.main.async { self.fadeOutAndOrderOut() }
    }

    // MARK: - Window lifecycle

    private func activate(on screen: NSScreen) {
        if let prev = activeScreen, prev !== screen, let prevSlot = slots[ObjectIdentifier(prev)] {
            prevSlot.window.animator().alphaValue = 0
        }
        let key = ObjectIdentifier(screen)
        let slot = slots[key] ?? makeSlot(for: screen)
        if slots[key] == nil { slots[key] = slot }
        activeScreen = screen
        applyWindowOrdering(slot: slot)
        startVisibilityWatch()
    }

    private func makeSlot(for screen: NSScreen) -> Slot {
        let w = NSWindow(contentRect: screen.frame, styleMask: [.borderless], backing: .buffered, defer: false)
        w.isOpaque = false
        w.backgroundColor = .clear
        w.hasShadow = false
        w.level = .floating
        w.ignoresMouseEvents = true
        // The virtual cursor follows the USER, not the target app — we
        // ALWAYS want it on the user's current Space.
        w.collectionBehavior = [.canJoinAllSpaces, .transient, .ignoresCycle, .fullScreenAuxiliary, .stationary]
        w.hidesOnDeactivate = false
        w.alphaValue = 0.0
        w.setFrame(screen.frame, display: false)

        let contentRect = NSRect(origin: .zero, size: screen.frame.size)
        let v = LensView(frame: contentRect)
        w.contentView = v

        return Slot(window: w, view: v)
    }

    /// Apply the current target-window z-binding to `slot`. When a
    /// target is set: level = target's CGWindowLayer, ordered directly
    /// above the target. This is the Lakr233 bgclick-rev trick — the
    /// panel lives in the target's z-stratum, so other windows at the
    /// same or higher layers can occlude it naturally.
    ///
    /// When no target is set: stay at the NSWindow floating level as a
    /// plain on-top overlay (only used by coord-only clicks without a
    /// prior snapshot).
    private func applyWindowOrdering(slot: Slot) {
        if let wid = targetWindowID {
            let targetLayer = WindowList.windowLayer(id: wid)
                ?? Int(CGWindowLevelForKey(.normalWindow))
            slot.window.level = NSWindow.Level(rawValue: targetLayer)
            slot.window.order(.above, relativeTo: Int(wid))
        } else {
            slot.window.level = .floating
            slot.window.orderFrontRegardless()
        }
    }

    private func restackActive() {
        guard let slot = activeSlot else { return }
        applyWindowOrdering(slot: slot)
    }

    private func reconcileScreens() {
        let live = Set(NSScreen.screens.map { ObjectIdentifier($0) })
        for (key, slot) in slots where !live.contains(key) {
            slot.window.orderOut(nil)
            slots.removeValue(forKey: key)
        }
        if let active = activeScreen, !live.contains(ObjectIdentifier(active)) {
            activeScreen = nil
        }
    }

    private func tearDownAll() {
        stopVisibilityWatch()
        for slot in slots.values {
            slot.view.clearMarks()
            slot.window.orderOut(nil)
        }
        slots.removeAll()
        activeScreen = nil
    }

    private func fadeOutAndOrderOut() {
        guard let slot = activeSlot else { return }
        let w = slot.window
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = reduceMotion ? 0.08 : 0.14
            ctx.timingFunction = CAMediaTimingFunction(name: .easeIn)
            w.animator().alphaValue = 0
        }, completionHandler: { [weak self] in
            guard let self = self else { return }
            slot.view.clearMarks()
            w.orderOut(nil)
            self.activeScreen = nil
            self.lastShownPoint = nil
            self.stopVisibilityWatch()
        })
    }

    private func hideImmediately() {
        DispatchQueue.main.async { self.tearDownAll() }
    }

    // MARK: - Visibility watcher

    /// Poll the target window's visibility every 250ms while the cursor
    /// is on screen. Two hide triggers:
    ///   - Target not composited at all (minimised, closed, other Space).
    ///   - Target composited but NOT the topmost window at the cursor
    ///     point (user dragged another app over the target mid-action —
    ///     continuing to draw would put the cursor on the other app's
    ///     content, which is exactly the "clicking on other apps" bug).
    private func startVisibilityWatch() {
        stopVisibilityWatch()
        guard targetWindowID != nil else { return }
        let t = Timer(timeInterval: 0.25, repeats: true) { [weak self] _ in
            guard let self = self, let wid = self.targetWindowID else { return }
            if !WindowList.isWindowOnScreen(id: wid) {
                self.fadeOutAndOrderOut()
                return
            }
            if let p = self.lastShownPoint,
               !WindowList.isWindowVisibleAtPoint(windowId: wid, point: p)
            {
                self.fadeOutAndOrderOut()
            }
        }
        RunLoop.main.add(t, forMode: .common)
        visibilityTimer = t
    }

    private func stopVisibilityWatch() {
        visibilityTimer?.invalidate()
        visibilityTimer = nil
    }

    // MARK: - Coord conversion

    /// Convert an AX top-left-origin screen point to a point in the
    /// containing screen's Cocoa window-local coord system (bottom-left
    /// origin, y=0 at screen bottom). Returns the target screen too so
    /// the overlay window can be opened on the correct display.
    private func axPointToCocoa(_ p: CGPoint) -> (pointInWindow: NSPoint, screen: NSScreen) {
        let screens = NSScreen.screens
        let primary = screens.first ?? NSScreen.main ?? NSScreen()
        let cocoaY = primary.frame.maxY - p.y
        let cocoaPoint = NSPoint(x: p.x, y: cocoaY)
        let targetScreen = screens.first(where: { $0.frame.contains(cocoaPoint) }) ?? primary
        let windowLocal = NSPoint(
            x: cocoaPoint.x - targetScreen.frame.origin.x,
            y: cocoaPoint.y - targetScreen.frame.origin.y
        )
        return (windowLocal, targetScreen)
    }

    // MARK: - Utilities

    @discardableResult
    private func runMainBlocking<T>(_ body: @escaping () -> T) -> T {
        if Thread.isMainThread { return body() }
        let sem = DispatchSemaphore(value: 0)
        var out: T!
        DispatchQueue.main.async {
            out = body()
            sem.signal()
        }
        sem.wait()
        return out
    }
}

// MARK: - LensView
//
// Flat pointer glyph + precision "+" crosshair at the tip + persistent
// click marks. Everything is Core Animation.

private final class LensView: NSView {
    private let cursorLayer = CALayer()
    /// Red "+" crosshair anchored at the cursor tip — shows the exact
    /// click position (matches Lakr233's bgclick-rev demo).
    private let precisionLayer = CAShapeLayer()
    private let badgeLayer = CALayer()
    /// Container for the persistent red "+" marks dropped at each
    /// click location.
    private let marksRoot = CALayer()

    private var cursorPoint: CGPoint = .zero

    private var cursorSize: CGSize { CGSize(width: 26, height: 26) }
    private var precisionSize: CGFloat { 9 }
    private var markSize: CGFloat { 11 }

    /// Mac arrow cursor tip is at top-left of the image — standard
    /// hotspot convention.
    private var tipAnchor: CGPoint { CGPoint(x: 0.0, y: 1.0) }

    var cursorPositionInView: CGPoint { cursorPoint }

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        wantsLayer = true
        let root = CALayer()
        root.frame = bounds
        layer = root
        root.masksToBounds = false

        // Persistent click marks — bottom of the stack so the live
        // cursor draws above them.
        marksRoot.frame = bounds
        root.addSublayer(marksRoot)

        // Cursor glyph.
        cursorLayer.bounds = CGRect(origin: .zero, size: cursorSize)
        cursorLayer.anchorPoint = tipAnchor
        cursorLayer.contentsGravity = .resizeAspect
        cursorLayer.contentsScale = NSScreen.main?.backingScaleFactor ?? 2.0
        cursorLayer.contents = Self.makeFlatCursorImage(size: cursorSize)
        root.addSublayer(cursorLayer)

        // Precision crosshair at the tip.
        precisionLayer.bounds = CGRect(x: 0, y: 0, width: precisionSize, height: precisionSize)
        precisionLayer.anchorPoint = CGPoint(x: 0.5, y: 0.5)
        precisionLayer.path = Self.plusPath(size: precisionSize, thickness: 1.2)
        precisionLayer.fillColor = NSColor.systemRed.cgColor
        precisionLayer.strokeColor = nil
        precisionLayer.opacity = 0.95
        root.addSublayer(precisionLayer)

        // Type / scroll badge.
        badgeLayer.bounds = CGRect(origin: .zero, size: CGSize(width: 22, height: 22))
        badgeLayer.anchorPoint = CGPoint(x: 0.5, y: 0.5)
        badgeLayer.contentsGravity = .resizeAspect
        badgeLayer.contentsScale = cursorLayer.contentsScale
        badgeLayer.opacity = 0
        root.addSublayer(badgeLayer)
    }

    required init?(coder: NSCoder) { fatalError() }

    /// Move the cursor to a new point. Returns the animation duration in
    /// seconds (caller blocks briefly on this so the motion is visible).
    @discardableResult
    func moveCursor(to point: CGPoint, preferredMs: Int, reduceMotion: Bool) -> TimeInterval {
        guard let win = window else { return 0 }
        let from = cursorPoint
        cursorPoint = point

        let wasVisible = win.isVisible && win.alphaValue > 0
        if !wasVisible {
            placeImmediately(at: point)
            win.orderFrontRegardless()
            NSAnimationContext.runAnimationGroup { ctx in
                ctx.duration = reduceMotion ? 0.08 : 0.14
                ctx.timingFunction = CAMediaTimingFunction(name: .easeOut)
                win.animator().alphaValue = 1.0
            }
            return reduceMotion ? 0.08 : 0.16
        }

        if reduceMotion {
            placeImmediately(at: point)
            return 0
        }

        let distance = hypot(point.x - from.x, point.y - from.y)
        // Fitts-ish: min 120ms for short hops, capped at 420ms for long
        // sweeps. Sub-linear in distance — feels hand-like.
        let duration = max(0.12, min(0.42, 0.10 + sqrt(distance) / 100))

        CATransaction.begin()
        CATransaction.setAnimationDuration(duration)
        CATransaction.setAnimationTimingFunction(
            CAMediaTimingFunction(controlPoints: 0.22, 1.0, 0.36, 1.0)
        )
        cursorLayer.position = point
        precisionLayer.position = point
        badgeLayer.position = CGPoint(x: point.x + 26, y: point.y - 26)
        CATransaction.commit()
        return duration
    }

    private func placeImmediately(at point: CGPoint) {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        cursorLayer.position = point
        precisionLayer.position = point
        badgeLayer.position = CGPoint(x: point.x + 26, y: point.y - 26)
        CATransaction.commit()
    }

    /// Drop a persistent red "+" at the current cursor tip and play a
    /// small press animation on the cursor itself.
    func triggerClick(reduceMotion: Bool) {
        dropMark(at: cursorPoint)
        if reduceMotion { return }
        let press = CAKeyframeAnimation(keyPath: "transform.scale")
        press.values = [1.0, 0.82, 1.05, 1.0]
        press.keyTimes = [0.0, 0.28, 0.68, 1.0]
        press.duration = 0.22
        press.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
        cursorLayer.add(press, forKey: "press")
    }

    private func dropMark(at point: CGPoint) {
        let m = CAShapeLayer()
        let sz = markSize
        m.bounds = CGRect(x: 0, y: 0, width: sz, height: sz)
        m.anchorPoint = CGPoint(x: 0.5, y: 0.5)
        m.position = point
        m.path = Self.plusPath(size: sz, thickness: 1.6)
        m.fillColor = NSColor.systemRed.cgColor
        marksRoot.addSublayer(m)
        let pop = CAKeyframeAnimation(keyPath: "transform.scale")
        pop.values = [0.2, 1.25, 1.0]
        pop.keyTimes = [0.0, 0.55, 1.0]
        pop.duration = 0.22
        m.add(pop, forKey: "pop")
    }

    func clearMarks() {
        for sub in marksRoot.sublayers ?? [] { sub.removeFromSuperlayer() }
    }

    func showBadge(symbol: String) {
        guard let img = Self.makeBadgeImage(symbol: symbol, size: badgeLayer.bounds.size) else { return }
        badgeLayer.contents = img
        let offset = CGPoint(x: cursorPoint.x + 30, y: cursorPoint.y - 30)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        badgeLayer.position = offset
        CATransaction.commit()
        let fade = CAKeyframeAnimation(keyPath: "opacity")
        fade.values = [0.0, 1.0, 1.0, 0.0]
        fade.keyTimes = [0.0, 0.15, 0.85, 1.0]
        fade.duration = 0.55
        fade.isRemovedOnCompletion = true
        badgeLayer.add(fade, forKey: "badgeFade")
    }

    // MARK: - Image factories

    /// Two-rectangle "+" path centred in a `size`×`size` bounding box.
    static func plusPath(size: CGFloat, thickness: CGFloat) -> CGPath {
        let p = CGMutablePath()
        p.addRect(CGRect(x: 0, y: (size - thickness) / 2, width: size, height: thickness))
        p.addRect(CGRect(x: (size - thickness) / 2, y: 0, width: thickness, height: size))
        return p
    }

    /// Flat blue pointer glyph with a white stroke. Built by stamping
    /// the system arrow tinted white at 8 neighbour offsets (creates a
    /// 1pt halo) then overlaying the same shape in solid blue.
    static func makeFlatCursorImage(size: CGSize) -> CGImage? {
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

        let arrow = NSCursor.arrow.image
        let r = NSRect(origin: .zero, size: size)

        let white = tintedImage(arrow, color: .white)
        for dx in [-1, 0, 1] as [CGFloat] {
            for dy in [-1, 0, 1] as [CGFloat] {
                if dx == 0 && dy == 0 { continue }
                white.draw(
                    in: r.offsetBy(dx: dx, dy: dy),
                    from: .zero, operation: .sourceOver, fraction: 1.0
                )
            }
        }

        let blue = NSColor(calibratedRed: 0.14, green: 0.44, blue: 1.0, alpha: 1.0)
        let tintedBlue = tintedImage(arrow, color: blue)
        tintedBlue.draw(in: r, from: .zero, operation: .sourceOver, fraction: 1.0)

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
        let pill = NSBezierPath(roundedRect: NSRect(origin: .zero, size: size), xRadius: 5, yRadius: 5)
        NSColor(calibratedRed: 0.14, green: 0.44, blue: 1.0, alpha: 1.0).setFill()
        pill.fill()
        let white = tintedImage(base, color: .white)
        let target = NSRect(
            origin: CGPoint(
                x: (size.width - base.size.width) / 2,
                y: (size.height - base.size.height) / 2
            ),
            size: base.size
        )
        white.draw(in: target, from: .zero, operation: .sourceOver, fraction: 1.0)
        NSGraphicsContext.restoreGraphicsState()
        return rep.cgImage
    }

    static func tintedImage(_ image: NSImage, color: NSColor) -> NSImage {
        let tinted = NSImage(size: image.size)
        tinted.lockFocus()
        color.set()
        let rect = NSRect(origin: .zero, size: image.size)
        image.draw(in: rect, from: .zero, operation: .sourceOver, fraction: 1.0)
        rect.fill(using: .sourceIn)
        tinted.unlockFocus()
        return tinted
    }
}
