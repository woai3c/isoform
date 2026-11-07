import Foundation
import XCTest
@testable import Yansu

/// Guards for P125: Today is never blank on a paired phone. The copy follows the computer's snapshot only.
final class TodayAwaitingBriefTests: XCTestCase {
    private func snapshot(briefs: String = "[]", currentBrief: String = "null", briefSchedule: String = "null") throws -> MobileTodaySnapshot {
        let json = """
        {"generatedAt":"2026-11-06T19:00:00Z","briefs":\(briefs),"needsYou":[],"needsYouCount":0,
         "day":{"free":[],"calendarConnected":false},"topics":[],"schedule":{"morning":"06:50","evening":"18:30"},
         "currentBrief":\(currentBrief),"briefSchedule":\(briefSchedule)}
        """
        return try JSONDecoder().decode(MobileTodaySnapshot.self, from: Data(json.utf8))
    }

    private let briefEntry = #"[{"id":"2026-11-05-evening","slot":"evening","localDate":"2026-11-05","madeAt":"2026-11-05T18:30:00Z","lead":"Your day","version":"abc"}]"#

    func testNoSnapshotYetIsStillFetching() {
        XCTAssertTrue(TodayAwaitingBrief.isFetching(nil))
    }

    func testSnapshotNamingABriefIsFetchingUntilItsTextIsSaved() throws {
        XCTAssertTrue(TodayAwaitingBrief.isFetching(try snapshot(briefs: briefEntry)))
        XCTAssertTrue(TodayAwaitingBrief.isFetching(try snapshot(currentBrief: #"{"id":"x","version":"y"}"#)))
    }

    func testSnapshotWithNoBriefIsNotFetching() throws {
        XCTAssertFalse(TodayAwaitingBrief.isFetching(try snapshot()))
    }

    func testNextBriefIsTheEarliestScheduledSlotAfterNow() throws {
        let morning = #"{"slot":"morning","at":"2026-11-07T14:50:00Z"}"#
        let evening = #"{"slot":"evening","at":"2026-11-06T02:30:00Z"}"#
        let past = #"{"slot":"morning","at":"2026-11-05T14:50:00Z"}"#
        let value = try snapshot(briefSchedule: "[\(past),\(morning),\(evening)]")
        let now = try XCTUnwrap(RelayTime.parse("2026-11-06T19:00:00Z"))
        let next = try XCTUnwrap(TodayAwaitingBrief.nextBriefDate(in: value, after: now))
        XCTAssertEqual(next, try XCTUnwrap(RelayTime.parse("2026-11-07T14:50:00Z")))
    }

    func testNoScheduleGivesNoNextBriefLine() throws {
        XCTAssertNil(TodayAwaitingBrief.nextBriefLine(in: try snapshot(), now: Date(), locale: Locale(identifier: "en_US")))
    }

    /// #631: a brief from an earlier day names its day, and Chinese time text keeps the space before the time.
    func testEarlierBriefNamesItsDayAndChineseTimeKeepsItsSpace() throws {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .current
        let now = try XCTUnwrap(calendar.date(from: DateComponents(year: 2026, month: 11, day: 6, hour: 12)))
        let yesterday = try XCTUnwrap(calendar.date(from: DateComponents(year: 2026, month: 11, day: 5, hour: 7, minute: 40)))
        let english = Locale(identifier: "en_US")
        XCTAssertEqual(TodayAwaitingBrief.dayName(for: yesterday, now: now, locale: english), "Yesterday")
        XCTAssertNil(TodayAwaitingBrief.dayName(for: now, now: now, locale: english))
        let early = try XCTUnwrap(calendar.date(from: DateComponents(year: 2026, month: 11, day: 6, hour: 6, minute: 50)))
        XCTAssertTrue(TodayAwaitingBrief.timeText(early, locale: Locale(identifier: "zh_CN")).contains("早上 6:50"))
        XCTAssertTrue(TodayAwaitingBrief.timeText(early, locale: english).hasPrefix("6:50"))
    }

    /// #631: a failed fetch shows its retry line only while its last outcome is a retry, and only for today's brief.
    @MainActor func testTodayFetchStateSaysRetryingUntilTheTriesStopThenStopped() {
        let today = "2026-11-06"
        XCTAssertEqual(BriefStore.todayFetchState(["2026-11-06-morning": "attempt 1 failed: hub noAnswer; retrying in 5s"], localDate: today), .retrying)
        XCTAssertEqual(BriefStore.todayFetchState(["2026-11-06-morning": "stopped after 1 attempt(s): hub notPaired"], localDate: today), .stopped)
        XCTAssertEqual(BriefStore.todayFetchState(["2026-11-06-morning": "ended: fetch stopped while retrying"], localDate: today), .stopped)
        XCTAssertEqual(BriefStore.todayFetchState(["2026-11-06-morning": "saved after 2 attempt(s)"], localDate: today), .none)
        XCTAssertEqual(BriefStore.todayFetchState(["2026-11-05-morning": "stopped after 1 attempt(s): hub notPaired"], localDate: today), .none)
    }

    @MainActor func testRetryLineFollowsTheLastFetchOutcomeForToday() {
        let today = "2026-11-06"
        XCTAssertEqual(BriefStore.todayFetchState(["2026-11-06-morning": "attempt 1 failed: hub noAnswer; retrying in 5s"], localDate: today), .retrying)
        XCTAssertNotEqual(BriefStore.todayFetchState(["2026-11-06-morning": "saved after 2 attempt(s)"], localDate: today), .retrying)
        XCTAssertNotEqual(BriefStore.todayFetchState(["2026-11-06-morning": "ended: fetch stopped while retrying"], localDate: today), .retrying)
        XCTAssertNotEqual(BriefStore.todayFetchState(["2026-11-05-morning": "attempt 1 failed: hub noAnswer; retrying in 5s"], localDate: today), .retrying)
        XCTAssertNotEqual(BriefStore.todayFetchState([:], localDate: today), .retrying)
    }
}
