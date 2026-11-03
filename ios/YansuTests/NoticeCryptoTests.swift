import Foundation
import XCTest
@testable import Yansu

// Sealed notice crypto (PROTOCOL.md section 14). The fixed-nonce vector was computed outside Swift with
// the Python cryptography package (ChaCha20-Poly1305, base64url of nonce || ciphertext || tag) and is checked here.
final class NoticeCryptoTests: XCTestCase {
    private let hub = "hub-test-1"
    private let dev = "dev-test-1"
    private let noticeID = "notice-test-1"

    // Fixed inputs for the vector below.
    private let vectorKey = Data((0x40...0x5f).map { UInt8($0) })
    private let vectorNonce = Data((0xa0...0xab).map { UInt8($0) })
    private let vectorCT = "oKGio6Slpqeoqaqr1-OtzkLYRLEeFLwH-VKXVdhXp7Cot5ZS5w7AIcZ0TRFXL2TD0NgFZLkSK1qN9HWALI0rs1fReuGYA_6XB1mGq6tzMwHmOURHxDRJ7rmDh_T5_mmjq5TuBl5C-Gri8rybf-_kjB5i28hSC9gLr8VMzlniwlh9g1LKY8jCvTJ6CJiTe0cIJmmsAG4bSmQ0kUpfiiOalSPtkgeB-KrOGGYaahegdyCS63-idehPrAVO3EW_odAbyzfRRsN0l-DyjeOHtD8LDuCOtLFlw66mD5jVaeJiz6qjjbRH-QtezG8U95miYyn8zb5_fidtKYM02Xn9VPyKqVCDziU7YdGA334hcteibTAwfzgb22KLiTXH28lYCZG18kXmr2oUDgyRD9VjDvE6GI3qsCFGu6378o_xIpVvDoe7C5BZk15awr03wLP5A2_wDzgtURzQw3zKYuEzlRSVgaThu394DoA0x7pRej--2JP1OFcngzDcR0-xQwM2VLrAl-8usJxB0n3ADGjC4JHh8JlGEYn0Qy3mQ-74QzkpyiujXv1z3EeJw5hk-fzD5Q6AUnE1TORxWhUh5zweNuFuBbOycUtRnkBjZIBKfddOfDTyT1nKyT0o4MSNrBI"
    private let vectorJSON = #"{"v":1,"id":"notice-test-1","kind":"decision_due","title":"Approve sending the Q3 reply to Sam?","body":"Sam asked for it this morning.","category":"YANSU_DECISION","actions":[{"id":"approve","label":"Approve","auth":true},{"id":"decline","label":"Decline","auth":true},{"id":"snooze","label":"Snooze","auth":false},{"id":"open","label":"Open","auth":false}],"subjectId":"req-123","createdAt":"2026-10-10T09:00:00Z","expiresAt":""}"#

    private func sampleJSON(id: String = "notice-test-1") -> Data {
        Data(#"{"v":1,"id":"\#(id)","kind":"meeting_ended","title":"Pricing sync is written up.","body":"3 decisions, 2 follow-ups.","category":"YANSU_MOMENT","actions":[],"subjectId":"","createdAt":"2026-10-10T09:00:00Z","expiresAt":null}"#.utf8)
    }

    private func key() -> Data {
        Data(repeating: 0x2a, count: 32)
    }

    func testRoundTripOpensTheSealedNotice() throws {
        let json = sampleJSON()
        let ct = try NoticeCrypto.seal(json, key: key(), hub: hub, dev: dev, noticeID: noticeID,
                                       nonce: RelayCrypto.randomBytes(12))
        let opened = try NoticeCrypto.open(ct, key: key(), hub: hub, dev: dev, noticeID: noticeID)
        XCTAssertEqual(opened, json)
        let plaintext = try NoticeCrypto.decode(opened)
        XCTAssertEqual(plaintext.title, "Pricing sync is written up.")
        XCTAssertEqual(plaintext.body, "3 decisions, 2 follow-ups.")
        XCTAssertEqual(plaintext.category, NoticeCategory.moment.rawValue)
    }

    func testSealedCopyIsPaddedToSixtyFourBytesAndTheSpacesAreStripped() throws {
        let json = sampleJSON()
        XCTAssertNotEqual(json.count % 64, 0, "the sample must need padding for this test to mean anything")
        let padded = NoticeCrypto.pad(json)
        XCTAssertEqual(padded.count % 64, 0)
        XCTAssertEqual(padded.prefix(json.count), json)
        XCTAssertTrue(padded.suffix(from: json.count).allSatisfy { $0 == 0x20 })
        XCTAssertEqual(try NoticeCrypto.unpad(padded), json)
    }

    func testAlreadyAlignedPlaintextIsNotPadded() throws {
        let aligned = Data(repeating: 0x7b, count: 64)
        XCTAssertEqual(NoticeCrypto.pad(aligned), aligned)
    }

    func testUnpadRejectsUnalignedAndEmptyInput() {
        XCTAssertThrowsError(try NoticeCrypto.unpad(Data("{}".utf8)))
        XCTAssertThrowsError(try NoticeCrypto.unpad(Data(repeating: 0x20, count: 64)))
        XCTAssertThrowsError(try NoticeCrypto.unpad(Data()))
    }

    func testAADBindsTheHubDeviceAndNoticeId() throws {
        let ct = try NoticeCrypto.seal(sampleJSON(), key: key(), hub: hub, dev: dev, noticeID: noticeID,
                                       nonce: RelayCrypto.randomBytes(12))
        XCTAssertThrowsError(try NoticeCrypto.open(ct, key: key(), hub: "hub-other", dev: dev, noticeID: noticeID))
        XCTAssertThrowsError(try NoticeCrypto.open(ct, key: key(), hub: hub, dev: "dev-other", noticeID: noticeID))
        XCTAssertThrowsError(try NoticeCrypto.open(ct, key: key(), hub: hub, dev: dev, noticeID: "notice-other"))
        XCTAssertEqual(NoticeCrypto.aad(hub: hub, dev: dev, noticeID: noticeID),
                       "yansu-notice-v1|hub-test-1|dev-test-1|notice-test-1")
    }

    func testAnyChangedCiphertextByteIsRejected() throws {
        let ct = try NoticeCrypto.seal(sampleJSON(), key: key(), hub: hub, dev: dev, noticeID: noticeID,
                                       nonce: RelayCrypto.randomBytes(12))
        var raw = try RelayCrypto.base64URLDecode(ct)
        raw[raw.count / 2] ^= 0x01
        let tampered = RelayCrypto.base64URLEncode(raw)
        XCTAssertThrowsError(try NoticeCrypto.open(tampered, key: key(), hub: hub, dev: dev, noticeID: noticeID))
    }

    func testWrongKeyAndTruncatedInputAreRejected() throws {
        let ct = try NoticeCrypto.seal(sampleJSON(), key: key(), hub: hub, dev: dev, noticeID: noticeID,
                                       nonce: RelayCrypto.randomBytes(12))
        XCTAssertThrowsError(try NoticeCrypto.open(ct, key: Data(repeating: 0x11, count: 32), hub: hub, dev: dev,
                                                   noticeID: noticeID))
        XCTAssertThrowsError(try NoticeCrypto.open(RelayCrypto.base64URLEncode(Data(repeating: 1, count: 20)),
                                                   key: key(), hub: hub, dev: dev, noticeID: noticeID))
        XCTAssertThrowsError(try NoticeCrypto.open("not base64url!", key: key(), hub: hub, dev: dev, noticeID: noticeID))
    }

    func testOversizedCiphertextIsRefusedBeforeDecoding() {
        let huge = String(repeating: "A", count: NoticeCrypto.maxCiphertextCharacters + 1)
        XCTAssertThrowsError(try NoticeCrypto.open(huge, key: key(), hub: hub, dev: dev, noticeID: noticeID)) { error in
            XCTAssertEqual(error as? NoticeCryptoError, .tooLarge)
        }
    }

    func testTheLargestNoticeTheWireBoundAllowsOpensAfterJSONEscaping() throws {
        // The hub escapes a less-than sign as a six-byte JSON escape: a backslash, u, 0, 0, 3 and c. Built from parts
        // here, so the escape is never mistaken for a Swift literal. A 72-character title and a 300-character body of
        // these escapes make 2,530 bytes of JSON, which pads to 2,560: the longest padded plaintext the 3,500-character
        // bound carries.
        let escapedLessThan = String(Character(Unicode.Scalar(UInt8(92)))) + "u003c"
        let title = String(repeating: escapedLessThan, count: 72)
        let body = String(repeating: escapedLessThan, count: 300)
        let json = Data(#"{"v":1,"id":"notice-test-1","kind":"decision_due","title":"\#(title)","body":"\#(body)","category":"YANSU_DECISION","actions":[{"id":"approve","label":"Approve","auth":true},{"id":"decline","label":"Decline","auth":false}],"subjectId":"req-123","createdAt":"2026-10-10T09:00:00Z","expiresAt":"2026-10-10T18:00:00Z"}"#.utf8)
        XCTAssertEqual(NoticeCrypto.pad(json).count, NoticeCrypto.maxPlaintext)

        let ct = try NoticeCrypto.seal(json, key: key(), hub: hub, dev: dev, noticeID: noticeID,
                                       nonce: RelayCrypto.randomBytes(12))
        XCTAssertGreaterThan(ct.utf8.count, 3072, "the 3,072-character cap used to refuse notices this size")
        XCTAssertLessThanOrEqual(ct.utf8.count, NoticeCrypto.maxCiphertextCharacters)

        let opened = try NoticeCrypto.open(ct, key: key(), hub: hub, dev: dev, noticeID: noticeID)
        let plaintext = try NoticeCrypto.decode(opened)
        XCTAssertEqual(plaintext.title, String(repeating: "<", count: 72))
        XCTAssertEqual(plaintext.body, String(repeating: "<", count: 300))
        XCTAssertEqual(plaintext.actions.map(\.id), ["approve", "decline"])
    }

    func testSealRefusesAPlaintextTheWireBoundCannotCarry() {
        let oversized = Data(repeating: 0x7b, count: NoticeCrypto.maxPlaintext + 1)
        XCTAssertThrowsError(try NoticeCrypto.seal(oversized, key: key(), hub: hub, dev: dev, noticeID: noticeID,
                                                   nonce: RelayCrypto.randomBytes(12))) { error in
            XCTAssertEqual(error as? NoticeCryptoError, .tooLarge)
        }
    }

    func testFixedNonceVectorMatchesTheIndependentComputation() throws {
        let ct = try NoticeCrypto.seal(Data(vectorJSON.utf8), key: vectorKey, hub: hub, dev: dev, noticeID: noticeID,
                                       nonce: vectorNonce)
        XCTAssertEqual(ct, vectorCT)
        let opened = try NoticeCrypto.open(vectorCT, key: vectorKey, hub: hub, dev: dev, noticeID: noticeID)
        XCTAssertEqual(String(decoding: opened, as: UTF8.self), vectorJSON)
    }

    func testDecodeIgnoresUnknownFieldsAndToleratesMissingOptionals() throws {
        let json = Data(#"{"v":1,"id":"n1","kind":"brief_ready","title":"Your brief is ready.","category":"YANSU_BRIEF","futureField":{"x":1}}"#.utf8)
        let plaintext = try NoticeCrypto.decode(json)
        XCTAssertNil(plaintext.body)
        XCTAssertNil(plaintext.subjectID)
        XCTAssertNil(plaintext.expiresAt)
        XCTAssertEqual(plaintext.actions, [])
    }

    func testDecodeRejectsAnotherProtocolVersion() {
        let json = Data(#"{"v":2,"id":"n1","kind":"brief_ready","title":"t","category":"YANSU_BRIEF"}"#.utf8)
        XCTAssertThrowsError(try NoticeCrypto.decode(json)) { error in
            XCTAssertEqual(error as? NoticeCryptoError, .unsupportedVersion)
        }
    }

    // mobile-protocol/vectors-v1.json, written by the Go test in wails-gui. Top level: hubId, deviceId,
    // noticeSecret (the X25519 secret S, base64url) and kNotice (the expected notice key). The `notice` entry holds
    // id, nonce (12 bytes), plaintext (the JSON text) and ct (the sealed text). Until the entry exists, this skips.
    func testNoticeEntryInTheSharedVectorsMatchesThePhone() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let url = root.appendingPathComponent("mobile-protocol/vectors-v1.json")
        guard FileManager.default.fileExists(atPath: url.path) else {
            throw XCTSkip("mobile-protocol/vectors-v1.json does not exist yet. The Go test TestMobileProtocolVectors generates it.")
        }
        let document = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
        guard let entry = document["notice"] as? [String: Any] else {
            throw XCTSkip("mobile-protocol/vectors-v1.json has no notice entry yet. Add it with the Go vectors, then this assertion runs.")
        }
        let hubID = try XCTUnwrap(document["hubId"] as? String)
        let deviceID = try XCTUnwrap(document["deviceId"] as? String)
        let secret = try RelayCrypto.decodeKey(try XCTUnwrap(document["noticeSecret"] as? String))
        let expectedKey = try RelayCrypto.decodeKey(try XCTUnwrap(document["kNotice"] as? String))
        let noticeID = try XCTUnwrap(entry["id"] as? String)
        let nonce = try RelayCrypto.base64URLDecode(try XCTUnwrap(entry["nonce"] as? String))
        let plaintext = try XCTUnwrap(entry["plaintext"] as? String)
        let ct = try XCTUnwrap(entry["ct"] as? String)

        let key = RelayCrypto.noticeKey(sharedSecret: secret, hubID: hubID, deviceID: deviceID)
        XCTAssertEqual(key, expectedKey, "kNotice must derive from noticeSecret with the hub and device salt")
        XCTAssertEqual(try NoticeCrypto.seal(Data(plaintext.utf8), key: key, hub: hubID, dev: deviceID,
                                             noticeID: noticeID, nonce: nonce), ct)
        let opened = try NoticeCrypto.open(ct, key: key, hub: hubID, dev: deviceID, noticeID: noticeID)
        let notice = try NoticeCrypto.decode(opened)
        XCTAssertEqual(notice.id, noticeID)
        XCTAssertFalse(notice.title.isEmpty)
    }
}
