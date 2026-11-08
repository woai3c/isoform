import XCTest

// The Chat tab is the chat, always (ChatTabView): no list of chats and no new chat. The seeded states
// (AppEnvironment.settingsUITestEnvironment and ChatUITestSeed) never contact a real hub: "chat" has a cached main
// chat and a Mac that cannot be reached, "chat-live" a Mac answered in-process, and "chat-live-cold" nothing cached
// yet, so the chat is found in the Mac's list.
final class MainChatUITests: XCTestCase {
    private let mainReply = "Drafted. It thanks Sam for the notes and asks for the final numbers by Thursday."
    private let unsentDraft = "Also split the travel receipts"

    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    /// A cached chat, with a Mac that cannot be reached.
    func testTheChatTabIsTheCachedMainChat() throws {
        try checkTheChatTabIsTheMainChat(state: "chat")
    }

    func testChatBoardInLightAndDarkAppearance() throws {
        for appearance in ["Light", "Dark"] {
            let app = launch(state: "chat", appearance: appearance)
            XCTAssertTrue(composer(app).waitForExistence(timeout: 15))
            XCTAssertTrue(message(mainReply, in: app).waitForExistence(timeout: 10))
            attach(app, named: "chat-\(appearance.lowercased())")
        }
    }

    /// A cached chat, with a Mac that answers the list, the transcript and the event poll.
    func testTheChatTabIsTheMainChatWhileTheMacAnswers() throws {
        try checkTheChatTabIsTheMainChat(state: "chat-live")
    }

    /// Nothing cached yet: the chat is found in the Mac's list, then shown.
    func testTheChatTabIsTheMainChatTheMacListsOnAFirstLaunch() throws {
        try checkTheChatTabIsTheMainChat(state: "chat-live-cold")
    }

    func testUnsentTextStaysInTheMainChatComposer() throws {
        let app = launch(state: "chat-live")
        let field = composer(app)
        XCTAssertTrue(field.waitForExistence(timeout: 15))
        XCTAssertTrue(message(mainReply, in: app).waitForExistence(timeout: 10))
        field.tap()
        field.typeText(unsentDraft)

        XCUIDevice.shared.press(.home)
        sleep(2)
        app.activate()
        XCTAssertTrue(composer(app).waitForExistence(timeout: 10))
        XCTAssertTrue(composerText(app).contains(unsentDraft), "the unsent text is still in the composer")
        attach(app, named: "unsent-text-kept")
    }

    private func checkTheChatTabIsTheMainChat(state: String) throws {
        let app = launch(state: state)
        XCTAssertTrue(composer(app).waitForExistence(timeout: 15), "the Chat tab opens on the chat")
        XCTAssertTrue(message(mainReply, in: app).waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["root.settings"].exists, "the chat is the tab root itself, with no list behind")
        XCTAssertFalse(app.buttons["chat.list.main"].exists, "there is no list of chats")
        XCTAssertFalse(app.buttons["root.compose"].exists, "the Chat tab starts no new chats")
        attach(app, named: "\(state)-main-chat")

        app.buttons["tab.today"].tap()
        XCTAssertTrue(waitUntilGone(composer(app)))
        app.buttons["tab.chat"].tap()
        XCTAssertTrue(message(mainReply, in: app).waitForExistence(timeout: 10), "back on Chat, the chat shows")

        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(message(mainReply, in: app).waitForExistence(timeout: 10), "back in the app, the chat shows")
        attach(app, named: "\(state)-main-chat-after-switches")
    }

    private func launch(state: String, appearance: String? = nil) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = state
        if let appearance {
            app.launchArguments += ["-AppleInterfaceStyle", appearance]
            app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance.lowercased()
        }
        app.launch()
        XCTAssertTrue(app.buttons["tab.chat"].waitForExistence(timeout: 15))
        // The app opens on Today, the first tab, since the three-tab companion (e54502db7). These tests are about
        // the Chat tab, so they pick it the way a person does.
        app.buttons["tab.chat"].tap()
        return app
    }

    private func composer(_ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any)["chat.composer"].firstMatch
    }

    private func composerText(_ app: XCUIApplication) -> String {
        (composer(app).value as? String) ?? ""
    }

    private func message(_ text: String, in app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    private func waitUntilGone(_ element: XCUIElement, timeout: TimeInterval = 5) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            if !element.exists { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        } while Date() < deadline
        return false
    }

    private func attach(_ app: XCUIApplication, named name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
