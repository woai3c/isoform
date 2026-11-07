import XCTest

/// P156 frames: the phone while the computer sleeps (PROTOCOL.md section 23). Each case launches a paired fixture
/// with the sleeping or held-send seed, waits on the element the state shows, and attaches a screenshot, light and dark.
/// YANSU_UITEST_SCREENSHOT_DIRECTORY also writes the PNGs to disk.
final class P156QueueUITests: XCTestCase {
    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    /// Today under the away line: "You can still ask and act. Isoform does it when your computer wakes."
    func testTodaySleepingLine() throws {
        for appearance in ["light", "dark"] {
            let app = launch(state: "feed-review", appearance: appearance, extra: [
                "YANSU_UITEST_CONNECTION_CAUSE": "sleeping", "YANSU_UITEST_KEEP_AWAKE": "off",
            ])
            XCTAssertTrue(app.descendants(matching: .any)["today.connection.canStillAct"].firstMatch
                .waitForExistence(timeout: 20), "the sleeping line is missing (\(appearance))")
            try capture(app, named: "today-sleeping-line-\(appearance)")
            app.terminate()
        }
    }

    /// Chat: a message waiting for the computer. A typed message shows "Waiting" with Undo (ChatAwayUITests);
    /// a message that carries a brief shows "Waits for your computer" (see testTalkAboutThisBriefWhileAsleep).
    func testWaitingChatMessage() throws {
        for appearance in ["light", "dark"] {
            let app = launch(state: "chat-away", appearance: appearance, extra: [
                "YANSU_UITEST_AWAY_CAUSE": "asleep", "YANSU_UITEST_AWAY_PHASE": "waiting", "YANSU_UITEST_TAB": "chat",
            ])
            let waiting = app.descendants(matching: .any).matching(identifier: "chat.message.waiting").firstMatch
            XCTAssertTrue(waiting.waitForExistence(timeout: 20), "no waiting message (\(appearance))")
            try capture(app, named: "chat-waiting-\(appearance)")
            app.terminate()
        }
    }

    /// The 24-hour line: a drafted reply the relay held a day, with Send again.
    func testTwentyFourHourLine() throws {
        for appearance in ["light", "dark"] {
            let app = launch(state: "feed-review", appearance: appearance, extra: [
                "YANSU_UITEST_CONNECTION_CAUSE": "sleeping", "YANSU_UITEST_KEEP_AWAKE": "off",
                "YANSU_UITEST_HELD_SEND": "expired",
            ])
            XCTAssertTrue(app.descendants(matching: .any)["tasks.expiredSend"].firstMatch.waitForExistence(timeout: 20),
                          "the 24-hour line is missing (\(appearance))")
            try capture(app, named: "day-line-expired-\(appearance)")
            app.terminate()
        }
    }

    /// The ask-again prompt: "Your computer is awake. Send the reply to Sam now?"
    func testAskAgainPrompt() throws {
        for appearance in ["light", "dark"] {
            let app = launch(state: "tasks-seeded", appearance: appearance, extra: ["YANSU_UITEST_HELD_SEND": "confirm"])
            XCTAssertTrue(app.alerts.firstMatch.waitForExistence(timeout: 20), "the prompt is missing (\(appearance))")
            try capture(app, named: "ask-again-prompt-\(appearance)")
            app.terminate()
        }
    }

    /// Needs you, under the Today sections: a proactive action queued while the computer sleeps says it waits.
    func testNeedsYouWaitsForComputer() throws {
        for appearance in ["light", "dark"] {
            let app = launch(state: "feed-review", appearance: appearance, extra: [
                "YANSU_UITEST_CONNECTION_CAUSE": "sleeping", "YANSU_UITEST_KEEP_AWAKE": "off", "YANSU_UITEST_TODAY_WAITS": "1",
            ])
            // The section label is uppercase on screen, so its accessibility label is uppercase too.
            let waits = app.staticTexts.matching(NSPredicate(format: "label ==[c] %@", "Waits for your computer")).firstMatch
            XCTAssertTrue(waits.waitForExistence(timeout: 20), "the Needs-you wait line is missing (\(appearance))")
            for _ in 0..<8 where !waits.isHittable { app.swipeUp() }
            try capture(app, named: "needs-you-waits-\(appearance)")
            app.terminate()
        }
    }

    /// Talk about this brief while the computer sleeps: the ask sheet opens with the brief attached. Its answer waits for
    /// the computer, and the same message in Chat says "Waits for your computer".
    func testTalkAboutThisBriefWhileAsleep() throws {
        for appearance in ["light", "dark"] {
            let app = launch(state: "feed-conversation", appearance: appearance, extra: [
                "YANSU_UITEST_TAB": "feed", "YANSU_UITEST_BRIEF_TALK": "offline", "YANSU_UITEST_ASK_OFFLINE": "1",
            ])
            XCTAssertTrue(app.buttons["feed.readAll"].waitForExistence(timeout: 20))
            app.buttons["feed.readAll"].tap()
            let talk = app.buttons["brief.talk"]
            for _ in 0..<12 where !talk.isHittable { app.swipeUp() }
            XCTAssertTrue(talk.waitForExistence(timeout: 10), "Talk about this brief is missing while asleep (\(appearance))")
            talk.tap()
            XCTAssertTrue(app.staticTexts["brief.ask.opener"].waitForExistence(timeout: 10),
                          "Talk did not open the ask sheet (\(appearance))")
            XCTAssertFalse(app.descendants(matching: .any)["brief.talk.line"].firstMatch.exists,
                           "Talk showed the unreachable line instead of opening (\(appearance))")
            try capture(app, named: "talk-about-brief-asleep-\(appearance)")
            app.buttons["brief.ask.choice.opener:yes"].firstMatch.tap()
            XCTAssertTrue(app.staticTexts["brief.ask.waiting"].firstMatch.waitForExistence(timeout: 10),
                          "the sheet does not say the question waits (\(appearance))")
            try capture(app, named: "ask-waits-for-computer-\(appearance)")
            // Open in Chat closes the sheet and opens the main chat with the brief attached.
            app.buttons["Open in Chat"].firstMatch.tap()
            let status = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "Waits for your computer")).firstMatch
            XCTAssertTrue(status.waitForExistence(timeout: 10), "the Chat message does not say it waits (\(appearance))")
            try capture(app, named: "chat-waits-for-computer-\(appearance)")
            app.terminate()
        }
    }

    private func launch(state: String, appearance: String, extra: [String: String]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = state
        app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
        for (key, value) in extra { app.launchEnvironment[key] = value }
        app.launch()
        return app
    }

    /// Attachments stay in the result bundle; YANSU_UITEST_SCREENSHOT_DIRECTORY also writes them to disk.
    private func capture(_ app: XCUIApplication, named name: String) throws {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let path = ProcessInfo.processInfo.environment["YANSU_UITEST_SCREENSHOT_DIRECTORY"], !path.isEmpty else { return }
        let directory = URL(fileURLWithPath: path, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try screenshot.pngRepresentation.write(to: directory.appendingPathComponent("\(name).png"), options: .atomic)
    }
}
