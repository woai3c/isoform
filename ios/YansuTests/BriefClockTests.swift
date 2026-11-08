import XCTest
@testable import Yansu

// The time in brief copy (BriefClock): the design's zh-Hans form, and the system's short time elsewhere.
final class BriefClockTests: XCTestCase {
    private let utc = TimeZone(secondsFromGMT: 0)!
    private let zh = Locale(identifier: "zh-Hans")

    private func at(_ hour: Int, _ minute: Int) throws -> Date {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = utc
        return try XCTUnwrap(calendar.date(from: DateComponents(year: 2026, month: 10, day: 28, hour: hour, minute: minute)))
    }

    func testChineseTimesWriteThePeriodInWordsAsTheDesignDoes() throws {
        XCTAssertEqual(BriefClock.text(try at(6, 50), locale: zh, timeZone: utc), "早上 6:50")
        XCTAssertEqual(BriefClock.text(try at(18, 0), locale: zh, timeZone: utc), "晚上 6:00")
        XCTAssertEqual(BriefClock.text(try at(9, 12), locale: zh, timeZone: utc), "早上 9:12",
                       "the design's own example: 9:12 AM is 早上")
    }

    func testChinesePeriodBoundaries() throws {
        let cases: [(hour: Int, minute: Int, expected: String)] = [
            (0, 0, "晚上 12:00"),
            (4, 59, "晚上 4:59"),
            (5, 0, "早上 5:00"),
            (11, 59, "早上 11:59"),
            (12, 0, "中午 12:00"),
            (12, 59, "中午 12:59"),
            (13, 0, "下午 1:00"),
            (17, 59, "下午 5:59"),
            (18, 0, "晚上 6:00"),
            (23, 59, "晚上 11:59"),
        ]
        for value in cases {
            XCTAssertEqual(BriefClock.text(try at(value.hour, value.minute), locale: zh, timeZone: utc), value.expected,
                           "\(value.hour):\(value.minute)")
        }
    }

    func testPeriodNamesFollowTheHour() {
        XCTAssertEqual(BriefClock.period(hour: 5), "早上")
        XCTAssertEqual(BriefClock.period(hour: 12), "中午")
        XCTAssertEqual(BriefClock.period(hour: 13), "下午")
        XCTAssertEqual(BriefClock.period(hour: 18), "晚上")
        XCTAssertEqual(BriefClock.period(hour: 2), "晚上")
    }

    func testOtherLocalesGetTheSystemShortTime() throws {
        let en = Locale(identifier: "en_US")
        let expected = try at(6, 50).formatted(Date.FormatStyle(date: .omitted, time: .shortened, locale: en, timeZone: utc))
        XCTAssertEqual(BriefClock.text(try at(6, 50), locale: en, timeZone: utc), expected)
        XCTAssertTrue(BriefClock.text(try at(6, 50), locale: en, timeZone: utc).contains("6:50"))
        XCTAssertFalse(BriefClock.text(try at(6, 50), locale: en, timeZone: utc).contains("早上"))
    }

    func testChineseDetectionReadsTheLanguageCode() {
        XCTAssertTrue(BriefClock.isChinese(zh))
        XCTAssertFalse(BriefClock.isChinese(Locale(identifier: "en_US")))
    }

    /// P136: a week's proposed time reads as the brief reads: no year for this year, the year for an earlier one.
    func testProposedTimeDatesOmitTheYearForThisYearOnly() {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "America/Los_Angeles")!
        let locale = Locale(identifier: "en_US")
        let now = calendar.date(from: DateComponents(year: 2026, month: 11, day: 7, hour: 10))!
        let thisYear = calendar.date(from: DateComponents(year: 2026, month: 10, day: 19, hour: 9))!
        let earlier = calendar.date(from: DateComponents(year: 2025, month: 10, day: 19, hour: 9))!
        let zone = calendar.timeZone
        // The system puts a narrow no-break space before AM; compare with plain spaces.
        func plain(_ text: String) -> String {
            text.replacingOccurrences(of: "\u{202F}", with: " ").replacingOccurrences(of: "\u{00A0}", with: " ")
        }
        XCTAssertEqual(plain(BriefClock.stamp(thisYear, now: now, locale: locale, timeZone: zone)), "Oct 19 at 9:00 AM")
        XCTAssertEqual(plain(BriefClock.stamp(earlier, now: now, locale: locale, timeZone: zone)), "Oct 19, 2025 at 9:00 AM")
    }
}
