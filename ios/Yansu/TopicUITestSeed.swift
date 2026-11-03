#if DEBUG && targetEnvironment(simulator)
import Foundation

/// The seeded "feed-topics" states (RC 1): today's brief with topics on its paragraphs, prepared questions and a
/// closing question with two answers, and an in-process computer that answers the topic routes. Everything is
/// decoded from JSON shaped as PROTOCOL.md 15.8 writes it, so the phone's own decoders read it.
enum TopicUITestSeed {
    static let halcyon = "project-halcyon"
    static let northwind = "project-northwind"
    static let following = "following-multi-agent"

    static func brief(now: Date) -> MobileBriefV1 {
        var brief = MobileBriefV1(version: 1, briefId: "ui-topics-brief", slot: "morning",
                                  localDate: FeedAssembly.localDate(for: now), timezone: TimeZone.current.identifier,
                                  asOf: stamp(now.addingTimeInterval(-3600)), narrativeStatus: "ok",
                                  narrative: [
                                      BriefParagraph(text: "Countersign the Northwind SOW before noon and Monday's kickoff is set.",
                                                     refs: ["e1"], topicId: northwind, topicName: "Northwind kickoff"),
                                      BriefParagraph(text: "Halcyon wants the sync moved to Oct 30. A reply that holds Nov 1 is waiting for you.",
                                                     refs: ["e2"], topicId: halcyon, topicName: "Halcyon ERP sync"),
                                      BriefParagraph(text: "One question before you start: should I hold Thursday afternoon for the Meridian memo?",
                                                     refs: ["e1"]),
                                  ],
                                  conversationKey: "ui-topics-version")
        brief.evidence = [
            BriefEvidence(ref: "e1", sourceKind: "calendar", sourceId: "ev-northwind", label: "Northwind kickoff",
                          observedAt: stamp(now.addingTimeInterval(-7200))),
            BriefEvidence(ref: "e2", sourceKind: "principal", sourceId: "p-halcyon", label: "Halcyon",
                          observedAt: stamp(now.addingTimeInterval(-5400))),
        ]
        var meeting = BriefItem(id: "meetings-1", title: "Pipeline review at 10:30", detail: "Talking points ready for 3 deals.",
                                at: stamp(now.addingTimeInterval(-1800)), refs: ["e1"])
        meeting.key = FeedAssembly.itemKey(section: "meetings", sourceKind: "calendar", sourceID: "ev-pipeline")
        brief.sections.meetings = [meeting]
        brief.cards = [
            MobileBriefCard(id: "q-sow", kind: "question", mode: "send", label: "Review the SOW",
                            message: "What changed in the SOW?",
                            refs: ["e1"], paragraph: 0),
            MobileBriefCard(id: "q-kickoff", kind: "question", mode: "send", label: "Check the kickoff",
                            message: "Who is on the kickoff?",
                            refs: ["e1"], paragraph: 0),
            MobileBriefCard(id: "q-oct30", kind: "question", mode: "send", label: "Review the date",
                            message: "Could we hit Oct 30?",
                            refs: ["e2"], paragraph: 1),
            MobileBriefCard(id: "answer-hold", kind: "action", mode: "send", label: "Hold Thursday afternoon",
                            message: "Hold Thursday 1:00 to 4:00 PM for the Meridian memo.", refs: ["e1"]),
            MobileBriefCard(id: "answer-skip", kind: "action", mode: "send", label: "Not this week",
                            message: "Not this week for the Meridian memo.", refs: ["e1"]),
            MobileBriefCard(id: "opener", kind: "opener", mode: "speak_first",
                            message: "Your morning starts with the Northwind SOW. What would you like to begin with?"),
        ]
        return brief
    }

    static func topics(now: Date) throws -> [MobileTopic] {
        let standup = Calendar.current.date(bySettingHour: 14, minute: 0, second: 0, of: now) ?? now
        return try decode(MobileTopicList.self, [
            "topics": [
                ["id": halcyon, "kind": "project", "name": "Halcyon ERP sync", "status": "waiting_on_you",
                 "latest": ["text": "Reply that holds Nov 1 is drafted."],
                 "next": ["text": "Standup", "at": stamp(standup)],
                 "peopleLine": ["names": ["Ops lead", "Priya"], "count": 4]],
                ["id": northwind, "kind": "project", "name": "Northwind kickoff", "status": "waiting_on_you",
                 "latest": ["text": "Countersign the SOW by noon."]],
                ["id": following, "kind": "following", "name": "Multi-agent coding", "status": "new_stories",
                 "newStories": 2, "why": ["lastSeenAt": stamp(now), "interestWhy": "Two stories picked from your topics."]],
            ],
        ]).topics
    }

    static func page(topicID: String, cursor: String?, extra: [[String: Any]], now: Date) throws -> MobileTopicPage {
        let ms = { (offset: TimeInterval) in Int64((now.timeIntervalSince1970 + offset) * 1000) }
        guard topicID == halcyon else {
            let items: [[String: Any]] = topicID == northwind
                ? [["id": "event:northwind", "kind": "event", "at": ms(-1800), "actor": "user",
                    "text": "Reviewed the Northwind SOW"]] : []
            return try decode(MobileTopicPage.self, ["sessionId": "ui-main-chat", "topicId": topicID, "items": items,
                                                     "hasMore": false, "nextCursor": ""])
        }
        if cursor == "older" {
            return try decode(MobileTopicPage.self, [
                "sessionId": "ui-main-chat", "topicId": topicID, "hasMore": false, "nextCursor": "",
                "items": [["id": "event:opened", "kind": "event", "at": ms(-172_800), "actor": "user",
                           "text": "opened the Halcyon thread"]],
            ])
        }
        let items: [[String: Any]] = [
            ["id": "event:staging", "kind": "event", "at": ms(-86_400), "actor": "user",
             "text": "pushed ERP connector v0.3 to staging"],
            ["id": "row-reply-card", "kind": "message", "at": ms(-7200),
             "message": ["seq": 41, "id": "row-reply-card", "role": "system", "kind": "proactive",
                         "text": "Halcyon is waiting on your reply", "toolName": "main_chat_proactive", "at": stamp(now),
                         "proactive": ["momentId": "ui-halcyon-reply", "kind": "waiting_on_reply",
                                       "headline": "Halcyon is waiting on your reply",
                                       "body": "Oct 30 would skip the staging preview. The drafted reply keeps Nov 1 and offers a preview on Oct 28.",
                                       "draft": true, "actionIds": ["open", "send", "not_now", "dismiss"],
                                       "actionLabels": ["open": "Open Mail", "send": "Send", "not_now": "Not now", "dismiss": "Dismiss"],
                                       "draftContent": ["to": "ops@example.com", "subject": "Halcyon ERP sync",
                                                        "text": "Nov 1 still works for us. We can offer a staging preview on Oct 28.",
                                                        "channel": "mail", "recipient": "Ops lead", "principalId": "person-ops",
                                                        "revision": "rev-1"],
                                       "localized": ["zh-CN": ["headline": "Halcyon 在等你回复",
                                                              "body": "改到 10 月 30 日会跳过预发布预览。草拟的回复保留 11 月 1 日，并提出 10 月 28 日预览。",
                                                              "actionLabels": ["open": "打开邮件", "send": "发送", "not_now": "暂时不用",
                                                                               "dismiss": "忽略"]]],
                                       "target": ["kind": "mail"], "evidence": [], "state": "pending"]]],
            ["id": "row-question", "kind": "message", "at": ms(-3600),
             "message": ["seq": 42, "id": "row-question", "role": "user", "kind": "text",
                         "text": "Could we hit Oct 30 if we push?", "toolName": "", "at": stamp(now)]],
            ["id": "row-answer", "kind": "message", "at": ms(-3500),
             "message": ["seq": 43, "id": "row-answer", "role": "assistant", "kind": "text",
                         "text": "Only by skipping the 2 open review comments on v0.3. Both touch invoice mapping, so they are worth keeping.",
                         "toolName": "", "at": stamp(now)]],
        ] + extra
        return try decode(MobileTopicPage.self, ["sessionId": "ui-main-chat", "topicId": topicID, "items": items,
                                                 "hasMore": true, "nextCursor": "older"])
    }

    static func stamp(_ date: Date) -> String {
        ISO8601DateFormatter().string(from: date)
    }

    private static func decode<T: Decodable>(_ type: T.Type, _ object: [String: Any]) throws -> T {
        try JSONDecoder().decode(type, from: JSONSerialization.data(withJSONObject: object))
    }
}

/// Card actions on the seeded topic, through the chat's action route.
struct TopicUITestProactiveHub: ProactiveActionHubAPI {
    func performProactiveAction(chatID: String, momentID: String, actionID: String,
                                idempotencyKey: String) async throws -> String {
        guard chatID == "ui-main-chat", momentID == "ui-halcyon-reply",
              ["not_now", "send"].contains(actionID), !idempotencyKey.isEmpty else {
            throw HubRequestError.rejected(status: 400, message: "This action is no longer available.")
        }
        return actionID == "not_now" ? "deferred" : "acted"
    }
}

/// The computer's topic routes for the seeded states. Sends are answered once per key, and the stored message and a
/// reply join the Halcyon topic, so the screen shows them on its next read.
actor TopicUITestHub: TopicHubAPI {
    private let now: Date
    private var sent: [[String: Any]] = []
    private var receipts: [String: ChatMessageAccepted] = [:]

    init(now: Date) {
        self.now = now
    }

    func supportsTopics() async throws -> Bool { true }

    func topics() async throws -> MobileTopicList {
        try await Task.sleep(nanoseconds: 200_000_000)
        return MobileTopicList(topics: try TopicUITestSeed.topics(now: now))
    }

    func topic(id: String, cursor: String?, limit: Int) async throws -> MobileTopicPage {
        try await Task.sleep(nanoseconds: 300_000_000)
        guard [TopicUITestSeed.halcyon, TopicUITestSeed.northwind, TopicUITestSeed.following].contains(id) else {
            throw HubRequestError.rejected(status: 404, message: "topic not found")
        }
        return try TopicUITestSeed.page(topicID: id, cursor: cursor, extra: sent, now: now)
    }

    func sendTopicMessage(topicID: String, text: String, idempotencyKey: String) async throws -> ChatMessageAccepted {
        if let accepted = receipts[idempotencyKey] { return accepted }
        guard topicID == TopicUITestSeed.halcyon else { throw HubRequestError.rejected(status: 404, message: "topic not found") }
        let seq = 44 + sent.count
        let at = Int64(Date().timeIntervalSince1970 * 1000)
        sent.append(["id": "row-sent-\(seq)", "kind": "message", "at": at,
                     "message": ["seq": seq, "id": "row-sent-\(seq)", "role": "user", "kind": "text", "text": text,
                                 "toolName": "", "at": TopicUITestSeed.stamp(Date())]])
        sent.append(["id": "row-reply-\(seq)", "kind": "message", "at": at + 1,
                     "message": ["seq": seq + 1, "id": "row-reply-\(seq)", "role": "assistant", "kind": "text",
                                 "text": "On it. I will keep Nov 1 in the reply.", "toolName": "",
                                 "at": TopicUITestSeed.stamp(Date())]])
        let accepted = ChatMessageAccepted(turnId: "ui-topic-turn-\(seq)", seq: seq)
        receipts[idempotencyKey] = accepted
        return accepted
    }
}
#endif
