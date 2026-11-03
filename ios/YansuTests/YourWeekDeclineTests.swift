import XCTest
@testable import Yansu

private actor WeekDeclineTestHub: YourWeekDeclineAPI {
    var saved = false
    var keys: [String] = []
    var loseFirst = true
    func weekDeclined(briefID: String, version: String) async throws -> Bool { saved }
    func declineWeek(briefID: String, version: String, idempotencyKey: String) async throws -> Bool {
        keys.append(idempotencyKey)
        saved = true
        if loseFirst { loseFirst = false; throw HubRequestError.noAnswer }
        return true
    }
}

@MainActor
final class YourWeekDeclineTests: XCTestCase {
    func testDeclinePersistsOfflineAndRetriesLostReceiptWithoutDuplicating() async throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let suite = "week-decline-tests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let journal = PendingWriteJournal(fileURL: dir.appendingPathComponent("writes.json"), hubID: { "hub" })
        let hub = WeekDeclineTestHub()
        var online = false
        let store = YourWeekDeclineStore(hub: hub, journal: journal, defaults: defaults, hubID: { "hub" }, online: { online })
        let week = MobileBriefWeek(start: "2026-10-19", end: "2026-10-25", time: [], done: [], open: [], learned: [])
        let brief = MobileBriefV1(briefId: "brief-1", narrative: [BriefParagraph(text: "Brief")], conversationKey: "revision")
        await store.decline(brief: brief, week: week)
        XCTAssertTrue(store.isPending(week))
        XCTAssertFalse(store.isDeclined(week))
        let queuedEntries = await journal.entries()
        XCTAssertEqual(queuedEntries.count, 1)

        let restored = YourWeekDeclineStore(hub: hub, journal: journal, defaults: defaults, hubID: { "hub" }, online: { online })
        await restored.load(brief: brief, week: week)
        XCTAssertTrue(restored.isPending(week))
        online = true
        await restored.retryPending()
        XCTAssertTrue(restored.isPending(week))
        await restored.retryPending()
        XCTAssertTrue(restored.isDeclined(week))
        XCTAssertFalse(restored.isPending(week))
        let keys = await hub.keys
        XCTAssertEqual(keys.count, 2)
        XCTAssertEqual(keys[0], keys[1])
        let remainingEntries = await journal.entries()
        XCTAssertTrue(remainingEntries.isEmpty)
    }
}
