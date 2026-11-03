import Foundation
import XCTest
@testable import Yansu

final class YourWeekTests: XCTestCase {
    func testYourWeekIsOptionalAndToleratesMalformedPayloads() throws {
        let missing = try decode(#"{"briefId":"week"}"#)
        XCTAssertNil(missing.yourWeek)

        let malformed = try decode(#"{"briefId":"week","yourWeek":"not-an-object"}"#)
        XCTAssertNil(malformed.yourWeek)
    }

    func testYourWeekDropsEmptyRowsAndAppliesCaps() throws {
        let brief = try decode(#"""
        {
          "briefId":"week",
          "yourWeek":{
            "start":"2026-10-19","end":"2026-10-25","lead":" ",
            "time":[
              {"name":"One","hours":4},{"name":"Two","hours":3},{"name":"Three","hours":2},
              {"name":"Four","hours":1},{"other":true,"hours":2},{"other":true,"hours":1},
              {"name":"Empty","hours":0}
            ],
            "done":[
              {"id":"d1","kind":"file","title":"One"},{"id":"d2","kind":"file","title":"Two"},
              {"id":"d3","kind":"file","title":"Three"},{"id":"d4","kind":"file","title":"Four"},
              {"id":"empty","kind":"file","title":" "}
            ],
            "doneMore":0,
            "open":[
              {"itemKey":"o1","title":"One"},{"itemKey":"o2","title":"Two"},
              {"itemKey":"o3","title":"Three"},{"itemKey":"o4","title":"Four"}
            ],
            "learned":[
              {"id":"l1","kind":"memory","text":"One"},{"id":"l2","kind":"memory","text":"Two"},
              {"id":"l3","kind":"memory","text":"Three"},{"id":"empty","kind":"memory","text":" "}
            ],
            "ask":{"text":" ","message":" "}
          }
        }
        """#)
        let week = try XCTUnwrap(brief.yourWeek)
        XCTAssertNil(week.lead)
        XCTAssertEqual(week.time.count, 4)
        XCTAssertEqual(week.time.filter(\.other).count, 1)
        XCTAssertEqual(week.done.map(\.id), ["d1", "d2", "d3"])
        XCTAssertNil(week.doneMore)
        XCTAssertEqual(week.open.map(\.itemKey), ["o1", "o2", "o3"])
        XCTAssertEqual(week.learned.map(\.id), ["l1", "l2"])
        XCTAssertNil(week.ask)
    }

    func testOpenRowsAndAskResolveThroughExistingSendCards() throws {
        let brief = try decode(#"""
        {
          "briefId":"week",
          "conversationKey":"week-version",
          "narrative":[{"text":"Weekly planning is in progress."}],
          "cards":[
            {"id":"open-card","kind":"item","mode":"send","message":"Ask about the open item"},
            {"id":"ask-card","kind":"question","mode":"send","message":"Ask the week's question"}
          ],
          "yourWeek":{
            "start":"2026-10-19","end":"2026-10-25",
            "open":[
              {"itemKey":"open","cardId":"open-card","title":"Open item","label":"Draft a plan"},
              {"itemKey":"missing","cardId":"missing-card","title":"No card","label":"Ask"},
              {"itemKey":"no-label","cardId":"open-card","title":"No label"}
            ],
            "ask":{"text":"Plan next week","message":"Plan next week","cardId":"ask-card"}
          }
        }
        """#)
        let week = try XCTUnwrap(brief.yourWeek)
        XCTAssertEqual(brief.yourWeekOpenCard(for: week.open[0])?.id, "open-card")
        XCTAssertNil(brief.yourWeekOpenCard(for: week.open[1]))
        XCTAssertNil(brief.yourWeekOpenCard(for: week.open[2]))
        guard case .card(let askCard) = brief.yourWeekAskTarget else {
            return XCTFail("Expected the matching send card")
        }
        XCTAssertEqual(askCard.id, "ask-card")

        var topicBrief = brief
        topicBrief.yourWeek?.ask = MobileBriefWeekAsk(text: "Plan", message: "Plan next week", cardId: "missing",
                                                       topicId: "topic-1")
        XCTAssertEqual(topicBrief.yourWeekAskTarget, .topic(id: "topic-1", message: "Plan next week"))
        topicBrief.yourWeek?.ask = MobileBriefWeekAsk(text: "Plan", message: "Plan next week", cardId: "missing")
        XCTAssertNil(topicBrief.yourWeekAskTarget)

        var oldBrief = brief
        oldBrief.conversationKey = nil
        XCTAssertNil(oldBrief.yourWeekOpenCard(for: week.open[0]))
        XCTAssertNil(oldBrief.yourWeekAskTarget)
    }

    func testYourWeekUITestSeedHasSendableOpenCard() throws {
        let brief = FeedUITestSeed.yourWeekBrief(now: Date())
        let week = try XCTUnwrap(brief.yourWeek)
        let row = try XCTUnwrap(week.open.first)

        XCTAssertNotNil(brief.chatReference)
        XCTAssertEqual(brief.yourWeekOpenCard(for: row)?.id, "week-open-card")
        XCTAssertEqual(brief.yourWeekAskTarget, .card(try XCTUnwrap(brief.sendCards.first { $0.id == "week-ask-card" })))
    }

    func testNotNowDismissalPersistsByBriefID() {
        let suite = "YourWeekTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }

        YourWeekAskDismissals(defaults: defaults).hide(briefID: "week-1")
        XCTAssertTrue(YourWeekAskDismissals(defaults: defaults).contains(briefID: "week-1"))
        XCTAssertFalse(YourWeekAskDismissals(defaults: defaults).contains(briefID: "week-2"))
    }

    private func decode(_ json: String) throws -> MobileBriefV1 {
        try JSONDecoder().decode(MobileBriefV1.self, from: Data(json.utf8))
    }
}
