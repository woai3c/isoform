import UIKit
import XCTest

// P162: at accessibility text sizes the tab bar steps aside in Chat while the keyboard is up, and the keyboard's
// Done brings it back. Default sizes keep the bar while typing (P159). Seeded chat only.
final class TabBarKeyboardUITests: XCTestCase {
    private let lastReply = "Drafted. It thanks Sam for the notes and asks for the final numbers by Thursday."

    override func setUpWithError() throws {
        try super.setUpWithError()
        // Every state is framed and measured even when one state misses the rule.
        continueAfterFailure = true
    }

    func testAtTheLargestSizeTheBarStepsAsideWhileTheKeyboardIsUpAndDoneBringsItBack() throws {
        for appearance in ["light", "dark"] {
            let app = launch(appearance: appearance, category: "UICTContentSizeCategoryAccessibilityXXXL")
            let reply = message(lastReply, in: app)
            XCTAssertTrue(reply.waitForExistence(timeout: 15), "the seeded reply is on screen")
            RunLoop.current.run(until: Date().addingTimeInterval(1.5))
            let tabBar = app.buttons["tab.chat"].firstMatch
            XCTAssertTrue(tabBar.waitForExistence(timeout: 5), "the bar shows with the keyboard down")
            let box = app.descendants(matching: .any)["chat.composerBox"].firstMatch
            XCTAssertTrue(box.waitForExistence(timeout: 10))
            let down = visibleReplyLines(reply, box: box, in: app)
            report(app, "P162 largest \(appearance) keyboard-down", lines: down, bar: tabBar.exists)

            let field = app.descendants(matching: .any)["chat.composer"].firstMatch
            field.tap()
            let keyboard = app.keyboards.firstMatch
            XCTAssertTrue(keyboard.waitForExistence(timeout: 5), "typing opens the keyboard")
            // Read the state the keyboard leaves the screen in, once the bar's change has finished.
            RunLoop.current.run(until: Date().addingTimeInterval(2.0))
            let up = visibleReplyLines(reply, box: box, in: app)
            let barUp = tabBar.exists && tabBar.isHittable
            report(app, "P162 largest \(appearance) keyboard-up", lines: up, bar: barUp)
            attach(app, named: "P162-largest-keyboard-up-\(appearance)")
            XCTAssertFalse(barUp, "at the largest size the bar steps aside while the keyboard is up (\(appearance))")

            let done = app.buttons["composer.keyboard.done"].firstMatch
            XCTAssertTrue(done.waitForExistence(timeout: 5), "the keyboard shows its Done button")
            done.tap()
            XCTAssertTrue(tabBar.waitForExistence(timeout: 5), "Done brings the bar back (\(appearance))")
            XCTAssertFalse(keyboard.exists, "Done puts the keyboard away (\(appearance))")
            app.terminate()
        }
    }

    func testAtTheDefaultSizeTheBarStaysWhileTheKeyboardIsUp() throws {
        for appearance in ["light", "dark"] {
            let app = launch(appearance: appearance, category: "UICTContentSizeCategoryL")
            let reply = message(lastReply, in: app)
            XCTAssertTrue(reply.waitForExistence(timeout: 15))
            let tabBar = app.buttons["tab.chat"].firstMatch
            XCTAssertTrue(tabBar.waitForExistence(timeout: 5))
            let field = app.descendants(matching: .any)["chat.composer"].firstMatch
            field.tap()
            let keyboard = app.keyboards.firstMatch
            XCTAssertTrue(keyboard.waitForExistence(timeout: 5), "typing opens the keyboard")
            RunLoop.current.run(until: Date().addingTimeInterval(2.0))
            XCTAssertTrue(tabBar.isHittable, "at the default size the bar stays while typing (P159, \(appearance))")
            XCTAssertLessThanOrEqual(tabBar.frame.maxY, keyboard.frame.minY + 1, "the bar sits above the keyboard (\(appearance))")
            app.terminate()
        }
    }

    // MARK: Helpers

    /// Whole lines of the last reply that sit between the navigation bar and the composer's box, at the body line
    /// height for the largest size. The reply's lines are counted from its frame, not read from the text.
    private func visibleReplyLines(_ reply: XCUIElement, box: XCUIElement, in app: XCUIApplication) -> (visible: Int, total: Int) {
        let largest = UITraitCollection(preferredContentSizeCategory: .accessibilityExtraExtraExtraLarge)
        let line = UIFont.preferredFont(forTextStyle: .body, compatibleWith: largest).lineHeight
        let top = max(reply.frame.minY, app.navigationBars.firstMatch.frame.maxY)
        let bottom = min(reply.frame.maxY, box.frame.minY)
        let visible = Int(max(0, bottom - top) / line + 0.01)
        let total = Int((reply.frame.height / line).rounded())
        return (visible, total)
    }

    private func report(_ app: XCUIApplication, _ label: String, lines: (visible: Int, total: Int), bar: Bool) {
        let line = "\(label): reply-lines-visible=\(lines.visible) of \(lines.total) tab-bar-shown=\(bar)"
        print(line)
        let attachment = XCTAttachment(string: line)
        attachment.name = "measure-\(label)"
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private func launch(appearance: String, category: String) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += ["-UIPreferredContentSizeCategoryName", category]
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "chat"
        app.launchEnvironment["YANSU_UITEST_TAB"] = "chat"
        app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
        app.launch()
        XCTAssertTrue(app.buttons["tab.chat"].waitForExistence(timeout: 15))
        return app
    }

    private func message(_ text: String, in app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    private func attach(_ app: XCUIApplication, named name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
