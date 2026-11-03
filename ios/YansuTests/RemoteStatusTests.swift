import Foundation
import XCTest
@testable import Yansu

final class RemoteStatusTests: XCTestCase {
    func testOnlyRemoteOfflineMeansAsleepAndATimeoutIsUnknown() {
        XCTAssertEqual(RemoteStatus.offline(for: RelayClientError.offline, remote: true), false)
        // A slow relay answer says nothing about the Mac: it must never be shown as asleep.
        XCTAssertNil(RemoteStatus.offline(for: RelayClientError.timedOut, remote: true))
        XCTAssertNil(RemoteStatus.offline(for: RelayClientError.offline, remote: false))
        XCTAssertNil(RemoteStatus.offline(for: RelayClientError.unauthorized, remote: true))
    }

    func testRemoteNoticeContextSkipsRelayCredentialsAndDecodesOldContexts() throws {
        let secrets = InMemorySecretStore()
        let context = NoticeRelayContext(origin: "http://127.0.0.1:41237", hubID: "hub",
                                         deviceID: "device", secretID: "generation", remote: true)
        try secrets.save("bearer", account: PairingSecretAccount.deviceBearer(secretID: "generation"))
        try SharedNoticeStore.save(key: Data(repeating: 7, count: 32), context: context, secrets: secrets)

        XCTAssertEqual(SharedNoticeStore.load(secrets: secrets)?.context, context)
        XCTAssertNil(SharedNoticeStore.relayCredentials(secrets: secrets))

        let oldContext = try JSONDecoder().decode(
            NoticeRelayContext.self,
            from: Data(#"{"origin":"https://relay.example","hubID":"hub","deviceID":"device"}"#.utf8)
        )
        XCTAssertFalse(oldContext.remote)
    }
}
