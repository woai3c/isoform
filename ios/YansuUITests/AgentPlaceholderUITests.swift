import XCTest

// P146 placeholder bars: gray bars and their headings show only while the copy is being read. A copy that cannot be
// read leaves both sections out; a copy that arrives later makes them appear. Frames go to walk-ios-6/P146b.
final class AgentPlaceholderUITests: XCTestCase {
    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    func testUnreadableCopyHasNoBarsNoHeadingsAndTheSectionsAppearLater() throws {
        for appearance in ["light", "dark"] {
            let unreadable = open(state: "placeholder", appearance: appearance)
            XCTAssertTrue(unreadable.staticTexts["Request your page"].firstMatch.waitForExistence(timeout: 15))
            XCTAssertFalse(unreadable.staticTexts["What it would specialize in"].exists)
            XCTAssertFalse(unreadable.staticTexts["A taste of what it knows"].exists)
            attach(unreadable, named: "P146b-unreadable-\(appearance)")
            unreadable.terminate()

            let late = open(state: "late", appearance: appearance)
            XCTAssertTrue(late.staticTexts["What it would specialize in"].firstMatch.waitForExistence(timeout: 2),
                          "while the copy is read, the gray bars sit under their headings")
            XCTAssertFalse(late.staticTexts["Weekly updates"].exists, "the specialties are not there yet")
            XCTAssertTrue(late.staticTexts["Weekly updates"].firstMatch.waitForExistence(timeout: 20),
                          "the sections appear when the copy arrives")
            attach(late, named: "P146b-specialties-\(appearance)")
            late.terminate()
        }
    }

    func testListedSpecialtiesShowBothSectionsInBothAppearances() throws {
        for appearance in ["light", "dark"] {
            let app = open(state: "opened", appearance: appearance)
            XCTAssertTrue(app.staticTexts["Weekly updates"].firstMatch.waitForExistence(timeout: 15))
            XCTAssertTrue(app.staticTexts["What it would specialize in"].firstMatch.exists)
            XCTAssertTrue(app.staticTexts["A taste of what it knows"].firstMatch.exists)
            attach(app, named: "P146b-listed-\(appearance)")
            app.terminate()
        }
    }

    private func open(state: String, appearance: String) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "paired"
        app.launchEnvironment["YANSU_UITEST_YOUR_AGENT_STATE"] = state
        app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
        app.launch()
        let settings = app.buttons["root.settings"].firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 15))
        settings.tap()
        let row = app.buttons["settings.yourAgent.open"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.tap()
        return app
    }

    private func attach(_ app: XCUIApplication, named name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
