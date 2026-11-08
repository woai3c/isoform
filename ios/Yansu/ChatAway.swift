import Foundation

/// Why the computer is away, as Chat's thread line says it (phone-connection v1.1.0 section 2).
enum ChatAwayCause: String, Codable, CaseIterable, Sendable, Identifiable {
    var id: String { rawValue }

    case asleep
    case asleepBattery = "asleep_battery"
    case lidClosed = "lid_closed"
    case off
    case closed
    case notAnswering = "not_answering"
    case phoneOffline = "phone_offline"
    case cantConnect = "cant_connect"

    /// Maps the Today connection cause and the relay goodbye's optional reason. No reason keeps the cause.
    init(connection: TodayStore.ConnectionCause, reason: String?) {
        switch connection {
        case .sleeping:
            switch reason {
            case "battery_floor", "low_power": self = .asleepBattery
            case "lid_closed": self = .lidClosed
            default: self = .asleep
            }
        case .shuttingDown: self = .off
        case .closing: self = .closed
        case .notAnswering: self = .notAnswering
        case .phoneOffline: self = .phoneOffline
        case .cantConnect: self = .cantConnect
        }
    }

    /// The line without "More". After 24 hours it says since when.
    func line(since start: Date?, now: Date, locale: Locale = .current, calendar: Calendar = .current) -> String {
        guard let start, now.timeIntervalSince(start) >= ChatAwayPeriod.longAway else { return shortLine }
        let when = ChatAwayCopy.since(start, now: now, locale: locale, calendar: calendar)
        switch self {
        case .asleep:
            return String(localized: "Your computer has been asleep since \(when). Isoform answers when it wakes.")
        case .asleepBattery:
            return String(localized: "Your computer has been asleep since \(when) to save its battery. Isoform answers when it wakes.")
        case .lidClosed:
            return String(localized: "Your computer's lid has been closed since \(when). Isoform answers when you open it.")
        case .off:
            return String(localized: "Your computer has been off since \(when). Isoform answers when it's back on.")
        case .closed:
            return String(localized: "Isoform has been closed on your computer since \(when). It answers when you open it.")
        case .notAnswering:
            return String(localized: "Your computer has been away since \(when). Isoform answers when it's back.")
        case .phoneOffline:
            return String(localized: "This iPhone has been offline since \(when). Your message goes out when it's back online.")
        case .cantConnect:
            return String(localized: "Isoform hasn't been able to connect since \(when). Your message goes out when it does.")
        }
    }

    var shortLine: String {
        switch self {
        case .asleep: return String(localized: "Your computer is asleep. Isoform answers when it wakes.")
        case .asleepBattery: return String(localized: "Your computer went to sleep to save its battery. Isoform answers when it wakes.")
        case .lidClosed: return String(localized: "Your computer's lid is closed. Isoform answers when you open it.")
        case .off: return String(localized: "Your computer is off. Isoform answers when it's back on.")
        case .closed: return String(localized: "Isoform is closed on your computer. It answers when you open it.")
        case .notAnswering: return String(localized: "Your computer isn't answering. Isoform answers when it's back.")
        case .phoneOffline: return String(localized: "This iPhone is offline. Your message goes out when it's back online.")
        case .cantConnect: return String(localized: "Isoform can't connect right now. Your message goes out when it does.")
        }
    }

    /// What the More sheet says to do.
    var directions: String {
        switch self {
        case .asleep, .asleepBattery, .lidClosed: return String(localized: "connection.sleeping.directions")
        case .off: return String(localized: "connection.shuttingDown.directions")
        case .closed: return String(localized: "connection.closed.directions")
        case .notAnswering: return String(localized: "connection.notAnswering.directions")
        case .phoneOffline: return String(localized: "connection.phoneOffline.directions")
        case .cantConnect: return String(localized: "connection.cantConnect.directions")
        }
    }
}

/// One stretch of time the computer was away while messages waited. Saved with the chat, so the line under the
/// first waiting message survives a relaunch, and stays green with the send times once the computer is back.
struct ChatAwayPeriod: Codable, Equatable, Sendable, Identifiable {
    static let longAway: TimeInterval = 24 * 60 * 60

    /// The away line for this stretch. "Since" only with the goodbye time, after 24 hours; otherwise the timeless line.
    func lineText(now: Date, locale: Locale = .current, calendar: Calendar = .current) -> String {
        cause.line(since: sinceKnown == true ? startedAt : nil, now: now, locale: locale, calendar: calendar)
    }

    let id: String
    var cause: ChatAwayCause
    let startedAt: Date
    var endedAt: Date? = nil
    /// True when `startedAt` is the relay's goodbye time. Without it, Chat never says "since": it uses the timeless
    /// line, as Today uses "as of" (PROTOCOL.md section 23). Optional so older saved periods still decode.
    var sinceKnown: Bool? = nil
    /// The waiting messages, in the order they were typed.
    var messageIDs: [String] = []
    /// When each message went out, by message id.
    var sentAt: [String: Date] = [:]
    /// The stored item seq of each message that went out, by message id.
    var seqs: [String: Int] = [:]
    /// When each message that went out was typed, by message id, for "You asked at".
    var typedAt: [String: Date] = [:]

    var isOver: Bool { endedAt != nil }

    func typedAt(seq: Int) -> Date? {
        seqs.first { $0.value == seq }.flatMap { typedAt[$0.key] }
    }
    var waitedMoreThanOne: Bool { messageIDs.count > 1 }

    /// The green line once every message went out: the latest send time.
    func backLine(locale: Locale = .current) -> String? {
        guard isOver, !messageIDs.isEmpty, let last = messageIDs.compactMap({ sentAt[$0] }).max(),
              messageIDs.allSatisfy({ sentAt[$0] != nil }) else { return nil }
        let time = ChatAwayCopy.time(last, locale: locale)
        return messageIDs.count == 1
            ? String(localized: "Your computer is back. This went out at \(time).")
            : String(localized: "Your computer is back. These went out at \(time).")
    }
}

enum ChatAwayCopy {
    static func time(_ date: Date, locale: Locale) -> String {
        date.formatted(.dateTime.hour().minute().locale(locale))
    }

    static func isChinese(_ locale: Locale) -> Bool { locale.language.languageCode?.identifier == "zh" }

    /// "yesterday at 10:02 PM" or "昨天晚上 10:02".
    static func since(_ start: Date, now: Date, locale: Locale, calendar: Calendar) -> String {
        if isChinese(locale) {
            let hour = calendar.component(.hour, from: start)
            let minute = calendar.component(.minute, from: start)
            let clock = String(format: "%d:%02d", hour % 12 == 0 ? 12 : hour % 12, minute)
            let period: String
            switch hour {
            case 0..<5: period = "凌晨"
            case 5..<9: period = "早上"
            case 9..<12: period = "上午"
            case 12..<13: period = "中午"
            case 13..<18: period = "下午"
            default: period = "晚上"
            }
            let day: String
            if calendar.isDate(start, inSameDayAs: now) { day = "今天" }
            else if let yesterday = calendar.date(byAdding: .day, value: -1, to: now),
                    calendar.isDate(start, inSameDayAs: yesterday) { day = "昨天" }
            else { day = start.formatted(.dateTime.month().day().locale(locale)) }
            return "\(day)\(period) \(clock) "
        }
        let clock = time(start, locale: locale)
        if calendar.isDate(start, inSameDayAs: now) { return clock }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(start, inSameDayAs: yesterday) {
            return "yesterday at \(clock)"
        }
        return "\(start.formatted(.dateTime.month(.abbreviated).day().locale(locale))) at \(clock)"
    }

    /// The quiet line over an answer: "You asked at 7:42 AM · Can you move".
    static func askedAt(_ asked: Date, question: String, locale: Locale) -> String {
        let words = question.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        let start = words.count > 40 ? String(words.prefix(40)) + "…" : words
        let when = time(asked, locale: locale)
        return String(localized: "You asked at \(when) · \(start)")
    }

    /// Whether an answer gets the quiet line: several messages waited, or it came more than an hour later.
    static func showsAskedAt(askedAt: Date, answeredAt: Date?, severalWaited: Bool) -> Bool {
        if severalWaited { return true }
        guard let answeredAt else { return false }
        return answeredAt.timeIntervalSince(askedAt) > 60 * 60
    }
}
