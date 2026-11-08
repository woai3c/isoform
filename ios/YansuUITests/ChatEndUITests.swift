import XCTest

// P161: the end of a transcript sits as far above the composer's box as two messages from different senders sit apart,
// at the default and the largest text size, with the keyboard down and up, in light and dark. The tab bar stays
// while the keyboard is up (P159). Seeded states only; the seeded pairing points at a relay that does not exist.
final class ChatEndUITests: XCTestCase {
    private let lastMessage = "Can you draft the reply to Sam about the Q3 deck?"
    private let lastReply = "Drafted. It thanks Sam for the notes and asks for the final numbers by Thursday."

    /// Points of tolerance for the gap to the box, against the gap between two senders.
    private let tolerance: CGFloat = 2

    override func setUpWithError() throws {
        try super.setUpWithError()
        // Every state is framed and measured even when one state misses the rule.
        continueAfterFailure = true
    }

    func testTheEndOfATranscriptSitsAsFarAboveTheBoxAsTwoSendersAreApart() throws {
        let sizes: [(name: String, category: String?)] = [("default", "UICTContentSizeCategoryL"),
                                                          ("largest", "UICTContentSizeCategoryAccessibilityXXXL")]
        for size in sizes {
            for appearance in ["light", "dark"] {
                let app = launch(state: "chat", appearance: appearance, category: size.category)
                let reply = message(lastReply, in: app)
                XCTAssertTrue(reply.waitForExistence(timeout: 15), "the seeded reply is on screen")
                XCTAssertTrue(waitUntilSettled(reply, in: app), "the chat opens at its newest message")
                // The open-time scroll and the composer's first layout finish a little after the reply is on screen.
                RunLoop.current.run(until: Date().addingTimeInterval(1.5))
                let user = message(lastMessage, in: app)
                XCTAssertTrue(user.exists, "the seeded message from this iPhone is on screen")
                let box = app.descendants(matching: .any)["chat.composerBox"].firstMatch
                XCTAssertTrue(box.waitForExistence(timeout: 10))
                let tabBar = app.buttons["tab.chat"].firstMatch

                // The user's group ends at its status caption when it has one ("Seen by your computer").
                let between = gapBetweenSenders(user: user, in: app, reply: reply)
                let end = box.frame.minY - reply.frame.maxY
                let measure = Measure(label: "P161 \(size.name) \(appearance) keyboard-down", between: between, boxGap: end,
                                      scrollBottom: scrollBottom(app), box: box.frame, bar: tabBar.frame, barTop: composerBarTop(app))
                report(app, measure)
                attach(app, named: "P161-end-\(size.name)-keyboard-down-\(appearance)")
                XCTAssertEqual(end, between, accuracy: tolerance,
                               "the gap to the box equals the gap between two senders (\(size.name), \(appearance))")
                XCTAssertLessThanOrEqual(reply.frame.maxY, box.frame.minY, "no reply sits under the composer (\(size.name), \(appearance))")
                XCTAssertTrue(tabBar.exists, "the tab bar shows")

                let field = app.descendants(matching: .any)["chat.composer"].firstMatch
                field.tap()
                let keyboard = app.keyboards.firstMatch
                XCTAssertTrue(keyboard.waitForExistence(timeout: 5), "typing opens the keyboard")
                RunLoop.current.run(until: Date().addingTimeInterval(3.0))
                let endUp = box.frame.minY - reply.frame.maxY
                let upMeasure = Measure(label: "P161 \(size.name) \(appearance) keyboard-up", between: between, boxGap: endUp,
                                        scrollBottom: scrollBottom(app), box: box.frame,
                                        bar: tabBar.exists ? tabBar.frame : .zero, barTop: composerBarTop(app))
                report(app, upMeasure)
                attach(app, named: "P161-end-\(size.name)-keyboard-up-\(appearance)")
                if size.name == "default" {
                    // The bar stays while the keyboard is up at default sizes (P159).
                    XCTAssertTrue(tabBar.exists && tabBar.isHittable, "the tab bar stays while the keyboard is up (P159)")
                    XCTAssertLessThanOrEqual(tabBar.frame.maxY, keyboard.frame.minY + 1, "the tab bar sits above the keyboard")
                    XCTAssertLessThanOrEqual(box.frame.maxY, tabBar.frame.minY, "the composer sits above the tab bar")
                } else {
                    // At accessibility sizes the bar steps aside while the keyboard is up (P162).
                    XCTAssertFalse(tabBar.isHittable, "the tab bar steps aside at the largest size while the keyboard is up (P162)")
                    XCTAssertLessThanOrEqual(box.frame.maxY, keyboard.frame.minY + 1, "the composer sits above the keyboard")
                }
                XCTAssertLessThanOrEqual(reply.frame.maxY, box.frame.minY, "the last message stays above the composer")
                XCTAssertEqual(endUp, between, accuracy: tolerance,
                               "with the keyboard up the gap to the box still equals the gap between senders (\(size.name), \(appearance))")
                app.terminate()
            }
        }
    }

    /// A reader scrolled up from the end: the transcript is cut at the box's top, with no empty strip between them.
    func testScrolledUpTheTranscriptIsCutAtTheBox() throws {
        let app = launch(state: "chat", appearance: "light", category: "UICTContentSizeCategoryL")
        let reply = message(lastReply, in: app)
        XCTAssertTrue(reply.waitForExistence(timeout: 15))
        XCTAssertTrue(waitUntilSettled(reply, in: app))
        let box = app.descendants(matching: .any)["chat.composerBox"].firstMatch
        XCTAssertTrue(box.waitForExistence(timeout: 10))
        let scroll = app.scrollViews.firstMatch
        XCTAssertTrue(scroll.waitForExistence(timeout: 5))
        scroll.swipeDown()
        scroll.swipeDown()
        RunLoop.current.run(until: Date().addingTimeInterval(1.0))
        let cut = box.frame.minY - scrollBottom(app)
        let line = "P161 mid-scroll: box-top=\(box.frame.minY) scroll-bottom=\(scrollBottom(app)) cut=\(cut)"
        print(line)
        attachText(line, named: "measure-P161-mid-scroll")
        attach(app, named: "P161-mid-scroll-light")
        XCTAssertEqual(cut, 0, accuracy: tolerance, "the scrolled transcript is cut at the box's top, with no strip between")
        app.terminate()
    }

    func testTheEmptyChatAtTheDefaultSizeEndsAtItsCard() throws {
        for appearance in ["light", "dark"] {
            let app = launch(state: "chat-empty", appearance: appearance, category: "UICTContentSizeCategoryL")
            let detail = app.staticTexts["Write below to start. Isoform answers on your computer."].firstMatch
            XCTAssertTrue(detail.waitForExistence(timeout: 15), "the empty card shows its copy")
            let box = app.descendants(matching: .any)["chat.composerBox"].firstMatch
            XCTAssertTrue(box.waitForExistence(timeout: 10))
            let end = box.frame.minY - detail.frame.maxY
            let measure = Measure(label: "P161 empty default \(appearance)", between: 0, boxGap: end,
                                  scrollBottom: scrollBottom(app), box: box.frame, bar: app.buttons["tab.chat"].firstMatch.frame)
            report(app, measure)
            attach(app, named: "P161-empty-default-\(appearance)")
            // At the default size the card sits at the top with the composer below it: the gap is measured, not limited.
            XCTAssertGreaterThanOrEqual(end, 0, "the card's copy is not under the composer")
            app.terminate()
        }
    }

    // MARK: Helpers

    private struct Measure {
        let label: String
        let between: CGFloat
        let boxGap: CGFloat
        let scrollBottom: CGFloat
        let box: CGRect
        let bar: CGRect
        var barTop: CGFloat = 0
    }

    private func launch(state: String, appearance: String, category: String?) -> XCUIApplication {
        let app = XCUIApplication()
        if let category {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", category]
        }
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = state
        app.launchEnvironment["YANSU_UITEST_TAB"] = "chat"
        app.launchEnvironment["YANSU_UITEST_APPEARANCE"] = appearance
        app.launch()
        XCTAssertTrue(app.buttons["tab.chat"].waitForExistence(timeout: 15))
        return app
    }

    private func message(_ text: String, in app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    private func composerBarTop(_ app: XCUIApplication) -> CGFloat {
        app.descendants(matching: .any)["chat.composerBar"].firstMatch.frame.minY
    }

    /// The bottom of the transcript's scroll area: where the content is cut, above the composer.
    private func scrollBottom(_ app: XCUIApplication) -> CGFloat {
        app.scrollViews.firstMatch.frame.maxY
    }

    /// From the bottom of the sender's group to the top of the next sender's first message.
    private func gapBetweenSenders(user: XCUIElement, in app: XCUIApplication, reply: XCUIElement) -> CGFloat {
        let caption = app.staticTexts.matching(identifier: "chat.message.status").firstMatch
        let userEnd = caption.exists && caption.frame.minY > user.frame.maxY ? caption.frame.maxY : user.frame.maxY
        return reply.frame.minY - userEnd
    }

    /// Waits until the element's frame stops moving (the transcript has finished scrolling to its end).
    private func waitUntilSettled(_ element: XCUIElement, in app: XCUIApplication, timeout: TimeInterval = 10) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        var last: CGRect?
        repeat {
            let frame = element.frame
            if frame == last, frame.maxY > 0 { return true }
            last = frame
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
        } while Date() < deadline
        return false
    }

    private func report(_ app: XCUIApplication, _ measure: Measure) {
        let line = "\(measure.label): between-senders=\(measure.between) box-gap=\(measure.boxGap) "
            + "bar-top=\(measure.barTop) box=\(measure.box) tab-bar=\(measure.bar)"
        print(line)
        attachText(line, named: "measure-\(measure.label)")
    }

    private func attachText(_ text: String, named name: String) {
        let attachment = XCTAttachment(string: text)
        attachment.name = name
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
