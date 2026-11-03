import XCTest
@testable import Yansu

// "Your brief is ready" (handoff 2026-10-28, section 6): it posts once the text and audio are on the phone, or two
// minutes after the text when the audio is late, and its buttons read or play the brief.
final class LocalBriefNoticeTimingTests: XCTestCase {
    private let pairing = TodayPairingIdentity(hubID: "hub", deviceID: "phone")

    private func defaults() throws -> UserDefaults {
        let suite = "LocalBriefNoticeTimingTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        addTeardownBlock { defaults.removePersistentDomain(forName: suite) }
        return defaults
    }

    func testNoticeBodyIsTheLeadSentenceWithTheGreetingJoinedToIt() {
        // Handoff 2026-10-27 (phone notifications): "with the brief's lead sentence as its body", never the greeting alone.
        let brief = MobileBriefV1(briefId: "b", slot: "morning", localDate: "2026-10-28", asOf: "2026-10-28T06:50:00Z",
                                  kind: "morning", narrativeStatus: "ok",
                                  narrative: [BriefParagraph(text: "Good morning. Three meetings today, and Priya is waiting on you. The launch review is at 3 PM.")])
        XCTAssertEqual(LocalBriefNotices.body(brief), "Good morning. Three meetings today, and Priya is waiting on you.")

        let chinese = MobileBriefV1(briefId: "zh", narrativeStatus: "ok",
                                    narrative: [BriefParagraph(text: "早上好。今天有三个会议。下午三点评审。")])
        XCTAssertEqual(LocalBriefNotices.body(chinese), "早上好。今天有三个会议。")

        let plain = MobileBriefV1(briefId: "p", narrativeStatus: "ok",
                                  narrative: [BriefParagraph(text: "Three meetings today. More detail follows.")])
        XCTAssertEqual(LocalBriefNotices.body(plain), "Three meetings today.")

        let greetingOnly = MobileBriefV1(briefId: "g", narrativeStatus: "ok", narrative: [BriefParagraph(text: "Good evening, Bo.")])
        XCTAssertEqual(LocalBriefNotices.body(greetingOnly), "Good evening, Bo.")
    }

    func testNoticeIsDueAsSoonAsTheAudioIsOnThePhone() throws {
        let now = try XCTUnwrap(RelayTime.parse("2026-10-28T07:00:00Z"))
        let text = now.addingTimeInterval(-10)
        XCTAssertEqual(LocalBriefNotices.readyDate(textArrivedAt: text, audioOnPhone: true, now: now), now)
    }

    func testNoticeWaitsTwoMinutesAfterTheTextWhenTheAudioIsLate() throws {
        let now = try XCTUnwrap(RelayTime.parse("2026-10-28T07:00:00Z"))
        let text = now.addingTimeInterval(-30)
        XCTAssertEqual(LocalBriefNotices.readyDate(textArrivedAt: text, audioOnPhone: false, now: now),
                       now.addingTimeInterval(90), "30 seconds of the two minutes have passed")
    }

    func testNoticeDoesNotWaitLongerThanTwoMinutesOnceThoseHavePassed() throws {
        let now = try XCTUnwrap(RelayTime.parse("2026-10-28T07:00:00Z"))
        let text = now.addingTimeInterval(-300)
        XCTAssertEqual(LocalBriefNotices.readyDate(textArrivedAt: text, audioOnPhone: false, now: now), now)
        XCTAssertEqual(LocalBriefNotices.audioWait, 120)
    }

    func testTextArrivalIsRecordedOnceSoTheWaitDoesNotRestart() throws {
        let defaults = try defaults()
        let first = try XCTUnwrap(RelayTime.parse("2026-10-28T07:00:00Z"))
        let later = first.addingTimeInterval(45)
        XCTAssertEqual(LocalBriefNotices.textArrival("brief-1", pairing: pairing, defaults: defaults, now: first), first)
        XCTAssertEqual(LocalBriefNotices.textArrival("brief-1", pairing: pairing, defaults: defaults, now: later), first)
        XCTAssertEqual(LocalBriefNotices.textArrival("brief-2", pairing: pairing, defaults: defaults, now: later), later,
                       "each brief has its own arrival")
    }

    func testPendingNoticeFollowsTheMacsCurrentBrief() throws {
        let defaults = try defaults()
        var snapshot = MobileTodaySnapshot.fixture(currentID: "morning", latestID: "morning")
        var previous = snapshot
        previous.currentBrief = TodayBriefRef(id: "evening", version: String(repeating: "a", count: 64))
        LocalBriefNotices.observe(snapshot, previous: previous, pairing: pairing, defaults: defaults)
        XCTAssertTrue(LocalBriefNotices.isPending("morning", pairing: pairing, defaults: defaults))
        XCTAssertFalse(LocalBriefNotices.isPending("evening", pairing: pairing, defaults: defaults))
        snapshot.currentBrief = nil
        LocalBriefNotices.observe(snapshot, previous: previous, pairing: pairing, defaults: defaults)
        XCTAssertTrue(LocalBriefNotices.isPending("morning", pairing: pairing, defaults: defaults),
                      "a Mac before 1.0 names its newest listed brief, which is the same one here")
    }

    func testBriefReadyCategoryHasReadAndPlayButtons() {
        let category = NotificationCategories.all.first { $0.identifier == NotificationCategories.localBriefCategory }
        XCTAssertEqual(category?.actions.map(\.identifier), [NotificationCategories.Action.read, NotificationCategories.Action.play])
    }
}

private extension MobileTodaySnapshot {
    static func fixture(currentID: String, latestID: String) -> MobileTodaySnapshot {
        MobileTodaySnapshot(generatedAt: "2026-10-28T07:00:00Z",
                            latestBrief: TodayBriefIndex(id: latestID, slot: "morning", localDate: "2026-10-28",
                                                         scheduledAt: nil, madeAt: "2026-10-28T06:50:00Z",
                                                         catchUp: nil, durationSeconds: nil, lead: "Good morning."),
                            briefs: [], needsYou: [], needsYouCount: 0,
                            day: TodayDay(next: nil, free: [], calendarConnected: false), topics: [],
                            briefRequest: nil, schedule: MobileBriefSchedule(morning: "06:50", evening: "18:00"),
                            currentBrief: TodayBriefRef(id: currentID, version: String(repeating: "b", count: 64)))
    }
}
