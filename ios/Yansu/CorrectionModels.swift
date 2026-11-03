import Foundation

/// Bodies for "Not right" on a memory and for one-time work. Each POST carries the journal key.
struct RejectAttributionRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let reason: String
}

struct RemovedPayload: Decodable, Equatable, Sendable {
    let removed: Bool
}

struct StartOneTimeWorkRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let title: String
    let brief: String
    let workDir: String
    /// Where the work came from (PROTOCOL.md 20.7b). Sent only to a computer that serves task threads: an older one
    /// refuses unknown fields.
    var origin: StartOneTimeWorkOrigin? = nil
}

struct StartOneTimeWorkOrigin: Encodable, Equatable, Sendable {
    let kind: String
    var label: String? = nil
}

/// The form behind "New one-time work" in the Chat tab.
struct OneTimeWorkDraft: Equatable, Sendable {
    var title = ""
    var brief = ""
    var workDir = ""
    var origin: StartOneTimeWorkOrigin?

    var trimmedTitle: String { title.trimmingCharacters(in: .whitespacesAndNewlines) }
    var trimmedBrief: String { brief.trimmingCharacters(in: .whitespacesAndNewlines) }
    var trimmedWorkDir: String { workDir.trimmingCharacters(in: .whitespacesAndNewlines) }

    var canStart: Bool {
        !trimmedTitle.isEmpty && !trimmedBrief.isEmpty
    }

    func request(idempotencyKey: String) -> StartOneTimeWorkRequest {
        StartOneTimeWorkRequest(idempotencyKey: idempotencyKey, title: trimmedTitle, brief: trimmedBrief,
                                workDir: trimmedWorkDir, origin: origin)
    }
}

/// The text the phone sends for a memory correction, after trimming.
enum CorrectionReason {
    static func normalized(_ text: String) -> String {
        text.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Reasons are short: the Mac keeps them with the correction, so the phone caps them.
    static let maxLength = 500
    /// The Mac refuses a reason over 1000 bytes (PROTOCOL.md 15.3 and 20.3), so a reason in Chinese is capped by bytes.
    static let maxBytes = 1000

    /// Trimmed, at most `maxLength` characters and `maxBytes` UTF-8 bytes, cut on a character boundary.
    static func bounded(_ text: String) -> String {
        var result = ""
        var bytes = 0
        for character in normalized(text).prefix(maxLength) {
            let size = String(character).utf8.count
            guard bytes + size <= maxBytes else { break }
            result.append(character)
            bytes += size
        }
        return result
    }
}
