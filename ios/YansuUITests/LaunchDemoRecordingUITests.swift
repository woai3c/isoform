import XCTest

/// Drives the launch demo take at a person's pace while `scripts/record-ios-launch-demo.sh` records the Simulator.
/// Runs only when the recorder asks for it (TEST_RUNNER_YANSU_LAUNCH_DEMO=1); prints DEMO_START and DEMO_END with the
/// time, so the recorder can cut the video to the take.
final class LaunchDemoRecordingUITests: XCTestCase {
    func testLaunchDemoTake() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["YANSU_LAUNCH_DEMO"] == "1", "Recorder only")
        let pace = Double(ProcessInfo.processInfo.environment["YANSU_LAUNCH_DEMO_PACE"] ?? "1") ?? 1
        func pause(_ seconds: Double) { Thread.sleep(forTimeInterval: seconds * pace) }
        func mark(_ name: String) { print("\(name) \(Date().timeIntervalSince1970)") }

        let app = XCUIApplication()
        app.launchEnvironment["YANSU_UITEST_SETTINGS_STATE"] = "launch-demo"
        app.launchEnvironment["YANSU_UITEST_TAB"] = "feed"
        app.launchEnvironment["YANSU_UITEST_OWNER_CHECK"] = "allow"
        app.launch()

        let play = app.buttons["brief.hearBrief"]
        XCTAssertTrue(play.waitForExistence(timeout: 20), "Today has no Play on the brief")
        let topic = app.buttons.matching(NSPredicate(format: "identifier == 'feed.topic' AND label CONTAINS 'Thursday review'")).firstMatch
        XCTAssertTrue(topic.waitForExistence(timeout: 10), "Today has no Thursday review topic")
        pause(2)
        mark("DEMO_START")

        // The brief, already written: hold, then play it.
        pause(2)
        play.tap()
        pause(2.6)

        // Down to the topics, and into Thursday review.
        // One slow swipe brings the topics up from under the tab bar.
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.86))
            .press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.12)),
                   withVelocity: 900, thenHoldForDuration: 0.2)
        pause(0.9)
        topic.tap()

        // Priya's reply, drafted: read it, then send it.
        let read = app.buttons["topic.readDraft"]
        XCTAssertTrue(read.waitForExistence(timeout: 10), "the topic has no draft to read")
        pause(1.1)
        read.tap()
        let send = app.buttons["topic.sendReply"]
        XCTAssertTrue(send.waitForExistence(timeout: 5), "the draft has no Send")
        pause(2.4)
        send.tap()
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH 'Sent via Mail'")).firstMatch
            .waitForExistence(timeout: 10), "Send showed no receipt")
        pause(1.4)

        // One question, answered from the computer.
        let box = app.textFields["topic.composer"].exists ? app.textFields["topic.composer"] : app.textViews["topic.composer"]
        box.tap()
        pause(0.4)
        let words = "What’s left before Thursday?".split(separator: " ")
        for (index, word) in words.enumerated() {
            box.typeText(index == words.count - 1 ? String(word) : word + " ")
            Thread.sleep(forTimeInterval: 0.12 * pace)
        }
        pause(0.7)
        // The arrow rises with the keyboard; tap where it is now, once more if the first tap landed mid-move.
        let arrow = app.buttons["topic.send"]
        let sentRow = app.descendants(matching: .any)["topic.message.user"]
        for _ in 0..<2 where !sentRow.exists {
            arrow.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
            _ = sentRow.waitForExistence(timeout: 1.5)
        }
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'Bring the import numbers'"))
            .firstMatch.waitForExistence(timeout: 15), "the question was not answered")
        pause(2.8)
        mark("DEMO_END")
    }
}
