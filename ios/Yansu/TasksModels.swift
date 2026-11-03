import Foundation
import Combine
import SwiftUI
import UIKit

struct MobileTaskProgress: Codable, Equatable, Hashable, Sendable {
    let done: Int
    let total: Int
}

struct MobileTask: Codable, Equatable, Hashable, Identifiable, Sendable {
    let id: String
    let title: String
    let status: String
    let group: String
    let blockedReason: String?
    let runner: String?
    let progress: MobileTaskProgress?
    let createdAt: String
    let updatedAt: String
    /// What came of a Done task (PROTOCOL.md 20.7b). Older computers omit it.
    var outcome: MobileTaskOutcome? = nil
    /// A Needs you task changed since its thread was last opened.
    var unread: Bool? = nil
    var openedAt: String? = nil
    var source: String? = nil

    var updatedDate: Date? { RelayTime.parse(updatedAt) }
}

struct MobileTaskStep: Codable, Equatable, Sendable {
    let text: String
    let state: String
}

struct MobileTaskDetail: Decodable, Equatable, Identifiable, Sendable {
    let id: String
    let title: String
    let status: String
    let group: String
    let blockedReason: String?
    let runner: String?
    let progress: MobileTaskProgress?
    let createdAt: String
    let updatedAt: String
    let startedAt: Date?
    let steps: [MobileTaskStep]
    var outcome: MobileTaskOutcome? = nil
    var unread: Bool? = nil
    var openedAt: String? = nil
    var source: String? = nil
    /// Where the task came from; nil when the computer did not record it.
    var origin: MobileTaskOrigin? = nil
    /// The thread, oldest first. Empty on older computers.
    var events: [MobileTaskEvent] = []
    /// The ··· items that apply now; nil when the computer predates task threads.
    var actions: [String]? = nil
    /// A prepared reply this task sends (Phone Threads §4).
    var draft: MobileTaskDraft? = nil
    var hold: PhoneHoldStatus? = nil
    /// The one-time ask after the work was marked done before its hold starts.
    var holdCleanup: Bool? = nil

    private enum CodingKeys: String, CodingKey {
        case id, title, status, group, blockedReason, runner, progress
        case createdAt, updatedAt, startedAt, steps
        case outcome, unread, openedAt, source, origin, events, actions, draft, hold, holdCleanup
    }

    init(id: String, title: String, status: String, group: String, blockedReason: String?,
         runner: String?, progress: MobileTaskProgress?, createdAt: String, updatedAt: String,
         startedAt: Date?, steps: [MobileTaskStep], outcome: MobileTaskOutcome? = nil,
         unread: Bool? = nil, origin: MobileTaskOrigin? = nil, events: [MobileTaskEvent] = [],
         actions: [String]? = nil, draft: MobileTaskDraft? = nil, hold: PhoneHoldStatus? = nil, holdCleanup: Bool? = nil) {
        self.holdCleanup = holdCleanup
        self.id = id
        self.title = title
        self.status = status
        self.group = group
        self.blockedReason = blockedReason
        self.runner = runner
        self.progress = progress
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.startedAt = startedAt
        self.steps = steps
        self.outcome = outcome
        self.unread = unread
        self.origin = origin
        self.events = events
        self.actions = actions
        self.draft = draft
        self.hold = hold
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        title = try container.decode(String.self, forKey: .title)
        status = try container.decode(String.self, forKey: .status)
        group = try container.decode(String.self, forKey: .group)
        blockedReason = try container.decodeIfPresent(String.self, forKey: .blockedReason)
        runner = try container.decodeIfPresent(String.self, forKey: .runner)
        progress = try container.decodeIfPresent(MobileTaskProgress.self, forKey: .progress)
        createdAt = try container.decode(String.self, forKey: .createdAt)
        updatedAt = try container.decode(String.self, forKey: .updatedAt)
        let rawStartedAt = try container.decodeIfPresent(String.self, forKey: .startedAt)
        startedAt = rawStartedAt.flatMap(RelayTime.parse)
        steps = try container.decode([MobileTaskStep].self, forKey: .steps)
        outcome = try container.decodeIfPresent(MobileTaskOutcome.self, forKey: .outcome)
        unread = try container.decodeIfPresent(Bool.self, forKey: .unread)
        openedAt = try container.decodeIfPresent(String.self, forKey: .openedAt)
        source = try container.decodeIfPresent(String.self, forKey: .source)
        origin = try container.decodeIfPresent(MobileTaskOrigin.self, forKey: .origin)
        hold = try container.decodeIfPresent(PhoneHoldStatus.self, forKey: .hold)
        holdCleanup = try container.decodeIfPresent(Bool.self, forKey: .holdCleanup)
        events = try container.decodeIfPresent([MobileTaskEvent].self, forKey: .events) ?? []
        actions = try container.decodeIfPresent([String].self, forKey: .actions)
        draft = try? container.decodeIfPresent(MobileTaskDraft.self, forKey: .draft)
    }

    var task: MobileTask {
        MobileTask(id: id, title: title, status: status, group: group,
                   blockedReason: blockedReason, runner: runner, progress: progress,
                   createdAt: createdAt, updatedAt: updatedAt, outcome: outcome, unread: unread, openedAt: openedAt, source: source)
    }

    /// Whether the computer serves this task's thread writes (PROTOCOL.md 20.7b).
    var hasThread: Bool { actions != nil }
}

enum TaskDetailFailure: Error, Equatable {
    case unavailable
    case forbidden
    case offline

    var message: String {
        switch self {
        case .unavailable: return String(localized: "This task is no longer on your computer")
        case .forbidden: return String(localized: "This phone can't see this task")
        case .offline: return String(localized: "Couldn't reach your computer")
        }
    }
}

enum TaskDetailLoadState: Equatable {
    case idle
    case loading(id: String)
    case loaded(MobileTaskDetail)
    case failed(id: String, TaskDetailFailure)

    var taskID: String? {
        switch self {
        case .idle:
            return nil
        case .loading(let id), .failed(let id, _):
            return id
        case .loaded(let detail):
            return detail.id
        }
    }
}

struct MobileTaskStopRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
}

enum TaskDetailCopy {
    static func progress(_ detail: MobileTaskDetail) -> String {
        let current = detail.steps.firstIndex { $0.state == "current" }.map { $0 + 1 }
            ?? detail.steps.firstIndex { $0.state == "pending" }.map { $0 + 1 }
            ?? detail.steps.count
        return String.localizedStringWithFormat(String(localized: "Step %d of %d"),
                                                current, detail.steps.count)
    }

    static func startedLine(_ detail: MobileTaskDetail?) -> String {
        let runner = detail?.runner ?? String(localized: "On your computer")
        guard let date = detail?.startedAt else { return runner }
        let time = date.formatted(date: .omitted, time: .shortened)
        return String.localizedStringWithFormat(String(localized: "%@ · started %@"), runner, time)
    }
}

struct MobileTaskCounts: Codable, Equatable, Sendable {
    let needsYou: Int
    let running: Int
    let doneThisWeek: Int
}

struct MobileTasksPage: Codable, Equatable, Sendable {
    let tasks: [MobileTask]
    let counts: MobileTaskCounts
    /// The computer serves task threads (PROTOCOL.md 20.7b): Done rows carry their outcome.
    var threads: Bool? = nil
    /// Briefs and routines due in the next two days.
    var scheduled: [MobileScheduledTask]? = nil
    var waitingItems: [TodayNeed]? = nil
    var waitingCount: Int? = nil
}

/// A row in Tasks › Scheduled: a brief or one of your routines, at its next run.
struct MobileScheduledTask: Codable, Equatable, Hashable, Identifiable, Sendable {
    let id: String
    let kind: String
    var slot: String? = nil
    var title: String? = nil
    let at: String

    var date: Date? { RelayTime.parse(at) }

    /// The row as a task, so it opens a thread like every other row.
    var task: MobileTask {
        MobileTask(id: id, title: title ?? "", status: "scheduled", group: "scheduled", blockedReason: nil,
                   runner: nil, progress: nil, createdAt: at, updatedAt: at, source: kind == "reminder" ? "list" : nil)
    }
}

struct TaskGroups: Equatable, Sendable {
    let needsYou: [MobileTask]
    let running: [MobileTask]
    let done: [MobileTask]

    /// With threads, a Done task shows only with its outcome sentence: never a bare "Done".
    static func make(from tasks: [MobileTask], threads: Bool = false) -> TaskGroups {
        func newestFirst(_ lhs: MobileTask, _ rhs: MobileTask) -> Bool {
            switch (lhs.updatedDate, rhs.updatedDate) {
            case let (left?, right?) where left != right: return left > right
            default: return lhs.id < rhs.id
            }
        }
        let needs = tasks.filter { $0.group == "needs_you" }.sorted {
            let left = statusPriority($0.status)
            let right = statusPriority($1.status)
            return left == right ? newestFirst($0, $1) : left < right
        }
        return TaskGroups(
            needsYou: Array(needs.prefix(20)),
            running: Array(tasks.filter { $0.group == "running" }.sorted(by: newestFirst).prefix(20)),
            done: Array(tasks.filter { $0.group == "done" && (!threads || $0.outcome != nil) }
                .sorted(by: newestFirst).prefix(20))
        )
    }

    private static func statusPriority(_ status: String) -> Int {
        switch status {
        case "needs_review": return 0
        case "blocked": return 1
        default: return 2
        }
    }
}

struct TaskStatusDiff: Equatable, Sendable {
    let newlyRunning: Set<String>
    let newlyFinished: Set<String>

    static func between(previous: [MobileTask], current: [MobileTask]) -> TaskStatusDiff {
        let previousByID = previous.reduce(into: [String: MobileTask]()) { $0[$1.id] = $1 }
        var newlyRunning: Set<String> = []
        var newlyFinished: Set<String> = []
        for task in current {
            let old = previousByID[task.id]
            if task.group == "running", old?.group != "running" {
                newlyRunning.insert(task.id)
            }
            if task.group == "done", old?.group == "running" {
                newlyFinished.insert(task.id)
            }
        }
        return TaskStatusDiff(newlyRunning: newlyRunning, newlyFinished: newlyFinished)
    }
}

enum TaskDeckSwipe: Equatable {
    case start
    case notNow
    case none
}

struct TaskDeckMotion: Equatable {
    let rotationDegrees: Double
    let doItTint: Double
    let notNowTint: Double
    /// How far toward the 35% line the drag is, 0 to 1: the word pill fills and the next card rises with it.
    var progress: Double = 0

    static func presentation(translation: Double, width: Double) -> TaskDeckMotion {
        let normalized = width > 0 ? translation / width : 0
        return TaskDeckMotion(
            rotationDegrees: min(10, max(-10, normalized * 10)),
            doItTint: min(1, max(0, normalized / 0.35)),
            notNowTint: min(1, max(0, -normalized / 0.35)),
            progress: min(1, abs(normalized) / 0.35)
        )
    }

    /// The pill turns solid at the line.
    var atLine: Bool { progress >= 1 }

    static func crossedThreshold(from previous: Double, to current: Double, width: Double) -> Bool {
        guard width > 0 else { return false }
        let threshold = width * 0.35
        let previousSide = previous >= threshold ? 1 : previous <= -threshold ? -1 : 0
        let currentSide = current >= threshold ? 1 : current <= -threshold ? -1 : 0
        return currentSide != 0 && currentSide != previousSide
    }
}

struct TaskDeck: Equatable {
    enum Section: Equatable { case needsYou, upNext, running, scheduled, done }
    static func visibleSections(groups: TaskGroups, hasOffers: Bool, hasScheduled: Bool = false) -> [Section] {
        var sections: [Section] = []
        if !groups.needsYou.isEmpty { sections.append(.needsYou) }
        if hasOffers { sections.append(.upNext) }
        if !groups.running.isEmpty { sections.append(.running) }
        if hasScheduled { sections.append(.scheduled) }
        if !groups.done.isEmpty { sections.append(.done) }
        return sections
    }

    static func claimFirstLean(defaults: UserDefaults = .standard, reduceMotion: Bool) -> Bool {
        guard !reduceMotion, !defaults.bool(forKey: "tasks.deck.leanShown") else { return false }
        defaults.set(true, forKey: "tasks.deck.leanShown")
        return true
    }

    private(set) var offers: [IdeaOffer] = []
    private(set) var index = 0
    private(set) var deferred: Set<String> = []
    private var latestOffers: [IdeaOffer] = []
    private var deferredOffers: [String: IdeaOffer] = [:]
    private var held: Set<String> = []
    private var starting: IdeaOffer?
    var current: IdeaOffer? { offers.indices.contains(index) ? offers[index] : nil }
    var next: IdeaOffer? { offers.indices.contains(index + 1) ? offers[index + 1] : nil }

    mutating func replace(_ offers: [IdeaOffer]) {
        reconcile(with: offers)
    }

    mutating func reconcile(with offers: [IdeaOffer]) {
        let previousCurrent = current
        latestOffers = offers

        for id in Array(deferred) {
            guard let deferredOffer = deferredOffers[id],
                  let refreshedOffer = offers.first(where: { $0.id == id }),
                  deferredOffer.version == refreshedOffer.version else {
                deferred.remove(id)
                deferredOffers.removeValue(forKey: id)
                continue
            }
        }

        var available = offers.filter { !deferred.contains($0.id) && !held.contains($0.id) }
        if let starting, !available.contains(where: { $0.id == starting.id }) {
            available.insert(starting, at: min(index, available.count))
        }
        self.offers = available
        if available.isEmpty {
            index = 0
        } else if let previousCurrent,
                  let currentIndex = available.firstIndex(where: { $0.id == previousCurrent.id }) {
            index = currentIndex
        } else if previousCurrent != nil {
            index = min(index, available.count - 1)
        } else {
            index = 0
        }
    }

    mutating func swipe(translation: Double, width: Double) -> TaskDeckSwipe {
        let threshold = width * 0.35
        if translation >= threshold {
            return .start
        }
        if translation <= -threshold {
            return .notNow
        }
        return .none
    }

    @discardableResult
    mutating func start() -> IdeaOffer? {
        guard let offer = current else { return nil }
        if starting?.id == offer.id { starting = nil }
        index += 1
        return offer
    }

    mutating func prepareStart(_ offer: IdeaOffer) {
        guard current?.id == offer.id else { return }
        starting = offer
    }

    mutating func cancelStart() {
        starting = nil
        reconcile(with: latestOffers)
    }

    @discardableResult
    mutating func notNow() -> IdeaOffer? {
        guard let offer = current else { return nil }
        deferred.insert(offer.id)
        deferredOffers[offer.id] = offer
        held.insert(offer.id)
        index += 1
        return offer
    }

    @discardableResult
    mutating func dismiss() -> IdeaOffer? {
        guard let offer = current else { return nil }
        held.insert(offer.id)
        index += 1
        return offer
    }

    mutating func restore(_ offer: IdeaOffer) {
        held.remove(offer.id)
        deferred.remove(offer.id)
        deferredOffers.removeValue(forKey: offer.id)
        guard let currentOffer = latestOffers.first(where: { $0.id == offer.id }) else {
            reconcile(with: latestOffers)
            return
        }
        let available = latestOffers.filter { !deferred.contains($0.id) && !held.contains($0.id) }
        offers = available
        index = available.firstIndex(where: { $0.id == offer.id }) ?? 0
        if let restoredIndex = available.firstIndex(where: { $0.id == offer.id }) {
            offers[restoredIndex] = currentOffer
        }
    }

    mutating func commitHold(_ offer: IdeaOffer) {
        held.remove(offer.id)
        deferred.insert(offer.id)
        deferredOffers[offer.id] = offer
        reconcile(with: latestOffers)
    }

    mutating func findMore(offers: [IdeaOffer]) {
        reconcile(with: offers)
    }
}

@MainActor
final class DecisionHoldCoordinator: ObservableObject {
    struct Hold: Identifiable, Equatable {
        let id: String
        let label: String
        let detail: String
    }

    @Published private(set) var holds: [Hold] = []

    private let sleep: @MainActor (Duration) async throws -> Void
    private let beginBackgroundTask: @MainActor (@escaping @MainActor () -> Void) -> UIBackgroundTaskIdentifier
    private let endBackgroundTask: @MainActor (UIBackgroundTaskIdentifier) -> Void
    private var timers: [String: Task<Void, Never>] = [:]
    private var commits: [String: @MainActor () async -> Void] = [:]

    static func shouldFlush(when phase: ScenePhase) -> Bool {
        phase == .background
    }

    init(
        sleep: @escaping @MainActor (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
        beginBackgroundTask: @escaping @MainActor (@escaping @MainActor () -> Void) -> UIBackgroundTaskIdentifier = { expirationHandler in
            UIApplication.shared.beginBackgroundTask(
                withName: "Flush task decisions",
                expirationHandler: expirationHandler
            )
        },
        endBackgroundTask: @escaping @MainActor (UIBackgroundTaskIdentifier) -> Void = {
            UIApplication.shared.endBackgroundTask($0)
        }
    ) {
        self.sleep = sleep
        self.beginBackgroundTask = beginBackgroundTask
        self.endBackgroundTask = endBackgroundTask
    }

    func begin(_ hold: Hold, commit: @escaping @MainActor () async -> Void) {
        guard timers[hold.id] == nil else { return }
        holds.append(hold)
        commits[hold.id] = commit
        timers[hold.id] = Task { @MainActor [weak self] in
            guard let self else { return }
            do {
                try await sleep(.seconds(5))
            } catch {
                return
            }
            guard !Task.isCancelled else { return }
            timers[hold.id] = nil
            holds.removeAll { $0.id == hold.id }
            guard let commit = commits.removeValue(forKey: hold.id) else { return }
            await commit()
        }
    }

    @discardableResult
    func undo(_ id: String) -> Bool {
        guard commits[id] != nil else { return false }
        timers.removeValue(forKey: id)?.cancel()
        commits.removeValue(forKey: id)
        holds.removeAll { $0.id == id }
        return true
    }

    func flushAll() async {
        let pendingCommits = Array(commits.values)
        timers.values.forEach { $0.cancel() }
        timers.removeAll()
        commits.removeAll()
        holds.removeAll()
        guard !pendingCommits.isEmpty else { return }

        var backgroundTask = UIBackgroundTaskIdentifier.invalid
        var ended = false
        var expirationBeforeBeginReturned = false
        let endTask = endBackgroundTask
        let endBackgroundTaskOnce: @MainActor () -> Void = {
            guard !ended else { return }
            ended = true
            guard backgroundTask != .invalid else {
                expirationBeforeBeginReturned = true
                return
            }
            endTask(backgroundTask)
        }
        backgroundTask = beginBackgroundTask(endBackgroundTaskOnce)
        if expirationBeforeBeginReturned, backgroundTask != .invalid {
            endTask(backgroundTask)
        }
        defer { endBackgroundTaskOnce() }
        await withTaskGroup(of: Void.self) { group in
            for commit in pendingCommits {
                group.addTask {
                    await commit()
                }
            }
        }
    }
}

enum TasksCopy {
    /// "1 needs you · 1 running". With threads the Done count stays off the line (Phone Threads §1); an older
    /// computer's line keeps it.
    static func subtitle(_ counts: MobileTaskCounts, showsDone: Bool = true) -> String {
        var parts: [String] = []
        if counts.needsYou == 1 {
            parts.append(String(localized: "tasks.oneNeedsYou"))
        } else if counts.needsYou > 0 {
            parts.append(String.localizedStringWithFormat(String(localized: "%d need you"), counts.needsYou))
        }
        if counts.running > 0 {
            parts.append(String.localizedStringWithFormat(String(localized: "%d running"), counts.running))
        }
        if showsDone && counts.doneThisWeek > 0 {
            parts.append(String.localizedStringWithFormat(String(localized: "%d done this week"), counts.doneThisWeek))
        }
        return parts.isEmpty ? String(localized: "Nothing running right now") : parts.joined(separator: " · ")
    }

    /// A Needs you task's state, without its progress: the copper words on its row.
    static func state(_ task: MobileTask) -> String {
        switch task.status {
        case "draft_ready": return String(localized: "Draft ready")
        case "needs_review": return String(localized: "Ready for review")
        case "blocked":
            switch task.blockedReason {
            case "interrupted": return String(localized: "Stopped: Isoform restarted")
            case "timeout": return String(localized: "Stopped: no activity")
            default: return String(localized: "Stopped on your computer")
            }
        default: return String(localized: "Waiting to start on your computer")
        }
    }

    static func detail(_ task: MobileTask, now: Date = Date(), locale: Locale = .current) -> String {
        switch task.group {
        case "needs_you":
            switch task.status {
            case "needs_review":
                guard let progress = task.progress else { return String(localized: "Ready for review") }
                return String.localizedStringWithFormat(
                    String(localized: "Ready for review · %d of %d checks"), progress.done, progress.total
                )
            case "blocked":
                switch task.blockedReason {
                case "interrupted": return String(localized: "Stopped: Isoform restarted")
                case "timeout": return String(localized: "Stopped: no activity")
                default: return String(localized: "Stopped on your computer")
                }
            default:
                return String(localized: "Waiting to start on your computer")
            }
        case "running":
            let runner: String
            switch task.runner {
            case "claude_code": runner = String(localized: "Claude Code on your computer")
            case "codex": runner = String(localized: "Codex on your computer")
            default: runner = String(localized: "On your computer")
            }
            guard let progress = task.progress else { return runner }
            return runner + String.localizedStringWithFormat(
                String(localized: " · %d of %d done"), progress.done, progress.total
            )
        default:
            let date = RelayTime.parse(task.updatedAt) ?? now
            let calendar = Calendar.current
            let day: String
            if calendar.isDateInToday(date) {
                day = String(localized: "Today")
            } else if calendar.isDateInYesterday(date) {
                day = String(localized: "Yesterday")
            } else {
                day = date.formatted(.dateTime.weekday(.wide).locale(locale))
            }
            return String.localizedStringWithFormat(String(localized: "Done · %@"), day)
        }
    }
}
