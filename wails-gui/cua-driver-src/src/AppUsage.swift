import AppKit
import Foundation

/// `list_apps`-style enumeration of apps — not just currently running, but
/// also recently-used apps with a frequency hint. Mirrors Codex's verb.
///
/// Sources:
///   • `NSWorkspace.runningApplications` for the "alive right now" set
///     (with a reliable frontmost flag).
///   • `NSMetadataQuery` over Spotlight's `kMDItemLastUsedDate` +
///     `kMDItemUseCount` to surface apps used in the last 14 days that
///     aren't running.
///
/// We dedupe by bundle identifier: if an app is both running and in the
/// Spotlight index, we take the running entry and fold in the metadata
/// (`use_count`, `last_used_at`).
enum AppUsage {

    struct AppInfo {
        let bundleId: String?
        let name: String
        let pid: pid_t?          // nil for recently-used-but-not-running
        let active: Bool
        let running: Bool
        let path: String?        // absolute .app path when known
        let useCount: Int?       // nil when not indexed
        let lastUsedAt: Date?    // nil when not indexed
    }

    static func listApps(days: Int = 14) -> [AppInfo] {
        var byBundle: [String: AppInfo] = [:]
        var byPathOnly: [AppInfo] = []
        let activePid = NSWorkspace.shared.frontmostApplication?.processIdentifier

        // 1) Running.
        for ra in NSWorkspace.shared.runningApplications where ra.activationPolicy == .regular {
            let bundle = ra.bundleIdentifier
            let info = AppInfo(
                bundleId: bundle,
                name: ra.localizedName ?? "(unknown)",
                pid: ra.processIdentifier,
                active: ra.processIdentifier == activePid,
                running: true,
                path: ra.bundleURL?.path,
                useCount: nil,
                lastUsedAt: nil
            )
            if let b = bundle { byBundle[b] = info } else { byPathOnly.append(info) }
        }

        // 2) Recently used (non-running). Spotlight metadata lookup is
        //    bounded by `days` so we don't walk the entire index.
        for md in recentMetadata(days: days) {
            let bundle = md.bundleId
            if let b = bundle, var existing = byBundle[b] {
                existing = AppInfo(
                    bundleId: existing.bundleId, name: existing.name, pid: existing.pid,
                    active: existing.active, running: existing.running, path: existing.path ?? md.path,
                    useCount: md.useCount, lastUsedAt: md.lastUsedAt
                )
                byBundle[b] = existing
                continue
            }
            let info = AppInfo(
                bundleId: bundle,
                name: md.name,
                pid: nil,
                active: false,
                running: false,
                path: md.path,
                useCount: md.useCount,
                lastUsedAt: md.lastUsedAt
            )
            if let b = bundle { byBundle[b] = info } else { byPathOnly.append(info) }
        }

        let all = Array(byBundle.values) + byPathOnly
        // Rank: active first, then running, then by use count desc, then name.
        return all.sorted { a, b in
            if a.active != b.active { return a.active }
            if a.running != b.running { return a.running }
            let ua = a.useCount ?? -1, ub = b.useCount ?? -1
            if ua != ub { return ua > ub }
            return a.name.lowercased() < b.name.lowercased()
        }
    }

    // MARK: - Spotlight

    private struct MDEntry {
        let bundleId: String?
        let name: String
        let path: String
        let useCount: Int?
        let lastUsedAt: Date?
    }

    private static func recentMetadata(days: Int) -> [MDEntry] {
        // NSMetadataQuery is async by design, but we run it synchronously
        // with a short timeout — we only need a snapshot for list_apps, not
        // live updates. The daemon runs on a background dispatch queue so
        // blocking briefly here is fine.
        let q = NSMetadataQuery()
        q.searchScopes = [NSMetadataQueryLocalComputerScope]
        let cutoff = Date().addingTimeInterval(-Double(days) * 86_400)
        q.predicate = NSPredicate(
            format: "kMDItemContentType == 'com.apple.application-bundle' AND kMDItemLastUsedDate >= %@",
            cutoff as CVarArg
        )
        q.valueListAttributes = [
            "kMDItemDisplayName", "kMDItemCFBundleIdentifier",
            "kMDItemLastUsedDate", "kMDItemUseCount", "kMDItemPath",
        ]
        q.sortDescriptors = [NSSortDescriptor(key: "kMDItemLastUsedDate", ascending: false)]

        let sem = DispatchSemaphore(value: 0)
        var observer: NSObjectProtocol?
        observer = NotificationCenter.default.addObserver(
            forName: .NSMetadataQueryDidFinishGathering,
            object: q, queue: nil
        ) { _ in
            sem.signal()
        }
        // NSMetadataQuery must be started on a thread with a running runloop.
        // Easiest: hop to main, start, stop there too.
        DispatchQueue.main.async { q.start() }
        let timedOut = sem.wait(timeout: .now() + 2.0) == .timedOut
        DispatchQueue.main.async { q.stop() }
        if let o = observer { NotificationCenter.default.removeObserver(o) }
        if timedOut { return [] }

        var out: [MDEntry] = []
        for i in 0..<q.resultCount {
            guard let item = q.result(at: i) as? NSMetadataItem else { continue }
            let bundle = item.value(forAttribute: "kMDItemCFBundleIdentifier") as? String
            let name = (item.value(forAttribute: "kMDItemDisplayName") as? String)
                ?? (item.value(forAttribute: "kMDItemFSName") as? String)
                ?? "(unknown)"
            let path = (item.value(forAttribute: "kMDItemPath") as? String) ?? ""
            let useCount = item.value(forAttribute: "kMDItemUseCount") as? Int
            let lastUsed = item.value(forAttribute: "kMDItemLastUsedDate") as? Date
            out.append(MDEntry(bundleId: bundle, name: name, path: path,
                               useCount: useCount, lastUsedAt: lastUsed))
        }
        return out
    }
}
