import ApplicationServices
import Foundation

/// Identity descriptor captured at register-time. Lets us find an
/// "equivalent" live AXUIElement after the original was destroyed — the
/// mechanism behind Codex's `RefetchableSkyshotAXTree`.
///
/// The fields are chosen so that together they're usually unique within
/// a window, but individually tolerant to minor UI redraws:
///   - role, subrole, identifier  → invariant under content change
///   - title, roleDescription     → stable for button-like widgets
///   - parentRoles                → sibling disambiguation when multiple
///     buttons share a title (e.g. table rows)
///
/// Capture cost: the values are already read during `walk`; descriptors
/// just record them. No extra AX calls at register time.
struct ElementDescriptor: Equatable, Hashable {
    let role: String
    let subrole: String?
    let title: String?
    let identifier: String?
    let roleDescription: String?
    /// Role chain from the element's direct parent up to (but excluding)
    /// the application root. Truncated at depth 8 — deeper hierarchies
    /// are rare and the extra depth adds little uniqueness.
    let parentRoles: [String]
}

/// Maps opaque refs (e1, e2, …) to live AXUIElements.
///
/// One snapshot at a time, per process (per PID of the target app).
/// Rationale: we want LLMs to operate on refs, never coordinates.
///
/// Stale detection has two tiers:
///   1. Cheap: before every action we re-query an attribute; if AX returns
///      `.invalidUIElement` the ref is stale.
///   2. Recovery: if a descriptor was captured at register time, we walk
///      the current AX tree looking for a matching element and, if
///      exactly one matches, silently rebind the ref. Multiple matches
///      surface `ref_stale` so the agent re-snaps rather than guessing.
final class ElementStore {
    struct Entry {
        var element: AXUIElement
        let snapshotId: String
        let pid: pid_t
        let descriptor: ElementDescriptor?
        /// Set by the AXObserver callback when the element is
        /// destroyed. `resolveRef` checks this before the cheap
        /// AX liveness probe, so we can skip straight to refetch
        /// when we already know the element is gone.
        var invalidated: Bool = false
    }

    private var table: [String: Entry] = [:]
    private var counter: Int = 0
    private let lock = NSLock()

    /// One AXObserver per target pid, reused across all elements of
    /// that pid. Ambient monitoring for `kAXUIElementDestroyedNotification`
    /// — matches Codex's `UIElementTreeInvalidationMonitor`. On-demand
    /// refetch (via cheap liveness probe in `resolveRef`) still runs
    /// as backup for the window where the notification hasn't been
    /// delivered yet.
    private var observers: [pid_t: AXObserver] = [:]

    /// Refs minted during the current snapshot — used to dedupe when
    /// `walk` visits the same live AXUIElement that was already assigned
    /// a ref in a previous snapshot.
    private var currentSnapshotRefs: Set<String> = []

    /// Matches Codex's UIElementTreeInvalidationMonitor: on a new
    /// snapshot we prune refs whose underlying AXUIElement is no longer
    /// valid (window closed, element destroyed, …) but KEEP valid refs
    /// with their existing IDs. This way e.g. `e7` before and after
    /// clicking a toolbar button still refers to the same element,
    /// letting the model chain actions without a round-trip re-snap.
    func beginSnapshot() -> String {
        lock.lock(); defer { lock.unlock() }
        currentSnapshotRefs.removeAll(keepingCapacity: true)
        // Prune refs whose element is gone. A single cheap attribute
        // read per entry: invalidUIElement → drop; anything else → keep.
        var stale: [String] = []
        for (ref, entry) in table {
            var raw: CFTypeRef?
            let err = AXUIElementCopyAttributeValue(entry.element, kAXRoleAttribute as CFString, &raw)
            if err == .invalidUIElement {
                stale.append(ref)
            }
        }
        for ref in stale { table.removeValue(forKey: ref) }
        return "s_" + ISO8601DateFormatter().string(from: Date()).replacingOccurrences(of: ":", with: "")
    }

    /// Register `element`, reusing an existing ref if the same AXUIElement
    /// was registered in a prior snapshot (matched by CFEqual). Otherwise
    /// mint a fresh ref.
    ///
    /// `descriptor` is optional to stay backward-compatible with any
    /// caller that doesn't have identity info handy. Omit it and we
    /// lose the refetch ability; that path is still correct, just not
    /// resilient to UI churn.
    func register(
        _ element: AXUIElement,
        snapshotId: String,
        pid: pid_t,
        descriptor: ElementDescriptor? = nil
    ) -> String {
        lock.lock(); defer { lock.unlock() }
        // Look for a surviving entry that points at the same element.
        for (ref, entry) in table {
            if entry.pid == pid, CFEqual(entry.element, element), !currentSnapshotRefs.contains(ref) {
                // Refresh the entry's snapshotId so stale-detection in
                // resolveRef knows this ref was seen in the latest snap.
                // Also update the descriptor — labels/identifiers can
                // shift between snapshots (e.g. dynamic button text).
                table[ref] = Entry(
                    element: element, snapshotId: snapshotId, pid: pid,
                    descriptor: descriptor ?? entry.descriptor
                )
                currentSnapshotRefs.insert(ref)
                return ref
            }
        }
        counter += 1
        let ref = "e\(counter)"
        table[ref] = Entry(
            element: element, snapshotId: snapshotId, pid: pid,
            descriptor: descriptor
        )
        currentSnapshotRefs.insert(ref)
        // Ambient invalidation: subscribe this element to the pid's
        // AXObserver so we learn the moment the UI destroys it. On
        // duplicate subscription AXObserverAddNotification returns
        // `.notificationAlreadyRegistered` — harmless.
        subscribeDestroyed(element: element, pid: pid)
        return ref
    }

    func resolve(_ ref: String) -> Entry? {
        lock.lock(); defer { lock.unlock() }
        return table[ref]
    }

    // MARK: - Ambient invalidation (AXObserver)

    private func ensureObserver(pid: pid_t) -> AXObserver? {
        // Called with `lock` held. Safe to mutate `observers`.
        if let existing = observers[pid] { return existing }
        var observer: AXObserver?
        let cb: AXObserverCallback = { _, element, _, refcon in
            guard let refcon = refcon else { return }
            let store = Unmanaged<ElementStore>.fromOpaque(refcon).takeUnretainedValue()
            store.handleDestroyed(element: element)
        }
        let err = AXObserverCreate(pid, cb, &observer)
        guard err == .success, let obs = observer else { return nil }
        // Attach to main run loop so callbacks fire in the daemon's
        // RunLoop.main.run() loop. Same run loop where snapshots and
        // actions are dispatched — no cross-thread concerns.
        CFRunLoopAddSource(
            CFRunLoopGetMain(),
            AXObserverGetRunLoopSource(obs),
            .defaultMode
        )
        observers[pid] = obs
        return obs
    }

    private func subscribeDestroyed(element: AXUIElement, pid: pid_t) {
        // Called with `lock` held (via `register`).
        guard let obs = ensureObserver(pid: pid) else { return }
        // Pass self as unretained pointer — ElementStore lives for
        // the whole daemon lifetime, so it outlives every AXObserver.
        let selfPtr = Unmanaged.passUnretained(self).toOpaque()
        _ = AXObserverAddNotification(
            obs,
            element,
            kAXUIElementDestroyedNotification as CFString,
            selfPtr
        )
    }

    private func handleDestroyed(element: AXUIElement) {
        lock.lock(); defer { lock.unlock() }
        // Linear scan — typical table size is O(100) elements; finding
        // by CFEqual is cheap (pointer compare under the hood for
        // live AX elements). Hoist to a reverse-index later only if
        // this shows up in profiles.
        for (ref, var entry) in table where CFEqual(entry.element, element) {
            entry.invalidated = true
            table[ref] = entry
        }
    }

    /// Attempt to rebind `ref` to a fresh live AXUIElement by searching
    /// the target app's current tree for a unique descriptor match.
    ///
    /// Returns the rebound entry on success. Returns nil if:
    ///   - The ref isn't in the table
    ///   - The original entry had no descriptor
    ///   - Zero matches (element is truly gone)
    ///   - Two or more matches (ambiguous — agent must re-snap so it
    ///     knows which instance it wants; Codex refuses with "cannot
    ///     guarantee uniqueness" in the same scenario)
    ///
    /// The search is bounded:
    ///   - Depth cap 12 (plenty for any realistic UI).
    ///   - Early stop once ≥2 matches (ambiguous outcome is settled).
    ///   - ~200 elements visited max.
    func refetchByDescriptor(_ ref: String) -> Entry? {
        lock.lock(); defer { lock.unlock() }
        guard let entry = table[ref], let desc = entry.descriptor else { return nil }

        let appRoot = AXUIElementCreateApplication(entry.pid)
        var matches: [AXUIElement] = []
        // DFS with depth cap. We walk the whole app because a destroyed
        // element's original parent may also have been destroyed — we
        // can't scope the search to the old parent-chain.
        var stack: [(AXUIElement, Int, [String])] = [(appRoot, 0, [])]
        var visited = 0
        while let (el, depth, roleChain) = stack.popLast() {
            visited += 1
            if visited > 200 { break }
            if matchesDescriptor(el, desc, parentRoles: roleChain) {
                matches.append(el)
                if matches.count >= 2 { break }  // ambiguous → give up
            }
            if depth >= 12 { continue }
            var raw: CFTypeRef?
            let err = AXUIElementCopyAttributeValue(el, kAXChildrenAttribute as CFString, &raw)
            guard err == .success, let kids = raw as? [AXUIElement] else { continue }
            // Role of `el` joins the chain for descendants.
            let myRole = (readAttr(el, kAXRoleAttribute) as? String) ?? ""
            let nextChain = roleChain + [myRole]
            for c in kids {
                stack.append((c, depth + 1, nextChain))
            }
        }
        guard matches.count == 1 else { return nil }
        let fresh = Entry(
            element: matches[0], snapshotId: entry.snapshotId, pid: entry.pid,
            descriptor: desc
        )
        table[ref] = fresh
        return fresh
    }

    func clear() {
        lock.lock(); defer { lock.unlock() }
        table.removeAll()
        counter = 0
        // Tear down ambient observers. AXObserver is a CF type; once
        // we drop our strong reference AND remove its run-loop source,
        // it can be deallocated cleanly.
        for obs in observers.values {
            CFRunLoopRemoveSource(
                CFRunLoopGetMain(),
                AXObserverGetRunLoopSource(obs),
                .defaultMode
            )
        }
        observers.removeAll()
    }

    // MARK: - Matching

    /// Returns true iff `el` plausibly represents the same UI thing as
    /// the descriptor was captured from. Strategy:
    ///   - role MUST match exactly
    ///   - subrole MUST match when captured
    ///   - identifier MUST match when captured (AXUIElement identifiers
    ///     are explicitly meant to be stable across redraws)
    ///   - title SHOULD match when captured (soft — nil-tolerant)
    ///   - roleDescription SHOULD match when captured (soft)
    ///   - parentRoles's last-k entries should match the current
    ///     roleChain's last-k entries (k = min of both lengths, capped
    ///     at 3). This catches "same widget, different window".
    private func matchesDescriptor(
        _ el: AXUIElement,
        _ desc: ElementDescriptor,
        parentRoles: [String]
    ) -> Bool {
        let role = (readAttr(el, kAXRoleAttribute) as? String) ?? ""
        guard role == desc.role else { return false }

        if let ds = desc.subrole {
            let subrole = readAttr(el, kAXSubroleAttribute) as? String
            guard subrole == ds else { return false }
        }

        if let di = desc.identifier, !di.isEmpty {
            let eid = readAttr(el, kAXIdentifierAttribute) as? String
            // Identifier, if captured, is authoritative — any mismatch
            // (including nil) means this is a different element.
            guard eid == di else { return false }
        }

        if let dt = desc.title {
            let t = (readAttr(el, kAXTitleAttribute) as? String) ?? ""
            guard t == dt else { return false }
        }

        if let drd = desc.roleDescription {
            let rd = readAttr(el, kAXRoleDescriptionAttribute) as? String
            // Tolerate nil on the live side (some apps lazily populate)
            if let r = rd, r != drd { return false }
        }

        // Parent-chain suffix match, bounded at last-3 roles. This is
        // the tie-breaker when several same-role / same-title widgets
        // exist (tab strips, list rows).
        let k = min(3, min(desc.parentRoles.count, parentRoles.count))
        if k > 0 {
            let dTail = Array(desc.parentRoles.suffix(k))
            let eTail = Array(parentRoles.suffix(k))
            guard dTail == eTail else { return false }
        }
        return true
    }

    private func readAttr(_ el: AXUIElement, _ name: String) -> Any? {
        var raw: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(el, name as CFString, &raw)
        return (err == .success) ? raw : nil
    }
}
