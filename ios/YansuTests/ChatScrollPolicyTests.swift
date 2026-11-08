import XCTest
@testable import Yansu

/// The chat's scroll rules over the layouts SwiftUI reports: content top and height in screen coordinates and
/// where the visible area ends above the composer. With the visible area ending at 600, content
/// of 2000 is at the bottom when its top is at -1400.
final class ChatScrollPolicyTests: XCTestCase {
    private let viewport: CGFloat = 600

    private func atBottom(_ policy: inout ChatScrollPolicy, height: CGFloat = 2000) -> ChatScrollPolicy.Action {
        policy.layout(contentTop: viewport - height, contentHeight: height, visibleBottom: viewport)
    }

    func testOpeningTheChatStartsAtTheNewestMessage() {
        var policy = ChatScrollPolicy()
        XCTAssertEqual(policy.appeared(), .scrollToBottom(animated: false))
        // The first layout has the content at its top, as SwiftUI lays it out before any scroll.
        XCTAssertEqual(policy.layout(contentTop: 0, contentHeight: 2000, visibleBottom: viewport),
                       .scrollToBottom(animated: false))
        XCTAssertEqual(atBottom(&policy), .none)
        XCTAssertTrue(policy.pinned)
    }

    func testFirstReadStartsAtItsBeginningWithoutFollowingItsBottom() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        policy.readFirstRead()
        XCTAssertFalse(policy.pinned)
        XCTAssertEqual(policy.layout(contentTop: 0, contentHeight: 2000, visibleBottom: viewport), .none)
        XCTAssertEqual(policy.layout(contentTop: 0, contentHeight: 2200, visibleBottom: viewport), .none)
        XCTAssertEqual(policy.layout(contentTop: -1200, contentHeight: 2200, visibleBottom: viewport), .none)
        XCTAssertFalse(policy.pinned, "layout must not mistake the first read for a bottom-follow request")
        policy.newContentArrived()
        XCTAssertTrue(policy.hasNewBelow)
        policy.readerDragged()
        _ = atBottom(&policy, height: 2200)
        XCTAssertTrue(policy.pinned)
    }

    func testAnOpeningScrollThatStopsShortOfTheNewestMessageKeepsFollowingIt() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        // The first layout has the content at its top, so the chat scrolls to its end.
        XCTAssertEqual(policy.layout(contentTop: 0, contentHeight: 2000, visibleBottom: viewport),
                       .scrollToBottom(animated: false))
        // The scroll stopped 17 pt short of the newest message: still pinned, and the next layout corrects it.
        XCTAssertEqual(policy.layout(contentTop: -1383, contentHeight: 2000, visibleBottom: viewport),
                       .scrollToBottom(animated: false))
        XCTAssertTrue(policy.pinned)
        XCTAssertEqual(atBottom(&policy), .none)
        XCTAssertTrue(policy.pinned)
    }

    func testTheMoveThatSettlesAnAreaChangeScrollIsNotTheReaderPullingBack() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        // The visible area ends 72 pt higher (the composer rose with the bar gone): the chat scrolls to its newest message.
        XCTAssertEqual(policy.layout(contentTop: viewport - 2000, contentHeight: 2000, visibleBottom: viewport - 72),
                       .scrollToBottom(animated: false))
        // The content settles 72 pt lower while that scroll finishes. That move is not the reader pulling back.
        XCTAssertEqual(policy.layout(contentTop: viewport - 2000 + 72, contentHeight: 2000, visibleBottom: viewport - 72),
                       .scrollToBottom(animated: false))
        XCTAssertTrue(policy.pinned, "the settling move does not unpin a reader at the bottom")
    }

    func testInitialProgrammaticScrollDoesNotLookLikeTheReaderScrolledUp() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        XCTAssertEqual(policy.layout(contentTop: 0, contentHeight: 2000, visibleBottom: viewport),
                       .scrollToBottom(animated: false))
        // The opening scroll is still on its way down: it keeps following the bottom rather than reading as a scroll away.
        XCTAssertEqual(policy.layout(contentTop: -500, contentHeight: 2000, visibleBottom: viewport),
                       .scrollToBottom(animated: false))
        XCTAssertTrue(policy.pinned)
        policy.newContentArrived()
        XCTAssertFalse(policy.hasNewBelow)
        XCTAssertEqual(atBottom(&policy), .none)
        XCTAssertTrue(policy.pinned)
    }

    func testTheFreshReadReplacingTheCachedRowsKeepsTheNewestInView() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy, height: 1200)
        // The fresh page is taller; the top has not moved, so nobody scrolled.
        XCTAssertEqual(policy.layout(contentTop: viewport - 1200, contentHeight: 1800, visibleBottom: viewport),
                       .scrollToBottom(animated: false))
        XCTAssertEqual(atBottom(&policy, height: 1800), .none)
        XCTAssertTrue(policy.pinned)
    }

    func testNewRowsAndAStreamingTurnKeepAChatAtTheBottomPinned() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        var height: CGFloat = 2000
        for growth in [CGFloat(20), 80, 140, 200] {
            // A row lands under the bottom the chat sits at; the next layout scrolls to the new bottom.
            let top = viewport - height
            height += growth
            policy.newContentArrived()
            XCTAssertEqual(policy.layout(contentTop: top, contentHeight: height, visibleBottom: viewport),
                           .scrollToBottom(animated: false), "grew by \(growth)")
            XCTAssertEqual(atBottom(&policy, height: height), .none)
        }
        XCTAssertTrue(policy.pinned)
        XCTAssertFalse(policy.hasNewBelow)
    }

    func testAReaderWhoScrolledUpIsNotYankedDownAndIsToldNewerRowsWait() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        // The reader drags the chat down by 400 points to read further up.
        XCTAssertEqual(policy.layout(contentTop: viewport - 2000 + 400, contentHeight: 2000, visibleBottom: viewport),
                       .none)
        XCTAssertFalse(policy.pinned)
        XCTAssertFalse(policy.hasNewBelow)

        policy.newContentArrived()
        XCTAssertEqual(policy.layout(contentTop: viewport - 2000 + 400, contentHeight: 2300, visibleBottom: viewport),
                       .none, "new rows must not move someone reading further up")
        XCTAssertTrue(policy.hasNewBelow)
        XCTAssertFalse(policy.pinned)
    }

    func testScrollingBackToTheBottomFollowsAgain() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        _ = policy.layout(contentTop: viewport - 2000 + 400, contentHeight: 2000, visibleBottom: viewport)
        policy.newContentArrived()

        // Within the margin of the bottom counts as reading the newest message.
        _ = policy.layout(contentTop: viewport - 2000 + 30, contentHeight: 2000, visibleBottom: viewport)
        XCTAssertTrue(policy.pinned)
        XCTAssertFalse(policy.hasNewBelow)
        XCTAssertEqual(policy.layout(contentTop: viewport - 2000 + 30, contentHeight: 2100, visibleBottom: viewport),
                       .scrollToBottom(animated: false))
    }

    func testLatestReturnsToTheNewestAndItsAnimationDoesNotCountAsScrollingAway() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        _ = policy.layout(contentTop: viewport - 2000 + 900, contentHeight: 2000, visibleBottom: viewport)
        XCTAssertFalse(policy.pinned)

        XCTAssertEqual(policy.jumpToLatest(), .scrollToBottom(animated: true))
        XCTAssertTrue(policy.pinned)
        // The animation's frames move the content up toward the bottom.
        for remaining in [CGFloat(600), 300, 100] {
            _ = policy.layout(contentTop: viewport - 2000 + remaining, contentHeight: 2000, visibleBottom: viewport)
            XCTAssertTrue(policy.pinned, "frame \(remaining) points from the bottom")
        }
        // A row lands before the animation ends: the chat still follows it.
        XCTAssertEqual(policy.layout(contentTop: viewport - 2000 + 100, contentHeight: 2200, visibleBottom: viewport),
                       .scrollToBottom(animated: false))
        _ = atBottom(&policy, height: 2200)
        XCTAssertTrue(policy.pinned)
    }

    func testPullingBackUpDuringTheJumpLeavesTheReaderWhereTheyAre() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        _ = policy.layout(contentTop: viewport - 2000 + 900, contentHeight: 2000, visibleBottom: viewport)
        _ = policy.jumpToLatest()
        _ = policy.layout(contentTop: viewport - 2000 + 600, contentHeight: 2000, visibleBottom: viewport)

        _ = policy.layout(contentTop: viewport - 2000 + 700, contentHeight: 2000, visibleBottom: viewport)

        XCTAssertFalse(policy.pinned)
        policy.newContentArrived()
        XCTAssertEqual(policy.layout(contentTop: viewport - 2000 + 700, contentHeight: 2100, visibleBottom: viewport),
                       .none)
    }

    func testReturningToTheChatScrollsToTheLatest() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        _ = policy.layout(contentTop: viewport - 2000 + 900, contentHeight: 2000, visibleBottom: viewport)
        policy.newContentArrived()
        XCTAssertTrue(policy.hasNewBelow)

        XCTAssertEqual(policy.appeared(), .scrollToBottom(animated: false))
        XCTAssertTrue(policy.pinned)
        XCTAssertFalse(policy.hasNewBelow)
        _ = atBottom(&policy)
        XCTAssertTrue(policy.pinned)
    }

    func testTheKeyboardShrinkingTheViewKeepsTheNewestMessageVisible() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)

        XCTAssertEqual(policy.layout(contentTop: viewport - 2000, contentHeight: 2000, visibleBottom: 300),
                       .scrollToBottom(animated: false))
    }

    func testTheKeyboardRisingWhileTheReaderIsAtTheBottomKeepsItThereEvenWhenTheContentMoved() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)

        // The keyboard shortens the visible area and SwiftUI shifts the content with it.
        XCTAssertEqual(policy.layout(contentTop: viewport - 2000 + 150, contentHeight: 2000, visibleBottom: 450),
                       .scrollToBottom(animated: false))
        XCTAssertTrue(policy.pinned)
        XCTAssertFalse(policy.hasNewBelow)
    }

    func testTheKeyboardFinishingItsRiseKeepsAReaderAtTheBottomAndLeavesOneWhoScrolledUpAlone() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        _ = atBottom(&policy)
        XCTAssertEqual(policy.keyboardRose(), .scrollToBottom(animated: false))
        XCTAssertTrue(policy.pinned)

        // The reader pulls back up, so the content moves down, away from the bottom.
        _ = policy.layout(contentTop: viewport - 2000 + 200, contentHeight: 2000, visibleBottom: viewport)
        XCTAssertFalse(policy.pinned, "a reader who scrolled away is not pulled back by the keyboard")
        XCTAssertEqual(policy.keyboardRose(), .none)
    }

    func testAShortChatNeedsNoScroll() {
        var policy = ChatScrollPolicy()
        _ = policy.appeared()
        XCTAssertEqual(policy.layout(contentTop: 0, contentHeight: 300, visibleBottom: viewport), .none)
        XCTAssertEqual(policy.layout(contentTop: 0, contentHeight: 450, visibleBottom: viewport), .none)
        XCTAssertTrue(policy.pinned)
    }
}
