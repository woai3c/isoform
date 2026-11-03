import SwiftUI

/// The phone's 14-day brief index, including relay briefs newer than the last hub snapshot. Grouped by day, and each
/// row opens the brief view. Looking at or playing a past brief never changes the current brief.
struct PastBriefsView: View {
    @EnvironmentObject private var today: TodayStore
    @EnvironmentObject private var brief: BriefStore
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var environment: AppEnvironment
    @Environment(\.locale) private var locale
    @Environment(\.tabChromeHeight) private var tabChromeHeight

    var body: some View {
        let now = Date()
        let entries = PastBriefIndex.entries(snapshot: today.snapshot, cached: brief.cachedBriefs,
                                             latestID: today.newestBrief()?.identifier, now: now)
        let groups = PastBriefGroups.groups(entries, today: FeedAssembly.localDate(for: now),
                                            yesterday: FeedAssembly.localDate(for: now.addingTimeInterval(-24 * 60 * 60)))
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text("Kept on this phone for 30 days")
                    .font(.subheadline)
                    .foregroundStyle(YansuTheme.secondary)
                ForEach(groups) { group in
                    VStack(alignment: .leading, spacing: 0) {
                        Text(verbatim: title(for: group.day))
                            .font(.footnote.weight(.semibold))
                            .foregroundStyle(YansuTheme.secondary)
                            .padding(.bottom, 6)
                            .accessibilityAddTraits(.isHeader)
                        ForEach(group.rows) { entry in
                            NavigationLink {
                                BriefDetailView(entry: HistoryEntry(id: entry.id, slot: entry.slot, localDate: entry.localDate,
                                                                    asOf: entry.madeDate, narrativeStatus: "ok", isCached: true,
                                                                    kind: entry.kind, dueAt: entry.scheduledAt),
                                                player: environment.audioPlayer, fallbackLead: entry.lead)
                            } label: {
                                row(entry)
                            }
                            .buttonStyle(.plain)
                            .accessibilityIdentifier("pastBriefs.row")
                            Divider().overlay(YansuTheme.borderSubtle)
                        }
                    }
                }
            }
            .padding(.horizontal, YansuTheme.gutter)
            .padding(.top, 8)
            .padding(.bottom, 32 + tabChromeHeight)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .yansuScreen()
        .navigationTitle(String(localized: "Past briefs"))
        .navigationBarTitleDisplayMode(.large)
        .toolbar(.hidden, for: .tabBar)
        .accessibilityIdentifier("pastBriefs.screen")
    }

    private func title(for day: PastBriefDay) -> String {
        switch day {
        case .today: return String(localized: "Today")
        case .yesterday: return String(localized: "Yesterday")
        case .date(let localDate):
            guard let date = BriefDates.day.date(from: localDate) else { return localDate }
            let formatter = DateFormatter()
            formatter.locale = locale
            formatter.timeZone = TimeZone(secondsFromGMT: 0)
            formatter.setLocalizedDateFormatFromTemplate("EEEMMMd")
            return formatter.string(from: date)
        }
    }

    private func row(_ entry: TodayBriefIndex) -> some View {
        // A row's place and whether it was heard come from this phone's copy of the brief, when it has one.
        let copy = brief.cachedBrief(withID: entry.id)
        let memory = copy.flatMap { environment.audioPlayer.playbackMemory(brief: $0, hubID: pairing.paired?.hubID ?? "") }
        return HStack(alignment: .center, spacing: 12) {
            VStack(alignment: .leading, spacing: 6) {
                Text(verbatim: BriefTitle.title(BriefTitleInput(index: entry)))
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(YansuTheme.ink)
                    .fixedSize(horizontal: false, vertical: true)
                Text(verbatim: entry.lead)
                    .font(.subheadline)
                    .foregroundStyle(YansuTheme.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("pastBriefs.lead")
                if let length = entry.durationSeconds.flatMap(BriefAudio.label) {
                    Text(verbatim: length)
                        .font(.footnote.monospacedDigit())
                        .foregroundStyle(YansuTheme.tertiary)
                        .accessibilityIdentifier("pastBriefs.length")
                }
            }
            Spacer(minLength: 8)
            PastBriefState(memory: memory)
        }
        .padding(.vertical, 14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
    }
}

/// The row's state: a ring for how much was heard, "Played" once it was heard to the end, or nothing.
private struct PastBriefState: View {
    let memory: BriefPlaybackMemory?

    var body: some View {
        if let memory, memory.played {
            Text("Played")
                .font(.footnote.weight(.medium))
                .foregroundStyle(YansuTheme.secondary)
                .accessibilityIdentifier("pastBriefs.played")
        } else if let memory, memory.progress > 0 {
            ZStack {
                Circle().stroke(YansuTheme.border, lineWidth: 2.5)
                Circle()
                    .trim(from: 0, to: memory.progress)
                    .stroke(YansuTheme.accent, style: StrokeStyle(lineWidth: 2.5, lineCap: .round))
                    .rotationEffect(.degrees(-90))
            }
            .frame(width: 22, height: 22)
            .accessibilityLabel(String(localized: "Part heard"))
            .accessibilityIdentifier("pastBriefs.ring")
        }
    }
}

/// The day a past brief was written, for its group heading.
enum PastBriefDay: Hashable {
    case today
    case yesterday
    case date(String)
}

struct PastBriefGroup: Identifiable, Equatable {
    let day: PastBriefDay
    let rows: [TodayBriefIndex]
    var id: PastBriefDay { day }
}

enum PastBriefGroups {
    /// Rows grouped by the day they were written: today, yesterday, then each date. The rows keep their order.
    static func groups(_ entries: [TodayBriefIndex], today: String, yesterday: String) -> [PastBriefGroup] {
        var order: [PastBriefDay] = []
        var rows: [PastBriefDay: [TodayBriefIndex]] = [:]
        for entry in entries {
            let day = day(entry.localDate, today: today, yesterday: yesterday)
            if rows[day] == nil { order.append(day) }
            rows[day, default: []].append(entry)
        }
        return order.map { PastBriefGroup(day: $0, rows: rows[$0] ?? []) }
    }

    static func day(_ localDate: String, today: String, yesterday: String) -> PastBriefDay {
        if localDate == today { return .today }
        if localDate == yesterday { return .yesterday }
        return .date(localDate)
    }
}

/// The briefs the brief view moves through: those with words, newest first.
enum BriefPages {
    static func order(_ briefs: [MobileBriefV1]) -> [String] {
        briefs.filter(\.hasWords)
            .sorted { ($0.asOfDate ?? .distantPast) > ($1.asOfDate ?? .distantPast) }
            .map(\.identifier)
    }

    /// The brief written before and after `briefID`, in time order. `ids` is newest first.
    static func neighbours(of briefID: String, in ids: [String]) -> (previous: String?, next: String?) {
        guard let index = ids.firstIndex(of: briefID) else { return (nil, nil) }
        let previous = ids.indices.contains(index + 1) ? ids[index + 1] : nil
        let next = index > 0 ? ids[index - 1] : nil
        return (previous, next)
    }
}

enum PastBriefIndex {
    static func entries(snapshot: MobileTodaySnapshot?, cached: [MobileBriefV1], latestID: String?, now: Date) -> [TodayBriefIndex] {
        var rows = Dictionary((snapshot?.briefs ?? []).map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let generated = snapshot?.generatedDate
        for value in cached where rows[value.identifier] == nil {
            guard value.narrativeStatus == nil || value.narrativeStatus == "ok",
                  let made = value.asOfDate, let localDate = value.localDate,
                  let lead = lead(value) else { continue }
            if let generated, made <= generated { continue }
            rows[value.identifier] = TodayBriefIndex(id: value.identifier, slot: value.displaySlot ?? value.slot ?? "",
                                                     localDate: localDate, scheduledAt: value.dueAt, madeAt: value.asOf ?? "",
                                                     catchUp: nil, durationSeconds: value.audio?.durationSeconds, lead: lead,
                                                     kind: value.kind)
        }
        // Every brief the library keeps: full text for 30 days.
        let cutoff = now.addingTimeInterval(-BriefCache.textRetentionInterval)
        return rows.values.filter { $0.id != latestID && !$0.lead.isEmpty && ($0.madeDate ?? .distantPast) >= cutoff }
            .sorted { ($0.madeDate ?? .distantPast) > ($1.madeDate ?? .distantPast) }
    }

    /// A brief's lead: its first full sentence, as Chat, Past briefs and the "ready" notice show it. A greeting is
    /// never a lead of its own: "Good morning." stays joined to the sentence after it.
    static func lead(_ brief: MobileBriefV1) -> String? {
        guard let text = brief.narrative.lazy.compactMap({ $0.text?.trimmingCharacters(in: .whitespacesAndNewlines) })
            .first(where: { !$0.isEmpty }) else { return brief.yourWeek?.lead }
        guard let first = sentenceEnd(in: text, from: text.startIndex) else { return text }
        var end = first
        if isGreeting(String(text[..<first]).trimmingCharacters(in: .whitespacesAndNewlines)) {
            end = sentenceEnd(in: text, from: first) ?? text.endIndex
        }
        return String(text[..<end]).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private static func sentenceEnd(in text: String, from start: String.Index) -> String.Index? {
        text.range(of: #"[.!?](?:\s|$)|[。！？]"#, options: .regularExpression, range: start..<text.endIndex)?.upperBound
    }

    /// "Good morning.", "Good evening, Bo.", "Hi!", "早上好。" and the like.
    static func isGreeting(_ sentence: String) -> Bool {
        let english = #"^(good (morning|afternoon|evening|night)|morning|evening|hi|hello|hey)(,? [^.!?,]{1,24})?[.!?]$"#
        let chinese = #"^[^。！？]{0,8}(早上好|上午好|中午好|下午好|晚上好|早安|晚安|你好|您好)[^。！？]{0,8}[。！？!.]$"#
        return sentence.range(of: english, options: [.regularExpression, .caseInsensitive]) != nil
            || sentence.range(of: chinese, options: .regularExpression) != nil
    }
}
