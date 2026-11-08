import Foundation

/// The one line on Today for sends the computer did not take in a day (PROTOCOL.md section 23). It names a single
/// held reply in Tasks, counts several, and Show opens the oldest of them wherever it is.
/// The name the phone may show for a person: the display name as the mail shows it ("Sam Lee"). A bare address is
/// never shown, so a name the computer could not read reads as no name (PROTOCOL.md section 23).
enum RecipientName {
    static func name(_ raw: String?) -> String? {
        guard let text = raw?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty, !text.contains("@") else {
            return nil
        }
        return text
    }

    /// The name with non-breaking spaces inside it, so a line wrap never splits "Sam Lee" across two lines.
    static func keepingTogether(_ name: String) -> String {
        name.replacingOccurrences(of: " ", with: "\u{00A0}")
    }
}

struct HeldSendNotice: Equatable {
    enum Place: Equatable {
        case chat(messageID: String)
        case task(taskID: String)
    }

    struct Item: Equatable {
        let place: Place
        let sentAt: Date
        /// The person a reply is to: the display name the computer sent with it, when there is one.
        let person: String?
    }

    let text: String
    /// Where Show takes the person: the oldest held send.
    let oldest: Place

    static func make(_ items: [Item]) -> HeldSendNotice? {
        guard let oldest = items.min(by: { $0.sentAt < $1.sentAt }) else { return nil }
        let text: String
        if items.count == 1, case .task = oldest.place {
            if let person = RecipientName.name(oldest.person) {
                text = String.localizedStringWithFormat(
                    String(localized: "Your computer didn't wake, so your reply to %@ wasn't sent."),
                    RecipientName.keepingTogether(person))
            } else {
                text = String(localized: "Your computer didn't wake, so your reply wasn't sent.")
            }
        } else if items.count == 1 {
            text = String(localized: "Your computer didn't wake, so your message wasn't sent.")
        } else {
            text = String.localizedStringWithFormat(
                String(localized: "Your computer didn't wake, so %lld messages weren't sent."), items.count)
        }
        return HeldSendNotice(text: text, oldest: oldest.place)
    }
}
