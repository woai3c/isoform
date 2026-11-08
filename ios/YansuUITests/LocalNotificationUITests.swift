import XCTest

final class LocalNotificationUITests: XCTestCase {
    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    func testOfflineAwakeOfferAndSettingsFooters() throws {
        let app = launch(offline: true, instant: false)
        let offer = app.descendants(matching: .any)["today.keepAwakeOffer"].firstMatch
        XCTAssertTrue(offer.waitForExistence(timeout: 15))
        attachScreenshot(app, named: "today-asleep-awake-offer")
        app.buttons["Turn on"].tap()
        XCTAssertTrue(app.staticTexts["Turns on when your computer is back."].waitForExistence(timeout: 10))
        openSettings(app)
        let toggle = app.switches["settings.keepAwake"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["So your phone can reach it while it's plugged in. The screen still turns off. A closed laptop still sleeps."].exists)
        XCTAssertTrue(app.staticTexts["Changes apply when your computer is back."].exists)
        attachScreenshot(app, named: "settings-keep-awake-offline")
        let footer = app.staticTexts["settings.notifications.lateFooter"]
        reveal(footer, in: app)
        XCTAssertTrue(footer.waitForExistence(timeout: 10))
        attachScreenshot(app, named: "settings-local-notifications")
    }

    func testInstantPushHidesLateFooter() throws {
        let app = launch(offline: false, instant: true)
        openSettings(app)
        XCTAssertTrue(app.descendants(matching: .any)["settings.group.notifications"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["settings.notifications.lateFooter"].exists)
    }

    func testOlderMacHidesUnknownKeepAwakeSwitch() throws {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "feed"
        app.launch()
        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 15))
        openSettings(app)
        XCTAssertTrue(app.descendants(matching: .any)["settings.group.mac"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.switches["settings.keepAwake"].exists)
    }

    func testNotNowStaysDismissedAfterRelaunch() throws {
        let app = launch(offline: true, instant: false)
        let offer = app.descendants(matching: .any)["today.keepAwakeOffer"].firstMatch
        XCTAssertTrue(offer.waitForExistence(timeout: 15))
        app.buttons["Not now"].firstMatch.tap()
        XCTAssertFalse(offer.exists)
        app.terminate()
        app.launchEnvironment.removeValue(forKey: "YANSU_UITEST_RESET_AWAKE_OFFER")
        app.launch()
        XCTAssertTrue(app.buttons["feed.summary"].waitForExistence(timeout: 15))
        XCTAssertFalse(offer.exists)
    }

    func testQuietHoursShortcutAndIconCountSwitch() throws {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "tasks-seeded"
        app.launch()
        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 15))
        openSettings(app)
        let quiet = app.buttons["settings.notifications.open"].firstMatch
        reveal(quiet, in: app)
        quiet.tap()
        let save = app.buttons["notifications.quietHours.save"]
        XCTAssertTrue(save.waitForExistence(timeout: 10))
        save.tap()
        XCTAssertTrue(quiet.waitForExistence(timeout: 10))
        let preferences = app.buttons["settings.notifications.preferences"].firstMatch
        reveal(preferences, in: app)
        preferences.tap()
        let badge = app.switches["notifications.iconBadge"]
        XCTAssertTrue(badge.waitForExistence(timeout: 10))
        badge.tap()
        attachScreenshot(app, named: "notifications-icon-count")
        badge.tap()
    }

    func testQuietHoursFailureStaysInEditor() throws {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "tasks-seeded"
        app.launchEnvironment["YANSU_UITEST_FAIL_QUIET_SAVE"] = "1"
        app.launch()
        openSettings(app)
        let quiet = app.buttons["settings.notifications.open"].firstMatch
        reveal(quiet, in: app)
        quiet.tap()
        let save = app.buttons["notifications.quietHours.save"]
        XCTAssertTrue(save.waitForExistence(timeout: 10))
        save.tap()
        XCTAssertTrue(app.staticTexts["notifications.quietHours.failure"].waitForExistence(timeout: 10))
        XCTAssertEqual(save.label, "Try again")
        attachScreenshot(app, named: "quiet-hours-save-failure")
    }

    func testNotificationPreferencesInChineseDark() throws {
        let app = XCUIApplication()
        app.launchArguments += ["-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN", "-AppleInterfaceStyle", "Dark"]
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "tasks-seeded"
        app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = "dark"
        app.launch()
        openSettings(app)
        let preferences = app.buttons["settings.notifications.preferences"].firstMatch
        reveal(preferences, in: app)
        preferences.tap()
        XCTAssertTrue(app.switches["notifications.iconBadge"].waitForExistence(timeout: 10))
        attachScreenshot(app, named: "notifications-icon-count-zh-Hans-dark")
    }

    func testKeepAwakeReservationFailureStaysByTheToggle() throws {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "feed"
        app.launchEnvironment["YANSU_UITEST_KEEP_AWAKE"] = "off"
        app.launchEnvironment["YANSU_UITEST_FAIL_AWAKE_SAVE"] = "1"
        app.launch()
        openSettings(app)
        let toggle = app.switches["settings.keepAwake"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))
        let before = toggle.value as? String
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)).tap()
        let failure = app.descendants(matching: .any)["settings.group.mac"].staticTexts["Could not save this change. Try again."]
        XCTAssertTrue(failure.waitForExistence(timeout: 10))
        XCTAssertEqual(toggle.value as? String, before)
        attachScreenshot(app, named: "keep-awake-save-failure")
    }

    func testBriefDeletionFailureShowsNoticeInSettings() throws {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "feed"
        app.launchEnvironment["YANSU_UITEST_FAIL_BRIEF_DELETE"] = "1"
        app.launch()
        openSettings(app)
        let delete = app.descendants(matching: .any)["settings.group.privacy"].buttons["Remove saved briefs"].firstMatch
        reveal(delete, in: app)
        delete.tap()
        app.sheets.buttons["Remove saved briefs"].firstMatch.tap()
        let notice = app.descendants(matching: .any)["settings.briefNotice"].firstMatch
        for _ in 0..<8 where !notice.isHittable { app.swipeDown() }
        XCTAssertTrue(notice.waitForExistence(timeout: 10))
        attachScreenshot(app, named: "brief-deletion-failure")
    }

    private func launch(offline: Bool, instant: Bool) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = offline ? "feed-offline" : "feed"
        app.launchEnvironment["YANSU_UITEST_KEEP_AWAKE"] = "off"
        app.launchEnvironment["YANSU_UITEST_INSTANT_PUSH"] = instant ? "true" : "false"
        app.launchEnvironment["YANSU_UITEST_RESET_AWAKE_OFFER"] = "1"
        app.launch()
        XCTAssertTrue(app.buttons["tab.today"].waitForExistence(timeout: 15))
        return app
    }

    private func reveal(_ element: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<8 {
            if element.exists && element.isHittable { return }
            app.swipeUp()
        }
        XCTAssertTrue(element.isHittable)
    }
}
