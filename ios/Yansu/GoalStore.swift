import Foundation

/// Goals from the Mac's Purpose Map (PROTOCOL.md section 20.5): the list the Goals tab shows, kept on this iPhone so
/// it reads offline, and the check-offs, reopenings and waits the user makes. These local changes
/// go through the write journal: a move shows at once, a retry reuses its key so the Mac applies it once, and a move the
/// Mac did not answer (offline, asleep) is kept on this iPhone and sent again on the next refresh. New goals start in
/// the chat, not here.
@MainActor
final class GoalStore: ObservableObject {
    enum LoadState: Equatable {
        case idle
        case loading
        case loaded
        case failed
    }

    /// The last done or forget, which Undo can take back for `undoSeconds`.
    struct Undoable: Equatable, Sendable {
        var goal: GoalView
        /// The goal as it was before the done or forget; Undo shows it again at once.
        var before: GoalView
        var title: String
        var action: GoalAction
        var at: Date
    }

    /// A move this iPhone made that the Mac has not answered yet, with the goal as it was before.
    struct PendingMove: Codable, Equatable, Sendable {
        var action: GoalAction
        var waitingOn: String?
        var original: GoalView
        /// What to show until the Mac answers, when the move knows better than `optimistic` (Undo).
        var shown: GoalView? = nil
    }

    /// Tracking goals first, then done ones, in the Mac's order, with this iPhone's unanswered moves shown.
    @Published private(set) var goals: [GoalView] = []
    @Published private(set) var counts = GoalCounts()
    @Published private(set) var loadState: LoadState = .idle
    /// When the list on screen was read from the Mac.
    @Published private(set) var readAt: Date?
    /// Goals with a write in flight.
    @Published private(set) var busy: Set<String> = []
    /// By goal id.
    @Published private(set) var pending: [String: PendingMove] = [:]
    @Published var notice: String?
    /// What Undo would take back; the page shows it for `undoSeconds`.
    @Published private(set) var undoable: Undoable?

    static let pageLimit = 100
    static let maxPages = 5
    static let textMaxBytes = 1000
    static let undoSeconds: TimeInterval = 10

    var tracking: [GoalView] { goals.filter { !$0.isDone && !$0.isForgotten } }
    var done: [GoalView] { goals.filter { $0.isDone && !$0.isForgotten } }

    private struct Saved: Codable {
        var goals: [GoalView]
        var counts: GoalCounts
        var readAt: Date?
        var pending: [String: PendingMove]?
    }

    private let hub: GoalsHubAPI
    private let journal: PendingWriteJournal
    private let isPaired: @MainActor () -> Bool
    private let fileURL: URL
    private let now: @Sendable () -> Date
    private let writer: JournaledWriter
    private var generation = UUID()

    init(hub: GoalsHubAPI,
         journal: PendingWriteJournal,
         isPaired: @escaping @MainActor () -> Bool,
         fileURL: URL,
         now: @escaping @Sendable () -> Date = { Date() },
         makeKey: @escaping @Sendable () -> String = { UUID().uuidString.lowercased() }) {
        self.hub = hub
        self.journal = journal
        self.isPaired = isPaired
        self.fileURL = fileURL
        self.now = now
        self.writer = JournaledWriter(journal: journal, makeKey: makeKey, now: now)
        loadCached()
    }

    /// The list last read from the Mac, from this iPhone, so Goals reads offline.
    func loadCached() {
        guard let data = try? Data(contentsOf: fileURL), let saved = try? Self.decoder.decode(Saved.self, from: data) else {
            return
        }
        goals = saved.goals
        counts = saved.counts
        readAt = saved.readAt
        pending = saved.pending ?? [:]
    }

    // MARK: Reading

    /// Reads every page the Mac has, up to `maxPages`, then sends the moves the Mac has not answered. A failed read
    /// keeps the list already on screen.
    func refresh() async {
        guard isPaired(), loadState != .loading else { return }
        let current = generation
        loadState = .loading
        var collected: [GoalView] = []
        var latestCounts = GoalCounts()
        var cursor: String?
        do {
            for _ in 0..<Self.maxPages {
                let page = try await hub.goalsPage(limit: Self.pageLimit, cursor: cursor)
                guard current == generation else { return }
                collected.append(contentsOf: page.goals)
                latestCounts = page.counts
                guard page.hasMore, let next = page.cursor, !next.isEmpty else { break }
                cursor = next
            }
        } catch {
            guard current == generation else { return }
            loadState = .failed
            return
        }
        goals = collected
        counts = latestCounts
        readAt = now()
        loadState = .loaded
        for (id, move) in pending {
            showOptimistic(id: id, move: move)
        }
        save()
        await sendPending()
    }

    // MARK: Moving

    /// The checkbox: a tracking goal is checked off, a done one reopens.
    @discardableResult
    func toggle(_ goal: GoalView) async -> Bool {
        await move(goal, action: goal.isDone ? .reopen : .resolve, waitingOn: nil)
    }

    /// Parks a tracking goal on someone or something.
    @discardableResult
    func markWaiting(_ goal: GoalView, on waitingOn: String) async -> Bool {
        let text = Self.bounded(waitingOn)
        return await move(goal, action: .waiting, waitingOn: text.isEmpty ? nil : text)
    }

    /// Hides a tracking goal on both devices, with Undo.
    @discardableResult
    func forget(_ goal: GoalView) async -> Bool {
        await move(goal, action: .forget, waitingOn: nil)
    }

    /// Takes back the last done or forget within `undoSeconds`. A move still on this iPhone is taken back here and
    /// never sent; one the Mac made is restored there.
    @discardableResult
    func undo() async -> Bool {
        guard let last = undoable else { return false }
        let id = last.goal.id
        // While the move is on its way, Undo waits for the Mac's answer; the banner stays.
        guard !busy.contains(id) else { return false }
        undoable = nil
        guard now().timeIntervalSince(last.at) <= Self.undoSeconds, isPaired() else { return false }
        if let move = pending[id] {
            pending[id] = nil
            restore(move.original)
            save()
            return true
        }
        if !goals.contains(where: { $0.id == id }) {
            // A refresh dropped the forgotten goal; put it back so the move can find it.
            goals.append(last.goal)
        }
        guard let current = goals.first(where: { $0.id == id }) else { return false }
        // A goal that was waiting comes back waiting, never as active for a moment.
        var shown = last.before
        shown.eventId = current.eventId
        return await move(current, action: .restore, waitingOn: nil, shown: shown)
    }

    /// Undo is offered no more.
    func clearUndo() {
        undoable = nil
    }

    /// One goal's story from the Mac.
    func detail(_ goalID: String) async throws -> GoalDetail {
        try await hub.goalDetail(goalID: goalID)
    }

    func dismissNotice() {
        notice = nil
    }

    func clearAfterUnpair() {
        generation = UUID()
        goals = []
        counts = GoalCounts()
        readAt = nil
        loadState = .idle
        busy.removeAll()
        pending = [:]
        notice = nil
        undoable = nil
        try? FileManager.default.removeItem(at: fileURL)
    }

    // MARK: Helpers

    private func move(_ goal: GoalView, action: GoalAction, waitingOn: String?, shown: GoalView? = nil) async -> Bool {
        guard isPaired(), !busy.contains(goal.id), pending[goal.id] == nil,
              let original = goals.first(where: { $0.id == goal.id }) else { return false }
        guard let eventID = original.eventId, !eventID.isEmpty else {
            // Without the event the user saw, the Mac could not tell a stale move from a fresh one.
            notice = GoalCopy.refreshFirst
            Task { await refresh() }
            return false
        }
        busy.insert(goal.id)
        let current = generation
        defer { if current == generation { busy.remove(goal.id) } }
        guard current == generation else { return false }
        let move = PendingMove(action: action, waitingOn: waitingOn, original: original, shown: shown)
        pending[goal.id] = move
        showOptimistic(id: goal.id, move: move)
        if action == .resolve || action == .forget {
            undoable = Undoable(goal: goals.first(where: { $0.id == goal.id }) ?? original, before: original,
                                title: original.title ?? "", action: action, at: now())
        }
        save()
        return await deliver(id: goal.id, move: move, announces: true, generation: current)
    }

    /// Sends every move the Mac has not answered, under the key the journal kept. A move whose key the journal no
    /// longer holds (it keeps keys for a day) is dropped and the notice says so: a new key could apply it twice.
    /// A dropped move is put back first, so a reload that fails offline cannot leave it on screen; the list is then
    /// read again, because the Mac's copy may have moved on since the tap.
    private func sendPending() async {
        let current = generation
        var expired: [PendingMove] = []
        for (id, move) in pending where !busy.contains(id) {
            let existing = await journal.existing(operation: .goalEvent, target: id,
                                                  fingerprint: Self.fingerprint(id: id, move: move))
            guard current == generation else { return }
            guard existing != nil else {
                pending[id] = nil
                expired.append(move)
                continue
            }
            busy.insert(id)
            _ = await deliver(id: id, move: move, announces: false, generation: current)
            if current == generation { busy.remove(id) }
        }
        guard current == generation else { return }
        if !expired.isEmpty {
            for move in expired {
                restore(move.original)
            }
            notice = GoalCopy.expired(expired.count)
            save()
            await refresh()
        }
    }

    private func deliver(id: String, move: PendingMove, announces: Bool, generation current: UUID) async -> Bool {
        let outcome = await writer.send(.goalEvent, target: id, choice: move.action.rawValue,
                                        fingerprint: Self.fingerprint(id: id, move: move)) { key in
            guard current == self.generation else { throw CancellationError() }
            return try await self.hub.goalEvent(goalID: id, GoalEventRequest(
                idempotencyKey: key, action: move.action.rawValue, expectedEventId: move.original.eventId ?? "",
                waitingOn: move.waitingOn))
        }
        guard current == generation else { return false }
        switch outcome {
        case .sent(let updated):
            pending[id] = nil
            apply(updated)
            if undoable?.goal.id == id {
                // Undo names the event the Mac made.
                undoable?.goal = updated
            }
            save()
            return true
        case .failed(_, .keep):
            // No answer yet, or a Mac error that is not a conflict (5xx, 429, anything unclassified): the move stays
            // on this iPhone and its key in the journal for the next refresh. Only a 409 means the goal changed.
            if announces { notice = GoalCopy.waiting }
            return false
        case .failed(let error, .drop), .failed(let error, .changed):
            pending[id] = nil
            if undoable?.goal.id == id { undoable = nil }
            restore(move.original)
            notice = Self.message(for: error)
            save()
            Task { await refresh() }
            return false
        case .notSaved:
            pending[id] = nil
            if undoable?.goal.id == id { undoable = nil }
            restore(move.original)
            notice = GoalCopy.saveFailed
            save()
            return false
        case .cancelled:
            return false
        }
    }

    /// Shows a move before the Mac answers, with the counts to match.
    private func showOptimistic(id: String, move: PendingMove) {
        guard let index = goals.firstIndex(where: { $0.id == id }) else {
            // The Mac's lists leave a forgotten goal out; an Undo still on its way keeps it on screen.
            if let shown = move.shown, !shown.isForgotten {
                insert(shown)
            }
            return
        }
        // A copy the Mac changed since the move was made wins over the view an Undo remembered.
        if move.shown != nil, goals[index].eventId != move.original.eventId {
            return
        }
        let moved = move.shown ?? Self.optimistic(goals[index], action: move.action, waitingOn: move.waitingOn)
        recount(from: goals[index], to: moved)
        goals[index] = moved
    }

    /// The Mac's answer replaces the goal on screen; a goal the Mac just finished leads the done ones, one it
    /// reopened leads the list.
    private func apply(_ updated: GoalView) {
        guard let index = goals.firstIndex(where: { $0.id == updated.id }) else {
            // A restored goal the last read left out (it was forgotten then) joins the list.
            if !updated.isForgotten { insert(updated) }
            return
        }
        recount(from: goals[index], to: updated)
        goals.remove(at: index)
        insert(updated, counted: false)
    }

    /// Puts a goal in its place: a done one leads the done goals, any other leads the list.
    private func insert(_ goal: GoalView, counted: Bool = true) {
        if counted { count(goal, by: 1) }
        if goal.isDone {
            let firstDone = goals.firstIndex(where: \.isDone) ?? goals.endIndex
            goals.insert(goal, at: firstDone)
        } else {
            goals.insert(goal, at: 0)
        }
    }

    private func restore(_ original: GoalView) {
        guard let index = goals.firstIndex(where: { $0.id == original.id }) else { return }
        recount(from: goals[index], to: original)
        goals[index] = original
    }

    private func recount(from before: GoalView, to after: GoalView) {
        count(before, by: -1)
        count(after, by: 1)
    }

    private func count(_ goal: GoalView, by step: Int) {
        guard !goal.isForgotten else { return }
        if goal.isDone {
            counts.done = max(0, counts.done + step)
        } else {
            counts.tracking = max(0, counts.tracking + step)
        }
    }

    static func optimistic(_ goal: GoalView, action: GoalAction, waitingOn: String?) -> GoalView {
        var moved = goal
        switch action {
        case .resolve:
            moved.group = "done"
            moved.state = "resolved"
            moved.status = String(localized: "Done today.")
            moved.slipping = false
        case .reopen:
            moved.group = "tracking"
            moved.state = "active"
            moved.status = String(localized: "Reopened today.")
        case .waiting:
            moved.state = "waiting"
            moved.status = waitingOn.map { String(localized: "Waiting on \($0) since today.") } ?? String(localized: "Waiting since today.")
            moved.slipping = false
        case .forget:
            moved.group = "forgotten"
            moved.state = "dismissed"
            moved.status = String(localized: "Forgotten today.")
            moved.slipping = false
        case .restore:
            moved.group = "tracking"
            moved.state = "active"
            moved.status = String(localized: "Reopened today.")
        }
        return moved
    }

    static func fingerprint(id: String, move: PendingMove) -> String {
        WriteFingerprint.make([id, move.action.rawValue, move.original.eventId ?? "", move.waitingOn ?? ""])
    }

    /// Trimmed and cut to the Mac's 1000 bytes on a character boundary.
    static func bounded(_ text: String) -> String {
        var result = ""
        var bytes = 0
        for character in text.trimmingCharacters(in: .whitespacesAndNewlines) {
            let size = String(character).utf8.count
            guard bytes + size <= textMaxBytes else { break }
            result.append(character)
            bytes += size
        }
        return result
    }

    private static func message(for error: Error) -> String {
        if case HubRequestError.conflict = error {
            return GoalCopy.changed
        }
        if case HubRequestError.rejected(let status, _) = error, status == 404 {
            return GoalCopy.gone
        }
        return (error as? LocalizedError)?.errorDescription ?? GoalCopy.generic
    }

    private func save() {
        let saved = Saved(goals: goals, counts: counts, readAt: readAt, pending: pending)
        guard let data = try? Self.encoder.encode(saved) else { return }
        try? FileManager.default.createDirectory(at: fileURL.deletingLastPathComponent(),
                                                 withIntermediateDirectories: true)
        try? data.write(to: fileURL, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    private static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        encoder.outputFormatting = [.sortedKeys]
        return encoder
    }()

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }()
}

enum GoalCopy {
    static var generic: String { String(localized: "Isoform could not finish that. Try again.") }
    static var saveFailed: String { String(localized: "Isoform could not save this on this phone, so nothing was sent to your computer. Try again.") }
    /// No answer, or an answer that is not a conflict (the Mac could not save it yet): the move and its key stay.
    static var waiting: String { String(localized: "Isoform could not get this to your computer yet. It is saved on this phone and sent again when your computer answers.") }
    static var changed: String { String(localized: "This goal changed on your computer. Here is the latest.") }
    static var gone: String { String(localized: "This goal is no longer on your computer.") }
    static var refreshFirst: String { String(localized: "Isoform needs the latest copy of this goal from your computer first. Try again once it refreshes.") }

    /// The Undo banner after a done or forget.
    static func undoMessage(_ action: GoalAction, title: String, locale: Locale = .current) -> String {
        action == .forget
            ? localized("Forgot \(title). It won't come back on its own.", locale: locale)
            : localized("Marked \(title) done.", locale: locale)
    }

    static func expired(_ count: Int, locale: Locale = .current) -> String {
        count == 1
            ? localized("Your computer did not get one change to your goals in time. Make it again if you still mean it.", locale: locale)
            : localized("Your computer did not get \(count) changes to your goals in time. Make them again if you still mean them.", locale: locale)
    }

    /// An explicit locale also chooses the resource language (Foundation's locale argument only formats values).
    static func localized(_ value: String.LocalizationValue, locale: Locale) -> String {
        let language = locale.language.languageCode?.identifier == "zh" ? "zh-Hans" : "en"
        let bundle = Bundle.main.path(forResource: language, ofType: "lproj").flatMap(Bundle.init(path:)) ?? .main
        return String(localized: value, bundle: bundle, locale: locale)
    }
}
