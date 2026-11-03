import Foundation
import XCTest
@testable import Yansu

// Past briefs groups by day, and the brief view moves through the briefs in time order (handoff 2026-10-28, section 5).
final class BriefPastBriefsTests: XCTestCase {
    private func row(_ id: String, localDate: String, madeAt: String) -> TodayBriefIndex {
        TodayBriefIndex(id: id, slot: "morning", localDate: localDate, scheduledAt: nil, madeAt: madeAt,
                        catchUp: nil, durationSeconds: nil, lead: "Lead \(id).")
    }

    func testRowsGroupByTodayYesterdayThenDateKeepingTheirOrder() {
        let rows = [
            row("today-evening", localDate: "2026-10-28", madeAt: "2026-10-28T18:00:00Z"),
            row("today-morning", localDate: "2026-10-28", madeAt: "2026-10-28T07:00:00Z"),
            row("yesterday", localDate: "2026-10-27", madeAt: "2026-10-27T18:00:00Z"),
            row("older", localDate: "2026-10-20", madeAt: "2026-10-20T07:00:00Z"),
            row("older-again", localDate: "2026-10-20", madeAt: "2026-10-20T18:00:00Z"),
        ]
        let groups = PastBriefGroups.groups(rows, today: "2026-10-28", yesterday: "2026-10-27")
        XCTAssertEqual(groups.map(\.day), [.today, .yesterday, .date("2026-10-20")])
        XCTAssertEqual(groups[0].rows.map(\.id), ["today-evening", "today-morning"])
        XCTAssertEqual(groups[1].rows.map(\.id), ["yesterday"])
        XCTAssertEqual(groups[2].rows.map(\.id), ["older", "older-again"])
    }

    func testAGroupWithNoRowsIsNotShown() {
        let rows = [row("only", localDate: "2026-10-20", madeAt: "2026-10-20T07:00:00Z")]
        XCTAssertEqual(PastBriefGroups.groups(rows, today: "2026-10-28", yesterday: "2026-10-27").map(\.day),
                       [.date("2026-10-20")])
        XCTAssertTrue(PastBriefGroups.groups([], today: "2026-10-28", yesterday: "2026-10-27").isEmpty)
    }

    private func brief(_ id: String, asOf: String, words: Bool = true) -> MobileBriefV1 {
        MobileBriefV1(briefId: id, slot: "morning", localDate: "2026-10-28", asOf: asOf, narrativeStatus: "ok",
                      narrative: words ? [BriefParagraph(text: "Words for \(id).")] : [BriefParagraph(text: "")])
    }

    func testTheBriefViewMovesThroughBriefsWithWordsNewestFirst() {
        let library = [
            brief("middle", asOf: "2026-10-28T12:00:00Z"),
            brief("newest", asOf: "2026-10-28T18:00:00Z"),
            brief("silent", asOf: "2026-10-28T15:00:00Z", words: false),
            brief("oldest", asOf: "2026-10-27T07:00:00Z"),
        ]
        let order = BriefPages.order(library)
        XCTAssertEqual(order, ["newest", "middle", "oldest"], "a brief with no words is never in the sequence")

        let middle = BriefPages.neighbours(of: "middle", in: order)
        XCTAssertEqual(middle.previous, "oldest", "earlier in time is previous")
        XCTAssertEqual(middle.next, "newest", "later in time is next")

        let first = BriefPages.neighbours(of: "newest", in: order)
        XCTAssertNil(first.next)
        XCTAssertEqual(first.previous, "middle")

        let last = BriefPages.neighbours(of: "oldest", in: order)
        XCTAssertNil(last.previous)
        XCTAssertEqual(last.next, "middle")

        let unknown = BriefPages.neighbours(of: "gone", in: order)
        XCTAssertNil(unknown.previous)
        XCTAssertNil(unknown.next)
    }

    func testLeadSentenceIsTheFirstSentenceOfTheFirstWrittenParagraph() {
        let text = MobileBriefV1(briefId: "lead", narrativeStatus: "ok",
                                 narrative: [BriefParagraph(text: ""), BriefParagraph(text: "First sentence. More detail follows.")])
        XCTAssertEqual(PastBriefIndex.lead(text), "First sentence.")
    }
}
