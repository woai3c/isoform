import XCTest
@testable import Yansu

/// A reply's recipient is shown by display name only (PROTOCOL.md section 23): never an address.
final class RecipientNameTests: XCTestCase {
    func testADisplayNameIsShownAsItIs() {
        XCTAssertEqual(RecipientName.name("Sam Lee"), "Sam Lee")
        XCTAssertEqual(RecipientName.name("  Sam Lee  "), "Sam Lee")
    }

    func testAnAddressOrNothingIsNoName() {
        XCTAssertNil(RecipientName.name("sam.lee@example.com"))
        XCTAssertNil(RecipientName.name(""))
        XCTAssertNil(RecipientName.name("   "))
        XCTAssertNil(RecipientName.name(nil))
    }

    /// A held reply with an address in its name reads "your reply" in Today's line, never the address.
    func testTodaysLineNeverShowsAnAddress() {
        let base = Date(timeIntervalSince1970: 1_800_000_000)
        let address = HeldSendNotice.make([.init(place: .task(taskID: "draft.a"), sentAt: base, person: "sam.lee@example.com")])
        XCTAssertEqual(address?.text, "Your computer didn't wake, so your reply wasn't sent.")
        let named = HeldSendNotice.make([.init(place: .task(taskID: "draft.a"), sentAt: base, person: "Sam Lee")])
        XCTAssertEqual(named?.text, "Your computer didn't wake, so your reply to Sam Lee wasn't sent.")
    }
}
