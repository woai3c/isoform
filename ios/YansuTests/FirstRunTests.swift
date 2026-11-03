import XCTest
@testable import Yansu

@MainActor
final class FirstRunTests: XCTestCase {
    private var defaults: UserDefaults!
    private var suite: String!

    override func setUp() {
        super.setUp()
        suite = "FirstRunTests.\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suite)!
    }

    override func tearDown() {
        defaults.removePersistentDomain(forName: suite)
        super.tearDown()
    }

    func testFreshLaunchShowsWelcomeUntilUserMakesAChoice() {
        XCTAssertTrue(FirstRunStore(defaults: defaults).shouldPresent(hasPairing: false, needsPairingRecovery: false))
        XCTAssertTrue(FirstRunStore(defaults: defaults).shouldPresent(hasPairing: false, needsPairingRecovery: false),
                      "Closing the app without a choice must not skip the welcome")
    }

    func testNotNowSurvivesRelaunch() {
        FirstRunStore(defaults: defaults).complete()
        XCTAssertFalse(FirstRunStore(defaults: defaults).shouldPresent(hasPairing: false, needsPairingRecovery: false))
    }

    func testExistingAndRecoverablePairingsBypassWelcome() {
        let firstRun = FirstRunStore(defaults: defaults)
        XCTAssertFalse(firstRun.shouldPresent(hasPairing: true, needsPairingRecovery: false))
        XCTAssertFalse(firstRun.shouldPresent(hasPairing: false, needsPairingRecovery: true))
    }

    func testPairingButtonsReusePairingAndCancellationLeavesTabsAvailable() {
        let firstRun = FirstRunStore(defaults: defaults)
        let router = NotificationRouter()
        firstRun.pair(using: router, scanCode: true)
        XCTAssertEqual(router.sheet, .pairing)
        XCTAssertTrue(router.startsPairingWithScanner)
        router.sheet = nil
        XCTAssertFalse(firstRun.shouldPresent(hasPairing: false, needsPairingRecovery: false))
        router.openPairing()
        XCTAssertFalse(router.startsPairingWithScanner, "Other pairing callers keep their entry screen")
        firstRun.pair(using: router, scanCode: false)
        XCTAssertEqual(router.sheet, .pairing)
        XCTAssertFalse(router.startsPairingWithScanner)
    }

    func testDownloadDestinationAndWelcomeTranslations() throws {
        XCTAssertEqual(FirstRunStore.computerAppURL.absoluteString, "https://isoform.ai")
        for language in ["en", "zh-Hans"] {
            let path = try XCTUnwrap(Bundle.main.path(forResource: language, ofType: "lproj"))
            let bundle = try XCTUnwrap(Bundle(path: path))
            let key = "Isoform works on your computer and answers to you here."
            let text = bundle.localizedString(forKey: key, value: nil, table: nil)
            XCTAssertFalse(text.isEmpty)
            if language == "zh-Hans" { XCTAssertNotEqual(text, key) }
            XCTAssertFalse(text.contains("Mac"))
            XCTAssertFalse(text.contains("iPhone"))
        }
    }
}
