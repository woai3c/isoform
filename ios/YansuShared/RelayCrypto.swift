import CryptoKit
import Foundation

// Implements mobile-protocol/PROTOCOL.md sections 2, 3 and 4. Every string that
// feeds a key derivation or an AAD is ASCII and byte-exact; change nothing here
// without changing the protocol document first.
enum RelayCryptoError: Error, Equatable {
    case invalidEncoding
    case invalidKeyLength
    case authenticationFailed
    case sequenceReplayed
    case unexpectedDirection
    case malformedPlaintext
    case sequenceExhausted
}

enum RelayCrypto {
    static let protocolVersion = 1
    // seq values are bounded to 2^53-1 so they survive JSON number handling (PROTOCOL.md section 4).
    static let maxSequence: UInt64 = 9_007_199_254_740_991

    // MARK: Encodings

    static func base64URLEncode(_ data: Data) -> String {
        data.base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    // Accepts only canonical base64url without padding, so a value that round-trips
    // through encode is the only accepted spelling.
    static func base64URLDecode(_ text: String) throws -> Data {
        guard !text.isEmpty, text.utf8.allSatisfy({ (48...57).contains($0) || (65...90).contains($0) ||
            (97...122).contains($0) || $0 == 45 || $0 == 95 }) else {
            throw RelayCryptoError.invalidEncoding
        }
        let standard = text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        let padding = String(repeating: "=", count: (4 - standard.count % 4) % 4)
        guard let data = Data(base64Encoded: standard + padding), base64URLEncode(data) == text else {
            throw RelayCryptoError.invalidEncoding
        }
        return data
    }

    static func decodeKey(_ text: String) throws -> Data {
        let data = try base64URLDecode(text)
        guard data.count == 32 else { throw RelayCryptoError.invalidKeyLength }
        return data
    }

    static func sha256Hex(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    static func randomBytes(_ count: Int) -> Data {
        var generator = SystemRandomNumberGenerator()
        return Data((0..<count).map { _ in UInt8.random(in: UInt8.min...UInt8.max, using: &generator) })
    }

    // MARK: Primitives

    static func hmacSHA256(key: Data, message: Data) -> Data {
        Data(HMAC<SHA256>.authenticationCode(for: message, using: SymmetricKey(data: key)))
    }

    static func hkdf(ikm: Data, salt: Data, info: String, length: Int) -> Data {
        let key = HKDF<SHA256>.deriveKey(inputKeyMaterial: SymmetricKey(data: ikm), salt: salt,
                                         info: Data(info.utf8), outputByteCount: length)
        return key.withUnsafeBytes { Data($0) }
    }

    // X25519 shared secret S. The same value comes out of either side's private key.
    static func sharedSecret(privateKey: Data, peerPublicKey: Data) throws -> Data {
        let secret = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: privateKey)
        let peer = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: peerPublicKey)
        let shared = try secret.sharedSecretFromKeyAgreement(with: peer)
        return shared.withUnsafeBytes { Data($0) }
    }

    static func publicKey(forPrivateKey privateKey: Data) throws -> Data {
        try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: privateKey).publicKey.rawRepresentation
    }

    static func makeDeviceKeyPair() -> (privateKey: Data, publicKey: Data) {
        let key = Curve25519.KeyAgreement.PrivateKey()
        return (key.rawRepresentation, key.publicKey.rawRepresentation)
    }

    // ChaCha20-Poly1305 IETF with a 12-byte nonce. Wire format is ciphertext || tag.
    static func seal(_ plaintext: Data, key: Data, nonce: Data, aad: String) throws -> Data {
        let sealed = try ChaChaPoly.seal(plaintext, using: SymmetricKey(data: key),
                                         nonce: ChaChaPoly.Nonce(data: nonce), authenticating: Data(aad.utf8))
        // Data(...) makes a zero-based copy. Concatenated slices keep their base indices, and integer subscripts trap on them.
        return Data(sealed.ciphertext + sealed.tag)
    }

    static func unseal(_ wire: Data, key: Data, nonce: Data, aad: String) throws -> Data {
        guard wire.count >= 16 else { throw RelayCryptoError.authenticationFailed }
        let split = wire.count - 16
        do {
            let box = try ChaChaPoly.SealedBox(nonce: ChaChaPoly.Nonce(data: nonce),
                                               ciphertext: wire.prefix(split), tag: wire.suffix(16))
            return Data(try ChaChaPoly.open(box, using: SymmetricKey(data: key), authenticating: Data(aad.utf8)))
        } catch {
            throw RelayCryptoError.authenticationFailed
        }
    }

    // Nonce for a pairing message (section 3.3): eleven zero bytes then one byte.
    static func pairingNonce(_ last: UInt8) -> Data {
        Data(repeating: 0, count: 11) + Data([last])
    }

    // Envelope nonce (section 4): four zero bytes then big-endian uint64 seq.
    static func envelopeNonce(seq: UInt64) -> Data {
        var bigEndian = seq.bigEndian
        return Data(repeating: 0, count: 4) + withUnsafeBytes(of: &bigEndian) { Data($0) }
    }

    // MARK: Pairing (sections 3.2 to 3.4)

    static func claimToken(secret: Data, pairingID: String) -> Data {
        hmacSHA256(key: secret, message: Data("yansu-claim-v1|\(pairingID)".utf8))
    }

    static func claimTokenHash(_ claimToken: Data) -> String {
        sha256Hex(claimToken)
    }

    static func pairingKey(devicePrivate: Data, hubPublic: Data, secret: Data,
                           hubID: String, pairingID: String) throws -> Data {
        let shared = try sharedSecret(privateKey: devicePrivate, peerPublicKey: hubPublic)
        return hkdf(ikm: shared, salt: secret, info: "yansu-pair-v1|\(hubID)|\(pairingID)", length: 32)
    }

    static func claimAAD(hubID: String, pairingID: String) -> String {
        "yansu-claim-v1|\(hubID)|\(pairingID)"
    }

    static func approvalAAD(hubID: String, pairingID: String) -> String {
        "yansu-approval-v1|\(hubID)|\(pairingID)"
    }

    static func sealClaim(_ plaintext: Data, kPair: Data, hubID: String, pairingID: String) throws -> Data {
        try seal(plaintext, key: kPair, nonce: pairingNonce(0x01), aad: claimAAD(hubID: hubID, pairingID: pairingID))
    }

    static func openApproval(_ wire: Data, kPair: Data, hubID: String, pairingID: String) throws -> Data {
        try unseal(wire, key: kPair, nonce: pairingNonce(0x02), aad: approvalAAD(hubID: hubID, pairingID: pairingID))
    }

    // Six digits both sides show. The big-endian uint32 is taken modulo 1000000.
    static func confirmationCode(kPair: Data) -> String {
        let bytes = hkdf(ikm: kPair, salt: Data(), info: "yansu-confirm-v1", length: 4)
        let value = bytes.reduce(UInt32(0)) { ($0 << 8) | UInt32($1) }
        let digits = String(value % 1_000_000)
        return String(repeating: "0", count: 6 - digits.count) + digits
    }

    // MARK: Session (sections 4 and 9)

    struct SessionKeys: Equatable {
        let d2h: Data
        let h2d: Data
    }

    static func sessionKeys(devicePrivate: Data, hubPublic: Data, hubID: String, deviceID: String) throws -> SessionKeys {
        let shared = try sharedSecret(privateKey: devicePrivate, peerPublicKey: hubPublic)
        let salt = Data("yansu-mobile-v1|\(hubID)|\(deviceID)".utf8)
        return SessionKeys(
            d2h: hkdf(ikm: shared, salt: salt, info: "d2h", length: 32),
            h2d: hkdf(ikm: shared, salt: salt, info: "h2d", length: 32)
        )
    }

    // kNotice (sealed notices, PROTOCOL.md section 14): HKDF over the same secret S as the session keys,
    // with info "notice". Derived once at pairing and kept in the shared Keychain for the extension.
    static func noticeKey(sharedSecret: Data, hubID: String, deviceID: String) -> Data {
        hkdf(ikm: sharedSecret, salt: Data("yansu-mobile-v1|\(hubID)|\(deviceID)".utf8), info: "notice", length: 32)
    }

    static func noticeKey(devicePrivate: Data, hubPublic: Data, hubID: String, deviceID: String) throws -> Data {
        let shared = try sharedSecret(privateKey: devicePrivate, peerPublicKey: hubPublic)
        return noticeKey(sharedSecret: shared, hubID: hubID, deviceID: deviceID)
    }

    static func envelopeAAD(hub: String, dev: String, dir: String, seq: UInt64, kind: String) -> String {
        "yansu-env-v1|\(protocolVersion)|\(hub)|\(dev)|\(dir)|\(seq)|\(kind)"
    }

    static func sealEnvelope(_ plaintext: Data, key: Data, hub: String, dev: String,
                             dir: String, seq: UInt64, kind: String) throws -> Data {
        try seal(plaintext, key: key, nonce: envelopeNonce(seq: seq),
                 aad: envelopeAAD(hub: hub, dev: dev, dir: dir, seq: seq, kind: kind))
    }

    static func openEnvelope(_ wire: Data, key: Data, hub: String, dev: String,
                             dir: String, seq: UInt64, kind: String) throws -> Data {
        try unseal(wire, key: key, nonce: envelopeNonce(seq: seq),
                 aad: envelopeAAD(hub: hub, dev: dev, dir: dir, seq: seq, kind: kind))
    }

    // Brief plaintext: UTF-8 JSON padded with 0x00 to a multiple of 4096 bytes.
    static let briefPadBlock = 4096
    static let briefMaxPlaintext = 256 * 1024

    static func padBrief(_ json: Data) -> Data {
        let remainder = json.count % briefPadBlock
        var padded = Data(json)
        guard remainder != 0 else { return padded }
        padded.append(Data(repeating: 0, count: briefPadBlock - remainder))
        return padded
    }

    static func unpadBrief(_ padded: Data) throws -> Data {
        var end = padded.endIndex
        while end > padded.startIndex, padded[padded.index(before: end)] == 0 {
            end = padded.index(before: end)
        }
        guard end > padded.startIndex, end - padded.startIndex <= briefMaxPlaintext else {
            throw RelayCryptoError.malformedPlaintext
        }
        return Data(padded[padded.startIndex..<end])
    }
}
