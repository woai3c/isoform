import Foundation
import XCTest
@testable import Yansu

final class TasksModelsTests: XCTestCase {
    func testTasksChineseCopyCoversHeadersProgressAndEmptyStates() throws {
        let path = try XCTUnwrap(Bundle.main.path(forResource: "zh-Hans", ofType: "lproj"))
        let bundle = try XCTUnwrap(Bundle(path: path))
        for key in ["Needs you", "Up next", "Running", "Done this week", "Review",
                    "UP NEXT · %d OF %d", "%d need you", "%d running", "%d done this week",
                    "Finding more tasks…", "More arrive after %@", "Nothing new right now. Try again later.",
                    "Couldn't find more tasks. Try again.", "Nothing running right now",
                    "Nothing needs you right now", "Nothing completed this week", "%d of %d"] {
            let translated = bundle.localizedString(forKey: key, value: nil, table: nil)
            XCTAssertFalse(translated.isEmpty, key)
            XCTAssertNotEqual(translated, key, key)
        }
        XCTAssertEqual(bundle.localizedString(forKey: "Needs you", value: nil, table: nil), "需要你")
        XCTAssertEqual(bundle.localizedString(forKey: "Review", value: nil, table: nil), "查看")
        XCTAssertEqual(String.localizedStringWithFormat(bundle.localizedString(forKey: "%d need you", value: nil, table: nil), 2),
                       "2 项需要你")
    }

    @MainActor
    func testTodayDoneUsesTasksOutcomesAndKeepsOpenedUntilNextBrief() async throws {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        func stamp(_ seconds: TimeInterval) -> String { ISO8601DateFormatter().string(from: now.addingTimeInterval(seconds)) }
        func row(_ id: String, by: String = "isoform", at: TimeInterval = -3600, opened: TimeInterval? = nil) -> MobileTask {
            MobileTask(id: id, title: id, status: "completed", group: "done", blockedReason: nil, runner: nil,
                       progress: nil, createdAt: stamp(at), updatedAt: stamp(at),
                       outcome: MobileTaskOutcome(kind: "completed", by: by, at: stamp(at), params: ["summary": id]),
                       openedAt: opened.map(stamp))
        }
        let page = MobileTasksPage(tasks: [row("open"), row("opened", opened: -600), row("old", at: -90_000),
                                           row("mine", by: "you"), row("draft.sent")],
                                   counts: MobileTaskCounts(needsYou: 0, running: 0, doneThisWeek: 5), threads: true)
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let store = TasksStore(hub: TasksStoreTestHub(page: page), isPaired: { true }, hubID: { "hub" },
                               fileURL: directory.appendingPathComponent("tasks.json"))
        await store.refresh()
        XCTAssertEqual(Set(store.doneForToday(now: now, briefMadeAt: now.addingTimeInterval(-1200)).map(\.id)), Set(["open", "opened"]))
        XCTAssertEqual(store.doneForToday(now: now, briefMadeAt: now).map(\.id), ["open"])
    }

    @MainActor
    func testOfflineDraftStaysQueuedAndCanBeUndone() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let journal = PendingWriteJournal(fileURL: directory.appendingPathComponent("journal.json"), hubID: { "hub" })
        let pending = try await journal.reserveTodayDraft(taskID: "draft.test", revision: "revision", text: "Edited words",
                                                          deviceID: "phone", makeKey: { "key" },
                                                          createdAt: Date().addingTimeInterval(-10))
        let page = MobileTasksPage(tasks: [], counts: MobileTaskCounts(needsYou: 0, running: 0, doneThisWeek: 0))
        let store = TasksStore(hub: TasksStoreTestHub(page: page), isPaired: { true }, hubID: { "hub" },
                               journal: journal, deviceID: { "phone" }, computerOnline: { false },
                               fileURL: directory.appendingPathComponent("tasks.json"))
        await store.retryDraftSends()
        XCTAssertEqual(store.pendingDraftSends.first?.todayDraftSend?.text, "Edited words")
        XCTAssertNil(store.pendingDraftSends.first?.todayDraftSend?.dispatchedAt)
        await store.undoDraftSend(pending.key)
        XCTAssertTrue(store.pendingDraftSends.isEmpty)
    }

    @MainActor
    func testCompletedWaitingTaskUpdatesSharedCountWhenRefreshFails() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let row = task("draft.test", status: "draft_ready", group: "needs_you")
        let need = TodayNeed(id: row.id, chatId: nil, card: nil, task: row)
        let page = MobileTasksPage(tasks: [row], counts: MobileTaskCounts(needsYou: 1, running: 0, doneThisWeek: 0),
                                   waitingItems: [need], waitingCount: 1)
        let store = TasksStore(hub: CompletedWaitingTaskHub(page: page), isPaired: { true }, hubID: { "hub" },
                               fileURL: directory.appendingPathComponent("tasks.json"))
        await store.refresh()
        let sent = await store.act(id: row.id, action: "send")
        XCTAssertTrue(sent)
        XCTAssertEqual(store.waitingCount, 0)
        XCTAssertTrue(store.waitingItems.isEmpty)
        XCTAssertEqual(store.page?.tasks.first?.group, "done")
    }

    @MainActor
    func testSentDraftClearsPendingBeforeSlowListRefresh() async throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let journal = PendingWriteJournal(fileURL: directory.appendingPathComponent("journal.json"), hubID: { "hub" })
        _ = try await journal.reserveTodayDraft(taskID: "draft.test", revision: "revision", text: nil,
                                                deviceID: "phone", makeKey: { "key" },
                                                createdAt: Date().addingTimeInterval(-10))
        let store = TasksStore(hub: SlowRefreshAfterSendHub(), isPaired: { true }, hubID: { "hub" },
                               journal: journal, deviceID: { "phone" },
                               fileURL: directory.appendingPathComponent("tasks.json"))
        await store.loadPendingDraftSends()
        XCTAssertEqual(store.pendingDraftSends.count, 1)
        let replay = Task { await store.retryDraftSends() }
        for _ in 0..<100 where !store.pendingDraftSends.isEmpty { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(store.pendingDraftSends.isEmpty, "a slow list refresh must not leave a sent draft checking")
        replay.cancel()
        await replay.value
    }

    func testVisibleSectionsFollowTasksOrderAndOmitEmptyGroups() {
        let empty = TaskGroups.make(from: [])
        XCTAssertEqual(TaskDeck.visibleSections(groups: empty, hasOffers: false), [])
        XCTAssertEqual(TaskDeck.visibleSections(groups: empty, hasOffers: true), [.upNext])
        let groups = TaskGroups.make(from: [
            task("needs", status: "active", group: "needs_you"),
            task("run", status: "in_progress", group: "running"),
            task("done", status: "completed", group: "done")
        ])
        XCTAssertEqual(TaskDeck.visibleSections(groups: groups, hasOffers: true), [.needsYou, .upNext, .running, .done])
        XCTAssertEqual(TaskDeck.visibleSections(groups: groups, hasOffers: false), [.needsYou, .running, .done])
    }

    func testLeanIsOncePerPhoneAndReduceMotionDoesNotConsumeIt() throws {
        let suite = "TaskDeckLeanTests-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        XCTAssertFalse(TaskDeck.claimFirstLean(defaults: defaults, reduceMotion: true))
        XCTAssertTrue(TaskDeck.claimFirstLean(defaults: defaults, reduceMotion: false))
        XCTAssertFalse(TaskDeck.claimFirstLean(defaults: try XCTUnwrap(UserDefaults(suiteName: suite)), reduceMotion: false))
    }

    func testNextCardIsInOrderAndUndoRestoresStack() {
        var deck = TaskDeck()
        let first = IdeaOffer(id: "one", kind: "task", title: "One", why: "", prompt: "One")
        let second = IdeaOffer(id: "two", kind: "task", title: "Two", why: "", prompt: "Two")
        deck.replace([first, second])
        XCTAssertEqual(deck.current, first)
        XCTAssertEqual(deck.next, second)
        _ = deck.notNow()
        XCTAssertEqual(deck.current, second)
        XCTAssertNil(deck.next)
        deck.restore(first)
        XCTAssertEqual(deck.current, first)
        XCTAssertEqual(deck.next, second)
        deck.replace([])
        XCTAssertNil(deck.current)
        XCTAssertNil(deck.next)
    }

    func testFindMoreResponseDecodesCooldownAndEmptyBatch() throws {
        let response = try JSONDecoder().decode(IdeasPage.self, from: Data(#"{"offers":[],"nextAllowedAt":"2026-10-27T18:00:00Z"}"#.utf8))
        XCTAssertTrue(response.offers.isEmpty)
        XCTAssertEqual(response.nextAllowedAt, "2026-10-27T18:00:00Z")
        XCTAssertNil(try JSONDecoder().decode(IdeasPage.self, from: Data(#"{"offers":[]}"#.utf8)).nextAllowedAt)
        XCTAssertThrowsError(try JSONDecoder().decode(IdeasPage.self, from: Data(#"{"offers":null}"#.utf8)))
    }
    func testTaskGroupsPrioritizeReviewThenBlockedAndLimitEachGroup() {
        let tasks = [
            task("active-new", status: "active", group: "needs_you", updated: "2026-10-26T12:00:00Z"),
            task("blocked-old", status: "blocked", group: "needs_you", updated: "2026-10-26T09:00:00Z"),
            task("review-old", status: "needs_review", group: "needs_you", updated: "2026-10-26T08:00:00Z"),
            task("blocked-new", status: "blocked", group: "needs_you", updated: "2026-10-26T11:00:00Z")
        ] + (0..<25).map {
            task("running-\($0)", status: "in_progress", group: "running",
                 updated: String(format: "2026-10-26T10:%02d:00Z", $0))
        } + (0..<22).map {
            task("done-\($0)", status: "completed", group: "done",
                 updated: String(format: "2026-10-26T09:%02d:00Z", $0))
        }

        let groups = TaskGroups.make(from: tasks)
        XCTAssertEqual(groups.needsYou.map(\.id), ["review-old", "blocked-new", "blocked-old", "active-new"])
        XCTAssertEqual(groups.running.count, 20)
        XCTAssertEqual(groups.done.count, 20)
        XCTAssertEqual(groups.running.first?.id, "running-24")
    }

    func testTaskCountsDecodeAndSubtitleHasEmptyAndNonzeroForms() throws {
        let data = Data("""
        {"tasks":[],"counts":{"needsYou":23,"running":4,"doneThisWeek":7}}
        """.utf8)
        let page = try JSONDecoder().decode(MobileTasksPage.self, from: data)
        XCTAssertEqual(page.counts.needsYou, 23)
        XCTAssertEqual(page.counts.running, 4)
        XCTAssertEqual(page.counts.doneThisWeek, 7)
        XCTAssertFalse(TasksCopy.subtitle(page.counts).isEmpty)
        XCTAssertFalse(TasksCopy.subtitle(MobileTaskCounts(needsYou: 0, running: 0, doneThisWeek: 0)).isEmpty)
    }

    func testOptionalTaskFieldsDecodeAndStatusDetailsAreBounded() throws {
        let data = Data("""
        {"id":"t1","title":"Check the result","status":"needs_review","group":"needs_you",
         "createdAt":"2026-10-26T10:00:00Z","updatedAt":"2026-10-26T11:00:00Z"}
        """.utf8)
        let decodedTask = try JSONDecoder().decode(MobileTask.self, from: data)
        XCTAssertNil(decodedTask.progress)
        XCTAssertEqual(TasksCopy.detail(decodedTask), "Ready for review")

        XCTAssertEqual(TasksCopy.detail(task("blocked", status: "blocked", group: "needs_you",
                                             blockedReason: "timeout")),
                       "Stopped: no activity")
        XCTAssertEqual(TasksCopy.detail(task("active", status: "active", group: "needs_you")),
                       "Waiting to start on your computer")
    }

    func testTaskDetailDecodesRunnerStepsAndProgressCopy() throws {
        let data = Data("""
        {"id":"task-1","title":"Run tests","status":"in_progress","group":"running",
         "runner":"claude_code","progress":{"done":1,"total":3},
         "createdAt":"2026-10-26T09:12:00Z","updatedAt":"2026-10-26T09:20:00Z",
         "startedAt":"2026-10-26T09:12:00Z",
         "steps":[{"text":"Build the app","state":"done"},
                  {"text":"Run compatibility tests","state":"current"},
                  {"text":"Review the result","state":"pending"}]}
        """.utf8)

        let detail = try JSONDecoder().decode(MobileTaskDetail.self, from: data)

        XCTAssertEqual(detail.runner, "claude_code")
        XCTAssertEqual(detail.startedAt, RelayTime.parse("2026-10-26T09:12:00Z"))
        XCTAssertEqual(detail.steps.map(\.state), ["done", "current", "pending"])
        XCTAssertEqual(detail.task.id, "task-1")
        XCTAssertEqual(TaskDetailCopy.progress(detail), "Step 2 of 3")
    }

    func testTaskDetailOmitsAnUnstartedAttemptAndItsCopy() throws {
        let data = Data("""
        {"id":"task-1","title":"Run tests","status":"active","group":"needs_you",
         "runner":"claude_code","createdAt":"2026-10-26T09:12:00Z",
         "updatedAt":"2026-10-26T09:20:00Z","steps":[]}
        """.utf8)

        let detail = try JSONDecoder().decode(MobileTaskDetail.self, from: data)

        XCTAssertNil(detail.startedAt)
        XCTAssertEqual(TaskDetailCopy.startedLine(detail), "claude_code")
    }

    func testCalendarHoldOfferRecognitionForCachedIdeas() {
        XCTAssertTrue(offer("hold", title: "Hold today 2 to 4 PM for the Q4 plan").isCalendarHoldOffer)
        XCTAssertTrue(offer("hold-zh", title: "留出今天下午 2 点到 4 点").isCalendarHoldOffer)
        XCTAssertTrue(offer("hold-zh-day", title: "为Q4计划留出周一上午").isCalendarHoldOffer)
        XCTAssertFalse(offer("review", title: "Review the Q4 plan").isCalendarHoldOffer)
        XCTAssertFalse(offer("block", title: "Block pop-up ads").isCalendarHoldOffer)
    }

    func testOfferDeckUsesThirtyFivePercentThresholdAndDoesNotRepeatDeferredItems() {
        var deck = TaskDeck()
        let first = offer("first")
        let second = offer("second")
        deck.replace([first, second])

        XCTAssertEqual(deck.swipe(translation: 34.9, width: 100), .none)
        XCTAssertEqual(deck.current?.id, "first")
        XCTAssertEqual(deck.swipe(translation: 35, width: 100), .start)
        XCTAssertEqual(deck.current?.id, "first")
        XCTAssertEqual(deck.start()?.id, "first")
        XCTAssertEqual(deck.current?.id, "second")
        XCTAssertEqual(deck.swipe(translation: -35, width: 100), .notNow)
        XCTAssertEqual(deck.current?.id, "second")
        XCTAssertEqual(deck.notNow()?.id, "second")
        XCTAssertNil(deck.current)

        deck.findMore(offers: [first, second])
        XCTAssertEqual(deck.current?.id, "first")
        XCTAssertEqual(deck.deferred, ["second"])
    }

    func testReconcileClearsAnOfferWhenTheRefreshIsEmpty() {
        var deck = TaskDeck()
        let first = offer("first")
        deck.reconcile(with: [first])
        XCTAssertEqual(deck.current, first)

        deck.reconcile(with: [])

        XCTAssertNil(deck.current)
    }

    func testReconcileDropsWithdrawnCurrentOfferAndShowsSurvivor() {
        var deck = TaskDeck()
        let withdrawn = offer("withdrawn")
        let survivor = offer("survivor")
        deck.reconcile(with: [withdrawn, survivor])

        deck.reconcile(with: [survivor])

        XCTAssertEqual(deck.current, survivor)
    }

    func testReconcileReplacesTheCurrentOfferWhenItsVersionChanges() {
        var deck = TaskDeck()
        deck.reconcile(with: [offer("same-id", title: "Old title", version: "old")])

        deck.reconcile(with: [offer("same-id", title: "New title", version: "new")])

        XCTAssertEqual(deck.current?.title, "New title")
    }

    func testUndoDoesNotRestoreAnOfferWithdrawnDuringItsHold() {
        var deck = TaskDeck()
        let held = offer("held")
        let survivor = offer("survivor")
        deck.reconcile(with: [held, survivor])
        XCTAssertEqual(deck.dismiss(), held)

        deck.reconcile(with: [survivor])
        deck.restore(held)

        XCTAssertEqual(deck.current, survivor)
        XCTAssertFalse(deck.offers.contains(where: { $0.id == held.id }))
    }

    func testReconcileKeepsTheCurrentSurvivorWhenEarlierOffersAreRemoved() {
        var deck = TaskDeck()
        let first = offer("first")
        let survivor = offer("survivor")
        let last = offer("last")
        deck.reconcile(with: [first, survivor, last])
        _ = deck.start()

        deck.reconcile(with: [last, survivor])

        XCTAssertEqual(deck.current, survivor)
    }

    func testReconcileKeepsTheDoItOfferAndAdvancesToTheNextOffer() {
        var deck = TaskDeck()
        let first = offer("first")
        let next = offer("next")
        deck.reconcile(with: [first, next])

        deck.reconcile(with: [first, next])

        XCTAssertEqual(deck.start(), first)
        XCTAssertEqual(deck.current, next)
    }

    func testReconcileKeepsAStartingOfferUntilChatOpens() {
        var deck = TaskDeck()
        let starting = offer("starting")
        deck.reconcile(with: [starting, offer("next")])
        deck.prepareStart(starting)

        deck.reconcile(with: [])

        XCTAssertEqual(deck.current, starting)
        XCTAssertEqual(deck.start(), starting)
        XCTAssertNil(deck.current)
    }

    func testDeckMotionClampsRotationAndTracksThresholdCrossingsInBothDirections() {
        XCTAssertEqual(TaskDeckMotion.presentation(translation: 200, width: 100).rotationDegrees, 10)
        XCTAssertEqual(TaskDeckMotion.presentation(translation: -200, width: 100).rotationDegrees, -10)
        XCTAssertTrue(TaskDeckMotion.crossedThreshold(from: 34, to: 35, width: 100))
        XCTAssertTrue(TaskDeckMotion.crossedThreshold(from: -34, to: -35, width: 100))
        XCTAssertFalse(TaskDeckMotion.crossedThreshold(from: 35, to: 20, width: 100))
        XCTAssertTrue(TaskDeckMotion.crossedThreshold(from: 20, to: 35, width: 100))
    }

    func testTaskStatusDiffFindsNewRunningAndRunningToDoneByStableID() {
        let previous = [task("kept", status: "in_progress", group: "running", updated: "2026-10-26T12:00:00Z"),
                        task("finished", status: "in_progress", group: "running", updated: "2026-10-26T12:00:00Z")]
        let current = [task("kept", status: "in_progress", group: "running", updated: "2026-10-26T12:00:00Z"),
                       task("finished", status: "completed", group: "done", updated: "2026-10-26T12:00:00Z"),
                       task("new", status: "in_progress", group: "running", updated: "2026-10-26T12:00:00Z")]
        XCTAssertEqual(TaskStatusDiff.between(previous: previous, current: current),
                       TaskStatusDiff(newlyRunning: ["new"], newlyFinished: ["finished"]))
    }

    @MainActor
    func testDecisionHoldsUndoIndependentlyAndExpireExactlyOnce() async throws {
        var durations: [Duration] = []
        var commits: [String: Int] = [:]
        let holds = DecisionHoldCoordinator { duration in
            durations.append(duration)
            try await Task.sleep(for: .milliseconds(10))
        }
        holds.begin(.init(id: "undo", label: "Not now", detail: "One")) {
            commits["undo", default: 0] += 1
        }
        holds.begin(.init(id: "expire", label: "Sent to your computer", detail: "Two")) {
            commits["expire", default: 0] += 1
        }
        XCTAssertTrue(holds.undo("undo"))
        try await Task.sleep(for: .milliseconds(40))
        XCTAssertEqual(commits["undo", default: 0], 0)
        XCTAssertEqual(commits["expire", default: 0], 1)
        XCTAssertEqual(holds.holds, [])
        XCTAssertEqual(durations, [.seconds(5), .seconds(5)])
    }

    @MainActor
    func testFlushAllCommitsEveryPendingHoldOnceBeforeItsTimerCompletes() async throws {
        var commits: [String: Int] = [:]
        let holds = DecisionHoldCoordinator { _ in
            try? await Task.sleep(for: .milliseconds(30))
        }
        holds.begin(.init(id: "first", label: "Not now", detail: "One")) {
            commits["first", default: 0] += 1
        }
        holds.begin(.init(id: "second", label: "Sent to your computer", detail: "Two")) {
            commits["second", default: 0] += 1
        }

        await holds.flushAll()
        XCTAssertEqual(commits, ["first": 1, "second": 1])
        XCTAssertTrue(holds.holds.isEmpty)
        try await Task.sleep(for: .milliseconds(50))

        XCTAssertEqual(commits, ["first": 1, "second": 1])
    }

    @MainActor
    func testFlushAllStartsEveryCommitBeforeWaitingAndEndsItsBackgroundTask() async {
        let allCommitsStarted = expectation(description: "all commits started")
        var started: Set<String> = []
        var firstCommitContinuation: CheckedContinuation<Void, Never>?
        var beginCount = 0
        var endedTasks: [UIBackgroundTaskIdentifier] = []
        let holds = DecisionHoldCoordinator(
            sleep: { _ in try await Task.sleep(for: .seconds(30)) },
            beginBackgroundTask: { _ in
                beginCount += 1
                return UIBackgroundTaskIdentifier(rawValue: 7)
            },
            endBackgroundTask: { endedTasks.append($0) }
        )
        for id in ["first", "second", "third"] {
            holds.begin(DecisionHoldCoordinator.Hold(id: id, label: "Not now", detail: id)) {
                started.insert(id)
                if started.count == 3 {
                    allCommitsStarted.fulfill()
                }
                if id == "first" {
                    await withCheckedContinuation { firstCommitContinuation = $0 }
                }
            }
        }

        let flush = Task { await holds.flushAll() }
        await fulfillment(of: [allCommitsStarted], timeout: 1)
        XCTAssertEqual(started, ["first", "second", "third"])
        XCTAssertTrue(holds.holds.isEmpty)
        XCTAssertEqual(beginCount, 1)
        XCTAssertTrue(endedTasks.isEmpty)

        firstCommitContinuation?.resume()
        await flush.value
        XCTAssertEqual(endedTasks, [UIBackgroundTaskIdentifier(rawValue: 7)])
    }

    @MainActor
    func testFlushAllExpirationEndsItsBackgroundTaskOnlyOnce() async {
        let commitStarted = expectation(description: "commit started")
        let taskID = UIBackgroundTaskIdentifier(rawValue: 9)
        var commitContinuation: CheckedContinuation<Void, Never>?
        var expirationHandler: (@MainActor () -> Void)?
        var endedTasks: [UIBackgroundTaskIdentifier] = []
        let holds = DecisionHoldCoordinator(
            sleep: { _ in try await Task.sleep(for: .seconds(30)) },
            beginBackgroundTask: {
                expirationHandler = $0
                return taskID
            },
            endBackgroundTask: { endedTasks.append($0) }
        )
        holds.begin(.init(id: "pending", label: "Not now", detail: "Pending")) {
            commitStarted.fulfill()
            await withCheckedContinuation { commitContinuation = $0 }
        }

        let flush = Task { await holds.flushAll() }
        await fulfillment(of: [commitStarted], timeout: 1)
        XCTAssertTrue(endedTasks.isEmpty)

        expirationHandler?()
        XCTAssertEqual(endedTasks, [taskID])
        commitContinuation?.resume()
        await flush.value
        XCTAssertEqual(endedTasks, [taskID])
    }

    @MainActor
    func testDecisionHoldsFlushOnlyWhenTheSceneEntersBackground() {
        XCTAssertFalse(DecisionHoldCoordinator.shouldFlush(when: .active))
        XCTAssertFalse(DecisionHoldCoordinator.shouldFlush(when: .inactive))
        XCTAssertTrue(DecisionHoldCoordinator.shouldFlush(when: .background))
    }

    @MainActor
    func testPairingCommitClearsTheOldTaskPageAndRepairState() async throws {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }

        let access = TasksStoreTestAccess()
        let page = MobileTasksPage(tasks: [task("old", status: "in_progress", group: "running")],
                                   counts: MobileTaskCounts(needsYou: 0, running: 1, doneThisWeek: 0))
        let fileURL = directory.appendingPathComponent("tasks.json")
        let store = TasksStore(hub: TasksStoreTestHub(page: page),
                               isPaired: { access.paired },
                               canRead: { access.canRead },
                               hubID: { access.hubID },
                               fileURL: fileURL)

        await store.refresh()
        XCTAssertEqual(store.page, page)
        XCTAssertTrue(FileManager.default.fileExists(atPath: fileURL.path))
        access.canRead = false
        await store.refresh()
        XCTAssertTrue(store.repairNeeded)

        access.hubID = "new-hub"
        access.canRead = true
        store.pairingCommitted()

        XCTAssertFalse(store.repairNeeded)
        XCTAssertNil(store.page)
        XCTAssertFalse(FileManager.default.fileExists(atPath: fileURL.path))
    }

    @MainActor
    func testPairingChangeClearsLoadedTaskDetail() async throws {
        let detail = MobileTaskDetail(
            id: "task-1", title: "Run tests", status: "in_progress", group: "running",
            blockedReason: nil, runner: nil, progress: nil,
            createdAt: "2026-10-26T09:12:00Z", updatedAt: "2026-10-26T09:20:00Z",
            startedAt: RelayTime.parse("2026-10-26T09:12:00Z"), steps: []
        )
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }

        let access = TasksStoreTestAccess()
        let store = TasksStore(hub: TasksStoreTestHub(
            page: MobileTasksPage(tasks: [], counts: MobileTaskCounts(needsYou: 0, running: 0, doneThisWeek: 0)),
            detail: detail
        ), isPaired: { access.paired }, canRead: { access.canRead },
            hubID: { access.hubID }, fileURL: directory.appendingPathComponent("tasks.json"))

        let loaded = await store.loadDetail(id: detail.id)
        XCTAssertEqual(loaded, .success(detail))
        access.hubID = "new-hub"
        store.pairingCommitted()

        XCTAssertNil(store.detail)
    }

    @MainActor
    func testTaskDetailOfflineFailureCanRetrySuccessfully() async {
        let detail = taskDetail("retry-task")
        let store = detailStore(responses: [.offline, .success(detail)])

        let first = await store.loadDetail(id: detail.id)
        XCTAssertEqual(first, .failure(.offline))
        XCTAssertEqual(store.detailLoadState, .failed(id: detail.id, .offline))

        let retry = await store.loadDetail(id: detail.id)
        XCTAssertEqual(retry, .success(detail))
        XCTAssertEqual(store.detailLoadState, .loaded(detail))
    }

    @MainActor
    func testTaskDetailNotFoundAndForbiddenErrorsAreMapped() async {
        let missingStore = detailStore(responses: [.notFound])
        let missing = await missingStore.loadDetail(id: "missing-task")
        XCTAssertEqual(missing, .failure(.unavailable))
        XCTAssertEqual(missingStore.detailLoadState, .failed(id: "missing-task", .unavailable))

        let forbiddenStore = detailStore(responses: [.forbidden])
        let forbidden = await forbiddenStore.loadDetail(id: "private-task")
        XCTAssertEqual(forbidden, .failure(.forbidden))
        XCTAssertEqual(forbiddenStore.detailLoadState, .failed(id: "private-task", .forbidden))
    }

    @MainActor
    func testLateTaskDetailSuccessCannotOverwriteNewerTask() async {
        let hub = TaskDetailControlledHub()
        let store = controlledDetailStore(hub)
        let detailA = taskDetail("task-a")
        let detailB = taskDetail("task-b")

        let requestA = Task { await store.loadDetail(id: detailA.id) }
        await hub.waitForRequest(detailA.id)
        let requestB = Task { await store.loadDetail(id: detailB.id) }
        await hub.waitForRequest(detailB.id)

        await hub.release(detailB.id, with: .success(detailB))
        _ = await requestB.value
        XCTAssertEqual(store.detailLoadState, .loaded(detailB))

        await hub.release(detailA.id, with: .success(detailA))
        _ = await requestA.value
        XCTAssertEqual(store.detailLoadState, .loaded(detailB))
    }

    @MainActor
    func testLateTaskDetailFailuresCannotOverwriteNewerTask() async {
        let errors: [Error] = [URLError(.notConnectedToInternet),
                               HubRequestError.rejected(status: 404, message: "Missing")]
        for error in errors {
            let hub = TaskDetailControlledHub()
            let store = controlledDetailStore(hub)
            let detailA = taskDetail("task-a")
            let detailB = taskDetail("task-b")

            let requestA = Task { await store.loadDetail(id: detailA.id) }
            await hub.waitForRequest(detailA.id)
            let requestB = Task { await store.loadDetail(id: detailB.id) }
            await hub.waitForRequest(detailB.id)

            await hub.release(detailB.id, with: .success(detailB))
            _ = await requestB.value
            XCTAssertEqual(store.detailLoadState, .loaded(detailB))

            await hub.release(detailA.id, with: .failure(error))
            _ = await requestA.value
            XCTAssertEqual(store.detailLoadState, .loaded(detailB))
        }
    }

    @MainActor
    func testTaskDetailStateFollowsCompletionOrderForCurrentRequest() async {
        let firstHub = TaskDetailControlledHub()
        let firstStore = controlledDetailStore(firstHub)
        let detailA = taskDetail("task-a")
        let detailB = taskDetail("task-b")
        let firstA = Task { await firstStore.loadDetail(id: detailA.id) }
        await firstHub.waitForRequest(detailA.id)
        let firstB = Task { await firstStore.loadDetail(id: detailB.id) }
        await firstHub.waitForRequest(detailB.id)

        await firstHub.release(detailB.id, with: .success(detailB))
        await firstHub.release(detailA.id, with: .success(detailA))
        _ = await firstA.value
        _ = await firstB.value
        XCTAssertEqual(firstStore.detailLoadState, .loaded(detailB))

        let secondHub = TaskDetailControlledHub()
        let secondStore = controlledDetailStore(secondHub)
        let secondA = Task { await secondStore.loadDetail(id: detailA.id) }
        await secondHub.waitForRequest(detailA.id)
        let secondB = Task { await secondStore.loadDetail(id: detailB.id) }
        await secondHub.waitForRequest(detailB.id)

        await secondHub.release(detailA.id, with: .success(detailA))
        _ = await secondA.value
        XCTAssertEqual(secondStore.detailLoadState, .loading(id: detailB.id))

        await secondHub.release(detailB.id, with: .success(detailB))
        _ = await secondB.value
        XCTAssertEqual(secondStore.detailLoadState, .loaded(detailB))
    }

    @MainActor
    func testCancelledTaskDetailRequestDoesNotAffectCurrentTask() async {
        let hub = TaskDetailControlledHub()
        let store = controlledDetailStore(hub)
        let detailA = taskDetail("task-a")
        let detailB = taskDetail("task-b")
        let requestA = Task { await store.loadDetail(id: detailA.id) }
        await hub.waitForRequest(detailA.id)
        let requestB = Task { await store.loadDetail(id: detailB.id) }
        await hub.waitForRequest(detailB.id)

        await hub.release(detailB.id, with: .success(detailB))
        _ = await requestB.value
        XCTAssertEqual(store.detailLoadState, .loaded(detailB))

        requestA.cancel()
        await hub.release(detailA.id, with: .success(detailA))
        let result = await requestA.value
        XCTAssertNil(result)
        XCTAssertEqual(store.detailLoadState, .loaded(detailB))
    }

    @MainActor
    func testPairingResetDropsLateTaskDetailResult() async {
        let hub = TaskDetailControlledHub()
        let store = controlledDetailStore(hub)
        let detail = taskDetail("task-a")
        let request = Task { await store.loadDetail(id: detail.id) }
        await hub.waitForRequest(detail.id)

        store.clearAfterUnpair()
        await hub.release(detail.id, with: .success(detail))
        let result = await request.value
        XCTAssertNil(result)
        XCTAssertEqual(store.detailLoadState, .idle)
    }

    @MainActor
    func testStopDecodesTaskViewResponseAndRefreshesSuccessfully() async throws {
        let response = Data("""
        {"id":"task-1","title":"Run tests","status":"stopped","group":"stopped",
         "createdAt":"2026-10-26T09:12:00Z","updatedAt":"2026-10-26T09:20:00Z"}
        """.utf8)
        let stopped = try JSONDecoder().decode(MobileTask.self, from: response)
        let page = MobileTasksPage(tasks: [stopped],
                                   counts: MobileTaskCounts(needsYou: 0, running: 0, doneThisWeek: 0))
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let access = TasksStoreTestAccess()
        let store = TasksStore(hub: TasksStoreTestHub(page: page, stoppedTask: stopped),
                               isPaired: { access.paired },
                               canRead: { access.canRead },
                               guardian: LocalDecisionGuard(),
                               hubID: { access.hubID },
                               fileURL: directory.appendingPathComponent("tasks.json"))

        XCTAssertTrue(store.canStop)
        let stoppedSuccessfully = await store.stop(id: "task-1")
        XCTAssertTrue(stoppedSuccessfully)
        XCTAssertNil(store.detail)
        XCTAssertEqual(store.page, page)
    }

    private func task(_ id: String, status: String, group: String, updated: String = "2026-10-26T12:00:00Z",
                      blockedReason: String? = nil) -> MobileTask {
        MobileTask(id: id, title: id, status: status, group: group, blockedReason: blockedReason,
                   runner: nil, progress: nil, createdAt: updated, updatedAt: updated)
    }

    private func taskDetail(_ id: String) -> MobileTaskDetail {
        MobileTaskDetail(id: id, title: "Run tests", status: "in_progress", group: "running",
                         blockedReason: nil, runner: nil, progress: nil,
                         createdAt: "2026-10-26T09:12:00Z", updatedAt: "2026-10-26T09:20:00Z",
                         startedAt: RelayTime.parse("2026-10-26T09:12:00Z"), steps: [])
    }

    @MainActor
    private func detailStore(responses: [TaskDetailResponse]) -> TasksStore {
        let page = MobileTasksPage(tasks: [], counts: MobileTaskCounts(needsYou: 0, running: 0, doneThisWeek: 0))
        return TasksStore(hub: TaskDetailSequenceHub(page: page, responses: responses),
                          isPaired: { true }, hubID: { "test-hub" })
    }

    @MainActor
    private func controlledDetailStore(_ hub: TaskDetailControlledHub) -> TasksStore {
        TasksStore(hub: hub, isPaired: { true }, hubID: { "test-hub" })
    }

    private func offer(_ id: String, title: String? = nil, version: String? = nil) -> IdeaOffer {
        IdeaOffer(id: id, kind: "suggestion", title: title ?? id, why: "", prompt: "", version: version)
    }
}

@MainActor
private final class TasksStoreTestAccess {
    var paired = true
    var canRead = true
    var hubID: String? = "old-hub"
}

private struct TasksStoreTestHub: TasksHubAPI {
    let page: MobileTasksPage
    var stoppedTask: MobileTask? = nil
    var detail: MobileTaskDetail? = nil
    func tasks() async throws -> MobileTasksPage { page }
    func task(id: String) async throws -> MobileTaskDetail {
        guard let detail, detail.id == id else { throw HubRequestError.invalidIdentifier }
        return detail
    }
    func stopTask(id: String, request: MobileTaskStopRequest) async throws -> MobileTask {
        guard let stoppedTask else { throw HubRequestError.invalidIdentifier }
        return stoppedTask
    }
}

private actor CompletedWaitingTaskHub: TasksHubAPI {
    let page: MobileTasksPage
    var reads = 0
    init(page: MobileTasksPage) { self.page = page }
    func tasks() async throws -> MobileTasksPage {
        reads += 1
        if reads > 1 { throw HubRequestError.noAnswer }
        return page
    }
    func task(id: String) async throws -> MobileTaskDetail { throw HubRequestError.noAnswer }
    func stopTask(id: String, request: MobileTaskStopRequest) async throws -> MobileTask { throw HubRequestError.noAnswer }
    func actOnTask(id: String, request: MobileTaskActionRequest) async throws -> MobileTaskDetail {
        MobileTaskDetail(id: id, title: id, status: "completed", group: "done", blockedReason: nil,
                         runner: nil, progress: nil, createdAt: "2026-10-26T12:00:00Z",
                         updatedAt: "2026-10-26T12:01:00Z", startedAt: nil, steps: [])
    }
}

private actor SlowRefreshAfterSendHub: TasksHubAPI {
    func tasks() async throws -> MobileTasksPage {
        try await Task.sleep(for: .seconds(30))
        throw HubRequestError.noAnswer
    }
    func task(id: String) async throws -> MobileTaskDetail { throw HubRequestError.noAnswer }
    func stopTask(id: String, request: MobileTaskStopRequest) async throws -> MobileTask { throw HubRequestError.noAnswer }
    func actOnTask(id: String, request: MobileTaskActionRequest) async throws -> MobileTaskDetail {
        MobileTaskDetail(id: id, title: id, status: "completed", group: "done", blockedReason: nil,
                         runner: nil, progress: nil, createdAt: "2026-10-26T12:00:00Z",
                         updatedAt: "2026-10-26T12:01:00Z", startedAt: nil, steps: [])
    }
}

private enum TaskDetailResponse: Sendable {
    case offline
    case notFound
    case forbidden
    case success(MobileTaskDetail)
}

private actor TaskDetailSequenceHub: TasksHubAPI {
    let page: MobileTasksPage
    var responses: [TaskDetailResponse]

    init(page: MobileTasksPage, responses: [TaskDetailResponse]) {
        self.page = page
        self.responses = responses
    }

    func tasks() async throws -> MobileTasksPage { page }

    func task(id: String) async throws -> MobileTaskDetail {
        guard !responses.isEmpty else { throw HubRequestError.noAnswer }
        switch responses.removeFirst() {
        case .offline:
            throw URLError(.notConnectedToInternet)
        case .notFound:
            throw HubRequestError.rejected(status: 404, message: "Missing")
        case .forbidden:
            throw HubRequestError.forbidden
        case .success(let detail):
            return detail
        }
    }

    func stopTask(id: String, request: MobileTaskStopRequest) async throws -> MobileTask {
        throw HubRequestError.invalidIdentifier
    }
}

private actor TaskDetailControlledHub: TasksHubAPI {
    private var requests: [String: Int] = [:]
    private var pending: [String: [CheckedContinuation<MobileTaskDetail, Error>]] = [:]
    private var requestWaiters: [String: [(count: Int, continuation: CheckedContinuation<Void, Never>)]] = [:]

    func tasks() async throws -> MobileTasksPage {
        MobileTasksPage(tasks: [], counts: MobileTaskCounts(needsYou: 0, running: 0, doneThisWeek: 0))
    }

    func task(id: String) async throws -> MobileTaskDetail {
        requests[id, default: 0] += 1
        let count = requests[id] ?? 0
        return try await withCheckedThrowingContinuation { continuation in
            pending[id, default: []].append(continuation)
            let waiters = requestWaiters[id, default: []]
            requestWaiters[id] = waiters.filter { waiter in
                guard waiter.count <= count else { return true }
                waiter.continuation.resume()
                return false
            }
        }
    }

    func waitForRequest(_ id: String, count: Int = 1) async {
        guard requests[id, default: 0] < count else { return }
        await withCheckedContinuation { continuation in
            requestWaiters[id, default: []].append((count, continuation))
        }
    }

    func release(_ id: String, with result: Result<MobileTaskDetail, Error>) {
        guard var continuations = pending[id], !continuations.isEmpty else { return }
        let continuation = continuations.removeFirst()
        pending[id] = continuations.isEmpty ? nil : continuations
        continuation.resume(with: result)
    }

    func stopTask(id: String, request: MobileTaskStopRequest) async throws -> MobileTask {
        throw HubRequestError.invalidIdentifier
    }
}
