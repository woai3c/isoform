import Foundation

enum NoticeCryptoError: Error, Equatable {
    case tooLarge
    case malformedPlaintext
    case unsupportedVersion
}

// ChaCha20-Poly1305 for sealed notices (PROTOCOL.md section 14). Wire format of `yansu.ct`:
// base64url(nonce12 || ciphertext || tag16), AAD "yansu-notice-v1|<hub>|<dev>|<noticeId>", and a plaintext
// JSON object padded with trailing spaces to a multiple of 64 bytes. The phone opens notices; sealing exists
// for the test vectors and the simulator fixture.
enum NoticeCrypto {
    static let version = 1
    static let padBlock = 64
    static let nonceLength = 12
    static let tagLength = 16
    // PROTOCOL.md 14.2: the wire form is at most 3,500 base64url characters, which carries at most 2,625 raw bytes.
    static let maxCiphertextCharacters = 3500
    // The raw bytes minus the nonce and tag leave 2,597 bytes of padded plaintext. The largest multiple of padBlock
    // that fits is 2,560, so no valid notice has a longer padded plaintext. Escaped JSON counts toward it.
    static let maxPlaintext = (maxCiphertextCharacters * 3 / 4 - nonceLength - tagLength) / padBlock * padBlock

    static func aad(hub: String, dev: String, noticeID: String) -> String {
        "yansu-notice-v1|\(hub)|\(dev)|\(noticeID)"
    }

    static func pad(_ json: Data) -> Data {
        let remainder = json.count % padBlock
        guard remainder != 0 else { return json }
        var padded = json
        padded.append(contentsOf: [UInt8](repeating: 0x20, count: padBlock - remainder))
        return padded
    }

    /// Removes the trailing spaces added by `pad`. The padded input must be block-aligned and within the size limit,
    /// and the result must be non-empty.
    static func unpad(_ padded: Data) throws -> Data {
        guard !padded.isEmpty, padded.count % padBlock == 0, padded.count <= maxPlaintext else {
            throw NoticeCryptoError.malformedPlaintext
        }
        var bytes = [UInt8](padded)
        while bytes.last == 0x20 {
            bytes.removeLast()
        }
        guard !bytes.isEmpty else { throw NoticeCryptoError.malformedPlaintext }
        return Data(bytes)
    }

    static func seal(_ json: Data, key: Data, hub: String, dev: String, noticeID: String, nonce: Data) throws -> String {
        guard nonce.count == nonceLength else { throw RelayCryptoError.invalidKeyLength }
        let padded = pad(json)
        guard padded.count <= maxPlaintext else { throw NoticeCryptoError.tooLarge }
        let wire = try RelayCrypto.seal(padded, key: key, nonce: nonce,
                                        aad: aad(hub: hub, dev: dev, noticeID: noticeID))
        return RelayCrypto.base64URLEncode(nonce + wire)
    }

    /// Returns the unpadded JSON. Any malformed or tampered input throws and the caller keeps the fallback.
    static func open(_ ct: String, key: Data, hub: String, dev: String, noticeID: String) throws -> Data {
        guard ct.utf8.count <= maxCiphertextCharacters else { throw NoticeCryptoError.tooLarge }
        let raw = try RelayCrypto.base64URLDecode(ct)
        guard raw.count > nonceLength + tagLength else { throw RelayCryptoError.authenticationFailed }
        let nonce = Data(raw.prefix(nonceLength))
        let wire = Data(raw.dropFirst(nonceLength))
        let padded = try RelayCrypto.unseal(wire, key: key, nonce: nonce,
                                            aad: aad(hub: hub, dev: dev, noticeID: noticeID))
        return try unpad(padded)
    }

    static func decode(_ json: Data) throws -> NoticePlaintext {
        let plaintext = try JSONDecoder().decode(NoticePlaintext.self, from: json)
        guard plaintext.version == version else { throw NoticeCryptoError.unsupportedVersion }
        return plaintext
    }
}
