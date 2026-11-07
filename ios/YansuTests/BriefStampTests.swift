import XCTest
@testable import Yansu

/// P136: a brief item's date line follows the desktop rule. Today shows a time alone, this year shows the day and
/// time with no year, and an earlier year shows the year too.
final class BriefStampTests: XCTestCase {
    private let locale = Locale(identifier: "en_US")
    private let utc = TimeZone(identifier: "UTC")!

    private var now: Date { date(2026, 11, 7, 10, 0) }

    private func date(_ year: Int, _ month: Int, _ day: Int, _ hour: Int, _ minute: Int) -> Date {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = utc
        return calendar.date(from: DateComponents(year: year, month: month, day: day, hour: hour, minute: minute))!
    }

    func testTodayIsATimeAlone() {
        let text = BriefClock.stamp(date(2026, 11, 7, 7, 15), now: now, locale: locale, timeZone: utc)
        XCTAssertEqual(text, BriefClock.text(date(2026, 11, 7, 7, 15), locale: locale, timeZone: utc))
        XCTAssertFalse(text.contains("2026"))
        XCTAssertFalse(text.contains("Nov"))
    }

    func testThisYearHasNoYear() {
        let text = BriefClock.stamp(date(2026, 11, 6, 7, 15), now: now, locale: locale, timeZone: utc)
        XCTAssertFalse(text.contains("2026"), text)
        XCTAssertTrue(text.contains("Nov 6"), text)
        XCTAssertTrue(text.contains(" at "), text)
    }

    func testAnEarlierYearKeepsTheYear() {
        let text = BriefClock.stamp(date(2025, 12, 31, 23, 5), now: now, locale: locale, timeZone: utc)
        XCTAssertTrue(text.contains("2025"), text)
    }
}
