import Foundation
import XCTest
@testable import Yansu

/// The Feed from cached briefs: fourteen days, one card per record, the nine sections mapped, and the same keys as
/// the Mac's (PROTOCOL.md section 20.2).
final class FeedAssemblyTests: XCTestCase {
    private let today = "2026-10-16"

    private func brief(_ localDate: String, slot: String = "morning", asOf: String? = nil,
                       _ items: [(section: String, title: String, sourceKind: String, sourceID: String, at: String?)],
                       keyed: Bool = true) -> MobileBriefV1 {
        var brief = MobileBriefV1(version: 1, briefId: "\(localDate)-\(slot)", slot: slot, localDate: localDate,
                                  asOf: asOf ?? "\(localDate)T\(slot == "evening" ? "18" : "07"):00:00Z")
        var counts: [String: Int] = [:]
        for (index, entry) in items.enumerated() {
            let ref = "e\(index + 1)"
            brief.evidence.append(BriefEvidence(ref: ref, sourceKind: entry.sourceKind, sourceId: entry.sourceID))
            counts[entry.section, default: 0] += 1
            var item = BriefItem(id: "\(entry.section)-\(counts[entry.section] ?? 1)", title: entry.title,
                                 at: entry.at, refs: [ref])
            if keyed {
                item.key = FeedAssembly.itemKey(section: entry.section, sourceKind: entry.sourceKind,
                                                sourceID: entry.sourceID)
            }
            switch entry.section {
            case "moved": brief.sections.moved.append(item)
            case "doing": brief.sections.doing.append(item)
            case "watching": brief.sections.watching.append(item)
            case "meetings": brief.sections.meetings.append(item)
            case "people": brief.sections.people.append(item)
            case "openLoops": brief.sections.openLoops.append(item)
            case "learned": brief.sections.learned.append(item)
            case "deliverables": brief.sections.deliverables.append(item)
            default: brief.sections.schedules.append(item)
            }
        }
        return brief
    }

    func testKeysMatchTheMacsGoldenBrief() {
        // From wails-gui/testdata/mobile_brief_golden_v1.json.
        XCTAssertEqual(FeedAssembly.itemKey(section: "moved", sourceKind: "history", sourceID: "h-2"),
                       "moved.0e4021db1056423d")
        XCTAssertEqual(FeedAssembly.itemKey(section: "learned", sourceKind: "memory", sourceID: "mem-1"),
                       "learned.6b8a24eccd44ad51")
        XCTAssertEqual(FeedAssembly.itemKey(section: "deliverables", sourceKind: "deliverable", sourceID: "chat:d-1"),
                       "deliverables.ee9321458c642e0a")
    }

    func testEveryFeedSectionBecomesItsCardKindAndOpenLoopsStayOut() throws {
        let all = brief(today, [
            ("moved", "Moved", "calendar", "c1", nil), ("meetings", "Meeting", "calendar", "c2", nil),
            ("people", "Person", "principal", "p1", nil), ("deliverables", "Deliverable", "deliverable", "d1", nil),
            ("watching", "Find", "find", "f1", nil), ("doing", "File", "file", "x1", nil),
            ("schedules", "Run", "schedule_run", "r1", nil), ("learned", "Memory", "memory", "m1", nil),
            ("openLoops", "Commitment", "purpose", "o1", nil),
        ])
        let cards = try XCTUnwrap(FeedAssembly.days(from: [all], today: today).first).cards
        XCTAssertEqual(cards.map(\.category), [.moved, .meetings, .people, .ready, .watching, .doing, .schedules, .learned])
        XCTAssertFalse(cards.contains { $0.section == "openLoops" }, "Goals owns open loops")
        XCTAssertEqual(Set(FeedCategory.allCases.map(\.artName)).count, FeedCategory.allCases.count)
    }

    func testADaysBriefsMergeAndTheNewestCopyOfARecordWins() throws {
        let morning = brief(today, [("watching", "github.com", "web", "site:github.com", "2026-10-16T06:00:00Z"),
                                    ("moved", "Filed the report", "history", "h1", "2026-10-16T05:00:00Z")])
        let evening = brief(today, slot: "evening", [("watching", "github.com", "web", "site:github.com",
                                                      "2026-10-16T17:00:00Z")])
        var renamed = evening
        renamed.sections.watching[0].detail = "21 visits"
        let day = try XCTUnwrap(FeedAssembly.days(from: [morning, renamed], today: today).first)
        XCTAssertEqual(day.cards.count, 2)
        let site = try XCTUnwrap(day.cards.first { $0.section == "watching" })
        XCTAssertEqual(site.item.detail, "21 visits")
        XCTAssertEqual(site.briefID, "\(today)-evening", "feedback goes to the brief the card was read from")
    }

    func testARecordShowsOnceOnTheNewestDayThatHasIt() {
        let yesterday = brief("2026-10-15", [("watching", "github.com", "web", "site:github.com", nil),
                                             ("doing", "plan.key", "file", "f1", nil)])
        let todayBrief = brief(today, [("watching", "github.com", "web", "site:github.com", nil)])
        let days = FeedAssembly.days(from: [yesterday, todayBrief], today: today)
        XCTAssertEqual(days.map(\.localDate), [today, "2026-10-15"])
        XCTAssertEqual(days[0].cards.map(\.item.title), ["github.com"])
        XCTAssertEqual(days[1].cards.map(\.item.title), ["plan.key"])
    }

    func testTheFeedKeepsFourteenDays() {
        let briefs = ["2026-10-03", "2026-10-02", "2026-09-20"].map { date in
            brief(date, [("moved", "On \(date)", "history", "h-\(date)", nil)])
        } + [MobileBriefV1(version: 1, briefId: "undated")]
        XCTAssertEqual(FeedAssembly.days(from: briefs, today: today).map(\.localDate), ["2026-10-03"])
    }

    func testABriefFromAMacAheadOfThePhoneCountsAsToday() throws {
        // The Mac is in Tokyo and already on the 17th; the phone in California is on the 16th.
        let ahead = brief("2026-10-17", [("moved", "Booked the venue", "history", "h-ahead", nil)])
        let earlier = brief("2026-10-15", [("moved", "Filed the report", "history", "h-earlier", nil)])
        let days = FeedAssembly.days(from: [earlier, ahead], today: today)
        XCTAssertEqual(days.map(\.localDate), [today, "2026-10-15"])
        XCTAssertEqual(days.first?.cards.map(\.item.title), ["Booked the venue"])
        XCTAssertEqual(FeedAssembly.feedDay("2026-10-17", today: today), today)
        XCTAssertEqual(FeedAssembly.feedDay("2026-10-15", today: today), "2026-10-15")
    }

    func testCardsOfOneKindAreNewestFirst() throws {
        let todayBrief = brief(today, [("moved", "Earlier", "history", "h1", "2026-10-16T05:00:00Z"),
                                       ("moved", "Later", "history", "h2", "2026-10-16T09:00:00Z"),
                                       ("moved", "Untimed", "history", "h3", nil)])
        let cards = try XCTUnwrap(FeedAssembly.days(from: [todayBrief], today: today).first).cards
        XCTAssertEqual(cards.map(\.item.title), ["Later", "Earlier", "Untimed"])
    }

    func testBriefsStoredBeforeKeysGetTheSameKeysAndSendTheirIDs() throws {
        let keyless = brief(today, [("moved", "Sent the deck", "history", "h1", nil),
                                    ("moved", "Sent the deck again", "history", "h1", nil)], keyed: false)
        let cards = try XCTUnwrap(FeedAssembly.days(from: [keyless], today: today).first).cards
        let key = FeedAssembly.itemKey(section: "moved", sourceKind: "history", sourceID: "h1")
        XCTAssertEqual(cards.map(\.key), [key, key + "-2"])
        XCTAssertEqual(cards.map(\.feedbackItemID), ["moved-1", "moved-2"])

        let keyed = brief(today, [("moved", "Sent the deck", "history", "h1", nil)])
        XCTAssertEqual(FeedAssembly.cards(in: keyed).first?.feedbackItemID, key)
    }

    func testOnlyACardOnOneMemoryCanRemoveIt() throws {
        let memory = brief(today, [("learned", "Prefers short replies", "memory", "mem-1", nil)])
        XCTAssertEqual(FeedAssembly.cards(in: memory).first?.memoryID, "mem-1")
        let site = brief(today, [("watching", "github.com", "web", "site:github.com", nil)])
        XCTAssertNil(FeedAssembly.cards(in: site).first?.memoryID)
    }

    func testOneTimeWorkKeepsItsStatusAndFullResultWithoutAChatTarget() throws {
        let data = Data("""
        {"localDate":"2026-10-16","sections":{"moved":[{
          "id":"work-1","title":"Draft the notes","kind":"one_time_work","status":"needs_review",
          "detail":"Finished the notes.\\nThe decisions are listed first.\\nThe remaining questions are at the end.",
          "url":"https://computer.example/chats/work-chat",
          "target":{"kind":"chat","id":"work-chat"}
        }]}}
        """.utf8)
        let decoded = try JSONDecoder().decode(MobileBriefV1.self, from: data)
        let card = try XCTUnwrap(FeedAssembly.cards(in: decoded).first)

        XCTAssertEqual(card.item.title, "Draft the notes")
        XCTAssertEqual(card.statusText, "Needs review")
        XCTAssertEqual(card.item.detail,
                       "Finished the notes.\nThe decisions are listed first.\nThe remaining questions are at the end.")
        XCTAssertNil(card.detailLineLimit, "the phone cannot open another chat to read the rest of the result")
        XCTAssertNil(card.item.url)
        XCTAssertNil(card.item.webURL, "the title renders as plain text instead of a dead chat link")
        let encoded = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(card.item)) as? [String: Any])
        XCTAssertNil(encoded["target"], "unknown chat targets are never retained by the phone")
        XCTAssertEqual(decoded.sections.moved.first?.url, "https://computer.example/chats/work-chat",
                       "presentation does not change the cached source brief")
    }

    func testOneTimeWorkWithNoTargetStillShowsItsStatusAndOtherFeedLinksStayAvailable() throws {
        var source = brief(today, [("moved", "Prepare the report", "history", "work-1", nil),
                                   ("watching", "A useful reference", "web", "reference", nil)])
        source.sections.moved[0].kind = "one_time_work"
        source.sections.moved[0].status = "in_progress"
        source.sections.moved[0].detail = "Isoform is working on it."
        source.sections.watching[0].url = "https://example.com/reference"
        let cards = FeedAssembly.cards(in: source)
        let work = try XCTUnwrap(cards.first { $0.isOneTimeWork })
        XCTAssertEqual(work.statusText, "In progress")
        XCTAssertEqual(work.item.detail, "Isoform is working on it.")
        XCTAssertNil(work.item.webURL)

        let reference = try XCTUnwrap(cards.first { $0.section == "watching" })
        XCTAssertEqual(reference.item.webURL?.absoluteString, "https://example.com/reference")
        XCTAssertNil(reference.statusText)
        XCTAssertEqual(reference.detailLineLimit, 2)
    }

    func testTheLocalDateIsThePhonesCalendarDay() throws {
        let instant = try XCTUnwrap(RelayTime.parse("2026-10-17T02:30:00Z"))
        XCTAssertEqual(FeedAssembly.localDate(for: instant, timeZone: try XCTUnwrap(TimeZone(identifier: "America/Los_Angeles"))),
                       "2026-10-16")
        XCTAssertEqual(FeedAssembly.localDate(for: instant, timeZone: try XCTUnwrap(TimeZone(identifier: "UTC"))), "2026-10-17")
    }

    func testCardAgesReadLikeAFeed() throws {
        let now = try XCTUnwrap(RelayTime.parse("2026-10-16T12:00:00Z"))
        XCTAssertEqual(FeedCard.age(of: now, now: now), "Now")
        XCTAssertEqual(FeedCard.age(of: now.addingTimeInterval(-15 * 60), now: now), "15m")
        XCTAssertEqual(FeedCard.age(of: now.addingTimeInterval(-2 * 3600), now: now), "2h")
        XCTAssertEqual(FeedCard.age(of: now.addingTimeInterval(-3 * 86400), now: now), "3d")
        XCTAssertNil(FeedCard.age(of: nil, now: now))
    }
}
