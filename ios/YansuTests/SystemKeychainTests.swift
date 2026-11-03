import Foundation
import Security
import XCTest
@testable import Yansu

// Runs against the real Keychain of the test host. The shared access group only works when the build carries
// the keychain-access-groups entitlement, so this test is the check that signing put it there.
final class SystemKeychainTests: XCTestCase {
    func testGeneratedPrivateKeyKeepsTheSameAccessibilityAsTheLegacyAccount() {
        XCTAssertEqual(KeychainAccessibility.forAccount(PairingSecretAccount.devicePrivateKey(secretID: "new")) as String,
                       kSecAttrAccessibleWhenUnlockedThisDeviceOnly as String)
        XCTAssertEqual(KeychainAccessibility.forAccount(PairingSecretAccount.deviceBearer(secretID: "new")) as String,
                       kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String)
    }

    func testSharedGroupRoundTripThroughTheSystemKeychain() throws {
        let store = KeychainSecretStore()
        let account = "test-round-trip-\(UUID().uuidString.lowercased())"
        defer { try? store.delete(account) }

        try store.save("round-trip", account: account)

        XCTAssertEqual(try store.read(account), "round-trip")
        XCTAssertTrue(try store.accountNames().contains(account))
    }

    func testAccountEnumerationIsScopedToTheConfiguredServiceAndAccessGroup() throws {
        let backend = InMemoryKeychainBackend()
        backend.seed(service: "test-pairing", accessGroup: "shared", account: "current", value: "value")
        backend.seed(service: "test-pairing", accessGroup: "other", account: "other-group", value: "value")
        backend.seed(service: "other-service", accessGroup: "shared", account: "other-service", value: "value")
        let store = KeychainSecretStore(service: "test-pairing", accessGroup: "shared", backend: backend)

        XCTAssertEqual(try store.accountNames(), ["current"])
    }
}
