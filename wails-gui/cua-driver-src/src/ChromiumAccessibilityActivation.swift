import ApplicationServices
import Foundation

private let chromiumAXObserverNoopCallback: AXObserverCallbackWithInfo = { _, _, _, _, _ in }

final class ChromiumAccessibilityActivation {
    static let shared = ChromiumAccessibilityActivation()

    private typealias AddNotificationAndCheckRemoteFn = @convention(c) (
        AXObserver,
        AXUIElement,
        CFString,
        UnsafeMutableRawPointer?
    ) -> AXError

    private static let addNotificationAndCheckRemote: AddNotificationAndCheckRemoteFn? = {
        _ = dlopen(
            "/System/Library/Frameworks/ApplicationServices.framework/Frameworks/HIServices.framework/HIServices",
            RTLD_LAZY
        )
        for name in [
            "_AXObserverAddNotificationAndCheckRemote",
            "AXObserverAddNotificationAndCheckRemote",
        ] {
            if let symbol = dlsym(UnsafeMutableRawPointer(bitPattern: -2), name) {
                return unsafeBitCast(symbol, to: AddNotificationAndCheckRemoteFn.self)
            }
        }
        return nil
    }()

    private let lock = NSLock()
    private var activatedPIDs = Set<pid_t>()
    private var observers: [pid_t: AXObserver] = [:]

    func activateIfNeeded(pid: pid_t, root: AXUIElement) {
        lock.lock()
        let alreadyActivated = activatedPIDs.contains(pid)
        lock.unlock()

        guard assertChromiumAccessibility(root: root) else { return }
        guard !alreadyActivated else { return }

        lock.lock()
        let inserted = activatedPIDs.insert(pid).inserted
        lock.unlock()
        guard inserted else { return }

        registerObserver(pid: pid, root: root)
        Thread.sleep(forTimeInterval: 0.5)
    }

    /// Remove and invalidate the observer for a pid (called when the target
    /// process terminates). Detaches its run loop source from the main run
    /// loop and drops our references, so dead AXObservers and their sources
    /// don't accumulate over the daemon's lifetime.
    func deactivate(pid: pid_t) {
        lock.lock()
        let observer = observers.removeValue(forKey: pid)
        activatedPIDs.remove(pid)
        lock.unlock()
        guard let observer else { return }
        if let source = AXObserverGetRunLoopSource(observer) as CFRunLoopSource? {
            CFRunLoopRemoveSource(CFRunLoopGetMain(), source, .defaultMode)
        }
    }

    /// Tear down every registered observer. Called on daemon shutdown so we
    /// don't leave run loop sources dangling on the main run loop.
    func cleanupAll() {
        lock.lock()
        let all = observers
        observers.removeAll()
        activatedPIDs.removeAll()
        lock.unlock()
        for observer in all.values {
            if let source = AXObserverGetRunLoopSource(observer) as CFRunLoopSource? {
                CFRunLoopRemoveSource(CFRunLoopGetMain(), source, .defaultMode)
            }
        }
    }

    private func assertChromiumAccessibility(root: AXUIElement) -> Bool {
        var accepted = false
        for attribute in ["AXManualAccessibility", "AXEnhancedUserInterface"] {
            let result = AXUIElementSetAttributeValue(
                root,
                attribute as CFString,
                kCFBooleanTrue
            )
            accepted = accepted || result == .success
        }
        return accepted
    }

    private func registerObserver(pid: pid_t, root: AXUIElement) {
        var observer: AXObserver?
        guard AXObserverCreateWithInfoCallback(
            pid,
            chromiumAXObserverNoopCallback,
            &observer
        ) == .success, let observer else {
            return
        }

        if let source = AXObserverGetRunLoopSource(observer) as CFRunLoopSource? {
            CFRunLoopAddSource(CFRunLoopGetMain(), source, .defaultMode)
        }

        for notification in notifications {
            _ = addNotification(observer: observer, element: root, notification: notification)
        }

        lock.lock()
        observers[pid] = observer
        lock.unlock()
    }

    private func addNotification(
        observer: AXObserver,
        element: AXUIElement,
        notification: CFString
    ) -> AXError {
        if let fn = Self.addNotificationAndCheckRemote {
            return fn(observer, element, notification, nil)
        }
        return AXObserverAddNotification(observer, element, notification, nil)
    }

    private let notifications: [CFString] = [
        kAXFocusedUIElementChangedNotification as CFString,
        kAXFocusedWindowChangedNotification as CFString,
        kAXApplicationActivatedNotification as CFString,
        kAXApplicationDeactivatedNotification as CFString,
        kAXApplicationHiddenNotification as CFString,
        kAXApplicationShownNotification as CFString,
        kAXWindowCreatedNotification as CFString,
        kAXWindowMovedNotification as CFString,
        kAXWindowResizedNotification as CFString,
        kAXValueChangedNotification as CFString,
        kAXTitleChangedNotification as CFString,
        kAXSelectedChildrenChangedNotification as CFString,
        kAXLayoutChangedNotification as CFString,
    ]
}
