import Foundation

// isoform://pair#<base64url(JSON)> as shown by the Mac (PROTOCOL.md section 3.1).
// Legacy yansu://pair# links remain accepted. The fragment is never sent over
// the network and is never logged.
struct PairingInvitation: Equatable, Sendable {
    let version: Int
    let relayOrigin: URL
    let irohTicket: String?
    let hubID: String
    let pairingID: String
    let secret: Data
    let hubPublicKey: Data
    let expiresAt: Date

    enum ParseError: LocalizedError, Equatable {
        case notAnInvitation
        case malformed
        case unsupportedVersion(Int)
        case expired
        case insecureRelay

        var errorDescription: String? {
            switch self {
            case .notAnInvitation:
                return "That is not an Isoform pairing code. Copy the full link shown on your computer."
            case .malformed:
                return "That pairing code is incomplete. Copy it again from your computer."
            case .unsupportedVersion:
                return "This pairing code needs a newer version of Isoform."
            case .expired:
                return "That pairing code has expired. Choose Create new code on your computer."
            case .insecureRelay:
                return "Isoform only connects to a relay over https."
            }
        }
    }

    private static let prefixes = ["isoform://pair#", "yansu://pair#"]
    private static let maxPayloadBytes = 4096
    private static let maxIrohTicketBytes = 2048

    #if DEBUG
    private static let allowsDevelopmentLoopbackByDefault = true
    #else
    private static let allowsDevelopmentLoopbackByDefault = false
    #endif

    private struct Wire: Decodable {
        let v: Int?
        let relay: String?
        let iroh: String?
        let hasIrohField: Bool
        let hub: String?
        let pairing: String?
        let secret: String?
        let hubPub: String?
        let exp: Int64?

        private enum CodingKeys: String, CodingKey {
            case v, relay, iroh, hub, pairing, secret, hubPub, exp
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            v = try container.decodeIfPresent(Int.self, forKey: .v)
            relay = try container.decodeIfPresent(String.self, forKey: .relay)
            hasIrohField = container.contains(.iroh)
            iroh = hasIrohField ? try container.decode(String.self, forKey: .iroh) : nil
            hub = try container.decodeIfPresent(String.self, forKey: .hub)
            pairing = try container.decodeIfPresent(String.self, forKey: .pairing)
            secret = try container.decodeIfPresent(String.self, forKey: .secret)
            hubPub = try container.decodeIfPresent(String.self, forKey: .hubPub)
            exp = try container.decodeIfPresent(Int64.self, forKey: .exp)
        }
    }

    static func parse(_ text: String, now: Date = Date(),
                      allowsDevelopmentLoopback: Bool = allowsDevelopmentLoopbackByDefault) throws -> PairingInvitation {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let prefix = prefixes.first(where: { trimmed.lowercased().hasPrefix($0) }) else {
            throw ParseError.notAnInvitation
        }
        let payload = String(trimmed.dropFirst(prefix.count))
        guard !payload.isEmpty, payload.utf8.count <= maxPayloadBytes,
              let json = try? RelayCrypto.base64URLDecode(payload),
              let wire = try? JSONDecoder().decode(Wire.self, from: json),
              let version = wire.v, let hub = wire.hub,
              let pairing = wire.pairing, let secretText = wire.secret,
              let hubPubText = wire.hubPub, let exp = wire.exp else {
            throw ParseError.malformed
        }
        guard version == RelayCrypto.protocolVersion else { throw ParseError.unsupportedVersion(version) }
        guard now.timeIntervalSince1970 < Double(exp) else { throw ParseError.expired }
        let irohTicket: String?
        if wire.hasIrohField {
            guard let ticket = wire.iroh, isValidIrohTicket(ticket) else { throw ParseError.malformed }
            irohTicket = ticket
        } else {
            irohTicket = nil
        }
        guard Identifiers.isCanonicalUUID(hub), Identifiers.isCanonicalUUID(pairing) else {
            throw ParseError.malformed
        }
        guard let secret = try? RelayCrypto.decodeKey(secretText),
              let hubPublicKey = try? RelayCrypto.decodeKey(hubPubText) else {
            throw ParseError.malformed
        }
        let origin: URL
        if let relayText = wire.relay {
            origin = try relayOrigin(relayText, allowsLoopback: irohTicket != nil || allowsDevelopmentLoopback)
        } else if irohTicket != nil {
            // A ticket-only invitation never dials this stored placeholder.
            origin = URL(string: "https://iroh.invalid")!
        } else {
            throw ParseError.malformed
        }
        return PairingInvitation(version: version, relayOrigin: origin, irohTicket: irohTicket,
                                 hubID: hub, pairingID: pairing,
                                 secret: secret, hubPublicKey: hubPublicKey,
                                 expiresAt: Date(timeIntervalSince1970: Double(exp)))
    }

    static func isValidIrohTicket(_ ticket: String) -> Bool {
        let bytes = Array(ticket.utf8)
        let prefix = Array("endpoint".utf8)
        guard bytes.count <= maxIrohTicketBytes, bytes.count >= prefix.count + 20,
              bytes.starts(with: prefix) else { return false }
        return bytes.dropFirst(prefix.count).allSatisfy {
            (97...122).contains($0) || (50...55).contains($0)
        }
    }

    static func hasInvitationPrefix(_ text: String) -> Bool {
        prefixes.contains { text.lowercased().hasPrefix($0) }
    }

    // An origin only: scheme and authority, no path, query, fragment or userinfo.
    static func relayOrigin(_ text: String) throws -> URL {
        try relayOrigin(text, allowsLoopback: allowsDevelopmentLoopbackByDefault)
    }

    static func relayOrigin(_ text: String, allowsLoopback: Bool) throws -> URL {
        guard let components = URLComponents(string: text),
              let scheme = components.scheme?.lowercased(),
              let host = components.host, !host.isEmpty,
              components.user == nil, components.password == nil,
              components.query == nil, components.fragment == nil,
              components.path.isEmpty || components.path == "/" else {
            throw ParseError.malformed
        }
        let loopback = ["127.0.0.1", "localhost", "::1"].contains(host.lowercased())
        let loopbackAllowed = loopback && allowsLoopback
        guard scheme == "https" || (scheme == "http" && loopbackAllowed) else {
            throw ParseError.insecureRelay
        }
        var origin = URLComponents()
        origin.scheme = scheme
        origin.host = host
        origin.port = components.port
        guard let url = origin.url else { throw ParseError.malformed }
        return url
    }
}

enum Identifiers {
    // UUID v4 as the protocol writes it: lowercase, 36 characters.
    static func isCanonicalUUID(_ value: String) -> Bool {
        let bytes = Array(value.utf8)
        guard bytes.count == 36 else { return false }
        for (index, byte) in bytes.enumerated() {
            if [8, 13, 18, 23].contains(index) {
                guard byte == 45 else { return false }
            } else {
                guard (48...57).contains(byte) || (97...102).contains(byte) else { return false }
            }
        }
        return true
    }

    // Brief ids look like 2026-10-09-morning or 2026-10-09-on_request-071500. Anything
    // else is refused before it can reach a request path.
    static func isSafeBriefID(_ value: String) -> Bool {
        guard !value.isEmpty, value.utf8.count <= 80 else { return false }
        return value.utf8.allSatisfy {
            (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) || $0 == 45 || $0 == 95
        }
    }

    /// Ids the Mac accepts in a request path (chats, permission requests, memories, notification kinds): 1 to 128
    /// printable ASCII bytes with no space, slash, question mark, hash or percent sign, which would change how the
    /// hub reads the path.
    static func isSafePathID(_ value: String) -> Bool {
        guard !value.isEmpty, value.utf8.count <= 128 else { return false }
        return value.utf8.allSatisfy { byte in
            (0x21...0x7E).contains(byte) && ![0x2F, 0x3F, 0x23, 0x25].contains(byte)
        }
    }
}
