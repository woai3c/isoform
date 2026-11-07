import SwiftUI

/// The same waiting card can appear in Tasks and in Today's first three rows.
struct PhoneWaitingProactiveRow: View {
    let need: TodayNeed
    @Environment(\.locale) private var locale
    @State private var showing = false

    var body: some View {
        if let item = need.card?.proactive, let chatID = need.chatId {
            Button { showing = true } label: {
                HStack(alignment: .center, spacing: 12) {
                    Circle().fill(YansuTheme.warning).frame(width: 8, height: 8)
                    VStack(alignment: .leading, spacing: 4) {
                        Text(item.copy(language: ChatPresentationLanguage(locale: locale).code).headline)
                            .font(.subheadline.weight(.semibold)).foregroundStyle(YansuTheme.ink)
                        if let source = item.evidence.first {
                            Text(source.sourceLabel(language: ChatPresentationLanguage(locale: locale).code))
                                .font(.footnote).foregroundStyle(YansuTheme.secondary)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    // P135: a row's action is always the outlined pill, as the task rows show it.
                    Text(ChatCardButtons.make(for: item).main?.label(locale: locale) ?? String(localized: "Review"))
                        .font(.footnote.weight(.semibold)).foregroundStyle(YansuTheme.ink)
                        .padding(.horizontal, 12).padding(.vertical, 6)
                        .overlay(Capsule().stroke(YansuTheme.border))
                }
                .padding(16).contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("today.waiting.\(need.id)")
            .sheet(isPresented: $showing) {
                ChatProactiveItemView(item: item, chatID: chatID, showsSource: true, queuesForToday: true) { showing = false }
            }
        }
    }
}

struct ChatProactiveItemView: View {
    let item: ChatProactiveView
    let chatID: String
    var showsSource = false
    var queuesForToday = false
    let onClose: () -> Void
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var pairing: PairingStore
    @Environment(\.locale) private var locale

    var body: some View {
        VStack(spacing: 8) {
            HStack {
                Spacer()
                Button(action: onClose) {
                    Image(systemName: "xmark")
                        .frame(width: 44, height: 44)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .keyboardShortcut(.cancelAction)
                .accessibilityLabel(String(localized: "Close"))
                .accessibilityIdentifier("chat.proactive.close")
            }
            ScrollView {
                if item.kind == "commitment_slipping" {
                    PhoneFindTimeAnswer(hub: environment.hub)
                } else {
                    ChatProactiveCard(item: item, isSelected: true, showsDetails: true,
                                      onOpen: onClose,
                                      performMute: {
                                          await perform("mute_kind")
                                      }) { action in
                        await perform(action.id)
                    }
                    .environmentObject(environment)
                    .environmentObject(pairing)
                    .padding(.bottom, 20)
                    if showsSource, item.target.kind == "mail" {
                        PhoneSourceLink(kind: .mail)
                    } else if showsSource, item.target.kind == "calendar",
                              let start = ChatCardButtons.eventStart(item).flatMap(RelayTime.parse) {
                        PhoneSourceLink(kind: .calendar(start))
                    }
                }
            }
        }
        .padding(.horizontal, YansuTheme.gutter)
        .background(YansuTheme.background)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("chat.proactive.item")
    }

    private func perform(_ actionID: String) async -> ProactiveActionOutcome {
        if queuesForToday {
            let outcome = await environment.actions.queueTodayAction(chatID: chatID, item: item, actionID: actionID, locale: locale)
            if case .pending = outcome, !environment.today.hubUnreachable, environment.brief.hubOnline != false {
                await environment.actions.retryToday()
                await environment.today.refresh()
            }
            return outcome
        }
        return await environment.actions.performProactiveAction(chatID: chatID, momentID: item.momentID,
                                                                 actionID: actionID,
                                                                 permissionRequest: item.permission != nil, locale: locale)
    }
}

struct PhoneSourceLink: View {
    enum Kind { case mail, calendar(Date) }
    let kind: Kind
    @Environment(\.openURL) private var openURL
    @State private var failed = false

    private var url: URL? {
        switch kind {
        case .mail: return URL(string: "message://")
        case .calendar(let date): return URL(string: "calshow:\(date.timeIntervalSinceReferenceDate)")
        }
    }
    private var title: String {
        switch kind {
        case .mail: return String(localized: "today.source.openMail")
        case .calendar: return String(localized: "today.source.openCalendar")
        }
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Button(title) { open() }.foregroundStyle(YansuTheme.accent)
                .accessibilityIdentifier("today.source.open")
            if failed {
                HStack {
                    Text(String(localized: "today.source.failed")).foregroundStyle(YansuTheme.secondary)
                    Button(String(localized: "Try again")) { open() }
                }.font(.footnote)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("today.source")
    }
    private func open() {
        guard let url else { failed = true; return }
        openURL(url) { accepted in failed = !accepted }
    }
}

struct PhoneFindTimeAnswer: View {
    let hub: HubChannel
    @Environment(\.locale) private var locale
    @State private var details: PhoneFreeTimeDetails?
    @State private var failed = false

    var body: some View {
        Group {
            if failed {
                Text("findTime.failed")
            } else if let details {
                if let answer = PhoneFindTimeCopy.answer(details, locale: locale) {
                    Text(verbatim: answer)
                } else {
                    Text("findTime.none")
                }
            } else {
                Text("findTime.checking")
            }
        }
        .font(.body)
        .foregroundStyle(YansuTheme.ink)
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityIdentifier("chat.findTime.answer")
        .task {
            do { details = try await hub.findTime() }
            catch { failed = true }
        }
    }
}

struct PhoneFindTimeSheet: View {
    let hub: HubChannel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            PhoneFindTimeAnswer(hub: hub)
                .padding(YansuTheme.gutter)
                .navigationTitle(String(localized: "mainChat.card.button.findTime"))
                .toolbar { Button("Done") { dismiss() } }
        }
    }
}

enum PhoneFindTimeCopy {
    static func answer(_ details: PhoneFreeTimeDetails, locale: Locale) -> String? {
        guard let plain = answer(details.ranges, locale: locale) else { return nil }
        guard !details.browserOnly.isEmpty else { return plain }
        let list = ListFormatter()
        list.locale = locale
        let checked = list.string(from: details.checkedCalendars) ?? ""
        let browser = list.string(from: details.browserOnly) ?? ""
        let timesOnly = answerTimes(details.ranges, locale: locale) ?? ""
        let key = checked.isEmpty ? "findTime.checkedGeneric" : "findTime.checked"
        let format = String(localized: String.LocalizationValue(key), locale: locale)
        let first = checked.isEmpty ? String(format: format, locale: locale, arguments: [timesOnly]) : String(format: format, locale: locale, arguments: [checked, timesOnly])
        let warning = String(format: String(localized: "findTime.browserOnly", locale: locale), locale: locale, arguments: [browser])
        return "\(first) \(warning)"
    }

    static func answer(_ ranges: [PhoneFreeTimeRange], locale: Locale) -> String? {
        guard let joined = answerTimes(ranges, locale: locale) else { return nil }
        return String(format: String(localized: "findTime.answer", locale: locale), locale: locale, arguments: [joined])
    }

    private static func answerTimes(_ ranges: [PhoneFreeTimeRange], locale: Locale) -> String? {
        let startFormat = DateFormatter()
        startFormat.locale = locale
        startFormat.setLocalizedDateFormatFromTemplate("EEEjm")
        let endFormat = DateFormatter()
        endFormat.locale = locale
        endFormat.setLocalizedDateFormatFromTemplate("jm")
        let template = String(localized: "findTime.range", locale: locale)
        let values = ranges.compactMap { range -> String? in
            guard let start = RelayTime.parse(range.start), let end = RelayTime.parse(range.end), end > start else { return nil }
            return String(format: template, locale: locale, arguments: [startFormat.string(from: start), endFormat.string(from: end)])
        }
        guard !values.isEmpty else { return nil }
        let list = ListFormatter()
        list.locale = locale
        guard let joined = list.string(from: values) else { return nil }
        return joined
    }
}

enum ChatProactiveLookup {
    static func find(momentID: String, in items: [ChatItemView]) -> ChatProactiveView? {
        for item in items {
            guard let proactive = item.proactive else { continue }
            if proactive.momentID == momentID { return proactive }
            if let nested = find(momentID: momentID, in: proactive.items), nested.momentID == momentID { return nested }
        }
        return nil
    }

    private static func find(momentID: String, in items: [ChatProactiveView]) -> ChatProactiveView? {
        for item in items {
            if item.momentID == momentID { return item }
            if let nested = find(momentID: momentID, in: item.items) { return nested }
        }
        return nil
    }
}
