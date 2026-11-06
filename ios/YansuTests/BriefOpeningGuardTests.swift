import Foundation
import XCTest
@testable import Yansu

/// Guard for P126: the card, the Brief headline and Past briefs open on the brief's own sentence, never on an item's
/// title (a site, a file or a thing the person worked on).
final class BriefOpeningGuardTests: XCTestCase {
    private func itemTitles(_ brief: MobileBriefV1) -> Set<String> {
        let sections = brief.sections
        let all = sections.workingToward + sections.commitments + sections.waitingOnYou + sections.moved + sections.doing
            + sections.watching + sections.meetings + sections.people + sections.openLoops + sections.learned
            + sections.deliverables + sections.schedules
        return Set(all.compactMap(\.title))
    }

    func testSeededBriefsOpenOnASentenceNotAnItemTitle() {
        let briefs = FeedUITestSeed.briefs(today: true, now: Date())
        XCTAssertFalse(briefs.isEmpty)
        for brief in briefs {
            let titles = itemTitles(brief)
            XCTAssertFalse(titles.isEmpty, "the seed should carry items, or the guard proves nothing")
            let lead = PastBriefIndex.lead(brief)
            XCTAssertNotNil(lead)
            XCTAssertFalse(titles.contains(lead ?? ""), "lead \(lead ?? "nil") is an item title")
            let opening = TodayBriefCard.opening(brief)?.text
            XCTAssertNotNil(opening)
            XCTAssertFalse(titles.contains(opening ?? ""), "opening \(opening ?? "nil") is an item title")
        }
    }
}
