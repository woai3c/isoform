import AppKit
import ApplicationServices
import CoreGraphics
import Foundation

final class BackgroundActivationSession {
    enum TapKind {
        case previous
        case target
    }

    private enum Phase {
        case deliveringToTarget
        case holding
        case finished
    }

    final class TapContext {
        // weak to break the retain cycle with BackgroundActivationSession,
        // which strongly holds these contexts via its `contexts` array.
        // With a strong ref here the session is never deallocated, so deinit
        // — which tears down the event taps and run loop sources — never
        // runs and they leak. finish() still cleans them up on the explicit
        // path; weak just lets deinit serve as a backstop.
        weak var session: BackgroundActivationSession?
        let kind: TapKind

        init(session: BackgroundActivationSession, kind: TapKind) {
            self.session = session
            self.kind = kind
        }
    }

    // Only the focus-message types shouldDrop ever inspects (raw 13/19/20,
    // see isFocusMessage). The tap is an active .defaultTap on the main run
    // loop, so the window server blocks delivery of every matched event
    // until the callback returns. With CGEventMask.max that included
    // keyboard and mouse events for the tapped pids — which the callback
    // always passed through unchanged — needlessly routing the user's
    // typing through a main-thread-gated tap and risking input stalls if
    // this process's main thread is busy. Narrowing to the focus messages
    // is behaviour-preserving (other types were never dropped) and keeps
    // keystrokes off the tap entirely.
    private static let focusSuppressionEventMask: CGEventMask =
        (1 << 13) | (1 << 19) | (1 << 20)
    private let targetPID: pid_t
    private let stateLock = NSLock()
    private var phase: Phase = .deliveringToTarget
    private var taps: [CFMachPort] = []
    private var sources: [CFRunLoopSource] = []
    private var contexts: [TapContext] = []
    private var lastWindowNumber: Int = 0
    private var finished = false
    // The user's frontmost app pid captured at session start. Keyboard
    // delivery re-keys the target window (makeWindowKeyWithoutActivating),
    // which steals the global key-window pointer; on finish we hand it back
    // to this pid so the user's text fields / cursor work again.
    private var restoreFocusPID: pid_t = 0

    private init(targetPID: pid_t) {
        self.targetPID = targetPID
    }

    static func start(targetPID: pid_t) throws -> BackgroundActivationSession {
        let session = BackgroundActivationSession(targetPID: targetPID)
        let previousApp = NSWorkspace.shared.frontmostApplication
        session.restoreFocusPID = previousApp?.processIdentifier ?? 0
        try session.installTapsIfNeeded(previousApp: previousApp)
        return session
    }

    static func activateWindow(targetPID: pid_t, windowNumber: Int, windowFrame: CGRect) {
        postWindowActivationEvent(targetPID: targetPID, windowNumber: windowNumber)
        postWindowCenterPrimer(targetPID: targetPID, windowNumber: windowNumber, windowFrame: windowFrame)
    }

    func activateWindow(windowNumber: Int, windowFrame: CGRect) {
        lastWindowNumber = windowNumber
        beginTargetDelivery()
        Self.activateWindow(targetPID: targetPID, windowNumber: windowNumber, windowFrame: windowFrame)
    }

    func beginTargetDelivery() {
        setPhase(.deliveringToTarget)
    }

    func holdFocusSuppressionUntilFinish() {
        setPhase(.holding)
    }

    func finish() {
        stateLock.lock()
        if finished {
            stateLock.unlock()
            return
        }
        finished = true
        phase = .finished
        let windowNumber = lastWindowNumber
        let tapsToEnd = taps
        let sourcesToEnd = sources
        let contextsToEnd = contexts
        taps.removeAll()
        sources.removeAll()
        contexts.removeAll()
        stateLock.unlock()

        if windowNumber != 0,
           NSWorkspace.shared.frontmostApplication?.processIdentifier != targetPID
        {
            Self.postApplicationFocusEvent(
                targetPID: targetPID,
                subtype: 2,
                windowNumber: windowNumber
            )
        }

        // Tear the taps down ON the main run loop. The tap callback
        // (backgroundActivationEventTapCallback) fires on the main thread,
        // since installTap adds each source to CFRunLoopGetMain(). finish()
        // itself can run on a background queue (handleQueue) or during
        // deinit, so disabling/invalidating the taps here would race a
        // callback that is mid-flight on the main thread — it would
        // takeUnretainedValue() a TapContext we are about to deallocate
        // (use-after-free). Routing teardown through the main run loop
        // serialises it against the callback: the main thread runs one or
        // the other, never both, and contextsToEnd stays alive in the
        // closure until the taps are gone.
        DispatchQueue.main.async {
            for tap in tapsToEnd {
                CGEvent.tapEnable(tap: tap, enable: false)
                CFMachPortInvalidate(tap)
            }
            for source in sourcesToEnd {
                CFRunLoopRemoveSource(CFRunLoopGetMain(), source, .commonModes)
            }
            withExtendedLifetime(contextsToEnd) {}
        }

        // Return the system key window to the user's original frontmost app.
        // Keyboard delivery stole the global key-window pointer via
        // makeWindowKeyWithoutActivating(target); without this, the user's
        // app stays frontmost but its text fields stop accepting input until
        // a manual click. Only re-key when the user's app is STILL the OS
        // frontmost (a pure background operation). If the target was pulled
        // frontmost, the user switched to it deliberately — leave focus be.
        if restoreFocusPID > 0,
           restoreFocusPID != targetPID,
           NSWorkspace.shared.frontmostApplication?.processIdentifier == restoreFocusPID
        {
            _ = makeWindowKeyWithoutActivating(pid: restoreFocusPID)
        }
    }

    deinit {
        finish()
    }

    private func installTapsIfNeeded(previousApp: NSRunningApplication?) throws {
        guard let previousApp, previousApp.processIdentifier != targetPID else { return }
        try installTap(kind: .previous, pid: previousApp.processIdentifier)
        try installTap(kind: .target, pid: targetPID)
    }

    private func installTap(kind: TapKind, pid: pid_t) throws {
        let context = TapContext(session: self, kind: kind)
        let pointer = Unmanaged.passUnretained(context).toOpaque()
        guard let tap = CGEvent.tapCreateForPid(
            pid: pid,
            place: .headInsertEventTap,
            options: .defaultTap,
            eventsOfInterest: Self.focusSuppressionEventMask,
            callback: backgroundActivationEventTapCallback,
            userInfo: pointer
        ) else {
            throw CUError.internalError("failed to install background activation tap for pid \(pid)")
        }
        guard let source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0) else {
            CFMachPortInvalidate(tap)
            throw CUError.internalError("failed to create background activation run loop source for pid \(pid)")
        }
        CFRunLoopAddSource(CFRunLoopGetMain(), source, .commonModes)
        CGEvent.tapEnable(tap: tap, enable: true)
        contexts.append(context)
        taps.append(tap)
        sources.append(source)
    }

    fileprivate func shouldDrop(kind: TapKind, type: CGEventType, event: CGEvent) -> Bool {
        let userData = event.getIntegerValueField(.eventSourceUserData)
        if userData == AlmaSyntheticEventSentinel { return false }
        guard isFocusMessage(type: type) else { return false }
        let currentPhase = stateLock.withLock { phase }
        switch currentPhase {
        case .deliveringToTarget, .holding:
            return kind == .previous
        case .finished:
            return false
        }
    }

    private func isFocusMessage(type: CGEventType) -> Bool {
        type.rawValue == 13 || type.rawValue == 19 || type.rawValue == 20
    }

    private func setPhase(_ newPhase: Phase) {
        stateLock.withLock {
            phase = newPhase
        }
    }

    private static func postWindowActivationEvent(targetPID: pid_t, windowNumber: Int) {
        guard windowNumber != 0 else { return }
        let event = NSEvent.otherEvent(
            with: .appKitDefined,
            location: .zero,
            modifierFlags: [],
            timestamp: 0,
            windowNumber: windowNumber,
            context: nil,
            subtype: 1,
            data1: 0,
            data2: 0
        )?.cgEvent
        guard let event else { return }
        event.setWindowAddressingFields(windowNumber: windowNumber)
        post(event, toPid: targetPID)
        usleep(20_000)
    }

    private static func postApplicationFocusEvent(targetPID: pid_t, subtype: Int16, windowNumber: Int) {
        let event = NSEvent.otherEvent(
            with: .appKitDefined,
            location: .zero,
            modifierFlags: [],
            timestamp: 0,
            windowNumber: windowNumber,
            context: nil,
            subtype: subtype,
            data1: 0,
            data2: 0
        )?.cgEvent
        guard let event else { return }
        event.setWindowAddressingFields(windowNumber: windowNumber)
        post(event, toPid: targetPID)
    }

    private static func postWindowCenterPrimer(targetPID: pid_t, windowNumber: Int, windowFrame: CGRect) {
        guard windowNumber != 0, windowFrame.width > 0, windowFrame.height > 0 else { return }
        let point = CGPoint(x: windowFrame.midX, y: windowFrame.midY)
        postMouse(
            .leftMouseDown,
            targetPID: targetPID,
            windowNumber: windowNumber,
            windowFrame: windowFrame,
            point: point,
            button: .left,
            clickState: 1,
            pressure: 1
        )
        usleep(30_000)
        postMouse(
            .leftMouseUp,
            targetPID: targetPID,
            windowNumber: windowNumber,
            windowFrame: windowFrame,
            point: point,
            button: .left,
            clickState: 1,
            pressure: 0
        )
        usleep(20_000)
    }

    static func click(
        targetPID: pid_t,
        windowNumber: Int,
        windowFrame: CGRect,
        at point: CGPoint,
        button: EventDispatch.MouseButton,
        clickCount: Int
    ) {
        for i in 1...max(1, clickCount) {
            postMouse(
                button.downType,
                targetPID: targetPID,
                windowNumber: windowNumber,
                windowFrame: windowFrame,
                point: point,
                button: button,
                clickState: Int64(i),
                pressure: 1
            )
            usleep(30_000)
            postMouse(
                button.upType,
                targetPID: targetPID,
                windowNumber: windowNumber,
                windowFrame: windowFrame,
                point: point,
                button: button,
                clickState: Int64(i),
                pressure: 0
            )
            if i < clickCount {
                usleep(40_000)
            }
        }
    }

    private static func postMouse(
        _ type: CGEventType,
        targetPID: pid_t,
        windowNumber: Int,
        windowFrame: CGRect,
        point: CGPoint,
        button: EventDispatch.MouseButton,
        clickState: Int64,
        pressure: Double
    ) {
        guard let event = CGEvent(
            mouseEventSource: nil,
            mouseType: type,
            mouseCursorPosition: point,
            mouseButton: button.cgButton
        ) else {
            return
        }
        event.setIntegerValueField(.mouseEventClickState, value: clickState)
        event.setDoubleValueField(.mouseEventPressure, value: pressure)
        event.setIntegerValueField(.eventTargetUnixProcessID, value: Int64(targetPID))
        event.setIntegerValueField(.mouseEventWindowUnderMousePointer, value: Int64(windowNumber))
        event.setIntegerValueField(.mouseEventWindowUnderMousePointerThatCanHandleThisEvent, value: Int64(windowNumber))
        event.setWindowAddressingFields(windowNumber: windowNumber)
        event.location = point
        let quartzWindowPoint = CGPoint(
            x: point.x - windowFrame.minX,
            y: point.y - windowFrame.minY
        )
        SetWindowLocation.fn(event, quartzWindowPoint)
        post(event, toPid: targetPID)
    }

    private static func post(_ event: CGEvent, toPid pid: pid_t) {
        event.setIntegerValueField(.eventSourceUserData, value: AlmaSyntheticEventSentinel)
        SynthPost.fn(pid, event)
    }
}

private let backgroundActivationEventTapCallback: CGEventTapCallBack = { _, type, event, rawContext in
    if type == .tapDisabledByTimeout || type == .tapDisabledByUserInput {
        return Unmanaged.passUnretained(event)
    }
    guard let rawContext else {
        return Unmanaged.passUnretained(event)
    }
    let context = Unmanaged<BackgroundActivationSession.TapContext>.fromOpaque(rawContext).takeUnretainedValue()
    if let session = context.session, session.shouldDrop(kind: context.kind, type: type, event: event) {
        return nil
    }
    return Unmanaged.passUnretained(event)
}

extension CGEvent {
    private static let targetWindowNumberField = CGEventField(rawValue: 51)
    private static let privateWindowRoutingField = CGEventField(rawValue: 58)

    func setWindowAddressingFields(windowNumber: Int) {
        if let windowNumberField = Self.targetWindowNumberField {
            setIntegerValueField(windowNumberField, value: Int64(windowNumber))
        }
        if let privateRoutingField = Self.privateWindowRoutingField {
            setIntegerValueField(privateRoutingField, value: 1)
        }
    }
}

extension NSLock {
    func withLock<T>(_ body: () throws -> T) rethrows -> T {
        lock()
        defer { unlock() }
        return try body()
    }
}
