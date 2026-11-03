import XCTest

final class YourAgentUITests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    func testSettingsRowOpensPageAndDoneClosesSheet() {
        let app = launch("opened")
        XCTAssertTrue(app.staticTexts["Rent out an agent of you"].exists)
        app.buttons["settings.done"].tap()
        XCTAssertTrue(app.buttons["root.settings"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["settings.yourAgent.open"].exists)
    }

    func testStillLearningHidesTaste() {
        let app = launch("learning")
        XCTAssertTrue(app.staticTexts["Isoform is still learning how you work. Your specialties show here once it has seen you do something a few times."].exists)
        XCTAssertFalse(app.staticTexts["A TASTE OF WHAT IT KNOWS"].exists)
        XCTAssertFalse(app.staticTexts["Lead the weekly update with what changed, not what happened."].exists)
    }

    func testOpenedFieldsAreEmptyAndSendDisabled() {
        let app = launch("opened")
        let name = app.textFields["yourAgent.name"]
        XCTAssertEqual(name.label, "Page name, after isoform.ai/agents/")
        XCTAssertEqual(app.textFields["yourAgent.email"].label, "Your email")
        reveal(name, in: app)
        XCTAssertEqual(name.value as? String, "yourname")
        XCTAssertEqual(app.textFields["yourAgent.email"].value as? String, "Your email")
        XCTAssertFalse(app.buttons["yourAgent.reserve"].isEnabled)
    }

    func testPlaceholderHasHeadingsWithoutLearningOrLoading() {
        let app = launch("placeholder")
        XCTAssertTrue(app.staticTexts["WHAT IT WOULD SPECIALIZE IN"].exists)
        XCTAssertTrue(app.staticTexts["A TASTE OF WHAT IT KNOWS"].exists)
        XCTAssertFalse(app.staticTexts["Weekly updates"].exists)
        XCTAssertFalse(app.staticTexts["yourAgent.stillLearning"].exists)
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS[c] 'loading'")).firstMatch.exists)
        reveal(app.buttons["yourAgent.reserve"], in: app)
        XCTAssertFalse(app.buttons["yourAgent.reserve"].isEnabled)
    }

    func testFailedSendKeepsSendEnabledAndEmailBoInCard() {
        let app = launch("failed")
        submit(app)
        let failure = app.staticTexts["Couldn't send the request. Try again."]
        XCTAssertTrue(failure.waitForExistence(timeout: 5))
        reveal(failure, in: app)
        XCTAssertTrue(app.buttons["yourAgent.writeBo"].exists)
        XCTAssertTrue(app.buttons["yourAgent.reserve"].isEnabled)
    }

    func testRateLimitNamesTimeAndDimsSend() {
        let app = launch("rate")
        submit(app)
        let rate = app.staticTexts["yourAgent.rateLimited"]
        XCTAssertTrue(rate.waitForExistence(timeout: 5))
        XCTAssertTrue(rate.label.hasPrefix("Too many tries. Try again at "))
        XCTAssertFalse(app.buttons["yourAgent.reserve"].isEnabled)
    }

    private func submit(_ app: XCUIApplication) {
        fill(app, name: "alex.rivera")
        let send = app.buttons["yourAgent.reserve"]
        reveal(send, in: app); send.tap()
    }

    func testTakenChecksOnLeavingFieldAndSuggestionFillsName() {
        let app = launch("taken")
        fill(app, name: "alex")
        let suggestion = app.staticTexts["yourAgent.suggestion"]
        XCTAssertTrue(suggestion.waitForExistence(timeout: 5))
        XCTAssertEqual(suggestion.label, "Taken. Try alex.rivera.")
        reveal(suggestion, in: app)
        app.links["alex.rivera"].tap()
        XCTAssertEqual(app.textFields["yourAgent.name"].value as? String, "alex.rivera")
    }

    func testSendAndChangePrefillsForm() {
        let app = launch("opened")
        fill(app, name: "alex.rivera")
        let send = app.buttons["yourAgent.reserve"]
        reveal(send, in: app)
        XCTAssertTrue(send.isEnabled)
        send.tap()
        XCTAssertTrue(app.staticTexts["Check your email"].waitForExistence(timeout: 5))
        let change = app.links["Change the name or email"]
        reveal(change, in: app); change.tap()
        XCTAssertEqual(app.textFields["yourAgent.name"].value as? String, "alex.rivera")
        XCTAssertEqual(app.textFields["yourAgent.email"].value as? String, "alex.rivera@example.com")
    }

    func testCodeVerificationAndWrongCode() {
        for state in ["code", "wrongCode"] {
            let app = launch(state)
            let field = app.textFields["yourAgent.codeField"]
            reveal(field, in: app)
            XCTAssertEqual(field.label, "6-digit code")
            field.tap(); field.typeText("123456")
            let result = state == "code" ? app.staticTexts["isoform.ai/agents/alex.rivera is yours."]
                : app.staticTexts["That code didn't match. Try again."]
            XCTAssertTrue(result.waitForExistence(timeout: 5))
            if state == "wrongCode" { XCTAssertEqual(field.value as? String ?? "", "") }
            app.terminate()
        }
    }

    func testChoiceOlderAndRanOutStates() {
        let choice = launch("choice")
        XCTAssertTrue(choice.buttons["yourAgent.keep"].exists)
        let switchButton = choice.buttons["yourAgent.switch"]
        reveal(switchButton, in: choice); switchButton.tap()
        XCTAssertTrue(choice.staticTexts["isoform.ai/agents/alex.rivera is yours."].waitForExistence(timeout: 5))
        choice.terminate()
        let older = launch("older")
        let send = older.buttons["yourAgent.sendCode"]
        reveal(send, in: older); send.tap()
        XCTAssertTrue(older.staticTexts["Check your email"].waitForExistence(timeout: 5))
        older.terminate()
        let ranOut = launch("ranOut")
        XCTAssertTrue(ranOut.staticTexts["yourAgent.ranOut"].waitForExistence(timeout: 5))
        XCTAssertEqual(ranOut.textFields["yourAgent.name"].value as? String, "alex.rivera")
        XCTAssertEqual(ranOut.textFields["yourAgent.email"].value as? String, "alex.rivera@example.com")
    }

    func testOfflineDisablesSend() {
        let app = launch("offline")
        let offline = app.staticTexts["yourAgent.offline"]
        XCTAssertTrue(offline.waitForExistence(timeout: 5))
        reveal(offline, in: app)
        XCTAssertEqual(offline.label, "Connect to the internet to send the request.")
        XCTAssertFalse(app.buttons["yourAgent.reserve"].isEnabled)
    }

    func testReviewScreenshots() throws {
        for appearance in ["light", "dark"] {
            for state in ["opened", "learning", "taken", "sent", "placeholder", "failed", "rate", "code", "wrongCode", "yours", "choice", "ranOut", "older"] {
                let app = launch(state, appearance: appearance)
                if state == "taken" {
                    fill(app, name: "alex")
                    XCTAssertTrue(app.staticTexts["yourAgent.suggestion"].waitForExistence(timeout: 5))
                }
                if state == "failed" || state == "rate" {
                    submit(app)
                    let status = state == "failed" ? app.staticTexts["Couldn't send the request. Try again."]
                        : app.staticTexts["yourAgent.rateLimited"]
                    XCTAssertTrue(status.waitForExistence(timeout: 5))
                }
                if state == "wrongCode" {
                    let field = app.textFields["yourAgent.codeField"]
                    reveal(field, in: app); field.tap(); field.typeText("123456")
                    XCTAssertTrue(app.staticTexts["That code didn't match. Try again."].waitForExistence(timeout: 5))
                }
                let target: XCUIElement
                switch state {
                case "placeholder": target = app.staticTexts["Rent out an agent of you"]
                case "sent", "yours": target = app.buttons["yourAgent.share"]
                case "code", "wrongCode": target = app.textFields["yourAgent.codeField"]
                case "choice": target = app.buttons["yourAgent.switch"]
                case "older": target = app.buttons["yourAgent.sendCode"]
                default: target = app.buttons["yourAgent.reserve"]
                }
                reveal(target, in: app)
                let screenshot = app.screenshot()
                let size = app.frame.width < 400 ? "se" : "pro-max"
                let name = "\(size)-\(state)-\(appearance)"
                let attachment = XCTAttachment(screenshot: screenshot)
                attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
                if let path = ProcessInfo.processInfo.environment["YANSU_UITEST_SCREENSHOT_DIRECTORY"] {
                    let directory = URL(fileURLWithPath: path, isDirectory: true)
                    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
                    try screenshot.pngRepresentation.write(to: directory.appendingPathComponent("\(name).png"), options: .atomic)
                }
                app.terminate()
            }
        }
    }

    private func launch(_ state: String, appearance: String = "light") -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "paired"
        app.launchEnvironment["YANSU_UITEST_YOUR_AGENT_STATE"] = state
        app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
        app.launch()
        openSettings(app)
        let row = app.buttons["settings.yourAgent.open"]
        XCTAssertTrue(row.waitForExistence(timeout: 5))
        XCTAssertTrue(row.label.contains("Agent of You"))
        reveal(row, in: app); row.tap()
        XCTAssertTrue(app.staticTexts["Rent out an agent of you"].waitForExistence(timeout: 5))
        let ready = state == "placeholder" ? app.staticTexts["WHAT IT WOULD SPECIALIZE IN"] : state == "learning"
            ? app.staticTexts["Isoform is still learning how you work. Your specialties show here once it has seen you do something a few times."]
            : app.staticTexts["Weekly updates"]
        XCTAssertTrue(ready.waitForExistence(timeout: 5))
        return app
    }

    private func fill(_ app: XCUIApplication, name: String) {
        let nameField = app.textFields["yourAgent.name"]
        reveal(nameField, in: app); nameField.tap(); nameField.typeText(name)
        XCTAssertFalse(app.staticTexts["yourAgent.suggestion"].exists)
        let email = app.textFields["yourAgent.email"]
        reveal(email, in: app); email.tap(); email.typeText("alex.rivera@example.com")
        let scroll = app.scrollViews["yourAgent.page"]
        scroll.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3))
            .press(forDuration: 0.1, thenDragTo: scroll.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85)))
    }

    private func reveal(_ element: XCUIElement, in app: XCUIApplication) {
        for _ in 0..<12 {
            if element.exists && element.isHittable { return }
            if element.exists && element.frame.minY < 100 { app.swipeDown(velocity: .slow) }
            else { app.swipeUp(velocity: .slow) }
        }
        XCTAssertTrue(element.isHittable)
    }
}
