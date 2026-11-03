import Foundation

// The one title for a brief (handoff 2026-10-28, section 1). The Today card, the brief view, the player, the
// lock screen, Past briefs and Chat all title a brief through this helper, so the same brief reads the same.
struct BriefTitleInput: Equatable, Sendable {
    var kind: String?
    var slot: String?
    var displaySlot: String?
    /// When the brief was made (`asOf`).
    var madeAt: Date?
    /// When a scheduled brief was due. Nil for a brief asked for by the person.
    var dueAt: Date?
    var timezone: String?
}

extension BriefTitleInput {
    init(brief: MobileBriefV1) {
        self.init(kind: brief.kind, slot: brief.slot, displaySlot: brief.displaySlot, madeAt: brief.asOfDate,
                  dueAt: brief.dueDate, timezone: brief.timezone)
    }

    /// A Past briefs row, from the Mac's Today index. A catch-up brief's scheduled time is its due time.
    init(index: TodayBriefIndex) {
        self.init(kind: index.kind, slot: index.slot, displaySlot: nil, madeAt: index.madeDate,
                  dueAt: index.scheduledAt.flatMap(RelayTime.parse), timezone: nil)
    }
}

enum BriefTitle {
    /// A brief made this long after it was due is late. The slack keeps an on-time brief from being called late.
    static let lateTolerance: TimeInterval = 5 * 60

    /// "Morning brief · 6:50 AM", "Evening brief · 6:00 PM", "Your week · 6:00 PM", "Brief · 8:41 AM". A brief made
    /// late names the time it was made; the "Late" line says when it was due. A brief from a Mac before 1.0 has no
    /// kind, so its name comes from the slot as it always did.
    static func title(_ input: BriefTitleInput) -> String {
        let name = kindName(input)
        guard let time = timeText(input) else { return name }
        return String(format: String(localized: "%@ · %@"), name, time)
    }

    /// "Late: your computer was asleep at 6:50 AM.", for a brief made after its due time; nil otherwise.
    static func lateLine(_ input: BriefTitleInput) -> String? {
        guard isLate(input), let dueAt = input.dueAt else { return nil }
        return String(format: String(localized: "Late: your computer was asleep at %@."), BriefView.timeText(dueAt))
    }

    static func isLate(_ input: BriefTitleInput) -> Bool {
        guard let madeAt = input.madeAt, let dueAt = input.dueAt else { return false }
        return madeAt.timeIntervalSince(dueAt) > lateTolerance
    }

    static func kindName(_ input: BriefTitleInput) -> String {
        switch input.kind {
        case "morning": return String(localized: "Morning brief")
        case "evening": return String(localized: "Evening brief")
        case "week": return String(localized: "Your week")
        case "on_request": return String(localized: "Brief")
        default: return briefSlotLabel(slot: input.displaySlot ?? input.slot, asOf: input.madeAt, timezone: input.timezone)
        }
    }

    /// The time the title shows: when a brief was asked for or made late, the time it was made; otherwise the
    /// scheduled time, falling back to the time it was made.
    static func timeText(_ input: BriefTitleInput) -> String? {
        let shown: Date?
        if input.kind == "on_request" || isLate(input) {
            shown = input.madeAt
        } else {
            shown = input.dueAt ?? input.madeAt
        }
        return shown.map(BriefView.timeText)
    }
}

extension MobileBriefV1 {
    /// The brief's title with its time (see `BriefTitle`).
    var titleText: String { BriefTitle.title(BriefTitleInput(brief: self)) }

    /// The "Late" line for this brief, when it was made after it was due.
    var lateLineText: String? { BriefTitle.lateLine(BriefTitleInput(brief: self)) }

    /// Whether the brief has words to read and to speak. Chat and the player never offer a brief without them.
    var hasWords: Bool {
        yourWeek != nil || narrative.contains { !($0.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }
}

extension BriefTitleInput {
    /// A library row, from the phone's copy or the Mac's list of briefs.
    init(entry: HistoryEntry) {
        self.init(kind: entry.kind, slot: entry.slot, displaySlot: entry.displaySlot, madeAt: entry.asOf,
                  dueAt: entry.dueAt.flatMap(RelayTime.parse), timezone: entry.timezone)
    }
}
