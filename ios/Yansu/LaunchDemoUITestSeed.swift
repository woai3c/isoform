#if DEBUG && targetEnvironment(simulator)
import Foundation

/// The launch demo's sample morning (YANSU_UITEST_SETTINGS_STATE=launch-demo): Bo's brief about Lena's 2:00, the two
/// failing Halcyon tests and Priya's reply; Priya's waiting reply with its draft in the Thursday review topic; and an
/// answer to one question asked there. Everything is made up; the hubs answer in-process and nothing leaves the
/// Simulator. Times are read on a demo clock that starts at 9:41 AM today, the status bar's time in the recordings.
enum LaunchDemoUITestSeed {
    static let chatID = ChatUITestSeed.mainChatID
    static let priyaMomentID = "demo-priya-reply"
    static let thursday = "topic-thursday-review"
    static let halcyon = "topic-halcyon-import"
    static let boardDeck = "topic-board-deck"
    static let answer = "Bring the import numbers. The two date parser tests still fail; the fix you sketched yesterday "
        + "is on the branch, so finish it before your 2:00 with Lena."

    private static let launchedAt = Date()
    private static let startsAt = Calendar.current.date(bySettingHour: 9, minute: 41, second: 0, of: launchedAt) ?? launchedAt

    /// The demo clock: 9:41 AM at launch, then running.
    static func now() -> Date { startsAt.addingTimeInterval(Date().timeIntervalSince(launchedAt)) }
    static func at(hour: Int, minute: Int, daysAgo: Int = 0) -> Date {
        let day = Calendar.current.date(byAdding: .day, value: -daysAgo, to: startsAt) ?? startsAt
        return Calendar.current.date(bySettingHour: hour, minute: minute, second: 0, of: day) ?? day
    }

    static let paragraphs = [
        "Good morning, Bo. 3 meetings today, and the one to prepare for is your 2:00 with Lena. Last time she asked whether the Halcyon import can take the full customer file.",
        "Halcyon is close. Yesterday’s session left two tests failing in the date parser, and the fix you sketched is still on the branch.",
        "Priya asked to move Thursday’s review to the afternoon. A short reply in your voice is ready for you to send.",
    ]

    static func brief() -> MobileBriefV1 {
        let asOf = at(hour: 7, minute: 0)
        let localDate = FeedAssembly.localDate(for: asOf)
        var brief = MobileBriefV1(version: 1, briefId: "\(localDate)-morning", slot: "morning", localDate: localDate,
                                  timezone: TimeZone.current.identifier, asOf: stamp(asOf), hubName: "Alex's MacBook Pro",
                                  narrativeStatus: "ok",
                                  narrative: [
                                      BriefParagraph(text: paragraphs[0]),
                                      BriefParagraph(text: paragraphs[1], topicId: halcyon, topicName: "Halcyon import"),
                                      BriefParagraph(text: paragraphs[2], topicId: thursday, topicName: "Thursday review"),
                                  ],
                                  conversationKey: "demo-morning-1")
        brief.cards = [
            MobileBriefCard(id: "lena", kind: "question", mode: "send", label: "Prep me for my 2:00 with Lena",
                            message: "Prep me for my 2:00 with Lena.", paragraph: 0),
            MobileBriefCard(id: "priya", kind: "question", mode: "send", label: "Reply to Priya about Thursday",
                            message: "Reply to Priya about Thursday.", paragraph: 2),
        ]
        return brief
    }

    static func audioParagraphs(duration: Double) -> [BriefAudioParagraph] {
        paragraphs.indices.map { BriefAudioParagraph(source: "narrative", index: $0,
                                                      startSeconds: Double($0) * duration / Double(paragraphs.count)) }
    }

    /// Priya's waiting reply as the computer sends it: the draft is complete, so the topic offers Send.
    static func priyaCard(state: String = "pending") -> [String: Any] {
        ["momentId": priyaMomentID, "kind": "waiting_on_reply",
         "headline": "Priya is waiting on your reply",
         "body": "She asked to move Thursday’s review to the afternoon. The reply says yes and offers 2:30.",
         "draft": true, "actionIds": ["send", "copy_draft", "open", "not_now"],
         "actionLabels": ["copy_draft": "Copy draft", "send": "Send", "open": "Open Mail", "not_now": "Not now"],
         "draftContent": [
             "to": "priya@halcyon.example", "subject": "Re: Thursday review",
             "text": "Hi Priya,\n\nThursday afternoon works. Let’s do 2:30, same room. I’ll bring the import numbers.\n\nBo",
             "channel": "mail", "recipient": "Priya", "principalId": "priya", "revision": "priya-1",
         ],
         "target": ["kind": "mail"], "evidence": [], "state": state]
    }

    static func priyaRow(seq: Int, state: String = "pending") -> [String: Any] {
        ["seq": seq, "id": "demo-priya-row", "role": "system", "kind": "proactive",
         "text": "Priya is waiting on your reply", "toolName": "main_chat_proactive",
         "at": stamp(at(hour: 8, minute: 52)), "proactive": priyaCard(state: state)]
    }

    static func transcript() throws -> [ChatItemView] {
        [ChatItemView(seq: 1, role: .user, kind: .text, text: "Did the Halcyon tests pass last night?", toolName: "",
                      at: stamp(at(hour: 22, minute: 14, daysAgo: 1)), permission: nil),
         ChatItemView(seq: 2, role: .assistant, kind: .text,
                      text: "Not yet. Two date parser tests still fail. I put them in this morning’s brief.", toolName: "",
                      at: stamp(at(hour: 22, minute: 15, daysAgo: 1)), permission: nil),
         try decode(ChatItemView.self, priyaRow(seq: 3))]
    }

    static func topics() throws -> [MobileTopic] {
        try decode(MobileTopicList.self, [
            "topics": [
                ["id": thursday, "kind": "project", "name": "Thursday review", "status": "waiting_on_you",
                 "latest": ["text": "A reply to Priya is ready to send."],
                 "peopleLine": ["names": ["Priya"], "count": 1]],
                ["id": halcyon, "kind": "project", "name": "Halcyon import", "status": "today",
                 "latest": ["text": "Two date parser tests still fail."],
                 "next": ["text": "Lena", "at": stamp(at(hour: 14, minute: 0))],
                 "peopleLine": ["names": ["Lena"], "count": 2]],
                ["id": boardDeck, "kind": "project", "name": "Board deck", "status": "moving",
                 "latest": ["text": "The import timing slide waits on Halcyon."]],
            ],
        ]).topics
    }

    static func today(duration: Double) throws -> MobileTodaySnapshot {
        let brief = brief()
        let card = try decode(ChatItemView.self, priyaRow(seq: 3)).proactive
        let lena = at(hour: 14, minute: 0)
        let entry = TodayBriefIndex(id: brief.identifier, slot: "morning", localDate: brief.localDate ?? "",
                                    scheduledAt: brief.asOf, madeAt: brief.asOf ?? stamp(now()), catchUp: false,
                                    durationSeconds: duration, lead: paragraphs[0])
        return MobileTodaySnapshot(
            generatedAt: stamp(now()), latestBrief: entry, briefs: [entry],
            needsYou: [TodayNeed(id: priyaMomentID, chatId: chatID, card: TodayNeed.Card(proactive: card), task: nil)],
            needsYouCount: 1,
            day: TodayDay(next: TodayDay.Event(title: "Halcyon with Lena", start: stamp(lena),
                                               end: stamp(lena.addingTimeInterval(45 * 60))),
                          free: [TodayDay.Range(start: stamp(at(hour: 10, minute: 0)), end: stamp(at(hour: 12, minute: 30)))],
                          calendarConnected: true),
            topics: try topics(), briefRequest: nil, schedule: MobileBriefSchedule(morning: "07:00", evening: "17:00"))
    }

    static func stamp(_ date: Date) -> String { ISO8601DateFormatter().string(from: date) }
    static func ms(_ date: Date) -> Int64 { Int64(date.timeIntervalSince1970 * 1000) }

    static func decode<T: Decodable>(_ type: T.Type, _ object: [String: Any]) throws -> T {
        try JSONDecoder().decode(type, from: JSONSerialization.data(withJSONObject: object))
    }
}

/// The demo's computer: the main chat, the topics and Priya's card. A question asked in a topic is answered a moment
/// later, and the turn event tells the open screen to read again.
actor LaunchDemoHub: ChatHubAPI, TopicHubAPI {
    /// A test or seed computer takes a send to its main chat (PROTOCOL.md 15.5a).
    func supportsMainChatSend() async -> Bool { true }
    private var priyaState = "pending"
    private var sent: [[String: Any]] = []
    private var turns: [MobileChatTurnEvent] = []
    private var receipts: [String: ChatMessageAccepted] = [:]

    // MARK: Chat

    func chats(limit: Int) async throws -> [ChatSummaryView] {
        [ChatUITestSeed.mainSummary(now: LaunchDemoUITestSeed.now())]
    }

    func chat(id: String, after: Int?, limit: Int) async throws -> ChatDetail {
        guard id == LaunchDemoUITestSeed.chatID else {
            throw HubRequestError.rejected(status: 404, message: "That chat is not on your computer.")
        }
        var rows = try LaunchDemoUITestSeed.transcript()
        rows[2] = try LaunchDemoUITestSeed.decode(ChatItemView.self, LaunchDemoUITestSeed.priyaRow(seq: 3, state: priyaState))
        rows += try sent.compactMap { $0["message"] as? [String: Any] }.map { try LaunchDemoUITestSeed.decode(ChatItemView.self, $0) }
        let page = after.map { cursor in rows.filter { $0.seq > cursor } } ?? Array(rows.suffix(limit))
        return ChatDetail(chat: ChatHeaderView(id: id, title: "Isoform", state: .idle), items: page,
                          pendingPermission: nil, cursor: rows.last?.seq ?? 0, hasMore: false)
    }

    func createChat(title: String, idempotencyKey: String) async throws -> String { throw HubRequestError.noAnswer }

    func sendMessage(chatID: String, text: String, idempotencyKey: String) async throws -> ChatMessageAccepted {
        guard chatID == LaunchDemoUITestSeed.chatID else { throw HubRequestError.noAnswer }
        return ask(text, key: idempotencyKey)
    }

    func sendMessage(chatID: String, text: String, offerReferences: [ChatOfferReference],
                     idempotencyKey: String) async throws -> ChatMessageAccepted {
        try await sendMessage(chatID: chatID, text: text, idempotencyKey: idempotencyKey)
    }

    func sendBriefMessage(chatID: String, text: String, reference: ChatBriefReference,
                          idempotencyKey: String) async throws -> ChatMessageAccepted {
        try await sendMessage(chatID: chatID, text: text, idempotencyKey: idempotencyKey)
    }

    func openBrief(chatID: String, reference: ChatBriefReference, idempotencyKey: String) async throws {}
    func openBriefParagraph(chatID: String, reference: ChatBriefReference, paragraph: Int,
                            idempotencyKey: String) async throws {}
    func openBriefParagraph(chatID: String, reference: ChatBriefReference, paragraph: Int, shownText: String,
                            idempotencyKey: String) async throws {}
    func answerPermission(chatID: String, requestID: String, optionID: String, idempotencyKey: String) async throws -> Bool {
        false
    }
    func cancel(chatID: String, idempotencyKey: String) async throws -> Bool { false }

    func pollChatTurns() async throws -> [MobileChatTurnEvent] {
        for _ in 0..<40 {
            if !turns.isEmpty {
                defer { turns.removeAll() }
                return turns
            }
            try await Task.sleep(for: .milliseconds(250))
        }
        return []
    }

    // MARK: Topics

    func supportsTopics() async throws -> Bool { true }

    func topics() async throws -> MobileTopicList { MobileTopicList(topics: try LaunchDemoUITestSeed.topics()) }

    func topic(id: String, cursor: String?, limit: Int) async throws -> MobileTopicPage {
        let empty: [String: Any] = ["sessionId": LaunchDemoUITestSeed.chatID, "topicId": id, "items": [Any](),
                                    "hasMore": false, "nextCursor": ""]
        guard id == LaunchDemoUITestSeed.thursday, cursor == nil else {
            guard [LaunchDemoUITestSeed.halcyon, LaunchDemoUITestSeed.boardDeck, LaunchDemoUITestSeed.thursday].contains(id) else {
                throw HubRequestError.rejected(status: 404, message: "topic not found")
            }
            return try LaunchDemoUITestSeed.decode(MobileTopicPage.self, empty)
        }
        let items: [[String: Any]] = [
            ["id": "event:priya-mail", "kind": "event", "at": LaunchDemoUITestSeed.ms(LaunchDemoUITestSeed.at(hour: 21, minute: 32, daysAgo: 1)),
             "actor": "user", "text": "read Priya’s email about Thursday"],
            ["id": "demo-priya-row", "kind": "message", "at": LaunchDemoUITestSeed.ms(LaunchDemoUITestSeed.at(hour: 8, minute: 52)),
             "message": LaunchDemoUITestSeed.priyaRow(seq: 3, state: priyaState)],
        ] + sent
        var page = empty
        page["items"] = items
        return try LaunchDemoUITestSeed.decode(MobileTopicPage.self, page)
    }

    func sendTopicMessage(topicID: String, text: String, idempotencyKey: String) async throws -> ChatMessageAccepted {
        guard topicID == LaunchDemoUITestSeed.thursday else {
            throw HubRequestError.rejected(status: 404, message: "topic not found")
        }
        return ask(text, key: idempotencyKey)
    }

    // MARK: Asking and Send

    private func ask(_ text: String, key: String) -> ChatMessageAccepted {
        if let accepted = receipts[key] { return accepted }
        let seq = 4 + sent.count
        let at = LaunchDemoUITestSeed.now()
        sent.append(["id": "demo-sent-\(seq)", "kind": "message", "at": LaunchDemoUITestSeed.ms(at),
                     "message": ["seq": seq, "id": "demo-sent-\(seq)", "role": "user", "kind": "text", "text": text,
                                 "toolName": "", "at": LaunchDemoUITestSeed.stamp(at)]])
        let accepted = ChatMessageAccepted(turnId: "demo-turn-\(seq)", seq: seq)
        receipts[key] = accepted
        Task { await self.answer(turnID: accepted.turnId) }
        return accepted
    }

    private func answer(turnID: String) async {
        try? await Task.sleep(for: .seconds(2.4))
        let seq = 4 + sent.count
        let at = LaunchDemoUITestSeed.now()
        sent.append(["id": "demo-answer-\(seq)", "kind": "message", "at": LaunchDemoUITestSeed.ms(at),
                     "message": ["seq": seq, "id": "demo-answer-\(seq)", "role": "assistant", "kind": "text",
                                 "text": LaunchDemoUITestSeed.answer, "toolName": "", "at": LaunchDemoUITestSeed.stamp(at)]])
        turns.append(MobileChatTurnEvent(event: "chat_turn", chatId: LaunchDemoUITestSeed.chatID, turnId: turnID,
                                         state: "done", seq: seq, at: LaunchDemoUITestSeed.stamp(at)))
    }

    /// Send goes out through Mail, and the computer posts its receipt to the chat: "Sent via Mail, 9:43 AM".
    func resolvePriya(_ actionID: String) -> String {
        priyaState = actionID == "not_now" ? "deferred" : "acted"
        guard actionID == "send" else { return priyaState }
        let seq = 4 + sent.count
        let at = LaunchDemoUITestSeed.now()
        let time = at.formatted(.dateTime.hour().minute().locale(Locale(identifier: "en_US")))
        sent.append(["id": "demo-sent-mail", "kind": "message", "at": LaunchDemoUITestSeed.ms(at),
                     "message": ["seq": seq, "id": "demo-sent-mail", "role": "system", "kind": "text",
                                 "text": "Sent via Mail, \(time)", "toolName": "main_chat_report",
                                 "at": LaunchDemoUITestSeed.stamp(at),
                                 "report": ["source": "message_send", "channel": "mail",
                                            "sentAt": LaunchDemoUITestSeed.ms(at), "principalId": "priya"]]])
        turns.append(MobileChatTurnEvent(event: "chat_turn", chatId: LaunchDemoUITestSeed.chatID, turnId: "demo-sent-mail",
                                         state: "done", seq: seq, at: LaunchDemoUITestSeed.stamp(at)))
        return priyaState
    }
}

/// Send and Not now on Priya's card, answered the way the computer answers them.
struct LaunchDemoProactiveHub: ProactiveActionHubAPI {
    let hub: LaunchDemoHub

    func performProactiveAction(chatID: String, momentID: String, actionID: String,
                                idempotencyKey: String) async throws -> String {
        guard chatID == LaunchDemoUITestSeed.chatID, momentID == LaunchDemoUITestSeed.priyaMomentID,
              ["send", "not_now"].contains(actionID), !idempotencyKey.isEmpty else {
            throw HubRequestError.rejected(status: 400, message: "This action is no longer available.")
        }
        try await Task.sleep(for: .milliseconds(700))
        return await hub.resolvePriya(actionID)
    }

    func recordProactiveOpened(chatID: String, momentID: String, idempotencyKey: String) async throws {}

    func muteProactiveKind(chatID: String, momentID: String, idempotencyKey: String) async throws -> String { "pending" }
}
#endif
