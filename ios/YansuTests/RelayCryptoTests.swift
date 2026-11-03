import Foundation
import XCTest
@testable import Yansu

final class RelayCryptoTests: XCTestCase {
    private let hubID = "11111111-2222-4333-8444-555555555555"
    private let pairingID = "66666666-7777-4888-8999-aaaaaaaaaaaa"
    private let deviceID = "99999999-aaaa-4bbb-8ccc-dddddddddddd"

    // MARK: Encodings

    func testBase64URLRoundTripUsesNoPaddingAndRejectsNonCanonicalText() throws {
        let bytes = Data([0xfb, 0xff, 0x00, 0x01, 0x02])
        let text = RelayCrypto.base64URLEncode(bytes)
        XCTAssertFalse(text.contains("="))
        XCTAssertFalse(text.contains("+"))
        XCTAssertFalse(text.contains("/"))
        XCTAssertEqual(try RelayCrypto.base64URLDecode(text), bytes)
        XCTAssertThrowsError(try RelayCrypto.base64URLDecode(text + "="))
        XCTAssertThrowsError(try RelayCrypto.base64URLDecode("ab+/"))
        XCTAssertThrowsError(try RelayCrypto.base64URLDecode("AB"))
        XCTAssertThrowsError(try RelayCrypto.base64URLDecode(""))
    }

    func testDecodeKeyRequiresExactly32Bytes() {
        XCTAssertNoThrow(try RelayCrypto.decodeKey(RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32))))
        XCTAssertThrowsError(try RelayCrypto.decodeKey(RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(31))))
    }

    // MARK: Key agreement and derivation

    func testKeyAgreementIsSymmetricAndProduces32Bytes() throws {
        let device = RelayCrypto.makeDeviceKeyPair()
        let hub = RelayCrypto.makeDeviceKeyPair()
        let fromDevice = try RelayCrypto.sharedSecret(privateKey: device.privateKey, peerPublicKey: hub.publicKey)
        let fromHub = try RelayCrypto.sharedSecret(privateKey: hub.privateKey, peerPublicKey: device.publicKey)
        XCTAssertEqual(fromDevice, fromHub)
        XCTAssertEqual(fromDevice.count, 32)
    }

    func testPairingKeyIsSymmetricBetweenDeviceAndHub() throws {
        let device = RelayCrypto.makeDeviceKeyPair()
        let hub = RelayCrypto.makeDeviceKeyPair()
        let secret = RelayCrypto.randomBytes(32)
        let deviceSide = try RelayCrypto.pairingKey(devicePrivate: device.privateKey, hubPublic: hub.publicKey,
                                                    secret: secret, hubID: hubID, pairingID: pairingID)
        let hubSide = try RelayCrypto.pairingKey(devicePrivate: hub.privateKey, hubPublic: device.publicKey,
                                                 secret: secret, hubID: hubID, pairingID: pairingID)
        XCTAssertEqual(deviceSide, hubSide)
        XCTAssertEqual(deviceSide.count, 32)
    }

    func testClaimTokenIsHMACOfPairingIDUnderSecret() {
        let secret = Data(repeating: 3, count: 32)
        let token = RelayCrypto.claimToken(secret: secret, pairingID: pairingID)
        XCTAssertEqual(token.count, 32)
        XCTAssertEqual(token, RelayCrypto.claimToken(secret: secret, pairingID: pairingID))
        XCTAssertNotEqual(token, RelayCrypto.claimToken(secret: secret, pairingID: "other"))
    }

    func testSessionKeysAreDirectionalAndAgreeAcrossSides() throws {
        let device = RelayCrypto.makeDeviceKeyPair()
        let hub = RelayCrypto.makeDeviceKeyPair()
        let fromDevice = try RelayCrypto.sessionKeys(devicePrivate: device.privateKey, hubPublic: hub.publicKey,
                                                     hubID: hubID, deviceID: deviceID)
        let fromHub = try RelayCrypto.sessionKeys(devicePrivate: hub.privateKey, hubPublic: device.publicKey,
                                                  hubID: hubID, deviceID: deviceID)
        XCTAssertEqual(fromDevice, fromHub)
        XCTAssertNotEqual(fromDevice.d2h, fromDevice.h2d)
    }

    func testConfirmationCodeIsSixDigitsAndDeterministic() {
        let kPair = Data(repeating: 7, count: 32)
        let code = RelayCrypto.confirmationCode(kPair: kPair)
        XCTAssertEqual(code.count, 6)
        XCTAssertTrue(code.allSatisfy { $0.isASCII && $0.isNumber })
        XCTAssertEqual(code, RelayCrypto.confirmationCode(kPair: kPair))
        XCTAssertNotEqual(code, RelayCrypto.confirmationCode(kPair: Data(repeating: 8, count: 32)))
    }

    // MARK: Pairing seal and open

    func testClaimSealOpenRoundTripFailsOnAADChangeAndRejectsApprovalNonce() throws {
        let kPair = RelayCrypto.randomBytes(32)
        let plaintext = Data("{\"deviceId\":\"x\"}".utf8)
        let wire = try RelayCrypto.sealClaim(plaintext, kPair: kPair, hubID: hubID, pairingID: pairingID)
        XCTAssertEqual(wire.count, plaintext.count + 16)
        let opened = try RelayCrypto.unseal(wire, key: kPair, nonce: RelayCrypto.pairingNonce(0x01),
                                            aad: RelayCrypto.claimAAD(hubID: hubID, pairingID: pairingID))
        XCTAssertEqual(opened, plaintext)
        XCTAssertThrowsError(try RelayCrypto.unseal(wire, key: kPair, nonce: RelayCrypto.pairingNonce(0x01),
                                                    aad: RelayCrypto.claimAAD(hubID: hubID, pairingID: "other")))
        // The approval nonce and AAD differ, so a claim cannot be replayed as an approval.
        XCTAssertThrowsError(try RelayCrypto.openApproval(wire, kPair: kPair, hubID: hubID, pairingID: pairingID))
    }

    func testApprovalRoundTripDetectsBitFlips() throws {
        let kPair = RelayCrypto.randomBytes(32)
        let plaintext = Data("{\"deviceId\":\"d\"}".utf8)
        let wire = try RelayCrypto.seal(plaintext, key: kPair, nonce: RelayCrypto.pairingNonce(0x02),
                                        aad: RelayCrypto.approvalAAD(hubID: hubID, pairingID: pairingID))
        XCTAssertEqual(try RelayCrypto.openApproval(wire, kPair: kPair, hubID: hubID, pairingID: pairingID), plaintext)
        var flipped = wire
        flipped[0] ^= 0x01
        XCTAssertThrowsError(try RelayCrypto.openApproval(flipped, kPair: kPair, hubID: hubID, pairingID: pairingID))
    }

    func testNonceLayoutsMatchTheProtocol() {
        XCTAssertEqual(RelayCrypto.pairingNonce(0x01), Data([0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1]))
        XCTAssertEqual(RelayCrypto.pairingNonce(0x02), Data([0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2]))
        XCTAssertEqual(RelayCrypto.envelopeNonce(seq: 0x0102), Data([0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01, 0x02]))
    }

    // MARK: Envelopes

    func testEnvelopeAADMatchesTheProtocolString() {
        XCTAssertEqual(RelayCrypto.envelopeAAD(hub: "H", dev: "D", dir: "d2h", seq: 17, kind: "req"),
                       "yansu-env-v1|1|H|D|d2h|17|req")
    }

    func testEnvelopeRoundTripFailsWhenAnyHeaderFieldIsRelabelled() throws {
        let keys = RelayCrypto.SessionKeys(d2h: RelayCrypto.randomBytes(32), h2d: RelayCrypto.randomBytes(32))
        let plaintext = Data("{\"id\":\"r1\"}".utf8)
        let wire = try RelayCrypto.sealEnvelope(plaintext, key: keys.d2h, hub: hubID, dev: deviceID,
                                                dir: "d2h", seq: 17, kind: "req")
        XCTAssertEqual(try RelayCrypto.openEnvelope(wire, key: keys.d2h, hub: hubID, dev: deviceID,
                                                    dir: "d2h", seq: 17, kind: "req"), plaintext)
        XCTAssertThrowsError(try RelayCrypto.openEnvelope(wire, key: keys.d2h, hub: hubID, dev: deviceID,
                                                          dir: "d2h", seq: 18, kind: "req"))
        XCTAssertThrowsError(try RelayCrypto.openEnvelope(wire, key: keys.d2h, hub: hubID, dev: deviceID,
                                                          dir: "d2h", seq: 17, kind: "res"))
        XCTAssertThrowsError(try RelayCrypto.openEnvelope(wire, key: keys.d2h, hub: hubID, dev: deviceID,
                                                          dir: "h2d", seq: 17, kind: "req"))
        XCTAssertThrowsError(try RelayCrypto.openEnvelope(wire, key: keys.d2h, hub: pairingID, dev: deviceID,
                                                          dir: "d2h", seq: 17, kind: "req"))
        XCTAssertThrowsError(try RelayCrypto.openEnvelope(wire, key: keys.h2d, hub: hubID, dev: deviceID,
                                                          dir: "d2h", seq: 17, kind: "req"))
    }

    // MARK: Brief padding

    func testBriefPaddingRoundTripsAndTrimsOnlyTrailingZeros() throws {
        let json = Data("{\"briefId\":\"2026-10-09-morning\"}".utf8)
        let padded = RelayCrypto.padBrief(json)
        XCTAssertEqual(padded.count % 4096, 0)
        XCTAssertEqual(try RelayCrypto.unpadBrief(padded), json)
        let exact = Data(repeating: 0x41, count: 4096)
        XCTAssertEqual(RelayCrypto.padBrief(exact).count, 4096)
        XCTAssertEqual(try RelayCrypto.unpadBrief(RelayCrypto.padBrief(exact)), exact)
    }

    func testBriefUnpaddingRejectsEmptyAndOversizedPlaintext() {
        XCTAssertThrowsError(try RelayCrypto.unpadBrief(Data(repeating: 0, count: 4096)))
        XCTAssertThrowsError(try RelayCrypto.unpadBrief(Data(repeating: 0x41, count: 256 * 1024 + 1)))
    }

    // MARK: Replay protection (PROTOCOL.md section 4)

    func testMessageStreamRejectsReplayAndRollback() throws {
        let vault = try makeVault()
        try vault.acceptMessage(seq: 5)
        XCTAssertThrowsError(try vault.acceptMessage(seq: 5))
        XCTAssertThrowsError(try vault.acceptMessage(seq: 4))
        try vault.acceptMessage(seq: 6)
        XCTAssertEqual(vault.lastAcceptedMessage(), 6)
    }

    func testBriefStreamAcceptsTheSameBriefAgainButRefusesRollback() throws {
        let vault = try makeVault()
        XCTAssertEqual(try vault.acceptBrief(seq: 10), .fresh)
        XCTAssertEqual(try vault.acceptBrief(seq: 10), .known)
        XCTAssertThrowsError(try vault.acceptBrief(seq: 9))
        XCTAssertEqual(try vault.acceptBrief(seq: 11), .fresh)
        XCTAssertEqual(vault.lastAcceptedBrief(), 11)
    }

    func testStreamsAreCheckedSeparately() throws {
        let vault = try makeVault()
        try vault.acceptMessage(seq: 50)
        XCTAssertEqual(try vault.acceptBrief(seq: 10), .fresh)
    }

    func testSendCounterSkipsAheadByOneThousandAfterRestart() throws {
        let directory = try makeTemporaryDirectory()
        let secrets = InMemorySecretStore()
        let first = PairingVault(directory: directory, secrets: secrets)
        try first.store(Self.sampleRecord(), devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32)),
                        bearer: RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32)))
        XCTAssertEqual(try first.nextSendSeq(), 1)
        XCTAssertEqual(try first.nextSendSeq(), 2)
        let restarted = PairingVault(directory: directory, secrets: secrets)
        XCTAssertEqual(try restarted.nextSendSeq(), 1003)
    }

    func testSequenceNumbersAreBoundedToTwoToTheFiftyThreeMinusOne() throws {
        XCTAssertEqual(RelayCrypto.maxSequence, 9_007_199_254_740_991)
        let vault = try makeVault()
        XCTAssertThrowsError(try vault.acceptMessage(seq: RelayCrypto.maxSequence + 1))
        XCTAssertThrowsError(try vault.acceptBrief(seq: RelayCrypto.maxSequence + 1))
        XCTAssertNoThrow(try vault.acceptMessage(seq: RelayCrypto.maxSequence))
    }

    func testSendCounterStopsAtTheBoundInsteadOfWrapping() throws {
        var record = Self.sampleRecord()
        record.counters.sendSeq = RelayCrypto.maxSequence - 1
        let vault = PairingVault(directory: try makeTemporaryDirectory(), secrets: InMemorySecretStore())
        try vault.store(record, devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32)),
                        bearer: RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32)))
        XCTAssertEqual(try vault.nextSendSeq(), RelayCrypto.maxSequence)
        XCTAssertThrowsError(try vault.nextSendSeq()) { error in
            XCTAssertEqual(error as? RelayCryptoError, .sequenceExhausted)
        }
    }

    // MARK: Helpers

    private func makeVault() throws -> PairingVault {
        let vault = PairingVault(directory: try makeTemporaryDirectory(), secrets: InMemorySecretStore())
        try vault.store(Self.sampleRecord(), devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32)),
                        bearer: RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32)))
        return vault
    }

    static func sampleRecord() -> PairedDevice {
        PairedDevice(hubID: "11111111-2222-4333-8444-555555555555",
                     hubName: "Test Mac",
                     hubPublicKey: RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(32)),
                     relayOrigin: "https://relay.example",
                     deviceID: "99999999-aaaa-4bbb-8ccc-dddddddddddd",
                     deviceName: "Test iPhone",
                     scopes: ["brief:read", "brief:refresh", "presence:read", "push"],
                     pairedAt: Date(timeIntervalSince1970: 1_800_000_000),
                     expiresAt: nil,
                     counters: SequenceCounters())
    }
}
