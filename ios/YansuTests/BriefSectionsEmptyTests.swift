import Foundation
import XCTest
@testable import Yansu

/// P131: a section with nothing in it is left out, so a quiet brief shows no empty heading or "nothing here" line.
final class BriefSectionsEmptyTests: XCTestCase {
    func testAnEmptySectionIsLeftOut() {
        XCTAssertFalse(BriefContentView.showsSection([]))
    }

    func testASectionWithAnItemIsShown() throws {
        let item = try JSONDecoder().decode(BriefItem.self, from: Data(#"{"title":"Filed the expense report"}"#.utf8))
        XCTAssertTrue(BriefContentView.showsSection([item]))
    }
}

extension BriefSectionsEmptyTests {
    /// #649: an empty Day shape shows no section; one with active time does.
    func testAnEmptyDayShapeIsLeftOut() throws {
        XCTAssertFalse(BriefContentView.hasDayShape(nil))
        let empty = try JSONDecoder().decode(BriefDayShape.self, from: Data("{}".utf8))
        XCTAssertFalse(BriefContentView.hasDayShape(empty))
        let active = try JSONDecoder().decode(BriefDayShape.self, from: Data(#"{"activeMinutes":42}"#.utf8))
        XCTAssertTrue(BriefContentView.hasDayShape(active))
    }
}
