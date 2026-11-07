import Foundation
import XCTest
@testable import Yansu

private struct BriefConversationGuard: DecisionGuarding {
    func authorize(reason: String) async throws {}
}

private actor BriefConversationHub: ChatHubAPI {
    /// A test or seed computer takes a send to its main chat (PROTOCOL.md 15.5a).
    func supportsMainChatSend() async -> Bool { true }
    struct Sent: Equatable {
        let chatID: String
        let text: String
        let reference: ChatBriefReference
        let key: String
    }
    var sent: [Sent] = []
    var fails = false
    var ordinarySends = 0
    private var holdList = false
    private var listContinuation: CheckedContinuation<Void, Never>?
    private var listWaiters: [CheckedContinuation<Void, Never>] = []
    func holdDiscovery() { holdList = true }
    func waitForDiscovery() async {
        if listContinuation != nil { return }
        await withCheckedContinuation { listWaiters.append($0) }
    }
    func releaseDiscovery() {
        listContinuation?.resume()
        listContinuation = nil
        holdList = false
    }
    func setFailure(_ value: Bool) { fails = value }
    func chats(limit: Int) async throws -> [ChatSummaryView] {
        if holdList {
            await withCheckedContinuation { continuation in
                listContinuation = continuation
                listWaiters.forEach { $0.resume() }
                listWaiters.removeAll()
            }
        }
        return [ChatSummaryView(id: "main", title: "Isoform", updatedAt: "", state: .idle, kind: .main)]
    }
    func chat(id: String, after: Int?, limit: Int) async throws -> ChatDetail {
        ChatDetail(chat: ChatHeaderView(id: id, title: "Isoform", state: .idle), items: [], pendingPermission: nil,
                   cursor: 0, hasMore: false)
    }
    func createChat(title: String, idempotencyKey: String) async throws -> String { "unused" }
    var ordinaryTexts: [String] = []
    var ordinaryAccepts = false
    func setOrdinaryAccepts(_ value: Bool) { ordinaryAccepts = value }
    func sendMessage(chatID: String, text: String, idempotencyKey: String) async throws -> ChatMessageAccepted {
        ordinarySends += 1
        ordinaryTexts.append(text)
        guard ordinaryAccepts else { throw HubRequestError.noAnswer }
        return ChatMessageAccepted(turnId: "plain-\(ordinarySends)", seq: ordinarySends)
    }
    func sendBriefMessage(chatID: String, text: String, reference: ChatBriefReference,
                          idempotencyKey: String) async throws -> ChatMessageAccepted {
        sent.append(Sent(chatID: chatID, text: text, reference: reference, key: idempotencyKey))
        if fails { throw HubRequestError.conflict(message: "This brief changed. Refresh and try again.") }
        return ChatMessageAccepted(turnId: "turn", seq: 1)
    }
    var conversationAsked: [String] = []
    var conversationFailure: HubRequestError?
    func setConversationFailure(_ value: HubRequestError?) { conversationFailure = value }
    func briefConversation(briefID: String) async throws -> BriefConversation {
        conversationAsked.append(briefID)
        if let conversationFailure { throw conversationFailure }
        return BriefConversation(briefId: briefID, briefVersion: "revision-1", chatId: "main")
    }
    var opened: [Sent] = []
    var openFailure: HubRequestError?
    func setOpenFailure(_ value: HubRequestError?) { openFailure = value }
    func openBrief(chatID: String, reference: ChatBriefReference, idempotencyKey: String) async throws {
        opened.append(Sent(chatID: chatID, text: "", reference: reference, key: idempotencyKey))
        if let openFailure { throw openFailure }
    }
    struct ParagraphOpen: Equatable {
        let chatID: String
        let reference: ChatBriefReference
        let paragraph: Int
        let key: String
    }
    var openedParagraphs: [ParagraphOpen] = []
    var paragraphFailure: HubRequestError?
    private var holdParagraphRequest = false
    private var paragraphContinuation: CheckedContinuation<Void, Never>?
    private var paragraphWaiters: [CheckedContinuation<Void, Never>] = []
    func setParagraphFailure(_ value: HubRequestError?) { paragraphFailure = value }
    func holdParagraph() { holdParagraphRequest = true }
    func waitForParagraph() async {
        if paragraphContinuation != nil { return }
        await withCheckedContinuation { paragraphWaiters.append($0) }
    }
    func releaseParagraph() {
        paragraphContinuation?.resume()
        paragraphContinuation = nil
        holdParagraphRequest = false
    }
    func openBriefParagraph(chatID: String, reference: ChatBriefReference, paragraph: Int,
                            idempotencyKey: String) async throws {
        openedParagraphs.append(ParagraphOpen(chatID: chatID, reference: reference, paragraph: paragraph,
                                              key: idempotencyKey))
        if holdParagraphRequest {
            await withCheckedContinuation { continuation in
                paragraphContinuation = continuation
                paragraphWaiters.forEach { $0.resume() }
                paragraphWaiters.removeAll()
            }
        }
        if let paragraphFailure { throw paragraphFailure }
    }
    var shownParagraphs: [String] = []
    func openBriefParagraph(chatID: String, reference: ChatBriefReference, paragraph: Int, shownText: String,
                            idempotencyKey: String) async throws {
        shownParagraphs.append(shownText)
    }
    func answerPermission(chatID: String, requestID: String, optionID: String, idempotencyKey: String) async throws -> Bool { false }
    func cancel(chatID: String, idempotencyKey: String) async throws -> Bool { false }
    func pollChatTurns() async throws -> [MobileChatTurnEvent] { [] }
}

@MainActor
final class BriefConversationTests: XCTestCase {
    private let now = RelayTime.parse("2026-10-18T18:30:00Z")!
    private let plan = MobileBriefCard(id: "card-plan", kind: "action", mode: "send", label: "Draft a plan",
                                       message: "Draft a plan for this brief.", refs: ["e1"])
    private func brief(version: String = "revision-1") -> MobileBriefV1 {
        var value = MobileBriefV1(briefId: "2026-10-18-morning", slot: "morning", localDate: "2026-10-18",
                                  timezone: "America/Los_Angeles", asOf: "2026-10-18T14:00:00Z",
                                  narrative: [BriefParagraph(text: "A written brief.")], conversationKey: version)
        value.cards = [plan, MobileBriefCard(id: "opener", kind: "opener", mode: "speak_first", message: "Where to start?")]
        return value
    }
    private func cardReference(_ card: MobileBriefCard, version: String = "revision-1") -> ChatBriefReference {
        ChatBriefReference(briefId: "2026-10-18-morning", briefVersion: version, cardId: card.id, cardMessage: card.message)
    }
    private func store(hub: BriefConversationHub, cache: ChatCache, journal: PendingWriteJournal,
                       isRemote: @escaping @MainActor () -> Bool = { false },
                       presence: @escaping @Sendable () async throws -> Bool = { true }) -> ChatStore {
        ChatStore(hub: hub, journal: journal, guardian: BriefConversationGuard(), presence: presence,
                  isPaired: { true }, cache: cache, isRemote: isRemote)
    }
    private func resources() throws -> (ChatCache, PendingWriteJournal) {
        let directory = try makeTemporaryDirectory()
        return (ChatCache(directory: directory.appendingPathComponent("chats")),
                PendingWriteJournal(fileURL: directory.appendingPathComponent("writes.json")))
    }

    func testPairingLineConnectionCopyReflectsRemoteReachability() {
        XCTAssertEqual(PairingLineCopy.connection(remote: true, online: false),
                       "Your computer is asleep; this will send when it wakes.")
        XCTAssertEqual(PairingLineCopy.connection(remote: true, online: true), "Online")
        XCTAssertEqual(PairingLineCopy.connection(remote: false, online: false), "Offline")
        XCTAssertEqual(PairingLineCopy.connection(remote: false, online: nil), "Connection status unavailable")
    }

    func testACardTapPutsItsWordsInTheBoxAndOnlySendCarriesTheCard() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let first = store(hub: hub, cache: cache, journal: journal)
        XCTAssertTrue(first.prefillBriefCard(plan, from: brief()))
        let beforeSend = await hub.sent
        XCTAssertTrue(beforeSend.isEmpty, "a tap sends nothing")
        let relaunched = store(hub: hub, cache: cache, journal: journal)
        await relaunched.refreshList()
        _ = relaunched.prepareMainChat()
        XCTAssertEqual(relaunched.takeReturnedDrafts(in: "main", appendingTo: ""), "Draft a plan for this brief.")
        XCTAssertEqual(relaunched.composerDraft(in: "main").briefReference, cardReference(plan))
        relaunched.setDraft("Draft a short plan for this brief.", in: "main")
        let again = store(hub: hub, cache: cache, journal: journal)
        _ = again.prepareMainChat()
        let sent = await again.sendComposer(in: "main")
        XCTAssertTrue(sent)
        let messages = await hub.sent
        XCTAssertEqual(messages.count, 1)
        XCTAssertEqual(messages.first?.chatID, "main")
        XCTAssertEqual(messages.first?.reference, cardReference(plan), "Send carries the card and the words it showed")
        XCTAssertEqual(messages.first?.text, "Draft a short plan for this brief.", "the user's edit goes as written")
        let ordinary = await hub.ordinarySends
        XCTAssertEqual(ordinary, 0)
    }

    func testOnlyTheBriefsOwnSendCardsGoInTheBox() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        let stranger = MobileBriefCard(id: "card-x", kind: "action", mode: "send", label: "Other", message: "Other words")
        XCTAssertFalse(chat.prefillBriefCard(stranger, from: brief()))
        XCTAssertFalse(chat.prefillBriefCard(brief().cards[1], from: brief()), "the opener is the computer's to say")
        var withdrawn = brief()
        withdrawn.narrative = []
        XCTAssertFalse(chat.prefillBriefCard(plan, from: withdrawn))
        XCTAssertEqual(chat.notice, BriefConversationCopy.unavailable)
        XCTAssertNil(cache.loadMainDrafts()?.first)
    }

    func testStaleBriefReturnsTheBoundDraftAndExactNotice() async throws {
        let hub = BriefConversationHub()
        await hub.setFailure(true)
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        await chat.refreshList()
        _ = chat.prepareMainChat()
        XCTAssertTrue(chat.prefillBriefCard(plan, from: brief()))
        _ = chat.takeReturnedDrafts(in: "main", appendingTo: "")
        _ = await chat.sendComposer(in: "main")
        XCTAssertEqual(chat.notice, "This brief changed. Refresh and try again.")
        XCTAssertEqual(chat.returnedDrafts.first?.briefReference, cardReference(plan))
        XCTAssertEqual(cache.loadOutgoing(chatID: "main")?.first?.briefReference, cardReference(plan))
        let entries = await journal.entries()
        XCTAssertTrue(entries.isEmpty)
    }

    /// Bo's Oct 29 chip question: a refusal is "Not sent" with Try again, never "Will send when your computer is
    /// back", and Try again sends the words as an ordinary message without the brief the computer refused.
    func testARefusedChipQuestionIsNotSentAndTryAgainSendsItAsAPlainMessage() async throws {
        let hub = BriefConversationHub()
        await hub.setFailure(true)
        await hub.setOrdinaryAccepts(true)
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        await chat.refreshList()
        await chat.open(chatID: "main")
        XCTAssertTrue(chat.prefillBriefCard(plan, from: brief()))
        _ = chat.takeReturnedDrafts(in: "main", appendingTo: "")
        _ = await chat.sendComposer(in: "main")
        let refused = try XCTUnwrap(cache.loadOutgoing(chatID: "main")?.first)
        for online in [true, false, nil] as [Bool?] {
            XCTAssertEqual(refused.deliveryStatus(computerOnline: online), .failed)
        }
        XCTAssertEqual(chat.returnedDrafts.first?.text, plan.message, "the words come back to the box")

        let retried = await chat.retryFailed(refused.id, in: "main")
        XCTAssertTrue(retried)
        let plain = await hub.ordinaryTexts
        XCTAssertEqual(plain, [plan.message])
        let briefSends = await hub.sent
        XCTAssertEqual(briefSends.count, 1, "the retry does not send the refused brief again")
        let after = cache.loadOutgoing(chatID: "main") ?? []
        XCTAssertFalse(after.contains { $0.hasFailed }, "Not sent goes once the words went")
        XCTAssertEqual(after.last?.state, .sent)
        XCTAssertNil(after.last?.briefReference)
        XCTAssertEqual(chat.draft(in: "main"), "")
    }

    /// The paragraph tap that met a 409 asks once more with the words this phone showed.
    func testParagraphTapAfterTheBriefMovedAsksAgainWithTheWordsShown() async throws {
        let hub = BriefConversationHub()
        await hub.setParagraphFailure(.conflict(message: "This brief changed. Refresh and try again."))
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        let opened = await chat.askAboutBriefParagraph(brief(), paragraph: 0)
        XCTAssertTrue(opened)
        let shown = await hub.shownParagraphs
        XCTAssertEqual(shown, ["A written brief."])
        XCTAssertNil(chat.notice)
    }

    /// A message that got no answer goes as soon as the phone hears from the computer again.
    func testAMessageWaitingForTheComputerGoesWhenThePhoneHearsFromIt() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        await chat.refreshList()
        await chat.open(chatID: "main")
        _ = await chat.send("Are you there?")
        XCTAssertEqual(cache.loadOutgoing(chatID: "main")?.first?.deliveryStatus(computerOnline: true),
                       .waitingForComputer, "no answer: the label says it waits for the computer")
        await hub.setOrdinaryAccepts(true)
        chat.heardFromComputer()
        for _ in 0..<50 where cache.loadOutgoing(chatID: "main")?.first?.state != .sent {
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        XCTAssertEqual(cache.loadOutgoing(chatID: "main")?.first?.state, .sent)
        let sends = await hub.ordinarySends
        XCTAssertEqual(sends, 2, "the same message, once more, under its journaled key")
    }

    func testTalkAboutThisBriefHasTheComputerSpeakFirstAndWaitsForTheAnswer() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        let opened = await chat.openBriefConversation(brief())
        XCTAssertTrue(opened)
        let requests = await hub.opened
        XCTAssertEqual(requests.map(\.chatID), ["main"])
        XCTAssertEqual(requests.first?.reference, brief().chatReference)
        XCTAssertFalse(requests.first?.key.isEmpty ?? true)
        let sent = await hub.sent
        XCTAssertTrue(sent.isEmpty, "the phone writes and sends nothing of its own")
        _ = chat.prepareMainChat()
        XCTAssertEqual(chat.takeReturnedDrafts(in: "main", appendingTo: ""), "")
        XCTAssertEqual(chat.composerDraft(in: "main").briefReference, brief().chatReference,
                       "the user's answer goes with the brief")
    }

    /// A brief the phone drew without a conversation reference is asked of the computer by its id; the computer's
    /// revision then carries the opener and the user's answer. No brief is made on the phone.
    func testTalkAboutABriefWithoutAReferenceAsksTheComputerForItsConversation() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        var unkeyed = brief()
        unkeyed.conversationKey = nil
        XCTAssertNil(unkeyed.chatReference)
        let opened = await chat.openBriefConversation(unkeyed)
        XCTAssertTrue(opened)
        let asked = await hub.conversationAsked
        XCTAssertEqual(asked, ["2026-10-18-morning"])
        let requests = await hub.opened
        XCTAssertEqual(requests.first?.reference, brief().chatReference)
        XCTAssertNil(chat.notice)
    }

    /// With the computer unreachable, Talk says so in the one line and opens nothing.
    func testTalkAboutABriefWithoutAReferenceSaysTheComputerIsBackWhenItCannotBeReached() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        await hub.setConversationFailure(.noAnswer)
        var unkeyed = brief()
        unkeyed.conversationKey = nil
        let opened = await chat.openBriefConversation(unkeyed)
        XCTAssertFalse(opened)
        XCTAssertEqual(chat.notice, BriefConversationCopy.unavailable)
        let requests = await hub.opened
        XCTAssertTrue(requests.isEmpty, "no opener is posted while the computer is away")
    }

    /// The computer's answer decides the one line under Talk. A gate refusal and a brief the computer does not
    /// hold both say the conversation will not open, with no reason. Only a route an older computer lacks says
    /// to update it; a computer that cannot be reached says it is away.
    func testTheComputersAnswerDecidesTheLineUnderTalk() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        var unkeyed = brief()
        unkeyed.conversationKey = nil
        let cases: [(HubRequestError, String)] = [
            (.conflict(message: "This brief changed. Refresh and try again."), BriefConversationCopy.gateRefused),
            (.rejected(status: 404, message: "brief not found"), BriefConversationCopy.gateRefused),
            (.rejected(status: 404, message: "mobile route not found"), BriefConversationCopy.computerTooOld),
            (.rejected(status: 404, message: ""), BriefConversationCopy.computerTooOld),
            (.noAnswer, BriefConversationCopy.unavailable),
        ]
        for (failure, line) in cases {
            await hub.setConversationFailure(failure)
            let resolved = await chat.briefConversationReference(for: unkeyed)
            XCTAssertNil(resolved.reference, "\(failure)")
            XCTAssertEqual(resolved.notice, line, "\(failure)")
        }
        XCTAssertNotEqual(BriefConversationCopy.computerTooOld, BriefConversationCopy.unavailable)
    }

    /// A brief with a reference and an unreachable computer: Talk gives the one line and opens no sheet, and makes
    /// no route call.
    func testTalkWithAReferenceAndOfflineGivesTheLineAndOpensNoSheet() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal, presence: { false })
        let resolved = await chat.resolveTalkReference(for: brief())
        XCTAssertNil(resolved.reference)
        XCTAssertEqual(resolved.notice, BriefConversationCopy.unavailable)
        let asked = await hub.conversationAsked
        XCTAssertTrue(asked.isEmpty)
        let opened = await hub.opened
        XCTAssertTrue(opened.isEmpty)
    }

    /// A brief with a reference and a reachable computer keeps its reference and makes no route call (an older
    /// computer keeps Talk).
    func testTalkWithAReferenceAndOnlineMakesNoRouteCall() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal, presence: { true })
        let resolved = await chat.resolveTalkReference(for: brief())
        XCTAssertEqual(resolved.reference, brief().chatReference)
        XCTAssertNil(resolved.notice)
        let asked = await hub.conversationAsked
        XCTAssertTrue(asked.isEmpty, "a brief with a reference makes no route call")
    }

    /// A brief without a reference still asks the computer by its id when the computer is reachable.
    func testTalkWithoutAReferenceStillAsksByIdWhenOnline() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal, presence: { true })
        var unkeyed = brief()
        unkeyed.conversationKey = nil
        let resolved = await chat.resolveTalkReference(for: unkeyed)
        XCTAssertEqual(resolved.reference, brief().chatReference)
        let asked = await hub.conversationAsked
        XCTAssertEqual(asked, ["2026-10-18-morning"])
    }

    /// An answer that never comes counts as unreachable, so Talk does not hang.
    func testTalkWithAReferenceTreatsASilentComputerAsUnreachable() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal, presence: {
            try await Task.sleep(nanoseconds: 5_000_000_000)
            return true
        })
        let resolved = await chat.resolveTalkReference(for: brief(), timeout: .milliseconds(100))
        XCTAssertNil(resolved.reference)
        XCTAssertEqual(resolved.notice, BriefConversationCopy.unavailable)
    }

    /// Talk gives way to its line when the phone already knows the computer is away, or a tap found it unreachable.
    /// A refusal or an older computer keeps the button, so those lines never replace it.
    func testTheUnreachableLineReplacesTalkOnlyWhenTheComputerIsAway() {
        XCTAssertTrue(BriefCardsView.talkReplacedByLine(knownAway: true, tapLine: nil))
        XCTAssertTrue(BriefCardsView.talkReplacedByLine(knownAway: false, tapLine: BriefConversationCopy.unavailable))
        XCTAssertFalse(BriefCardsView.talkReplacedByLine(knownAway: false, tapLine: nil))
        XCTAssertFalse(BriefCardsView.talkReplacedByLine(knownAway: false, tapLine: BriefConversationCopy.gateRefused))
        XCTAssertFalse(BriefCardsView.talkReplacedByLine(knownAway: false, tapLine: BriefConversationCopy.computerTooOld))
    }

    func testSheetOpenerRetriesItsReceiptWithoutChangingChatDraft() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        await chat.refreshList()
        chat.setDraft("Existing draft", in: "main")
        let reference = try XCTUnwrap(brief().chatReference)
        await hub.setOpenFailure(.noAnswer)
        await chat.postBriefOpener(reference: reference)
        await hub.setOpenFailure(nil)
        await chat.postBriefOpener(reference: reference)
        let requests = await hub.opened
        XCTAssertEqual(requests.count, 2)
        XCTAssertEqual(requests[0].key, requests[1].key)
        XCTAssertEqual(chat.draft(in: "main"), "Existing draft")
        let pending = await journal.entries()
        XCTAssertTrue(pending.isEmpty)
    }

    func testParagraphTapSendsItsReferenceAndIndexAndLeavesAnEmptyComposer() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        let opened = await chat.askAboutBriefParagraph(brief(), paragraph: 1)
        XCTAssertTrue(opened)
        let requests = await hub.openedParagraphs
        XCTAssertEqual(requests.count, 1)
        XCTAssertEqual(requests.first?.chatID, "main")
        XCTAssertEqual(requests.first?.reference, brief().chatReference)
        XCTAssertEqual(requests.first?.paragraph, 1)
        XCTAssertFalse(requests.first?.key.isEmpty ?? true)
        let sent = await hub.sent
        XCTAssertTrue(sent.isEmpty, "the paragraph tap posts no phone-authored message")
        XCTAssertEqual(chat.returnedDrafts.first?.briefReference, brief().chatReference)
        _ = chat.prepareMainChat()
        XCTAssertEqual(chat.takeReturnedDrafts(in: "main", appendingTo: ""), "")
        XCTAssertEqual(chat.draft(in: "main"), "")
        XCTAssertEqual(chat.composerDraft(in: "main").briefReference, brief().chatReference)
    }

    func testParagraphTapMapsUnavailableAndOfflineAndDoesNotCrossUnpairing() async throws {
        for (failure, expected) in [
            (HubRequestError.rejected(status: 404, message: "route not found"), BriefConversationCopy.unavailable),
            (.noAnswer, BriefConversationCopy.offline),
        ] {
            let hub = BriefConversationHub()
            await hub.setParagraphFailure(failure)
            let (cache, journal) = try resources()
            let chat = store(hub: hub, cache: cache, journal: journal)
            let opened = await chat.askAboutBriefParagraph(brief(), paragraph: 0)
            XCTAssertFalse(opened)
            XCTAssertEqual(chat.notice, expected)
            XCTAssertNil(cache.loadMainDrafts()?.first)
        }

        let hub = BriefConversationHub()
        await hub.holdParagraph()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        let request = Task { await chat.askAboutBriefParagraph(brief(), paragraph: 0) }
        await hub.waitForParagraph()
        XCTAssertTrue(chat.clearAfterUnpair())
        await hub.releaseParagraph()
        let opened = await request.value
        XCTAssertFalse(opened)
        XCTAssertNil(chat.composerDraft(in: "main").briefReference)
        XCTAssertNil(cache.loadMainDrafts()?.first)
    }

    func testACardTapAfterTalkReplacesTheEmptyAnswerBox() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        let opened = await chat.openBriefConversation(brief())
        XCTAssertTrue(opened)
        _ = chat.prepareMainChat()
        XCTAssertEqual(chat.takeReturnedDrafts(in: "main", appendingTo: ""), "")
        XCTAssertTrue(chat.prefillBriefCard(plan, from: brief()))
        XCTAssertEqual(chat.takeReturnedDrafts(in: "main", appendingTo: chat.draft(in: "main")), plan.message)
        XCTAssertEqual(chat.composerDraft(in: "main").briefReference, cardReference(plan))
        XCTAssertTrue(chat.returnedDrafts.isEmpty)
        XCTAssertNotEqual(chat.notice, BriefConversationCopy.finishDraft)
    }

    func testTalkWhenTheComputerCannotBeReachedSaysItIsOfflineAndLeavesTheBox() async throws {
        let hub = BriefConversationHub()
        await hub.setOpenFailure(.noAnswer)
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        let opened = await chat.openBriefConversation(brief())
        XCTAssertFalse(opened)
        XCTAssertEqual(chat.notice, BriefConversationCopy.offline)
        XCTAssertNil(cache.loadMainDrafts()?.first)

        await hub.setOpenFailure(.conflict(message: BriefConversationCopy.changed))
        let stale = await chat.openBriefConversation(brief())
        XCTAssertFalse(stale)
        XCTAssertEqual(chat.notice, BriefConversationCopy.changed)

        // A computer without the route answers 404: it is there, so the phone does not call it offline.
        await hub.setOpenFailure(.rejected(status: 404, message: "route not found"))
        let older = await chat.openBriefConversation(brief())
        XCTAssertFalse(older)
        XCTAssertEqual(chat.notice, BriefConversationCopy.unavailable)
    }

    func testBriefTapsOnARemoteComputerDoNotPromiseALaterSend() async throws {
        let openHub = BriefConversationHub()
        await openHub.setOpenFailure(.noAnswer)
        let (openCache, openJournal) = try resources()
        let openChat = store(hub: openHub, cache: openCache, journal: openJournal, isRemote: { true })
        let opened = await openChat.openBriefConversation(brief())
        XCTAssertFalse(opened)
        XCTAssertEqual(openChat.notice, "Your computer is offline.")
        XCTAssertNotEqual(openChat.notice, "Your computer is asleep; this will send when it wakes.")

        let paragraphHub = BriefConversationHub()
        await paragraphHub.setParagraphFailure(.noAnswer)
        let (paragraphCache, paragraphJournal) = try resources()
        let paragraphChat = store(hub: paragraphHub, cache: paragraphCache, journal: paragraphJournal,
                                 isRemote: { true })
        let paragraphOpened = await paragraphChat.askAboutBriefParagraph(brief(), paragraph: 0)
        XCTAssertFalse(paragraphOpened)
        XCTAssertEqual(paragraphChat.notice, "Your computer is offline.")
        XCTAssertNotEqual(paragraphChat.notice, "Your computer is asleep; this will send when it wakes.")
    }

    func testIncompatibleBriefWaitsUntilCurrentDraftIsDiscarded() async throws {
        let hub = BriefConversationHub()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        await chat.refreshList()
        _ = chat.prepareMainChat()
        XCTAssertTrue(chat.prefillBriefCard(plan, from: brief()))
        _ = chat.takeReturnedDrafts(in: "main", appendingTo: "")
        XCTAssertTrue(chat.prefillBriefCard(plan, from: brief(version: "revision-2")))
        _ = chat.takeReturnedDrafts(in: "main", appendingTo: chat.draft(in: "main"))
        XCTAssertEqual(chat.composerDraft(in: "main").briefReference?.briefVersion, "revision-1")
        XCTAssertEqual(chat.returnedDrafts.count, 1)
        XCTAssertTrue(chat.discardComposer(in: "main"))
        _ = chat.takeReturnedDrafts(in: "main", appendingTo: "")
        XCTAssertEqual(chat.composerDraft(in: "main").briefReference?.briefVersion, "revision-2")
        XCTAssertTrue(chat.returnedDrafts.isEmpty)
    }

    func testTalkCannotCrossPairingsDuringMainChatDiscovery() async throws {
        let hub = BriefConversationHub()
        await hub.holdDiscovery()
        let (cache, journal) = try resources()
        let chat = store(hub: hub, cache: cache, journal: journal)
        let task = Task { await chat.openBriefConversation(brief()) }
        await hub.waitForDiscovery()
        XCTAssertTrue(chat.clearAfterUnpair())
        await hub.releaseDiscovery()
        let result = await task.value
        XCTAssertFalse(result)
        let opened = await hub.opened
        XCTAssertTrue(opened.isEmpty)
    }

    func testOlderBriefsStayReadableAndRetryFingerprintsChangeOnlyWithACard() throws {
        let old = try JSONDecoder().decode(MobileBriefV1.self, from: Data(#"{"version":1,"briefId":"old"}"#.utf8))
        XCTAssertNil(old.chatReference)
        XCTAssertTrue(old.cards.isEmpty)
        var withdrawn = brief()
        withdrawn.narrative = []
        XCTAssertNil(withdrawn.chatReference)
        XCTAssertTrue(withdrawn.sendCards.isEmpty)
        let oldComposer = try JSONDecoder().decode(ChatComposerDraft.self,
            from: Data(#"{"text":"hello","offerReferences":[],"recoveredDraftIDs":[]}"#.utf8))
        XCTAssertNil(oldComposer.briefReference)
        let oldReference = try JSONDecoder().decode(ChatBriefReference.self,
            from: Data(#"{"briefId":"2026-10-18-morning","briefVersion":"revision-1","offerId":"offer-1"}"#.utf8))
        XCTAssertNil(oldReference.cardId)
        var first = ChatOutgoingMessage(id: "message", text: "Same text", after: nil, sentAt: now, state: .sending,
                                        briefReference: brief().chatReference)
        // A message queued by an older build keeps its fingerprint, so its saved retry key still matches.
        XCTAssertEqual(first.deliveryFingerprint, WriteFingerprint.make(
            ["Same text", "message", "brief", "2026-10-18-morning", "revision-1", ""]))
        let original = first.deliveryFingerprint
        first.briefReference = brief(version: "revision-2").chatReference
        XCTAssertNotEqual(first.deliveryFingerprint, original)
        first.briefReference = brief().chatReference
        first.briefReference?.cardId = plan.id
        first.briefReference?.cardMessage = plan.message
        let withCard = first.deliveryFingerprint
        XCTAssertNotEqual(withCard, original)
        first.briefReference?.cardMessage = "Other words"
        XCTAssertNotEqual(first.deliveryFingerprint, withCard)
    }

    func testCardsAndAttentionReasonsDecodeAndOddCardsDoNotBreakTheBrief() throws {
        let json = #"""
        {"version":1,"briefId":"b1","conversationKey":"k","narrative":[{"text":"Text."}],
         "cards":[{"id":"a1","kind":"action","mode":"send","label":"Draft a plan","message":"Draft a plan.","refs":["e1"],"facts":"x","writer":"model"},
                  {"id":"i1","kind":"item","mode":"send","message":"What next?","itemKey":"people:dana"},
                  {"id":"opener","kind":"opener","mode":"speak_first","message":"Hello.","refs":[]}],
         "sections":{"people":[{"id":"p1","key":"people:dana","title":"Dana","attentionReason":"Waiting since Tuesday","attentionRank":2,"since":"2026-10-14T09:00:00Z"}]}}
        """#
        let value = try JSONDecoder().decode(MobileBriefV1.self, from: Data(json.utf8))
        XCTAssertEqual(value.cards.map(\.id), ["a1", "i1", "opener"])
        XCTAssertEqual(value.actionCards.map(\.id), ["a1"])
        let dana = try XCTUnwrap(value.sections.people.first)
        XCTAssertEqual(dana.attentionReason, "Waiting since Tuesday")
        XCTAssertEqual(dana.attentionRank, 2)
        XCTAssertEqual(dana.since, "2026-10-14T09:00:00Z")
        XCTAssertEqual(value.itemCard(for: dana)?.id, "i1")
        let odd = try JSONDecoder().decode(MobileBriefV1.self, from: Data(
            #"{"version":1,"briefId":"b2","cards":"not a list","sections":{"people":[{"title":"x","attentionRank":"high"}]}}"#.utf8))
        XCTAssertTrue(odd.cards.isEmpty)
        XCTAssertNil(odd.sections.people.first?.attentionRank)
        XCTAssertEqual(odd.sections.people.first?.title, "x")
    }

    func testAtMostTwoButtonsAndOnlySendCardsWithWords() {
        var value = brief()
        value.cards = (1...3).map { MobileBriefCard(id: "a\($0)", kind: "action", mode: "send", label: "Do \($0)", message: "Do \($0).") } +
            [MobileBriefCard(id: "blank", kind: "action", mode: "send", label: "Blank", message: "  "),
             MobileBriefCard(id: "nolabel", kind: "action", mode: "send", message: "No label.")]
        XCTAssertEqual(value.actionCards.map(\.id), ["a1", "a2"])
        XCTAssertFalse(value.sendCards.contains { $0.id == "blank" })
        value.conversationKey = nil
        XCTAssertTrue(value.sendCards.isEmpty, "a brief the computer cannot discuss has no taps")
    }

    func testAttentionListsReasonedItemsByRankThenAgeOncePerGoal() {
        var value = brief()
        func item(_ id: String, reason: String?, rank: Int? = nil, since: String? = nil, kind: String? = nil,
                  purpose: String? = nil) -> BriefItem {
            var item = BriefItem(id: id, title: id)
            item.attentionReason = reason
            item.attentionRank = rank
            item.since = since
            item.kind = kind
            item.purposeId = purpose
            return item
        }
        value.sections.commitments = [item("late", reason: "Due today", rank: 2, since: "2026-10-10T00:00:00Z", purpose: "goal-1"),
                                      item("plain", reason: nil)]
        value.sections.workingToward = [item("same-goal", reason: "Also goal 1", rank: 1, purpose: "goal-1")]
        value.sections.people = [item("dana", reason: "Waiting since Tuesday", rank: 2, since: "2026-10-01T00:00:00Z")]
        value.sections.moved = [item("found", reason: "A find", rank: 1, kind: "find"),
                                item("urgent", reason: "Blocked", rank: 1)]
        value.sections.openLoops = [item("loop", reason: "Open", rank: nil)]
        XCTAssertEqual(value.attentionItems.compactMap(\.id), ["urgent", "dana", "late"],
                       "rank first, then the longest open, one per goal, no finds, at most three")
    }
}
