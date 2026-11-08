import XCTest

// P153 (empty Chat at the largest text size): the empty chat opens at its top under the title bar, and at its end the
// copy's last line sits just above the composer. Design walk v1.0.4, frame C4, light and dark. Seeded state only.
final class ChatEmptyUITests: XCTestCase {
    override func setUpWithError() throws {
        try super.setUpWithError()
        continueAfterFailure = false
    }

    func testEmptyChatAtTheLargestSizeOpensAtTopAndClearsTheComposer() throws {
        for appearance in ["light", "dark"] {
            let app = XCUIApplication()
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
            app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "chat-empty"
            app.launchEnvironment["YANSU_UITEST_TAB"] = "chat"
            app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
            app.launch()
            XCTAssertTrue(app.buttons["tab.chat"].waitForExistence(timeout: 15))

            let empty = app.descendants(matching: .any)["chat.emptyState"].firstMatch
            XCTAssertTrue(empty.waitForExistence(timeout: 15), "the empty chat shows its card")
            let title = app.staticTexts["No messages yet."].firstMatch
            XCTAssertTrue(title.exists, "the title is whole")
            let detail = app.staticTexts["Write below to start. Isoform answers on your computer."].firstMatch
            XCTAssertTrue(detail.exists, "the copy is whole")
            let composer = app.descendants(matching: .any)["chat.composer"].firstMatch
            XCTAssertTrue(composer.waitForExistence(timeout: 10))
            let bar = app.navigationBars.staticTexts["Chat"].firstMatch
            if bar.exists {
                XCTAssertGreaterThanOrEqual(title.frame.minY, bar.frame.maxY, "the empty chat opens below the title bar")
            }
            // The chat opens at its top: the card starts under the title bar and is taller than the visible area, so its
            // copy runs under the composer until the chat is scrolled.
            let topEdge = bar.exists ? bar.frame.maxY : 0
            XCTAssertLessThan(empty.frame.minY, (topEdge + composer.frame.minY) / 2,
                              "the empty chat opens with its card at the top (P153)")
            measure(app, appearance: appearance, state: "open", copy: detail, composer: composer)
            attach(app, named: "C4-empty-chat-open-\(appearance)")

            // Scroll to the end with drags on the card: coordinate drags reach the scroll view, while swipes on the
            // scroll element do not.
            let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.55))
            let to = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.05))
            for _ in 0..<3 {
                from.press(forDuration: 0.05, thenDragTo: to, withVelocity: .slow, thenHoldForDuration: 0.1)
            }
            RunLoop.current.run(until: Date().addingTimeInterval(1.0))

            // At its end the copy's last line sits just above the composer (P153): a gap of 0 to 40 pt.
            // The composer's top is its visible box, which starts above the text field by the field's own padding.
            let box = app.descendants(matching: .any)["chat.composerBox"].firstMatch
            XCTAssertTrue(box.waitForExistence(timeout: 5), "the composer box is on screen")
            measure(app, appearance: appearance, state: "end", copy: detail, composer: box)
            attach(app, named: "C4-empty-chat-end-\(appearance)")
            let gap = box.frame.minY - detail.frame.maxY
            XCTAssertGreaterThanOrEqual(gap, 0, "at its end the copy's last line is not under the composer (P153)")
            XCTAssertLessThanOrEqual(gap, 40, "the gap from the copy's last line to the composer is 40 pt or less (P153)")
            app.terminate()
        }
    }

    /// Frames in points, read from the accessibility tree: the scroll view, the copy's last line and the composer's top.
    private func measure(_ app: XCUIApplication, appearance: String, state: String, copy: XCUIElement, composer: XCUIElement) {
        let scroll = app.scrollViews.firstMatch
        let line = "P153 \(state) \(appearance): screen=\(app.frame) scroll=\(scroll.frame) "
            + "copy.maxY=\(copy.frame.maxY) composer.minY=\(composer.frame.minY) "
            + "gap=\(composer.frame.minY - copy.frame.maxY)"
        print(line)
        let attachment = XCTAttachment(string: line)
        attachment.name = "P153-measure-\(state)-\(appearance)"
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private func attach(_ app: XCUIApplication, named name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
