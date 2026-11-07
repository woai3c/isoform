import XCTest
@testable import Yansu

/// P154 guard: open loops show a heading only when two or more kinds are named. An item with no kind joins the list
/// without a heading, and never reads "Other" or carries a count.
final class OpenLoopHeadingTests: XCTestCase {
    private func loop(_ id: String, _ kind: String?) -> BriefItem {
        var item = BriefItem(id: id, title: id)
        item.kind = kind
        return item
    }

    func testTwoNamedKindsShowHeadingsAndAnUnkindItemJoinsWithoutOne() {
        let groups = BriefContentView.openLoopGroups(for: [
            loop("a", "commitment"), loop("b", "blocker"), loop("c", nil), loop("d", "commitment"),
        ])
        XCTAssertEqual(groups.map(\.heading), ["Commitments", "Blockers", nil])
        XCTAssertEqual(groups.map { $0.items.map(\.id) }, [["a", "d"], ["b"], ["c"]])
    }

    func testOneNamedKindShowsNoHeadingAndNoCount() {
        let groups = BriefContentView.openLoopGroups(for: [loop("a", "commitment"), loop("b", nil)])
        XCTAssertEqual(groups.count, 1)
        XCTAssertNil(groups[0].heading)
        XCTAssertEqual(groups[0].items.map(\.id), ["a", "b"])
    }

    func testOnlyUnkindItemsShowNoHeadingAndNeverOther() {
        let groups = BriefContentView.openLoopGroups(for: [loop("a", nil), loop("b", nil)])
        XCTAssertEqual(groups.count, 1)
        XCTAssertNil(groups[0].heading)
        XCTAssertFalse(groups.contains { ($0.heading ?? "").contains("Other") })
    }

    func testNoHeadingEverCarriesACountOrTheWordOther() {
        let groups = BriefContentView.openLoopGroups(for: [
            loop("a", "commitment"), loop("b", "question"), loop("c", nil), loop("d", "unknown_kind"),
        ])
        for group in groups {
            guard let heading = group.heading else { continue }
            XCTAssertFalse(heading.contains("("), heading)
            XCTAssertNotEqual(heading, "Other", heading)
        }
    }
}
