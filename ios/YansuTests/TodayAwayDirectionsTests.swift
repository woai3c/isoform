import XCTest
@testable import Yansu

/// P147: Today's away line follows the computer's reason, the way Chat's does, and matches the Computer sheet's words.
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

extension TodayAwayDirectionsTests {
    /// P147: the lid and battery lines end with Isoform picking up, and the waiting clause appears only with something to send.
    func testTheLidLineKeepsTheSentenceAboutPickingUp() {
        XCTAssertTrue(ComputerSleepReason.directions(for: .sleeping, reason: .lidClosed).hasSuffix(String(localized: "connection.sleepReason.picksUp")))
    }

    func testTheWaitingClauseAppearsOnlyWhenSomethingWaits() {
        let plain = ComputerSleepReason.directions(for: .sleeping, reason: nil)
        let waiting = ComputerSleepReason.directions(for: .sleeping, reason: nil, sentWaiting: true)
        XCTAssertNotEqual(plain, waiting)
        XCTAssertTrue(waiting.hasSuffix(String(localized: "connection.sentWaiting").replacingOccurrences(of: "，", with: ",")) || waiting.contains(String(localized: "connection.sentWaiting")))
        XCTAssertEqual(ComputerSleepReason.directions(for: .sleeping, reason: nil, sentWaiting: false), plain)
    }
}

extension TodayAwayDirectionsTests {
    /// Today's away line keeps the design's "Open the lid." (the Computer sheet shows the same words).
    func testTodayLidLineSaysOpenTheLid() {
        XCTAssertTrue(ComputerSleepReason.directions(for: .sleeping, reason: .lidClosed).hasPrefix("Open the lid."))
    }
}

extension TodayAwayDirectionsTests {
    /// The sheet's first line says "since" when the sleep time is known, and "as of" when only the last-heard time is.
    func testTheSheetLineSaysSinceWhenTheSleepTimeIsKnown() {
        XCTAssertEqual(ComputerSleepReason.lidClosed.status(sleptAt: "6:20 PM", asOf: "6:40 PM"), "Asleep since 6:20 PM · Its lid is closed.")
    }

    func testTheSheetLineSaysAsOfWithoutTheSleepTime() {
        XCTAssertEqual(ComputerSleepReason.lidClosed.status(sleptAt: nil, asOf: "6:40 PM"), "Asleep as of 6:40 PM · Its lid is closed.")
    }
}

/// P133: Today's away line sits under the date and uses the Computer sheet's first line, word for word.
final class TodayAwayLineTests: XCTestCase {
    func testASleepWithAGoodbyeTimeSaysSinceInTheSheetWords() {
        XCTAssertEqual(TodayStore.ConnectionCause.firstLine(.sleeping, reason: .lidClosed, sleptAt: "6:46 PM", asOf: "6:50 PM"),
                       "Asleep since 6:46 PM · Its lid is closed.")
    }

    func testASleepWithoutAGoodbyeTimeSaysAsOf() {
        XCTAssertEqual(TodayStore.ConnectionCause.firstLine(.sleeping, reason: .lidClosed, sleptAt: nil, asOf: "6:40 PM"),
                       "Asleep as of 6:40 PM · Its lid is closed.")
    }

    func testAPlainSleepWithAGoodbyeTimeUsesTheSleepingSinceLine() {
        XCTAssertEqual(TodayStore.ConnectionCause.firstLine(.sleeping, reason: nil, sleptAt: "6:46 PM", asOf: "6:50 PM"),
                       String.localizedStringWithFormat(String(localized: "connection.sleepingSince"), "6:46 PM"))
    }

    func testAnotherCauseReadsAsOfTheLastHeardTime() {
        XCTAssertEqual(TodayStore.ConnectionCause.firstLine(.cantConnect, reason: nil, sleptAt: nil, asOf: "6:40 PM"),
                       "\(String(localized: "connection.cantConnect")) · as of 6:40 PM")
    }

    func testWithNoTimeTheLineIsTheCauseTitle() {
        XCTAssertEqual(TodayStore.ConnectionCause.firstLine(.cantConnect, reason: nil, sleptAt: nil, asOf: nil),
                       TodayStore.ConnectionCause.cantConnect.title)
    }
}
