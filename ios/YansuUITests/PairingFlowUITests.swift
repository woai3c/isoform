import XCTest

// Drives the real pairing flow against a live hub and relay. Run with
//   TEST_RUNNER_YANSU_UITEST_INVITATION="isoform://pair#..." xcodebuild test ... -only-testing:YansuUITests/PairingFlowUITests
// where the invitation comes from the desktop (Settings > iPhone) or from the
// Go harness in wails-gui (`YANSU_MOBILE_E2E=1 YANSU_MOBILE_E2E_HOLD=20m go test -run TestMobileEndToEndThroughRelayBinary`),
// which approves the pairing and sends a sample brief. Without an invitation
// the test is skipped.
final class PairingFlowUITests: XCTestCase {
    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    func testPairsWithAPastedInvitationAndShowsTheFeed() throws {
        var invitation = ProcessInfo.processInfo.environment["YANSU_UITEST_INVITATION"] ?? ""
        if let path = ProcessInfo.processInfo.environment["YANSU_UITEST_INVITATION_FILE"],
           let contents = try? String(contentsOfFile: path, encoding: .utf8) {
            invitation = contents.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        guard invitation.hasPrefix("isoform://pair#") || invitation.hasPrefix("yansu://pair#") else {
            throw XCTSkip("set TEST_RUNNER_YANSU_UITEST_INVITATION (or _FILE) to a live Isoform or legacy pairing link")
        }
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_INVITATION"] = invitation
        app.launch()
        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 20))
        unpairIfNeeded(app)

        let pairButton = app.buttons["Pair with your computer"].firstMatch
        XCTAssertTrue(pairButton.waitForExistence(timeout: 10))
        pairButton.tap()
        attach(app, named: "pairing-sheet")

        let field = app.descendants(matching: .any)["pairing.pasteField"].firstMatch
        if !field.waitForExistence(timeout: 15) {
            attach(app, named: "pairing-sheet-missing-field")
            XCTFail("the paste field never appeared")
        }
        // Debug builds prefill the field from the launch environment; type as a fallback.
        let pasteButton = app.buttons["pairing.pasteButton"].firstMatch
        XCTAssertTrue(pasteButton.waitForExistence(timeout: 5))
        if !pasteButton.isEnabled {
            field.tap()
            field.typeText(invitation)
        }
        XCTAssertTrue(pasteButton.isEnabled, "the link did not reach the field")
        pasteButton.tap()

        // The Mac may approve within a second, so the waiting screen is optional.
        let waiting = app.staticTexts["Compare this code with your computer, then approve there."]
        let paired = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'This phone is paired with'")).firstMatch
        if waiting.waitForExistence(timeout: 3) {
            attach(app, named: "pairing-waiting-for-approval")
        }
        XCTAssertTrue(paired.waitForExistence(timeout: 90), "the Mac's approval never arrived")
        attach(app, named: "pairing-approved")
        app.buttons["pairing.done"].firstMatch.tap()

        // Done closes Settings and opens the Feed, which reads the brief the hub sent after approval; a foreground
        // cycle and a pull to refresh are the fallbacks. The harness brief has cards, or at least its day under Earlier.
        XCTAssertTrue(app.buttons["tab.today"].isSelected, "pairing did not end on the Feed")
        let card = app.descendants(matching: .any).matching(identifier: "feed.card").firstMatch
        let briefRow = app.buttons.matching(NSPredicate(format: "identifier IN %@", ["feed.todayBrief", "feed.earlier"])).firstMatch
        func briefArrived() -> Bool { card.exists || briefRow.exists }
        if !card.waitForExistence(timeout: 20) && !briefRow.exists {
            XCUIDevice.shared.press(.home)
            app.activate()
            _ = card.waitForExistence(timeout: 20)
        }
        var attempts = 0
        while !briefArrived() && attempts < 3 {
            let start = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.25))
            let end = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85))
            start.press(forDuration: 0.15, thenDragTo: end)
            _ = card.waitForExistence(timeout: 8)
            attempts += 1
        }
        XCTAssertTrue(briefArrived(), "the brief never reached the Feed")
        attach(app, named: "feed-top")
        app.swipeUp()
        attach(app, named: "feed-middle")
        app.swipeUp()
        attach(app, named: "feed-bottom")

        openSettings(app)
        XCTAssertTrue(app.buttons["settings.unpair"].firstMatch.waitForExistence(timeout: 10), "Settings does not show the paired state")
        attach(app, named: "settings-paired")
    }

    // A previous run leaves the Simulator paired; unpair through Settings first.
    private func attach(_ app: XCUIApplication, named name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
