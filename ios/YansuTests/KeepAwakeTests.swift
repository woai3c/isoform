import Foundation
import XCTest
@testable import Yansu

final class KeepAwakeTests: XCTestCase {
    private actor Hub: KeepAwakeAPI {
        var online = false
        var writes: [Bool] = []
        var value = false
        func connect() { online = true }
        func keepAwakeStatus() async throws -> HubStatus {
            guard online else { throw HubRequestError.noAnswer }
            return HubStatus(hubName: nil, appVersion: nil, protocolVersion: nil,
                             narrativeAvailable: nil, lastBriefAt: nil, keepAwake: value, instantPush: false)
        }
        func keepAwake() async throws -> KeepAwakeSetting {
            guard online else { throw HubRequestError.noAnswer }
            return KeepAwakeSetting(on: value, pairedPhones: 1, onPower: true)
        }
        func setKeepAwake(_ on: Bool) async throws -> KeepAwakeSetting {
            guard online else { throw HubRequestError.noAnswer }
            writes.append(on)
            value = on
            return KeepAwakeSetting(on: value, pairedPhones: 1, onPower: true)
        }
        func recorded() -> [Bool] { writes }
    }

    @MainActor
    func testUnknownIsHiddenAndOfflineChangeReplaysAfterRelaunch() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let suite = "KeepAwakeTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let hub = Hub()
        let identity = TodayPairingIdentity(hubID: "hub", deviceID: "phone")
        let url = directory.appendingPathComponent("journal.json")
        let journal = PendingWriteJournal(fileURL: url, hubID: { "hub" })
        let first = KeepAwakeStore(hub: hub, journal: journal, identity: { identity }, defaults: defaults)
        XCTAssertNil(first.on)
        await first.set(true)
        XCTAssertFalse(first.pending)
        await hub.connect()
        await first.refresh()
        XCTAssertEqual(first.on, false)
        let offline = OfflineHub()
        let queued = KeepAwakeStore(hub: offline, journal: journal, identity: { identity }, defaults: defaults)
        await queued.set(true)
        XCTAssertTrue(queued.pending)
        XCTAssertEqual(queued.on, true)
        let restoredJournal = PendingWriteJournal(fileURL: url, hubID: { "hub" })
        let restored = KeepAwakeStore(hub: hub, journal: restoredJournal, identity: { identity }, defaults: defaults)
        await restored.refresh()
        XCTAssertEqual(restored.on, true)
        XCTAssertFalse(restored.pending)
        let writes = await hub.recorded()
        XCTAssertEqual(writes, [true])
    }

    private actor OfflineHub: KeepAwakeAPI {
        func keepAwakeStatus() async throws -> HubStatus { throw HubRequestError.noAnswer }
        func keepAwake() async throws -> KeepAwakeSetting { throw HubRequestError.noAnswer }
        func setKeepAwake(_ on: Bool) async throws -> KeepAwakeSetting { throw HubRequestError.noAnswer }
    }

    @MainActor
    func testReservationFailureKeepsConfirmedValueAndReportsFailure() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let hub = Hub()
        await hub.connect()
        let identity = TodayPairingIdentity(hubID: "hub", deviceID: "phone")
        let journal = PendingWriteJournal(fileURL: directory.appendingPathComponent("journal.json"), hubID: { "hub" },
                                          writeData: { _, _ in throw CocoaError(.fileWriteNoPermission) })
        let suite = "KeepAwakeTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let store = KeepAwakeStore(hub: hub, journal: journal, identity: { identity }, defaults: defaults)
        await store.refresh()
        await store.set(true)
        XCTAssertEqual(store.on, false)
        XCTAssertTrue(store.saveFailed)
        XCTAssertFalse(store.pending)
        let writes = await hub.recorded()
        XCTAssertTrue(writes.isEmpty)
    }

    func testBriefDeliveryWaitsForSlotAndQuietHours() throws {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = try XCTUnwrap(TimeZone(secondsFromGMT: 0))
        let now = try XCTUnwrap(RelayTime.parse("2026-10-27T06:00:00Z"))
        let slot = try XCTUnwrap(RelayTime.parse("2026-10-27T07:00:00Z"))
        let eight = try XCTUnwrap(RelayTime.parse("2026-10-27T08:00:00Z"))
        XCTAssertEqual(LocalBriefNotices.deliveryDate(scheduled: slot, now: now, quietStart: "22:00", quietEnd: "08:00", calendar: calendar), eight)
        XCTAssertEqual(LocalBriefNotices.deliveryDate(scheduled: slot, now: eight, quietStart: "22:00", quietEnd: "08:00", calendar: calendar), eight)
        XCTAssertNil(LocalBriefNotices.deliveryDate(scheduled: slot, now: slot.addingTimeInterval(90000), quietStart: "22:00", quietEnd: "08:00", calendar: calendar))
    }

    func testBriefCandidateOnlyAppearsAfterAChangeAndSurvivesAnotherRefresh() throws {
        let suite = "LocalBriefTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let pairing = TodayPairingIdentity(hubID: "hub", deviceID: "phone")
        let entry = TodayBriefIndex(id: "new", slot: "morning", localDate: "2026-10-27",
                                    scheduledAt: "2026-10-27T07:00:00Z", madeAt: "2026-10-27T06:50:00Z",
                                    catchUp: false, durationSeconds: nil, lead: "The day is ready.")
        func snapshot(_ brief: TodayBriefIndex?) -> MobileTodaySnapshot {
            MobileTodaySnapshot(generatedAt: "2026-10-27T06:55:00Z", latestBrief: brief, briefs: brief.map { [$0] } ?? [],
                                needsYou: [], needsYouCount: 0, day: TodayDay(next: nil, free: [], calendarConnected: false),
                                topics: [], briefRequest: nil, schedule: MobileBriefSchedule(morning: "07:00", evening: "19:00"))
        }
        let baseline = snapshot(nil)
        LocalBriefNotices.observe(snapshot(entry), previous: nil, pairing: pairing, defaults: defaults)
        XCTAssertFalse(LocalBriefNotices.isPending(entry.id, pairing: pairing, defaults: defaults))
        LocalBriefNotices.observe(snapshot(entry), previous: baseline, pairing: pairing, defaults: defaults)
        LocalBriefNotices.observe(snapshot(entry), previous: snapshot(entry), pairing: pairing, defaults: defaults)
        XCTAssertTrue(LocalBriefNotices.isPending(entry.id, pairing: pairing, defaults: defaults))
        LocalBriefNotices.observe(baseline, previous: snapshot(entry), pairing: pairing, defaults: defaults)
        XCTAssertFalse(LocalBriefNotices.isPending(entry.id, pairing: pairing, defaults: defaults))
    }
}
