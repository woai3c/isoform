import Foundation
import XCTest
@testable import Yansu

final class PastBriefsTests: XCTestCase {
    func testIndexUsesLeadsAndIncludesNewerRelayBodiesWithinThirtyDays() throws {
        let now = try XCTUnwrap(RelayTime.parse("2026-10-27T12:00:00Z"))
        let earlier = TodayBriefIndex(id: "earlier", slot: "evening", localDate: "2026-10-26", scheduledAt: nil,
                                      madeAt: "2026-10-26T18:00:00Z", catchUp: nil, durationSeconds: nil, lead: "The plan shipped.")
        // Kept on the phone for 30 days, so a brief from 26 days ago is listed and one from 31 days ago is not.
        let kept = TodayBriefIndex(id: "kept", slot: "morning", localDate: "2026-10-01", scheduledAt: nil,
                                   madeAt: "2026-10-01T07:00:00Z", catchUp: nil, durationSeconds: nil, lead: "Kept.")
        let tooOld = TodayBriefIndex(id: "old", slot: "morning", localDate: "2026-09-26", scheduledAt: nil,
                                     madeAt: "2026-09-26T07:00:00Z", catchUp: nil, durationSeconds: nil, lead: "Old.")
        let snapshot = MobileTodaySnapshot(generatedAt: "2026-10-27T08:00:00Z", latestBrief: nil,
                                           briefs: [earlier, kept, tooOld], needsYou: [], needsYouCount: 0,
                                           day: TodayDay(next: nil, free: [], calendarConnected: false), topics: [],
                                           briefRequest: nil, schedule: MobileBriefSchedule(morning: "07:00", evening: "18:00"))
        let latest = MobileBriefV1(briefId: "latest", slot: "morning", localDate: "2026-10-27",
                                   asOf: "2026-10-27T11:00:00Z", narrativeStatus: "ok",
                                   narrative: [BriefParagraph(text: "Latest.")])
        let relay = MobileBriefV1(briefId: "relay", slot: "morning", localDate: "2026-10-27",
                                  asOf: "2026-10-27T09:00:00Z", narrativeStatus: "ok",
                                  narrative: [BriefParagraph(text: "First sentence. More detail follows.")])
        let withdrawn = MobileBriefV1(briefId: "withdrawn", slot: "morning", localDate: "2026-10-27",
                                      asOf: "2026-10-27T10:00:00Z", narrativeStatus: "unavailable",
                                      narrative: [BriefParagraph(text: "Do not show.")])
        let rows = PastBriefIndex.entries(snapshot: snapshot, cached: [latest, relay, withdrawn], latestID: "latest", now: now)
        XCTAssertEqual(rows.map(\.id), ["relay", "earlier", "kept"])
        XCTAssertEqual(rows.map(\.lead), ["First sentence.", "The plan shipped.", "Kept."])
    }
}
