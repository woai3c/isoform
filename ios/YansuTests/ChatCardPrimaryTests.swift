import XCTest
@testable import Yansu

/// P130: the card's filled button is the first choice that is not a dismissal; Not now is never filled.
final class ChatCardPrimaryTests: XCTestCase {
    private func button(_ id: String) -> ChatCardButton {
        ChatCardButton(id: id, labelKey: id, labelValue: nil, behavior: .answer, actionID: nil)
    }

    func testNotNowAloneIsNeverTheFilledButton() {
        XCTAssertNil(ChatCardPrimary.index(in: [button("not_now")]))
    }

    func testTheFirstRealActionIsFilledWhateverItsSlot() {
        XCTAssertEqual(ChatCardPrimary.index(in: [button("review"), button("not_now")]), 0)
        XCTAssertEqual(ChatCardPrimary.index(in: [button("not_now"), button("send")]), 1)
    }
}
