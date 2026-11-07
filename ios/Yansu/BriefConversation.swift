import Foundation

/// The computer issues this opaque revision; the phone never reconstructs brief evidence or context.
struct ChatBriefReference: Codable, Equatable, Sendable {
    let briefId: String
    let briefVersion: String
    var offerId: String? = nil
    /// The brief card whose words a tap put in the box (backlog item 58), and those words as shown: the computer
    /// reads the card's sources again at Send and refuses the shown words sent unedited once they are stale.
    var cardId: String? = nil
    var cardMessage: String? = nil
    var ask: Bool? = nil
    var paragraph: Int? = nil
    var paragraphText: String? = nil
    var weekTopicId: String? = nil
    var weekMessage: String? = nil
}

struct MobileBriefOffer: Codable, Equatable, Sendable, Identifiable {
    let id: String
    let label: String
    let prompt: String
}

struct SavedBriefAsk: Decodable, Equatable, Sendable {
    let question: String
    let answer: String
}

struct SavedBriefAskPayload: Decodable, Equatable, Sendable {
    let answer: SavedBriefAsk?
}

struct SendBriefMessageRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let text: String
    let briefId: String
    let briefVersion: String
    let offerId: String?
    var cardId: String? = nil
    var cardMessage: String? = nil
    var ask: Bool? = nil
    var paragraph: Int? = nil
    var paragraphText: String? = nil
    var weekTopicId: String? = nil
    var weekMessage: String? = nil
}

/// "Talk about this brief": the computer says the brief's opener first in the chat (section 15.3).
struct BriefOpenerRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let briefId: String
    let briefVersion: String
}

/// Tapping a paragraph lets the computer quote its own delivered words in the main chat.
struct BriefParagraphRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let briefId: String
    let briefVersion: String
    let paragraph: Int
    /// Only on the ask after a 409, so a computer from before it never sees the field.
    var text: String? = nil
}

extension MobileBriefV1 {
    var chatReference: ChatBriefReference? {
        guard let conversationKey, !conversationKey.isEmpty, Identifiers.isSafePathID(identifier),
              (yourWeek != nil || narrative.contains(where: { !($0.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty })) else { return nil }
        return ChatBriefReference(briefId: identifier, briefVersion: conversationKey)
    }
}

enum BriefConversationCopy {
    static var changed: String { String(localized: "This brief changed. Refresh and try again.") }
    static var finishDraft: String {
        String(localized: "Send or discard your current draft before discussing another brief or idea.")
    }
    static var unavailable: String {
        String(localized: "Talk about this works when your computer is back.")
    }
    /// The computer's gate refused the brief (withheld, withdrawn, Closed Eyes, or no such brief). One line, no reason,
    /// no Try again: asking again does not change a gate's answer.
    static var gateRefused: String { String(localized: "Couldn't open this conversation.") }
    /// The computer answered but has no conversation route yet (an older Mac). Not an error code, not the
    /// unreachable line: the computer did answer.
    static var computerTooOld: String {
        String(localized: "Talk about this works after Isoform updates on your computer.")
    }
    static var offline: String { String(localized: "Your computer is offline.") }
    /// A question, an answer or "Talk about this brief" tapped while the computer is offline.
    static var waitsForComputer: String {
        String(localized: "Your computer is offline. Questions and answers wait until it is back.")
    }
}
