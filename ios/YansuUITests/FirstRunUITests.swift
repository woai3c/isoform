import XCTest

final class FirstRunUITests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    func testWelcomePairsThenKeepsFallbackAfterCancelAndRelaunch() {
        let app = fixture()
        app.launch()
        XCTAssertTrue(app.staticTexts["welcome.title"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["tab.today"].exists)
        attachScreenshot(app, named: "first-run-english")
        reveal("welcome.pair", in: app)
        app.buttons["welcome.pair"].tap()
        XCTAssertTrue(app.buttons["pairing.close"].waitForExistence(timeout: 10))
        app.buttons["pairing.close"].tap()
        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 10))
        app.terminate()
        app.launch()
        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["welcome.title"].exists)
    }

    func testChineseWelcomeCanBeSkipped() {
        let app = fixture()
        app.launchArguments += ["-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
        app.launch()
        let title = app.staticTexts["welcome.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 10))
        XCTAssertEqual(title.label, "Isoform 在电脑上为你工作，你可以在这里与它交流。")
        attachScreenshot(app, named: "first-run-chinese")
        reveal("welcome.skip", in: app)
        app.buttons["welcome.skip"].tap()
        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 10))
    }

    private func fixture() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "first-run"
        app.launchEnvironment["YANSU_UITEST_SETTINGS_ID"] = UUID().uuidString
        return app
    }

    private func reveal(_ identifier: String, in app: XCUIApplication) {
        for _ in 0..<5 {
            if app.buttons[identifier].isHittable { return }
            app.swipeUp()
        }
        XCTAssertTrue(app.buttons[identifier].isHittable)
    }
}
