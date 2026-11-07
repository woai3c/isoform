import SwiftUI

// The Today card (handoff 2026-10-28, sections 2 and 4). What it shows is decided by `TodayBriefCard`, which is a set
// of pure functions of what the phone holds, so every state of the design's table is tested without a view. The view
// lays out the decided lines and calls the player; it reads the brief from the phone's library and never fetches it.
enum TodayBriefCard {
    /// The most chips the card shows (design section 2).
    static let chipLimit = 3

    /// Which brief the card shows. The phone's current brief, unless the person is playing or reading the older brief the
    /// card held when a newer one arrived. That older brief stays, and the current one waits behind the newer-brief row.
    struct Choice: Equatable {
        let shown: MobileBriefV1
        /// The newer brief the card holds back, for the "A newer brief is ready" row.
        let newer: MobileBriefV1?
    }

    static func choice(current: MobileBriefV1, held: String?, library: [MobileBriefV1],
                       isBusy: (String) -> Bool) -> Choice {
        guard let held, held != current.identifier, isBusy(held),
              let kept = library.first(where: { $0.identifier == held }) else {
            return Choice(shown: current, newer: nil)
        }
        return Choice(shown: kept, newer: current)
    }

    /// The opening paragraph: the first paragraph that says something, other than the closing question. A greeting on
    /// its own is passed over for the paragraph after it, so the card never shows a bare greeting. Only a brief whose
    /// words are all greetings, or a brief with a single closing question, falls back to what it has.
    static func opening(_ brief: MobileBriefV1) -> (index: Int, text: String)? {
        let paragraphs: [(index: Int, text: String)] = brief.narrative.enumerated().compactMap { index, paragraph in
            guard let text = paragraph.text?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty else { return nil }
            return (index, text)
        }
        let spoken = paragraphs.filter { $0.index != brief.closingParagraphIndex }
        let candidates = spoken.isEmpty ? paragraphs : spoken
        return candidates.first { !isGreeting($0.text) } ?? candidates.first
    }

    /// The paragraphs whose chips the card shows: the first `chipLimit` paragraphs, in order, that have a chip. The
    /// closing question is not one of them; its answers are the brief's own.
    static func chipParagraphs(_ brief: MobileBriefV1) -> [Int] {
        brief.narrative.indices
            .filter { $0 != brief.closingParagraphIndex && brief.paragraphChip(forParagraph: $0) != nil }
            .prefix(chipLimit)
            .map { $0 }
    }

    private static let greetings: Set<String> = [
        "good morning", "good afternoon", "good evening", "good night", "hello", "hi",
        "早上好", "上午好", "中午好", "下午好", "晚上好", "你好",
    ]

    /// A paragraph that is only a greeting: "Good morning.", "早上好。".
    static func isGreeting(_ text: String) -> Bool {
        let word = text.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: ".!,。！，"))
            .lowercased()
        return greetings.contains(word)
    }

    /// Play is shown only for a brief with words (design section 2) once its audio can play: it is on the phone, the
    /// player already holds this brief (playing or paused, so the card is the player and the mini player steps aside,
    /// section 4), or the brief has audio on the computer while the computer can be reached (the tap fetches it,
    /// section 6). A brief whose package has no audio yet shows no Play: text first, audio right after.
    static func showsPlay(hasWords: Bool, hasAudio: Bool, holdsBrief: Bool, audioOnComputer: Bool,
                          computerOnline: Bool?) -> Bool {
        hasWords && (hasAudio || holdsBrief || audioOnComputer && computerOnline != false)
    }

    /// The wordless rows the card shows once, before any brief has arrived after pairing (design section 2).
    static func showsPlaceholder(hasBrief: Bool, placeholderShown: Bool) -> Bool {
        !hasBrief && !placeholderShown
    }

    /// "Making a new brief · ready around 8:43 AM", while a requested brief is being made. Nil for any other state.
    static func makingLine(_ request: TodayBriefRequest?, time: (Date) -> String) -> String? {
        guard let request, ["queued", "making"].contains(request.state), let ready = request.readyDate else { return nil }
        return String(format: String(localized: "Making a new brief · ready around %@"), time(ready))
    }

    /// "A newer brief is ready · 8:43 AM ›", the row for a newer brief the card holds back.
    static func newerLine(_ newer: MobileBriefV1, time: (Date) -> String) -> String {
        let shown = BriefTitle.timeText(BriefTitleInput(brief: newer)) ?? time(newer.asOfDate ?? Date())
        return String(format: String(localized: "A newer brief is ready · %@ ›"), shown)
    }

    /// The scheduled brief that is due while the Mac is asleep and that the current brief does not cover: its time, or
    /// nil. A slot counts from its time today or yesterday, and the current brief covers it when it was made at or after
    /// the slot (within the late tolerance).
    static func dueWhileAsleep(_ schedule: MobileBriefSchedule, current: MobileBriefV1, asleep: Bool, now: Date,
                               calendar: Calendar = .current) -> Date? {
        guard asleep else { return nil }
        let times = [schedule.morning, schedule.evening].compactMap { $0 }
        let days = [0, -1].compactMap { calendar.date(byAdding: .day, value: $0, to: now) }
        let due = days.flatMap { day in times.compactMap { clockDate($0, on: day, calendar: calendar) } }
            .filter { $0 <= now }
            .max()
        guard let due else { return nil }
        let made = current.asOfDate ?? .distantPast
        return made >= due.addingTimeInterval(-BriefTitle.lateTolerance) ? nil : due
    }

    /// "Your 6:50 AM brief will come when your computer wakes."
    static func waitingLine(_ due: Date, time: (Date) -> String) -> String {
        String(format: String(localized: "Your %@ brief will come when your computer wakes."), time(due))
    }

    /// The local time of an "HH:mm" slot on a day, or nil for any other text.
    static func clockDate(_ value: String, on day: Date, calendar: Calendar = .current) -> Date? {
        let parts = value.split(separator: ":").compactMap { Int($0) }
        guard parts.count == 2, (0..<24).contains(parts[0]), (0..<60).contains(parts[1]) else { return nil }
        return calendar.date(bySettingHour: parts[0], minute: parts[1], second: 0, of: day)
    }
}

/// Whether the wordless placeholder rows have been shown for this pairing. They show once: the first time the card has no
/// brief to show after pairing (design section 2).
enum TodayBriefPlaceholder {
    static func key(hubID: String) -> String { "today.briefPlaceholderShown." + hubID }

    static func wasShown(hubID: String, defaults: UserDefaults = .standard) -> Bool {
        defaults.bool(forKey: key(hubID: hubID))
    }

    static func markShown(hubID: String, defaults: UserDefaults = .standard) {
        defaults.set(true, forKey: key(hubID: hubID))
    }

    static func reset(hubID: String, defaults: UserDefaults = .standard) {
        defaults.removeObject(forKey: key(hubID: hubID))
    }
}

/// The Today card for the phone's current brief. It holds an older brief only while the person plays or reads it
/// (`held`), so a newer brief replaces the old one in place with a crossfade otherwise.
struct TodayBriefCardView: View {
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    let current: MobileBriefV1
    let hubID: String
    @ObservedObject var player: BriefAudioPlayer
    var topics: TopicStore? = nil
    var onPrepareInTopic: ((String, String) -> Bool)? = nil
    let canMakeBrief: Bool
    let now: Date
    var onPastBriefs: () -> Void

    @EnvironmentObject private var brief: BriefStore
    @EnvironmentObject private var todayStore: TodayStore
    @EnvironmentObject private var onScreen: BriefOnScreen
    @Environment(\.locale) private var locale
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var held: String?
    /// Whether the shown brief's audio is on the phone. It is read when the brief or the audio changes, not on every
    /// playback tick, which re-renders this card.
    @State private var hasAudio = false

    var body: some View {
        let isBusy = busy
        let choice = TodayBriefCard.choice(current: current, held: held, library: brief.cachedBriefs, isBusy: isBusy)
        let shown = choice.shown
        let holdsBrief = player.started && player.hubID == hubID && player.brief?.identifier == shown.identifier
        let asleep = todayStore.hubUnreachable || brief.hubOnline == false
        let schedule = todayStore.snapshot?.schedule ?? MobileBriefSchedule(morning: nil, evening: nil)
        let due = TodayBriefCard.dueWhileAsleep(schedule, current: shown, asleep: asleep, now: now)
        let entry = HistoryEntry(id: shown.identifier, slot: shown.slot, localDate: shown.localDate,
                                 asOf: shown.asOfDate, narrativeStatus: shown.narrativeStatus, isCached: true,
                                 displaySlot: shown.displaySlot, timezone: shown.timezone,
                                 kind: shown.kind, dueAt: shown.dueAt)
        YansuCard {
            VStack(alignment: .leading, spacing: 14) {
                if let making = TodayBriefCard.makingLine(todayStore.snapshot?.briefRequest, time: time) {
                    Text(making)
                        .font(.footnote)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("feed.brief.making")
                }
                HStack(alignment: .top, spacing: 8) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(verbatim: TodayAwaitingBrief.cardTitle(shown, now: now, locale: locale))
                            .font(.subheadline.weight(.semibold))
                            .foregroundStyle(YansuTheme.secondary)
                            .accessibilityIdentifier("feed.brief.title")
                        if let late = shown.lateLineText {
                            Text(late)
                                .font(.footnote)
                                .foregroundStyle(YansuTheme.secondary)
                                .accessibilityIdentifier("feed.brief.late")
                        }
                    }
                    Spacer(minLength: 0)
                    Menu {
                        Button("Make my brief now") { Task { await todayStore.requestFresh() } }
                            .disabled(!canMakeBrief)
                    } label: {
                        // P134: the 44 pt tap target stays, but its height no longer adds a band under the title row.
                        Image(systemName: "ellipsis").frame(width: 44, height: 44)
                            .padding(.vertical, -12)
                    }
                    .accessibilityLabel("More options")
                }
                if TodayBriefCard.showsPlay(hasWords: shown.hasWords, hasAudio: hasAudio, holdsBrief: holdsBrief,
                                            audioOnComputer: shown.audio?.isFinished == true && !shown.audioIsWithdrawn,
                                            computerOnline: asleep ? false : brief.hubOnline) {
                    HStack(spacing: 10) {
                        TodayBriefPlayButton(brief: shown, hubID: hubID, player: player, hasAudio: hasAudio)
                        if let length = shown.audio?.durationLabel {
                            Text(verbatim: length)
                                .font(.footnote.weight(.medium))
                                .foregroundStyle(YansuTheme.secondary)
                                .accessibilityIdentifier("feed.brief.length")
                        }
                    }
                }
                BriefCardsView(brief: shown, showsAttention: false, showsParagraphs: true, topics: topics,
                               onPrepareInTopic: onPrepareInTopic, preview: true)
                if let due {
                    Text(TodayBriefCard.waitingLine(due, time: time))
                        .font(.footnote)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("feed.brief.waiting")
                }
                if todayStore.freshFailed {
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text("Couldn't make the new brief.")
                            .font(.footnote)
                            .foregroundStyle(YansuTheme.secondary)
                        Spacer(minLength: 0)
                        Button("Make it now") { Task { await todayStore.requestFresh() } }
                            .font(.footnote.weight(.semibold))
                            .foregroundStyle(YansuTheme.accent)
                            .disabled(!canMakeBrief)
                            .accessibilityIdentifier("feed.brief.makeNow")
                    }
                    .accessibilityIdentifier("feed.brief.failed")
                }
                if let notice = brief.notice {
                    NoticeBanner(message: notice, dismiss: brief.dismissNotice)
                }
                if let newer = choice.newer {
                    Button {
                        held = nil
                    } label: {
                        HStack {
                            Text(TodayBriefCard.newerLine(newer, time: time))
                            Spacer(minLength: 0)
                        }
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(YansuTheme.accent)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("feed.brief.newer")
                }
                // Side-by-side links stack at accessibility sizes, so their labels never run into each other.
                let readAll = NavigationLink {
                    BriefDetailView(entry: entry, player: player,
                                    fallbackLead: todayStore.snapshot?.briefs.first { $0.id == shown.identifier }?.lead)
                } label: {
                    Text("Read all")
                }
                .accessibilityIdentifier("feed.readAll")
                let pastBriefs = Button(action: onPastBriefs) {
                    HStack(spacing: 4) {
                        Text("Past briefs")
                        Image(systemName: "chevron.right").font(.caption.weight(.semibold))
                    }
                }
                .accessibilityIdentifier("feed.pastBriefs")
                // The two links sit at the card's two ends at the default size, and stack at accessibility sizes (P149).
                if dynamicTypeSize.isAccessibilitySize {
                    VStack(alignment: .leading, spacing: 10) { readAll; pastBriefs }
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(YansuTheme.accent)
                } else {
                    HStack { readAll; Spacer(minLength: 12); pastBriefs }
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(YansuTheme.accent)
                }
            }
        }
        .id(shown.identifier)
        .transition(.opacity)
        .animation(reduceMotion ? nil : .easeInOut(duration: 0.3), value: shown.identifier)
        .briefOnScreen(shown.identifier)
        .onChange(of: current.identifier) { old, _ in
            // The brief on the card stays while the person plays or reads it.
            if isBusy(old) { held = old }
        }
        .onChange(of: held.map(isBusy) ?? false) { _, stillBusy in
            if !stillBusy { held = nil }
        }
        .onChange(of: AudioCheck(briefID: shown.identifier, revision: player.audioRevision), initial: true) { _, _ in
            hasAudio = player.hasAudioOnPhone(brief: shown, hubID: hubID)
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("feed.brief")
    }

    /// The brief is busy while the person plays it (the player holds it and has started it, until it ends) or reads it.
    private func busy(_ briefID: String) -> Bool {
        let playing = player.brief?.identifier == briefID && player.hubID == hubID && player.started && !player.finished
        return playing || onScreen.isReading(briefID)
    }

    private func time(_ date: Date) -> String {
        BriefClock.text(date, locale: locale)
    }

    /// What the card's audio check depends on: the brief shown, and the player's revision when audio is stored or dropped.
    private struct AudioCheck: Equatable {
        let briefID: String
        let revision: Int
    }
}

/// The Play control on the Today card: a play or pause symbol with a ring for the progress of the brief it plays. It shows
/// no spinner; a tap while the audio is still being prepared simply waits.
private struct TodayBriefPlayButton: View {
    let brief: MobileBriefV1
    let hubID: String
    @ObservedObject var player: BriefAudioPlayer
    /// Whether the audio is on the phone. When it is not, the tap fetches it from the computer.
    let hasAudio: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let selected = player.matches(brief: brief, hubID: hubID)
        let playing = selected && player.state == .playing
        let progress = selected && player.duration > 0 ? min(1, max(0, player.position / player.duration)) : 0
        Button {
            let tappedAt = ProcessInfo.processInfo.systemUptime
            Task {
                await Task.yield()
                await player.toggle(brief: brief, hubID: hubID, tappedAt: tappedAt, localFirst: hasAudio)
            }
        } label: {
            Image(systemName: playing ? "pause.fill" : "play.fill")
                .contentTransition(.symbolEffect(.replace))
                .animation(reduceMotion ? nil : .easeInOut(duration: 0.25), value: playing)
                .foregroundStyle(YansuTheme.accent)
                .frame(width: 40, height: 40)
                .background(YansuTheme.accentWash, in: Circle())
                .overlay {
                    if progress > 0 {
                        Circle()
                            .trim(from: 0, to: progress)
                            .stroke(YansuTheme.accent, style: StrokeStyle(lineWidth: 2, lineCap: .round))
                            .rotationEffect(.degrees(-90))
                            .padding(-3)
                    }
                }
        }
        .buttonStyle(.plain)
        .accessibilityLabel(playing ? "Pause your brief" : "Hear your brief")
        .accessibilityValue(selected ? "\(BriefPlaybackTime.text(player.position)) / \(BriefPlaybackTime.text(player.duration))" : "")
        .accessibilityIdentifier("brief.hearBrief")
    }
}
