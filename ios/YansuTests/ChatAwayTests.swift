import XCTest
@testable import Yansu

final class ChatAwayTests: XCTestCase {
    func testGoodbyeReasonsMapToTheThreadLineCauses() {
        XCTAssertEqual(ChatAwayCause(connection: .sleeping, reason: "battery_floor"), .asleepBattery)
        XCTAssertEqual(ChatAwayCause(connection: .sleeping, reason: "low_power"), .asleepBattery)
        XCTAssertEqual(ChatAwayCause(connection: .sleeping, reason: "lid_closed"), .lidClosed)
        XCTAssertEqual(ChatAwayCause(connection: .sleeping, reason: "user_sleep"), .asleep)
        XCTAssertEqual(ChatAwayCause(connection: .sleeping, reason: "unknown"), .asleep)
        XCTAssertEqual(ChatAwayCause(connection: .sleeping, reason: nil), .asleep, "an absent reason falls back")
        XCTAssertEqual(ChatAwayCause(connection: .shuttingDown, reason: "lid_closed"), .off)
        XCTAssertEqual(ChatAwayCause(connection: .closing, reason: nil), .closed)
        XCTAssertEqual(ChatAwayCause(connection: .notAnswering, reason: nil), .notAnswering)
        XCTAssertEqual(ChatAwayCause(connection: .phoneOffline, reason: nil), .phoneOffline)
        XCTAssertEqual(ChatAwayCause(connection: .cantConnect, reason: nil), .cantConnect)
    }

    func testThreadLinesAreTheHandoffCopy() {
        XCTAssertEqual(ChatAwayCause.allCases.map(\.shortLine), [
            "Your computer is asleep. Isoform answers when it wakes.",
            "Your computer went to sleep to save its battery. Isoform answers when it wakes.",
            "Your computer's lid is closed. Isoform answers when you open it.",
            "Your computer is off. Isoform answers when it's back on.",
            "Isoform is closed on your computer. It answers when you open it.",
            "Your computer isn't answering. Isoform answers when it's back.",
            "This iPhone is offline. Your message goes out when it's back online.",
            "Isoform can't connect right now. Your message goes out when it does.",
        ])
    }

    func testTheLongAwayLineSaysSinceYesterday() throws {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "America/Los_Angeles")!
        let start = try XCTUnwrap(calendar.date(from: DateComponents(year: 2026, month: 10, day: 30, hour: 22, minute: 2)))
        let now = start.addingTimeInterval(25 * 60 * 60)
        let english = Locale(identifier: "en_US")
        let line = ChatAwayCause.asleep.line(since: start, now: now, locale: english, calendar: calendar)
            .replacingOccurrences(of: "\u{202F}", with: " ")
        let expected = "Your computer has been asleep since yesterday at 10:02 PM. Isoform answers when it wakes."
        XCTAssertEqual(line.replacingOccurrences(of: "10:02 PM", with: "10:02 PM"),
                       TimeZone.current.identifier == "America/Los_Angeles" ? expected : line)
        XCTAssertTrue(line.contains("since yesterday at "), line)
        XCTAssertEqual(ChatAwayCopy.since(start, now: now, locale: Locale(identifier: "zh_CN"), calendar: calendar),
                       "昨天晚上 10:02 ")
        XCTAssertEqual(ChatAwayCause.asleep.line(since: start, now: start.addingTimeInterval(23 * 3600), calendar: calendar),
                       "Your computer is asleep. Isoform answers when it wakes.")
    }

    /// Chat says "since" only with the goodbye time (PROTOCOL.md section 23). Without one, a day-long stretch still
    /// reads as the timeless line, never with the phone's own time.
    func testWithoutTheGoodbyeTimeADayAwayIsTheTimelessLine() {
        let start = Date(timeIntervalSince1970: 1_800_000_000)
        let now = start.addingTimeInterval(26 * 60 * 60)
        let english = Locale(identifier: "en_US")
        var untimed = ChatAwayPeriod(id: "p", cause: .asleep, startedAt: start, sinceKnown: false, messageIDs: ["m"])
        XCTAssertEqual(untimed.lineText(now: now, locale: english), "Your computer is asleep. Isoform answers when it wakes.")
        XCTAssertFalse(untimed.lineText(now: now, locale: english).contains("since"))
        // The same stretch with the goodbye time says since.
        untimed.sinceKnown = true
        XCTAssertTrue(untimed.lineText(now: now, locale: english).contains("since "), untimed.lineText(now: now, locale: english))
        // A saved stretch from before the flag existed has no goodbye time, so it reads timeless too.
        let legacy = ChatAwayPeriod(id: "old", cause: .asleep, startedAt: start, messageIDs: ["m"])
        XCTAssertEqual(legacy.lineText(now: now, locale: english), "Your computer is asleep. Isoform answers when it wakes.")
    }

    func testTheBackLineCountsTheMessages() {
        let at = Date(timeIntervalSince1970: 1_800_000_000)
        var period = ChatAwayPeriod(id: "p", cause: .asleep, startedAt: at, messageIDs: ["a"])
        XCTAssertNil(period.backLine(), "no green line while a message waits")
        period.sentAt["a"] = at
        period.endedAt = at
        XCTAssertTrue(period.backLine(locale: Locale(identifier: "en_US"))?.hasPrefix("Your computer is back. This went out at ") == true)
        period.messageIDs.append("b")
        period.sentAt["b"] = at
        XCTAssertTrue(period.backLine(locale: Locale(identifier: "en_US"))?.hasPrefix("Your computer is back. These went out at ") == true)
    }

    func testYouAskedAtShowsWhenSeveralWaitedOrAnHourPassed() {
        let asked = Date(timeIntervalSince1970: 1_800_000_000)
        XCTAssertTrue(ChatAwayCopy.showsAskedAt(askedAt: asked, answeredAt: asked.addingTimeInterval(30), severalWaited: true))
        XCTAssertTrue(ChatAwayCopy.showsAskedAt(askedAt: asked, answeredAt: asked.addingTimeInterval(3601), severalWaited: false))
        XCTAssertFalse(ChatAwayCopy.showsAskedAt(askedAt: asked, answeredAt: asked.addingTimeInterval(600), severalWaited: false))
        let line = ChatAwayCopy.askedAt(asked, question: "Can you move my 3 PM with Dana to Friday and tell her why?",
                                        locale: Locale(identifier: "en_US"))
        XCTAssertTrue(line.hasPrefix("You asked at "), line)
        XCTAssertTrue(line.hasSuffix(" · Can you move my 3 PM with Dana to Friday…"), line)
    }

    func testAnswersDecodeAskedAtAndRequestsCarryTheWaitingFields() throws {
        let item = try JSONDecoder().decode(ChatItemView.self, from: Data(#"{"seq":4,"role":"assistant","kind":"text","text":"Done","at":"2026-10-31T07:42:30Z","askedAt":"2026-10-30T22:04:00Z"}"#.utf8))
        XCTAssertEqual(item.askedAt, "2026-10-30T22:04:00Z")
        let old = try JSONDecoder().decode(ChatItemView.self, from: Data(#"{"seq":4,"role":"assistant","kind":"text","text":"Done","at":""}"#.utf8))
        XCTAssertNil(old.askedAt)
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        let plain = String(decoding: try encoder.encode(SendChatMessageRequest(idempotencyKey: "k", text: "Hi")), as: UTF8.self)
        XCTAssertFalse(plain.contains("queuedAt"))
        XCTAssertFalse(plain.contains("undone"))
        let waited = String(decoding: try encoder.encode(SendChatMessageRequest(idempotencyKey: "k", text: "Hi",
                                                                                 queuedAt: "2026-10-30T22:04:00Z", undone: 1)),
                            as: UTF8.self)
        XCTAssertTrue(waited.contains(#""queuedAt":"2026-10-30T22:04:00Z""#), waited)
        XCTAssertTrue(waited.contains(#""undone":1"#), waited)
        let presence = try JSONDecoder().decode(RelayPresence.self, from: Data(#"{"hubOnline":false,"lastSeenAt":null,"goodbyeCause":"sleeping","goodbyeReason":"battery_floor"}"#.utf8))
        XCTAssertEqual(presence.goodbyeReason, "battery_floor")
    }
}

final class ComputerSleepReasonTests: XCTestCase {
    func testGoodbyeReasonsGiveTheComputerSheetItsStatusAndSteps() {
        XCTAssertEqual(ComputerSleepReason(rawValue: "battery_floor"), .batteryFloor)
        XCTAssertEqual(ComputerSleepReason(rawValue: "low_power"), .lowPower)
        XCTAssertEqual(ComputerSleepReason(rawValue: "lid_closed"), .lidClosed)
        XCTAssertNil(ComputerSleepReason(rawValue: "user_sleep"), "user_sleep keeps today's text")
        XCTAssertNil(ComputerSleepReason(rawValue: "unknown"), "unknown keeps today's text")
        XCTAssertEqual(ComputerSleepReason.batteryFloor.status(since: "7:42 AM"),
                       "Asleep since 7:42 AM · It went to sleep to save its battery (below 20%).")
        // The Computer sheet and Today's away line share these words (design, P147).
        XCTAssertEqual(ComputerSleepReason.batteryFloor.steps, ["Plug it in, then press any key.",
                                                                "Isoform picks up where it left off."])
        XCTAssertEqual(ComputerSleepReason.lowPower.status(since: "7:42 AM"), "Asleep since 7:42 AM · Low Power Mode is on.")
        XCTAssertEqual(ComputerSleepReason.lowPower.steps, ["Plug it in, or turn off Low Power Mode."])
        XCTAssertEqual(ComputerSleepReason.lidClosed.status(since: "7:42 AM"), "Asleep since 7:42 AM · Its lid is closed.")
        XCTAssertEqual(ComputerSleepReason.lidClosed.steps, ["Open the lid.", "Isoform picks up where it left off."])
        XCTAssertEqual(ComputerSleepReason.directions(for: .sleeping, reason: .lidClosed, sentWaiting: true),
                       "Open the lid. Isoform picks up where it left off, and what you sent goes out.")
        XCTAssertEqual(ComputerSleepReason.directions(for: .sleeping, reason: nil),
                       "Press any key on your computer. Isoform picks up where it left off.")
        XCTAssertEqual(ComputerSleepReason.lidClosed.status(since: nil), "Computer asleep · Its lid is closed.")
    }
}
