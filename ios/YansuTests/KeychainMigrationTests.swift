import Foundation
import Security
import XCTest
@testable import Yansu

// The one-time move of Isoform's Keychain items into the shared access group. Runs against an in-memory backend.
final class KeychainMigrationTests: XCTestCase {
    private let service = KeychainSecretStore.service
    private let legacyGroup = "TEAMID.com.isoform.companion"
    private let sharedGroup = "TEAMID.com.isoform.companion.shared"
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUpWithError() throws {
        try super.setUpWithError()
        suiteName = "yansu.tests.keychain.\(UUID().uuidString)"
        defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
    }

    override func tearDownWithError() throws {
        defaults.removePersistentDomain(forName: suiteName)
        try super.tearDownWithError()
    }

    func testLegacyItemsMoveIntoTheSharedGroupAndTheOriginalsAreRemoved() {
        let backend = InMemoryKeychainBackend()
        backend.seed(service: service, accessGroup: legacyGroup, account: PairingSecretAccount.devicePrivateKey, value: "private")
        backend.seed(service: service, accessGroup: legacyGroup, account: PairingSecretAccount.deviceBearer, value: "bearer")

        let ran = KeychainMigration.runIfNeeded(backend: backend, service: service, sharedGroup: sharedGroup, defaults: defaults)

        XCTAssertTrue(ran)
        XCTAssertEqual(try backend.value(service: service, account: PairingSecretAccount.devicePrivateKey,
                                         accessGroup: sharedGroup), "private")
        XCTAssertEqual(try backend.value(service: service, account: PairingSecretAccount.deviceBearer,
                                         accessGroup: sharedGroup), "bearer")
        XCTAssertNil(try backend.value(service: service, account: PairingSecretAccount.deviceBearer,
                                       accessGroup: legacyGroup))
        XCTAssertTrue(defaults.bool(forKey: KeychainMigration.flagKey))
    }

    func testAnItemAlreadyInTheSharedGroupKeepsItsValue() {
        let backend = InMemoryKeychainBackend()
        backend.seed(service: service, accessGroup: sharedGroup, account: PairingSecretAccount.deviceBearer, value: "current")
        backend.seed(service: service, accessGroup: legacyGroup, account: PairingSecretAccount.deviceBearer, value: "stale")

        KeychainMigration.runIfNeeded(backend: backend, service: service, sharedGroup: sharedGroup, defaults: defaults)

        XCTAssertEqual(try backend.value(service: service, account: PairingSecretAccount.deviceBearer,
                                         accessGroup: sharedGroup), "current")
        XCTAssertNil(try backend.value(service: service, account: PairingSecretAccount.deviceBearer,
                                       accessGroup: legacyGroup))
    }

    func testTheMigrationRunsOnlyOnce() {
        let backend = InMemoryKeychainBackend()
        KeychainMigration.runIfNeeded(backend: backend, service: service, sharedGroup: sharedGroup, defaults: defaults)
        backend.seed(service: service, accessGroup: legacyGroup, account: PairingSecretAccount.deviceBearer, value: "later")

        let secondRun = KeychainMigration.runIfNeeded(backend: backend, service: service, sharedGroup: sharedGroup,
                                                      defaults: defaults)

        XCTAssertFalse(secondRun)
        XCTAssertEqual(try backend.value(service: service, account: PairingSecretAccount.deviceBearer,
                                         accessGroup: legacyGroup), "later")
    }

    func testAFailedWriteKeepsTheLegacyItemAndLeavesTheMigrationOpen() {
        let backend = InMemoryKeychainBackend()
        backend.seed(service: service, accessGroup: legacyGroup, account: PairingSecretAccount.deviceBearer, value: "bearer")
        backend.failWrites = true

        let ran = KeychainMigration.runIfNeeded(backend: backend, service: service, sharedGroup: sharedGroup, defaults: defaults)

        XCTAssertFalse(ran)
        XCTAssertEqual(try backend.value(service: service, account: PairingSecretAccount.deviceBearer,
                                         accessGroup: legacyGroup), "bearer")
        XCTAssertFalse(defaults.bool(forKey: KeychainMigration.flagKey))
    }

    func testWithoutASharedGroupNothingMovesAndNothingIsMarkedDone() {
        let backend = InMemoryKeychainBackend()
        backend.seed(service: service, accessGroup: legacyGroup, account: PairingSecretAccount.deviceBearer, value: "bearer")

        let ran = KeychainMigration.runIfNeeded(backend: backend, service: service, sharedGroup: nil, defaults: defaults)

        XCTAssertFalse(ran)
        XCTAssertEqual(backend.snapshot().count, 1)
        XCTAssertFalse(defaults.bool(forKey: KeychainMigration.flagKey))
    }

    func testOtherServicesAreLeftAlone() {
        let backend = InMemoryKeychainBackend()
        backend.seed(service: "com.example.other", accessGroup: legacyGroup, account: "keep", value: "x")

        KeychainMigration.runIfNeeded(backend: backend, service: service, sharedGroup: sharedGroup, defaults: defaults)

        XCTAssertEqual(try backend.value(service: "com.example.other", account: "keep", accessGroup: legacyGroup), "x")
    }

    func testOnlyTheDevicePrivateKeyStaysWhenUnlockedAndTheRestOpenAfterFirstUnlock() {
        XCTAssertEqual(KeychainAccessibility.forAccount(PairingSecretAccount.devicePrivateKey) as String,
                       kSecAttrAccessibleWhenUnlockedThisDeviceOnly as String)
        XCTAssertEqual(KeychainAccessibility.forAccount(PairingSecretAccount.deviceBearer) as String,
                       kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String)
        XCTAssertEqual(KeychainAccessibility.forAccount(SharedNoticeAccount.key) as String,
                       kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String)
        XCTAssertEqual(KeychainAccessibility.forAccount(SharedNoticeAccount.context) as String,
                       kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String)
    }

    func testSecretStoreWritesThroughTheConfiguredGroup() throws {
        let backend = InMemoryKeychainBackend()
        let store = KeychainSecretStore(service: service, accessGroup: sharedGroup, backend: backend)

        try store.save("value-1", account: PairingSecretAccount.deviceBearer)

        XCTAssertEqual(try store.read(PairingSecretAccount.deviceBearer), "value-1")
        XCTAssertEqual(try backend.value(service: service, account: PairingSecretAccount.deviceBearer,
                                         accessGroup: sharedGroup), "value-1")
        try store.delete(PairingSecretAccount.deviceBearer)
        XCTAssertNil(try store.read(PairingSecretAccount.deviceBearer))
    }
}
