import Foundation
import XCTest
@testable import Yansu

@MainActor
final class RootViewTests: XCTestCase {
    func testTabTitlesFollowThePhonesLanguageChoice() {
        let en = Locale(identifier: "en-US")
        let zh = Locale(identifier: "zh-Hans-CN")
        let other = Locale(identifier: "fr-FR")

        for tab in [NotificationRouter.Tab.today, .chat, .tasks] {
            XCTAssertEqual(RootView.tabTitle(tab, locale: en), RootView.tabTitle(tab, locale: other),
                           "a language the phone does not offer falls back to English")
        }
        XCTAssertEqual([NotificationRouter.Tab.today, .chat, .tasks].map { RootView.tabTitle($0, locale: en) },
                       ["Today", "Chat", "Tasks"])
        XCTAssertEqual([NotificationRouter.Tab.today, .chat, .tasks].map { RootView.tabTitle($0, locale: zh) },
                       ["今日", "聊天", "任务"])
    }
}
