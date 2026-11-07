import XCTest
@testable import Yansu

/// P132: a paired phone is never told to pair.
@MainActor
final class NotificationPairingFailureTests: XCTestCase {
    func testOnlyAnUnpairedPhoneIsToldToPair() {
        XCTAssertEqual(NotificationSettingsStore.pairingFailureKey(isPaired: false, storageBlocked: false), "notifications.error.notPaired")
        XCTAssertEqual(NotificationSettingsStore.pairingFailureKey(isPaired: false, storageBlocked: true), "notifications.error.notPaired")
        XCTAssertNotEqual(NotificationSettingsStore.pairingFailureKey(isPaired: true, storageBlocked: false), "notifications.error.notPaired")
        XCTAssertNotEqual(NotificationSettingsStore.pairingFailureKey(isPaired: true, storageBlocked: true), "notifications.error.notPaired")
    }

    func testAPairedPhoneWithAStorageBlockKeepsItsOwnState() {
        XCTAssertNil(NotificationSettingsStore.pairingFailureKey(isPaired: true, storageBlocked: true))
        XCTAssertEqual(NotificationSettingsStore.pairingFailureKey(isPaired: true, storageBlocked: false), "notifications.error.change")
    }
}

/// P132 page rule: the Notifications page shows its choices, one Send a test, only once the computer has answered.
@MainActor
final class NotificationPageStateTests: XCTestCase {
    func testChoicesShowOnlyAfterTheComputerAnswers() {
        XCTAssertTrue(NotificationSettingsView.showsChoices(.loaded))
        XCTAssertFalse(NotificationSettingsView.showsChoices(.idle))
        XCTAssertFalse(NotificationSettingsView.showsChoices(.loading))
        XCTAssertFalse(NotificationSettingsView.showsChoices(.failed("Couldn't reach your computer.")))
    }
}
