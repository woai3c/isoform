import Foundation

/// The five kinds a goal can be, from the Purpose Map (PROTOCOL.md section 20.5).
enum GoalKind: String, CaseIterable, Codable, Sendable {
    case commitment
    case responsibility
    case goal
    case concern
    case opportunity
}

/// The areas Create a goal offers. Each opens the chat with a sentence to finish; the Mac's chat refines the
/// goal, picks its kind and keeps the category as a label.
enum GoalCategory: String, CaseIterable, Identifiable, Sendable {
    case health
    case relationships
    case finance
    case career
    case interests
    case productivity
    case other

    var id: String { rawValue }

    var title: String {
        switch self {
        case .health: return String(localized: "Health")
        case .relationships: return String(localized: "Relationships")
        case .finance: return String(localized: "Finance")
        case .career: return String(localized: "Career")
        case .interests: return String(localized: "Interests")
        case .productivity: return String(localized: "Productivity")
        case .other: return String(localized: "Something else")
        }
    }

    /// An outline symbol, so the list reads as a quiet menu.
    var systemImage: String {
        switch self {
        case .health: return "heart"
        case .relationships: return "person.2"
        case .finance: return "dollarsign.circle"
        case .career: return "briefcase"
        case .interests: return "paintpalette"
        case .productivity: return "checklist"
        case .other: return "ellipsis.circle"
        }
    }

    /// What the chat's composer starts with, a whole sentence to send or add to.
    var chatPrefill: String {
        switch self {
        case .other: return String(localized: "I want to create a goal.")
        default: return String(localized: "I want to create a goal about \(title.lowercased())")
        }
    }
}

/// One goal as the Mac sends it. Decoding is tolerant, like the brief's: a newer Mac can add fields.
struct GoalView: Codable, Equatable, Identifiable, Sendable {
    var id: String
    var kind: String? = nil
    var title: String? = nil
    var state: String? = nil
    var group: String? = nil
    var status: String? = nil
    var slipping: Bool = false
    var dueAt: String? = nil
    var updatedAt: String? = nil
    var eventId: String? = nil
    var addedByYou: Bool = false

    var isDone: Bool { group == "done" }
    /// Just forgotten: hidden from every list, kept only so Undo can bring it back.
    var isForgotten: Bool { group == "forgotten" || state == "dismissed" }
}

extension GoalView {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        kind = try container.decodeIfPresent(String.self, forKey: .kind)
        title = try container.decodeIfPresent(String.self, forKey: .title)
        state = try container.decodeIfPresent(String.self, forKey: .state)
        group = try container.decodeIfPresent(String.self, forKey: .group)
        status = try container.decodeIfPresent(String.self, forKey: .status)
        slipping = try container.decodeIfPresent(Bool.self, forKey: .slipping) ?? false
        dueAt = try container.decodeIfPresent(String.self, forKey: .dueAt)
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt)
        eventId = try container.decodeIfPresent(String.self, forKey: .eventId)
        addedByYou = try container.decodeIfPresent(Bool.self, forKey: .addedByYou) ?? false
    }
}

struct GoalCounts: Codable, Equatable, Sendable {
    var tracking: Int = 0
    var done: Int = 0
    var onMacOnly: Int = 0
}

extension GoalCounts {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        tracking = try container.decodeIfPresent(Int.self, forKey: .tracking) ?? 0
        done = try container.decodeIfPresent(Int.self, forKey: .done) ?? 0
        onMacOnly = try container.decodeIfPresent(Int.self, forKey: .onMacOnly) ?? 0
    }
}

struct GoalPage: Decodable, Equatable, Sendable {
    var goals: [GoalView] = []
    var counts = GoalCounts()
    var cursor: String? = nil
    var hasMore = false
}

extension GoalPage {
    private enum CodingKeys: String, CodingKey { case goals, counts, cursor, hasMore }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        // One goal the phone cannot read never hides the rest.
        goals = ((try? container.decodeIfPresent([Lossy<GoalView>].self, forKey: .goals)) ?? nil)?
            .compactMap(\.value) ?? []
        counts = try container.decodeIfPresent(GoalCounts.self, forKey: .counts) ?? GoalCounts()
        cursor = try container.decodeIfPresent(String.self, forKey: .cursor)
        hasMore = try container.decodeIfPresent(Bool.self, forKey: .hasMore) ?? false
    }
}

/// Decodes one element of an array, or nothing.
private struct Lossy<Value: Decodable>: Decodable {
    let value: Value?

    init(from decoder: Decoder) throws {
        value = try? Value(from: decoder)
    }
}

/// A move on a goal: check it off, reopen it, or park it on someone.
enum GoalAction: String, Codable, Sendable {
    case resolve
    case reopen
    case waiting
    /// Hides the goal everywhere; the Mac keeps it so Undo can bring it back.
    case forget
    /// Undo for a done or forgotten goal.
    case restore
}

/// One goal's story (GET /goals/{id}): the goal, the aim when it says more than the title, and what happened to it,
/// newest first.
struct GoalDetail: Decodable, Equatable, Sendable {
    struct Entry: Decodable, Equatable, Sendable {
        var what: String
        var at: String
    }

    var goal: GoalView
    var outcome: String? = nil
    var history: [Entry] = []
}

extension GoalDetail {
    private enum CodingKeys: String, CodingKey { case goal, outcome, history }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        goal = try container.decode(GoalView.self, forKey: .goal)
        outcome = try container.decodeIfPresent(String.self, forKey: .outcome)
        history = ((try? container.decodeIfPresent([Lossy<Entry>].self, forKey: .history)) ?? nil)?
            .compactMap(\.value) ?? []
    }
}

/// Every move names the event the user saw, so a goal that moved on the Mac since answers 409 instead of being
/// changed blind.
struct GoalEventRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let action: String
    let expectedEventId: String
    let waitingOn: String?
}

struct GoalPayload: Decodable, Equatable, Sendable {
    let goal: GoalView
}

/// The Goals reads and moves, as the phone sees them; new goals start in the chat. HubChannel conforms; tests substitute a stub.
protocol GoalsHubAPI: Sendable {
    func goalsPage(limit: Int, cursor: String?) async throws -> GoalPage
    func goalDetail(goalID: String) async throws -> GoalDetail
    func goalEvent(goalID: String, _ request: GoalEventRequest) async throws -> GoalView
}

extension HubChannel: GoalsHubAPI {
    func goalsPage(limit: Int, cursor: String?) async throws -> GoalPage {
        try await perform(.goals(limit: limit, cursor: cursor)).payload(GoalPage.self)
    }

    func goalDetail(goalID: String) async throws -> GoalDetail {
        try await perform(.goal(id: goalID)).payload(GoalDetail.self)
    }

    func goalEvent(goalID: String, _ request: GoalEventRequest) async throws -> GoalView {
        guard Identifiers.isSafePathID(goalID) else { throw HubRequestError.invalidIdentifier }
        return try await post("/goals/\(goalID)/events", request, as: GoalPayload.self).goal
    }
}
