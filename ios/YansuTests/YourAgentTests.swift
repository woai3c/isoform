import XCTest
@testable import Yansu

final class YourAgentTests: XCTestCase {
    func testNameAndEmailRules() {
        for value in ["alex.rivera", "jose.nunez", "alexrivera", "a.rivera", "abc", "a.b", String(repeating: "a", count: 30)] {
            XCTAssertTrue(YourAgentRules.validName(value), value)
        }
        for value in ["", "ab", ".abc", "abc.", "a..b", "Alex", "a_b", "中文", "abc\n", String(repeating: "a", count: 31)] + Array(YourAgentRules.reserved) {
            XCTAssertFalse(YourAgentRules.validName(value), value)
        }
        XCTAssertEqual(YourAgentRules.normalize(" ALEX.RIVERA \n"), "alex.rivera")
        for value in ["a@example.com", "a+b@c.d", String(repeating: "a", count: 242) + "@example.com"] {
            XCTAssertTrue(YourAgentRules.validEmail(value))
        }
        for value in ["", "a@b", "a@@example.com", "a b@c.d", "a@example.com\n", String(repeating: "a", count: 243) + "@example.com"] {
            XCTAssertFalse(YourAgentRules.validEmail(value))
        }
    }

    func testHubRouteAndLenientCopy() throws {
        XCTAssertEqual(HubRoute.yourAgent.path, "/your-agent")
        XCTAssertEqual(HubRoute.yourAgent.method, "GET")
        XCTAssertEqual(HubRoute.yourAgent.traceName, "your-agent")
        XCTAssertEqual(HubRoute.yourAgent.body, "")
        XCTAssertEqual(HubRoute.yourAgent.query, "")
        let value = try HubResponse(status: 200, body: #"{"ok":true,"data":{"hub":"hub","token":"secret"}}"#).payload(YourAgentCopy.self)
        XCTAssertEqual(value.specialties, [])
        XCTAssertEqual(value.taste, [])
        XCTAssertEqual(value.nameCandidates, [])
        XCTAssertFalse(value.stillLearning)
    }

    func testClientRequests() async throws {
        let server = AgentProtocolState()
        let client = makeClient(server)
        var copy = YourAgentCopy(hub: "hub +/", token: "secret")
        copy.nameCandidates = ["alex.rivera", "", "alexrivera", "a.rivera"]
        server.respond(200, #"{"name":"alex","available":true}"#)
        let check = try await client.check(name: " ALEX ", copy: copy)
        XCTAssertTrue(check.available)
        var request = try XCTUnwrap(server.requests.last)
        XCTAssertEqual(request.url?.path, "/api/agents/check")
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
        XCTAssertEqual(request.timeoutInterval, 10)
        let candidates = ["alex.rivera", "alexrivera", "a.rivera", "alex"]
        XCTAssertEqual(try body(request) as NSDictionary, ["name": "alex", "hub": copy.hub, "token": "secret", "candidates": candidates] as NSDictionary)
        server.respond(200, Self.reservedJSON)
        _ = try await client.reserve(name: " ALEX ", email: " ME@EXAMPLE.COM ", copy: copy)
        request = try XCTUnwrap(server.requests.last)
        XCTAssertEqual(request.url?.path, "/api/agents/reserve")
        XCTAssertEqual(try body(request) as NSDictionary, ["name": "alex", "email": "me@example.com", "hub": copy.hub,
            "token": "secret", "lang": "zh-CN", "client": "iphone", "candidates": candidates] as NSDictionary)
        let reservation = try await client.reservation(copy: copy)
        XCTAssertEqual(reservation?.name, "alex")
        request = try XCTUnwrap(server.requests.last)
        XCTAssertEqual(request.httpMethod, "GET")
        XCTAssertNil(request.httpBody)
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer secret")
        XCTAssertEqual(URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems, [URLQueryItem(name: "hub", value: copy.hub)])
        XCTAssertEqual(AgentsClient(language: "en-US").language, "en")
        server.respond(200, #"{"name":"alex","available":true}"#)
        _ = try await client.check(name: " ALEX ", copy: YourAgentCopy(hub: "hub", token: "secret"))
        XCTAssertEqual(try body(XCTUnwrap(server.requests.last))["candidates"] as? [String], ["alex"])
    }

    func testClientErrorsAndUnreachable() async throws {
        let server = AgentProtocolState(); let client = makeClient(server)
        let copy = YourAgentCopy(hub: "hub", token: "secret")
        let cases: [(Int, String, AgentsError)] = [
            (409, #"{"error":"taken","suggestion":"alex.rivera"}"#, .taken(suggestion: "alex.rivera")),
            (422, #"{"error":"not_allowed"}"#, .notAllowed),
            (422, #"{"error":"invalid_name"}"#, .invalidName),
            (422, #"{"error":"invalid_email"}"#, .invalidEmail),
            (403, #"{"error":"token"}"#, .token), (429, #"{"error":"rate"}"#, .rate),
            (500, #"{"error":"token"}"#, .failed), (200, "<html>oops</html>", .unreachable),
            (201, Self.reservedJSON, .unreachable), (422, #"{"error":"other"}"#, .failed),
            (400, #"{"error":"invalid_hub"}"#, .failed), (413, #"{"error":"body_too_large"}"#, .failed),
            (403, "not json", .unreachable)]
        for (status, json, expected) in cases {
            server.respond(status, json)
            do { _ = try await client.reserve(name: "alex", email: "a@example.com", copy: copy); XCTFail("Expected \(expected)") }
            catch { XCTAssertEqual(error as? AgentsError, expected) }
        }
        server.respond(404, #"{"error":"none"}"#)
        let none = try await client.reservation(copy: copy)
        XCTAssertNil(none)
        server.respond(200, #"{"name":"alex","available":false,"reason":"taken","suggestion":"alex.rivera"}"#)
        let taken = try await client.check(name: "alex", copy: copy)
        XCTAssertEqual(taken.suggestion, "alex.rivera")
        for reason in ["not_allowed", "invalid"] {
            server.respond(200, "{\"name\":\"alex\",\"available\":false,\"reason\":\"\(reason)\"}")
            let result = try await client.check(name: "alex", copy: copy)
            XCTAssertEqual(result.reason, reason)
        }
        server.respond(200, #"{"name":"alex","available":false}"#)
        do { _ = try await client.check(name: "alex", copy: copy); XCTFail() }
        catch { XCTAssertEqual(error as? AgentsError, .unreachable) }
        server.error = URLError(.notConnectedToInternet)
        do { _ = try await client.reservation(copy: copy); XCTFail() }
        catch { XCTAssertEqual(error as? AgentsError, .unreachable) }
    }

    @MainActor
    func testStoreReserveOutcomesChangeAndOffline() async throws {
        let server = AgentProtocolState(); let directory = testDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = makeStore(server, directory: directory)
        XCTAssertNil(store.copy); XCTAssertFalse(store.canReserve)
        store.apply(YourAgentCopy(hub: "hub", token: "secret"))
        store.name = "alex"; store.email = "wrong"; store.emailDidLoseFocus()
        XCTAssertTrue(store.emailInvalid); XCTAssertEqual(store.status, .clear); XCTAssertFalse(store.canReserve)
        store.email = "a@example.com"; store.emailDidLoseFocus()
        XCTAssertTrue(store.canReserve)
        for (code, json, status) in [(409, #"{"error":"taken","suggestion":"alex.rivera"}"#, YourAgentStore.Status.taken("alex.rivera")),
            (422, #"{"error":"not_allowed"}"#, .notAllowed), (422, #"{"error":"invalid_name"}"#, .notAllowed),
            (422, #"{"error":"invalid_email"}"#, .clear), (403, #"{"error":"token"}"#, .failed),
            (400, #"{"error":"invalid_hub"}"#, .failed)] {
            store.email = "a@example.com"
            store.name = "alex"; server.respond(code, json)
            await store.reserve()
            XCTAssertEqual(store.status, status)
            XCTAssertEqual(store.emailInvalid, json.contains("invalid_email"))
            XCTAssertTrue(store.reachable)
            XCTAssertFalse(store.inFlight)
        }
        store.name = "alex"; server.respond(200, Self.reservedJSON)
        await store.reserve()
        XCTAssertEqual(store.reservation?.name, "alex")
        store.change()
        XCTAssertNil(store.reservation); XCTAssertEqual(store.name, "alex"); XCTAssertEqual(store.email, "a@example.com")
        server.respond(502, "Bad gateway"); await store.reserve()
        XCTAssertFalse(store.reachable); XCTAssertFalse(store.canReserve)
        store.setReachable(true); XCTAssertTrue(store.canReserve)
        store.setReachable(false); XCTAssertFalse(store.canReserve)
        XCTAssertTrue(store.clearAfterUnpair())
        XCTAssertNil(store.copy); XCTAssertEqual(store.name, ""); XCTAssertEqual(store.email, "")
    }

    @MainActor
    func testLeaveFieldSuggestionLocalRejectionAndStaleCheck() async throws {
        let server = AgentProtocolState(); let directory = testDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = makeStore(server, directory: directory)
        var copy = YourAgentCopy(hub: "hub", token: "secret")
        copy.nameCandidates = ["alex.rivera"]
        store.apply(copy)
        XCTAssertEqual(store.name, ""); XCTAssertEqual(store.email, "")
        server.respond(200, #"{"name":"alex","available":false,"reason":"taken","suggestion":"alex.rivera"}"#)
        store.name = "ale"; store.name = "alex"
        try await Task.sleep(nanoseconds: 450_000_000)
        XCTAssertTrue(server.requests.isEmpty)
        store.nameDidLoseFocus()
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertEqual(server.requests.count, 1); XCTAssertEqual(store.status, .taken("alex.rivera"))
        server.respond(200, #"{"name":"alex.rivera","available":true}"#)
        store.useSuggestion(); XCTAssertEqual(store.name, "alex.rivera")
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertEqual(store.status, .clear)
        store.email = "wrong"; store.emailDidLoseFocus()
        XCTAssertTrue(store.emailInvalid)
        store.name = "admin"; XCTAssertEqual(store.status, .clear)
        store.nameDidLoseFocus(); XCTAssertEqual(store.status, .notAllowed)
        XCTAssertTrue(store.emailInvalid)
        store.name = ".."; XCTAssertEqual(store.status, .clear)
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(server.requests.count, 2)

        let held = HeldAgentChecks()
        let stale = YourAgentStore(hub: AgentTestHub(), api: held,
            cache: YourAgentCache(directory: directory, secrets: InMemorySecretStore()),
            hubID: { "hub" }, canRead: { true }, monitorNetwork: false)
        stale.apply(YourAgentCopy(hub: "hub", token: "secret"))
        stale.name = "older"
        stale.nameDidLoseFocus()
        await held.waitForCheck()
        stale.name = "newer"
        stale.nameDidLoseFocus()
        await held.waitForCheck()
        await held.finish("newer", available: true)
        await held.finish("older", available: false)
        await Task.yield()
        XCTAssertEqual(stale.status, .clear)
    }

    @MainActor
    func testCacheScopeOpenAndClear() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore(); let cache = YourAgentCache(directory: directory, secrets: secrets)
        var copy = YourAgentCopy(hub: "hub", token: "top-secret")
        copy.reservation = AgentReservation(name: "alex", email: "a@example.com", reservedAt: "2026-10-27T20:00:00Z")
        try cache.save(copy)
        XCTAssertFalse(try String(contentsOf: directory.appendingPathComponent("copy.json"), encoding: .utf8).contains("top-secret"))
        XCTAssertEqual(try cache.load(hubID: "hub"), copy)
        let server = AgentProtocolState(); server.respond(503, "no server")
        let store = YourAgentStore(hub: AgentTestHub(), api: makeClient(server), cache: cache,
            hubID: { "hub" }, canRead: { false }, monitorNetwork: false)
        await store.open()
        XCTAssertEqual(store.copy?.hub, "hub"); XCTAssertEqual(store.reservation, copy.reservation)
        XCTAssertFalse(store.reachable)
        server.respond(404, #"{"error":"none"}"#)
        await store.open(); XCTAssertNil(store.reservation)
        XCTAssertTrue(store.reachable)
        XCTAssertNil(try cache.load(hubID: "another"))
        XCTAssertTrue(secrets.accounts.isEmpty)
        XCTAssertFalse(FileManager.default.fileExists(atPath: directory.appendingPathComponent("copy.json").path))
        try cache.save(copy)
        XCTAssertTrue(store.clearAfterUnpair()); XCTAssertTrue(secrets.accounts.isEmpty)
        XCTAssertNil(try cache.load(hubID: "hub"))
    }

    private static let reservedJSON = #"{"name":"alex","email":"a@example.com","reservedAt":"2026-10-27T20:00:00Z"}"#

    func testReservationDecodingAndCacheRoundTrip() throws {
        let decoder = JSONDecoder()
        let old = try decoder.decode(AgentReservation.self, from: Data(Self.reservedJSON.utf8))
        XCTAssertNil(old.status); XCTAssertEqual(old.state, .verified); XCTAssertNil(old.place)
        let json = #"{"name":"alex","email":"a@example.com","reservedAt":"2026-10-27T20:00:00Z","status":"choice","expiresAt":"2099-01-01T00:00:00Z","place":214,"oldName":"old"}"#
        let value = try decoder.decode(AgentReservation.self, from: Data(json.utf8))
        XCTAssertEqual(value.status, .choice); XCTAssertEqual(value.place, 214); XCTAssertEqual(value.oldName, "old")
        XCTAssertEqual(try decoder.decode(AgentReservation.self, from: JSONEncoder().encode(value)), value)
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let cache = YourAgentCache(directory: directory, secrets: InMemorySecretStore())
        var copy = YourAgentCopy(hub: "hub", token: "secret"); copy.reservation = value
        try cache.save(copy); XCTAssertEqual(try cache.load(hubID: "hub"), copy)
    }

    func testCodeClientRequestsAndErrors() async throws {
        let server = AgentProtocolState(); let client = makeClient(server)
        let copy = YourAgentCopy(hub: "hub", token: "secret")
        server.respond(200, Self.reservedJSON)
        _ = try await client.verify(code: "123456", copy: copy)
        XCTAssertEqual(try body(XCTUnwrap(server.requests.last)) as NSDictionary, ["hub": "hub", "token": "secret", "code": "123456"] as NSDictionary)
        _ = try await client.resend(copy: copy)
        XCTAssertEqual(server.requests.last?.url?.lastPathComponent, "resend")
        _ = try await client.choose(keep: true, copy: copy)
        XCTAssertEqual(try body(XCTUnwrap(server.requests.last))["keep"] as? Bool, true)
        server.respond(200, #"{"reservation":null}"#)
        let cancelled = try await client.cancel(copy: copy); XCTAssertNil(cancelled)
        for (status, json, expected) in [(400, #"{"error":"wrong_code"}"#, AgentsError.wrongCode),
                                         (410, #"{"error":"expired"}"#, .expired),
                                         (409, #"{"error":"taken","suggestion":"alex2"}"#, .taken(suggestion: "alex2")),
                                         (429, #"{"error":"rate"}"#, .rate)] {
            server.respond(status, json)
            do { _ = try await client.verify(code: "123456", copy: copy); XCTFail("Expected \(expected)") }
            catch { XCTAssertEqual(error as? AgentsError, expected) }
        }
        server.respond(404, #"{"error":"none"}"#)
        do { _ = try await client.resend(copy: copy); XCTFail() }
        catch { XCTAssertEqual(error as? AgentsError, AgentsError.none) }
    }

    @MainActor
    func testVerifySuccessWrongAndExpired() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let api = FlowAgentAPI(); let store = flowStore(api, directory: directory)
        var copy = YourAgentCopy(hub: "hub", token: "secret"); copy.reservation = flowReservation(.pending)
        for error in [AgentsError.wrongCode, .expired] {
            store.apply(copy); await api.setVerify(.failure(error))
            store.code = "123456"; await store.verificationTask?.value
            XCTAssertEqual(store.code, ""); XCTAssertFalse(store.verifying)
            XCTAssertEqual(store.codeStatus, error == .wrongCode ? .wrong : .expired)
            XCTAssertEqual(store.step, .code)
        }
        let verified = flowReservation(.verified, place: 214)
        store.apply(copy); await api.setVerify(.success(verified))
        store.code = "123456"; await store.verificationTask?.value
        XCTAssertEqual(store.step, .yours); XCTAssertEqual(store.reservation, verified)
    }

    @MainActor
    func testVerifyingFlagWhileCodeIsChecked() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let api = HeldVerificationAPI()
        let store = YourAgentStore(hub: AgentTestHub(), api: api,
            cache: YourAgentCache(directory: directory, secrets: InMemorySecretStore()),
            hubID: { "hub" }, canRead: { false }, monitorNetwork: false)
        var copy = YourAgentCopy(hub: "hub", token: "secret"); copy.reservation = flowReservation(.pending); store.apply(copy)
        store.code = "123456"
        await api.waitForVerify()
        XCTAssertTrue(store.verifying)
        await api.finish(flowReservation(.verified, place: 214))
        await store.verificationTask?.value
        XCTAssertFalse(store.verifying); XCTAssertEqual(store.step, .yours)
    }

    @MainActor
    func testResendSentAndMissingReservesAgain() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let api = FlowAgentAPI(); let store = flowStore(api, directory: directory)
        let pending = flowReservation(.pending)
        var copy = YourAgentCopy(hub: "hub", token: "secret"); copy.reservation = pending; store.apply(copy)
        await api.setResend(.success(pending)); await store.sendCode()
        XCTAssertTrue(store.sent); XCTAssertEqual(store.step, .code)
        try await Task.sleep(for: .milliseconds(1750)); XCTAssertFalse(store.sent)
        await api.setResend(.failure(.none)); await api.setReserve(.success(pending))
        await store.sendCode()
        XCTAssertEqual(store.reservation, pending); XCTAssertTrue(store.sent)
        let calls = await api.calls
        XCTAssertEqual(calls, ["resend", "resend", "reserve:alex:a@example.com"])
        await api.setReserve(.failure(.taken(suggestion: "alex2")))
        await store.sendCode()
        XCTAssertEqual(store.step, .form); XCTAssertEqual(store.status, .taken("alex2"))
        XCTAssertEqual(store.name, "alex"); XCTAssertEqual(store.email, "a@example.com")
    }

    @MainActor
    func testCancelAndChoose() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let api = FlowAgentAPI(); let store = flowStore(api, directory: directory)
        var copy = YourAgentCopy(hub: "hub", token: "secret"); copy.reservation = flowReservation(.pending); store.apply(copy)
        await store.cancelRequest()
        XCTAssertEqual(store.step, .form); XCTAssertEqual(store.name, "alex"); XCTAssertEqual(store.email, "a@example.com")
        for keep in [true, false] {
            copy.reservation = flowReservation(.choice, oldName: "old"); store.apply(copy)
            let chosen = AgentReservation(name: keep ? "old" : "alex", email: "a@example.com", reservedAt: "2026-10-27T20:00:00Z", status: .verified)
            await api.setChoose(.success(chosen)); await store.choose(keep: keep)
            XCTAssertEqual(store.reservation, chosen); XCTAssertEqual(store.step, .yours)
        }
        let calls = await api.calls; XCTAssertEqual(calls, ["cancel", "choose:true", "choose:false"])
    }

    @MainActor
    func testRanOutFromCacheAndTimerRechecksName() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let api = FlowAgentAPI(); let cache = YourAgentCache(directory: directory, secrets: InMemorySecretStore())
        var copy = YourAgentCopy(hub: "hub", token: "secret"); copy.reservation = flowReservation(.pending)
        try cache.save(copy)
        let store = YourAgentStore(hub: AgentTestHub(), api: api, cache: cache, hubID: { "hub" }, canRead: { false }, monitorNetwork: false)
        await api.setTaken(); await store.open()
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertTrue(store.ranOut); XCTAssertEqual(store.name, "alex"); XCTAssertEqual(store.email, "a@example.com")
        XCTAssertEqual(store.status, .taken("alex2"))
        await store.open()
        XCTAssertEqual(store.step, .form); XCTAssertTrue(store.ranOut)
        var expiring = flowReservation(.pending)
        expiring.expiresAt = ISO8601DateFormatter().string(from: Date().addingTimeInterval(1))
        copy.reservation = expiring; store.apply(copy)
        try await Task.sleep(for: .milliseconds(1200))
        XCTAssertTrue(store.ranOut); XCTAssertEqual(store.step, .form); XCTAssertEqual(store.status, .taken("alex2"))
    }

    @MainActor
    func testEarlierRequestSendCodeAndTaken() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let api = FlowAgentAPI(); let store = flowStore(api, directory: directory)
        let earlier = flowReservation(.unconfirmed)
        var copy = YourAgentCopy(hub: "hub", token: "secret"); copy.reservation = earlier; store.apply(copy)
        XCTAssertEqual(store.step, .confirm)
        var withCode = earlier; withCode.expiresAt = "2099-01-01T00:00:00Z"
        await api.setResend(.success(withCode)); await store.sendCode(); XCTAssertEqual(store.step, .code)
        store.apply(copy); await api.setResend(.failure(.taken(suggestion: "alex2"))); await store.sendCode()
        XCTAssertEqual(store.step, .form); XCTAssertEqual(store.status, .taken("alex2")); XCTAssertEqual(store.name, "alex")
    }

    private func flowReservation(_ status: AgentReservation.Status, place: Int? = nil, oldName: String? = nil) -> AgentReservation {
        AgentReservation(name: "alex", email: "a@example.com", reservedAt: "2026-10-27T20:00:00Z", status: status,
                         expiresAt: status == .pending || status == .choice ? "2099-01-01T00:00:00Z" : nil,
                         place: place, oldName: oldName)
    }
    @MainActor private func flowStore(_ api: FlowAgentAPI, directory: URL) -> YourAgentStore {
        YourAgentStore(hub: AgentTestHub(), api: api, cache: YourAgentCache(directory: directory, secrets: InMemorySecretStore()),
                       hubID: { "hub" }, canRead: { false }, monitorNetwork: false)
    }
    @MainActor
    func testSeparateEmailErrorAndOpenUsesCurrentNetworkPath() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let server = AgentProtocolState()
        var networkAvailable = false
        let store = YourAgentStore(hub: AgentTestHub(), api: makeClient(server),
            cache: YourAgentCache(directory: directory, secrets: InMemorySecretStore()),
            hubID: { "hub" }, canRead: { false }, monitorNetwork: false,
            networkAvailable: { networkAvailable })
        await store.open()
        XCTAssertFalse(store.reachable)
        networkAvailable = true
        await store.open()
        XCTAssertTrue(store.reachable)
        store.apply(YourAgentCopy(hub: "hub", token: "secret"))
        server.respond(200, #"{"name":"alex","available":false,"reason":"taken","suggestion":"alex.rivera"}"#)
        store.name = "alex"; store.nameDidLoseFocus()
        try await Task.sleep(nanoseconds: 150_000_000)
        store.email = "wrong"; store.emailDidLoseFocus()
        XCTAssertEqual(store.status, .taken("alex.rivera")); XCTAssertTrue(store.emailInvalid)
        store.name = "newname"
        XCTAssertEqual(store.status, .clear); XCTAssertTrue(store.emailInvalid)
        store.email = "valid@example.com"
        XCTAssertFalse(store.emailInvalid)
        server.respond(400, #"{"error":"invalid_hub"}"#)
        store.nameDidLoseFocus()
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertEqual(store.status, .clear); XCTAssertTrue(store.reachable)
        await store.reserve()
        XCTAssertEqual(store.status, .failed); XCTAssertTrue(store.reachable)
    }

    @MainActor
    func testEnvironmentForgetPairingClearsAgentCacheAndSecrets() throws {
        let directory = testDirectory()
        let suite = "your-agent-tests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite); try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        let environment = AppEnvironment(directory: directory.appendingPathComponent("vault"), secrets: secrets,
            briefDirectory: directory.appendingPathComponent("briefs"), journalURL: directory.appendingPathComponent("journal.json"),
            chatDirectory: directory.appendingPathComponent("chats"),
            widgetStore: WidgetSnapshotStore(fileURL: directory.appendingPathComponent("widget.json")), pushDefaults: defaults)
        let cache = YourAgentCache(directory: directory.appendingPathComponent("vault/your-agent"), secrets: secrets)
        try cache.save(YourAgentCopy(hub: "hub", token: "secret"))
        try environment.forgetPairingLocally()
        XCTAssertTrue(secrets.accounts.isEmpty)
        XCTAssertNil(try cache.load(hubID: "hub"))
        XCTAssertNil(environment.yourAgent.copy)
    }

    @MainActor
    func testOpenFetchesCopyAndReservation() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let server = AgentProtocolState(); server.respond(200, Self.reservedJSON)
        var copy = YourAgentCopy(hub: "hub", token: "secret")
        copy.nameCandidates = ["alex.rivera"]
        let store = YourAgentStore(hub: AgentCopyHub(copy: copy), api: makeClient(server),
            cache: YourAgentCache(directory: directory, secrets: InMemorySecretStore()),
            hubID: { "hub" }, canRead: { true }, monitorNetwork: false)
        await store.open()
        XCTAssertEqual(store.name, "")
        XCTAssertEqual(store.email, "")
        XCTAssertEqual(store.reservation?.name, "alex")
        XCTAssertEqual(server.requests.count, 1)
    }
    @MainActor
    func testInputFilteringAndRulesOnlyAfterFieldExitOrSend() async {
        XCTAssertEqual(YourAgentRules.filterName(" ALEX..Rivera_+@中😃\n"), "alex.rivera")
        XCTAssertEqual(YourAgentRules.filterName("éE\u{301}Ａ１-09"), "09")
        XCTAssertEqual(YourAgentRules.filterName("...a...b..."), ".a.b.")
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let store = makeStore(AgentProtocolState(), directory: directory)
        for value in ["", "ab", ".alex", "alex.", String(repeating: "a", count: 31)] {
            store.name = value
            XCTAssertEqual(store.status, .clear)
            store.nameDidLoseFocus()
            XCTAssertEqual(store.status, .rules, value)
            store.name = value
            await store.reserve()
            XCTAssertEqual(store.status, .rules, value)
        }
        for value in ["abc", "a.b", String(repeating: "a", count: 30)] {
            store.name = value
            store.nameDidLoseFocus()
            XCTAssertEqual(store.status, .clear, value)
        }
        store.name = "ADMIN"; store.nameDidLoseFocus()
        XCTAssertEqual(store.name, "admin"); XCTAssertEqual(store.status, .notAllowed)
        store.name = "AL_ex..Rivera!"
        XCTAssertEqual(store.name, "alex.rivera"); XCTAssertEqual(store.status, .clear)
    }

    @MainActor
    func testNoStillLearningBeforeFirstCopy() async {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let store = makeStore(AgentProtocolState(), directory: directory)
        XCTAssertNil(store.copy); XCTAssertFalse(store.isStillLearning)
        await store.open()
        XCTAssertNil(store.copy); XCTAssertFalse(store.isStillLearning)
        var copy = YourAgentCopy(hub: "hub", token: "secret")
        copy.stillLearning = false
        store.apply(copy); XCTAssertFalse(store.isStillLearning)
        copy.stillLearning = true
        store.apply(copy); XCTAssertTrue(store.isStillLearning)
    }

    @MainActor
    func testTokenRefreshAndSingleRetryPersistsNewCredentials() async throws {
        let scenarios: [[AgentsError]] = [[.token], [.token, .token, .token]]
        for errors in scenarios {
            let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
            let secrets = InMemorySecretStore()
            let cache = YourAgentCache(directory: directory, secrets: secrets)
            let hub = RefreshAgentHub()
            let api = RetryAgentAPI(errors: errors)
            let store = YourAgentStore(hub: hub, api: api, cache: cache,
                hubID: { "hub" }, canRead: { true }, monitorNetwork: false)
            store.apply(YourAgentCopy(hub: "hub", token: "old"))
            store.name = "alex"; store.email = "a@example.com"
            await store.reserve()
            let tokens = await api.tokens
            let reads = await hub.reads
            XCTAssertEqual(tokens, ["old", "fresh"]); XCTAssertEqual(reads, 1)
            XCTAssertEqual(store.copy?.token, "fresh")
            XCTAssertEqual(try cache.load(hubID: "hub")?.token, "fresh")
            XCTAssertFalse(try String(contentsOf: directory.appendingPathComponent("copy.json"), encoding: .utf8).contains("fresh"))
            if errors.count == 1 { XCTAssertEqual(store.reservation?.name, "alex") }
            else { XCTAssertEqual(store.status, .failed); XCTAssertTrue(store.canReserve) }
        }
    }

    @MainActor
    func testGenericSingleRetryAndUnreachableDoesNotRetry() async {
        let scenarios: [[AgentsError]] = [[.failed], [.failed, .failed, .failed], [.unreachable], [.invalidEmail]]
        for errors in scenarios {
            let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
            let api = RetryAgentAPI(errors: errors)
            let hub = RefreshAgentHub()
            let store = YourAgentStore(hub: hub, api: api,
                cache: YourAgentCache(directory: directory, secrets: InMemorySecretStore()),
                hubID: { "hub" }, canRead: { true }, monitorNetwork: false)
            store.apply(YourAgentCopy(hub: "hub", token: "old"))
            store.name = "alex"; store.email = "a@example.com"
            await store.reserve()
            let tokens = await api.tokens
            let reads = await hub.reads
            XCTAssertEqual(tokens.count, errors.first == .failed ? 2 : 1)
            XCTAssertEqual(reads, 0)
            if errors.count > 1 { XCTAssertEqual(store.status, .failed); XCTAssertTrue(store.canReserve) }
            if errors == [.failed] { XCTAssertNotNil(store.reservation) }
            if errors == [.unreachable] { XCTAssertFalse(store.reachable) }
            if errors == [.invalidEmail] { XCTAssertTrue(store.emailInvalid) }
        }
    }

    @MainActor
    func testRateLimitRetryAtAndNextUTCHourWithAutomaticReenable() async throws {
        let directory = testDirectory(); defer { try? FileManager.default.removeItem(at: directory) }
        let server = AgentProtocolState(); let client = makeClient(server)
        let copy = YourAgentCopy(hub: "hub", token: "secret")
        let date = try XCTUnwrap(ISO8601DateFormatter().date(from: "2027-10-28T04:40:00Z"))
        for text in ["2027-10-28T04:40:00Z", "2027-10-28T04:40:00.000Z"] {
            server.respond(429, "{\"error\":\"rate\",\"retryAt\":\"\(text)\"}")
            do { _ = try await client.reserve(name: "alex", email: "a@example.com", copy: copy); XCTFail() }
            catch { XCTAssertEqual(error as? AgentsError, .rateUntil(date)) }
        }
        let store = makeStore(server, directory: directory)
        store.apply(copy); store.name = "alex"; store.email = "a@example.com"
        for json in [#"{"error":"rate"}"#, #"{"error":"rate","retryAt":"bad"}"#] {
            XCTAssertTrue(store.clearAfterUnpair())
            store.apply(copy); store.name = "alex"; store.email = "a@example.com"
            server.respond(429, json)
            let start = Date()
            await store.reserve()
            let retry = try XCTUnwrap(store.retryAt)
            XCTAssertGreaterThan(retry, start)
            XCTAssertLessThanOrEqual(retry.timeIntervalSince(start), 3600)
            XCTAssertEqual(retry.timeIntervalSince1970.truncatingRemainder(dividingBy: 3600), 0)
            XCTAssertFalse(store.canReserve); XCTAssertEqual(store.status, .clear)
        }
        XCTAssertTrue(store.clearAfterUnpair())
        store.apply(copy); store.name = "alex"; store.email = "a@example.com"
        let api = RetryAgentAPI(errors: [.rateUntil(Date().addingTimeInterval(0.25))])
        let timed = YourAgentStore(hub: AgentTestHub(), api: api,
            cache: YourAgentCache(directory: directory, secrets: InMemorySecretStore()),
            hubID: { "hub" }, canRead: { true }, monitorNetwork: false)
        timed.apply(copy); timed.name = "alex"; timed.email = "a@example.com"
        await timed.reserve()
        XCTAssertNotNil(timed.retryAt); XCTAssertFalse(timed.canReserve)
        timed.name = "alex2"; XCTAssertFalse(timed.canReserve)
        try await Task.sleep(for: .milliseconds(350))
        XCTAssertNil(timed.retryAt); XCTAssertTrue(timed.canReserve)
        await timed.reserve(); XCTAssertNotNil(timed.reservation)
        let requests = await api.tokens
        XCTAssertEqual(requests.count, 2)
    }

    private func testDirectory() -> URL { FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString) }
    private func makeClient(_ state: AgentProtocolState) -> AgentsClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [AgentURLProtocol.self]
        let id = UUID().uuidString
        AgentURLProtocol.register(state, id: id)
        return AgentsClient(session: URLSession(configuration: config), baseURL: URL(string: "https://\(id).test/api/agents")!, language: "zh-Hans")
    }
    @MainActor private func makeStore(_ state: AgentProtocolState, directory: URL) -> YourAgentStore {
        YourAgentStore(hub: AgentTestHub(), api: makeClient(state),
            cache: YourAgentCache(directory: directory, secrets: InMemorySecretStore()),
            hubID: { "hub" }, canRead: { true }, monitorNetwork: false)
    }
    private func body(_ request: URLRequest) throws -> [String: Any] {
        if let data = request.httpBody { return try JSONSerialization.jsonObject(with: data) as! [String: Any] }
        let stream = try XCTUnwrap(request.httpBodyStream)
        stream.open(); defer { stream.close() }
        var data = Data(); var buffer = [UInt8](repeating: 0, count: 1024)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }; data.append(buffer, count: count)
        }
        return try JSONSerialization.jsonObject(with: data) as! [String: Any]
    }
}

private struct AgentTestHub: YourAgentHubAPI {
    func yourAgent() async throws -> YourAgentCopy { throw HubRequestError.noAnswer }
}

private actor FlowAgentAPI: AgentsAPI {
    private var verifyResult: Result<AgentReservation, AgentsError> = .failure(.failed)
    private var resendResult: Result<AgentReservation, AgentsError> = .failure(.failed)
    private var reserveResult: Result<AgentReservation, AgentsError> = .failure(.failed)
    private var chooseResult: Result<AgentReservation, AgentsError> = .failure(.failed)
    private var taken = false
    private(set) var calls: [String] = []
    func setVerify(_ result: Result<AgentReservation, AgentsError>) { verifyResult = result }
    func setResend(_ result: Result<AgentReservation, AgentsError>) { resendResult = result }
    func setReserve(_ result: Result<AgentReservation, AgentsError>) { reserveResult = result }
    func setChoose(_ result: Result<AgentReservation, AgentsError>) { chooseResult = result }
    func setTaken() { taken = true }
    func check(name: String, copy: YourAgentCopy) async throws -> AgentNameCheck {
        AgentNameCheck(name: name, available: !taken, reason: taken ? "taken" : nil, suggestion: taken ? "alex2" : nil)
    }
    func reservation(copy: YourAgentCopy) async throws -> AgentReservation? { nil }
    func reserve(name: String, email: String, copy: YourAgentCopy) async throws -> AgentReservation {
        calls.append("reserve:\(name):\(email)"); return try reserveResult.get()
    }
    func verify(code: String, copy: YourAgentCopy) async throws -> AgentReservation {
        calls.append("verify:\(code)"); return try verifyResult.get()
    }
    func resend(copy: YourAgentCopy) async throws -> AgentReservation {
        calls.append("resend"); return try resendResult.get()
    }
    func choose(keep: Bool, copy: YourAgentCopy) async throws -> AgentReservation {
        calls.append("choose:\(keep)"); return try chooseResult.get()
    }
    func cancel(copy: YourAgentCopy) async throws -> AgentReservation? { calls.append("cancel"); return nil }
}

private actor HeldVerificationAPI: AgentsAPI {
    private var continuation: CheckedContinuation<AgentReservation, Error>?
    private var waiter: CheckedContinuation<Void, Never>?
    func waitForVerify() async {
        if continuation == nil { await withCheckedContinuation { waiter = $0 } }
    }
    func finish(_ value: AgentReservation) { continuation?.resume(returning: value); continuation = nil }
    func verify(code: String, copy: YourAgentCopy) async throws -> AgentReservation {
        try await withCheckedThrowingContinuation { continuation in
            self.continuation = continuation; waiter?.resume(); waiter = nil
        }
    }
    func check(name: String, copy: YourAgentCopy) async throws -> AgentNameCheck { throw AgentsError.failed }
    func reserve(name: String, email: String, copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func reservation(copy: YourAgentCopy) async throws -> AgentReservation? { nil }
    func resend(copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func choose(keep: Bool, copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func cancel(copy: YourAgentCopy) async throws -> AgentReservation? { nil }
}

private actor RefreshAgentHub: YourAgentHubAPI {
    private(set) var reads = 0
    func yourAgent() async throws -> YourAgentCopy {
        reads += 1
        return YourAgentCopy(hub: "hub", token: "fresh")
    }
}

private actor RetryAgentAPI: AgentsAPI {
    var errors: [AgentsError]
    private(set) var tokens: [String] = []
    init(errors: [AgentsError]) { self.errors = errors }
    func check(name: String, copy: YourAgentCopy) async throws -> AgentNameCheck {
        AgentNameCheck(name: name, available: true, reason: nil, suggestion: nil)
    }
    func reservation(copy: YourAgentCopy) async throws -> AgentReservation? { nil }
    func verify(code: String, copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func resend(copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func choose(keep: Bool, copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func cancel(copy: YourAgentCopy) async throws -> AgentReservation? { throw AgentsError.failed }
    func reserve(name: String, email: String, copy: YourAgentCopy) async throws -> AgentReservation {
        tokens.append(copy.token)
        if !errors.isEmpty { throw errors.removeFirst() }
        return AgentReservation(name: name, email: email, reservedAt: "2026-10-28T04:40:00Z")
    }
}

private struct AgentCopyHub: YourAgentHubAPI {
    let copy: YourAgentCopy
    func yourAgent() async throws -> YourAgentCopy { copy }
}

private final class AgentProtocolState: @unchecked Sendable {
    private let lock = NSLock()
    private var status = 200
    private var json = "{}"
    private var recorded: [URLRequest] = []
    var error: URLError?
    var requests: [URLRequest] { lock.lock(); defer { lock.unlock() }; return recorded }
    func respond(_ status: Int, _ json: String) { lock.lock(); defer { lock.unlock() }; self.status = status; self.json = json }
    func answer(_ request: URLRequest) -> (Int, Data, URLError?) {
        lock.lock(); defer { lock.unlock() }
        recorded.append(request)
        return (status, Data(json.utf8), error)
    }
}

private final class AgentURLProtocol: URLProtocol, @unchecked Sendable {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var states: [String: AgentProtocolState] = [:]
    static func register(_ state: AgentProtocolState, id: String) {
        lock.lock(); defer { lock.unlock() }; states[id.lowercased() + ".test"] = state
    }
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.lock.lock(); let state = Self.states[request.url!.host!.lowercased()]; Self.lock.unlock()
        guard let state else { client?.urlProtocol(self, didFailWithError: URLError(.badURL)); return }
        let (status, data, error) = state.answer(request)
        if let error { client?.urlProtocol(self, didFailWithError: error); return }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status,
            httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() { }
}

private actor HeldAgentChecks: AgentsAPI {
    private var pending: [String: CheckedContinuation<AgentNameCheck, Error>] = [:]
    private var waiter: CheckedContinuation<Void, Never>?
    private var count = 0
    private var observed = 0
    func check(name: String, copy: YourAgentCopy) async throws -> AgentNameCheck {
        try await withCheckedThrowingContinuation { continuation in
            pending[name] = continuation; count += 1; waiter?.resume(); waiter = nil
        }
    }
    func waitForCheck() async {
        if count <= observed { await withCheckedContinuation { waiter = $0 } }
        observed = count
    }
    func finish(_ name: String, available: Bool) {
        pending.removeValue(forKey: name)?.resume(returning: AgentNameCheck(name: name, available: available,
            reason: available ? nil : "taken", suggestion: nil))
    }
    func reserve(name: String, email: String, copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.unreachable }
    func reservation(copy: YourAgentCopy) async throws -> AgentReservation? { nil }
    func verify(code: String, copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func resend(copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func choose(keep: Bool, copy: YourAgentCopy) async throws -> AgentReservation { throw AgentsError.failed }
    func cancel(copy: YourAgentCopy) async throws -> AgentReservation? { throw AgentsError.failed }
}
