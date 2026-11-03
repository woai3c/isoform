import Foundation
import XCTest
@testable import Yansu

/// Answers the topic routes in-process and records what the phone sent, under the keys it used.
private actor StubTopicHub: TopicHubAPI {
    struct Sent: Equatable {
        let topicID: String
        let text: String
        let key: String
    }

    var flag: Bool = true
    var statusFailure: Error?
    var listFailures: [Error] = []
    var list = MobileTopicList(topics: [])
    var pages: [String: MobileTopicPage] = [:]
    var pageFailures: [Error] = []
    var sendFailures: [Error] = []
    private(set) var statusCalls = 0
    private(set) var listCalls = 0
    private(set) var pageCalls: [String?] = []
    private(set) var sent: [Sent] = []

    func set(flag: Bool) { self.flag = flag }
    func set(statusFailure: Error?) { self.statusFailure = statusFailure }
    func set(list: [MobileTopic]) { self.list = MobileTopicList(topics: list) }
    func set(projection: MobileTopicList) { self.list = projection }
    func failList(_ errors: [Error]) { listFailures = errors }
    func set(page: MobileTopicPage, cursor: String? = nil) { pages[cursor ?? ""] = page }
    func failPages(_ errors: [Error]) { pageFailures = errors }
    func failSends(_ errors: [Error]) { sendFailures = errors }

    func supportsTopics() async throws -> Bool {
        statusCalls += 1
        if let statusFailure { throw statusFailure }
        return flag
    }

    func topics() async throws -> MobileTopicList {
        listCalls += 1
        if !listFailures.isEmpty { throw listFailures.removeFirst() }
        return list
    }

    func topic(id: String, cursor: String?, limit: Int) async throws -> MobileTopicPage {
        pageCalls.append(cursor)
        if !pageFailures.isEmpty { throw pageFailures.removeFirst() }
        return pages[cursor ?? ""] ?? MobileTopicPage(sessionId: "main", topicId: id, items: [], hasMore: false, nextCursor: nil)
    }

    func sendTopicMessage(topicID: String, text: String, idempotencyKey: String) async throws -> ChatMessageAccepted {
        sent.append(Sent(topicID: topicID, text: text, key: idempotencyKey))
        if !sendFailures.isEmpty { throw sendFailures.removeFirst() }
        return ChatMessageAccepted(turnId: "turn-\(sent.count)", seq: 90 + sent.count)
    }
}

private final class Keys: @unchecked Sendable {
    private let lock = NSLock()
    private var next = 0

    func make() -> String {
        lock.lock()
        defer { lock.unlock() }
        next += 1
        return "key-\(next)"
    }
}

private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
    try JSONDecoder().decode(type, from: Data(json.utf8))
}

// MARK: - Decoding

final class TopicDecodingTests: XCTestCase {
    func testTopicListDecodesPendingAndPresenceWithOlderComputerDefaults() throws {
        let pending = try decode(MobileTopicList.self, #"{"topics":[],"refreshing":true,"hasTopics":true}"#)
        XCTAssertTrue(pending.refreshing)
        XCTAssertTrue(pending.hasTopics)
        let older = try decode(MobileTopicList.self, #"{"topics":[]}"#)
        XCTAssertFalse(older.refreshing)
        XCTAssertFalse(older.hasTopics)
    }

    func testBriefParagraphTopicsAndQuestionCardsDecodeAsSectionEightWritesThem() throws {
        let brief = try decode(MobileBriefV1.self, """
        {"version":1,"briefId":"2026-10-22-morning","slot":"morning","localDate":"2026-10-22","conversationKey":"rev-1",
         "narrative":[
           {"text":"Countersign the SOW.","refs":["e1"],"topicId":"project:northwind","topicName":"Northwind kickoff"},
           {"text":"Halcyon wants Oct 30.","refs":["e1"]},
           {"text":"What should we do next?","refs":["e1"]}],
         "cards":[
           {"id":"q1","kind":"question","mode":"send","label":"Review the SOW","message":"What changed in the SOW?","refs":["e1"],"paragraph":0},
           {"id":"q2","kind":"question","mode":"send","label":"Check kickoff","message":"Who is on the kickoff?","refs":["e1"],"paragraph":0},
           {"id":"q3","kind":"question","mode":"send","label":"Third question","message":"A third question","refs":["e1"],"paragraph":0},
           {"id":"q4","kind":"question","mode":"send","label":"Review the date","message":"Could we hit Oct 30?","refs":["e1"],"paragraph":1},
           {"id":"a1","kind":"action","mode":"send","label":"Review Thursday","message":"Review Thursday afternoon.","refs":["e1"]},
           {"id":"a2","kind":"action","mode":"send","label":"Not this week","message":"Not this week.","refs":["e1"]}],
         "evidence":[{"ref":"e1","sourceKind":"calendar","sourceId":"ev-1"}]}
        """)
        XCTAssertEqual(brief.narrative[0].topicId, "project:northwind")
        XCTAssertEqual(brief.narrative[0].topicName, "Northwind kickoff")
        XCTAssertEqual(brief.narrative[0].topic?.id, "project:northwind")
        XCTAssertNil(brief.narrative[1].topic)
        XCTAssertEqual(brief.paragraphChip(forParagraph: 0)?.id, "q1", "the first question is the paragraph's chip")
        XCTAssertEqual(brief.paragraphChip(forParagraph: 1)?.id, "q4")
        XCTAssertEqual(brief.cards.first { $0.id == "q1" }?.paragraph, 0, "zero is encoded and read as zero")
        XCTAssertNil(brief.cards.first { $0.id == "a1" }?.paragraph)
        XCTAssertEqual(brief.closingParagraphIndex, 2)
        XCTAssertEqual(brief.actionCards.map(\.id), ["a1", "a2"], "the closing answers are the action cards")
        // The cache writes the fields back, so a brief read offline keeps its topics and questions.
        let cached = try JSONDecoder().decode(MobileBriefV1.self, from: JSONEncoder().encode(brief))
        XCTAssertEqual(cached, brief)
    }

    func testClosingHoldQuestionIsRemovedBeforeCardsAreShown() throws {
        let brief = try decode(MobileBriefV1.self, """
        {"conversationKey":"rev-1","narrative":[{"text":"A meeting is coming up."},
         {"text":"Should I hold Thursday afternoon?"}],
         "cards":[{"id":"a1","kind":"action","mode":"send","label":"Hold Thursday",
                   "message":"Hold Thursday afternoon."}]}
        """)
        XCTAssertEqual(brief.narrative.count, 1)
        XCTAssertTrue(brief.actionCards.isEmpty)
        XCTAssertNil(brief.conversationKey)
    }

    func testParagraphChipsUseFirstLabeledCardsDeduplicateAndCapAtThree() throws {
        var brief = try decode(MobileBriefV1.self, """
        {"briefId":"2026-10-22-morning","conversationKey":"rev",
         "narrative":[{"text":"One"},{"text":"Two"},{"text":"Three"},{"text":"Four"},{"text":"Should I wait?"}],
         "cards":[
           {"id":"first","kind":"question","mode":"send","label":"Review   launch","message":"First question","paragraph":0},
           {"id":"same-paragraph","kind":"question","mode":"send","label":"Second question","message":"Second question","paragraph":0},
           {"id":"duplicate-label","kind":"question","mode":"send","label":" review launch ","message":"Duplicate label","paragraph":2},
           {"id":"missing-label","kind":"item","mode":"send","message":"No label","paragraph":1},
           {"id":"item-chip","kind":"item","mode":"send","label":"Ask Dana","message":"Item question","paragraph":1},
           {"id":"third-chip","kind":"question","mode":"send","label":"What changed","message":"Third question","paragraph":2},
           {"id":"over-cap","kind":"question","mode":"send","label":"Fourth chip","message":"Fourth question","paragraph":3},
           {"id":"closing","kind":"question","mode":"send","label":"Closing question","message":"Closing question","paragraph":4},
           {"id":"empty-message","kind":"question","mode":"send","label":"Empty message","message":"  ","paragraph":3}
         ]}
        """)
        XCTAssertEqual(brief.paragraphChips.mapValues(\.id), [0: "first", 1: "item-chip", 2: "third-chip"])
        XCTAssertEqual(brief.paragraphChip(forParagraph: 1)?.label, "Ask Dana")
        brief.conversationKey = nil
        XCTAssertTrue(brief.paragraphChips.isEmpty, "an undiscussable brief has no chips")
    }

    func testAnOlderBriefWithoutTheNewFieldsStillDecodes() throws {
        let brief = try decode(MobileBriefV1.self, """
        {"version":1,"briefId":"2026-10-01-morning","slot":"morning","localDate":"2026-10-01","conversationKey":"rev",
         "narrative":[{"text":"A quiet day.","refs":["e1"]},{"text":"Tomorrow starts early."}],
         "cards":[{"id":"a1","kind":"action","mode":"send","label":"Plan","message":"Plan tomorrow.","refs":["e1"]}],
         "evidence":[{"ref":"e1","sourceKind":"history","sourceId":"h"}]}
        """)
        XCTAssertNil(brief.narrative[0].topicId)
        XCTAssertNil(brief.narrative[0].topic)
        XCTAssertNil(brief.cards[0].paragraph)
        XCTAssertTrue(brief.paragraphChips.isEmpty)
        XCTAssertNil(brief.closingParagraphIndex, "no question mark, no closing question")
        XCTAssertEqual(brief.actionCards.count, 1)
    }

    func testMalformedOrUnsafeTopicFieldsLeaveTheParagraphWithoutAButton() throws {
        let brief = try decode(MobileBriefV1.self, """
        {"narrative":[{"text":"One","topicId":42,"topicName":"Numbers"},
                      {"text":"Two","topicId":"a/b","topicName":"Slash"},
                      {"text":"Three","topicId":"ok","topicName":"  "},
                      {"text":"四？","topicId":"zh","topicName":"中文"}],
         "cards":[{"id":"q","kind":"question","mode":"send","message":"Why?","paragraph":"first"}]}
        """)
        XCTAssertNil(brief.narrative[0].topicId)
        XCTAssertEqual(brief.narrative[0].text, "One", "the paragraph still reads")
        XCTAssertNil(brief.narrative[1].topic, "an id that cannot go in a path gets no button")
        XCTAssertNil(brief.narrative[2].topic, "an empty name gets no button")
        XCTAssertEqual(brief.narrative[3].topic?.name, "中文")
        XCTAssertNil(brief.cards[0].paragraph)
        XCTAssertEqual(brief.closingParagraphIndex, 3, "a full-width question mark closes too")
    }

    func testStatusCarriesTheTopicsFlagOnlyOnNewerComputers() throws {
        let newer = try decode(HubEnvelope<HubStatus>.self,
                               #"{"ok":true,"data":{"hubName":"Mac","protocolVersion":1,"topics":true}}"#)
        XCTAssertEqual(newer.data?.topics, true)
        let older = try decode(HubEnvelope<HubStatus>.self, #"{"ok":true,"data":{"hubName":"Mac","protocolVersion":1}}"#)
        XCTAssertNil(older.data?.topics)
    }

    func testTopicListDecodesItsFieldsAndDropsTopicsThePhoneCannotOpen() throws {
        let list = try decode(MobileTopicList.self, """
        {"topics":[
          {"id":"project:halcyon","kind":"project","name":"Halcyon ERP sync","status":"waiting_on_you",
           "latest":{"text":"Reply drafted."},"next":{"text":"Standup","at":"2026-10-22T21:00:00Z"},
           "peopleLine":{"names":["Ops lead","Priya"],"count":4}},
          {"id":"following:agents","kind":"following","name":"Multi-agent coding","status":"new_stories","newStories":2,
           "why":{"lastSeenAt":"2026-10-21T10:00:00Z","interestWhy":"You read about it."}},
          {"id":"bad/id","kind":"project","name":"Unopenable","status":"today"},
          {"id":"no-name","kind":"project","name":"","status":"today"},
          {"id":"project:halcyon","kind":"project","name":"Duplicate","status":"today"},
          "not an object"]}
        """)
        XCTAssertEqual(list.topics.map(\.id), ["project:halcyon", "following:agents"])
        let halcyon = list.topics[0]
        XCTAssertEqual(halcyon.latest?.text, "Reply drafted.")
        XCTAssertEqual(halcyon.next?.atDate, RelayTime.parse("2026-10-22T21:00:00Z"))
        XCTAssertEqual(halcyon.peopleLine, MobileTopicPeople(names: ["Ops lead", "Priya"], count: 4))
        XCTAssertEqual(halcyon.newStories, 0, "omitted when zero")
        XCTAssertTrue(list.topics[1].isFollowing)
        XCTAssertEqual(list.topics[1].newStories, 2)
        XCTAssertEqual(list.topics[1].why?.interestWhy, "You read about it.")
        XCTAssertEqual(TopicLines.people(halcyon), "Ops lead, Priya and 2 others")
        XCTAssertEqual(TopicStatusChip.label(for: list.topics[1]), "2 new")
    }

    func testWaitingOnThemHasAStatusLabel() throws {
        let topic = try decode(MobileTopic.self,
            #"{"id":"project:halcyon","kind":"project","name":"Halcyon","status":"waiting_on_them"}"#)

        XCTAssertEqual(TopicStatusChip.label(for: topic), "Waiting on them")
    }

    func testTopicShowsAStructuredMessageSendReport() throws {
        let item = try decode(MobileTopicItem.self, #"""
        {"id":"row-receipt","kind":"message","at":1761120060000,
         "message":{"seq":12,"id":"row-receipt","role":"system","kind":"text","text":"English fallback",
                    "report":{"source":"message_send","channel":"mail","sentAt":1761120060000}}}
        """#)

        XCTAssertTrue(TopicRow.isShown(item))
        XCTAssertEqual(item.message?.report?.channel, "mail")
    }

    func testTopicPageDecodesEventsAndMessagesOldestFirst() throws {
        let page = try decode(MobileTopicPage.self, """
        {"sessionId":"main","topicId":"project:halcyon","hasMore":true,"nextCursor":"b2xkZXI",
         "items":[
           {"id":"event:1","kind":"event","at":1761120000000,"actor":"user","text":"pushed v0.3 to staging"},
           {"id":"row-1","kind":"message","at":1761120060000,
            "message":{"seq":12,"id":"row-1","role":"user","kind":"text","text":"Could we hit Oct 30?","toolName":"","at":"2026-10-22T08:01:00Z"}},
           {"id":"row-2","kind":"message","at":1761120120000,
            "message":{"seq":13,"id":"row-2","role":"assistant","kind":"text","text":"Only by skipping two reviews.","toolName":"","at":"2026-10-22T08:02:00Z"}},
           {"kind":"event","text":"no id"}]}
        """)
        XCTAssertEqual(page.items.map(\.id), ["event:1", "row-1", "row-2"], "a row without an id is left out")
        XCTAssertEqual(page.items[0].at, Date(timeIntervalSince1970: 1_761_120_000), "Unix milliseconds")
        XCTAssertEqual(page.items[0].actor, "user")
        XCTAssertEqual(page.items[1].message?.role, .user)
        XCTAssertEqual(page.items[1].message?.seq, 12)
        XCTAssertEqual(page.items[2].message?.text, "Only by skipping two reviews.")
        XCTAssertTrue(page.hasMore)
        XCTAssertEqual(page.nextCursor, "b2xkZXI")
        let last = try decode(MobileTopicPage.self, #"{"sessionId":"main","topicId":"t","items":[],"hasMore":false,"nextCursor":""}"#)
        XCTAssertNil(last.nextCursor, "an empty cursor means there is nothing before")
    }

    func testTopicRoutesUseTheProtocolPathsAndBoundTheirQuery() {
        XCTAssertEqual(HubRoute.topics.path, "/topics")
        XCTAssertEqual(HubRoute.topics.method, "GET")
        XCTAssertEqual(HubRoute.topic(id: "project:halcyon", cursor: nil, limit: 50).path, "/topics/project:halcyon")
        XCTAssertEqual(HubRoute.topic(id: "t", cursor: nil, limit: 80).query, "limit=50")
        XCTAssertEqual(HubRoute.topic(id: "t", cursor: "b2xk-_", limit: 10).query, "cursor=b2xk-_&limit=10")
        XCTAssertEqual(HubRoute.topic(id: "t", cursor: "", limit: 0).query, "limit=1")
    }

    func testAnOlderComputerAnswering404ReadsAsNotFound() throws {
        let response = HubResponse(status: 404, body: #"{"ok":false,"error":"not found"}"#)
        XCTAssertThrowsError(try response.payload(MobileTopicList.self)) { error in
            XCTAssertTrue((error as? HubRequestError)?.isNotFound == true)
        }
        let paused = HubResponse(status: 409, body: #"{"ok":false,"error":"TODAY_CHAT_OBSERVATION_PAUSED"}"#)
        XCTAssertThrowsError(try paused.payload(MobileTopicPage.self)) { error in
            XCTAssertTrue((error as? HubRequestError)?.isObservationPaused == true)
        }
    }
}

// MARK: - Store and thread

@MainActor
final class TopicStoreTests: XCTestCase {
    private var scopes = PairingStore.requestedScopes
    private var online: Bool? = nil
    private var keys = Keys()

    override func setUp() async throws {
        try await super.setUp()
        scopes = PairingStore.requestedScopes
        online = nil
        keys = Keys()
    }

    override func tearDown() async throws {
        try await super.tearDown()
    }

    private func makeStore(_ hub: StubTopicHub, journal: PendingWriteJournal? = nil) throws -> (TopicStore, PendingWriteJournal) {
        let journal = try journal ?? PendingWriteJournal(fileURL: makeTemporaryDirectory().appendingPathComponent("writes.json"))
        let keys = keys
        let store = TopicStore(hub: hub, journal: journal, isPaired: { true }, scopes: { [unowned self] in self.scopes },
                               computerOnline: { [unowned self] in self.online }, makeKey: { keys.make() })
        return (store, journal)
    }

    private static let halcyon = MobileTopic(id: "project:halcyon", kind: "project", name: "Halcyon ERP sync",
                                             status: "waiting_on_you")

    private static func page(_ items: [MobileTopicItem], hasMore: Bool = false, cursor: String? = nil) -> MobileTopicPage {
        MobileTopicPage(sessionId: "main", topicId: halcyon.id, items: items, hasMore: hasMore, nextCursor: cursor)
    }

    private static func message(_ id: String, seq: Int, role: ChatRole, _ text: String) -> MobileTopicItem {
        MobileTopicItem(id: id, kind: "message", at: Date(), message: ChatItemView(seq: seq, stableID: id, role: role,
                                                                                    kind: .text, text: text, toolName: "",
                                                                                    at: "", permission: nil))
    }

    // MARK: Older computers

    func testWithheldBootstrapStaysVisibleUntilPublicationAndLastRemovalHidesIt() async throws {
        let hub = StubTopicHub()
        await hub.set(projection: MobileTopicList(topics: [], refreshing: true, hasTopics: true))
        let (store, _) = try makeStore(hub)
        await store.refresh()
        XCTAssertTrue(store.refreshing, "a successful bootstrap is still pending on the computer")
        XCTAssertTrue(store.hasTopics)
        XCTAssertTrue(store.showsTopics, "Feed must show its refreshing section with no rows")
        await hub.failList([HubRequestError.noAnswer])
        await store.refresh()
        XCTAssertTrue(store.refreshing, "a missing answer cannot confirm a pending bootstrap is empty")
        XCTAssertTrue(store.showsTopics)
        await hub.set(projection: MobileTopicList(topics: [Self.halcyon], hasTopics: true))
        await store.refresh()
        XCTAssertFalse(store.refreshing)
        XCTAssertTrue(store.showsTopics)
        XCTAssertEqual(store.topics, [Self.halcyon], "publication replaces the bootstrap on the next pending read")
        let calls = await hub.listCalls
        XCTAssertEqual(calls, 3, "pending does not block the next Feed read")
        await hub.set(projection: MobileTopicList(topics: [], hasTopics: false))
        await store.refresh()
        XCTAssertFalse(store.refreshing)
        XCTAssertFalse(store.hasTopics)
        XCTAssertFalse(store.showsTopics, "removing the last topic is authoritative emptiness")
    }

    func testKnownWithheldTopicsRemainVisibleAndUnpairClearsPendingState() async throws {
        let hub = StubTopicHub()
        await hub.set(projection: MobileTopicList(topics: [], hasTopics: true))
        let (store, _) = try makeStore(hub)
        await store.refresh()
        XCTAssertFalse(store.refreshing)
        XCTAssertTrue(store.showsTopics, "completed withholding offers Retry")
        await hub.set(projection: MobileTopicList(topics: [], refreshing: true, hasTopics: true))
        await store.refresh()
        store.clearAfterUnpair()
        XCTAssertFalse(store.refreshing)
        XCTAssertFalse(store.hasTopics)
        XCTAssertFalse(store.showsTopics)
    }

    func testAComputerWithoutTheTopicsFlagKeepsTodaysFeed() async throws {
        let hub = StubTopicHub()
        await hub.set(flag: false)
        let (store, _) = try makeStore(hub)
        await store.refresh()
        XCTAssertEqual(store.support, .unsupported)
        XCTAssertTrue(store.topics.isEmpty)
        let listCalls = await hub.listCalls
        XCTAssertEqual(listCalls, 0, "the list is never asked of a computer without the routes")
        // The next refresh asks again: the computer may have been updated since.
        await hub.set(flag: true)
        await hub.set(list: [Self.halcyon])
        await store.refresh()
        XCTAssertEqual(store.support, .supported)
        XCTAssertEqual(store.topics, [Self.halcyon])
    }

    func testARoute404MarksTheComputerAsOlderWithoutCallingItOffline() async throws {
        let hub = StubTopicHub()
        await hub.failList([HubRequestError.rejected(status: 404, message: "not found")])
        let (store, _) = try makeStore(hub)
        await store.refresh()
        XCTAssertEqual(store.support, .unsupported)
        XCTAssertTrue(store.topics.isEmpty)
    }

    func testNoAnswerKeepsTheTopicsRowAsItWas() async throws {
        let hub = StubTopicHub()
        await hub.set(list: [Self.halcyon])
        let (store, _) = try makeStore(hub)
        await store.refresh()
        await hub.failList([HubRequestError.noAnswer])
        await store.refresh()
        XCTAssertEqual(store.topics, [Self.halcyon])
        XCTAssertEqual(store.support, .supported)
        let statusCalls = await hub.statusCalls
        XCTAssertEqual(statusCalls, 1, "once the computer said yes, the list's own answer says if that changed")
    }

    func testTheListNeedsBriefRead() async throws {
        let hub = StubTopicHub()
        scopes = ["push"]
        let (store, _) = try makeStore(hub)
        await store.refresh()
        let calls = await hub.statusCalls
        XCTAssertEqual(calls, 0)
        XCTAssertEqual(store.support, .unknown)
    }

    // MARK: Reading a topic

    func testWithoutChatReadTheTopicListLoadsButTheScreenSaysNotAllowed() async throws {
        let hub = StubTopicHub()
        await hub.set(list: [Self.halcyon])
        scopes = ["brief:read", "chat:write"]
        let (store, _) = try makeStore(hub)
        await store.refresh()
        XCTAssertEqual(store.topics, [Self.halcyon], "brief:read is enough for the list")
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        XCTAssertEqual(thread.state, .notAllowed)
        let reads = await hub.pageCalls
        XCTAssertTrue(reads.isEmpty, "nothing is asked that the computer would refuse")
    }

    func testAForbiddenAnswerIsNotAllowedToo() async throws {
        let hub = StubTopicHub()
        await hub.failPages([HubRequestError.forbidden])
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        XCTAssertEqual(thread.state, .notAllowed)
    }

    func testATopicTheComputerNoLongerHasIsGoneAndAnOlderComputerIsUnsupported() async throws {
        let hub = StubTopicHub()
        await hub.failPages([HubRequestError.rejected(status: 404, message: "topic not found")])
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        XCTAssertEqual(store.support, .supported, "the screen asks first when it does not know yet")
        XCTAssertEqual(thread.state, .gone)

        let older = StubTopicHub()
        await older.set(flag: false)
        let (olderStore, _) = try makeStore(older)
        let olderThread = TopicThread(topicID: Self.halcyon.id, store: olderStore)
        await olderThread.load()
        XCTAssertEqual(olderThread.state, .unsupported)
        let reads = await older.pageCalls
        XCTAssertTrue(reads.isEmpty)
    }

    func testPausedObservingShowsItsOwnState() async throws {
        let hub = StubTopicHub()
        await hub.failPages([HubRequestError.conflict(message: "TODAY_CHAT_OBSERVATION_PAUSED")])
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        XCTAssertEqual(thread.state, .paused)
    }

    func testOfflineTheScreenSaysSoAndTheBoxWaitsForTheComputer() async throws {
        let hub = StubTopicHub()
        await hub.failPages([HubRequestError.noAnswer, HubRequestError.noAnswer])
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        XCTAssertEqual(thread.state, .offline)
        XCTAssertTrue(thread.computerOffline)
        thread.edit("Could we hit Oct 30?")
        let sent = await thread.send()
        XCTAssertFalse(sent)
        XCTAssertEqual(thread.notice, TopicCopy.waitsForComputer)
        XCTAssertEqual(thread.draft, "Could we hit Oct 30?", "the words stay in the box")
        let calls = await hub.sent
        XCTAssertTrue(calls.isEmpty)
        let reads = await hub.pageCalls
        XCTAssertEqual(reads.count, 2, "Send asked the computer again before giving up")
    }

    func testWhenTheComputerIsBackSendAsksAgainAndSends() async throws {
        let hub = StubTopicHub()
        await hub.failPages([HubRequestError.noAnswer])
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        XCTAssertTrue(thread.computerOffline)
        thread.edit("Could we hit Oct 30?")
        let sent = await thread.send()
        XCTAssertTrue(sent)
        XCTAssertFalse(thread.computerOffline)
        let calls = await hub.sent
        XCTAssertEqual(calls.map(\.text), ["Could we hit Oct 30?"])
        thread.stop()
    }

    func testTheRelaySayingOfflineStartsTheScreenOffline() async throws {
        online = false
        let (store, _) = try makeStore(StubTopicHub())
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        XCTAssertTrue(thread.computerOffline)
        await thread.load()
        XCTAssertFalse(thread.computerOffline, "a page that arrives proves the computer answered")
        XCTAssertEqual(thread.state, .loaded)
    }

    func testRowsStayOnScreenWhenARefreshGetsNoAnswer() async throws {
        let hub = StubTopicHub()
        await hub.set(page: Self.page([Self.message("row-1", seq: 1, role: .assistant, "Hello")]))
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        await hub.failPages([HubRequestError.noAnswer])
        await thread.load()
        XCTAssertEqual(thread.state, .loaded)
        XCTAssertEqual(thread.items.map(\.id), ["row-1"])
        XCTAssertEqual(thread.notice, TopicCopy.showingSaved)
    }

    func testEarlierPagesGoAboveAndStayThroughARefresh() async throws {
        let hub = StubTopicHub()
        let newest = [Self.message("row-3", seq: 3, role: .user, "Three"), Self.message("row-4", seq: 4, role: .assistant, "Four")]
        await hub.set(page: Self.page(newest, hasMore: true, cursor: "c1"))
        await hub.set(page: Self.page([Self.message("row-1", seq: 1, role: .user, "One")], hasMore: false), cursor: "c1")
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        XCTAssertTrue(thread.hasMore)
        await thread.loadEarlier()
        XCTAssertEqual(thread.items.map(\.id), ["row-1", "row-3", "row-4"])
        XCTAssertFalse(thread.hasMore)
        await thread.load()
        XCTAssertEqual(thread.items.map(\.id), ["row-1", "row-3", "row-4"], "the newest page replaces only its own rows")
        let reads = await hub.pageCalls
        XCTAssertEqual(reads, [nil, "c1", nil], "the cursor comes from the computer, never from the rows")
    }

    // MARK: The box and the cards

    func testTheBoxSurvivesLeavingTheScreenAndSendsNothingOnItsOwn() async throws {
        let hub = StubTopicHub()
        let (store, journal) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        thread.edit("Keep Nov 1 in the reply.")
        let again = TopicThread(topicID: Self.halcyon.id, store: store)
        XCTAssertEqual(again.draft, "Keep Nov 1 in the reply.")
        let sent = await hub.sent
        XCTAssertTrue(sent.isEmpty, "typing sends nothing; only Send sends")
        let writes = await journal.entries()
        XCTAssertTrue(writes.isEmpty)
    }

    func testAPageNamesTheMainChatThatCardActionsGoTo() async throws {
        let hub = StubTopicHub()
        await hub.set(page: MobileTopicPage(sessionId: "main-session", topicId: Self.halcyon.id, items: [],
                                            hasMore: false, nextCursor: nil))
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        XCTAssertNil(thread.sessionID)
        await thread.load()
        XCTAssertEqual(thread.sessionID, "main-session")
        await hub.set(page: MobileTopicPage(sessionId: "bad/id", topicId: Self.halcyon.id, items: [], hasMore: false,
                                            nextCursor: nil))
        await thread.load()
        XCTAssertEqual(thread.sessionID, "main-session", "an id the phone cannot put in a path is not used")
    }

    func testAReplyCardOffersSendOnlyForACompleteOfferedDraftAndKeepsNotNow() throws {
        let card = try decode(ChatProactiveView.self, """
        {"momentId":"m1","kind":"waiting_on_reply","headline":"Halcyon is waiting","body":"A reply is drafted.",
         "draft":true,"actionIds":["copy_draft","open","send","not_now","dismiss"],
         "actionLabels":{"copy_draft":"Copy draft","open":"Open Mail","send":"Send","not_now":"Not now","dismiss":"Dismiss"},
         "draftContent":{"to":"ops@example.com","subject":"Halcyon","text":"Nov 1 still works for us.",
                         "channel":"mail","recipient":"Ops lead","principalId":"person-ops","revision":"rev-1"},
         "target":{"kind":"mail"},"evidence":[],"state":"pending"}
        """)
        XCTAssertEqual(TopicReplyCard.sendAction(for: card, language: "en")?.label, "Send the reply")
        XCTAssertEqual(TopicReplyCard.notNowAction(for: card, language: "en")?.label, "Not now")
        XCTAssertFalse(DecisionAuthenticationPolicy.requiresOwnerConfirmation(proactiveActionID: "send",
                                                                                permissionRequest: false),
                       "the visible draft and Send tap are the authority; no Face ID")
        XCTAssertFalse(DecisionAuthenticationPolicy.requiresOwnerConfirmation(proactiveActionID: "not_now",
                                                                                permissionRequest: false),
                       "Not now asks for no Face ID")
        var answered = card
        answered.state = "deferred"
        XCTAssertNil(TopicReplyCard.sendAction(for: answered, language: "en"))
        XCTAssertNil(TopicReplyCard.notNowAction(for: answered, language: "en"), "a card already answered offers nothing")
        let incomplete = try decode(ChatProactiveView.self, """
        {"momentId":"m2","kind":"waiting_on_reply","headline":"H","body":"B","draft":true,
         "actionIds":["send","not_now"],"actionLabels":{"send":"Send","not_now":"Not now"},
         "draftContent":{"to":"ops@example.com","subject":"Q3","text":""},"state":"pending"}
        """)
        XCTAssertNil(TopicReplyCard.sendAction(for: incomplete, language: "en"))
        XCTAssertNotNil(TopicReplyCard.notNowAction(for: incomplete, language: "en"))
        let row = MobileTopicItem(id: "row", kind: "message", at: Date(),
                                  message: ChatItemView(seq: 1, stableID: "row", role: .system, kind: .proactive,
                                                        text: "H", toolName: "main_chat_proactive", at: "",
                                                        permission: nil, proactive: card))
        XCTAssertTrue(TopicRow.isShown(row))
        var unavailable = card
        unavailable.kind = "thread_update_unavailable"
        let withdrawn = MobileTopicItem(id: "withdrawn", kind: "message", at: Date(),
                                        message: ChatItemView(seq: 2, stableID: "withdrawn", role: .system,
                                                              kind: .proactive, text: "Unavailable", toolName: "",
                                                              at: "", permission: nil, proactive: unavailable))
        XCTAssertFalse(TopicRow.isShown(withdrawn))
    }

    func testWordsTypedWhileSendingStayInTheBox() async throws {
        let hub = StubTopicHub()
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        thread.edit("First")
        let sending = Task { await thread.send() }
        await Task.yield()
        thread.edit("First, and more")
        let sent = await sending.value
        XCTAssertTrue(sent)
        XCTAssertEqual(thread.draft, "First, and more")
        thread.stop()
    }

    func testAnEmptyNewestPageKeepsTheRowsOnScreen() async throws {
        let hub = StubTopicHub()
        await hub.set(page: Self.page([Self.message("row-1", seq: 1, role: .assistant, "Hello")]))
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        await hub.set(page: Self.page([], hasMore: true, cursor: "c1"))
        await thread.load()
        XCTAssertEqual(thread.items.map(\.id), ["row-1"], "hidden newest rows do not empty the screen")
        await hub.set(page: Self.page([], hasMore: false))
        await thread.load()
        XCTAssertTrue(thread.items.isEmpty, "an empty topic is empty")
    }

    func testAfterLeavingTheScreenASendStillShowsButNothingReadsOn() async throws {
        let hub = StubTopicHub()
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        thread.edit("Hello")
        let sending = Task { await thread.send() }
        thread.stop()
        let sent = await sending.value
        XCTAssertTrue(sent)
        XCTAssertNotNil(thread.pending)
        XCTAssertFalse(thread.awaitingReply, "no reading on for a reply nobody is looking at")
    }

    func testABriefQuestionPutsItsWordsInTheMainChatBoxAndSendsNothing() async throws {
        let directory = try makeTemporaryDirectory()
        let hub = QuestionChatHub()
        let chat = ChatStore(hub: hub, journal: PendingWriteJournal(fileURL: directory.appendingPathComponent("w.json")),
                             guardian: LocalDecisionGuard(), presence: { true }, isPaired: { true },
                             cache: ChatCache(directory: directory.appendingPathComponent("chats")))
        var brief = MobileBriefV1(briefId: "2026-10-22-morning", slot: "morning", localDate: "2026-10-22",
                                  narrative: [BriefParagraph(text: "Halcyon wants Oct 30.", topicId: "project:halcyon",
                                                             topicName: "Halcyon ERP sync")],
                                  conversationKey: "rev-1")
        let question = MobileBriefCard(id: "q1", kind: "question", mode: "send", message: "Could we hit Oct 30?",
                                       refs: ["e1"], paragraph: 0)
        brief.cards = [question]
        XCTAssertTrue(chat.prefillBriefCard(question, from: brief))
        let sends = await hub.sends
        XCTAssertEqual(sends, 0, "a question fills the box; only Send sends")
        await chat.refreshList()
        _ = chat.prepareMainChat()
        XCTAssertEqual(chat.takeReturnedDrafts(in: "main", appendingTo: ""), "Could we hit Oct 30?")
        XCTAssertEqual(chat.composerDraft(in: "main").briefReference?.cardId, "q1",
                       "Send names the card, so the computer keeps its topic")
    }

    func testQuestionsAndAnswersOfflineOnlySayTheyWait() {
        XCTAssertEqual(BriefCardsView.offlineNotice(computerOnline: false), BriefConversationCopy.waitsForComputer)
        XCTAssertNil(BriefCardsView.offlineNotice(computerOnline: true))
        XCTAssertNil(BriefCardsView.offlineNotice(computerOnline: nil), "unknown is not offline")
    }

    // MARK: Sending

    func testATopicSendIsJournaledAndARetryReusesItsKey() async throws {
        let hub = StubTopicHub()
        await hub.failSends([HubRequestError.noAnswer])
        let (store, journal) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        thread.edit("  Could we hit Oct 30?  ")
        let first = await thread.send()
        XCTAssertFalse(first)
        XCTAssertEqual(thread.notice, TopicCopy.noAnswer)
        XCTAssertEqual(thread.draft, "  Could we hit Oct 30?  ", "unanswered words stay in the box")
        XCTAssertTrue(thread.computerOffline)
        let kept = await journal.entries()
        XCTAssertEqual(kept.map(\.operation), [.topicMessage])
        XCTAssertEqual(kept.first?.target, Self.halcyon.id)

        await thread.load()
        let second = await thread.send()
        XCTAssertTrue(second)
        let sent = await hub.sent
        XCTAssertEqual(sent.count, 2)
        XCTAssertEqual(sent.map(\.text), ["Could we hit Oct 30?", "Could we hit Oct 30?"])
        XCTAssertEqual(sent[0].key, sent[1].key, "the computer sends a retried message once")
        XCTAssertEqual(thread.draft, "")
        XCTAssertEqual(thread.pending, TopicThread.Pending(text: "Could we hit Oct 30?", seq: 92))
        let left = await journal.entries()
        XCTAssertTrue(left.isEmpty, "an answered key is forgotten")
        thread.stop()
    }

    func testOtherWordsGetTheirOwnKey() async throws {
        let hub = StubTopicHub()
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        thread.edit("First")
        _ = await thread.send()
        thread.edit("Second")
        _ = await thread.send()
        let sent = await hub.sent
        XCTAssertEqual(Set(sent.map(\.key)).count, 2)
        thread.stop()
    }

    func testABusyMainChatKeepsTheWordsAndDropsTheKey() async throws {
        let hub = StubTopicHub()
        await hub.failSends([ChatHubError.busy])
        let (store, journal) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        thread.edit("Draft the reply")
        let sent = await thread.send()
        XCTAssertFalse(sent)
        XCTAssertEqual(thread.notice, TopicCopy.busy)
        XCTAssertEqual(thread.draft, "Draft the reply")
        XCTAssertFalse(thread.computerOffline, "busy is an answer")
        let kept = await journal.entries()
        XCTAssertTrue(kept.isEmpty, "nothing was taken, so a later send starts afresh")
    }

    func testRefusalsSayWhyAndKeepTheWords() async throws {
        let cases: [(Error, String)] = [
            (HubRequestError.conflict(message: "TODAY_CHAT_OBSERVATION_PAUSED"), TopicCopy.paused),
            (HubRequestError.conflict(message: "source access changed"), TopicCopy.changed),
            (HubRequestError.rejected(status: 404, message: "topic not found"), TopicCopy.gone),
            (HubRequestError.forbidden, TopicCopy.sendNotAllowed),
        ]
        for (error, expected) in cases {
            let hub = StubTopicHub()
            await hub.failSends([error])
            let (store, _) = try makeStore(hub)
            let thread = TopicThread(topicID: Self.halcyon.id, store: store)
            await thread.load()
            thread.edit("Words")
            let sent = await thread.send()
            XCTAssertFalse(sent)
            XCTAssertEqual(thread.notice, expected, "\(error)")
            XCTAssertEqual(thread.draft, "Words")
        }
    }

    func testWithoutChatWriteNothingIsSent() async throws {
        let hub = StubTopicHub()
        scopes = ["brief:read", "chat:read"]
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        thread.edit("Hello")
        let sent = await thread.send()
        XCTAssertFalse(sent)
        XCTAssertEqual(thread.notice, TopicCopy.sendNotAllowed)
        let calls = await hub.sent
        XCTAssertTrue(calls.isEmpty)
    }

    func testTooLongWordsAreRefusedBeforeAnythingIsSaved() async throws {
        let hub = StubTopicHub()
        let (store, journal) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        thread.edit(String(repeating: "a", count: TopicThread.maxMessageBytes + 1))
        let sent = await thread.send()
        XCTAssertFalse(sent)
        XCTAssertEqual(thread.notice, TopicCopy.tooLong)
        let writes = await journal.entries()
        XCTAssertTrue(writes.isEmpty)
    }

    func testTurnEventRefreshesOnlyTheMatchingChat() async throws {
        let hub = StubTopicHub()
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        await hub.set(page: Self.page([Self.message("new", seq: 2, role: .assistant, "A new reply")]))
        func event(_ chatID: String) throws -> MobileChatTurnEvent {
            let data = try JSONSerialization.data(withJSONObject: ["event": "chat_turn", "chatId": chatID,
                                                                    "turnId": "turn", "state": "done", "seq": 2])
            return try JSONDecoder().decode(MobileChatTurnEvent.self, from: data)
        }
        await thread.receiveTurn(try event("other"))
        XCTAssertFalse(thread.items.contains { $0.id == "new" })
        await thread.receiveTurn(try event("main"))
        XCTAssertTrue(thread.items.contains { $0.id == "new" })
    }

    func testThePendingMessageLeavesWhenAReadShowsItAndTheReplyEndsTheWait() async throws {
        let hub = StubTopicHub()
        let (store, _) = try makeStore(hub)
        let thread = TopicThread(topicID: Self.halcyon.id, store: store)
        await thread.load()
        thread.edit("Could we hit Oct 30?")
        _ = await thread.send()
        XCTAssertNotNil(thread.pending)
        XCTAssertTrue(thread.awaitingReply)
        await hub.set(page: Self.page([Self.message("row-91", seq: 91, role: .user, "Could we hit Oct 30?"),
                                       Self.message("row-92", seq: 92, role: .assistant, "Only by skipping reviews.")]))
        await thread.load()
        XCTAssertNil(thread.pending, "the stored row replaces it")
        XCTAssertFalse(thread.awaitingReply)
        thread.stop()
    }

    func testUnpairingForgetsTopicsAndBoxes() async throws {
        let hub = StubTopicHub()
        await hub.set(list: [Self.halcyon])
        let (store, _) = try makeStore(hub)
        await store.refresh()
        store.saveDraft(TopicStore.Draft(text: "Hello"), for: Self.halcyon.id)
        store.clearAfterUnpair()
        XCTAssertEqual(store.support, .unknown)
        XCTAssertTrue(store.topics.isEmpty)
        XCTAssertEqual(store.draft(for: Self.halcyon.id), TopicStore.Draft())
    }
}

/// The chat for the question test: it lists the chat and counts any send.
private actor QuestionChatHub: ChatHubAPI {
    var sends = 0
    func chats(limit: Int) async throws -> [ChatSummaryView] {
        [ChatSummaryView(id: "main", title: "Isoform", updatedAt: "", state: .idle, kind: .main)]
    }
    func chat(id: String, after: Int?, limit: Int) async throws -> ChatDetail {
        ChatDetail(chat: ChatHeaderView(id: id, title: "Isoform", state: .idle), items: [], pendingPermission: nil,
                   cursor: 0, hasMore: false)
    }
    func createChat(title: String, idempotencyKey: String) async throws -> String { "unused" }
    func sendMessage(chatID: String, text: String, idempotencyKey: String) async throws -> ChatMessageAccepted {
        sends += 1
        return ChatMessageAccepted(turnId: "turn")
    }
    func sendBriefMessage(chatID: String, text: String, reference: ChatBriefReference,
                          idempotencyKey: String) async throws -> ChatMessageAccepted {
        sends += 1
        return ChatMessageAccepted(turnId: "turn")
    }
    func answerPermission(chatID: String, requestID: String, optionID: String, idempotencyKey: String) async throws -> Bool { false }
    func cancel(chatID: String, idempotencyKey: String) async throws -> Bool { false }
    func pollChatTurns() async throws -> [MobileChatTurnEvent] { [] }
}

// MARK: - Strings

/// Every string the Feed and the Topic screen show goes through Localizable.xcstrings, in English and Chinese.
final class FeedLocalizationTests: XCTestCase {
    private static let root = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    private static let files = ["FeedView.swift", "BriefCardsView.swift", "TopicView.swift", "TopicStore.swift",
                                "FeedCard.swift", "FeedAssembly.swift", "BriefConversation.swift"]

    func testEveryFeedAndTopicStringHasEnglishAndChinese() throws {
        let data = try Data(contentsOf: Self.root.appendingPathComponent("ios/Yansu/Resources/Localizable.xcstrings"))
        let catalog = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let strings = try XCTUnwrap(catalog["strings"] as? [String: [String: Any]])
        // Literal keys SwiftUI and String(localized:) look up. Interpolations become %@ or %lld.
        let pattern = try NSRegularExpression(pattern:
            #"(?:(?:Text|Button|Label|ProgressView|TextField|accessibilityLabel|accessibilityHint|navigationTitle|sectionLabel)\(|String\(localized:)\s*"((?:[^"\\]|\\.)*)""#)
        let interpolation = try NSRegularExpression(pattern: #"\\\(((?:[^()]|\([^()]*\))*)\)"#)
        var checked = 0
        for file in Self.files {
            let source = try String(contentsOf: Self.root.appendingPathComponent("ios/Yansu/\(file)"), encoding: .utf8)
            for match in pattern.matches(in: source, range: NSRange(source.startIndex..., in: source)) {
                guard let range = Range(match.range(at: 1), in: source) else { continue }
                let raw = String(source[range])
                let key = Self.key(raw, interpolation: interpolation)
                // Product names and symbols are not words to translate.
                guard key.rangeOfCharacter(from: .letters) != nil,
                      !["Isoform", "%@", "%lld"].contains(key) else { continue }
                checked += 1
                let entry = strings[key]
                let localizations = entry?["localizations"] as? [String: Any]
                XCTAssertNotNil(localizations?["en"], "\(file): \"\(key)\" has no English entry")
                XCTAssertNotNil(localizations?["zh-Hans"], "\(file): \"\(key)\" has no Chinese entry")
            }
        }
        XCTAssertGreaterThan(checked, 80, "the scan found the screens' strings")
    }

    func testRetiredBriefActionLabelsUseLocalizedNotNowCopy() {
        XCTAssertTrue(BriefCardsView.usesNotNowLabel("Skip"))
        XCTAssertTrue(BriefCardsView.usesNotNowLabel("Later"))
        XCTAssertTrue(BriefCardsView.usesNotNowLabel("Not now"))
        XCTAssertTrue(BriefCardsView.usesNotNowLabel("跳过"))
        XCTAssertTrue(BriefCardsView.usesNotNowLabel("稍后"))
        XCTAssertFalse(BriefCardsView.usesNotNowLabel("Draft a plan"))
    }

    private static func key(_ raw: String, interpolation: NSRegularExpression) -> String {
        var key = ""
        var last = raw.startIndex
        for match in interpolation.matches(in: raw, range: NSRange(raw.startIndex..., in: raw)) {
            guard let whole = Range(match.range, in: raw), let inner = Range(match.range(at: 1), in: raw) else { continue }
            key += raw[last..<whole.lowerBound]
            let expression = raw[inner]
            let isInteger = ["newStories", "others", "minutes", "count"].contains { expression.contains($0) }
            key += isInteger ? "%lld" : "%@"
            last = whole.upperBound
        }
        key += raw[last...]
        return key.replacingOccurrences(of: #"\""#, with: "\"")
    }
}
