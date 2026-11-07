import XCTest
@testable import Yansu

final class ChatDeliveryTests: XCTestCase {
    private let start = Date(timeIntervalSince1970: 1_800_000_000)

    private func message(_ id: String, _ text: String, after: Int?, state: ChatDeliveryState = .sending,
                         seq: Int? = nil, turnID: String? = nil, second: TimeInterval = 0) -> ChatOutgoingMessage {
        ChatOutgoingMessage(id: id, text: text, after: after, sentAt: start.addingTimeInterval(second),
                            turnID: turnID, seq: seq, state: state)
    }

    private func row(_ seq: Int, _ role: ChatRole, _ text: String = "", kind: ChatItemKind = .text) -> ChatItemView {
        ChatItemView(seq: seq, role: role, kind: kind, text: text, toolName: "", at: "", permission: nil)
    }

    func testAWaitingRowSaysItWaitsForTheComputer() {
        XCTAssertEqual(ChatDeliveryStatus.waitingForComputer.label, "Waits for your computer")
    }

    /// A waiting message a day old is one the relay has let go; a fresh one, or one the computer took, is not.
    func testAWaitingMessageExpiresAfterADayAndOnlyThen() {
        let waiting = message("m", "Can you move my 3 PM?", after: 0)
        let dayLater = start.addingTimeInterval(ChatOutgoingMessage.relayRetention)
        XCTAssertFalse(waiting.expiredWhileWaiting(computerOnline: false, now: start.addingTimeInterval(3600)))
        XCTAssertTrue(waiting.expiredWhileWaiting(computerOnline: false, now: dayLater))
        let taken = message("t", "Sent", after: 0, state: .sent, seq: 4)
        XCTAssertFalse(taken.expiredWhileWaiting(computerOnline: false, now: dayLater), "a message the computer took is not waiting")
        XCTAssertFalse(waiting.expiredWhileWaiting(computerOnline: true, now: dayLater), "with the computer online it is sending, not waiting")
    }

    func testStatesOnlyMoveForward() {
        XCTAssertEqual(ChatDeliveryState.sending.advanced(to: .sent), .sent)
        XCTAssertEqual(ChatDeliveryState.seen.advanced(to: .sent), .seen)
        XCTAssertEqual(ChatDeliveryState.sent.advanced(to: .sending), .sent)
        XCTAssertEqual([ChatDeliveryState.sending, .sent, .seen].map(\.label),
                       ["Sending", "Sent to your computer", "Seen by your computer"])
    }

    func testDeliveryStatusDistinguishesOfflineWaitingFromComputerReceipts() {
        var outgoing = message("m", "Hello", after: 0)
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: true), .sending)
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: false), .waitingForComputer)
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: nil), .sending,
                       "a computer whose state is not known yet is not called away")
        outgoing.deliveryIssue = .waitingForTurn
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: nil), .waitingForTurn)
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: true), .waitingForTurn)
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: false), .waitingForComputer,
                       "an old running header must not imply an answer is coming while offline")
        outgoing.deliveryIssue = .waitingForComputer
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: true), .waitingForComputer,
                       "presence alone does not acknowledge the queued message")
        outgoing.state = .sent
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: false), .sent)
        outgoing.state = .seen
        XCTAssertEqual(outgoing.deliveryStatus(computerOnline: false), .seen)
        XCTAssertEqual(ChatDeliveryStatus.waitingForComputer.label, "Waits for your computer")
        XCTAssertEqual(ChatDeliveryStatus.waitingForComputer.explanation,
                       "This phone keeps trying automatically. You do not need to send this message again.")
    }

    func testFailedDeliveryStaysVisibleWithoutBecomingAQueuedOrSeenMessage() {
        var failed = message("failed", "Keep this text", after: 0)
        failed.deliveryIssue = .failed
        failed.returnedToDraft = true
        XCTAssertEqual(failed.deliveryStatus(computerOnline: false), .failed)
        for online in [true, false, nil] as [Bool?] {
            XCTAssertEqual(failed.deliveryStatus(computerOnline: online).label, "Not sent",
                           "a refusal is never shown as waiting for the computer")
        }
        XCTAssertFalse(failed.isQueued)
        XCTAssertEqual(ChatDelivery.drawnBelowRows([failed], rows: []).map(\.id), [failed.id])
        failed.failureDraftRecovered = true
        XCTAssertFalse(failed.isQueued, "recovering its draft cannot turn a failure receipt into a retry")
        XCTAssertEqual(ChatDelivery.reconcile([failed], items: [row(1, .user, failed.text), row(2, .assistant)],
                                              pendingPermission: nil), [failed])
    }

    func testDeliveryIssuesSurviveRelaunchAndOlderSavedMessagesStillDecode() throws {
        for issue in [ChatDeliveryIssue.waitingForComputer, .waitingForTurn, .failed] {
            var outgoing = message("m", "Hello", after: 0)
            outgoing.deliveryIssue = issue
            let data = try JSONEncoder().encode(outgoing)
            XCTAssertEqual(try JSONDecoder().decode(ChatOutgoingMessage.self, from: data), outgoing)
            var legacy = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
            legacy.removeValue(forKey: "deliveryIssue")
            let restored = try JSONDecoder().decode(ChatOutgoingMessage.self,
                                                    from: JSONSerialization.data(withJSONObject: legacy))
            XCTAssertNil(restored.deliveryIssue)
            XCTAssertTrue(restored.isQueued)
        }
    }

    func testCachedRunningHeaderDoesNotClaimTheOfflineComputerIsWorking() {
        XCTAssertTrue(ChatView.showsRunningState(.running, computerOnline: true))
        XCTAssertFalse(ChatView.showsRunningState(.running, computerOnline: false))
        XCTAssertFalse(ChatView.showsRunningState(.running, computerOnline: nil))
        XCTAssertFalse(ChatView.showsRunningState(.idle, computerOnline: true))
    }

    func testAnAnsweredMessageIsTiedToTheFirstMatchingItemAfterWhatWasOnScreen() {
        let items = [row(4, .user, "ok"), row(5, .assistant, "Done"), row(6, .user, "ok")]

        let result = ChatDelivery.reconcile([message("m", "ok", after: 5, state: .sent, turnID: "t")], items: items,
                                            pendingPermission: nil, answeredBefore: ["m"])

        XCTAssertEqual(result.first?.seq, 6, "the older ok was already on screen when this one was sent")
        XCTAssertEqual(result.first?.state, .sent)
    }

    func testAMessageTheMacHasNotAnsweredIsNeverTiedByItsText() {
        // The ok after it may be one typed on the Mac; only the Mac's answer says which row is this message's.
        let items = [row(4, .user, "ok"), row(5, .assistant, "Done"), row(6, .user, "ok")]

        let result = ChatDelivery.reconcile([message("m", "ok", after: 5)], items: items, pendingPermission: nil,
                                            answeredBefore: ["m"])

        XCTAssertNil(result.first?.seq)
        XCTAssertEqual(result.first?.state, .sending)
    }

    func testAnOldMacsAnswerTiesByTextOnlyInAReadThatStartedAfterIt() {
        let items = [row(4, .assistant, "Done"), row(5, .user, "ok")]
        let answered = [message("m", "ok", after: 4, state: .sent, turnID: "t")]

        let racing = ChatDelivery.reconcile(answered, items: items, pendingPermission: nil, answeredBefore: [])
        XCTAssertNil(racing.first?.seq, "a read already on its way when the 202 came need not hold the row")

        let later = ChatDelivery.reconcile(answered, items: items, pendingPermission: nil, answeredBefore: ["m"])
        XCTAssertEqual(later.first?.seq, 5)
    }

    func testARowOlderThanThePageIsPlacedBeforeItAndSeen() {
        let page = (40...49).map { row($0, .assistant, "Step \($0)") }
        let answered = [message("m", "Run the checks", after: 1, state: .sent, turnID: "t")]

        let result = ChatDelivery.reconcile(answered, items: page, pendingPermission: nil, answeredBefore: ["m"])

        XCTAssertEqual(result.first?.seq, 39)
        XCTAssertEqual(result.first?.state, .seen)

        // A page that reaches back to what was on screen would hold the row; without it nothing is guessed.
        let whole = (2...9).map { row($0, .assistant, "Step \($0)") }
        let unmatched = ChatDelivery.reconcile(answered, items: whole, pendingPermission: nil, answeredBefore: ["m"])
        XCTAssertNil(unmatched.first?.seq)
    }

    func testAPossibleRowIsOneWithTheTextAfterWhatWasOnScreenThatNothingElseClaims() {
        let items = [row(2, .user, "yes"), row(3, .assistant, "Done"), row(4, .user, "yes"), row(5, .user, "yes")]
        let claimedByOther = message("a", "yes", after: 1, state: .sent, seq: 4, turnID: "t")
        let queued = message("b", "yes", after: 3, second: 1)

        XCTAssertEqual(ChatDelivery.possibleRow(for: queued, in: [claimedByOther, queued], items: items, ruledOut: []), 5)
        XCTAssertNil(ChatDelivery.possibleRow(for: queued, in: [claimedByOther, queued], items: items, ruledOut: [5]))
        XCTAssertNil(ChatDelivery.possibleRow(for: message("c", "yes", after: nil), in: [], items: items, ruledOut: []),
                     "without knowing what was on screen, any old yes could match")
    }

    func testWithoutAnEarlierReadOnlyTheMacsAnswerTiesTheMessageToTheNewestMatch() {
        let items = [row(4, .user, "ok"), row(5, .assistant, "Done"), row(6, .user, "ok")]

        let waiting = ChatDelivery.reconcile([message("m", "ok", after: nil)], items: items, pendingPermission: nil)
        XCTAssertNil(waiting.first?.seq, "an old message with the same text must not count as this one")
        XCTAssertEqual(waiting.first?.state, .sending)

        let accepted = ChatDelivery.reconcile([message("m", "ok", after: nil, state: .sent, turnID: "t")],
                                              items: items, pendingPermission: nil, answeredBefore: ["m"])
        XCTAssertEqual(accepted.first?.seq, 6)
    }

    func testTwoMessagesWithTheSameTextClaimDifferentItems() {
        let items = [row(2, .user, "ok"), row(3, .assistant, "Done"), row(4, .user, "ok")]
        let messages = [message("a", "ok", after: 1, state: .sent, turnID: "t1"),
                        message("b", "ok", after: 1, state: .sent, turnID: "t2", second: 1)]

        let result = ChatDelivery.reconcile(messages, items: items, pendingPermission: nil, answeredBefore: ["a", "b"])

        XCTAssertEqual(result.map(\.seq), [2, 4])
    }

    func testAShortenedCopyStillMatchesTheMessage() {
        let text = String(repeating: "a", count: 5000)
        let shortened = String(repeating: "a", count: 4084) + " [truncated]"

        XCTAssertTrue(ChatDelivery.matches(row(1, .user, shortened), text: text))
        XCTAssertFalse(ChatDelivery.matches(row(1, .user, shortened), text: "b" + text))
        XCTAssertFalse(ChatDelivery.matches(row(1, .assistant, text), text: text))
    }

    func testWhatCountsAsTheMacAnswering() {
        let sent = [message("m", "Hi", after: 0, state: .sent, seq: 1)]

        let status = ChatDelivery.reconcile(sent, items: [row(1, .user, "Hi"), row(2, .system, "Report")],
                                            pendingPermission: nil)
        XCTAssertEqual(status.first?.state, .sent, "a status line is not an answer")

        for answer in [row(2, .assistant, "Hello"), row(2, .tool, "Read file", kind: .toolUse),
                       row(2, .system, "Failed", kind: .error), row(2, .user, "Typed on the Mac")] {
            let result = ChatDelivery.reconcile(sent, items: [row(1, .user, "Hi"), answer], pendingPermission: nil)
            XCTAssertEqual(result.first?.state, .seen, "\(answer.role) \(answer.kind)")
        }

        let permission = ChatItemView(seq: 2, role: .system, kind: .permissionRequest, text: "Allow?", toolName: "",
                                      at: "", permission: nil)
        let waiting = ChatDelivery.reconcile(sent, items: [row(1, .user, "Hi")], pendingPermission: permission)
        XCTAssertEqual(waiting.first?.state, .seen)
    }

    func testATurnEventMarksItsMessageAndEveryEarlierStoredOneSeen() {
        let messages = [message("a", "One", after: 0, state: .sent, seq: 1, turnID: "t1"),
                        message("b", "Two", after: 0, state: .sending, second: 1),
                        message("c", "Three", after: 1, state: .sent, seq: 3, turnID: "t3", second: 2)]

        let result = ChatDelivery.markSeen(messages, turnID: "t3")

        XCTAssertEqual(result.map(\.state), [.seen, .sending, .seen],
                       "a turn runs after the earlier ones; one still on its way is not seen")
        XCTAssertEqual(ChatDelivery.markSeen(messages, turnID: "").map(\.state), messages.map(\.state))
    }

    func testTheEmptyChatCardGoesOnceAMessageIsOnItsWay() {
        let empty = OpenChat(header: ChatHeaderView(id: "c", title: "", state: .idle), items: [])

        XCTAssertTrue(ChatView.showsEmptyChat(empty, outgoing: []))
        XCTAssertFalse(ChatView.showsEmptyChat(empty, outgoing: [message("m", "Hello", after: 0)]),
                       "a Sending message must not sit under \"No messages yet\"")
    }

    func testASentMessageOlderThanTheNewestPageStaysDrawnUntilTheMacSeesIt() {
        let page = (51...100).map { row($0, .system, "Status \($0)") }
        let sent = [message("m", "Run the checks", after: 1, state: .sent, seq: 2, turnID: "t")]

        let waiting = ChatDelivery.reconcile(sent, items: page, pendingPermission: nil)

        XCTAssertEqual(page.count, ChatCache.itemLimit)
        XCTAssertEqual(waiting.first?.state, .sent, "system statuses do not show the Mac answering")
        XCTAssertEqual(ChatDelivery.drawnBelowRows(waiting, rows: page).map(\.id), ["m"],
                       "a Sent message must stay visible even when its row fell out of the newest page")

        let answered = ChatDelivery.reconcile(waiting, items: page + [row(101, .assistant, "Done")],
                                              pendingPermission: nil)
        XCTAssertEqual(answered.first?.state, .seen)
        XCTAssertTrue(ChatDelivery.drawnBelowRows(answered, rows: page).isEmpty,
                      "once seen, a message older than the page is history")
    }

    func testAMessageNewerThanTheRowsIsDrawnEvenBeyondTheLoadedPage() {
        let page = (51...100).map { row($0, .assistant, "Step \($0)") }
        let sent = message("sent", "Next task", after: 100, state: .sent, seq: 151, turnID: "t1")
        let seen = message("seen", "Another task", after: 100, state: .seen, seq: 153, turnID: "t2", second: 1)

        XCTAssertEqual(ChatDelivery.drawnBelowRows([sent, seen], rows: page).map(\.id), ["sent", "seen"],
                       "a seq farther away than a page still belongs below these older rows")
        XCTAssertTrue(ChatDelivery.drawnBelowRows([sent], rows: page + [row(151, .user, "Next task")]).isEmpty,
                      "the stored row supplies its bubble once it arrives")
    }

    func testATurnEventKeepsAnOlderOutgoingBubbleUntilAReadBringsItsRow() {
        let page = [row(1, .user, "Hi"), row(2, .assistant, "Hello")]
        let messages = [message("a", "First task", after: 2, state: .sent, seq: 3, turnID: "t1"),
                        message("b", "Second task", after: 2, second: 1)]

        let seen = ChatDelivery.markSeen(messages, turnID: "t1")
        let waitingForRead = ChatDelivery.pruned(seen, rows: page)

        XCTAssertEqual(waitingForRead.map(\.state), [.seen, .sending])
        XCTAssertEqual(ChatDelivery.drawnBelowRows(waitingForRead, rows: page).map(\.id), ["a", "b"],
                       "the event must not erase the first bubble while its row is still off screen")
        XCTAssertEqual(ChatDelivery.pruned(seen, rows: nil).map(\.id), ["a", "b"],
                       "a chat without a loaded transcript keeps the same evidence until a read")

        let withRow = page + [row(3, .user, "First task")]
        XCTAssertEqual(ChatDelivery.pruned(waitingForRead, rows: withRow).map(\.id), ["b"],
                       "the older Seen label can be dropped after its stored row supplies the bubble")
    }

    func testOnlyMessagesStillOnTheirWayAndTheNewestKeepALabel() {
        let messages = [message("a", "One", after: 0, state: .seen, seq: 1),
                        message("b", "Two", after: 1, state: .sent, seq: 3, second: 1),
                        message("c", "Three", after: 3, state: .seen, seq: 5, second: 2)]
        let rows = [row(1, .user, "One"), row(3, .user, "Two"), row(5, .user, "Three")]

        XCTAssertEqual(ChatDelivery.pruned(messages, rows: rows).map(\.id), ["b", "c"])
    }

    func testReturnedDraftsAreKeptWithoutBeingQueuedDrawnOrReconciled() {
        let newest = message("sent", "Delivered", after: 0, state: .seen, seq: 1, turnID: "t1")
        var refused = message("draft", "Try this later", after: 1, second: 1)
        refused.returnedToDraft = true
        let messages = [newest, refused]

        XCTAssertFalse(refused.isQueued)
        XCTAssertEqual(ChatDelivery.drawnBelowRows(messages, rows: nil).map(\.id), ["sent"])
        XCTAssertTrue(ChatDelivery.drawnBelowRows(messages, rows: [row(1, .user, "Delivered")]).isEmpty)
        XCTAssertEqual(ChatDelivery.pruned(messages, rows: [row(1, .user, "Delivered")]).map(\.id),
                       ["sent", "draft"], "a draft does not replace the newest delivered message's label")

        // Draft records never participate in delivery transitions, even if they retain earlier receipt fields.
        refused.turnID = "old-turn"
        refused.seq = 2
        refused.state = .sent
        let later = message("later", "After the draft", after: 2, state: .seen, seq: 3, turnID: "t3", second: 2)
        XCTAssertEqual(ChatDelivery.markSeen([refused, later], turnID: "old-turn").first, refused)
        XCTAssertEqual(ChatDelivery.reconcile([refused, later], items: [row(3, .assistant, "Done")],
                                              pendingPermission: nil).first, refused)
    }
}

private struct LegacyQueueOwnerCheck: DecisionGuarding {
    func authorize(reason: String) async throws {}
}

private actor LegacyQueueHub: ChatHubAPI, ActionHubAPI {
    struct Send: Equatable {
        let chatID: String
        let text: String
        let key: String
    }

    private var headers: [String: ChatHeaderView] = [:]
    private var sendFailure: Error?
    private var sends: [Send] = []

    func seed(_ id: String, state: ChatState = .idle) {
        headers[id] = ChatHeaderView(id: id, title: "Research", state: state)
    }

    func failSend(_ error: Error?) { sendFailure = error }
    func sent() -> [Send] { sends }

    func chats(limit: Int) async throws -> [ChatSummaryView] {
        // The hidden chat does not need to appear on the current, limited chat-list page.
        [ChatSummaryView(id: "main", title: "Chat", updatedAt: "", state: .idle, kind: .main)]
    }

    func chat(id: String, after: Int?, limit: Int) async throws -> ChatDetail {
        guard let header = headers[id] else { throw HubRequestError.rejected(status: 404, message: "Gone") }
        return ChatDetail(chat: header, items: [], pendingPermission: nil, cursor: 0, hasMore: false)
    }

    func sendMessage(chatID: String, text: String, idempotencyKey: String) async throws -> ChatMessageAccepted {
        sends.append(Send(chatID: chatID, text: text, key: idempotencyKey))
        if let sendFailure { throw sendFailure }
        return ChatMessageAccepted(turnId: "turn-\(sends.count)")
    }

    func createChat(title: String, idempotencyKey: String) async throws -> String { "unused" }
    func answerPermission(chatID: String, requestID: String, optionID: String, idempotencyKey: String) async throws -> Bool { false }
    func cancel(chatID: String, idempotencyKey: String) async throws -> Bool { false }
    func pollChatTurns() async throws -> [MobileChatTurnEvent] { [] }
    func rejectAttribution(memoryID: String, reason: String, idempotencyKey: String) async throws -> Bool { false }
    func startOneTimeWork(_ request: StartOneTimeWorkRequest) async throws -> String { "unused" }
}

/// Messages an older build queued in a chat the phone no longer shows: since 2026-10-16 it has only the chat.
@MainActor
final class LegacyChatQueueTests: XCTestCase {
    private struct Harness {
        let hub: LegacyQueueHub
        let cache: ChatCache
        let journal: PendingWriteJournal
        let journalURL: URL
        let store: ChatStore
    }

    private let instant = Date(timeIntervalSince1970: 1_800_000_000)

    private func makeHarness() async throws -> Harness {
        let directory = try makeTemporaryDirectory()
        let cache = ChatCache(directory: directory.appendingPathComponent("chats"))
        try cache.rememberMainChat(ChatSummaryView(id: "main", title: "Chat", updatedAt: "", state: .idle, kind: .main))
        try cache.save(ChatHeaderView(id: "main", title: "Chat", state: .idle), items: [])
        try cache.save(ChatHeaderView(id: "hidden", title: "Research", state: .idle), items: [])
        let url = directory.appendingPathComponent("desk-writes.json")
        let journal = PendingWriteJournal(fileURL: url)
        let hub = LegacyQueueHub()
        let store = makeStore(hub: hub, journal: journal, cache: cache)
        store.prepareMainChat()
        return Harness(hub: hub, cache: cache, journal: journal, journalURL: url, store: store)
    }

    private func makeStore(hub: LegacyQueueHub, journal: PendingWriteJournal, cache: ChatCache) -> ChatStore {
        ChatStore(hub: hub, journal: journal, guardian: LegacyQueueOwnerCheck(), presence: { true },
                  isPaired: { true }, cache: cache, makeKey: { "unexpected-new-key" })
    }

    /// A message the first build queued: keyed by its text alone, and two days old, past the journal's usual day.
    private func queue(_ text: String, in h: Harness) async throws {
        let message = ChatOutgoingMessage(id: "old-message", text: text, after: 0, sentAt: instant, state: .sending)
        try h.cache.saveOutgoing((h.cache.loadOutgoing(chatID: "hidden") ?? []) + [message], chatID: "hidden")
        try await h.journal.insert(PendingWrite(key: "original-key", operation: .chatMessage, target: "hidden",
                                                choice: "message", fingerprint: WriteFingerprint.make([text]),
                                                createdAt: instant.addingTimeInterval(-172_800)))
    }

    func testAQueuedMessageKeepsItsKeyPastPruningAndGoesAtLaunch() async throws {
        let h = try await makeHarness()
        try await queue("Research this", in: h)
        await h.hub.seed("hidden")
        let keys = await h.store.queuedMessageKeys()
        XCTAssertEqual(keys, ["original-key"], "the key of a message still waiting outlives the journal's day")
        let instant = instant
        let actions = ActionStore(hub: h.hub, journal: h.journal, guardian: LegacyQueueOwnerCheck(),
                                  isPaired: { true }, now: { instant })
        await actions.pruneJournal(keepingKeys: keys)

        h.store.resumeQueuedMessages()
        var sends = await h.hub.sent()
        for _ in 0..<200 where sends.isEmpty {
            try await Task.sleep(nanoseconds: 10_000_000)
            sends = await h.hub.sent()
        }

        XCTAssertEqual(sends, [LegacyQueueHub.Send(chatID: "hidden", text: "Research this", key: "original-key")],
                       "a retry gets the computer's first answer instead of starting a second turn")
        XCTAssertEqual(h.store.openChatID, "main")
    }

    func testTextRefusedInAHiddenChatComesToTheMainComposerUnderThatChatsName() async throws {
        let h = try await makeHarness()
        try await queue("Original message", in: h)
        await h.hub.seed("hidden")
        await h.hub.failSend(HubRequestError.rejected(status: 400, message: "Not accepted"))

        h.store.resumeQueuedMessages()
        for _ in 0..<200 where h.store.returnedDrafts.isEmpty {
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertEqual(h.store.returnedDrafts.map(\.chatID), ["hidden"])
        XCTAssertEqual(h.store.takeReturnedDrafts(in: "hidden-elsewhere", appendingTo: "Kept"), "Kept",
                       "only the chat's composer takes another chat's text")

        XCTAssertEqual(h.store.takeReturnedDrafts(in: "main", appendingTo: "Typed since"),
                       "Typed since\nUnsent message for chat \"Research\":\nOriginal message")
        XCTAssertTrue(h.store.returnedDrafts.isEmpty)
        let relaunched = makeStore(hub: h.hub, journal: PendingWriteJournal(fileURL: h.journalURL), cache: h.cache)
        relaunched.prepareMainChat()
        relaunched.resumeQueuedMessages()
        XCTAssertEqual(relaunched.takeReturnedDrafts(in: "main", appendingTo: ""), "", "the text is taken once")
    }
}
