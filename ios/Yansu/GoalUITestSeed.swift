#if DEBUG && targetEnvironment(simulator)
import Foundation

/// The cached goals of the seeded "goals" UI test state: six tracking goals (one slipping, one waiting) and two done,
/// written where GoalStore reads its cache.
enum GoalUITestSeed {
    private struct Saved: Encodable {
        var goals: [GoalView]
        var counts: GoalCounts
        var readAt: Date?
    }

    static func write(to url: URL, now: Date) throws {
        let stamp = ISO8601DateFormatter().string(from: now)
        func goal(_ id: String, _ kind: GoalKind, _ title: String, _ status: String, group: String = "tracking",
                  state: String = "active", slipping: Bool = false, byYou: Bool = true) -> GoalView {
            GoalView(id: id, kind: kind.rawValue, title: title, state: state, group: group, status: status,
                     slipping: slipping, updatedAt: stamp, eventId: "evt-\(id)", addedByYou: byYou)
        }
        let goals = [
            goal("g1", .commitment, "Send the contract draft to Ana",
                 "Due today, and nothing has moved on it yet. You added it 3 days ago.", slipping: true),
            goal("g2", .commitment, "Renew the passport before Nov 3",
                 "Due on Nov 3. You added it yesterday. The aim: Travel to Lisbon in December with a valid passport."),
            goal("g3", .commitment, "Get the lease signed", "Waiting on Ana since Tuesday: her signature on page 4.",
                 state: "waiting"),
            goal("g4", .goal, "Run three times a week", "You added it 2 days ago. The aim: Finish the city half in under two hours."),
            goal("g5", .responsibility, "Keep the release notes current", "Isoform noticed it yesterday.", byYou: false),
            goal("g6", .opportunity, "Write up the onboarding interviews", "Last updated on Oct 9.", byYou: false),
            goal("g7", .goal, "Book the dentist", "Done on Oct 14.", group: "done", state: "resolved"),
            goal("g8", .commitment, "Send Priya the Q3 numbers", "Done yesterday.", group: "done", state: "resolved"),
        ]
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let data = try encoder.encode(Saved(goals: goals, counts: GoalCounts(tracking: 6, done: 2, onMacOnly: 1),
                                            readAt: now))
        try data.write(to: url)
    }
}

/// Ideas and their main-chat hand-off use isolated seeded state, with no relay or production data.
enum IdeasUITestSeed {
    static let offers = [
        IdeaOffer(id: "suggestion.ui-notes", kind: "suggestion", title: "I can tidy the project notes",
                  why: "You worked on these notes three times this week.",
                  prompt: "Help me tidy the project notes. Show me a plan before making changes.",
                  version: String(repeating: "a", count: 64),
                  whyLocalized: ["en": "You worked on these notes three times this week.", "zh-CN": "这周你已三次处理这些笔记。"],
                  evidence: IdeaEvidence(text: "You opened the project notes three times this week and left them unsorted.",
                                         textLocalized: ["en": "You opened the project notes three times this week and left them unsorted.",
                                                         "zh-CN": "这周你三次打开项目笔记，都没有整理。"],
                                         source: "claude_code", at: "2026-10-27T22:40:00Z"),
                  deckKind: "claude_code"),
        IdeaOffer(id: "find.ui-research", kind: "topic_find", title: "Explore local AI tools",
                  why: "You have been reading about local AI tools.",
                  prompt: "Help me explore this idea: local AI tools.", version: String(repeating: "b", count: 64),
                  whyLocalized: ["en": "You have been reading about local AI tools.", "zh-CN": "你最近在阅读有关本地 AI 工具的内容。"])
    ]

    private struct Saved: Encodable {
        let hubID = "settings-ui-hub"
        let ideas: [IdeaOffer]
        let readAt: Date
        let pending: Set<String> = []
    }

    static func write(to url: URL, empty: Bool, now: Date) throws {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        try encoder.encode(Saved(ideas: empty ? [] : offers, readAt: now)).write(to: url)
    }
}

actor IdeasUITestHub: IdeasHubAPI {
    private var items: [IdeaOffer]
    private var receipts: Set<String> = []

    init(empty: Bool, motionCapture: Bool = false) {
        items = empty ? [] : IdeasUITestSeed.offers
        if motionCapture {
            items.append(IdeaOffer(id: "suggestion.ui-followup", kind: "suggestion", title: "Organize the interview notes",
                                   why: "Your interview notes are ready to organize.",
                                   prompt: "Help me organize the interview notes.", version: String(repeating: "c", count: 64)))
        }
    }

    func offers() async throws -> IdeasPage { IdeasPage(offers: items) }

    func findMoreOffers() async throws -> IdeasPage {
        try await Task.sleep(for: .seconds(3))
        return IdeasPage(offers: items, nextAllowedAt: ISO8601DateFormatter().string(from: Date().addingTimeInterval(3600)))
    }

    func declineOffer(id: String, _ request: IdeaDeclineRequest) async throws -> IdeaDeclinePayload {
        guard ["dismiss", "not_now"].contains(request.decision) else { throw HubRequestError.malformedResponse }
        if receipts.insert(request.idempotencyKey).inserted {
            items.removeAll { $0.id == id }
        }
        return IdeaDeclinePayload(declined: true)
    }
}
#endif
