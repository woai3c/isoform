import Foundation
import XCTest
@testable import Yansu

final class LocalNoticePlannerTests: XCTestCase {
    private func settings(quietStart: String = "22:00", quietEnd: String = "08:00", budget: Int = 6,
                          enabled: Bool = true) -> NotificationSettings {
        let kind = NotificationKind(kind: "work_blocked", enabled: enabled, defaultOn: true, paused: false,
                                    mutedUntil: nil, sent: 0, opened: 0, acted: 0, dismissed: 0, muted: 0)
        return NotificationSettings(available: true, kinds: [kind], quietStart: quietStart, quietEnd: quietEnd,
                                    dailyInterruptBudget: budget, digestTimes: [], statsDays: 30)
    }
    private func notice(_ id: String, at: Date, expires: Date? = nil, priority: Double = 0,
                        timeSensitive: Bool = false) -> LocalOpenNotice {
        LocalOpenNotice(id: id, kind: "work_blocked", title: id, body: id, moment: at,
                        expires: expires, priority: priority, timeSensitive: timeSensitive,
                        countsBudget: !timeSensitive)
    }
    private var utc: Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(secondsFromGMT: 0)!
        return calendar
    }

    func testNewOpenItemsBatchAndResolvedOrSeenItemsStayOut() throws {
        let now = try XCTUnwrap(RelayTime.parse("2026-10-27T12:00:00Z"))
        let items = [notice("a", at: now), notice("b", at: now, priority: 10), notice("c", at: now),
                     notice("seen", at: now), notice("expired", at: now, expires: now)]
        let result = LocalNoticePlanner.plan(current: items, previouslySeen: ["seen", "resolved"], held: [],
                                             delivered: [], settings: settings(), now: now, activeDeliveredToday: 0,
                                             lastQuietBreak: nil, calendar: utc)
        XCTAssertTrue(result.batch)
        XCTAssertEqual(LocalNoticePlanner.batchTitle(count: 3, locale: Locale(identifier: "en_US")), "3 things need you")
        XCTAssertEqual(LocalNoticePlanner.batchTitle(count: 3, locale: Locale(identifier: "zh_CN")), "3 件事需要你")
        XCTAssertEqual(result.deliver.map(\.id), ["b", "a", "c"])
        XCTAssertTrue(result.deferred.isEmpty)
    }

    func testQuietItemsWaitAndAreRecheckedAndBudgetHoldsActiveItems() throws {
        let quiet = try XCTUnwrap(RelayTime.parse("2026-10-27T23:00:00Z"))
        let items = [notice("one", at: quiet), notice("urgent", at: quiet, timeSensitive: true)]
        let held = LocalNoticePlanner.plan(current: items, previouslySeen: [], held: [], delivered: [],
                                            settings: settings(), now: quiet, activeDeliveredToday: 0,
                                            lastQuietBreak: quiet.addingTimeInterval(-60), calendar: utc)
        XCTAssertEqual(held.deferred, ["one", "urgent"])
        let later = try XCTUnwrap(RelayTime.parse("2026-10-28T08:01:00Z"))
        let rechecked = LocalNoticePlanner.plan(current: [items[0]], previouslySeen: ["one", "urgent"], held: held.deferred,
                                                 delivered: [], settings: settings(budget: 1), now: later,
                                                 activeDeliveredToday: 1, lastQuietBreak: nil, calendar: utc)
        XCTAssertTrue(rechecked.deliver.isEmpty)
        XCTAssertEqual(rechecked.deferred, ["one"])
        let muted = LocalNoticePlanner.plan(current: items, previouslySeen: [], held: [], delivered: [],
                                             settings: settings(enabled: false), now: later, activeDeliveredToday: 0,
                                             lastQuietBreak: nil, calendar: utc)
        XCTAssertTrue(muted.deliver.isEmpty)
    }

    func testLateSuffixUsesMomentTimeOnlyAfterFifteenMinutes() throws {
        let moment = try XCTUnwrap(RelayTime.parse("2026-10-27T08:12:00Z"))
        XCTAssertNil(LocalNoticePlanner.lateSuffix(moment: moment, now: moment.addingTimeInterval(900), locale: Locale(identifier: "en_US"), calendar: utc))
        XCTAssertEqual(LocalNoticePlanner.lateSuffix(moment: moment, now: moment.addingTimeInterval(901), locale: Locale(identifier: "en_US"), calendar: utc), "· 8:12 AM")
        XCTAssertEqual(LocalNoticePlanner.lateSuffix(moment: moment, now: moment.addingTimeInterval(901), locale: Locale(identifier: "zh_CN"), calendar: utc), "· 上午 8:12")
    }
}
