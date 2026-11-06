import Foundation
import XCTest
@testable import Yansu

/// Records the Goals reads and writes GoalStore makes, under the keys it used, and can refuse them.
private final class StubGoalsHub: GoalsHubAPI, @unchecked Sendable {
    struct Event: Equatable {
        let goalID: String
        let request: GoalEventRequest
    }

    private let lock = NSLock()
    private var pages: [GoalPage] = []
    private var failures: [Error] = []
    private var eventFailures: [Error] = []
    private var events: [Event] = []
    private var cursors: [String?] = []
    private var readsToAllow = Int.max

    func locked<T>(_ body: () throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try body()
    }

    var sentEvents: [Event] { locked { events } }
    var askedCursors: [String?] { locked { cursors } }

    func serve(_ pages: [GoalPage]) { locked { self.pages = pages } }
    func failNext(_ errors: [Error]) { locked { readsToAllow = 0; failures = errors } }
    /// Fails only the next event writes, so a refresh can read the list and then resend.
    func failNextEvents(_ errors: [Error]) { locked { eventFailures = errors } }
    /// Lets `successes` more reads answer, then fails: a refresh can find an expired move while its reload is offline.
    func failReads(after successes: Int, with errors: [Error]) {
        locked {
            readsToAllow = successes
            failures = errors
        }
    }

    private func nextFailure() -> Error? {
        guard readsToAllow == 0 else {
            readsToAllow -= 1
            return nil
        }
        return failures.isEmpty ? nil : failures.removeFirst()
    }

    func goalsPage(limit: Int, cursor: String?) async throws -> GoalPage {
        let outcome: (Error?, GoalPage) = locked {
            cursors.append(cursor)
            let page = pages.isEmpty ? GoalPage() : pages.removeFirst()
            return (nextFailure(), page)
        }
        if let failure = outcome.0 { throw failure }
        return outcome.1
    }

    func goalDetail(goalID: String) async throws -> GoalDetail {
        GoalDetail(goal: GoalView(id: goalID, kind: "goal", title: "Server copy", state: "active", group: "tracking",
                                  status: "You added it today.", eventId: "evt-\(goalID)"),
                   outcome: "The launch ships.", history: [.init(what: "Added", at: "2026-10-02T09:00:00Z")])
    }

    func goalEvent(goalID: String, _ request: GoalEventRequest) async throws -> GoalView {
        let failure: Error? = locked {
            events.append(Event(goalID: goalID, request: request))
            return eventFailures.isEmpty ? nextFailure() : eventFailures.removeFirst()
        }
        if let failure { throw failure }
        switch request.action {
        case "forget":
            return GoalView(id: goalID, kind: "goal", title: "Server copy", state: "dismissed", group: "forgotten",
                            status: "Forgotten today.", eventId: "evt-\(request.idempotencyKey)")
        case "resolve":
            return GoalView(id: goalID, kind: "goal", title: "Server copy", state: "resolved", group: "done",
                            status: "Done today.", eventId: "evt-\(request.idempotencyKey)")
        case "waiting":
            return GoalView(id: goalID, kind: "goal", title: "Server copy", state: "waiting", group: "tracking",
                            status: "Waiting on \(request.waitingOn ?? "Someone else") since today.",
                            eventId: "evt-\(request.idempotencyKey)")
        default:
            return GoalView(id: goalID, kind: "goal", title: "Server copy", state: "active", group: "tracking",
                            status: "Reopened today.", eventId: "evt-\(request.idempotencyKey)")
        }
    }
}

private final class GoalKeys: @unchecked Sendable {
    private let lock = NSLock()
    private var count = 0

    func next() -> String {
        lock.lock()
        defer { lock.unlock() }
        count += 1
        return "goal-key-\(count)"
    }
}

@MainActor
final class GoalStoreTests: XCTestCase {
    private struct Harness {
        let hub: StubGoalsHub
        let journal: PendingWriteJournal
        let directory: URL
        let store: GoalStore
    }

    private func makeHarness(hub: StubGoalsHub = StubGoalsHub(), directory: URL? = nil,
                             now: @escaping @Sendable () -> Date = { Date() }) throws -> Harness {
        let folder = try directory ?? makeTemporaryDirectory()
        let journal = PendingWriteJournal(fileURL: folder.appendingPathComponent("writes.json"))
        let keys = GoalKeys()
        let store = GoalStore(hub: hub, journal: journal, isPaired: { true },
                              fileURL: folder.appendingPathComponent("goals.json"), now: now,
                              makeKey: { keys.next() })
        return Harness(hub: hub, journal: journal, directory: folder, store: store)
    }

    private func goal(_ id: String, done: Bool = false) -> GoalView {
        GoalView(id: id, kind: "goal", title: "Goal \(id)", state: done ? "resolved" : "active",
                 group: done ? "done" : "tracking", status: done ? "Done yesterday." : "You added it today.",
                 eventId: "evt-\(id)")
    }

    func testAPageDecodesTolerantlyAndSkipsAGoalItCannotRead() throws {
        let json = """
        {"goals":[{"id":"g1","kind":"commitment","title":"Send the draft","state":"active","group":"tracking",
                   "status":"Slipping: due today","slipping":true,"eventId":"e1","future":1},
                  {"title":"no id"},
                  {"id":"g2"}],
         "counts":{"tracking":2,"done":0,"onMacOnly":1},"cursor":"abc","hasMore":true,"newField":true}
        """
        let page = try JSONDecoder().decode(GoalPage.self, from: Data(json.utf8))
        XCTAssertEqual(page.goals.map(\.id), ["g1", "g2"])
        XCTAssertTrue(page.goals[0].slipping)
        XCTAssertFalse(page.goals[1].isDone)
        XCTAssertEqual(page.counts, GoalCounts(tracking: 2, done: 0, onMacOnly: 1))
        XCTAssertEqual(page.cursor, "abc")
        XCTAssertTrue(page.hasMore)
    }

    func testRefreshReadsEveryPageAndKeepsTheListOffline() async throws {
        let h = try makeHarness()
        h.hub.serve([
            GoalPage(goals: [goal("a"), goal("b")], counts: GoalCounts(tracking: 2, done: 1), cursor: "next", hasMore: true),
            GoalPage(goals: [goal("c", done: true)], counts: GoalCounts(tracking: 2, done: 1), hasMore: false),
        ])
        await h.store.refresh()
        XCTAssertEqual(h.hub.askedCursors, [nil, "next"])
        XCTAssertEqual(h.store.tracking.map(\.id), ["a", "b"])
        XCTAssertEqual(h.store.done.map(\.id), ["c"])
        XCTAssertEqual(h.store.loadState, .loaded)

        // Offline: the next launch shows what was read, and a failed read keeps it.
        let reopened = try makeHarness(directory: h.directory)
        XCTAssertEqual(reopened.store.goals.map(\.id), ["a", "b", "c"])
        reopened.hub.failNext([HubRequestError.noAnswer])
        await reopened.store.refresh()
        XCTAssertEqual(reopened.store.goals.map(\.id), ["a", "b", "c"])
        XCTAssertEqual(reopened.store.loadState, .failed)

        reopened.store.clearAfterUnpair()
        XCTAssertTrue(reopened.store.goals.isEmpty)
        XCTAssertTrue(try makeHarness(directory: h.directory).store.goals.isEmpty)
    }

    func testCheckingOffSendsTheEventTheUserSawAndMovesTheGoalToDone() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a"), goal("b"), goal("c", done: true)],
                              counts: GoalCounts(tracking: 2, done: 1))])
        await h.store.refresh()
        let checked = await h.store.toggle(h.store.goals[0])
        XCTAssertTrue(checked)
        XCTAssertEqual(h.hub.sentEvents, [.init(goalID: "a", request: GoalEventRequest(
            idempotencyKey: "goal-key-1", action: "resolve", expectedEventId: "evt-a", waitingOn: nil))])
        XCTAssertEqual(h.store.tracking.map(\.id), ["b"])
        XCTAssertEqual(h.store.done.map(\.id), ["a", "c"], "the newest done goal leads")
        XCTAssertEqual(h.store.counts, GoalCounts(tracking: 1, done: 2))
        let journal = await h.journal.entries()
        XCTAssertTrue(journal.isEmpty)

        let reopened = await h.store.toggle(h.store.done[0])
        XCTAssertTrue(reopened)
        XCTAssertEqual(h.hub.sentEvents.last?.request.action, "reopen")
        XCTAssertEqual(h.store.tracking.first?.id, "a")
    }

    func testWaitingParksATrackedGoalOnWhatTheUserTyped() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a"), goal("b")], counts: GoalCounts(tracking: 2))])
        await h.store.refresh()
        let parked = await h.store.markWaiting(h.store.goals[0], on: "  Ana  ")
        XCTAssertTrue(parked)
        XCTAssertEqual(h.hub.sentEvents, [.init(goalID: "a", request: GoalEventRequest(
            idempotencyKey: "goal-key-1", action: "waiting", expectedEventId: "evt-a", waitingOn: "Ana"))])
        XCTAssertEqual(h.store.tracking.map(\.id), ["a", "b"], "a waiting goal is still tracked")
        XCTAssertEqual(h.store.tracking.first?.state, "waiting")
        XCTAssertEqual(h.store.tracking.first?.status, "Waiting on Ana since today.")
        XCTAssertEqual(h.store.counts, GoalCounts(tracking: 2))
        XCTAssertTrue(h.store.pending.isEmpty)

        // Nothing typed: no waitingOn is sent, and the Mac names it.
        let blank = await h.store.markWaiting(h.store.goals[1], on: "   ")
        XCTAssertTrue(blank)
        XCTAssertEqual(h.hub.sentEvents.last?.request.action, "waiting")
        XCTAssertNil(h.hub.sentEvents.last?.request.waitingOn)
        XCTAssertEqual(h.store.tracking.first?.status, "Waiting on Someone else since today.")
    }

    func testGoalChangesDoNotAskForOwnerAuthentication() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        let checked = await h.store.toggle(h.store.goals[0])
        XCTAssertTrue(checked)
        XCTAssertEqual(h.hub.sentEvents.count, 1)
        XCTAssertEqual(h.store.done.map(\.id), ["a"])
        XCTAssertTrue(h.store.pending.isEmpty)
        XCTAssertNil(h.store.notice)
        let journal = await h.journal.entries()
        XCTAssertTrue(journal.isEmpty)
    }

    func testACheckOffTheMacDidNotAnswerStaysAndIsSentAgainOnTheNextRefresh() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a"), goal("b")], counts: GoalCounts(tracking: 2))])
        await h.store.refresh()
        h.hub.failNext([HubRequestError.noAnswer])
        let first = await h.store.toggle(h.store.goals[0])
        XCTAssertFalse(first)
        XCTAssertEqual(h.store.done.map(\.id), ["a"], "an unanswered check-off still shows")
        XCTAssertEqual(h.store.counts, GoalCounts(tracking: 1, done: 1))
        XCTAssertNotNil(h.store.pending["a"])
        XCTAssertEqual(h.store.notice, GoalCopy.waiting)
        let again = await h.store.toggle(h.store.done[0])
        XCTAssertFalse(again, "a goal with a move on its way takes no other move")

        // The next launch still shows it, and the next refresh sends it under the same key.
        let reopened = try makeHarness(hub: h.hub, directory: h.directory)
        XCTAssertEqual(reopened.store.done.map(\.id), ["a"])
        h.hub.serve([GoalPage(goals: [goal("a"), goal("b")], counts: GoalCounts(tracking: 2))])
        await reopened.store.refresh()
        XCTAssertEqual(h.hub.sentEvents.map(\.request.idempotencyKey), ["goal-key-1", "goal-key-1"],
                       "the retry reuses the journaled key, so the Mac applies it once")
        XCTAssertTrue(reopened.store.pending.isEmpty)
        XCTAssertEqual(reopened.store.done.map(\.id), ["a"])
        XCTAssertEqual(reopened.store.counts, GoalCounts(tracking: 1, done: 1))
        let journal = await reopened.journal.entries()
        XCTAssertTrue(journal.isEmpty)
    }

    func testAMacErrorThatIsNotAConflictKeepsTheMoveAndItsKey() async throws {
        struct Unclassified: Error {}
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        h.hub.failNext([HubRequestError.rejected(status: 503, message: "the computer could not save this goal")])
        let moved = await h.store.toggle(h.store.goals[0])
        XCTAssertFalse(moved)
        XCTAssertNotNil(h.store.pending["a"], "a Mac that could not save keeps the move")
        XCTAssertEqual(h.store.notice, GoalCopy.waiting)
        XCTAssertNotEqual(h.store.notice, GoalCopy.changed)
        var journal = await h.journal.entries()
        XCTAssertEqual(journal.map(\.key), ["goal-key-1"])

        // An error the phone cannot classify is retried the same way, under the same key.
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        h.hub.failNextEvents([Unclassified()])
        await h.store.refresh()
        XCTAssertNotNil(h.store.pending["a"])
        journal = await h.journal.entries()
        XCTAssertEqual(journal.map(\.key), ["goal-key-1"])

        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        XCTAssertEqual(h.hub.sentEvents.map(\.request.idempotencyKey), ["goal-key-1", "goal-key-1", "goal-key-1"])
        XCTAssertTrue(h.store.pending.isEmpty)
        XCTAssertEqual(h.store.done.map(\.id), ["a"])
    }

    func testOnlyAConflictReadsTheGoalAgainUnderANewKey() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1)),
                     GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        h.hub.failNext([HubRequestError.conflict(message: "this goal changed on your computer")])
        _ = await h.store.toggle(h.store.goals[0])
        XCTAssertEqual(h.store.notice, GoalCopy.changed)
        XCTAssertTrue(h.store.pending.isEmpty)
        let journal = await h.journal.entries()
        XCTAssertTrue(journal.isEmpty, "a conflict drops the key")
        for _ in 0..<20 where h.store.loadState != .loaded || h.hub.askedCursors.count < 2 {
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        _ = await h.store.toggle(h.store.goals[0])
        XCTAssertEqual(h.hub.sentEvents.map(\.request.idempotencyKey), ["goal-key-1", "goal-key-2"],
                       "a move made again after a conflict is a new write")
    }

    func testAMoveWhoseKeyExpiredReloadsTheListWithANotice() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        h.hub.failNext([HubRequestError.noAnswer])
        _ = await h.store.markWaiting(h.store.goals[0], on: "  Ana  ")
        XCTAssertEqual(h.store.tracking.first?.status, "Waiting on Ana since today.")
        XCTAssertEqual(h.hub.sentEvents.first?.request.waitingOn, "Ana")
        let keys = await h.journal.entries().map(\.key)
        await h.journal.remove(keys: keys)

        // The Mac's copy moved on since the tap: the expiry reads the list again (the refresh, then the reload)
        // instead of putting back the snapshot from before the tap.
        var newer = goal("a")
        newer.title = "Renamed on your computer"
        newer.status = "Due this Friday."
        h.hub.serve([GoalPage(goals: [newer], counts: GoalCounts(tracking: 1)),
                     GoalPage(goals: [newer], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        XCTAssertEqual(h.hub.sentEvents.count, 1, "a move without its key is never sent under a new one")
        XCTAssertEqual(h.store.tracking.first?.title, "Renamed on your computer")
        XCTAssertEqual(h.store.tracking.first?.status, "Due this Friday.")
        XCTAssertTrue(h.store.pending.isEmpty)
        XCTAssertEqual(h.store.notice, GoalCopy.expired(1))
    }

    func testAnExpiredMoveWhoseReloadFailsIsPutBackWithANotice() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        h.hub.failNext([HubRequestError.noAnswer])
        let moved = await h.store.toggle(h.store.goals[0])
        XCTAssertFalse(moved)
        XCTAssertEqual(h.store.done.map(\.id), ["a"], "an unanswered check-off shows as done until the Mac answers")
        let keys = await h.journal.entries().map(\.key)
        await h.journal.remove(keys: keys)

        // The next refresh reads the list and finds the move's key expired, but the reload it then tries fails:
        // the phone is offline. The goal goes back to where it was and the notice says the change did not land.
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        h.hub.failReads(after: 1, with: [HubRequestError.noAnswer])
        await h.store.refresh()
        XCTAssertEqual(h.hub.sentEvents.count, 1, "a move without its key is never sent under a new one")
        XCTAssertEqual(h.store.loadState, .failed, "the reload after the expiry failed")
        XCTAssertTrue(h.store.pending.isEmpty)
        XCTAssertEqual(h.store.tracking.map(\.id), ["a"], "the move the Mac never took is put back")
        XCTAssertTrue(h.store.done.isEmpty)
        XCTAssertEqual(h.store.tracking.first?.status, "You added it today.")
        XCTAssertEqual(h.store.counts, GoalCounts(tracking: 1))
        XCTAssertEqual(h.store.notice, GoalCopy.expired(1))

        // The next launch shows the same: the cache no longer holds the move the Mac never took.
        let reopened = try makeHarness(directory: h.directory)
        XCTAssertEqual(reopened.store.tracking.map(\.id), ["a"])
        XCTAssertTrue(reopened.store.done.isEmpty)
    }

    func testTheExpiredNoticeFollowsThePhonesLanguageChoice() {
        let en = Locale(identifier: "en-US")
        let zh = Locale(identifier: "zh-Hans-CN")
        XCTAssertEqual(GoalCopy.expired(1, locale: en),
                       "Your computer did not get one change to your goals in time. Make it again if you still mean it.")
        XCTAssertEqual(GoalCopy.expired(3, locale: en),
                       "Your computer did not get 3 changes to your goals in time. Make them again if you still mean them.")
        XCTAssertEqual(GoalCopy.expired(1, locale: zh),
                       "你的电脑没有及时收到一项目标更改。如果你仍需要这项更改，请重新操作。")
        XCTAssertEqual(GoalCopy.expired(3, locale: zh),
                       "你的电脑没有及时收到 3 项目标更改。如果你仍需要这些更改，请重新操作。")
    }

    func testAGoalThatChangedOnTheMacIsPutBackAndReadAgain() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1)),
                     GoalPage(goals: [goal("a", done: true)], counts: GoalCounts(done: 1))])
        await h.store.refresh()
        h.hub.failNext([HubRequestError.conflict(message: "this goal changed on your computer")])
        let moved = await h.store.toggle(h.store.goals[0])
        XCTAssertFalse(moved)
        XCTAssertEqual(h.store.notice, GoalCopy.changed)
        for _ in 0..<20 where h.store.done.isEmpty {
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        XCTAssertEqual(h.store.done.map(\.id), ["a"], "the Mac's version replaces the stale one")
    }

    func testEachCategoryHandsOffASentenceToFinishInChat() {
        XCTAssertEqual(GoalCategory.allCases.map(\.title),
                       ["Health", "Relationships", "Finance", "Career", "Interests", "Productivity", "Something else"])
        XCTAssertEqual(GoalCategory.health.chatPrefill, "I want to create a goal about health")
        // Something else is a whole sentence too, never one cut off mid-way.
        XCTAssertEqual(GoalCategory.other.chatPrefill, "I want to create a goal.")
    }

    func testEveryMoveNamesTheEventTheUserSaw() async throws {
        let encoder = JSONEncoder()
        for action in ["resolve", "reopen", "waiting"] {
            let body = try encoder.encode(GoalEventRequest(idempotencyKey: "k", action: action, expectedEventId: "evt-a",
                                                           waitingOn: nil))
            let json = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
            XCTAssertEqual(json["expectedEventId"] as? String, "evt-a", action)
        }

        let h = try makeHarness()
        var unread = goal("a")
        unread.eventId = nil
        h.hub.serve([GoalPage(goals: [unread], counts: GoalCounts(tracking: 1)),
                     GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        let moved = await h.store.toggle(h.store.goals[0])
        XCTAssertFalse(moved)
        XCTAssertTrue(h.hub.sentEvents.isEmpty, "a goal without its event is read again, not moved")
        XCTAssertEqual(h.store.notice, GoalCopy.refreshFirst)
        for _ in 0..<20 where h.store.goals.first?.eventId == nil {
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        let second = await h.store.toggle(h.store.goals[0])
        XCTAssertTrue(second)
        XCTAssertEqual(h.hub.sentEvents.map(\.request.expectedEventId), ["evt-a"])
    }

    func testTextIsCappedAtTheMacsByteLimit() {
        let capped = GoalStore.bounded(String(repeating: "目", count: 400))
        XCTAssertLessThanOrEqual(capped.utf8.count, GoalStore.textMaxBytes)
        XCTAssertEqual(capped.count, 333)
    }

    func testGoalWritesAreKeptByJournalPruning() {
        let write = PendingWrite(key: "k", operation: .goalEvent, target: "g1", choice: "resolve",
                                 fingerprint: "f", createdAt: Date())
        XCTAssertFalse(ActionStore.isRetired(write))
    }

    func testTheGoalsRouteCarriesItsLimitAndCursor() {
        XCTAssertEqual(HubRoute.goals(limit: 500, cursor: nil).query, "limit=100")
        XCTAssertEqual(HubRoute.goals(limit: 30, cursor: "eyJnIjowfQ").query, "limit=30&cursor=eyJnIjowfQ")
        XCTAssertEqual(HubRoute.goals(limit: 30, cursor: nil).path, "/goals")
        XCTAssertEqual(HubRoute.goals(limit: 30, cursor: nil).method, "GET")
    }

    // MARK: Forget and Undo (item 66)

    func testForgettingHidesTheGoalAndUndoAfterTheMacAnswerRestoresIt() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a"), goal("b")], counts: GoalCounts(tracking: 2))])
        await h.store.refresh()
        let forgot = await h.store.forget(h.store.goals[0])
        XCTAssertTrue(forgot)
        XCTAssertEqual(h.hub.sentEvents.map(\.request.action), ["forget"])
        XCTAssertEqual(h.store.tracking.map(\.id), ["b"], "a forgotten goal leaves the list")
        XCTAssertTrue(h.store.done.isEmpty)
        XCTAssertEqual(h.store.counts.tracking, 1)
        XCTAssertEqual(h.store.undoable?.action, .forget)
        XCTAssertEqual(h.store.undoable?.title, "Goal a")
        XCTAssertEqual(GoalCopy.undoMessage(.forget, title: "Goal a", locale: Locale(identifier: "en")),
                       "Forgot Goal a. It won't come back on its own.")

        let undone = await h.store.undo()
        XCTAssertTrue(undone)
        XCTAssertEqual(h.hub.sentEvents.last?.request, GoalEventRequest(
            idempotencyKey: "goal-key-2", action: "restore", expectedEventId: "evt-goal-key-1", waitingOn: nil),
            "Undo names the event the Mac made")
        XCTAssertEqual(Set(h.store.tracking.map(\.id)), ["a", "b"])
        XCTAssertEqual(h.store.counts.tracking, 2)
        XCTAssertNil(h.store.undoable)
    }

    func testUndoOfAForgetStillOnThisPhoneTakesItBackWithoutSendingAnything() async throws {
        let h = try makeHarness()
        h.hub.serve([GoalPage(goals: [goal("a")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        h.hub.failNext([HubRequestError.noAnswer])
        let forgot = await h.store.forget(h.store.goals[0])
        XCTAssertFalse(forgot)
        XCTAssertNotNil(h.store.pending["a"])
        XCTAssertTrue(h.store.tracking.isEmpty, "an unanswered forget still hides the goal")
        let undone = await h.store.undo()
        XCTAssertTrue(undone)
        XCTAssertNil(h.store.pending["a"], "the forget is never sent")
        XCTAssertEqual(h.store.tracking.map(\.id), ["a"])
        XCTAssertEqual(h.hub.sentEvents.map(\.request.action), ["forget"], "only the unanswered try")
        await h.store.refresh()
        XCTAssertEqual(h.hub.sentEvents.map(\.request.action), ["forget"], "a refresh does not resend it")
    }

    func testMarkingDoneOffersUndoAndUndoExpiresAfterTenSeconds() async throws {
        let clock = GoalClock()
        let h = try makeHarness(now: { clock.now })
        h.hub.serve([GoalPage(goals: [goal("a"), goal("b")], counts: GoalCounts(tracking: 2))])
        await h.store.refresh()
        await h.store.toggle(h.store.goals[0])
        XCTAssertEqual(h.store.undoable?.action, .resolve)
        XCTAssertEqual(GoalCopy.undoMessage(.resolve, title: "Goal a", locale: Locale(identifier: "en")), "Marked Goal a done.")
        clock.advance(GoalStore.undoSeconds + 1)
        let late = await h.store.undo()
        XCTAssertFalse(late)
        XCTAssertEqual(h.hub.sentEvents.map(\.request.action), ["resolve"], "nothing is sent after ten seconds")
        XCTAssertEqual(h.store.done.map(\.id), ["a"])

        await h.store.toggle(h.store.tracking[0])
        clock.advance(GoalStore.undoSeconds - 1)
        let inTime = await h.store.undo()
        XCTAssertTrue(inTime)
        XCTAssertEqual(h.hub.sentEvents.last?.request.action, "restore")
        XCTAssertEqual(h.store.tracking.map(\.id), ["b"], "the Mac's answer brings the goal back")
    }

    func testUndoOfAWaitingGoalShowsItWaitingWhileTheMacAnswers() async throws {
        let h = try makeHarness()
        var waiting = goal("a")
        waiting.state = "waiting"
        waiting.status = "Waiting on Ana since Tuesday."
        h.hub.serve([GoalPage(goals: [waiting], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        await h.store.forget(h.store.goals[0])
        XCTAssertTrue(h.store.tracking.isEmpty)
        h.hub.failNext([HubRequestError.noAnswer])
        let undone = await h.store.undo()
        XCTAssertFalse(undone, "the Mac has not answered the Undo yet")
        XCTAssertEqual(h.store.pending["a"]?.action, .restore)
        XCTAssertEqual(h.hub.sentEvents.last?.request.expectedEventId, "evt-goal-key-1", "Undo names the forget it takes back")
        let shown = try XCTUnwrap(h.store.tracking.first)
        XCTAssertEqual(shown.state, "waiting", "never active for a moment")
        XCTAssertEqual(shown.status, "Waiting on Ana since Tuesday.")
        XCTAssertEqual(h.store.counts.tracking, 1)
    }

    func testAnUndoOnItsWayOutlivesARefresh() async throws {
        let h = try makeHarness()
        var waiting = goal("a")
        waiting.state = "waiting"
        waiting.status = "Waiting on Ana since Tuesday."
        h.hub.serve([GoalPage(goals: [waiting, goal("b")], counts: GoalCounts(tracking: 2))])
        await h.store.refresh()
        await h.store.forget(h.store.goals[0])
        h.hub.failNext([HubRequestError.noAnswer])
        _ = await h.store.undo()
        XCTAssertEqual(h.store.pending["a"]?.action, .restore)

        // The computer's next list leaves the forgotten goal out, and the resend goes unanswered too.
        h.hub.serve([GoalPage(goals: [goal("b")], counts: GoalCounts(tracking: 1))])
        h.hub.failNextEvents([HubRequestError.noAnswer])
        await h.store.refresh()
        XCTAssertEqual(Set(h.store.tracking.map(\.id)), ["a", "b"], "the Undo on its way keeps the goal on screen")
        XCTAssertEqual(h.store.tracking.first(where: { $0.id == "a" })?.state, "waiting")
        XCTAssertEqual(h.store.counts.tracking, 2)

        // The computer answers: the goal stays, as the computer sends it.
        h.hub.serve([GoalPage(goals: [goal("b")], counts: GoalCounts(tracking: 1))])
        await h.store.refresh()
        XCTAssertNil(h.store.pending["a"])
        XCTAssertEqual(Set(h.store.tracking.map(\.id)), ["a", "b"])
        XCTAssertEqual(h.store.tracking.first(where: { $0.id == "a" })?.title, "Server copy")
    }

    func testAMoveSavedBeforeUndoRememberedItsViewStillReads() throws {
        let json = #"{"action":"resolve","original":{"id":"a","title":"Goal a","group":"tracking"}}"#
        let move = try JSONDecoder().decode(GoalStore.PendingMove.self, from: Data(json.utf8))
        XCTAssertEqual(move.action, .resolve)
        XCTAssertNil(move.shown)
    }

    func testTheGoalPagesWordsHaveChinese() {
        let zh = Locale(identifier: "zh-Hans")
        for (english, chinese) in [("Mark done", "标记为完成"), ("Reopen", "重新打开"), ("Waiting on someone", "在等别人"),
                                   ("Forget this goal", "忘掉这个目标"), ("Talk about this goal", "聊聊这个目标"),
                                   ("Checking with your computer.", "正在向电脑确认。"), ("Cancel", "取消"), ("Save", "保存"),
                                   ("Tracking", "进行中"), ("Create a goal", "设定一个目标"), ("No goals yet.", "还没有目标。"),
                                   ("Not on your computer yet.", "还没同步到电脑。"), ("Show %lld more", "再显示 %lld 个"),
                                   ("Health", "健康"), ("Forgotten today.", "今天忘掉了。"),
                                   ("Isoform could not get this to your computer yet. It is saved on this phone and sent again when your computer answers.",
                                    "Isoform 还没能把这一步送到你的电脑。它已保存在这部手机上，电脑回应后会再次发送。")] {
            XCTAssertEqual(GoalCopy.localized(String.LocalizationValue(english), locale: zh), chinese, english)
        }
    }

    func testAGoalsPageDecodesItsStory() async throws {
        let json = """
        {"goal":{"id":"g1","title":"Renew passport","state":"waiting","group":"tracking","eventId":"e1"},
         "outcome":"Passport in hand before the trip.","history":[{"what":"Waiting","at":"2026-10-09T10:00:00Z"},{"bad":1}]}
        """
        let detail = try JSONDecoder().decode(GoalDetail.self, from: Data(json.utf8))
        XCTAssertEqual(detail.goal.id, "g1")
        XCTAssertEqual(detail.outcome, "Passport in hand before the trip.")
        XCTAssertEqual(detail.history, [.init(what: "Waiting", at: "2026-10-09T10:00:00Z")])
        XCTAssertEqual(HubRoute.goal(id: "g1").path, "/goals/g1")
        XCTAssertEqual(HubRoute.goal(id: "g1").method, "GET")
        let h = try makeHarness()
        let read = try await h.store.detail("g1")
        XCTAssertEqual(read.outcome, "The launch ships.")
    }
}

/// A clock the Undo tests move by hand.
private final class GoalClock: @unchecked Sendable {
    private let lock = NSLock()
    private var current = Date(timeIntervalSince1970: 1_800_000_000)

    var now: Date {
        lock.lock()
        defer { lock.unlock() }
        return current
    }

    func advance(_ seconds: TimeInterval) {
        lock.lock()
        current = current.addingTimeInterval(seconds)
        lock.unlock()
    }
}
