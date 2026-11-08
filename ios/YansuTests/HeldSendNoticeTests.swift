import XCTest
@testable import Yansu

/// Today's one line for sends the computer did not take in a day (PROTOCOL.md section 23).
final class HeldSendNoticeTests: XCTestCase {
    private let base = Date(timeIntervalSince1970: 1_800_000_000)

    func testASingleNamedReplyInTasksNamesThePerson() {
        let notice = HeldSendNotice.make([.init(place: .task(taskID: "draft.a"), sentAt: base, person: "Sam")])
        XCTAssertEqual(notice?.text, "Your computer didn't wake, so your reply to Sam wasn't sent.")
        XCTAssertEqual(notice?.oldest, .task(taskID: "draft.a"))
    }

    /// The name keeps its spaces as non-breaking spaces, so a wrap never splits "Sam Lee" (v1.0.4).
    func testTheNameStaysOnOneLine() {
        let notice = HeldSendNotice.make([.init(place: .task(taskID: "draft.a"), sentAt: base, person: "Sam Lee")])
        XCTAssertEqual(notice?.text, "Your computer didn't wake, so your reply to Sam\u{00A0}Lee wasn't sent.")
    }

    func testASingleReplyWithNoNameSaysYourReply() {
        let notice = HeldSendNotice.make([.init(place: .task(taskID: "draft.a"), sentAt: base, person: nil)])
        XCTAssertEqual(notice?.text, "Your computer didn't wake, so your reply wasn't sent.")
        let blank = HeldSendNotice.make([.init(place: .task(taskID: "draft.a"), sentAt: base, person: "")])
        XCTAssertEqual(blank?.text, "Your computer didn't wake, so your reply wasn't sent.")
    }

    func testASingleChatMessageSaysYourMessage() {
        let notice = HeldSendNotice.make([.init(place: .chat(messageID: "m1"), sentAt: base, person: nil)])
        XCTAssertEqual(notice?.text, "Your computer didn't wake, so your message wasn't sent.")
    }

    func testSeveralAreCountedAcrossChatAndTasks() {
        let items: [HeldSendNotice.Item] = [
            .init(place: .chat(messageID: "m1"), sentAt: base, person: nil),
            .init(place: .task(taskID: "draft.a"), sentAt: base.addingTimeInterval(60), person: "Sam"),
            .init(place: .chat(messageID: "m2"), sentAt: base.addingTimeInterval(120), person: nil),
        ]
        XCTAssertEqual(HeldSendNotice.make(items)?.text, "Your computer didn't wake, so 3 messages weren't sent.")
    }

    func testShowOpensTheOldestWhereverItIs() {
        let items: [HeldSendNotice.Item] = [
            .init(place: .chat(messageID: "newer"), sentAt: base.addingTimeInterval(300), person: nil),
            .init(place: .task(taskID: "draft.oldest"), sentAt: base, person: "Sam"),
            .init(place: .chat(messageID: "middle"), sentAt: base.addingTimeInterval(120), person: nil),
        ]
        XCTAssertEqual(HeldSendNotice.make(items)?.oldest, .task(taskID: "draft.oldest"))
    }

    func testNothingHeldMakesNoLine() {
        XCTAssertNil(HeldSendNotice.make([]))
    }
}
