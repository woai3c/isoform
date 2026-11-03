import Foundation
import XCTest
@testable import Yansu

// Cross-language check: mobile-protocol/vectors-v1.json is written by the Go test in wails-gui
// (go test -run TestMobileProtocolVectors -update). Until it exists the tests skip, not pass.
final class RelayCryptoVectorTests: XCTestCase {
    private struct Vectors: Decodable {
        struct Message: Decodable {
            let plaintext: String
            let ct: String
        }

        struct Envelope: Decodable {
            let dir: String
            let seq: UInt64
            let kind: String
            let plaintext: String
            let ct: String
        }

        let version: Int
        let hubPrivate: String
        let hubPublic: String
        let devicePrivate: String
        let devicePublic: String
        let hubId: String
        let deviceId: String
        let pairingId: String
        let secret: String
        let kPair: String
        let claimToken: String
        let confirmCode: String
        let kD2H: String
        let kH2D: String
        let claim: Message
        let approval: Message
        let envelopes: [Envelope]
    }

    private func loadVectors() throws -> Vectors {
        // ios/YansuTests/<file> -> repository root is three levels up.
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let url = root.appendingPathComponent("mobile-protocol/vectors-v1.json")
        guard FileManager.default.fileExists(atPath: url.path) else {
            throw XCTSkip("mobile-protocol/vectors-v1.json does not exist yet. The Go test TestMobileProtocolVectors generates it.")
        }
        return try JSONDecoder().decode(Vectors.self, from: Data(contentsOf: url))
    }

    func testKeysDerivationsAndConfirmationCodeMatchTheVectors() throws {
        let v = try loadVectors()
        XCTAssertEqual(v.version, 1)
        let devicePrivate = try RelayCrypto.decodeKey(v.devicePrivate)
        let hubPrivate = try RelayCrypto.decodeKey(v.hubPrivate)
        let devicePublic = try RelayCrypto.decodeKey(v.devicePublic)
        let hubPublic = try RelayCrypto.decodeKey(v.hubPublic)
        XCTAssertEqual(try RelayCrypto.publicKey(forPrivateKey: devicePrivate), devicePublic)
        XCTAssertEqual(try RelayCrypto.publicKey(forPrivateKey: hubPrivate), hubPublic)

        let secret = try RelayCrypto.decodeKey(v.secret)
        let kPair = try RelayCrypto.pairingKey(devicePrivate: devicePrivate, hubPublic: hubPublic, secret: secret,
                                               hubID: v.hubId, pairingID: v.pairingId)
        XCTAssertEqual(RelayCrypto.base64URLEncode(kPair), v.kPair)
        XCTAssertEqual(RelayCrypto.base64URLEncode(RelayCrypto.claimToken(secret: secret, pairingID: v.pairingId)),
                       v.claimToken)
        XCTAssertEqual(RelayCrypto.confirmationCode(kPair: kPair), v.confirmCode)

        let keys = try RelayCrypto.sessionKeys(devicePrivate: devicePrivate, hubPublic: hubPublic,
                                               hubID: v.hubId, deviceID: v.deviceId)
        XCTAssertEqual(RelayCrypto.base64URLEncode(keys.d2h), v.kD2H)
        XCTAssertEqual(RelayCrypto.base64URLEncode(keys.h2d), v.kH2D)
    }

    func testSealedMessagesOpenToTheirPlaintextAndResealToTheSameBytes() throws {
        let v = try loadVectors()
        let devicePrivate = try RelayCrypto.decodeKey(v.devicePrivate)
        let hubPublic = try RelayCrypto.decodeKey(v.hubPublic)
        let secret = try RelayCrypto.decodeKey(v.secret)
        let kPair = try RelayCrypto.pairingKey(devicePrivate: devicePrivate, hubPublic: hubPublic, secret: secret,
                                               hubID: v.hubId, pairingID: v.pairingId)
        try assertSealed(v.claim.plaintext, ct: v.claim.ct, key: kPair, nonce: RelayCrypto.pairingNonce(0x01),
                         aad: RelayCrypto.claimAAD(hubID: v.hubId, pairingID: v.pairingId))
        try assertSealed(v.approval.plaintext, ct: v.approval.ct, key: kPair, nonce: RelayCrypto.pairingNonce(0x02),
                         aad: RelayCrypto.approvalAAD(hubID: v.hubId, pairingID: v.pairingId))

        let d2h = try RelayCrypto.decodeKey(v.kD2H)
        let h2d = try RelayCrypto.decodeKey(v.kH2D)
        XCTAssertFalse(v.envelopes.isEmpty)
        for envelope in v.envelopes {
            let key = envelope.dir == "d2h" ? d2h : h2d
            try assertSealed(envelope.plaintext, ct: envelope.ct, key: key,
                             nonce: RelayCrypto.envelopeNonce(seq: envelope.seq),
                             aad: RelayCrypto.envelopeAAD(hub: v.hubId, dev: v.deviceId, dir: envelope.dir,
                                                          seq: envelope.seq, kind: envelope.kind))
        }
    }

    private func assertSealed(_ plaintext: String, ct: String, key: Data, nonce: Data, aad: String) throws {
        let wire = try RelayCrypto.base64URLDecode(ct)
        let opened = try RelayCrypto.unseal(wire, key: key, nonce: nonce, aad: aad)
        XCTAssertEqual(String(decoding: opened, as: UTF8.self), plaintext)
        let resealed = try RelayCrypto.seal(Data(plaintext.utf8), key: key, nonce: nonce, aad: aad)
        XCTAssertEqual(RelayCrypto.base64URLEncode(resealed), ct)
    }
}
