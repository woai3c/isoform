import Foundation

/// The one line on Today for sends the computer did not take in a day (PROTOCOL.md section 23). It names a single
/// held reply in Tasks, counts several, and Show opens the oldest of them wherever it is.
struct HeldSendNotice: Equatable {
    enum Place: Equatable {
        case chat(messageID: String)
        case task(taskID: String)
    }

    struct Item: Equatable {
        let place: Place
        let sentAt: Date
        /// The person a reply is to, when the phone knows it. Replies do not carry one yet, so usually nil.
        let person: String?
    }

    let text: String
    /// Where Show takes the person: the oldest held send.
    let oldest: Place

    static func make(_ items: [Item]) -> HeldSendNotice? {
        guard let oldest = items.min(by: { $0.sentAt < $1.sentAt }) else { return nil }
        let text: String
        if items.count == 1, case .task = oldest.place {
            if let person = oldest.person, !person.isEmpty {
                text = String.localizedStringWithFormat(
                    String(localized: "Your computer didn't wake, so your reply to %@ wasn't sent."), person)
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
