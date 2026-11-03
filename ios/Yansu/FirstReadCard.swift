import SwiftUI

/// The checked portrait and its explicit answers (Phone Threads section 6).
struct FirstReadCard: View {
    let item: ChatProactiveView
    let chatID: String
    let onPortraitChanged: () -> Void
    let onCorrectionSent: () -> Void
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var chat: ChatStore
    @EnvironmentObject private var router: NotificationRouter
    @Environment(\.locale) private var locale
    @State private var confirmed = false
    @State private var confirmedAt: Date?
    @State private var correcting = false
    @State private var correction = ""
    @State private var busy = false
    @State private var undoRequested = false
    @State private var error = false
    @State private var timerID = UUID()

    private var language: String { ChatPresentationLanguage(locale: locale).code }
    private var helps: [String] { item.localized[language]?.helps ?? item.helps }
    private var showUndo: Bool { confirmedAt.map { Date().timeIntervalSince($0) < 5 } ?? false }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            YansuCard {
                VStack(alignment: .leading, spacing: 12) {
                    label(String(localized: "What I see so far"))
                        .accessibilityIdentifier("firstRead.label")
                    Text(verbatim: item.sentences.joined(separator: " "))
                        .font(.body)
                        .foregroundStyle(YansuTheme.ink)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("firstRead.sentences")
                    if confirmed {
                        VStack(alignment: .leading, spacing: 7) {
                            HStack(spacing: 7) {
                                Image(systemName: "checkmark").accessibilityHidden(true)
                                Text(String(localized: "You said that's you"))
                                    .accessibilityIdentifier("firstRead.confirmed")
                                if showUndo || (busy && !undoRequested) {
                                    Text("·").accessibilityHidden(true)
                                    Button(String(localized: "Undo")) { undo() }
                                        .accessibilityIdentifier("firstRead.undo")
                                }
                            }
                            .font(.subheadline.weight(.semibold))
                            .foregroundStyle(YansuTheme.accent)
                            Text(String(localized: "Your portrait in About you starts from this."))
                                .font(.subheadline)
                                .foregroundStyle(YansuTheme.secondary)
                        }
                    } else if correcting {
                        VStack(alignment: .leading, spacing: 10) {
                            TextField(String(localized: "What's off?"), text: $correction,
                                      prompt: Text(String(localized: "What's off?")).foregroundColor(YansuTheme.secondary),
                                      axis: .vertical)
                                .textFieldStyle(.roundedBorder)
                                .accessibilityIdentifier("firstRead.correction")
                            Button(String(localized: "Send")) { sendCorrection() }
                                .buttonStyle(FirstReadButtonStyle(prominent: true))
                                .disabled(busy || correction.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                                .accessibilityIdentifier("firstRead.send")
                        }
                    } else {
                        HStack(spacing: 8) {
                            Button(String(localized: "That's me")) { confirm() }
                                .buttonStyle(FirstReadButtonStyle(prominent: false))
                                .disabled(busy)
                                .accessibilityIdentifier("firstRead.confirm")
                            Button(String(localized: "Not quite")) {
                                correcting = true
                                onPortraitChanged()
                            }
                                .buttonStyle(FirstReadButtonStyle(prominent: false))
                                .disabled(busy)
                                .accessibilityIdentifier("firstRead.notQuite")
                        }
                    }
                    if error {
                        Text(String(localized: "Couldn't update this first read. Try again."))
                            .font(.footnote)
                            .foregroundStyle(YansuTheme.secondary)
                            .accessibilityIdentifier("firstRead.error")
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("firstRead.portrait")

            if helps.count >= 2 {
                YansuCard {
                    VStack(alignment: .leading, spacing: 12) {
                        label(String(localized: "How I'll help"))
                        ForEach(Array(helps.enumerated()), id: \.offset) { index, line in
                            HStack(alignment: .top, spacing: 10) {
                                // With fewer than three facts their positions no longer
                                // identify their sources, so use a neutral mark.
                                Image(systemName: helps.count == 3 ? ["envelope", "sun.max", "terminal"][min(index, 2)] : "circle")
                                    .foregroundStyle(YansuTheme.accent)
                                    .accessibilityHidden(true)
                                Text(verbatim: line)
                                    .foregroundStyle(YansuTheme.ink)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("firstRead.help")
            }
            if item.offerCount > 0 {
                YansuCard {
                    VStack(alignment: .leading, spacing: 10) {
                        label(String(localized: "Up next"))
                        Text(item.offerCount == 1
                             ? String(localized: "One thing I can take off your plate")
                             : String.localizedStringWithFormat(String(localized: "%d things I can take off your plate"), item.offerCount))
                            .font(.headline)
                            .foregroundStyle(YansuTheme.ink)
                        Text(String(localized: "In Tasks › Up next, each with what I saw. Nothing starts until you say Do it."))
                            .font(.subheadline)
                            .foregroundStyle(YansuTheme.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                        Button(String(localized: "See them")) { router.openUpNext() }
                            .buttonStyle(FirstReadButtonStyle(prominent: true))
                            .accessibilityIdentifier("firstRead.seeThem")
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("firstRead.offers")
            }
        }
        .onAppear { syncFromItem() }
        .onChange(of: item.state) { _, _ in if !busy { syncFromItem() } }
        .task(id: timerID) {
            guard let at = confirmedAt else { return }
            let delay = max(0, 5 - Date().timeIntervalSince(at))
            try? await Task.sleep(for: .seconds(delay))
            guard !Task.isCancelled else { return }
            confirmedAt = nil
        }
    }

    private func label(_ value: String) -> some View {
        Text(value)
            .font(.caption.weight(.semibold))
            .foregroundStyle(YansuTheme.secondary)
            .textCase(.uppercase)
    }

    private func syncFromItem() {
        confirmed = item.state == "acted"
        if let millis = item.firstReadConfirmedAt, confirmed {
            confirmedAt = Date(timeIntervalSince1970: Double(millis) / 1000)
            timerID = UUID()
        }
    }

    private func answer(_ action: String, text: String = "") async -> FirstReadAnswer? {
        let writer = JournaledWriter(journal: environment.journal,
                                     makeKey: { UUID().uuidString.lowercased() }, now: { Date() })
        let outcome = await writer.send(.chatProactiveAction, target: "first-read/\(item.momentID)", choice: action,
                                        fingerprint: WriteFingerprint.make([item.momentID, action, text])) { key in
            try await environment.firstReadHub.answerFirstRead(momentID: item.momentID, action: action, text: text, idempotencyKey: key)
        }
        switch outcome {
        case .sent(let answer): return answer
        case .cancelled: return nil
        case .notSaved, .failed: error = true; return nil
        }
    }

    private func confirm() {
        guard !busy else { return }
        busy = true
        error = false
        undoRequested = false
        confirmed = true
        confirmedAt = Date()
        timerID = UUID()
        onPortraitChanged()
        Task {
            guard let result = await answer("confirm_portrait") else {
                confirmed = false
                onPortraitChanged()
                busy = false
                return
            }
            if undoRequested {
                let undone = await answer("undo_portrait")
                confirmed = undone == nil
                confirmedAt = nil
                onPortraitChanged()
            } else {
                confirmedAt = result.confirmedAt.map { Date(timeIntervalSince1970: Double($0) / 1000) }
                timerID = UUID()
            }
            busy = false
            await chat.refreshAfterProactiveAction(chatID: chatID)
        }
    }

    private func undo() {
        if busy {
            undoRequested = true
            confirmed = false
            onPortraitChanged()
            return
        }
        busy = true
        error = false
        Task {
            if await answer("undo_portrait") != nil {
                confirmed = false
                confirmedAt = nil
                onPortraitChanged()
            }
            busy = false
            await chat.refreshAfterProactiveAction(chatID: chatID)
        }
    }

    private func sendCorrection() {
        guard !busy else { return }
        busy = true
        error = false
        let text = correction.trimmingCharacters(in: .whitespacesAndNewlines)
        Task {
            if await answer("correct_portrait", text: text) != nil {
                correcting = false
                correction = ""
                onCorrectionSent()
            }
            busy = false
            await chat.refreshAfterProactiveAction(chatID: chatID)
        }
    }
}

private struct FirstReadButtonStyle: ButtonStyle {
    let prominent: Bool
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.subheadline.weight(.semibold))
            .padding(.horizontal, 16)
            .padding(.vertical, 9)
            .foregroundStyle(prominent ? YansuTheme.onSolid : YansuTheme.ink)
            .background(prominent ? YansuTheme.solid : YansuTheme.surface, in: Capsule())
            .overlay { Capsule().strokeBorder(prominent ? YansuTheme.solid : YansuTheme.border, lineWidth: 1) }
            .opacity(configuration.isPressed ? 0.7 : 1)
    }
}
