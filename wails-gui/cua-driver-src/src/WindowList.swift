import CoreGraphics
import Foundation

/// Bridge to `CGWindowListCopyWindowInfo` — used when the AX tree is empty
/// (Qt / custom-drawn / AX-disabled apps) so we can still find *something*
/// to anchor a screenshot + pixel click against.
///
/// Rationale: AX's `AXWindowsAttribute` is cooperative — if the app hasn't
/// implemented NSAccessibility (or hides windows behind a custom role), we
/// get nothing. CGWindowList doesn't care; it reports any window owned by
/// the pid that's composited on screen.
enum WindowList {
    struct Entry {
        let id: CGWindowID
        let title: String?
        let bounds: CGRect
        let pid: pid_t
        let layer: Int
    }

    /// Topmost on-screen window (smallest layer, largest area) belonging to
    /// `pid`. Ignores Dock/menubar-like windows by filtering out non-zero
    /// layers, which is good enough for real user-facing apps.
    static func topWindow(for pid: pid_t) -> Entry? {
        let entries = windows(for: pid)
        return entries.first { $0.layer == 0 }
            ?? entries.first
    }

    /// Is `windowId` the topmost non-menubar window covering the screen
    /// point `point` (AX coords, top-left origin)?
    ///
    /// Used by the lens overlay to decide whether to render. If some
    /// OTHER app's window sits on top of the target at the point where
    /// we're about to click, the user can't see the target there —
    /// drawing the virtual cursor on top of whatever is actually visible
    /// would put a floating arrow over the user's current app. Better
    /// to skip the lens in that case and just do the click silently
    /// (the PIP window, if enabled, still shows what's happening).
    static func isWindowVisibleAtPoint(windowId: CGWindowID, point: CGPoint) -> Bool {
        let opts: CGWindowListOption = [.excludeDesktopElements, .optionOnScreenOnly]
        guard let arr = CGWindowListCopyWindowInfo(opts, kCGNullWindowID) as? [[String: Any]] else {
            return true // Fall open — assume visible when we can't tell.
        }
        // CGWindowList is ordered front-to-back. Walk it and find the
        // first window whose bounds contain `point` at layer 0 (normal
        // windows, not floating panels / menubars).
        for info in arr {
            let layer = (info[kCGWindowLayer as String] as? Int) ?? 0
            guard layer == 0 else { continue }
            guard let wid = info[kCGWindowNumber as String] as? CGWindowID else { continue }
            guard let boundsDict = info[kCGWindowBounds as String] as? [String: Any] else { continue }
            let rect = CGRect(
                x: (boundsDict["X"] as? CGFloat) ?? 0,
                y: (boundsDict["Y"] as? CGFloat) ?? 0,
                width: (boundsDict["Width"] as? CGFloat) ?? 0,
                height: (boundsDict["Height"] as? CGFloat) ?? 0
            )
            if rect.contains(point) {
                // Topmost window at this point. If it's our target, the
                // target is visible here. If it's some other window,
                // our target is occluded at this point.
                return wid == windowId
            }
        }
        return false
    }

    /// Z-stratum (kCGWindowLayer) of the window. The lens overlay uses
    /// this so it can set its own NSWindow.level to the target's layer,
    /// which means other windows at the same layer with higher order
    /// can naturally occlude the virtual cursor (per Lakr233's bgclick
    /// demo) instead of it hovering at a fixed floating level.
    static func windowLayer(id: CGWindowID) -> Int? {
        return entry(id: id)?.layer
    }

    /// True iff the window is currently composited on-screen. False when
    /// the window is minimised, its app is hidden, or the window has been
    /// closed. Used by LensOverlay to gate the virtual cursor so it's
    /// only visible when the target is visible.
    static func isWindowOnScreen(id: CGWindowID) -> Bool {
        return entry(id: id) != nil
    }

    /// Look up a single window's CGWindowList info by ID, including its
    /// live `kCGWindowName` (the WindowServer's title for the window,
    /// which updates near-instantly on NSWindow.title changes). Used
    /// by Daemon.snapToDict to surface `live_title` alongside the
    /// possibly-stale AX title in get_app_state.
    static func entry(id: CGWindowID) -> Entry? {
        let opts: CGWindowListOption = [.excludeDesktopElements, .optionOnScreenOnly]
        guard let arr = CGWindowListCopyWindowInfo(opts, kCGNullWindowID) as? [[String: Any]] else {
            return nil
        }
        for info in arr {
            guard (info[kCGWindowNumber as String] as? CGWindowID) == id else { continue }
            let pid = (info[kCGWindowOwnerPID as String] as? pid_t) ?? 0
            let boundsDict = (info[kCGWindowBounds as String] as? [String: Any]) ?? [:]
            let rect = CGRect(
                x: (boundsDict["X"] as? CGFloat) ?? 0,
                y: (boundsDict["Y"] as? CGFloat) ?? 0,
                width: (boundsDict["Width"] as? CGFloat) ?? 0,
                height: (boundsDict["Height"] as? CGFloat) ?? 0
            )
            let title = info[kCGWindowName as String] as? String
            let layer = (info[kCGWindowLayer as String] as? Int) ?? 0
            return Entry(id: id, title: title, bounds: rect, pid: pid, layer: layer)
        }
        return nil
    }

    static func windows(for pid: pid_t) -> [Entry] {
        let opts: CGWindowListOption = [.excludeDesktopElements, .optionOnScreenOnly]
        guard let arr = CGWindowListCopyWindowInfo(opts, kCGNullWindowID) as? [[String: Any]] else {
            return []
        }
        var out: [Entry] = []
        for info in arr {
            guard
                let owner = info[kCGWindowOwnerPID as String] as? pid_t,
                owner == pid,
                let wid = info[kCGWindowNumber as String] as? CGWindowID,
                let boundsDict = info[kCGWindowBounds as String] as? [String: Any]
            else { continue }
            let rect = CGRect(
                x: (boundsDict["X"] as? CGFloat) ?? 0,
                y: (boundsDict["Y"] as? CGFloat) ?? 0,
                width: (boundsDict["Width"] as? CGFloat) ?? 0,
                height: (boundsDict["Height"] as? CGFloat) ?? 0
            )
            // Skip trivially sized windows — menubar overlays, tooltips, etc.
            if rect.width < 40 || rect.height < 40 { continue }
            let layer = (info[kCGWindowLayer as String] as? Int) ?? 0
            let title = info[kCGWindowName as String] as? String
            out.append(Entry(id: wid, title: title, bounds: rect, pid: pid, layer: layer))
        }
        // Topmost (smallest layer) first, then largest area.
        return out.sorted {
            if $0.layer != $1.layer { return $0.layer < $1.layer }
            return ($0.bounds.width * $0.bounds.height) > ($1.bounds.width * $1.bounds.height)
        }
    }
}
