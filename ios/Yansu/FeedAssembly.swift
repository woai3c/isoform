import CryptoKit
import Foundation

/// The kinds of card the Feed shows. Each maps one brief section (docs/iphone-muse-design.md section 2.2) and has its
/// own illustration in the asset catalog. Open loops are not here: Goals owns them.
enum FeedCategory: String, CaseIterable, Sendable {
    case moved
    case meetings
    case people
    case ready
    case watching
    case doing
    case schedules
    case learned

    /// The order cards of a day appear in: what changed and who is waiting first, then what Isoform noticed.
    static let order: [FeedCategory] = [.moved, .meetings, .people, .ready, .watching, .doing, .schedules, .learned]

    var label: String {
        switch self {
        case .moved: return String(localized: "Moved")
        case .meetings: return String(localized: "Meetings")
        case .people: return String(localized: "Who you know")
        case .ready: return String(localized: "Ready")
        case .watching: return String(localized: "Watching")
        case .doing: return String(localized: "Doing")
        case .schedules: return String(localized: "Schedules")
        case .learned: return String(localized: "Learned")
        }
    }

    /// The image set for this kind of card.
    var artName: String { "FeedArt-\(rawValue)" }
}

/// One card: a brief item with the brief it came from and the sources behind it.
struct FeedCardModel: Identifiable, Equatable, Sendable {
    let key: String
    let category: FeedCategory
    let section: String
    let item: BriefItem
    let briefID: String
    let localDate: String
    let sources: [BriefEvidence]
    /// How the Mac names the item in feedback: its key, or its id in a brief stored before keys (PROTOCOL.md 20.3).
    let feedbackItemID: String

    var id: String { key }

    /// One-time work is readable here even though its execution chat has no phone entry point.
    var isOneTimeWork: Bool { item.kind == "one_time_work" }

    var statusText: String? {
        guard isOneTimeWork, let status = item.status?.trimmingCharacters(in: .whitespacesAndNewlines),
              !status.isEmpty else { return nil }
        let words = status.replacingOccurrences(of: "_", with: " ")
        return words.prefix(1).uppercased() + words.dropFirst()
    }

    /// The result has no separate destination on the phone, so the card must show all of it.
    var detailLineLimit: Int? { isOneTimeWork ? nil : 2 }

    /// The one memory the item rests on, which "Not right" removes on the Mac.
    var memoryID: String? {
        let ids = Set(sources.filter { $0.sourceKind == "memory" }.compactMap(\.sourceId).filter { !$0.isEmpty })
        return ids.count == 1 ? ids.first : nil
    }
}

/// One day of the Feed: the cards of every brief written that day, each record once.
struct FeedDay: Identifiable, Equatable, Sendable {
    let localDate: String
    let cards: [FeedCardModel]

    var id: String { localDate }
}

/// Builds the Feed from the briefs cached on this iPhone. It keeps the last 14 days, merges the briefs of each day,
/// and shows each record once: the newest brief's copy, on the newest day that has it.
enum FeedAssembly {
    static let windowDays = 14

    /// The sections the Feed reads, in the brief's own order, with the card kind each becomes.
    static let sections: [(name: String, items: KeyPath<BriefSections, [BriefItem]>, category: FeedCategory?)] = [
        ("moved", \.moved, .moved),
        ("doing", \.doing, .doing),
        ("watching", \.watching, .watching),
        ("meetings", \.meetings, .meetings),
        ("people", \.people, .people),
        ("openLoops", \.openLoops, nil),
        ("learned", \.learned, .learned),
        ("deliverables", \.deliverables, .ready),
        ("schedules", \.schedules, .schedules),
    ]

    /// Days newest first, from today back 13 days. Briefs without a local date are left out. A brief dated after
    /// the phone's day comes from a Mac whose timezone is ahead, so it counts as today.
    static func days(from briefs: [MobileBriefV1], today: String, calendar: Calendar = utcCalendar) -> [FeedDay] {
        let earliest = earliestDay(today: today, calendar: calendar)
        let ordered = briefs
            .filter { brief in
                guard let day = brief.localDate.map({ feedDay($0, today: today) }) else { return false }
                return earliest.map { day >= $0 } ?? true
            }
            .sorted { lhs, rhs in
                let left = feedDay(lhs.localDate ?? "", today: today)
                let right = feedDay(rhs.localDate ?? "", today: today)
                if left != right { return left > right }
                return (lhs.asOfDate ?? .distantPast) > (rhs.asOfDate ?? .distantPast)
            }
        var seen = Set<String>()
        var byDay: [String: [FeedCardModel]] = [:]
        var dayOrder: [String] = []
        for brief in ordered {
            guard let day = brief.localDate.map({ feedDay($0, today: today) }) else { continue }
            for card in cards(in: brief) where !seen.contains(card.key) {
                seen.insert(card.key)
                if byDay[day] == nil {
                    byDay[day] = []
                    dayOrder.append(day)
                }
                byDay[day]?.append(card)
            }
        }
        return dayOrder.map { day in FeedDay(localDate: day, cards: sorted(byDay[day] ?? [])) }
    }

    /// Every card one brief offers, in section order.
    static func cards(in brief: MobileBriefV1) -> [FeedCardModel] {
        guard let localDate = brief.localDate else { return [] }
        var out: [FeedCardModel] = []
        for section in sections {
            guard let category = section.category else { continue }
            let items = brief.sections[keyPath: section.items]
            for (item, key) in zip(items, keys(section: section.name, items: items, brief: brief)) {
                guard let title = item.title, !title.isEmpty else { continue }
                var displayedItem = item
                if displayedItem.kind == "one_time_work" {
                    // A cached or newer brief may carry a chat link; the phone cannot open that chat.
                    displayedItem.url = nil
                }
                out.append(FeedCardModel(key: key, category: category, section: section.name, item: displayedItem,
                                         briefID: brief.identifier, localDate: localDate,
                                         sources: brief.sources(for: item.refs),
                                         feedbackItemID: item.key ?? item.id ?? key))
            }
        }
        return out
    }

    /// The items' keys: the Mac's when it sent them, otherwise the same value computed from the evidence.
    static func keys(section: String, items: [BriefItem], brief: MobileBriefV1) -> [String] {
        var seen: [String: Int] = [:]
        return items.map { item in
            if let key = item.key, !key.isEmpty { return key }
            let source = item.refs.first.flatMap { ref in brief.evidence.first { $0.ref == ref } }
            var key = itemKey(section: section, sourceKind: source?.sourceKind ?? "", sourceID: source?.sourceId ?? "")
            seen[key, default: 0] += 1
            if let count = seen[key], count > 1 {
                key += "-\(count)"
            }
            return key
        }
    }

    /// `<section>.<16 hex digits>`: the first 8 bytes of SHA-256 over the source kind, a zero byte and the source id.
    static func itemKey(section: String, sourceKind: String, sourceID: String) -> String {
        var data = Data(sourceKind.utf8)
        data.append(0)
        data.append(contentsOf: Data(sourceID.utf8))
        let digest = SHA256.hash(data: data).prefix(8).map { String(format: "%02x", $0) }.joined()
        return "\(section).\(digest)"
    }

    /// The Feed day a brief belongs to: its own local date, or today when it is dated later.
    static func feedDay(_ localDate: String, today: String) -> String {
        min(localDate, today)
    }

    /// The phone's calendar day, in the form briefs use for `localDate`.
    static func localDate(for date: Date, timeZone: TimeZone = .current) -> String {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timeZone
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: date)
    }

    static let utcCalendar: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(secondsFromGMT: 0) ?? .current
        return calendar
    }()

    private static func earliestDay(today: String, calendar: Calendar) -> String? {
        guard let day = BriefDates.day.date(from: today),
              let earliest = calendar.date(byAdding: .day, value: -(windowDays - 1), to: day) else { return nil }
        return BriefDates.day.string(from: earliest)
    }

    /// Cards of one day by kind, then newest first; cards without a time keep their order at the end of their kind.
    private static func sorted(_ cards: [FeedCardModel]) -> [FeedCardModel] {
        let rank = Dictionary(uniqueKeysWithValues: FeedCategory.order.enumerated().map { ($1, $0) })
        return cards.enumerated().sorted { lhs, rhs in
            let left = rank[lhs.element.category] ?? 0
            let right = rank[rhs.element.category] ?? 0
            if left != right { return left < right }
            let leftAt = lhs.element.item.atDate
            let rightAt = rhs.element.item.atDate
            if leftAt != rightAt {
                return (leftAt ?? .distantPast) > (rightAt ?? .distantPast)
            }
            return lhs.offset < rhs.offset
        }.map(\.element)
    }
}
