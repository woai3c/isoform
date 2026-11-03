import Foundation
import Security
@testable import Yansu

// Shared test doubles. Nothing here touches the network or the real Keychain.

final class InMemorySecretStore: SecretStore, @unchecked Sendable {
    private let lock = NSLock()
    private var values: [String: String] = [:]
    private var failSaves = false

    func setFailSaves(_ fail: Bool) {
        lock.lock()
        defer { lock.unlock() }
        failSaves = fail
    }

    func read(_ account: String) throws -> String? {
        lock.lock()
        defer { lock.unlock() }
        return values[account]
    }

    func accountNames() throws -> [String] {
        Array(accounts)
    }

    func save(_ value: String, account: String) throws {
        lock.lock()
        defer { lock.unlock() }
        if failSaves { throw YansuError.message("Keychain unavailable in test") }
        values[account] = value
    }

    func delete(_ account: String) throws {
        lock.lock()
        defer { lock.unlock() }
        values[account] = nil
    }

    var accounts: Set<String> {
        lock.lock()
        defer { lock.unlock() }
        return Set(values.keys)
    }
}

// Keychain items keyed by service, group and account, so migration tests can see which group an item is in.
final class InMemoryKeychainBackend: KeychainBackend, @unchecked Sendable {
    struct Key: Hashable {
        let service: String
        let accessGroup: String?
        let account: String
    }

    private let lock = NSLock()
    private var items: [Key: String] = [:]
    var failWrites = false

    func seed(service: String, accessGroup: String?, account: String, value: String) {
        lock.lock()
        defer { lock.unlock() }
        items[Key(service: service, accessGroup: accessGroup, account: account)] = value
    }

    func snapshot() -> [Key: String] {
        lock.lock()
        defer { lock.unlock() }
        return items
    }

    func entries(service: String) throws -> [KeychainEntry] {
        lock.lock()
        defer { lock.unlock() }
        return items.filter { $0.key.service == service }.map {
            KeychainEntry(account: $0.key.account, accessGroup: $0.key.accessGroup, value: $0.value)
        }
    }

    func value(service: String, account: String, accessGroup: String?) throws -> String? {
        lock.lock()
        defer { lock.unlock() }
        return items[Key(service: service, accessGroup: accessGroup, account: account)]
    }

    func write(service: String, account: String, accessGroup: String?, value: String,
               accessible: CFString) throws {
        lock.lock()
        defer { lock.unlock() }
        if failWrites { throw YansuError.message("Keychain unavailable in test") }
        items[Key(service: service, accessGroup: accessGroup, account: account)] = value
    }

    func remove(service: String, account: String, accessGroup: String?) throws {
        lock.lock()
        defer { lock.unlock() }
        items[Key(service: service, accessGroup: accessGroup, account: account)] = nil
    }
}

func makeTemporaryDirectory() throws -> URL {
    let url = FileManager.default.temporaryDirectory
        .appendingPathComponent("yansu-tests-\(UUID().uuidString)", isDirectory: true)
    try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
    return url
}

// The claim plaintext as the hub reads it (PROTOCOL.md section 3.3).
struct TestClaimPayload: Decodable {
    let deviceId: String
    let deviceName: String
    let devicePub: String
    let bearerHash: String
    let platform: String
    let appVersion: String
    let scopes: [String]
    let pushEnvironment: String
    let simulator: Bool?
}

// Plays the Mac's side of pairing with real keys: it derives kPair, opens the claim, and seals an approval.
final class TestHub: @unchecked Sendable {
    let hubID = UUID().uuidString.lowercased()
    let pairingID = UUID().uuidString.lowercased()
    let secret = RelayCrypto.randomBytes(32)
    let relay = "https://relay.example"
    private let keyPair = RelayCrypto.makeDeviceKeyPair()

    var hubPublicKey: Data { keyPair.publicKey }
    var hubPublicText: String { RelayCrypto.base64URLEncode(keyPair.publicKey) }

    func invitationText(exp: Int64, relayOrigin: String? = nil) -> String {
        let payload: [String: Any] = [
            "v": 1,
            "relay": relayOrigin ?? relay,
            "hub": hubID,
            "pairing": pairingID,
            "secret": RelayCrypto.base64URLEncode(secret),
            "hubPub": hubPublicText,
            "exp": exp,
        ]
        let json = try! JSONSerialization.data(withJSONObject: payload, options: [.sortedKeys])
        return "isoform://pair#" + RelayCrypto.base64URLEncode(json)
    }

    // X25519(hubPrivate, devicePublic) equals the device's X25519(devicePrivate, hubPublic) (section 3.3).
    func pairingKey(devicePublic: Data) throws -> Data {
        try RelayCrypto.pairingKey(devicePrivate: keyPair.privateKey, hubPublic: devicePublic,
                                   secret: secret, hubID: hubID, pairingID: pairingID)
    }

    func openClaim(_ body: PairingClaimBody) throws -> TestClaimPayload {
        let kPair = try pairingKey(devicePublic: RelayCrypto.decodeKey(body.devicePub))
        let plaintext = try RelayCrypto.unseal(try RelayCrypto.base64URLDecode(body.ct), key: kPair,
                                               nonce: RelayCrypto.pairingNonce(0x01),
                                               aad: RelayCrypto.claimAAD(hubID: hubID, pairingID: pairingID))
        return try JSONDecoder().decode(TestClaimPayload.self, from: plaintext)
    }

    func confirmationCode(for body: PairingClaimBody) throws -> String {
        RelayCrypto.confirmationCode(kPair: try pairingKey(devicePublic: RelayCrypto.decodeKey(body.devicePub)))
    }

    func approval(for body: PairingClaimBody, deviceIDOverride: String? = nil,
                  hubPubOverride: String? = nil) throws -> PairingPoll {
        let claim = try openClaim(body)
        let kPair = try pairingKey(devicePublic: RelayCrypto.decodeKey(body.devicePub))
        let plaintext: [String: Any] = [
            "deviceId": deviceIDOverride ?? claim.deviceId,
            "hubName": "Test Mac",
            "scopes": claim.scopes,
            "expiresAt": "2027-01-07T07:00:00Z",
            "hubPub": hubPubOverride ?? hubPublicText,
        ]
        let json = try JSONSerialization.data(withJSONObject: plaintext, options: [.sortedKeys])
        let ct = try RelayCrypto.seal(json, key: kPair, nonce: RelayCrypto.pairingNonce(0x02),
                                      aad: RelayCrypto.approvalAAD(hubID: hubID, pairingID: pairingID))
        return PairingPoll(state: "approved", approval: .init(ct: RelayCrypto.base64URLEncode(ct)))
    }
}

// A scripted relay for PairingStore. Each poll consumes one script step; an exhausted script
// answers "declined" so a test can never loop forever.
final class StubPairingTransport: PairingTransport, @unchecked Sendable {
    typealias PollStep = (PairingClaimBody) throws -> PairingPoll

    private let lock = NSLock()
    private var steps: [PollStep] = []
    private var claimFailure: Error?
    private var claimFailureQueue: [Error] = []
    private(set) var claimAttempts: [PairingClaimBody] = []
    private var unpairFailure: Error?
    private(set) var claims: [PairingClaimBody] = []
    private(set) var claimOrigins: [URL] = []
    private(set) var pollOrigins: [URL] = []
    private(set) var unpairCount = 0

    func script(_ steps: [PollStep]) {
        lock.lock()
        defer { lock.unlock() }
        self.steps = steps
    }

    func failClaim(with error: Error) {
        lock.lock()
        defer { lock.unlock() }
        claimFailure = error
    }

    // Fails the next claim attempts in order, then falls back to the permanent failure (if any).
    func queueClaimFailures(_ errors: [Error]) {
        lock.lock()
        defer { lock.unlock() }
        claimFailureQueue = errors
    }

    func failUnpair(with error: Error) {
        lock.lock()
        defer { lock.unlock() }
        unpairFailure = error
    }

    func claimPairing(origin: URL, pairingID: String, body: PairingClaimBody) async throws {
        lock.lock()
        claimAttempts.append(body)
        claimOrigins.append(origin)
        let queued: Error? = claimFailureQueue.isEmpty ? nil : claimFailureQueue.removeFirst()
        let failure = queued ?? claimFailure
        if failure == nil { claims.append(body) }
        lock.unlock()
        if let failure { throw failure }
    }

    func pollPairing(origin: URL, pairingID: String, claimToken: String) async throws -> PairingPoll {
        lock.lock()
        pollOrigins.append(origin)
        let step: PollStep? = steps.isEmpty ? nil : steps.removeFirst()
        let claim = claims.last
        lock.unlock()
        guard let claim else { throw RelayClientError.invalidResponse }
        guard let step else { return PairingPoll(state: "declined", approval: nil) }
        return try step(claim)
    }

    func unpair(_ credentials: RelayCredentials) async throws {
        lock.lock()
        unpairCount += 1
        let failure = unpairFailure
        lock.unlock()
        if let failure { throw failure }
    }
}
