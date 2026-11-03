import Foundation
import UserNotifications

struct LocalTodayNotice: Sendable {
    let identifier: String
    let noticeIDs: [String]
    let pairing: TodayPairingIdentity
    let title: String
    let body: String
    let level: String
    var destinationKind: String? = nil
    var destinationID: String? = nil
}

protocol LocalTodayNoticeSink: Sendable {
    func deliveredIDs(pairing: TodayPairingIdentity) async -> Set<String>
    func deliver(_ notice: LocalTodayNotice) async throws
}

struct SystemLocalTodayNoticeSink: LocalTodayNoticeSink {
    private func pairingKey(_ pairing: TodayPairingIdentity) -> String {
        RelayCrypto.sha256Hex(Data("\(pairing.hubID)/\(pairing.deviceID)".utf8))
    }

    func deliveredIDs(pairing: TodayPairingIdentity) async -> Set<String> {
        let center = UNUserNotificationCenter.current()
        let delivered = await center.deliveredNotifications().map(\.request)
        let pending = await center.pendingNotificationRequests()
        return Set((delivered + pending).flatMap { request -> [String] in
            let info = request.content.userInfo
            if info["localTodayPairing"] as? String == pairingKey(pairing),
               let ids = info["localTodayNoticeIDs"] as? [String] { return ids }
            guard let push = NoticePush.parse(info), push.hub == pairing.hubID, push.dev == pairing.deviceID else { return [] }
            return [push.id]
        })
    }

    func deliver(_ notice: LocalTodayNotice) async throws {
        let center = UNUserNotificationCenter.current()
        let permission = await center.notificationSettings().authorizationStatus
        guard [.authorized, .provisional, .ephemeral].contains(permission) else { throw HubRequestError.forbidden }
        let content = UNMutableNotificationContent()
        content.title = notice.title
        content.body = notice.body
        content.sound = .default
        content.categoryIdentifier = NoticeCategory.brief.rawValue
        switch notice.level {
        case "time-sensitive": content.interruptionLevel = .timeSensitive
        case "active": content.interruptionLevel = .active
        default: content.interruptionLevel = .passive
        }
        var info: [String: Any] = ["localTodayNoticeIDs": notice.noticeIDs, "localToday": true,
                                   "localTodayPairing": pairingKey(notice.pairing)]
        if let kind = notice.destinationKind, let id = notice.destinationID, NoticeIdentifier.isSafe(id) {
            info["destinationKind"] = kind
            info["destinationId"] = id
        }
        content.userInfo = info
        let trigger = UNTimeIntervalNotificationTrigger(timeInterval: 1, repeats: false)
        try await center.add(UNNotificationRequest(identifier: notice.identifier, content: content, trigger: trigger))
    }
}

/// Retains only identities and pacing receipts, scoped to the pairing and protected like the Today cache.
@MainActor
final class LocalTodayNotices {
    private struct State: Codable {
        let pairing: TodayPairingIdentity
        var initialized = false
        var seen: Set<String> = []
        var held: Set<String> = []
        var delivered: [String: Date] = [:]
        var activeDeliveries: [Date] = []
        var lastQuietBreak: Date?
    }

    private let fileURL: URL
    private let sink: LocalTodayNoticeSink
    private var busy = false

    init(fileURL: URL, sink: LocalTodayNoticeSink = SystemLocalTodayNoticeSink()) {
        self.fileURL = fileURL
        self.sink = sink
    }

    private func load(_ pairing: TodayPairingIdentity) -> State {
        guard let data = try? Data(contentsOf: fileURL), let state = try? JSONDecoder().decode(State.self, from: data),
              state.pairing == pairing else { return State(pairing: pairing) }
        return state
    }

    private func save(_ state: State) -> Bool {
        do {
            try FileManager.default.createDirectory(at: fileURL.deletingLastPathComponent(), withIntermediateDirectories: true)
            try JSONEncoder().encode(state).write(to: fileURL, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            return true
        } catch { return false }
    }

    func clear() {
        try? FileManager.default.removeItem(at: fileURL)
    }

    static func cancelPending(center: UNUserNotificationCenter = .current()) async {
        let requests = await center.pendingNotificationRequests()
        center.removePendingNotificationRequests(withIdentifiers: requests.map(\.identifier).filter { $0.hasPrefix("local.today.") })
    }

    func observeForeground(_ snapshot: MobileTodaySnapshot, pairing: TodayPairingIdentity) {
        guard !busy else { return }
        let ids = Set(Self.candidates(snapshot.needsYou).map(\.id))
        var state = load(pairing)
        state.initialized = true
        state.seen = ids.union(state.delivered.keys)
        state.held.formIntersection(ids)
        _ = save(state)
    }

    /// Remember newly observed IDs before any further network read, so a failed
    /// preferences fetch can retry them against the next live snapshot.
    func record(current: MobileTodaySnapshot, previous: MobileTodaySnapshot?, pairing: TodayPairingIdentity,
                suppress: Bool) -> Bool {
        guard !busy else { return false }
        let ids = Set(Self.candidates(current.needsYou).map(\.id))
        var state = load(pairing)
        if !state.initialized {
            if let previous {
                let priorRows = Set(previous.needsYou.map(\.id))
                state.seen = Set(current.needsYou.filter { priorRows.contains($0.id) }.compactMap(\.noticeId))
            } else {
                state.seen = ids
            }
            state.initialized = true
        }
        if let previous {
            let legacyRows = Set(previous.needsYou.filter { $0.noticeId == nil }.map(\.id))
            state.seen.formUnion(current.needsYou.filter { legacyRows.contains($0.id) }.compactMap(\.noticeId))
        }
        state.held.formIntersection(ids)
        if suppress { state.held.removeAll() }
        else { state.held.formUnion(ids.subtracting(state.seen)) }
        state.seen = ids.union(state.delivered.keys)
        return save(state)
    }

    func process(current: MobileTodaySnapshot, previous: MobileTodaySnapshot?, pairing: TodayPairingIdentity,
                 settings: NotificationSettings, instantPush: Bool, excludedIDs: Set<String> = [], now: Date = Date(),
                 isCurrent: @escaping @MainActor () -> Bool) async {
        guard !busy, settings.available, isCurrent() else { return }
        busy = true
        defer { busy = false }
        let notices = Self.candidates(current.needsYou).filter { !excludedIDs.contains($0.id) }
        let currentIDs = Set(notices.map(\.id))
        var state = load(pairing)
        if !state.initialized {
            if let previous {
                let previousRows = Set(previous.needsYou.map(\.id))
                state.seen = Set(current.needsYou.filter { previousRows.contains($0.id) }.compactMap(\.noticeId))
            } else {
                state.seen = currentIDs
            }
            state.initialized = true
        }
        state.delivered = state.delivered.filter { now.timeIntervalSince($0.value) < 14 * 24 * 60 * 60 }
        state.activeDeliveries = state.activeDeliveries.filter { Calendar.current.isDate($0, inSameDayAs: now) }
        let remoteIDs = await sink.deliveredIDs(pairing: pairing)
        guard isCurrent(), !Task.isCancelled else { return }
        for id in remoteIDs { state.delivered[id] = now }
        SharedNoticeDedup.prune(now: now)
        for id in currentIDs where SharedNoticeDedup.contains(hub: pairing.hubID, device: pairing.deviceID, id: id) {
            state.delivered[id] = now
        }
        state.held.formIntersection(currentIDs)
        if instantPush {
            state.seen.formUnion(currentIDs)
            state.held.removeAll()
            _ = save(state)
            return
        }
        let macActive = current.generatedDate.map { Calendar.current.isDate($0, inSameDayAs: now) } == true
            ? max(0, current.noticePacing?.activeToday ?? 0) : 0
        let macQuiet = current.noticePacing?.lastQuietBreak.flatMap(RelayTime.parse)
        let lastQuiet = [state.lastQuietBreak, macQuiet].compactMap { $0 }.max()
        let plan = LocalNoticePlanner.plan(current: notices, previouslySeen: state.seen, held: state.held,
                                           delivered: Set(state.delivered.keys), settings: settings, now: now,
                                           activeDeliveredToday: state.activeDeliveries.count + macActive,
                                           lastQuietBreak: lastQuiet)
        state.held = plan.deferred
        state.seen.formUnion(currentIDs)
        // Persist the retry set before handing a notification to the system.
        state.held.formUnion(plan.deliver.map(\.id))
        guard save(state) else { return }
        let groups = plan.batch ? [plan.deliver] : plan.deliver.map { [$0] }
        for group in groups where !group.isEmpty {
            guard isCurrent(), !Task.isCancelled else { return }
            guard group.allSatisfy({ $0.expires.map { $0 > Date() } ?? true }) else { continue }
            let first = group[0]
            let ids = group.map(\.id)
            let late = LocalNoticePlanner.lateSuffix(moment: first.moment, now: now, locale: .current)
            let base = first.body.isEmpty ? first.title : first.body
            let body = late.map { base + " " + $0 } ?? base
            let title = group.count >= 3 ? LocalNoticePlanner.batchTitle(count: group.count) : first.title
            let level = group.contains(where: { $0.timeSensitive }) ? "time-sensitive" :
                group.contains(where: { $0.countsBudget }) ? "active" : "passive"
            let framed = [pairing.hubID, pairing.deviceID] + ids.sorted()
            let digest = RelayCrypto.sha256Hex(Data(framed.map { "\($0.utf8.count):\($0)" }.joined(separator: "|").utf8))
            do {
                try await sink.deliver(LocalTodayNotice(identifier: "local.today.\(digest)", noticeIDs: ids,
                                                       pairing: pairing, title: title, body: body, level: level,
                                                       destinationKind: first.destinationKind,
                                                       destinationID: first.destinationID))
            } catch { break }
            guard isCurrent() else {
                await Self.cancelPending()
                return
            }
            for id in ids { state.delivered[id] = now; state.held.remove(id) }
            if group.contains(where: { $0.countsBudget }) { state.activeDeliveries.append(now) }
            if group.contains(where: { $0.kind == "work_blocked" && $0.timeSensitive }),
               LocalBriefNotices.deliveryDate(scheduled: now, now: now, quietStart: settings.quietStart,
                                               quietEnd: settings.quietEnd).map({ $0 > now }) == true {
                state.lastQuietBreak = now
            }
            guard save(state) else { return }
        }
    }

    static func pendingActionNoticeIDs(_ needs: [TodayNeed], writes: [PendingWrite],
                                       pairing: TodayPairingIdentity) -> Set<String> {
        let digests = Set(writes.compactMap { write -> String? in
            guard write.hubID == pairing.hubID, write.todayDeviceID == pairing.deviceID else { return nil }
            return write.todayReplay?.cardDigest
        })
        return Set(needs.compactMap { need in
            guard let card = need.card?.proactive, let digest = try? PendingWriteJournal.cardDigest(card),
                  digests.contains(digest) else { return nil }
            return need.noticeId
        })
    }

    static func candidates(_ needs: [TodayNeed], locale: Locale = .current) -> [LocalOpenNotice] {
        let language = ChatPresentationLanguage(locale: locale).code
        return needs.compactMap { need in
            guard let id = need.noticeId, !id.isEmpty, let kind = need.kind, let priority = need.priority,
                  let moment = need.createdAt.flatMap(RelayTime.parse),
                  ["passive", "active", "time-sensitive"].contains(priority) else { return nil }
            let title: String
            let body: String
            var expires = need.expiresAt.flatMap(RelayTime.parse)
            if let card = need.card?.proactive {
                guard card.canAct, !card.isWithdrawnThreadUpdate else { return nil }
                let copy = card.copy(language: language)
                title = copy.headline
                body = copy.body
                if let cardExpiry = card.expiresAt.map({ Date(timeIntervalSince1970: Double($0) / 1000) }) {
                    expires = [expires, cardExpiry].compactMap { $0 }.min()
                }
            } else if let task = need.task {
                title = task.title
                body = task.blockedReason ?? ""
            } else { return nil }
            guard !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
            let rank = priority == "time-sensitive" ? 3.0 : priority == "active" ? 2.0 : 1.0
            return LocalOpenNotice(id: id, kind: kind, title: title, body: body, moment: moment, expires: expires,
                                   priority: rank, timeSensitive: priority == "time-sensitive", countsBudget: priority == "active",
                                   destinationKind: need.destinationKind, destinationID: need.destinationId)
        }
    }
}
