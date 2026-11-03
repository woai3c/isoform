import Foundation

enum YourAgentRules {
    static let reserved: Set<String> = Set("isoform agents agent admin administrator support help www api app apps mail email root system status blog docs about login signup settings bo team staff official security billing".split(separator: " ").map(String.init))
    static func normalize(_ value: String) -> String {
        value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    }
    static func filterName(_ value: String) -> String {
        var result = ""
        for character in value.lowercased() {
            if character.unicodeScalars.count == 1,
               let scalar = character.unicodeScalars.first,
               (97...122).contains(scalar.value) || (48...57).contains(scalar.value) {
                result.append(character)
            } else if character == ".", result.last != "." {
                result.append(character)
            }
        }
        return result
    }
    static func needsNameRules(_ value: String) -> Bool {
        !(3...30).contains(value.count) || value.first == "." || value.last == "."
    }
    private static func matches(_ value: String, _ pattern: String) -> Bool {
        let range = NSRange(value.startIndex..., in: value)
        return (try? NSRegularExpression(pattern: pattern).firstMatch(in: value, range: range)?.range) == range
    }
    static func validName(_ value: String) -> Bool {
        matches(value, "^[a-z0-9](?:[a-z0-9]|\\.(?=[a-z0-9])){2,29}$") && !reserved.contains(value)
    }
    static func validEmail(_ value: String) -> Bool {
        value.count <= 254 && matches(value, "^[^\\s@]+@[^\\s@]+\\.[^\\s@]+$")
    }
    /// Parses the server's ISO 8601 times, with or without fractional seconds.
    static func date(_ text: String) -> Date? {
        if let date = ISO8601DateFormatter().date(from: text) { return date }
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: text)
    }
}

struct AgentReservation: Codable, Equatable, Sendable {
    /// Missing from an older server: a missing status means verified.
    enum Status: String, Codable, Equatable, Sendable {
        case pending, verified, unconfirmed, choice
    }
    var name: String
    var email: String
    var reservedAt: String
    var status: Status?
    /// Set while a code is out (pending, choice, and unconfirmed with a code out). ISO 8601 UTC.
    var expiresAt: String?
    /// Verified only: the place in line.
    var place: Int?
    /// Choice only: the page this email already has.
    var oldName: String?

    init(name: String, email: String, reservedAt: String, status: Status? = nil,
         expiresAt: String? = nil, place: Int? = nil, oldName: String? = nil) {
        self.name = name; self.email = email; self.reservedAt = reservedAt
        self.status = status; self.expiresAt = expiresAt; self.place = place; self.oldName = oldName
    }

    var state: Status { status ?? .verified }
    var expiryDate: Date? { expiresAt.flatMap(YourAgentRules.date) }
    /// The name is held and lapses when expiresAt passes: a pending request, a choice, or an earlier request with a code out.
    var holdsName: Bool {
        state == .pending || state == .choice || (state == .unconfirmed && expiresAt != nil)
    }
    /// A 6-digit code is out for this request, so the code step shows.
    var codeIsOut: Bool {
        state == .pending || (state == .unconfirmed && expiresAt != nil)
    }
}

struct YourAgentCopy: Codable, Equatable, Sendable {
    struct Specialty: Codable, Equatable, Sendable {
        let name: String
        let seen: Int
    }
    var specialties: [Specialty] = []
    var taste: [String] = []
    var stillLearning = false
    var nameCandidates: [String] = []
    var hub: String
    var token: String
    var reservation: AgentReservation?

    enum CodingKeys: String, CodingKey {
        case specialties, taste, stillLearning, nameCandidates, hub, token, reservation
    }
    init(hub: String, token: String) { self.hub = hub; self.token = token }
    init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        specialties = try values.decodeIfPresent([Specialty].self, forKey: .specialties) ?? []
        taste = try values.decodeIfPresent([String].self, forKey: .taste) ?? []
        stillLearning = try values.decodeIfPresent(Bool.self, forKey: .stillLearning) ?? false
        nameCandidates = try values.decodeIfPresent([String].self, forKey: .nameCandidates) ?? []
        hub = try values.decode(String.self, forKey: .hub)
        token = try values.decode(String.self, forKey: .token)
        reservation = try values.decodeIfPresent(AgentReservation.self, forKey: .reservation)
    }
}

protocol YourAgentHubAPI: Sendable {
    func yourAgent() async throws -> YourAgentCopy
}

extension HubChannel: YourAgentHubAPI {
    func yourAgent() async throws -> YourAgentCopy {
        try await perform(.yourAgent).payload(YourAgentCopy.self)
    }
}
