import SwiftUI

struct BriefAskRequest: Identifiable {
    let id = UUID()
    let reference: ChatBriefReference
    let quote: String?
    var initialQuestion: String? = nil
    var openingQuestion: String? = nil
    var answerChoices: [MobileBriefCard] = []
}

/// A brief Ask uses the main chat's durable outgoing queue and its chat_turn event stream.
struct BriefAskSheet: View {
    let request: BriefAskRequest
    @EnvironmentObject private var chat: ChatStore
    @EnvironmentObject private var router: NotificationRouter
    @Environment(\.dismiss) private var dismiss
    @FocusState private var focused: Bool
    @State private var text = ""
    @State private var question: String?
    @State private var chatID: String?
    @State private var messageID: String?
    @State private var anchor: Int?
    @State private var submitting = false
    @State private var stoppedAnswer: String?
    @State private var savedAnswer: String?
    @State private var editingFailedMessageID: String?
    @State private var selectedReference: ChatBriefReference?
    @State private var openerReady = false

    private var outgoing: ChatOutgoingMessage? {
        guard let chatID, let messageID else { return nil }
        return chat.outgoingMessages(in: chatID).first { $0.id == messageID }
    }

    private var streamFailed: Bool {
        turnState == "error"
    }

    private var canSubmit: Bool {
        !submitting && (request.openingQuestion == nil || openerReady) && !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private var turnState: String? {
        guard let chatID, let turnID = outgoing?.turnID else { return nil }
        return chat.recentTurnEvents.last { $0.chatId == chatID && $0.turnId == turnID }?.state
    }

    private var answer: String {
        if let stoppedAnswer { return stoppedAnswer }
        if let savedAnswer { return savedAnswer }
        guard let chatID, chat.transcript?.header.id == chatID else { return "" }
        if let live = chat.transcript?.live, let turnID = outgoing?.turnID, live.turnId == turnID {
            return live.text
        }
        guard
              let seq = anchor ?? outgoing?.seq, let rows = chat.transcript?.items else { return "" }
        let following = rows.filter { $0.seq > seq }.sorted { $0.seq < $1.seq }
        return following.prefix { $0.role != .user }
            .filter { $0.role == .assistant && $0.kind == .text }
            .map(\.text).filter { !$0.isEmpty }.joined(separator: "\n\n")
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        if let quote = request.quote {
                            Text(verbatim: quote)
                                .foregroundStyle(YansuTheme.secondary)
                                .padding(14)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(YansuTheme.surfaceWarm, in: RoundedRectangle(cornerRadius: 14))
                                .accessibilityIdentifier("brief.ask.quote")
                        }
                        if let openingQuestion = request.openingQuestion {
                            Text(verbatim: openingQuestion)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(14)
                                .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: 14))
                                .accessibilityIdentifier("brief.ask.opener")
                            if question == nil {
                                ForEach(request.answerChoices) { card in
                                    Button {
                                        var reference = request.reference
                                        reference.ask = true
                                        reference.cardId = card.id
                                        reference.cardMessage = card.message
                                        selectedReference = reference
                                        submit(card.message)
                                    } label: { Text(verbatim: card.label ?? "") }
                                        .buttonStyle(YansuChipButtonStyle())
                                        .disabled(!openerReady)
                                        .accessibilityIdentifier("brief.ask.choice.\(card.id)")
                                }
                            }
                        }
                        if let question {
                            HStack {
                                Spacer(minLength: 32)
                                Text(verbatim: question)
                                    .padding(.horizontal, 14).padding(.vertical, 10)
                                    .foregroundStyle(YansuTheme.onSolid)
                                    .background(YansuTheme.solid, in: RoundedRectangle(cornerRadius: 18))
                                    .accessibilityIdentifier("brief.ask.question")
                                    .opacity(outgoing?.hasFailed == true ? 0.55 : 1)
                            }
                            if !answer.isEmpty {
                                Text(verbatim: answer)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(14)
                                    .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: 14))
                                    .accessibilityIdentifier("brief.ask.answer")
                                if savedAnswer != nil || turnState == "done" {
                                    Button("Refresh") { submit(question) }
                                        .font(.footnote.weight(.semibold)).foregroundStyle(YansuTheme.accent)
                                        .disabled(submitting)
                                        .accessibilityIdentifier("brief.ask.refresh")
                                }
                            } else if outgoing?.isQueued == true && (chat.hubOnline == false || outgoing?.deliveryIssue == .waitingForComputer) {
                                HStack {
                                    Text("Sending when your computer is back")
                                        .accessibilityIdentifier("brief.ask.waiting")
                                    if outgoing?.askUndispatched == true, let chatID, let messageID {
                                        Button("Undo") {
                                            Task {
                                                if await chat.undoBriefAsk(chatID: chatID, messageID: messageID) {
                                                    text = question
                                                    self.question = nil
                                                    self.messageID = nil
                                                    focused = true
                                                }
                                            }
                                        }
                                        .accessibilityIdentifier("brief.ask.undo")
                                    }
                                }
                                .font(.footnote).foregroundStyle(YansuTheme.secondary)
                            } else if outgoing?.hasFailed == true {
                                HStack {
                                    Text("Didn't send")
                                    Button("Try again") { submit(question) }
                                        .accessibilityIdentifier("brief.ask.retry")
                                    Button("Edit") {
                                        editingFailedMessageID = messageID
                                        text = question
                                        self.question = nil
                                        messageID = nil
                                        focused = true
                                    }
                                    .accessibilityIdentifier("brief.ask.edit")
                                }
                                .font(.footnote).foregroundStyle(YansuTheme.secondary)
                            } else if stoppedAnswer == nil && !streamFailed {
                                Text(verbatim: "...").foregroundStyle(YansuTheme.secondary)
                            }
                            if stoppedAnswer != nil {
                                HStack {
                                    Text("Stopped")
                                    Button("Start over") {
                                        stoppedAnswer = nil
                                        self.question = nil
                                        messageID = nil
                                        anchor = nil
                                        focused = true
                                    }
                                    .accessibilityIdentifier("brief.ask.startOver")
                                }
                                .font(.footnote).foregroundStyle(YansuTheme.secondary)
                            } else if streamFailed {
                                HStack {
                                    Text("The answer stopped.")
                                        .accessibilityIdentifier("brief.ask.streamFailed")
                                    Button("Try again") { submit(question) }
                                        .accessibilityIdentifier("brief.ask.streamRetry")
                                }
                                .font(.footnote).foregroundStyle(YansuTheme.secondary)
                            } else if let chatID, let turnID = outgoing?.turnID, turnState == "running" {
                                Button("Stop") {
                                    let partial = answer
                                    Task {
                                        if await chat.stopBriefAsk(chatID: chatID, turnID: turnID) {
                                            stoppedAnswer = partial
                                        }
                                    }
                                }
                                .font(.footnote.weight(.semibold)).foregroundStyle(YansuTheme.accent)
                                .accessibilityIdentifier("brief.ask.stop")
                            }
                        }
                        if let notice = chat.notice, outgoing?.deliveryIssue != .waitingForComputer,
                           outgoing?.hasFailed != true {
                            Text(verbatim: notice).font(.footnote).foregroundStyle(YansuTheme.secondary)
                        }
                    }
                    .padding(YansuTheme.gutter)
                }
                HStack(spacing: 10) {
                    TextField(question != nil ? String(localized: "Ask a follow-up") :
                                request.openingQuestion != nil ? String(localized: "Ask about this brief") :
                                String(localized: "Ask anything about this paragraph"), text: $text, axis: .vertical)
                        .focused($focused)
                        .padding(.horizontal, 14)
                        .frame(minHeight: 44)
                        .background(YansuTheme.composer, in: Capsule())
                        .overlay { Capsule().strokeBorder(YansuTheme.composerBorder, lineWidth: 1) }
                        .layoutPriority(1)
                        .accessibilityIdentifier("brief.ask.field")
                    Button { submit(text) } label: {
                        Text("Ask").font(.subheadline.weight(.semibold))
                            .foregroundStyle(canSubmit ? YansuTheme.onSolid : YansuTheme.secondary)
                            .padding(.horizontal, 18).frame(minHeight: 44)
                            .background(canSubmit ? YansuTheme.solid : YansuTheme.surfaceWarm, in: Capsule())
                    }
                        .buttonStyle(.plain)
                        .fixedSize(horizontal: true, vertical: false)
                        .disabled(!canSubmit)
                        .accessibilityIdentifier("brief.ask.send")
                }
                .padding(YansuTheme.gutter)
            }
            .background(YansuTheme.background)
            .navigationTitle(String(localized: "Ask about this"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    if question != nil {
                        Button(String(localized: "Open in Chat")) { dismiss(); router.openMainChat() }
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(String(localized: "Done")) { dismiss() }
                        .accessibilityIdentifier("brief.ask.done")
                }
            }
            .task { await chat.watchForEvents() }
            .task {
                await chat.refreshBriefAskPresence()
                if request.openingQuestion != nil {
                    if chat.hubOnline != false { await chat.postBriefOpener(reference: request.reference) }
                    openerReady = true
                    return
                }
                let saved = chat.hubOnline == false ? nil : await chat.savedBriefAsk(reference: request.reference)
                guard question == nil, text.isEmpty else { return }
                if let saved {
                    question = saved.question
                    savedAnswer = saved.answer
                    focused = false
                } else if let initial = request.initialQuestion {
                    if let existing = chat.existingBriefAsk(reference: request.reference, question: initial) {
                        chatID = existing.chatID
                        messageID = existing.messageID
                        question = initial
                    } else {
                        submit(initial)
                    }
                }
            }
            .onAppear { focused = request.initialQuestion == nil && request.openingQuestion == nil }
            .onChange(of: outgoing?.seq) { _, value in if let value { anchor = value } }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("brief.ask.sheet")
        }
    }

    private func submit(_ input: String) {
        let words = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !words.isEmpty, !submitting else { return }
        let previousFailure = outgoing?.hasFailed == true ? messageID : editingFailedMessageID
        submitting = true
        Task {
            let sent = await chat.sendBriefAsk(words, reference: selectedReference ?? request.reference) { chatID, messageID in
                if let previousFailure {
                    chat.discardFailedBriefAsk(chatID: chatID, messageID: previousFailure)
                }
                self.editingFailedMessageID = nil
                self.chatID = chatID
                self.messageID = messageID
                self.anchor = nil
                self.savedAnswer = nil
                self.question = words
                self.text = ""
                self.focused = false
            }
            if !sent { text = words }
            submitting = false
        }
    }
}
