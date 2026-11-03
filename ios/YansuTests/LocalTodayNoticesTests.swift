import Foundation
import XCTest
@testable import Yansu

final class LocalTodayNoticesTests: XCTestCase {
    @MainActor
    func testNoticeMetadataDecodesAndOldEntriesRemainReadable() throws {
        let raw = #"{"id":"task-1","noticeId":"task:task-1","createdAt":"2026-10-27T08:12:00Z","kind":"work_blocked","priority":"active","expiresAt":"2026-10-27T10:00:00Z","destinationKind":"task","destinationId":"task-1"}"#
        let entry = try JSONDecoder().decode(TodayNeed.self, from: Data(raw.utf8))
        XCTAssertEqual(entry.noticeId, "task:task-1")
        XCTAssertEqual(entry.expiresAt, "2026-10-27T10:00:00Z")
        XCTAssertEqual(entry.destinationKind, "task")
        XCTAssertEqual(entry.destinationId, "task-1")
        let old = try JSONDecoder().decode(TodayNeed.self, from: Data(#"{"id":"old"}"#.utf8))
        XCTAssertNil(old.noticeId)
        XCTAssertNil(old.destinationId)
        XCTAssertTrue(LocalTodayNotices.candidates([old]).isEmpty)
    }

    func testDeliveredNoticeDedupeIsScopedToPairing() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        XCTAssertTrue(SharedNoticeDedup.record(hub: "hub", device: "phone", id: "notice", directory: directory))
        XCTAssertTrue(SharedNoticeDedup.contains(hub: "hub", device: "phone", id: "notice", directory: directory))
        XCTAssertFalse(SharedNoticeDedup.contains(hub: "hub", device: "other", id: "notice", directory: directory))
        XCTAssertFalse(SharedNoticeDedup.contains(hub: "other", device: "phone", id: "notice", directory: directory))
        SharedNoticeDedup.prune(directory: directory, now: Date().addingTimeInterval(15 * 24 * 60 * 60))
        XCTAssertFalse(SharedNoticeDedup.contains(hub: "hub", device: "phone", id: "notice", directory: directory))
    }

    @MainActor
    func testPendingActionSuppressesOnlyMatchingPairingAndCard() throws {
        let card = try JSONDecoder().decode(ChatProactiveView.self, from: Data(#"{"momentId":"m1","kind":"reminder","state":"pending","actionIds":["done"],"actionLabels":{"done":"Done"}}"#.utf8))
        let need = TodayNeed(id: "m1", chatId: "chat", card: .init(proactive: card), task: nil, noticeId: "notice-1")
        var write = PendingWrite(key: "key", operation: .chatProactiveAction, target: "m1", choice: "done",
                                 fingerprint: "fingerprint", createdAt: Date(), hubID: "hub")
        write.todayDeviceID = "phone"
        write.todayReplay = .init(chatID: "chat", momentID: "m1", cardDigest: try PendingWriteJournal.cardDigest(card),
                                  actionID: "done", dispatched: false)
        XCTAssertEqual(LocalTodayNotices.pendingActionNoticeIDs([need], writes: [write],
                       pairing: .init(hubID: "hub", deviceID: "phone")), ["notice-1"])
        XCTAssertTrue(LocalTodayNotices.pendingActionNoticeIDs([need], writes: [write],
                      pairing: .init(hubID: "hub", deviceID: "other")).isEmpty)
    }

    private actor Sink: LocalTodayNoticeSink {
        var sent: [LocalTodayNotice] = []
        var remote: Set<String> = []
        func deliveredIDs(pairing: TodayPairingIdentity) async -> Set<String> { remote }
        func deliver(_ notice: LocalTodayNotice) async throws { sent.append(notice) }
        func values() -> [LocalTodayNotice] { sent }
        func setRemote(_ ids: Set<String>) { remote = ids }
    }

    private func need(_ id: String, created: Date) -> TodayNeed {
        let task = MobileTask(id: id, title: "Task \(id)", status: "blocked", group: "needs_you",
                              blockedReason: "Review \(id)", runner: nil, progress: nil,
                              createdAt: ISO8601DateFormatter().string(from: created), updatedAt: ISO8601DateFormatter().string(from: created))
        return TodayNeed(id: id, chatId: nil, card: nil, task: task, noticeId: "task:\(id)",
                         createdAt: task.createdAt, kind: "work_blocked", priority: "active",
                         destinationKind: "task", destinationId: id)
    }
    private func snapshot(_ needs: [TodayNeed]) -> MobileTodaySnapshot {
        MobileTodaySnapshot(generatedAt: "2026-10-27T12:00:00Z", latestBrief: nil, briefs: [], needsYou: needs,
                            needsYouCount: needs.count, day: TodayDay(next: nil, free: [], calendarConnected: false),
                            topics: [], briefRequest: nil, schedule: MobileBriefSchedule(morning: "07:00", evening: "19:00"))
    }
    private var settings: NotificationSettings {
        NotificationSettings(available: true, kinds: [NotificationSettingsTests.kind("work_blocked", enabled: true)],
                             quietStart: "00:00", quietEnd: "00:00", dailyInterruptBudget: 6, digestTimes: [], statsDays: 30)
    }

    @MainActor
    func testRefreshBatchesNewItemsAndPersistsDedupe() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let sink = Sink()
        let url = directory.appendingPathComponent("notices.json")
        let store = LocalTodayNotices(fileURL: url, sink: sink)
        let identity = TodayPairingIdentity(hubID: "hub", deviceID: "phone")
        let now = try XCTUnwrap(RelayTime.parse("2026-10-27T12:00:00Z"))
        let before = snapshot([need("old", created: now)])
        let after = snapshot([need("old", created: now)] + ["a", "b", "c"].map { need($0, created: now.addingTimeInterval(-3600)) })
        XCTAssertTrue(store.record(current: after, previous: before, pairing: identity, suppress: false))
        await store.process(current: after, previous: before, pairing: identity, settings: settings, instantPush: false,
                            now: now, isCurrent: { true })
        let first = await sink.values()
        XCTAssertEqual(first.count, 1)
        XCTAssertEqual(Set(first[0].noticeIDs), ["task:a", "task:b", "task:c"])
        XCTAssertEqual(first[0].title, "3 things need you")
        XCTAssertEqual(first[0].destinationKind, "task")
        XCTAssertTrue(["a", "b", "c"].contains(first[0].destinationID ?? ""))
        XCTAssertTrue(first[0].body.contains("Review \(first[0].destinationID ?? "missing")"),
                      "the batch opens the item named in its body")
        XCTAssertTrue(first[0].body.contains("·"))
        let reopened = LocalTodayNotices(fileURL: url, sink: sink)
        XCTAssertTrue(reopened.record(current: after, previous: after, pairing: identity, suppress: false))
        await reopened.process(current: after, previous: after, pairing: identity, settings: settings, instantPush: false,
                               now: now, isCurrent: { true })
        let second = await sink.values()
        XCTAssertEqual(second.count, 1)
    }

    @MainActor
    func testSingleNoticeCarriesItsTaskDestination() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let sink = Sink()
        let store = LocalTodayNotices(fileURL: directory.appendingPathComponent("notices.json"), sink: sink)
        let identity = TodayPairingIdentity(hubID: "hub", deviceID: "phone")
        let now = Date()
        let before = snapshot([])
        var item = need("target", created: now)
        item.destinationKind = "task"
        item.destinationId = "target"
        let after = snapshot([item])
        XCTAssertTrue(store.record(current: after, previous: before, pairing: identity, suppress: false))
        await store.process(current: after, previous: before, pairing: identity, settings: settings,
                            instantPush: false, now: now, isCurrent: { true })
        let sent = await sink.values()
        XCTAssertEqual(sent.count, 1)
        XCTAssertEqual(sent.first?.destinationKind, "task")
        XCTAssertEqual(sent.first?.destinationID, "target")
    }

    @MainActor
    func testHeldItemsAreRecheckedAndRemoteIDsAreDeduplicated() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let sink = Sink()
        let store = LocalTodayNotices(fileURL: directory.appendingPathComponent("notices.json"), sink: sink)
        let identity = TodayPairingIdentity(hubID: "hub", deviceID: "phone")
        let now = Date()
        let before = snapshot([])
        let initial = snapshot([need("resolved", created: now), need("remote", created: now)])
        XCTAssertTrue(store.record(current: initial, previous: before, pairing: identity, suppress: false))
        await sink.setRemote(["task:remote"])
        let later = snapshot([need("remote", created: now)])
        XCTAssertTrue(store.record(current: later, previous: initial, pairing: identity, suppress: false))
        await store.process(current: later, previous: initial, pairing: identity, settings: settings, instantPush: false,
                            now: now, isCurrent: { true })
        let sent = await sink.values()
        XCTAssertTrue(sent.isEmpty)
    }

    @MainActor
    func testMacBudgetAndQuietBreakAreIncludedInPacing() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let sink = Sink()
        let store = LocalTodayNotices(fileURL: directory.appendingPathComponent("notices.json"), sink: sink)
        let identity = TodayPairingIdentity(hubID: "hub", deviceID: "phone")
        let now = try XCTUnwrap(RelayTime.parse("2026-10-27T12:00:00Z"))
        let before = snapshot([])
        var after = snapshot([need("new", created: now)])
        after.noticePacing = TodayNoticePacing(activeToday: 1, lastQuietBreak: nil)
        let limited = NotificationSettings(available: true, kinds: settings.kinds, quietStart: "00:00", quietEnd: "00:00",
                                           dailyInterruptBudget: 1, digestTimes: [], statsDays: 30)
        XCTAssertTrue(store.record(current: after, previous: before, pairing: identity, suppress: false))
        await store.process(current: after, previous: before, pairing: identity, settings: limited, instantPush: false,
                            now: now, isCurrent: { true })
        let sent = await sink.values()
        XCTAssertTrue(sent.isEmpty)
    }

    @MainActor
    func testExpiredMomentIsNotDelivered() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let sink = Sink()
        let store = LocalTodayNotices(fileURL: directory.appendingPathComponent("notices.json"), sink: sink)
        let identity = TodayPairingIdentity(hubID: "hub", deviceID: "phone")
        let now = Date()
        var expired = need("expired", created: now.addingTimeInterval(-3600))
        expired.expiresAt = ISO8601DateFormatter().string(from: now.addingTimeInterval(-10))
        let before = snapshot([])
        let after = snapshot([expired])
        XCTAssertTrue(store.record(current: after, previous: before, pairing: identity, suppress: false))
        await store.process(current: after, previous: before, pairing: identity, settings: settings, instantPush: false,
                            now: now, isCurrent: { true })
        let sent = await sink.values()
        XCTAssertTrue(sent.isEmpty)
    }
}
