import Foundation

// MobileBriefV1 from mobile-protocol/PROTOCOL.md section 8. Decoding is tolerant: every field is
// optional, arrays default to empty, and unknown fields are ignored, so a newer Mac can send a
// brief this phone still reads. Encoding writes the same shape back into the local cache.

struct MobileBriefV1: Codable, Equatable, Sendable {
    var version: Int? = nil
    var briefId: String? = nil
    var slot: String? = nil
    var displaySlot: String? = nil
    var localDate: String? = nil
    var timezone: String? = nil
    var windowStart: String? = nil
    var asOf: String? = nil
    var hubName: String? = nil
    var coldStart: Bool? = nil
    /// morning, evening, week or on_request: names the brief's title (PROTOCOL.md 8.1). Nil on a Mac before 1.0.
    var kind: String? = nil
    /// When a scheduled brief was due (RFC 3339). Nil for a brief asked for by the person.
    var dueAt: String? = nil
    var narrativeStatus: String? = nil
    var narrative: [BriefParagraph] = []
    var conversationKey: String? = nil
    var offers: [MobileBriefOffer] = []
    /// The brief's prepared clicks (section 8 `cards`): buttons, row questions and Isoform's opener. The phone
    /// shows their words as given and never writes its own.
    var cards: [MobileBriefCard] = []
    /// No longer shown: the Desk it counted is gone, and the Mac sends 0. Decoded so older briefs still read the same.
    var needsYouCount: Int? = nil
    /// Present when the brief was made other than on time: "mac_asleep", "late", or "made_ahead" (a brief made before its slot, P157).
    var generation: BriefGeneration? = nil
    var sections: BriefSections = BriefSections()
    var dayShape: BriefDayShape? = nil
    var counts: BriefCounts? = nil
    var evidence: [BriefEvidence] = []
    var limits: BriefLimits? = nil
    /// The spoken brief stored on the Mac, with the sealed relay copy when one was uploaded (section 8 `audio`).
    var audio: BriefAudio? = nil
    var yourWeek: MobileBriefWeek? = nil
    var weekQuestion: MorningBriefWeekQuestion? = nil
}

struct MorningBriefWeekQuestion: Codable, Equatable, Sendable {
    var weekStart: String
    var recommendation: String?
}

extension MobileBriefV1 {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        version = try container.decodeIfPresent(Int.self, forKey: .version)
        briefId = try container.decodeIfPresent(String.self, forKey: .briefId)
        slot = try container.decodeIfPresent(String.self, forKey: .slot)
        displaySlot = try container.decodeIfPresent(String.self, forKey: .displaySlot)
        localDate = try container.decodeIfPresent(String.self, forKey: .localDate)
        timezone = try container.decodeIfPresent(String.self, forKey: .timezone)
        windowStart = try container.decodeIfPresent(String.self, forKey: .windowStart)
        asOf = try container.decodeIfPresent(String.self, forKey: .asOf)
        hubName = try container.decodeIfPresent(String.self, forKey: .hubName)
        coldStart = try container.decodeIfPresent(Bool.self, forKey: .coldStart)
        kind = try? container.decodeIfPresent(String.self, forKey: .kind)
        dueAt = try? container.decodeIfPresent(String.self, forKey: .dueAt)
        narrativeStatus = try container.decodeIfPresent(String.self, forKey: .narrativeStatus)
        narrative = try container.decodeIfPresent([BriefParagraph].self, forKey: .narrative) ?? []
        conversationKey = try? container.decodeIfPresent(String.self, forKey: .conversationKey)
        offers = (try? container.decodeIfPresent([MobileBriefOffer].self, forKey: .offers)) ?? []
        cards = (try? container.decodeIfPresent([MobileBriefCard].self, forKey: .cards)) ?? []
        needsYouCount = try container.decodeIfPresent(Int.self, forKey: .needsYouCount)
        // Read under its own key, so the existing keys stay as they were.
        if let generated = try? decoder.container(keyedBy: BriefGenerationKey.self) {
            generation = try? generated.decodeIfPresent(BriefGeneration.self, forKey: .generation)
        }
        sections = try container.decodeIfPresent(BriefSections.self, forKey: .sections) ?? BriefSections()
        dayShape = try container.decodeIfPresent(BriefDayShape.self, forKey: .dayShape)
        counts = try container.decodeIfPresent(BriefCounts.self, forKey: .counts)
        evidence = try container.decodeIfPresent([BriefEvidence].self, forKey: .evidence) ?? []
        limits = try container.decodeIfPresent(BriefLimits.self, forKey: .limits)
        audio = try? container.decodeIfPresent(BriefAudio.self, forKey: .audio)
        yourWeek = try? container.decodeIfPresent(MobileBriefWeek.self, forKey: .yourWeek)
        weekQuestion = try? container.decodeIfPresent(MorningBriefWeekQuestion.self, forKey: .weekQuestion)
        yourWeek?.sanitize()
        sanitizeBriefCopy()
    }

    var identifier: String {
        briefId ?? "\(localDate ?? "unknown")-\(slot ?? "brief")"
    }

    var asOfDate: Date? {
        asOf.flatMap(RelayTime.parse)
    }

    var dueDate: Date? {
        dueAt.flatMap(RelayTime.parse)
    }

    var localDay: Date? {
        guard let localDate else { return nil }
        return BriefDates.day.date(from: localDate)
    }

    var isColdStart: Bool {
        coldStart == true
    }

    // Evidence lookup by ref, so a chip can open the item's sources.
    func sources(for refs: [String]) -> [BriefEvidence] {
        refs.compactMap { ref in evidence.first { $0.ref == ref } }
    }

    // Purpose rows lead with their identity. That reference identifies the row; it is not
    // observed activity. Later purpose refs can identify concrete next steps.
    func primaryPurposeSource(for item: BriefItem) -> BriefEvidence? {
        guard let ref = item.refs.first,
              let source = evidence.first(where: { $0.ref == ref }),
              source.sourceKind == "purpose" else { return nil }
        return source
    }

    func purposeID(for item: BriefItem) -> String? {
        if let id = item.purposeId, !id.isEmpty { return id }
        return primaryPurposeSource(for: item)?.sourceId
    }

    // Purpose identities (including a next step) describe the goal graph, not where work was observed.
    func evidenceSources(for refs: [String]) -> [BriefEvidence] {
        sources(for: refs).filter { $0.sourceKind != "purpose" }
    }

    func purposeSources(for item: BriefItem) -> [BriefEvidence] {
        item.evidenceUnavailable == true ? [] : evidenceSources(for: item.refs)
    }

    var hasPurposeSections: Bool {
        !sections.workingToward.isEmpty || !sections.commitments.isEmpty || !sections.waitingOnYou.isEmpty
    }

    var showsOpenLoopsSection: Bool {
        !remainingOpenLoops.isEmpty || !hasPurposeSections
    }

    // The Mac keeps the legacy purpose rows for older phones. New phones show each purpose once,
    // using source identity rather than titles so unrelated work with the same title is preserved.
    var remainingOpenLoops: [BriefItem] {
        let purposeItems = Array(sections.workingToward.prefix(5)) + sections.commitments +
            Array(sections.waitingOnYou.prefix(3))
        let represented = Set(purposeItems.compactMap { item in
            purposeID(for: item)
        }.filter { !$0.isEmpty })
        guard !represented.isEmpty else { return sections.openLoops }
        return sections.openLoops.filter { item in
            item.kind == "one_time_work" || !represented.contains(purposeID(for: item) ?? "")
        }
    }

    var slotLabel: String {
        briefSlotLabel(slot: displaySlot ?? slot, asOf: asOfDate, timezone: timezone)
    }
}

/// One prepared click on a brief (section 8 `cards`). `kind` is `action` (a button), `item` (a row's question)
/// or `opener` (what the computer says first); `mode` is `send` or `speak_first`.
struct MobileBriefCard: Codable, Equatable, Sendable, Identifiable {
    var id: String
    var kind: String
    var mode: String
    var label: String? = nil
    var message: String
    var refs: [String] = []
    var itemKey: String? = nil
    /// A `question` card's paragraph: the zero-based index into the delivered narrative (RC 1, section 8).
    var paragraph: Int? = nil
}

struct MobileBriefWeek: Codable, Equatable, Sendable {
    var start: String
    var end: String
    var lead: String?
    var time: [MobileBriefWeekTime]
    var done: [MobileBriefWeekDone]
    var doneMore: Int?
    var open: [MobileBriefWeekOpen]
    var learned: [MobileBriefWeekLearned]
    var ask: MobileBriefWeekAsk?

    private enum CodingKeys: String, CodingKey {
        case start, end, lead, time, done, doneMore, open, learned, ask
    }

    init(start: String, end: String, lead: String? = nil,
         time: [MobileBriefWeekTime] = [], done: [MobileBriefWeekDone] = [],
         doneMore: Int? = nil, open: [MobileBriefWeekOpen] = [],
         learned: [MobileBriefWeekLearned] = [], ask: MobileBriefWeekAsk? = nil) {
        self.start = start
        self.end = end
        self.lead = lead
        self.time = time
        self.done = done
        self.doneMore = doneMore
        self.open = open
        self.learned = learned
        self.ask = ask
        sanitize()
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        start = (try? container.decodeIfPresent(String.self, forKey: .start)) ?? ""
        end = (try? container.decodeIfPresent(String.self, forKey: .end)) ?? ""
        lead = try? container.decodeIfPresent(String.self, forKey: .lead)
        time = (try? container.decodeIfPresent([MobileBriefWeekTime].self, forKey: .time)) ?? []
        done = (try? container.decodeIfPresent([MobileBriefWeekDone].self, forKey: .done)) ?? []
        doneMore = try? container.decodeIfPresent(Int.self, forKey: .doneMore)
        open = (try? container.decodeIfPresent([MobileBriefWeekOpen].self, forKey: .open)) ?? []
        learned = (try? container.decodeIfPresent([MobileBriefWeekLearned].self, forKey: .learned)) ?? []
        ask = try? container.decodeIfPresent(MobileBriefWeekAsk.self, forKey: .ask)
        sanitize()
    }

    mutating func sanitize() {
        lead = lead.flatMap { Self.hasText($0) ? $0 : nil }
        var timeTopics = 0
        var sawOther = false
        time = time.filter { row in
            guard row.hours > 0 else { return false }
            if row.other {
                guard !sawOther else { return false }
                sawOther = true
                return true
            }
            guard timeTopics < 3, let name = row.name, Self.hasText(name) else { return false }
            timeTopics += 1
            return true
        }
        done = Array(done.filter { row in
            guard Self.hasText(row.id) else { return false }
            if row.kind == "meetings" { return (row.count ?? 0) > 0 }
            return row.title.map(Self.hasText) == true
        }.prefix(3))
        doneMore = doneMore.flatMap { $0 > 0 ? $0 : nil }
        open = Array(open.filter { Self.hasText($0.itemKey) && Self.hasText($0.title) }.prefix(3))
        learned = Array(learned.filter { Self.hasText($0.id) && Self.hasText($0.text) }.prefix(2))
        if let ask, !Self.hasText(ask.text) || !Self.hasText(ask.message) {
            self.ask = nil
        }
    }

    private static func hasText(_ value: String) -> Bool {
        !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }
}

struct MobileBriefWeekTime: Codable, Equatable, Sendable, Identifiable {
    var topicId: String?
    var name: String?
    var other: Bool
    var hours: Int

    var id: String { other ? "other" : (topicId ?? name ?? "") }

    private enum CodingKeys: String, CodingKey { case topicId, name, other, hours }

    init(topicId: String? = nil, name: String? = nil, other: Bool = false, hours: Int) {
        self.topicId = topicId
        self.name = name
        self.other = other
        self.hours = hours
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        topicId = try? container.decodeIfPresent(String.self, forKey: .topicId)
        name = try? container.decodeIfPresent(String.self, forKey: .name)
        other = (try? container.decodeIfPresent(Bool.self, forKey: .other)) ?? false
        hours = (try? container.decodeIfPresent(Int.self, forKey: .hours)) ?? 0
    }
}

struct MobileBriefWeekDone: Codable, Equatable, Sendable, Identifiable {
    var id: String
    var kind: String
    var title: String?
    var at: String?
    var count: Int?
    var source: String?
    var sourceId: String?
    var path: String?

    private enum CodingKeys: String, CodingKey { case id, kind, title, at, count, source, sourceId, path }

    init(id: String, kind: String, title: String? = nil, at: String? = nil, count: Int? = nil,
         source: String? = nil, sourceId: String? = nil, path: String? = nil) {
        self.id = id
        self.kind = kind
        self.title = title
        self.at = at
        self.count = count
        self.source = source
        self.sourceId = sourceId
        self.path = path
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        kind = try container.decode(String.self, forKey: .kind)
        title = try? container.decodeIfPresent(String.self, forKey: .title)
        at = try? container.decodeIfPresent(String.self, forKey: .at)
        count = try? container.decodeIfPresent(Int.self, forKey: .count)
        source = try? container.decodeIfPresent(String.self, forKey: .source)
        sourceId = try? container.decodeIfPresent(String.self, forKey: .sourceId)
        path = try? container.decodeIfPresent(String.self, forKey: .path)
    }
}

struct MobileBriefWeekOpen: Codable, Equatable, Sendable, Identifiable {
    var itemKey: String
    var cardId: String?
    var title: String
    var since: String?
    var label: String?

    var id: String { itemKey }

    private enum CodingKeys: String, CodingKey { case itemKey, cardId, title, since, label }

    init(itemKey: String, cardId: String? = nil, title: String, since: String? = nil, label: String? = nil) {
        self.itemKey = itemKey
        self.cardId = cardId
        self.title = title
        self.since = since
        self.label = label
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        itemKey = try container.decode(String.self, forKey: .itemKey)
        cardId = try? container.decodeIfPresent(String.self, forKey: .cardId)
        title = try container.decode(String.self, forKey: .title)
        since = try? container.decodeIfPresent(String.self, forKey: .since)
        label = try? container.decodeIfPresent(String.self, forKey: .label)
    }
}

struct MobileBriefWeekLearned: Codable, Equatable, Sendable, Identifiable {
    var id: String
    var kind: String
    var text: String
    var memoryId: String?
    var engagementKind: String?

    private enum CodingKeys: String, CodingKey { case id, kind, text, memoryId, engagementKind }

    init(id: String, kind: String, text: String, memoryId: String? = nil, engagementKind: String? = nil) {
        self.id = id
        self.kind = kind
        self.text = text
        self.memoryId = memoryId
        self.engagementKind = engagementKind
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        kind = try container.decode(String.self, forKey: .kind)
        text = try container.decode(String.self, forKey: .text)
        memoryId = try? container.decodeIfPresent(String.self, forKey: .memoryId)
        engagementKind = try? container.decodeIfPresent(String.self, forKey: .engagementKind)
    }
}

struct MobileBriefWeekHold: Codable, Equatable, Sendable {
    var start: String
    var end: String
    var label: String?

    private enum CodingKeys: String, CodingKey { case start, end, label }

    init(start: String, end: String, label: String? = nil) {
        self.start = start
        self.end = end
        self.label = label
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        start = try container.decode(String.self, forKey: .start)
        end = try container.decode(String.self, forKey: .end)
        label = try? container.decodeIfPresent(String.self, forKey: .label)
    }
}

struct MobileBriefWeekAsk: Codable, Equatable, Sendable {
    var text: String
    var message: String
    var day: String?
    var cardId: String?
    var topicId: String?
    var hold: MobileBriefWeekHold?

    private enum CodingKeys: String, CodingKey { case text, message, day, cardId, topicId, hold }

    init(text: String, message: String, day: String? = nil, cardId: String? = nil,
         topicId: String? = nil, hold: MobileBriefWeekHold? = nil) {
        self.text = text
        self.message = message
        self.day = day
        self.cardId = cardId
        self.topicId = topicId
        self.hold = hold
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        text = try container.decode(String.self, forKey: .text)
        message = try container.decode(String.self, forKey: .message)
        day = try? container.decodeIfPresent(String.self, forKey: .day)
        cardId = try? container.decodeIfPresent(String.self, forKey: .cardId)
        topicId = try? container.decodeIfPresent(String.self, forKey: .topicId)
        hold = try? container.decodeIfPresent(MobileBriefWeekHold.self, forKey: .hold)
    }
}

enum MobileBriefWeekAskTarget: Equatable {
    case card(MobileBriefCard)
    case topic(id: String, message: String)
}

struct YourWeekAskDismissals {
    private static let storageKey = "brief.yourWeek.dismissedAskIDs"
    private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    func contains(briefID: String) -> Bool {
        (defaults.stringArray(forKey: Self.storageKey) ?? []).contains(briefID)
    }

    func hide(briefID: String) {
        var identifiers = Set(defaults.stringArray(forKey: Self.storageKey) ?? [])
        identifiers.insert(briefID)
        defaults.set(identifiers.sorted(), forKey: Self.storageKey)
    }
}

extension MobileBriefCard {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        kind = try container.decode(String.self, forKey: .kind)
        mode = try container.decode(String.self, forKey: .mode)
        label = try? container.decodeIfPresent(String.self, forKey: .label)
        message = try container.decode(String.self, forKey: .message)
        refs = (try? container.decodeIfPresent([String].self, forKey: .refs)) ?? []
        itemKey = try? container.decodeIfPresent(String.self, forKey: .itemKey)
        paragraph = try? container.decodeIfPresent(Int.self, forKey: .paragraph)
    }
}

extension MobileBriefV1 {
    /// Cards a tap may put in the chat box: the brief must be one the computer can discuss.
    var sendCards: [MobileBriefCard] {
        guard chatReference != nil else { return [] }
        return cards.filter { $0.mode == "send" && !$0.id.isEmpty && !$0.message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }

    /// At most two buttons, each a verb and the thing.
    var actionCards: [MobileBriefCard] {
        Array(sendCards.filter { $0.kind == "action" && !($0.label ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }.prefix(2))
    }

    /// One prepared question per paragraph, in the order the computer supplied the cards.
    var paragraphChips: [Int: MobileBriefCard] {
        var chips: [Int: MobileBriefCard] = [:]
        var labels = Set<String>()
        for card in sendCards {
            guard card.kind == "question" || card.kind == "item" && card.paragraph != nil,
                  let paragraph = card.paragraph,
                  narrative.indices.contains(paragraph),
                  paragraph != closingParagraphIndex,
                  chips[paragraph] == nil,
                  let label = card.label?.trimmingCharacters(in: .whitespacesAndNewlines),
                  !label.isEmpty,
                  !card.message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { continue }
            let normalizedLabel = label.split(whereSeparator: { $0.isWhitespace })
                .joined(separator: " ").lowercased()
            guard labels.insert(normalizedLabel).inserted else { continue }
            chips[paragraph] = card
            if chips.count == 3 { break }
        }
        return chips
    }

    func paragraphChip(forParagraph paragraph: Int) -> MobileBriefCard? {
        paragraphChips[paragraph]
    }

    func yourWeekOpenCard(for row: MobileBriefWeekOpen) -> MobileBriefCard? {
        guard let label = row.label,
              !label.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              let cardID = row.cardId else { return nil }
        return sendCards.first { $0.id == cardID }
    }

    var yourWeekAskTarget: MobileBriefWeekAskTarget? {
        guard let ask = yourWeek?.ask else { return nil }
        if let cardID = ask.cardId, let card = sendCards.first(where: { $0.id == cardID }) {
            return .card(card)
        }
        guard let topicID = ask.topicId,
              !topicID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !ask.message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return .topic(id: topicID, message: ask.message)
    }

    /// The brief ends on one question, as Today shows it on the computer: its last paragraph, when that asks
    /// something. Its answers are the action cards.
    var closingParagraphIndex: Int? {
        guard let last = narrative.indices.last,
              let text = narrative[last].text?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty,
              text.contains("?") || text.contains("\u{FF1F}") else { return nil }
        return last
    }

    /// The question a row's chat action puts in the box, when the computer wrote one for it.
    func itemCard(for item: BriefItem) -> MobileBriefCard? {
        guard let key = item.key ?? item.id, !key.isEmpty else { return nil }
        return sendCards.first { $0.kind == "item" && $0.itemKey == key }
    }

    /// "What needs your attention", as Today lists it: items the computer gave a reason, one per goal, the most
    /// pressing first, then the longest open, at most three.
    var attentionItems: [BriefItem] {
        let candidates = sections.commitments + sections.workingToward + sections.waitingOnYou + sections.moved +
            sections.deliverables + sections.people + sections.openLoops
        var seenPurposes = Set<String>()
        var seenKeys = Set<String>()
        var out: [BriefItem] = []
        for item in candidates {
            guard item.evidenceUnavailable != true, item.kind != "find",
                  !(item.attentionReason ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { continue }
            if let purpose = purposeID(for: item), !purpose.isEmpty {
                guard seenPurposes.insert(purpose).inserted else { continue }
            }
            if let key = item.key ?? item.id, !key.isEmpty {
                guard seenKeys.insert(key).inserted else { continue }
            }
            out.append(item)
        }
        let rank = { (item: BriefItem) in (item.attentionRank ?? 0) > 0 ? item.attentionRank! : Int.max }
        let since = { (item: BriefItem) in item.since.flatMap(RelayTime.parse) ?? .distantFuture }
        return Array(out.enumerated().sorted { lhs, rhs in
            if rank(lhs.element) != rank(rhs.element) { return rank(lhs.element) < rank(rhs.element) }
            if since(lhs.element) != since(rhs.element) { return since(lhs.element) < since(rhs.element) }
            return lhs.offset < rhs.offset
        }.map(\.element).prefix(3))
    }
}

// Calendar day formatter for the protocol's localDate (YYYY-MM-DD in the Mac's timezone).
enum BriefDates {
    static let day: DateFormatter = {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
    }()
}

struct BriefParagraph: Codable, Equatable, Sendable {
    var text: String? = nil
    var refs: [String] = []
    var hold: MobileBriefWeekHold? = nil
    /// The topic the paragraph is about (RC 1, section 8): its registry id and readable name, both optional. Older
    /// briefs and paragraphs with no readable topic have neither.
    var topicId: String? = nil
    var topicName: String? = nil
}

extension BriefParagraph {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        text = try container.decodeIfPresent(String.self, forKey: .text)
        refs = try container.decodeIfPresent([String].self, forKey: .refs) ?? []
        hold = try? container.decodeIfPresent(MobileBriefWeekHold.self, forKey: .hold)
        topicId = try? container.decodeIfPresent(String.self, forKey: .topicId)
        topicName = try? container.decodeIfPresent(String.self, forKey: .topicName)
    }

    /// The topic button's target: an id the phone may put in a route path, with a name to show.
    var topic: (id: String, name: String)? {
        guard let id = topicId, Identifiers.isSafePathID(id),
              let name = topicName?.trimmingCharacters(in: .whitespacesAndNewlines), !name.isEmpty else { return nil }
        return (id, name)
    }
}

struct BriefSections: Codable, Equatable, Sendable {
    var workingToward: [BriefItem] = []
    var commitments: [BriefItem] = []
    var waitingOnYou: [BriefItem] = []
    var moved: [BriefItem] = []
    var doing: [BriefItem] = []
    var watching: [BriefItem] = []
    var meetings: [BriefItem] = []
    var people: [BriefItem] = []
    var openLoops: [BriefItem] = []
    var learned: [BriefItem] = []
    var deliverables: [BriefItem] = []
    var schedules: [BriefItem] = []
}

extension BriefSections {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        workingToward = try container.decodeIfPresent([BriefItem].self, forKey: .workingToward) ?? []
        commitments = try container.decodeIfPresent([BriefItem].self, forKey: .commitments) ?? []
        waitingOnYou = try container.decodeIfPresent([BriefItem].self, forKey: .waitingOnYou) ?? []
        moved = try container.decodeIfPresent([BriefItem].self, forKey: .moved) ?? []
        doing = try container.decodeIfPresent([BriefItem].self, forKey: .doing) ?? []
        watching = try container.decodeIfPresent([BriefItem].self, forKey: .watching) ?? []
        meetings = try container.decodeIfPresent([BriefItem].self, forKey: .meetings) ?? []
        people = try container.decodeIfPresent([BriefItem].self, forKey: .people) ?? []
        openLoops = try container.decodeIfPresent([BriefItem].self, forKey: .openLoops) ?? []
        learned = try container.decodeIfPresent([BriefItem].self, forKey: .learned) ?? []
        deliverables = try container.decodeIfPresent([BriefItem].self, forKey: .deliverables) ?? []
        schedules = try container.decodeIfPresent([BriefItem].self, forKey: .schedules) ?? []
    }

    var isEmpty: Bool {
        workingToward.isEmpty && commitments.isEmpty && waitingOnYou.isEmpty &&
            moved.isEmpty && doing.isEmpty && watching.isEmpty && meetings.isEmpty && people.isEmpty &&
            openLoops.isEmpty && learned.isEmpty && deliverables.isEmpty && schedules.isEmpty
    }
}

struct BriefItem: Codable, Equatable, Sendable {
    var id: String? = nil
    /// The item's section and source record, the same in every brief that shows it (PROTOCOL.md section 20.2).
    /// Briefs stored before keys have none; FeedAssembly computes the same key from the evidence.
    var key: String? = nil
    var title: String? = nil
    var detail: String? = nil
    var status: String? = nil
    var at: String? = nil
    var kind: String? = nil
    var refs: [String] = []
    var url: String? = nil
    var lastTouch: String? = nil
    var nextStep: String? = nil
    var dueAt: String? = nil
    var momentId: String? = nil
    var chatSessionId: String? = nil
    var chatMessageId: String? = nil
    /// Unavailable evidence permits only the title, state, and fallback detail to be displayed.
    var evidenceUnavailable: Bool? = nil
    var purposeId: String? = nil
    /// Why the item needs the user now, one short line the computer wrote (section 8); empty when it does not.
    var attentionReason: String? = nil
    var attentionRank: Int? = nil
    var since: String? = nil
}

extension BriefItem {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id)
        key = try container.decodeIfPresent(String.self, forKey: .key)
        title = try container.decodeIfPresent(String.self, forKey: .title)
        detail = try container.decodeIfPresent(String.self, forKey: .detail)
        status = try container.decodeIfPresent(String.self, forKey: .status)
        at = try container.decodeIfPresent(String.self, forKey: .at)
        kind = try container.decodeIfPresent(String.self, forKey: .kind)
        refs = try container.decodeIfPresent([String].self, forKey: .refs) ?? []
        url = try container.decodeIfPresent(String.self, forKey: .url)
        lastTouch = try container.decodeIfPresent(String.self, forKey: .lastTouch)
        nextStep = try container.decodeIfPresent(String.self, forKey: .nextStep)
        dueAt = try container.decodeIfPresent(String.self, forKey: .dueAt)
        momentId = try container.decodeIfPresent(String.self, forKey: .momentId)
        chatSessionId = try container.decodeIfPresent(String.self, forKey: .chatSessionId)
        chatMessageId = try container.decodeIfPresent(String.self, forKey: .chatMessageId)
        evidenceUnavailable = try container.decodeIfPresent(Bool.self, forKey: .evidenceUnavailable)
        purposeId = try container.decodeIfPresent(String.self, forKey: .purposeId)
        attentionReason = try? container.decodeIfPresent(String.self, forKey: .attentionReason)
        attentionRank = try? container.decodeIfPresent(Int.self, forKey: .attentionRank)
        since = try? container.decodeIfPresent(String.self, forKey: .since)
    }

    var atDate: Date? {
        at.flatMap(RelayTime.parse)
    }

    var lastTouchDate: Date? { lastTouch.flatMap(RelayTime.parse) }
    var dueDate: Date? { dueAt.flatMap(RelayTime.parse) }
    var hasMainChatReference: Bool { !(chatMessageId ?? "").isEmpty }

    // Compare calendar days in the Mac's timezone, including when the phone reads an older brief.
    // A deadline earlier today stays due today; only earlier days are overdue.
    func dueLabel(now: Date = Date(), timezone: String? = nil) -> String? {
        guard evidenceUnavailable != true, let dueDate else { return nil }
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timezone.flatMap(TimeZone.init(identifier:)) ?? .current
        let today = calendar.startOfDay(for: now)
        let dueDay = calendar.startOfDay(for: dueDate)
        if dueDay < today { return "Overdue" }
        if dueDay == today { return "Due today" }
        return nil
    }

    // Only secure web links can open from a brief, including briefs read from the cache.
    var webURL: URL? {
        guard evidenceUnavailable != true, let url, let destination = URL(string: url),
              destination.scheme?.lowercased() == "https",
              let host = destination.host, !host.isEmpty else { return nil }
        return destination
    }

    // Open loops are grouped by kind. Known kinds get product labels; any other kind is
    // shown as plain words so a new kind never appears as a raw enum.
    static func kindLabel(_ kind: String?) -> String {
        switch kind {
        case "commitment": return "Commitments"
        case "question": return "Open questions"
        case "follow_up", "followup": return "Follow-ups"
        case "waiting", "waiting_on": return "Waiting on others"
        case "blocker": return "Blockers"
        case "one_time_work": return "One-time work"
        case "find": return "Finds"
        case "calendar": return "Calendar"
        case "calendar_day": return "Day overview"
        case let other?:
            let words = other.replacingOccurrences(of: "_", with: " ")
            return words.prefix(1).uppercased() + words.dropFirst()
        case nil:
            return "Other"
        }
    }
}

struct BriefDayShape: Codable, Equatable, Sendable {
    var activeMinutes: Int? = nil
    var hours: [BriefHour] = []
    var topApps: [BriefApp] = []
    var firstActive: String? = nil
    var lastActive: String? = nil
    var breakMinutes: Int? = nil
}

extension BriefDayShape {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        activeMinutes = try container.decodeIfPresent(Int.self, forKey: .activeMinutes)
        hours = try container.decodeIfPresent([BriefHour].self, forKey: .hours) ?? []
        topApps = try container.decodeIfPresent([BriefApp].self, forKey: .topApps) ?? []
        firstActive = try container.decodeIfPresent(String.self, forKey: .firstActive)
        lastActive = try container.decodeIfPresent(String.self, forKey: .lastActive)
        breakMinutes = try container.decodeIfPresent(Int.self, forKey: .breakMinutes)
    }

    var firstActiveDate: Date? { firstActive.flatMap(RelayTime.parse) }
    var lastActiveDate: Date? { lastActive.flatMap(RelayTime.parse) }
}

struct BriefHour: Codable, Equatable, Sendable {
    var hour: Int? = nil
    var activeMinutes: Int? = nil
}

struct BriefApp: Codable, Equatable, Sendable {
    var name: String? = nil
    var activeMinutes: Int? = nil
}

struct BriefCounts: Codable, Equatable, Sendable {
    var openLoopsByKind: [String: Int] = [:]
    var selfUpdatesPending: Int? = nil
    var learningItems: Int? = nil
    var needsYou: Int? = nil
}

extension BriefCounts {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        openLoopsByKind = try container.decodeIfPresent([String: Int].self, forKey: .openLoopsByKind) ?? [:]
        selfUpdatesPending = try container.decodeIfPresent(Int.self, forKey: .selfUpdatesPending)
        learningItems = try container.decodeIfPresent(Int.self, forKey: .learningItems)
        needsYou = try container.decodeIfPresent(Int.self, forKey: .needsYou)
    }
}

struct BriefEvidence: Codable, Equatable, Sendable {
    var ref: String? = nil
    var sourceKind: String? = nil
    var sourceId: String? = nil
    var label: String? = nil
    var observedAt: String? = nil
}

extension BriefEvidence {
    var observedDate: Date? {
        observedAt.flatMap(RelayTime.parse)
    }

    // Product labels for the sources an item was built from.
    var sourceLabel: String {
        switch sourceKind {
        case "history", "activity": return "Activity"
        case "activity_run": return "Activity summary"
        case "activity_session": return "Work session"
        case "engagement_moment": return "Reminder"
        case "chat_message": return "Chat"
        case "watching": return "Watching"
        case "web": return "Browsing"
        case "file": return "Document"
        case "purpose": return "Goal"
        case "meeting": return "Meeting"
        case "calendar": return "Calendar"
        case "calendar_day": return "Day overview"
        case "find": return "Find"
        case "memory": return "Memory"
        case "knowledge": return "How you do things"
        case "deliverable": return "Deliverable"
        case "schedule_run": return "Scheduled run"
        case "principal": return "Your input"
        case "learning": return "Learning"
        case "summary": return "Summary"
        case "news": return "News"
        default: return "Source"
        }
    }
}

struct BriefLimits: Codable, Equatable, Sendable {
    var itemsPerSection: Int? = nil
    var evidenceRefs: Int? = nil
    var truncatedSections: [String] = []
    var excludedSensitiveItems: Int? = nil
}

extension BriefLimits {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        itemsPerSection = try container.decodeIfPresent(Int.self, forKey: .itemsPerSection)
        evidenceRefs = try container.decodeIfPresent(Int.self, forKey: .evidenceRefs)
        truncatedSections = try container.decodeIfPresent([String].self, forKey: .truncatedSections) ?? []
        excludedSensitiveItems = try container.decodeIfPresent(Int.self, forKey: .excludedSensitiveItems)
    }
}

// One row of GET /briefs on the hub (section 7): metadata only, no content.
struct BriefSummary: Codable, Equatable, Sendable, Identifiable {
    var briefId: String? = nil
    var slot: String? = nil
    var localDate: String? = nil
    var asOf: String? = nil
    var narrativeStatus: String? = nil

    var id: String { briefId ?? "\(localDate ?? "unknown")-\(slot ?? "brief")" }

    var asOfDate: Date? {
        asOf.flatMap(RelayTime.parse)
    }
}

// Cached on-request briefs predate displaySlot. Use the same default slot
// clock as the computer until a newer document supplies its configured slot.
func briefSlotLabel(slot: String?, asOf: Date?, timezone: String? = nil) -> String {
    if slot == "morning" { return String(localized: "Morning brief") }
    if slot == "evening" { return String(localized: "Evening brief") }
    var calendar = Calendar.current
    if let timezone, let zone = TimeZone(identifier: timezone) { calendar.timeZone = zone }
    let hour = asOf.map { calendar.component(.hour, from: $0) } ?? 9
    return hour < 7 || hour >= 18 ? String(localized: "Evening brief") : String(localized: "Morning brief")
}

// Final guard for older cached briefs as well as newly received documents.
// The computer applies the same vocabulary before assembly and delivery.
private enum BriefCopyHygiene {
    static let unfinished = try! NSRegularExpression(pattern: #"(?<![A-Za-z0-9_])(?:Name|NAME|FIRST_NAME|LAST_NAME|CONTACT_NAME|TODO|TBD|INSERT_[A-Z_]+)(?![A-Za-z0-9_])|\{\{?\s*[A-Za-z_][A-Za-z_0-9.]*\s*\}\}?|%[sdv]|__[A-Za-z_]+__|(?i:\[(?:name|first_name|last_name|contact_name|contact|person|subject|project|topic)\]|<(?:name|first_name|last_name|contact_name|contact|person|subject|project|topic)>|\$(?:name|first_name|last_name|contact_name|contact|person|subject|project|topic)\b|\b(?:unidentified|unnamed|unknown) contacts?\b|(?:\b(?:for|to|about)|给|关于|为)\s*[?？])"#)

    static let privacyMask = try! NSRegularExpression(pattern: #"\[(?:REDACTED:[a-z_]+|masked|\.\.\.)\]"#)

    static func containsRedaction(_ text: String) -> Bool {
        text.range(of: "[REDACTED", options: .caseInsensitive) != nil
    }

    static func safe(_ text: String) -> Bool {
        // New deliveries use the computer's localized display sanitizer.
        // Withhold legacy cached masks, including their damaged word fragments.
        guard !containsRedaction(text) else { return false }
        let text = privacyMask.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text), withTemplate: "")
        return unfinished.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)) == nil
    }

    static func safeLines(_ text: String?) -> String? {
        text?.components(separatedBy: "\n").filter(safe).joined(separator: "\n")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    static func items(_ items: [BriefItem]) -> [BriefItem] {
        items.compactMap { original in
            guard safe(original.title ?? "") else { return nil }
            var item = original
            item.detail = safeLines(item.detail)
            item.nextStep = safeLines(item.nextStep)
            item.attentionReason = safeLines(item.attentionReason)
            return item
        }
    }
}

extension MobileBriefV1 {
    private mutating func sanitizeBriefCopy() {
        if let recommendation = weekQuestion?.recommendation,
           !BriefCopyHygiene.safe(recommendation) || BriefCopyHygiene.containsRedaction(recommendation) {
            weekQuestion = nil
        }
        let sectionPaths: [WritableKeyPath<BriefSections, [BriefItem]>] = [
            \.workingToward, \.commitments, \.waitingOnYou, \.moved, \.doing, \.watching,
            \.meetings, \.people, \.openLoops, \.learned, \.deliverables, \.schedules,
        ]
        let last = narrative.last
        let closingText = last?.text?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let holdQuestion = (closingText.hasSuffix("?") || closingText.hasSuffix("？")) &&
            closingText.range(of: #"\b(hold|reserve|block|set aside|make time)\b|预留|留出|留给|安排.*时间"#,
                              options: [.regularExpression, .caseInsensitive]) != nil
        let hideHold = last?.hold != nil || holdQuestion
        let redactedCopy = hideHold || narrative.contains { BriefCopyHygiene.containsRedaction($0.text ?? "") }
            || cards.contains { BriefCopyHygiene.containsRedaction(($0.label ?? "") + $0.message) }
            || offers.contains { BriefCopyHygiene.containsRedaction($0.label + $0.prompt) }
            || sectionPaths.contains { path in
                sections[keyPath: path].contains { item in
                    BriefCopyHygiene.containsRedaction([item.title, item.detail, item.nextStep, item.attentionReason].compactMap { $0 }.joined())
                }
            }
        offers = offers.filter { BriefCopyHygiene.safe($0.label) && BriefCopyHygiene.safe($0.prompt) }
        for path in sectionPaths {
            sections[keyPath: path] = BriefCopyHygiene.items(sections[keyPath: path])
        }
        evidence = evidence.map { original in
            var ref = original
            ref.label = BriefCopyHygiene.safeLines(ref.label)
            return ref
        }
        var positions: [Int: Int] = [:]
        var shown: [BriefParagraph] = []
        var changed = redactedCopy
        for (index, var paragraph) in narrative.enumerated() {
            if hideHold && index == narrative.count - 1 { continue }
            let original = paragraph.text ?? ""
            let text = BriefCopyHygiene.safeLines(original) ?? ""
            changed = changed || text != original
            guard !text.isEmpty else { continue }
            paragraph.text = text
            if !BriefCopyHygiene.safe(paragraph.topicName ?? "") { paragraph.topicName = nil; paragraph.topicId = nil }
            positions[index] = shown.count
            shown.append(paragraph)
        }
        narrative = shown
        cards = cards.compactMap { original in
            var card = original
            guard BriefCopyHygiene.safe(card.label ?? ""), BriefCopyHygiene.safe(card.message) else { return nil }
            if let index = card.paragraph {
                guard let position = positions[index] else { return nil }
                card.paragraph = position
            }
            if changed && card.kind == "action" { return nil }
            return card
        }
        // A paragraph dropped above moves the paragraphs after it, so the spoken timings follow the new positions.
        if var spoken = audio {
            spoken.paragraphs = spoken.paragraphs?.compactMap { timing in
                guard timing.source == "narrative" else { return timing }
                guard let position = positions[timing.index] else { return nil }
                var moved = timing
                moved.index = position
                return moved
            }
            audio = spoken
        }
        if changed { audio = nil; conversationKey = nil }
    }
}

// Brief keys use section.hash; older records can use section:id or section-id.
func isCommitmentBriefItemKey(_ key: String) -> Bool {
    ["commitments.", "commitments:", "commitments-"].contains { key.hasPrefix($0) }
}

/// The brief's generation record (PROTOCOL.md 8.1). Only the reason and the times are read here.
struct BriefGeneration: Codable, Equatable, Sendable {
    var reason: String? = nil
    var scheduledAt: String? = nil
    var generatedAt: String? = nil
    var message: String? = nil
}

private enum BriefGenerationKey: String, CodingKey {
    case generation
}
