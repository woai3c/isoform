import XCTest
@testable import Yansu

/// R75: the Mac's optional assistantSignOut field on the phone's Today snapshot.
final class AssistantSignOutTests: XCTestCase {
    private func snapshot() -> MobileTodaySnapshot {
        MobileTodaySnapshot(generatedAt: "2026-11-07T10:00:00Z", latestBrief: nil, briefs: [], needsYou: [],
                            needsYouCount: 0, day: TodayDay(next: nil, free: [], calendarConnected: false),
                            topics: [], briefRequest: nil,
                            schedule: MobileBriefSchedule(morning: "07:00", evening: "18:00"))
    }

    func testSignOutRoundTripsThroughTheSnapshot() throws {
        var value = snapshot()
        value.assistantSignOut = TodayAssistantSignOut(assistant: "claude-code")
        let data = try JSONEncoder().encode(value)
        let decoded = try JSONDecoder().decode(MobileTodaySnapshot.self, from: data)
        XCTAssertEqual(decoded.assistantSignOut?.assistant, "claude-code")
        XCTAssertEqual(decoded.assistantSignOut?.displayName, "Claude Code")
    }

    func testOlderSnapshotWithoutTheFieldDecodesWithNoSignOut() throws {
        let data = try JSONEncoder().encode(snapshot())
        let text = String(decoding: data, as: UTF8.self)
        XCTAssertFalse(text.contains("assistantSignOut"), "a snapshot with no sign-out must omit the field")
        let decoded = try JSONDecoder().decode(MobileTodaySnapshot.self, from: data)
        XCTAssertNil(decoded.assistantSignOut)
    }

    func testCodexNameAndNoButtonContract() {
        XCTAssertEqual(TodayAssistantSignOut(assistant: "codex").displayName, "Codex")
        XCTAssertEqual(TodayAssistantSignOut(assistant: "claude-code").displayName, "Claude Code")
    }
}
