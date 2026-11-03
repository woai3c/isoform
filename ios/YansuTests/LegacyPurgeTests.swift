import Foundation
import XCTest
@testable import Yansu

final class LegacyPurgeTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUpWithError() throws {
        try super.setUpWithError()
        suiteName = "yansu.tests.purge.\(UUID().uuidString)"
        defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
    }

    override func tearDownWithError() throws {
        defaults.removePersistentDomain(forName: suiteName)
        try super.tearDownWithError()
    }

    private final class Recorder: @unchecked Sendable {
        var deleted: [String] = []
    }

    private func targets(directory: URL, accounts: [String], recorder: Recorder) -> LegacyPurge.Targets {
        LegacyPurge.Targets(supportDirectory: directory, defaults: defaults,
                            keychainAccounts: { accounts },
                            deleteKeychainAccount: { recorder.deleted.append($0) })
    }

    func testPurgeRemovesLegacyFilesDefaultsAndKeychainItemsOnce() throws {
        let directory = try makeTemporaryDirectory()
        for name in LegacyPurge.legacyFileNames {
            try Data("legacy".utf8).write(to: directory.appendingPathComponent(name))
        }
        let keeper = directory.appendingPathComponent("pairing.json")
        try Data("current".utf8).write(to: keeper)
        defaults.set("draft", forKey: "yansu.chat.compose-drafts")
        defaults.set(true, forKey: "yansu.push.optIn")

        let recorder = Recorder()
        let accounts = ["provider-openai", "hub-old-identity", "agent-service-7", "device-private-key", "device-bearer"]
        let ran = LegacyPurge.runIfNeeded(targets(directory: directory, accounts: accounts, recorder: recorder))

        XCTAssertTrue(ran)
        for name in LegacyPurge.legacyFileNames {
            XCTAssertFalse(FileManager.default.fileExists(atPath: directory.appendingPathComponent(name).path), name)
        }
        XCTAssertTrue(FileManager.default.fileExists(atPath: keeper.path))
        XCTAssertNil(defaults.object(forKey: "yansu.chat.compose-drafts"))
        XCTAssertTrue(defaults.bool(forKey: "yansu.push.optIn"))
        XCTAssertEqual(Set(recorder.deleted), ["provider-openai", "hub-old-identity", "agent-service-7"])
        XCTAssertTrue(defaults.bool(forKey: LegacyPurge.flagKey))

        // A second launch does nothing, even if legacy-looking files appear again.
        try Data("again".utf8).write(to: directory.appendingPathComponent("companion.json"))
        let secondRan = LegacyPurge.runIfNeeded(targets(directory: directory, accounts: accounts, recorder: recorder))
        XCTAssertFalse(secondRan)
        XCTAssertTrue(FileManager.default.fileExists(atPath: directory.appendingPathComponent("companion.json").path))
        XCTAssertEqual(recorder.deleted.count, 3)
    }

    func testPurgeIsHarmlessWhenNothingLegacyExists() throws {
        let directory = try makeTemporaryDirectory()
        let recorder = Recorder()
        XCTAssertTrue(LegacyPurge.runIfNeeded(targets(directory: directory, accounts: [], recorder: recorder)))
        XCTAssertTrue(recorder.deleted.isEmpty)
        XCTAssertTrue(defaults.bool(forKey: LegacyPurge.flagKey))
    }
}
