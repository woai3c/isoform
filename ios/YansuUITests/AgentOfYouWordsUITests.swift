import XCTest

// P146 (Agent of You naming): one name, Agent of You, on the page and its title; sentence-case headings. Design walk
// v1.0.4, frame A1, light and dark. Seeded state only. Frames are attached in the result bundle and exported from there.
final class AgentOfYouWordsUITests: XCTestCase {
    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    func testAgentOfYouPageUsesOneNameAndSentenceCaseHeadings() throws {
        for appearance in ["light", "dark"] {
            let app = XCUIApplication()
            app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
            app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "paired"
            app.launchEnvironment["YANSU_UITEST_YOUR_AGENT_STATE"] = "placeholder"
            app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
            app.launch()
            let settings = app.buttons["root.settings"].firstMatch
            XCTAssertTrue(settings.waitForExistence(timeout: 15))
            settings.tap()
            let row = app.buttons["settings.yourAgent.open"].firstMatch
            XCTAssertTrue(row.waitForExistence(timeout: 10))
            row.tap()

            XCTAssertTrue(app.staticTexts["Agent of You"].firstMatch.waitForExistence(timeout: 10), "the page is named Agent of You")
            XCTAssertFalse(app.staticTexts["Rent out an agent of you"].exists, "the old title is gone")
            // A specialties section with nothing to list is left out (cf216d870, P146), so the heading is absent here.
            XCTAssertFalse(app.staticTexts["What it would specialize in"].exists, "an empty section is left out")
            XCTAssertTrue(app.staticTexts["Coming soon"].firstMatch.waitForExistence(timeout: 10),
                          "headings are sentence case")
            XCTAssertFalse(app.staticTexts["COMING SOON"].exists, "no all-caps heading")
            XCTAssertFalse(app.staticTexts["WHAT IT WOULD SPECIALIZE IN"].exists, "no all-caps heading")
            attach(app, named: "A1-agent-of-you-\(appearance)")
            app.terminate()
        }
    }

    private func attach(_ app: XCUIApplication, named name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
