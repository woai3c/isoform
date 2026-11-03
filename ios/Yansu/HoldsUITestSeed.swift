#if DEBUG && targetEnvironment(simulator)
import Foundation

actor HoldsUITestHub: IdeasHubAPI, HoldsHubAPI, TasksHubAPI {
    private let created = Date()
    private var placedAt: Date?
    private var removed = false
    private var done = false
    private var kept = false
    private var holdAttempts = 0
    /// YANSU_UITEST_HOLDS: "away" (the computer misses the first Hold), "away-passed" (and the time passes
    /// before it is back), "fail" (Hold fails twice), "filled" (the slot fills up) or "readonly" (Work can't
    /// be changed).
    private let mode = ProcessInfo.processInfo.environment["YANSU_UITEST_HOLDS"] ?? ""
    private let slot: PhoneHoldSlot
    private var proposal: PhoneHoldProposal
    private var card: PhoneHoldOffer

    init() {
        let format = ISO8601DateFormatter()
        let start = Date(timeIntervalSince1970: ceil(Date().addingTimeInterval(7200).timeIntervalSince1970 / 1800) * 1800)
        let slot = PhoneHoldSlot(start: format.string(from: start), end: format.string(from: start.addingTimeInterval(7200)))
        let later = (1...3).map { day in
            PhoneHoldSlot(start: format.string(from: start.addingTimeInterval(Double(day) * 86400)),
                          end: format.string(from: start.addingTimeInterval(Double(day) * 86400 + 7200)))
        }
        let title = Locale.current.language.languageCode?.identifier == "zh" ? "Q4 计划" : "Q4 plan"
        let proposal = PhoneHoldProposal(id: "hold-ui", thing: PhoneHoldThing(title: title, sourceKind: "up_next", sourceId: "suggestion.hold-ui"), calendarId: "work", slots: [slot] + later)
        self.slot = slot
        self.proposal = proposal
        self.card = PhoneHoldOffer(proposal: proposal, calendars: [PhoneHoldCalendar(id: "work", title: "Work", meetingsAccount: true), PhoneHoldCalendar(id: "home", title: "Home")], why: Locale.preferredLanguages.first?.hasPrefix("zh") == true ? "Q4 计划周一要交。" : "The Q4 plan is due Monday.", token: "ui-token")
    }

    func supportsHolds() async throws -> Bool { true }
    func offers(includeHolds: Bool) async throws -> IdeasPage {
        guard includeHolds, placedAt == nil || removed else { return IdeasPage(offers: []) }
        return IdeasPage(offers: [IdeaOffer(id: "suggestion.hold-ui", kind: "suggestion", title: "Hold today 2 to 4 PM for the Q4 plan", why: "The Q4 plan is due Monday.", prompt: "Q4 plan", version: String(repeating: "a", count: 64), evidence: IdeaEvidence(text: "The Q4 plan is due Monday.", textLocalized: ["en": "The Q4 plan is due Monday.", "zh-CN": "Q4 计划周一要交。"], source: "calendar", at: stamp(created)), deckKind: "your_day", hold: card)])
    }
    func offers() async throws -> IdeasPage { try await offers(includeHolds: true) }
    func declineOffer(id: String, _ request: IdeaDeclineRequest) async throws -> IdeaDeclinePayload { IdeaDeclinePayload(declined: true) }
    func holdProposal(kind: String, id: String) async throws -> PhoneHoldOffer {
        // A fresh card after a hold that never went: the same thing, a new token.
        if mode == "away-passed" { card = PhoneHoldOffer(proposal: card.proposal, calendars: card.calendars, why: card.why, token: "ui-token-fresh") }
        return card
    }
    func holdStatus(id: String) async throws -> PhoneHoldStatus { status() }
    func holdAction(_ action: PhoneHoldAction) async throws -> PhoneHoldStatus {
        switch action.action {
        case "hold":
            holdAttempts += 1
            if mode.hasPrefix("away") && action.token == "ui-token" {
                if holdAttempts == 1 { throw HubRequestError.noAnswer }
                if mode == "away-passed" { throw HubRequestError.conflict(message: "HOLD_TIME_PASSED") }
            }
            if mode == "fail" && holdAttempts <= 2 { throw HubRequestError.conflict(message: "HOLD_FAILED") }
            if mode == "filled" && holdAttempts == 1, let next = card.proposal.slots.dropFirst().first {
                return PhoneHoldStatus(id: proposal.id, state: "conflict", start: next.start, end: next.end,
                                       conflictTitle: "Design review", conflictStart: slot.start)
            }
            if mode == "readonly" && holdAttempts == 1 {
                return PhoneHoldStatus(id: proposal.id, state: "calendar_unavailable", calendarId: "home")
            }
            placedAt = Date(); removed = false
        case "select_calendar": return PhoneHoldStatus(id: proposal.id, state: "selected", calendarId: action.calendarId)
        case "undo_hold":
            removed = true
            let previous = status()
            proposal = PhoneHoldProposal(id: "hold-ui-next", thing: proposal.thing, calendarId: "work", slots: [slot])
            card = PhoneHoldOffer(proposal: proposal, calendars: card.calendars, why: card.why, token: "ui-token-next")
            placedAt = nil
            return previous
        case "remove_hold": removed = true
        case "undo_remove_hold": removed = false
        default: throw HubRequestError.invalidIdentifier
        }
        return status()
    }
    private func status() -> PhoneHoldStatus {
        let state = removed ? "removed" : (placedAt.map { Date().timeIntervalSince($0) >= 5 } == true ? "held" : "pending_write")
        return PhoneHoldStatus(id: proposal.id, state: state, taskId: "hold." + proposal.id, calendarId: "work", title: proposal.thing.title, start: slot.start, end: slot.end, writeAfter: placedAt.map { stamp($0.addingTimeInterval(5)) })
    }
    func tasks() async throws -> MobileTasksPage {
        guard placedAt != nil, !removed || done else { return MobileTasksPage(tasks: [], counts: MobileTaskCounts(needsYou: 0, running: 0, doneThisWeek: 0), threads: true) }
        let pending = status().state == "pending_write"
        let outcome = pending ? nil : MobileTaskOutcome(kind: "hold_placed", by: "isoform", at: stamp(created), params: ["start": slot.start, "end": slot.end])
        let task = MobileTask(id: "hold." + proposal.id, title: proposal.thing.title, status: pending ? "in_progress" : "completed", group: pending ? "running" : "done", blockedReason: nil, runner: nil, progress: nil, createdAt: stamp(created), updatedAt: stamp(Date()), outcome: outcome)
        return MobileTasksPage(tasks: [task], counts: MobileTaskCounts(needsYou: 0, running: pending ? 1 : 0, doneThisWeek: pending ? 0 : 1), threads: true)
    }
    func task(id: String) async throws -> MobileTaskDetail {
        guard let task = try await tasks().tasks.first(where: { $0.id == id }) else { throw HubRequestError.invalidIdentifier }
        return MobileTaskDetail(id: task.id, title: task.title, status: task.status, group: task.group, blockedReason: nil, runner: nil, progress: nil, createdAt: task.createdAt, updatedAt: task.updatedAt, startedAt: nil, steps: [], outcome: task.outcome, origin: MobileTaskOrigin(kind: "up_next", at: stamp(created)), actions: done ? [] : ["mark_done"], hold: status(), holdCleanup: done && !kept && !removed ? true : nil)
    }
    func stopTask(id: String, request: MobileTaskStopRequest) async throws -> MobileTask { throw HubRequestError.invalidIdentifier }
    func replyToTask(id: String, request: MobileTaskReplyRequest) async throws -> MobileTaskDetail { try await task(id: id) }
    func actOnTask(id: String, request: MobileTaskActionRequest) async throws -> MobileTaskDetail {
        switch request.action {
        case "mark_done": done = true
        case "keep_hold": kept = true
        default: break
        }
        return try await task(id: id)
    }
    private func stamp(_ date: Date) -> String { ISO8601DateFormatter().string(from: date) }
}
#endif
