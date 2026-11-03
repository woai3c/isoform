import Foundation
import XCTest
@testable import Yansu

// Receipts go to POST /v1/notices/{noticeId}/receipts with the device bearer, and are retried once.
final class NoticeReceiptsTests: XCTestCase {
    private final class Counter: @unchecked Sendable {
        var calls = 0
    }

    private let fixedDate = Date(timeIntervalSince1970: 1_800_000_000)

    private func secrets(bearer: String = "bearer-receipts") throws -> InMemorySecretStore {
        let secrets = InMemorySecretStore()
        try SharedNoticeStore.save(key: Data(repeating: 0x21, count: 32),
                                   context: NoticeRelayContext(origin: "https://relay.example.test",
                                                               hubID: "hub-r", deviceID: "dev-r"),
                                   secrets: secrets)
        try secrets.save(bearer, account: PairingSecretAccount.deviceBearer)
        return secrets
    }

    private func receipts(_ secrets: SecretStore, attempts: Int = 2) -> NoticeReceipts {
        NoticeReceipts(relay: RelayClient(session: StubURLProtocol.session()), secrets: secrets,
                       attempts: attempts, retryDelayNanoseconds: 0, now: { self.fixedDate })
    }

    private func okResponse(_ request: URLRequest) -> (HTTPURLResponse, Data) {
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!
        return (response, Data(#"{"ok":true,"data":{}}"#.utf8))
    }

    func testPostsTheReceiptWithTheDeviceBearerAndTheProtocolBody() async throws {
        StubURLProtocol.install { request, _ in self.okResponse(request) }

        let sent = await receipts(try secrets()).post(noticeID: "notice-r1", state: .acted, action: "later")

        XCTAssertTrue(sent)
        let (request, body) = try XCTUnwrap(StubURLProtocol.requests.last)
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.absoluteString, "https://relay.example.test/v1/notices/notice-r1/receipts")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer bearer-receipts")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
        XCTAssertNil(request.value(forHTTPHeaderField: "Origin"))
        let json = try JSONSerialization.jsonObject(with: try XCTUnwrap(body)) as? [String: String]
        XCTAssertEqual(json?["state"], "acted")
        XCTAssertEqual(json?["action"], "later")
        XCTAssertEqual(json?["at"], "2027-01-15T08:00:00Z")
    }

    func testDeliveredReceiptHasAnEmptyActionAndTheSameRoute() async throws {
        StubURLProtocol.install { request, _ in self.okResponse(request) }

        _ = await receipts(try secrets()).post(noticeID: "notice-r2", state: .delivered)

        let (_, body) = try XCTUnwrap(StubURLProtocol.requests.last)
        let json = try JSONSerialization.jsonObject(with: try XCTUnwrap(body)) as? [String: String]
        XCTAssertEqual(json?["state"], "delivered")
        XCTAssertEqual(json?["action"], "")
    }

    func testARejectedReceiptIsRetriedOnceAndThenSucceeds() async throws {
        let counter = Counter()
        StubURLProtocol.install { request, _ in
            counter.calls += 1
            if counter.calls == 1 {
                let response = HTTPURLResponse(url: request.url!, statusCode: 500, httpVersion: nil, headerFields: nil)!
                return (response, Data(#"{"ok":false,"error":"busy"}"#.utf8))
            }
            return self.okResponse(request)
        }

        let sent = await receipts(try secrets()).post(noticeID: "notice-r3", state: .opened)

        XCTAssertTrue(sent)
        XCTAssertEqual(counter.calls, 2)
    }

    func testAReceiptThatFailsTwiceIsDroppedWithoutThrowing() async throws {
        let counter = Counter()
        StubURLProtocol.install { request, _ in
            counter.calls += 1
            let response = HTTPURLResponse(url: request.url!, statusCode: 503, httpVersion: nil, headerFields: nil)!
            return (response, Data(#"{"ok":false,"error":"down"}"#.utf8))
        }

        let sent = await receipts(try secrets()).post(noticeID: "notice-r4", state: .dismissed)

        XCTAssertFalse(sent)
        XCTAssertEqual(counter.calls, 2)
    }

    func testNoRequestIsSentForAnUnsafeIdOrWithoutCredentials() async throws {
        StubURLProtocol.install { request, _ in self.okResponse(request) }
        let before = StubURLProtocol.requests.count

        let unsafe = await receipts(try secrets()).post(noticeID: "a/b", state: .opened)
        let withoutBearer = InMemorySecretStore()
        let unpaired = await receipts(withoutBearer).post(noticeID: "notice-r5", state: .opened)

        XCTAssertFalse(unsafe)
        XCTAssertFalse(unpaired)
        XCTAssertEqual(StubURLProtocol.requests.count, before)
    }
}
