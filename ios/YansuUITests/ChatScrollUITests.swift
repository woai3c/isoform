import XCTest

// The chat over a seeded cached transcript (AppEnvironment.settingsUITestEnvironment and ChatUITestSeed).
// The seeded pairing points at a relay that does not exist, so no hub is contacted and a new message waits for the computer.
// Proactive presentation states supply canned action responses through the authentication policy and write journal.
final class ChatScrollUITests: XCTestCase {
    private let lastMessage = "Can you draft the reply to Sam about the Q3 deck?"
    private let lastReply = "Drafted. It thanks Sam for the notes and asks for the final numbers by Thursday."
    private let firstMessage = "What is on my calendar this morning?"

    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    func testTheChatOpensAtTheNewestMessageAndLeavesAReaderWhoScrolledUpWhereTheyAre() throws {
        let app = launch(state: "chat")
        let newest = message(lastReply, in: app)
        XCTAssertTrue(newest.waitForExistence(timeout: 15))
        // A busy Simulator can take a while to lay out the first screen.
        XCTAssertTrue(waitUntilInView(newest, in: app, timeout: 20), "the chat opens at its newest message")
        XCTAssertFalse(isInView(message(firstMessage, in: app), in: app), "the oldest rows start above the screen")
        let status = app.staticTexts.matching(identifier: "chat.message.status")
            .matching(NSPredicate(format: "label == %@", "Seen by your computer")).firstMatch
        XCTAssertTrue(status.exists)
        XCTAssertEqual(status.label, "Seen by your computer")
        XCTAssertTrue(isInView(status, in: app))
        XCTAssertFalse(app.buttons["chat.latest"].exists)
        attach(app, named: "chat-opens-at-newest-seen")

        let transcript = app.scrollViews.firstMatch
        transcript.swipeDown()
        transcript.swipeDown()
        XCTAssertFalse(waitUntilInView(newest, in: app, timeout: 2), "the chat must not pull the reader back down")
        let latest = app.buttons["chat.latest"].firstMatch
        XCTAssertTrue(latest.waitForExistence(timeout: 5), "a reader further up gets the Latest control")
        attach(app, named: "chat-scrolled-up-latest")

        latest.tap()
        XCTAssertTrue(waitUntilInView(newest, in: app), "Latest returns to the newest message")
        XCTAssertTrue(waitUntilGone(latest), "the control leaves once the chat is at the bottom")

        // Leaving the tab and coming back starts at the newest message again.
        transcript.swipeDown()
        transcript.swipeDown()
        XCTAssertFalse(waitUntilInView(newest, in: app, timeout: 2))
        app.buttons["tab.today"].tap()
        app.buttons["tab.chat"].tap()
        XCTAssertTrue(waitUntilInView(newest, in: app), "returning to the chat shows the newest message")
        attach(app, named: "chat-returned-at-newest")
    }

    func testAMessageSentWhileTheComputerIsUnreachableExplainsAutomaticDelivery() throws {
        let app = launch(state: "chat")
        XCTAssertTrue(waitUntilInView(message(lastReply, in: app), in: app))
        let composer = app.descendants(matching: .any)["chat.composer"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 10))
        composer.tap()
        composer.typeText("Send it when it looks right.")
        app.buttons["chat.send"].firstMatch.tap()

        let sent = message("Send it when it looks right.", in: app)
        XCTAssertTrue(sent.waitForExistence(timeout: 10), "the message shows at once")
        XCTAssertTrue(waitUntilInView(sent, in: app), "sending keeps the chat at the bottom")
        // In Chat a waiting message has the short label; the thread line explains (phone-connection v1.1.0 section 2).
        let waiting = app.staticTexts.matching(identifier: "chat.message.status")
            .matching(NSPredicate(format: "label == %@", "Waits for your computer")).firstMatch
        XCTAssertTrue(waiting.waitForExistence(timeout: 10))
        XCTAssertTrue(waitUntilInView(waiting, in: app), "the status sits under the new message, in view")
        XCTAssertTrue(app.descendants(matching: .any)["chat.away.line"].firstMatch.waitForExistence(timeout: 10),
                      "the thread line says why it waits")
        attach(app, named: "chat-message-sending")

        // The relay cannot be reached, so the message is still on its way after the request gives up.
        let note = app.staticTexts["notice.message"].firstMatch
        XCTAssertTrue(note.waitForExistence(timeout: 60), "the offline note explains why")
        XCTAssertTrue(waiting.exists, "the message continues waiting without asking the user to resend")
        let composerText = (app.descendants(matching: .any)["chat.composer"].firstMatch.value as? String) ?? ""
        XCTAssertFalse(composerText.contains("Send it when it looks right."), "the message left the composer for good")
        attach(app, named: "chat-message-sending-offline")
    }

    func testAMessageTheMacStoredShowsSent() throws {
        let app = launch(state: "chat-sent")
        let sent = message(lastMessage, in: app)
        XCTAssertTrue(sent.waitForExistence(timeout: 15))
        XCTAssertTrue(waitUntilInView(sent, in: app))
        let statuses = app.staticTexts.matching(identifier: "chat.message.status")
        let status = statuses.element(boundBy: statuses.count - 1)
        XCTAssertTrue(status.waitForExistence(timeout: 5))
        XCTAssertEqual(status.label, "Sent to your computer")
        XCTAssertTrue(waitUntilInView(status, in: app), "the status under the newest message is in view")
        attach(app, named: "chat-message-sent")
    }

    /// The floating tab bar covers the bottom of the tab page, so the composer must stand above it, and above
    /// the keyboard while typing. A composer under the bar leaves its field and Send unreachable.
    func testTheComposerStandsAboveTheTabBarAndTheKeyboard() throws {
        let app = launch(state: "chat")
        let composer = app.descendants(matching: .any)["chat.composer"].firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 15))
        let send = app.buttons["chat.send"].firstMatch
        let chatTab = app.buttons["tab.chat"].firstMatch
        XCTAssertTrue(chatTab.waitForExistence(timeout: 5))
        XCTAssertLessThanOrEqual(composer.frame.maxY, chatTab.frame.minY, "the field sits above the tab bar")
        XCTAssertLessThanOrEqual(send.frame.maxY, chatTab.frame.minY, "Send sits above the tab bar")
        XCTAssertTrue(composer.isHittable, "the field can be reached")
        attach(app, named: "chat-composer-above-tab-bar")

        composer.tap()
        XCTAssertTrue(waitUntilGone(chatTab), "the tab bar steps aside while the field has focus")
        XCTAssertTrue(composer.isHittable, "the field stays reachable while typing")
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 5), "typing opens the keyboard")
        XCTAssertLessThanOrEqual(composer.frame.maxY, keyboard.frame.minY, "the field stays above the keyboard")
        attach(app, named: "chat-composer-with-keyboard")
    }

    func testProactiveCardsAndExpandableStatusRowsInLightAndDark() throws {
        for appearance in ["light", "dark"] {
            let cardApp = launch(state: "chat-proactive", appearance: appearance)
            let card = cardApp.descendants(matching: .any)["chat.proactive.card"].firstMatch
            XCTAssertTrue(card.waitForExistence(timeout: 15))
            XCTAssertTrue(waitUntilInView(card, in: cardApp), "the whole proactive card fits above the composer")
            XCTAssertTrue(message("A quick review of the launch plan", in: cardApp).exists)
            XCTAssertTrue(message("Waiting on you", in: cardApp).exists)
            let actions = cardApp.buttons.matching(identifier: "chat.proactive.action")
            XCTAssertEqual(actions.count, 2)
            XCTAssertTrue(proactiveAction("Do once", in: cardApp).isHittable)
            XCTAssertTrue(proactiveAction("Not now", in: cardApp).isHittable)
            try attachPresentation(cardApp, named: "chat-proactive-card-\(appearance)")
            cardApp.terminate()

            let statusApp = launch(state: "chat-status", appearance: appearance)
            let status = statusApp.buttons["chat.status"].firstMatch
            XCTAssertTrue(status.waitForExistence(timeout: 15))
            XCTAssertTrue(waitUntilInView(status, in: statusApp))
            XCTAssertEqual(status.value as? String, "Collapsed")
            XCTAssertFalse(statusApp.descendants(matching: .any)["chat.proactive.card"].firstMatch.exists)
            try attachPresentation(statusApp, named: "chat-status-row-\(appearance)")

            status.tap()
            XCTAssertEqual(status.value as? String, "Expanded")
            XCTAssertTrue(message("Your morning brief is ready.", in: statusApp).exists)
            XCTAssertTrue(message("Calendar capture is paused.", in: statusApp).exists)
            try attachPresentation(statusApp, named: "chat-status-row-expanded-\(appearance)")
            statusApp.terminate()
        }
    }

    func testProactiveAndStatusChromeUsesSimplifiedChinese() throws {
        let cardApp = launch(state: "chat-proactive", ownerCheck: "allow", locale: "zh_CN")
        let state = cardApp.staticTexts["chat.proactive.state"].firstMatch
        XCTAssertTrue(state.waitForExistence(timeout: 15))
        XCTAssertEqual(state.label, "状态：等待你回应")
        proactiveAction("Not now", in: cardApp).tap()
        let result = cardApp.staticTexts["chat.proactive.result"].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 10))
        XCTAssertEqual(result.label, "已留待稍后处理")
        cardApp.terminate()

        let statusApp = launch(state: "chat-status", locale: "zh_CN")
        let status = statusApp.buttons["chat.status"].firstMatch
        XCTAssertTrue(status.waitForExistence(timeout: 15))
        XCTAssertEqual(status.label, "来自 Isoform 的 2 条更新")
        XCTAssertEqual(status.value as? String, "已收起")
        status.tap()
        XCTAssertEqual(status.value as? String, "已展开")
        statusApp.terminate()
    }

    func testASeededProactiveActionShowsItsResultInline() throws {
        let app = launch(state: "chat-proactive", ownerCheck: "allow")
        let action = proactiveAction("Not now", in: app)
        XCTAssertTrue(action.waitForExistence(timeout: 15))
        action.tap()

        let result = app.staticTexts["chat.proactive.result"].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 10))
        XCTAssertEqual(result.label, "Saved for later")
        XCTAssertTrue(app.descendants(matching: .any)["chat.proactive.card"].firstMatch.exists)
        XCTAssertFalse(action.exists && action.isEnabled, "an accepted action cannot be submitted again")
        attach(app, named: "chat-proactive-action-result")
    }

    func testChatCardsOpenTheItemSheetAndStayInChat() throws {
        let app = launch(state: "chat-cards", appearance: "light")
        let prep = app.buttons["chat.proactive.button.open_prep"].firstMatch
        XCTAssertTrue(prep.waitForExistence(timeout: 15))
        prep.tap()

        let item = app.descendants(matching: .any)["chat.proactive.item"].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["tab.chat"].isSelected)
        try attachPresentation(app, named: "chat-card-item-sheet-light")
        app.buttons["chat.proactive.close"].tap()
        XCTAssertTrue(waitUntilGone(item))

        // The card's buttons are built only while the card is near the screen. The composer now stands above the
        // tab bar, so this card can sit just above the visible area; scroll back to it first.
        let notNow = app.buttons["chat.proactive.button.not_now"].firstMatch
        for _ in 0..<4 where !notNow.exists {
            app.scrollViews.firstMatch.swipeDown()
        }
        XCTAssertTrue(notNow.waitForExistence(timeout: 10))
        notNow.tap()
        XCTAssertTrue(app.buttons["tab.chat"].isSelected)
        XCTAssertTrue(app.staticTexts["chat.proactive.result"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["Open brief"].exists)
        XCTAssertFalse(app.buttons["Dismiss"].exists)
    }

    func testChatCardItemSheetInDarkAppearance() throws {
        let app = launch(state: "chat-cards", appearance: "dark")
        let prep = app.buttons["chat.proactive.button.open_prep"].firstMatch
        XCTAssertTrue(prep.waitForExistence(timeout: 15))
        prep.tap()
        XCTAssertTrue(app.descendants(matching: .any)["chat.proactive.item"].waitForExistence(timeout: 10))
        try attachPresentation(app, named: "chat-card-item-sheet-dark")
    }

    func testARefusedSeededProactiveActionRestoresItsCard() throws {
        let app = launch(state: "chat-proactive", ownerCheck: "allow", proactiveResponse: "refuse")
        let action = proactiveAction("Not now", in: app)
        XCTAssertTrue(action.waitForExistence(timeout: 15))
        action.tap()

        let result = app.staticTexts["chat.proactive.result"].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 10))
        XCTAssertEqual(result.label, "This action is no longer available.")
        XCTAssertTrue(app.descendants(matching: .any)["chat.proactive.card"].firstMatch.exists)
        XCTAssertTrue(action.isEnabled, "a refused action leaves the card available")
        XCTAssertTrue(message("Waiting on you", in: app).exists)
        attach(app, named: "chat-proactive-action-refused")
    }

    func testSavingAProactiveSuggestionForLaterDoesNotAskForOwnerAuthentication() throws {
        let app = launch(state: "chat-proactive", ownerCheck: "decline")
        let action = proactiveAction("Not now", in: app)
        XCTAssertTrue(action.waitForExistence(timeout: 15))
        action.tap()

        let result = app.staticTexts["chat.proactive.result"].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 10))
        XCTAssertEqual(result.label, "Saved for later")
        XCTAssertFalse(action.exists && action.isEnabled, "the local choice is applied even with a declining owner guard")
        attach(app, named: "chat-proactive-local-choice")
    }

    func testDecliningOwnerCheckStillBlocksStartingProactiveWork() throws {
        let app = launch(state: "chat-proactive", ownerCheck: "decline")
        let action = proactiveAction("Do once", in: app)
        XCTAssertTrue(action.waitForExistence(timeout: 15))
        action.tap()

        let result = app.staticTexts["chat.proactive.result"].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 10))
        XCTAssertEqual(result.label,
                       "Nothing changed on your computer. Confirm with Face ID, Touch ID or your passcode to continue.")
        XCTAssertTrue(action.isEnabled)
        XCTAssertTrue(message("Waiting on you", in: app).exists)
        attach(app, named: "chat-proactive-execution-owner-check-declined")
    }

    func testDraftMustBeVisibleBeforeSendAndSendNeedsNoOwnerCheck() throws {
        let app = launch(state: "chat-draft", ownerCheck: "decline")
        let read = app.buttons["chat.proactive.readDraft"]
        XCTAssertTrue(read.waitForExistence(timeout: 15))
        let draft = app.descendants(matching: .any)["chat.proactive.draft"].firstMatch
        let send = proactiveAction("Send the reply", in: app)
        XCTAssertFalse(draft.exists)
        XCTAssertFalse(send.exists)
        XCTAssertFalse(message("Read the draft in Isoform on your computer.", in: app).exists)

        read.tap()
        XCTAssertTrue(draft.waitForExistence(timeout: 5))
        XCTAssertTrue(message("To sam@example.com", in: app).exists)
        XCTAssertTrue(message("Subject Q3 deck", in: app).exists)
        XCTAssertTrue(message("Thanks for the notes. Please send the final numbers by Thursday.", in: app).exists)
        XCTAssertTrue(send.waitForExistence(timeout: 5))
        send.tap()

        let result = app.staticTexts["chat.proactive.result"].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 10))
        XCTAssertEqual(result.label, "Done")
        XCTAssertTrue(draft.exists, "the shared result does not discard the draft words")
        let faceID = NSPredicate(format: "label CONTAINS[c] 'Face ID' OR label CONTAINS[c] 'Touch ID' OR label CONTAINS[c] 'passcode'")
        XCTAssertEqual(app.descendants(matching: .any).matching(faceID).count, 0)
        attach(app, named: "chat-draft-sent")
    }

    /// Bo's Oct 29 chip question: a message the computer refused says Not sent with Try again, never "Will send
    /// when your computer is back", and Try again sends the words from the box.
    func testARefusedMessageSaysNotSentAndTryAgainSendsIt() throws {
        let app = launch(state: "chat-not-sent")
        let refused = message("What should I prepare before the review?", in: app)
        XCTAssertTrue(refused.waitForExistence(timeout: 15))
        let status = app.descendants(matching: .any).matching(identifier: "chat.message.status")
            .matching(NSPredicate(format: "label == %@", "Not sent")).firstMatch
        XCTAssertTrue(status.waitForExistence(timeout: 10))
        XCTAssertFalse(app.descendants(matching: .any)
            .matching(NSPredicate(format: "label == %@", "Will send when your computer is back")).firstMatch.exists,
                       "a refusal is not a computer that is away")
        let retry = app.buttons["chat.message.retry"].firstMatch
        XCTAssertTrue(retry.waitForExistence(timeout: 5))
        XCTAssertEqual(retry.label, "Try again")
        XCTAssertTrue(waitUntilInView(retry, in: app))
        attach(app, named: "chat-message-not-sent")

        retry.tap()
        XCTAssertTrue(XCTWaiter.wait(for: [XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "exists == false"), object: status)], timeout: 15) == .completed,
                      "Not sent goes once the words are sent again")
        let composerText = (app.descendants(matching: .any)["chat.composer"].firstMatch.value as? String) ?? ""
        XCTAssertFalse(composerText.contains("What should I prepare"), "the words left the box")
        attach(app, named: "chat-message-not-sent-retried")
    }

    func testRefusedDraftSendKeepsTheFullDraftVisible() throws {
        let app = launch(state: "chat-draft", ownerCheck: "decline", proactiveResponse: "refuse")
        app.buttons["chat.proactive.readDraft"].tap()
        let draft = app.descendants(matching: .any)["chat.proactive.draft"].firstMatch
        let send = proactiveAction("Send the reply", in: app)
        XCTAssertTrue(send.waitForExistence(timeout: 15))
        send.tap()

        let result = app.staticTexts["chat.proactive.result"].firstMatch
        XCTAssertTrue(result.waitForExistence(timeout: 10))
        XCTAssertEqual(result.label, "Not sent. Try again.")
        XCTAssertTrue(draft.exists)
        XCTAssertTrue(message("Thanks for the notes. Please send the final numbers by Thursday.", in: app).exists)
        XCTAssertTrue(send.isEnabled)
        attach(app, named: "chat-draft-send-refused")
    }

    func testDraftChromeUsesSimplifiedChinese() throws {
        let app = launch(state: "chat-draft", ownerCheck: "decline", locale: "zh_CN")
        let read = app.buttons["chat.proactive.readDraft"]
        XCTAssertTrue(read.waitForExistence(timeout: 15))
        XCTAssertEqual(read.label, "查看草稿")
        read.tap()
        XCTAssertEqual(app.buttons["chat.proactive.readDraft"].label, "收起草稿")
        XCTAssertTrue(message("收件人：sam@example.com", in: app).exists)
        XCTAssertTrue(message("主题：Q3 deck", in: app).exists)
        let send = proactiveAction("发送回复", in: app)
        XCTAssertTrue(send.waitForExistence(timeout: 5))
    }

    private func launch(state: String, appearance: String? = nil, ownerCheck: String? = nil,
                        proactiveResponse: String? = nil, locale: String? = nil) -> XCUIApplication {
        let app = XCUIApplication()
        if let locale {
            app.launchArguments += ["-AppleLanguages", "(zh-Hans)", "-AppleLocale", locale]
        }
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = state
        app.launchEnvironment["YANSU_UITEST_TAB"] = "chat"
        app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
        app.launchEnvironment["YANSU_UITEST_OWNER_CHECK"] = ownerCheck
        app.launchEnvironment["YANSU_UITEST_PROACTIVE_ACTION"] = proactiveResponse
        app.launch()
        // Tab titles follow the phone's language (Chinese when a locale is set).
        XCTAssertTrue(app.buttons["tab.chat"].waitForExistence(timeout: 15))
        return app
    }

    private func proactiveAction(_ label: String, in app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(identifier: "chat.proactive.action")
            .matching(NSPredicate(format: "label BEGINSWITH %@", label)).firstMatch
    }

    private func message(_ text: String, in app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    /// Waits until the element is wholly on screen between the navigation bar and the composer; hittable alone
    /// also passes for a row half under the composer.
    private func waitUntilInView(_ element: XCUIElement, in app: XCUIApplication, timeout: TimeInterval = 10) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            if isInView(element, in: app) { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        } while Date() < deadline
        return false
    }

    private func isInView(_ element: XCUIElement, in app: XCUIApplication) -> Bool {
        guard element.exists else { return false }
        let composer = app.descendants(matching: .any)["chat.composer"].firstMatch
        let bar = app.navigationBars.firstMatch
        guard composer.exists, bar.exists else { return false }
        let frame = element.frame
        return frame.minY >= bar.frame.maxY - 1 && frame.maxY <= composer.frame.minY - 1
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

    /// Attachments always remain in the result bundle. A runner with access to the repository may also set
    /// TEST_RUNNER_YANSU_UITEST_SCREENSHOT_DIRECTORY to the absolute ios/.build/screenshots path.
    private func attachPresentation(_ app: XCUIApplication, named name: String) throws {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let path = ProcessInfo.processInfo.environment["YANSU_UITEST_SCREENSHOT_DIRECTORY"],
              !path.isEmpty else { return }
        let directory = URL(fileURLWithPath: path, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try screenshot.pngRepresentation.write(to: directory.appendingPathComponent("\(name).png"), options: .atomic)
    }
}
