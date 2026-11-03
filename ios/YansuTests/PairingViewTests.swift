import Foundation
import XCTest
@testable import Yansu

@MainActor
final class PairingViewTests: XCTestCase {
    /// Both Done buttons after a successful pairing share this finish: a Done that only closes the sheet fails it.
    func testPairingDoneFetchesTheBriefDismissesAndShowsTheFeed() {
        let router = NotificationRouter()
        router.selectedTab = .tasks
        router.openPairing()
        var refreshedBrief = false
        var dismissed = false
        PairingView.finish(refreshBrief: { refreshedBrief = true }, dismiss: { dismissed = true }, router: router)
        XCTAssertTrue(refreshedBrief, "Done fetches the first brief instead of waiting for the next refresh")
        XCTAssertTrue(dismissed, "Done closes the pairing sheet")
        XCTAssertNil(router.sheet, "Done leaves no sheet open")
        XCTAssertEqual(router.selectedTab, .today, "Done ends on Today, where the first brief arrives")
    }
}
