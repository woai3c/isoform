import Foundation
import XCTest
@testable import Yansu

// Stubbed HTTP for the relay client. A test installs a handler; every request is recorded with its body.
final class StubURLProtocol: URLProtocol {
    typealias Handler = (URLRequest, Data?) throws -> (HTTPURLResponse, Data)

    private static let lock = NSLock()
    private static var handler: Handler?
    private static var recorded: [(URLRequest, Data?)] = []

    static func install(_ newHandler: @escaping Handler) {
        lock.lock()
        defer { lock.unlock() }
        handler = newHandler
        recorded = []
    }

    static var requests: [(URLRequest, Data?)] {
        lock.lock()
        defer { lock.unlock() }
        return recorded
    }

    static func session() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubURLProtocol.self]
        return URLSession(configuration: configuration)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let body = Self.bodyData(of: request)
        Self.lock.lock()
        Self.recorded.append((request, body))
        let current = Self.handler
        Self.lock.unlock()
        do {
            guard let current else { throw URLError(.badServerResponse) }
            let (response, data) = try current(request, body)
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}

    private static func bodyData(of request: URLRequest) -> Data? {
        if let data = request.httpBody { return data }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }
            data.append(buffer, count: count)
        }
        return data
    }
}

final class RelayClientTests: XCTestCase {
    private let origin = URL(string: "https://relay.example")!
    private let bearer = RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32))

    private var credentials: RelayCredentials {
        RelayCredentials(origin: origin, bearer: bearer)
    }

    private func respond(_ status: Int, json: String = "{\"ok\":false}", headers: [String: String] = [:]) {
        StubURLProtocol.install { request, _ in
            let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1",
                                           headerFields: headers)!
            return (response, Data(json.utf8))
        }
    }

    private func envelope(seq: UInt64 = 1) -> RelayEnvelope {
        RelayEnvelope(v: 1, hub: "11111111-2222-4333-8444-555555555555", dev: "99999999-aaaa-4bbb-8ccc-dddddddddddd",
                      dir: "d2h", seq: seq, kind: "req", ct: "AAAA")
    }

    private func client() -> RelayClient {
        RelayClient(session: StubURLProtocol.session())
    }

    func testAttachmentReturnsTheRawBodyAndMapsErrors() async throws {
        let bytes = Data((0..<64).map { UInt8($0) })
        StubURLProtocol.install { request, _ in
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1",
                                           headerFields: ["Content-Type": "application/octet-stream"])!
            return (response, bytes)
        }
        let id = "7a4f7d3e-1b2c-4d5e-8f90-123456789abc"
        let got = try await client().attachment(credentials, id: id)
        XCTAssertEqual(got, bytes)
        let (request, _) = try XCTUnwrap(StubURLProtocol.requests.last)
        XCTAssertEqual(request.httpMethod, "GET")
        XCTAssertEqual(request.url?.path, "/v1/attachments/\(id)")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer \(bearer)")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Accept"), "application/octet-stream")

        respond(404)
        do {
            _ = try await client().attachment(credentials, id: id)
            XCTFail("a missing attachment should throw")
        } catch let error as RelayClientError {
            XCTAssertEqual(error, .notFound)
        }
        respond(403)
        do {
            _ = try await client().attachment(credentials, id: id)
            XCTFail("a device without brief:read should throw")
        } catch let error as RelayClientError {
            XCTAssertEqual(error, .forbidden)
        }
    }

    func testTheBearerIsTheExact43CharacterBase64URLText() {
        XCTAssertEqual(bearer.count, 43)
        XCTAssertFalse(bearer.contains("="))
    }

    func testClaimStatusCodesSurfaceAsTheirOwnErrors() async throws {
        let body = PairingClaimBody(hub: "h", claimToken: "t", deviceId: "d", devicePub: "p", ct: "c")
        let expectations: [(Int, RelayClientError)] = [
            (403, .forbidden), (404, .notFound), (409, .conflict), (410, .gone),
            (413, .payloadTooLarge), (415, .unsupportedMediaType), (503, .unavailable),
        ]
        for (status, expected) in expectations {
            respond(status)
            do {
                try await client().claimPairing(origin: origin, pairingID: "p1", body: body)
                XCTFail("status \(status) should throw")
            } catch let error as RelayClientError {
                XCTAssertEqual(error, expected, "status \(status)")
            }
        }
    }

    func testClaimPostsJSONWithoutAnOriginHeader() async throws {
        respond(202, json: "{\"ok\":true,\"data\":{\"state\":\"claimed\"}}")
        let body = PairingClaimBody(hub: "h", claimToken: "t", deviceId: "d", devicePub: "p", ct: "c")
        try await client().claimPairing(origin: origin, pairingID: "p1", body: body)
        let (request, sent) = try XCTUnwrap(StubURLProtocol.requests.last)
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
        XCTAssertNil(request.value(forHTTPHeaderField: "Origin"))
        XCTAssertEqual(try JSONDecoder().decode(PairingClaimBody.self, from: try XCTUnwrap(sent)), body)
    }

    // The relay reads the claim token from X-Yansu-Claim (PROTOCOL.md section 5). Relay headers keep the yansu
    // name: a rename of this header made every pairing poll answer 401 while the Mac still saw the claim.
    func testPairingPollSendsTheClaimTokenInXYansuClaim() async throws {
        respond(200, json: "{\"ok\":true,\"data\":{\"state\":\"claimed\"}}")
        _ = try await client().pollPairing(origin: origin, pairingID: "p1", claimToken: "claim-token")
        let (request, _) = try XCTUnwrap(StubURLProtocol.requests.last)
        XCTAssertEqual(request.httpMethod, "GET")
        XCTAssertEqual(request.url?.path, "/v1/pairings/p1")
        XCTAssertEqual(request.value(forHTTPHeaderField: "X-Yansu-Claim"), "claim-token")
    }

    func testRateLimitCarriesRetryAfterSeconds() async throws {
        respond(429, headers: ["Retry-After": "60"])
        do {
            try await client().sendRequest(credentials, envelope: envelope())
            XCTFail("expected a rate limit")
        } catch {
            XCTAssertEqual(error as? RelayClientError, .rateLimited(retryAfter: 60))
        }
    }

    func testBadRequestKeepsTheRelayMessage() async throws {
        respond(400, json: "{\"ok\":false,\"error\":\"invalid envelope\"}")
        do {
            try await client().sendRequest(credentials, envelope: envelope())
            XCTFail("expected a bad request")
        } catch {
            XCTAssertEqual(error as? RelayClientError, .badRequest(message: "invalid envelope"))
        }
    }

    func testPushTokenStatusCodesAreTyped() async throws {
        for (status, expected) in [(400, RelayClientError.badRequest(message: "bad token")), (409, .conflict), (503, .unavailable)] {
            respond(status, json: "{\"ok\":false,\"error\":\"bad token\"}")
            do {
                _ = try await client().registerPushToken(credentials, deviceToken: String(repeating: "a", count: 64),
                                                         environment: "development")
                XCTFail("status \(status) should throw")
            } catch let error as RelayClientError {
                if case .badRequest = expected {
                    guard case .badRequest = error else { return XCTFail("status \(status): \(error)") }
                } else {
                    XCTAssertEqual(error, expected, "status \(status)")
                }
            }
        }
    }

    func testResponsePollWaitIsCappedAtTwentyFiveSeconds() async throws {
        respond(200, json: "{\"ok\":true,\"data\":{\"envelopes\":[]}}")
        _ = try await client().pollResponses(credentials, after: 41, wait: 99)
        let (request, _) = try XCTUnwrap(StubURLProtocol.requests.last)
        let items = URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)?.queryItems ?? []
        XCTAssertEqual(items.first { $0.name == "wait" }?.value, "25")
        XCTAssertEqual(items.first { $0.name == "after" }?.value, "41")
    }

    func testBriefListLimitIsKeptWithinOneToSeven() async throws {
        respond(200, json: "{\"ok\":true,\"data\":{\"briefs\":[]}}")
        _ = try await client().briefs(credentials, limit: 99)
        let high = try XCTUnwrap(URLComponents(url: try XCTUnwrap(StubURLProtocol.requests.last?.0.url),
                                               resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "limit" }?.value)
        XCTAssertEqual(high, "7")
        _ = try await client().briefs(credentials, limit: 0)
        let low = try XCTUnwrap(URLComponents(url: try XCTUnwrap(StubURLProtocol.requests.last?.0.url),
                                              resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "limit" }?.value)
        XCTAssertEqual(low, "1")
    }

    func testUnpairNeedsTheUnpairedReceipt() async throws {
        respond(200, json: "{\"ok\":true,\"data\":{\"unpaired\":true}}")
        try await client().unpair(credentials)

        respond(200, json: "{\"ok\":true,\"data\":{\"unpaired\":false}}")
        do {
            try await client().unpair(credentials)
            XCTFail("an unpair receipt without unpaired:true must not count as success")
        } catch {
            XCTAssertEqual(error as? RelayClientError, .invalidResponse)
        }
    }

    func testSuccessWithoutOKTrueIsAnInvalidResponse() async throws {
        respond(200, json: "{\"ok\":false}")
        do {
            _ = try await client().presence(credentials)
            XCTFail("expected an invalid response")
        } catch {
            XCTAssertEqual(error as? RelayClientError, .invalidResponse)
        }
    }

    // MARK: Hub round trip with a rejected seq

    // A 409 on the first send must lead to a retry with a new seq, and the reply must still match.
    @MainActor
    func testHubRequestRetriesAConflictingSeqWithANewOneAndMatchesTheReply() async throws {
        let hub = RelayCrypto.makeDeviceKeyPair()
        let device = RelayCrypto.makeDeviceKeyPair()
        let hubID = "11111111-2222-4333-8444-555555555555"
        let deviceID = "99999999-aaaa-4bbb-8ccc-dddddddddddd"
        let keys = try RelayCrypto.sessionKeys(devicePrivate: device.privateKey, hubPublic: hub.publicKey,
                                               hubID: hubID, deviceID: deviceID)
        let vault = PairingVault(directory: try makeTemporaryDirectory(), secrets: InMemorySecretStore())
        let bearerText = RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32))
        try vault.store(PairedDevice(hubID: hubID, hubName: "Test Mac",
                                     hubPublicKey: RelayCrypto.base64URLEncode(hub.publicKey),
                                     relayOrigin: "https://relay.example", deviceID: deviceID,
                                     deviceName: "Test iPhone", scopes: ["brief:read"],
                                     pairedAt: Date(), expiresAt: nil, counters: SequenceCounters()),
                        devicePrivateKey: RelayCrypto.base64URLEncode(device.privateKey), bearer: bearerText)

        // Hub side: refuse the first request with 409, accept the retry, then answer it.
        final class Recorder: @unchecked Sendable {
            var sentSeqs: [UInt64] = []
            var requestID: String?
            var answered = false
        }
        let recorder = Recorder()
        let acknowledged = expectation(description: "The accepted response is acknowledged")
        StubURLProtocol.install { request, body in
            func json(_ status: Int, _ text: String) -> (HTTPURLResponse, Data) {
                (HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: nil)!,
                 Data(text.utf8))
            }
            let path = request.url?.path ?? ""
            if path == "/v1/requests" {
                let sent = try JSONDecoder().decode(RelayEnvelope.self, from: try XCTUnwrap(body))
                recorder.sentSeqs.append(sent.seq)
                if recorder.sentSeqs.count == 1 { return json(409, "{\"ok\":false,\"error\":\"seq conflict\"}") }
                let plaintext = try RelayCrypto.openEnvelope(try RelayCrypto.base64URLDecode(sent.ct), key: keys.d2h,
                                                             hub: hubID, dev: deviceID, dir: "d2h",
                                                             seq: sent.seq, kind: "req")
                let object = try JSONSerialization.jsonObject(with: plaintext) as? [String: Any]
                recorder.requestID = object?["id"] as? String
                return json(202, "{\"ok\":true,\"data\":{\"accepted\":true}}")
            }
            if path == "/v1/responses" {
                guard let id = recorder.requestID, !recorder.answered else {
                    return json(200, "{\"ok\":true,\"data\":{\"envelopes\":[]}}")
                }
                recorder.answered = true
                let innerBody = "{\"ok\":true,\"data\":{\"hubName\":\"Test Mac\",\"narrativeAvailable\":true}}"
                let reply = try JSONSerialization.data(withJSONObject: ["id": id, "status": 200, "body": innerBody])
                let ct = try RelayCrypto.sealEnvelope(reply, key: keys.h2d, hub: hubID, dev: deviceID,
                                                      dir: "h2d", seq: 5, kind: "res")
                let envelope = RelayEnvelope(v: 1, hub: hubID, dev: deviceID, dir: "h2d", seq: 5, kind: "res",
                                             ct: RelayCrypto.base64URLEncode(ct))
                let envelopeJSON = String(decoding: try JSONEncoder().encode(envelope), as: UTF8.self)
                return json(200, "{\"ok\":true,\"data\":{\"envelopes\":[\(envelopeJSON)]}}")
            }
            acknowledged.fulfill()
            return json(200, "{\"ok\":true,\"data\":{\"upTo\":5}}")
        }

        let channel = HubChannel(vault: vault, relay: RelayClient(session: StubURLProtocol.session()))
        let status = try await channel.status()

        XCTAssertEqual(status.hubName, "Test Mac")
        XCTAssertEqual(recorder.sentSeqs.count, 2)
        XCTAssertNotEqual(recorder.sentSeqs[0], recorder.sentSeqs[1], "the retry must not reuse the rejected seq")
        XCTAssertEqual(vault.lastAcceptedMessage(), 5)
        await fulfillment(of: [acknowledged], timeout: 2)
        await channel.prepareForPairingChange()
    }
}
