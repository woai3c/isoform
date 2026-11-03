import XCTest

/// Pairs with a live hub, opens the Chat tab (the chat), sends a message and waits for the
/// assistant's reply. Skipped unless a live invitation is supplied (README, "Pairing links and the live pairing
/// test"). It needs the hub's chat routes (`GET /chats`, `GET /chats/{id}`, `POST /chats/{id}/messages`) and the
/// `chat_turn` events; until those exist on the Mac this test cannot pass against a live hub.
final class ChatFlowUITests: XCTestCase {
    private let stepTimeout: TimeInterval = 30
    /// A live model can take a while to answer, so the reply has a longer budget than the screens do.
    private let replyTimeout: TimeInterval = 180
    private let prompt = "Reply with one short sentence confirming you can read this."

    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
        // Permission answers still ask for the owner check; ordinary corrections and saving work do not.
        SimulatorBiometrics.setEnrolled(true)
    }

    override func tearDownWithError() throws {
        SimulatorBiometrics.setEnrolled(false)
        try super.tearDownWithError()
    }

    func testSendsAMessageAndSeesTheAssistantReply() throws {
        guard let invitation = liveInvitation() else {
            throw XCTSkip("set TEST_RUNNER_YANSU_UITEST_INVITATION (or _FILE) to a live Isoform or legacy pairing link")
        }
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_INVITATION"] = invitation
        app.launch()
        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: stepTimeout))
        unpairIfNeeded(app)
        pairWithInvitation(app, invitation: invitation)

        // The Chat tab is the chat itself.
        openChatTab(app)

        let composer = app.descendants(matching: .any)["chat.composer"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: stepTimeout), "the chat has no composer")
        composer.tap()
        composer.typeText(prompt)
        attachScreenshot(app, named: "chat-composer-filled")

        let send = app.buttons["chat.send"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: stepTimeout), "the composer has no Send button")
        XCTAssertTrue(send.isEnabled, "Send stays disabled with a message in the composer")

        // The chat may already hold replies from earlier runs, so the check counts replies from this send on.
        let replies = app.descendants(matching: .any).matching(identifier: "chat.message.assistant")
        let repliesBefore = replies.count
        send.tap()

        let userMessage = app.descendants(matching: .any).matching(identifier: "chat.message.user")
            .containing(NSPredicate(format: "label CONTAINS %@", prompt)).firstMatch
        XCTAssertTrue(userMessage.waitForExistence(timeout: stepTimeout), "the sent message did not appear")
        attachScreenshot(app, named: "chat-sent")

        guard waitForReply(app, after: repliesBefore) else {
            attachScreenshot(app, named: "chat-no-reply")
            XCTFail("no assistant reply arrived within \(Int(replyTimeout)) seconds")
            return
        }
        attachScreenshot(app, named: "chat-reply")
    }

    private func waitForReply(_ app: XCUIApplication, after count: Int) -> Bool {
        let replies = app.descendants(matching: .any).matching(identifier: "chat.message.assistant")
        let deadline = Date().addingTimeInterval(replyTimeout)
        while Date() < deadline {
            if replies.count > count { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(1))
        }
        return replies.count > count
    }

    private func openChatTab(_ app: XCUIApplication) {
        let tab = app.buttons["tab.chat"].firstMatch
        XCTAssertTrue(tab.waitForExistence(timeout: stepTimeout), "the Chat tab is missing")
        tab.tap()
    }
}
