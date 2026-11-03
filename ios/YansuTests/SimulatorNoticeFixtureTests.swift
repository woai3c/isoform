import Foundation
import XCTest
@testable import Yansu

// Seeds the simulator's shared Keychain with a notice key and relay context, so a sealed push can be checked
// with `xcrun simctl push` without a live hub. Runs only when YANSU_NOTICE_FIXTURE_DIR names a folder. It writes
// fixture.json there with the key and identifiers the sealing script needs.
final class SimulatorNoticeFixtureTests: XCTestCase {
    func testSeedsTheSharedKeychainForASimulatorPush() throws {
        guard let folder = ProcessInfo.processInfo.environment["YANSU_NOTICE_FIXTURE_DIR"], !folder.isEmpty else {
            throw XCTSkip("Set YANSU_NOTICE_FIXTURE_DIR to seed the simulator for a sealed push check.")
        }
        let key = Data(repeating: 0x5c, count: 32)
        let context = NoticeRelayContext(origin: "https://relay.invalid", hubID: "hub-sim-fixture",
                                         deviceID: "dev-sim-fixture")
        let secrets = KeychainSecretStore()
        try SharedNoticeStore.save(key: key, context: context, secrets: secrets)
        try secrets.save("bearer-sim-fixture", account: PairingSecretAccount.deviceBearer)

        let manifest: [String: String] = ["key": RelayCrypto.base64URLEncode(key),
                                          "hub": context.hubID, "dev": context.deviceID]
        let data = try JSONSerialization.data(withJSONObject: manifest, options: [.sortedKeys])
        try data.write(to: URL(fileURLWithPath: folder, isDirectory: true).appendingPathComponent("fixture.json"))
    }
}
