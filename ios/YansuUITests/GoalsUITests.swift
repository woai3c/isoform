import XCTest

final class GoalsUITests: XCTestCase {
    func testGoalsDeepLinkAliasOpensToday() throws {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "tasks-seeded"
        app.launchEnvironment["YANSU_UITEST_TAB"] = "goals"
        app.launch()

        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 15))
        XCTAssertTrue(app.buttons["tab.today"].isSelected)
    }
}
