import Foundation
import XCTest
@testable import Yansu

// The title on every screen comes from one rule (handoff 2026-10-28, section 1).
final class BriefTitleTests: XCTestCase {
    private func date(_ text: String) throws -> Date {
        try XCTUnwrap(RelayTime.parse(text))
    }

    private func expectedTitle(_ name: String, _ time: Date) -> String {
        "\(name) · \(BriefView.timeText(time))"
    }

    func testScheduledMorningUsesItsDueTime() throws {
        let due = try date("2026-10-28T13:50:00Z")
        let input = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil,
                                    madeAt: try date("2026-10-28T13:51:00Z"), dueAt: due, timezone: nil)
        XCTAssertEqual(BriefTitle.title(input), expectedTitle(String(localized: "Morning brief"), due))
        XCTAssertNil(BriefTitle.lateLine(input))
    }

    /// A brief made before its slot names the time it was made, never its slot's time (R64, P157).
    func testMadeAheadBriefNamesItsMadeAtTimeNotItsSlot() throws {
        let slot = try date("2026-11-08T14:50:00Z")
        let made = try date("2026-11-08T07:20:00Z")
        let input = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil, madeAt: made, dueAt: slot,
                                    timezone: "America/Los_Angeles", madeAhead: true)
        let title = BriefTitle.title(input)
        XCTAssertTrue(title.hasPrefix(String(localized: "Morning brief")))
        XCTAssertFalse(title.contains(BriefView.timeText(slot)))
        XCTAssertTrue(title.contains(BriefView.timeText(made)) || title.contains("11:20"))
    }

    func testMadeAheadDaytimeBriefSaysMadeAt() throws {
        let slot = try date("2026-11-08T14:50:00Z")
        let made = try date("2026-11-08T15:00:00Z")
        let input = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil, madeAt: made, dueAt: slot,
                                    timezone: "America/Los_Angeles", madeAhead: true)
        XCTAssertFalse(BriefTitle.title(input).contains("last night"))
    }

    /// The day before the slot, before 6 PM, reads "yesterday" (early-brief v1.0.1).
    func testMadeAheadBriefFromTheDayBeforeBefore6PMSaysYesterday() throws {
        let slot = try date("2026-11-08T14:50:00Z")
        let made = try date("2026-11-08T01:00:00Z")
        let input = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil, madeAt: made, dueAt: slot,
                                    timezone: "America/Los_Angeles", madeAhead: true)
        let title = BriefTitle.title(input)
        XCTAssertTrue(title.contains("made yesterday at"), title)
        XCTAssertFalse(title.contains("last night"), title)
    }

    /// An older day gives its date, not a bare time (v1.0.2).
    func testMadeAheadFromAnOlderDayNamesTheDate() throws {
        let slot = try date("2026-11-08T14:50:00Z")
        let made = try date("2026-11-06T23:10:00Z")
        let input = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil, madeAt: made, dueAt: slot,
                                    timezone: "America/Los_Angeles", madeAhead: true)
        let title = BriefTitle.title(input)
        XCTAssertTrue(title.hasPrefix(String(localized: "Morning brief") + " · made "), title)
        XCTAssertFalse(title.contains("last night"), title)
        XCTAssertFalse(title.contains("yesterday"), title)
    }

    /// With no time, a made-ahead brief shows its slot's name alone (v1.0.2).
    func testMadeAheadWithNoTimeShowsTheSlotNameAlone() throws {
        let slot = try date("2026-11-08T14:50:00Z")
        let input = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil, madeAt: nil, dueAt: slot,
                                    timezone: "America/Los_Angeles", madeAhead: true)
        XCTAssertEqual(BriefTitle.title(input), String(localized: "Morning brief"))
    }

    func testScheduledEveningAndTheWeekUseTheirKindAndDueTime() throws {
        let due = try date("2026-10-28T18:00:00Z")
        let made = try date("2026-10-28T18:02:00Z")
        let evening = BriefTitleInput(kind: "evening", slot: "evening", displaySlot: nil, madeAt: made, dueAt: due, timezone: nil)
        XCTAssertEqual(BriefTitle.title(evening), expectedTitle(String(localized: "Evening brief"), due))
        let week = BriefTitleInput(kind: "week", slot: "evening", displaySlot: nil, madeAt: made, dueAt: due, timezone: nil)
        XCTAssertEqual(BriefTitle.title(week), expectedTitle(String(localized: "Your week"), due))
    }

    func testAskedForBriefUsesTheTimeItWasMade() throws {
        let made = try date("2026-10-28T15:41:00Z")
        let input = BriefTitleInput(kind: "on_request", slot: "on_request", displaySlot: "evening", madeAt: made,
                                    dueAt: nil, timezone: nil)
        XCTAssertEqual(BriefTitle.title(input), expectedTitle(String(localized: "Brief"), made))
        XCTAssertNil(BriefTitle.lateLine(input))
    }

    func testLateBriefNamesTheTimeItWasMadeAndWhenItWasDue() throws {
        let due = try date("2026-10-28T13:50:00Z")
        let made = try date("2026-10-28T14:12:00Z")
        let input = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil, madeAt: made, dueAt: due, timezone: nil)
        XCTAssertTrue(BriefTitle.isLate(input))
        XCTAssertEqual(BriefTitle.title(input), expectedTitle(String(localized: "Morning brief"), made))
        XCTAssertEqual(BriefTitle.lateLine(input),
                       String(format: String(localized: "Late: your computer was asleep at %@."), BriefView.timeText(due)))
    }

    func testFewMinutesOfSlackIsNotLate() throws {
        let due = try date("2026-10-28T13:50:00Z")
        let slightlyAfter = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil,
                                            madeAt: due.addingTimeInterval(BriefTitle.lateTolerance), dueAt: due, timezone: nil)
        XCTAssertFalse(BriefTitle.isLate(slightlyAfter))
        XCTAssertNil(BriefTitle.lateLine(slightlyAfter))
        let beyond = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil,
                                     madeAt: due.addingTimeInterval(BriefTitle.lateTolerance + 1), dueAt: due, timezone: nil)
        XCTAssertTrue(BriefTitle.isLate(beyond))
    }

    func testBriefFromBeforeKindsKeepsTheSlotNameAndItsTime() throws {
        let made = try date("2026-10-28T13:00:00Z")
        let legacy = BriefTitleInput(kind: nil, slot: "evening", displaySlot: nil, madeAt: made, dueAt: nil, timezone: nil)
        XCTAssertEqual(BriefTitle.kindName(legacy), String(localized: "Evening brief"))
        XCTAssertEqual(BriefTitle.title(legacy), expectedTitle(String(localized: "Evening brief"), made))
    }

    func testBriefWithoutAnyTimeShowsOnlyItsName() {
        let input = BriefTitleInput(kind: "morning", slot: "morning", displaySlot: nil, madeAt: nil, dueAt: nil, timezone: nil)
        XCTAssertEqual(BriefTitle.title(input), String(localized: "Morning brief"))
    }

    func testMobileBriefAndLibraryRowAgreeOnTheTitle() throws {
        var brief = MobileBriefV1(briefId: "2026-10-28-evening", slot: "evening", localDate: "2026-10-28",
                                  asOf: "2026-10-28T18:02:00Z", narrativeStatus: "ok")
        brief.kind = "evening"
        brief.dueAt = "2026-10-28T18:00:00Z"
        let due = try date("2026-10-28T18:00:00Z")
        XCTAssertEqual(brief.titleText, expectedTitle(String(localized: "Evening brief"), due))
        let entry = HistoryEntry(id: brief.identifier, slot: brief.slot, localDate: brief.localDate, asOf: brief.asOfDate,
                                 narrativeStatus: brief.narrativeStatus, isCached: true, kind: brief.kind, dueAt: brief.dueAt)
        XCTAssertEqual(entry.titleText, brief.titleText)
    }

    func testPastBriefRowUsesItsScheduledTimeAsTheDueTime() throws {
        let index = TodayBriefIndex(id: "row", slot: "morning", localDate: "2026-10-27", scheduledAt: "2026-10-27T13:50:00Z",
                                    madeAt: "2026-10-27T14:20:00Z", catchUp: true, durationSeconds: nil, lead: "Lead.",
                                    kind: "morning")
        let title = BriefTitle.title(BriefTitleInput(index: index))
        let made = try date("2026-10-27T14:20:00Z")
        XCTAssertEqual(title, expectedTitle(String(localized: "Morning brief"), made))
        XCTAssertNotNil(BriefTitle.lateLine(BriefTitleInput(index: index)))
    }

    func testChatNeverOffersABriefWithNoWords() {
        var empty = MobileBriefV1(briefId: "empty", narrativeStatus: "ok", narrative: [BriefParagraph(text: "  ")])
        XCTAssertFalse(empty.hasWords)
        XCTAssertFalse(BriefCache.isCurrentCandidate(empty))
        empty.narrative = [BriefParagraph(text: "A sentence to read.")]
        XCTAssertTrue(empty.hasWords)
    }
}
