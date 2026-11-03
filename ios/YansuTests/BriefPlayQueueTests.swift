import Foundation
import XCTest
@testable import Yansu

@MainActor
final class BriefPlayQueueTests: XCTestCase {
    func testOnlyTransientConnectionFailuresQueuePlay() {
        XCTAssertTrue(BriefPlayQueue.shouldQueue(after: HubRequestError.noAnswer))
        XCTAssertTrue(BriefPlayQueue.shouldQueue(after: RelayClientError.offline))
        XCTAssertTrue(BriefPlayQueue.shouldQueue(after: URLError(.timedOut)))
        XCTAssertFalse(BriefPlayQueue.shouldQueue(after: HubRequestError.rejected(status: 404, message: "gone")))
        XCTAssertFalse(BriefPlayQueue.shouldQueue(after: HubRequestError.forbidden))
        XCTAssertFalse(BriefPlayQueue.shouldQueue(after: HubRequestError.queueFull(retryAfter: nil)))
        XCTAssertFalse(BriefPlayQueue.shouldQueue(after: CancellationError()))
    }

    func testPendingPlayIsDurableAndScopedToThePairing() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appendingPathComponent("pending/play.json")
        let entry = BriefPlayQueue.Entry(hubID: "hub", deviceID: "phone", briefID: "brief-1")
        try BriefPlayQueue(fileURL: url).save(entry)
        let reopened = BriefPlayQueue(fileURL: url)
        XCTAssertEqual(reopened.load(pairing: .init(hubID: "hub", deviceID: "phone")), entry)
        XCTAssertNil(reopened.load(pairing: .init(hubID: "hub", deviceID: "other")))
        XCTAssertNil(reopened.load(pairing: .init(hubID: "other", deviceID: "phone")))
        XCTAssertThrowsError(try reopened.save(.init(hubID: "hub", deviceID: "phone", briefID: "bad/id")))
        try reopened.clear()
        XCTAssertNil(reopened.load(pairing: .init(hubID: "hub", deviceID: "phone")))
    }
}
