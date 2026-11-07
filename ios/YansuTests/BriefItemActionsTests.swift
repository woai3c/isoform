import Foundation
import XCTest
@testable import Yansu

/// P152 guard: a brief shows each item once, in its own section, and the item's feedback is in its row. The old
/// Cards section repeated every item; none of these cases may bring a repeated item back, or drop an action.
final class BriefItemActionsTests: XCTestCase {
    private func quietBrief() -> MobileBriefV1 {
        var value = MobileBriefV1(briefId: "2026-11-06-morning", slot: "morning", localDate: "2026-11-06",
                                  timezone: "America/Los_Angeles", asOf: "2026-11-06T14:00:00Z",
                                  narrative: [BriefParagraph(text: "A quiet morning.")], conversationKey: "revision-1")
        value.sections.watching = [BriefItem(id: "watch-1", title: "github.com", refs: ["web-ref"])]
        value.evidence = [BriefEvidence(ref: "web-ref", sourceKind: "web", sourceId: "github.com", label: "github.com")]
        return value
    }

    private func fullBrief() -> MobileBriefV1 {
        var value = quietBrief()
        value.sections.moved = [BriefItem(id: "moved-1", title: "Filed the Halcyon invoice", refs: ["memory-ref"])]
        value.sections.doing = [BriefItem(id: "doing-1", title: "Drafting the launch notes", refs: ["doc-ref"])]
        value.evidence += [BriefEvidence(ref: "memory-ref", sourceKind: "memory", sourceId: "mem-halcyon", label: "Halcyon"),
                           BriefEvidence(ref: "doc-ref", sourceKind: "deliverable", sourceId: "launch-notes", label: "Notes")]
        return value
    }

    /// Every item of the brief is in the Feed's cards exactly once, and each item finds its own card.
    func testEachItemShowsOnceWithItsCardAndNoCardRepeatsAnother() {
        for value in [quietBrief(), fullBrief()] {
            let cards = FeedAssembly.cards(in: value)
            XCTAssertEqual(Set(cards.map(\.key)).count, cards.count, "no card is offered twice")
            let items = value.sections.watching + value.sections.moved + value.sections.doing
            XCTAssertEqual(cards.count, items.count, "one card per item")
            for item in items {
                let card = BriefItemActions.card(for: item, in: cards)
                XCTAssertEqual(card?.item.id, item.id, "\(item.title ?? "") finds its own card")
            }
        }
    }

    /// A plain item offers Useful and Not for me in its row, with feedback allowed; a viewer without feedback
    /// permission offers neither.
    func testAPlainItemOffersUsefulAndNotForMeOnlyWhenFeedbackIsAllowed() throws {
        let cards = FeedAssembly.cards(in: quietBrief())
        let card = try XCTUnwrap(cards.first)
        XCTAssertEqual(BriefItemActions.actions(for: card, canGiveFeedback: true), [.useful, .notForMe])
        XCTAssertEqual(BriefItemActions.actions(for: card, canGiveFeedback: false), [])
    }

    /// A remembered item's row is Sources and Not quite, and nothing else: no Useful and no Not for me, whether or
    /// not the viewer may give feedback. "Not quite" is the row's memory correction, shown when the card names exactly
    /// one remembered fact (the same rule as the card's memoryID), and only with memory-correction permission.
    func testARememberedItemsRowIsSourcesAndNotQuiteOnly() throws {
        let brief = fullBrief()
        let cards = FeedAssembly.cards(in: brief)
        let card = try XCTUnwrap(BriefItemActions.card(for: brief.sections.moved[0], in: cards))
        XCTAssertEqual(card.memoryID, "mem-halcyon", "Not quite names the one remembered fact")
        XCTAssertEqual(BriefItemActions.actions(for: card, canGiveFeedback: true), [], "no Useful, no Not for me")
        XCTAssertEqual(BriefItemActions.actions(for: card, canGiveFeedback: false), [])
        XCTAssertFalse(card.sources.isEmpty, "the row's Sources come from the card")
    }

    /// A plain item in the same brief keeps Useful and Not for me, so the remembered rule is not the default.
    func testAPlainItemInTheSameBriefKeepsItsChips() throws {
        let brief = fullBrief()
        let cards = FeedAssembly.cards(in: brief)
        let card = try XCTUnwrap(BriefItemActions.card(for: brief.sections.doing[0], in: cards))
        XCTAssertNil(card.memoryID)
        XCTAssertEqual(BriefItemActions.actions(for: card, canGiveFeedback: true), [.useful, .notForMe])
    }

    /// The item row falls back to the card's sources when the brief's own sources are empty, so an action the card
    /// offered is still reachable.
    func testTheCardsSourcesAreTheSourcesOfTheRow() throws {
        let cards = FeedAssembly.cards(in: fullBrief())
        let doing = try XCTUnwrap(BriefItemActions.card(for: fullBrief().sections.doing[0], in: cards))
        XCTAssertEqual(doing.sources.map(\.sourceId), ["launch-notes"])
    }
}
