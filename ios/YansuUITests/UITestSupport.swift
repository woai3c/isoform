import XCTest

/// Helpers shared by the live UI tests. They take the same steps a person takes: unpair the Simulator if it
/// is paired, then open Pair with your Mac and paste the link.
extension XCTestCase {
    /// The live invitation from the launch environment or the file the harness writes. Nil when none was
    /// supplied, so the live tests skip instead of failing.
    func liveInvitation() -> String? {
        var invitation = ProcessInfo.processInfo.environment["YANSU_UITEST_INVITATION"] ?? ""
        if let path = ProcessInfo.processInfo.environment["YANSU_UITEST_INVITATION_FILE"],
           let contents = try? String(contentsOfFile: path, encoding: .utf8) {
            invitation = contents.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        return invitation.hasPrefix("isoform://pair#") || invitation.hasPrefix("yansu://pair#") ? invitation : nil
    }

    func attachScreenshot(_ app: XCUIApplication, named name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    /// Opens the Settings sheet from the current tab.
    func openSettings(_ app: XCUIApplication) {
        let settings = app.buttons["root.settings"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 15), "the tab root has no profile button")
        tapUntil(settings, shows: app.buttons["settings.done"].firstMatch)
    }

    /// Opens Library from Today.
    func openLibrary(_ app: XCUIApplication) {
        let library = app.buttons["feed.library"].firstMatch
        XCTAssertTrue(library.waitForExistence(timeout: 10), "Today has no Library entry")
        library.tap()
    }

    /// Taps a control that presents a sheet and waits for the sheet. A tap that lands while the app is still
    /// settling after launch can be lost, so it is tried once more.
    func tapUntil(_ control: XCUIElement, shows result: XCUIElement, timeout: TimeInterval = 5) {
        for _ in 0..<2 {
            control.tap()
            if result.waitForExistence(timeout: timeout) { return }
        }
    }

    func closeSettings(_ app: XCUIApplication) {
        let done = app.buttons["settings.done"].firstMatch
        if done.waitForExistence(timeout: 5) {
            done.tap()
        }
    }

    /// Removes the pairing from Settings when the Simulator is paired, so the next step can pair again.
    func unpairIfNeeded(_ app: XCUIApplication) {
        app.buttons["tab.today"].tap()
        guard !app.buttons["Pair with your computer"].firstMatch.waitForExistence(timeout: 5) else { return }
        openSettings(app)
        let unpair = app.buttons["settings.unpair"].firstMatch
        if !unpair.waitForExistence(timeout: 5) {
            app.swipeUp()
        }
        guard unpair.waitForExistence(timeout: 5) else {
            attachScreenshot(app, named: "settings-no-unpair-control")
            XCTFail("the Simulator is paired but Settings shows no unpair control")
            return
        }
        unpair.tap()
        attachScreenshot(app, named: "settings-unpair-dialog")
        let confirm = app.buttons["Unpair"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "the unpair confirmation did not appear")
        confirm.tap()
        sleep(2)
        attachScreenshot(app, named: "settings-after-unpair")
        XCTAssertTrue(app.buttons["settings.pair"].firstMatch.waitForExistence(timeout: 30), "unpairing did not finish")
        closeSettings(app)
        app.buttons["tab.today"].tap()
    }

    /// Opens Pair with your Mac, pastes the live link, waits for the Mac's approval, and closes the sheet.
    func pairWithInvitation(_ app: XCUIApplication, invitation: String) {
        let pairButton = app.buttons["Pair with your computer"].firstMatch
        XCTAssertTrue(pairButton.waitForExistence(timeout: 10), "the Feed tab offers no pairing button")
        pairButton.tap()

        let field = app.descendants(matching: .any)["pairing.pasteField"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 15), "the paste field never appeared")
        let pasteButton = app.buttons["pairing.pasteButton"].firstMatch
        XCTAssertTrue(pasteButton.waitForExistence(timeout: 5), "the paste button never appeared")
        if !pasteButton.isEnabled {
            field.tap()
            field.typeText(invitation)
        }
        XCTAssertTrue(pasteButton.isEnabled, "the link did not reach the field")
        pasteButton.tap()

        let paired = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'This phone is paired with'")).firstMatch
        XCTAssertTrue(paired.waitForExistence(timeout: 90), "the Mac's approval never arrived")
        app.buttons["pairing.done"].firstMatch.tap()
    }
}
