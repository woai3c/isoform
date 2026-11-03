import XCTest

// Grants notification permission on a Simulator so a `simctl push` can show a banner and its actions.
// It is skipped unless the test runner gets YANSU_UITEST_GRANT_NOTIFICATIONS=1, so the normal suite never
// touches the Simulator's permission state:
//   TEST_RUNNER_YANSU_UITEST_GRANT_NOTIFICATIONS=1 xcodebuild test ... -only-testing:YansuUITests/NotificationPermissionUITests
final class NotificationPermissionUITests: XCTestCase {
    private let stepTimeout: TimeInterval = 30

    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
        guard ProcessInfo.processInfo.environment["YANSU_UITEST_GRANT_NOTIFICATIONS"] == "1" else {
            throw XCTSkip("set TEST_RUNNER_YANSU_UITEST_GRANT_NOTIFICATIONS=1 to grant notification permission on this Simulator")
        }
    }

    func testGrantsNotificationPermissionAndSchedulesTheTestNotice() throws {
        let app = XCUIApplication()
        // The notification card is paired-only. Seed isolated display state while keeping the real
        // iOS permission prompt and local-notification scheduler, without needing a live Mac.
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "paired"
        app.launch()
        openSettings(app)
        let notifications = app.buttons["settings.notifications.preferences"].firstMatch
        XCTAssertTrue(notifications.waitForExistence(timeout: stepTimeout))
        revealButton(notifications, in: app)
        notifications.tap()

        let testButton = app.buttons["settings.testNotification"].firstMatch
        XCTAssertTrue(testButton.waitForExistence(timeout: stepTimeout), "Settings has no test notification button")
        revealButton(testButton, in: app)
        attachScreenshot(app, named: "notification-settings-before")
        testButton.tap()

        // The explanation sheet appears only after a paired iPhone gets its first brief. Drive through it if it shows.
        let accept = app.buttons["push.permission.accept"].firstMatch
        if accept.waitForExistence(timeout: 3) {
            attachScreenshot(app, named: "notification-explanation-sheet")
            accept.tap()
        }

        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.alerts.buttons["Allow"].firstMatch
        if allow.waitForExistence(timeout: 10) {
            attachScreenshot(springboard, named: "notification-permission-alert")
            allow.tap()
        }

        // Settings shows this line only after authorization is granted and the local notice is scheduled.
        let scheduled = app.staticTexts["A local test is scheduled on this phone. It arrives in a few seconds."].firstMatch
        if !scheduled.waitForExistence(timeout: stepTimeout) {
            attachScreenshot(app, named: "notification-permission-not-granted")
            let denied = app.staticTexts["Turn on notifications for Isoform in the iOS Settings app first."].firstMatch
            XCTFail(denied.exists
                    ? "iOS did not grant notification permission to Isoform"
                    : "Settings never confirmed that the test notification was scheduled")
            return
        }
        attachScreenshot(app, named: "notification-test-scheduled")
    }

    private func revealButton(_ element: XCUIElement, in app: XCUIApplication, maxSwipes: Int = 6) {
        var swipes = 0
        while !(element.exists && element.isHittable) && swipes < maxSwipes {
            app.swipeUp()
            swipes += 1
        }
    }
}
