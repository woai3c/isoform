import XCTest
@testable import Yansu

final class BrainDumpTests: XCTestCase {
    func testOlderStatusOmitsCapability() throws {
        let status = try JSONDecoder().decode(HubStatus.self, from: Data("{}".utf8))
        XCTAssertNil(status.brainDump)
    }

    func testItemsPreserveNoRunnerReminder() throws {
        let data = Data(#"{"items":[{"id":"a","title":"Fix tests","who":"reminder","noRunner":true}]}"#.utf8)
        let result = try JSONDecoder().decode(BrainDumpParseResult.self, from: data)
        XCTAssertEqual(result.items.first?.who, "reminder")
        XCTAssertEqual(result.items.first?.noRunner, true)
        let roundTrip = try JSONDecoder().decode(BrainDumpItem.self, from: JSONEncoder().encode(result.items[0]))
        XCTAssertEqual(roundTrip, result.items[0])
    }

    func testRejectedAddCannotLookSuccessful() {
        let response = HubResponse(status: 409, body: #"{"ok":false,"error":"changed"}"#)
        XCTAssertThrowsError(try response.payload(BrainDumpAddResult.self))
    }
}
