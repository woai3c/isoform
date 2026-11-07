import XCTest
@testable import Yansu

/// P147: Today's away line follows the computer's reason, the way Chat's does.
final class TodayAwayDirectionsTests: XCTestCase {
    func testAClosedLidGetsItsOwnStep() {
        let text = ComputerSleepReason.directions(for: .sleeping, reason: .lidClosed)
        XCTAssertEqual(text, ComputerSleepReason.lidClosed.steps.joined(separator: " "))
        XCTAssertNotEqual(text, TodayStore.ConnectionCause.sleeping.directions)
    }

    func testBatteryAndLowPowerGetTheirOwnSteps() {
        XCTAssertEqual(ComputerSleepReason.directions(for: .sleeping, reason: .batteryFloor),
                       ComputerSleepReason.batteryFloor.steps.joined(separator: " "))
        XCTAssertEqual(ComputerSleepReason.directions(for: .sleeping, reason: .lowPower),
                       ComputerSleepReason.lowPower.steps.joined(separator: " "))
    }

    func testAPlainSleepKeepsTheGenericLine() {
        XCTAssertEqual(ComputerSleepReason.directions(for: .sleeping, reason: nil), TodayStore.ConnectionCause.sleeping.directions)
    }

    func testOtherCausesAreUnchanged() {
        XCTAssertEqual(ComputerSleepReason.directions(for: .shuttingDown, reason: .lidClosed),
                       TodayStore.ConnectionCause.shuttingDown.directions)
    }
}
