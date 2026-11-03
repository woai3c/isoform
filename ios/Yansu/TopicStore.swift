import Foundation

/// "Your topics" on the Feed (PROTOCOL.md 15.8). The list needs `brief:read`. A computer whose `/status` has no
/// `topics` flag, or whose `/topics` answers 404, has no topics yet: the Feed then stays as it was, with no row and no
/// topic buttons, and nothing calls the computer offline for it.
@MainActor
final class TopicStore: ObservableObject {
    enum Support: Equatable {
        case unknown
        case supported
        case unsupported
    }

    /// The words in one topic's box.
    struct Draft: Equatable {
        var text = ""
    }

    @Published private(set) var support: Support = .unknown
    @Published private(set) var topics: [MobileTopic] = []
    @Published private(set) var refreshing = false
    @Published private(set) var hasTopics = false
    private var loading = false

    let hub: TopicHubAPI
    let writer: JournaledWriter
    let scopes: @MainActor () -> [String]
    /// What the phone last heard about the computer: false only when the relay said it is offline.
    let computerOnline: @MainActor () -> Bool?
    private let isPaired: @MainActor () -> Bool
    /// Each topic's box, kept while the app runs so leaving the Topic screen does not lose it.
    private var drafts: [String: Draft] = [:]
    /// Changes when the pairing goes, so nothing read or sent for the old pairing lands after it.
    private(set) var generation = UUID()

    init(hub: TopicHubAPI,
         journal: PendingWriteJournal,
         isPaired: @escaping @MainActor () -> Bool,
         scopes: @escaping @MainActor () -> [String],
         computerOnline: @escaping @MainActor () -> Bool? = { nil },
         now: @escaping @Sendable () -> Date = { Date() },
         makeKey: @escaping @Sendable () -> String = { UUID().uuidString.lowercased() }) {
        self.hub = hub
        self.isPaired = isPaired
        self.scopes = scopes
        self.computerOnline = computerOnline
        self.writer = JournaledWriter(journal: journal, makeKey: makeKey, now: now)
    }

    var canList: Bool { isPaired() && scopes().contains("brief:read") }
    var showsTopics: Bool { support == .supported && (!topics.isEmpty || refreshing || hasTopics) }

    /// Asks the computer whether it has topics (once it said yes, the list's own 404 answers that), then reads the
    /// list. A read that gets no answer keeps the row as it was.
    func refresh() async {
        guard canList, !loading else { return }
        let current = generation
        var pending = refreshing
        loading = true
        refreshing = true
        defer {
            if current == generation {
                loading = false
                refreshing = pending
            }
        }
        do {
            if support != .supported {
                let supported = try await hub.supportsTopics()
                guard current == generation else { return }
                guard supported else {
                    support = .unsupported
                    topics = []
                    hasTopics = false
                    pending = false
                    return
                }
                support = .supported
            }
            let list = try await hub.topics()
            guard current == generation else { return }
            topics = list.topics
            hasTopics = list.hasTopics || !list.topics.isEmpty
            pending = list.refreshing
        } catch {
            guard current == generation else { return }
            switch error {
            case let error as HubRequestError where error.isNotFound:
                support = .unsupported
                topics = []
                hasTopics = false
                pending = false
            case HubRequestError.forbidden:
                topics = []
                hasTopics = false
                pending = false
            default:
                break
            }
        }
    }

    func topic(_ id: String) -> MobileTopic? {
        topics.first { $0.id == id }
    }

    func draft(for topicID: String) -> Draft {
        drafts[topicID] ?? Draft()
    }

    func saveDraft(_ draft: Draft, for topicID: String) {
        drafts[topicID] = draft.text.isEmpty ? nil : draft
    }

    func clearAfterUnpair() {
        generation = UUID()
        support = .unknown
        topics = []
        drafts = [:]
        refreshing = false
        hasTopics = false
        loading = false
    }
}

/// One topic's screen: the chat filtered to the topic, paged from the newest, and a box that sends to the chat
/// with the topic attached. Reading needs `chat:read`; a phone without it sees a "not allowed" state. Sending
/// needs `chat:write` and goes through the write journal, so a retry after a lost answer reuses its key and the
/// computer sends it once. Nothing here asks for Face ID: composing in the chat needs no owner check.
/// Cards show a complete prepared reply and use the chat action route for Send and Not now.
@MainActor
final class TopicThread: ObservableObject {
    enum State: Equatable {
        case loading
        case loaded
        /// This phone's pairing does not allow reading the chat.
        case notAllowed
        /// The computer has no topic routes yet.
        case unsupported
        /// The topic is not on the computer, or its sources are no longer readable.
        case gone
        /// Observing is paused on the computer, which shows no topic then.
        case paused
        /// No answer from the computer, and nothing read yet.
        case offline
        case failed
    }

    /// What this phone just sent, shown under the rows until a read shows the stored message.
    struct Pending: Equatable {
        let text: String
        let seq: Int?
    }

    @Published private(set) var state: State = .loading
    @Published private(set) var items: [MobileTopicItem] = []
    @Published private(set) var hasMore = false
    @Published private(set) var loadingEarlier = false
    @Published private(set) var sending = false
    @Published private(set) var pending: Pending?
    @Published private(set) var awaitingReply = false
    /// No answer came from the computer on the last read or send, or the relay says it is offline.
    @Published private(set) var computerOffline: Bool
    @Published var notice: String?
    @Published private(set) var draft: String
    /// The chat the topic filters, from the last page read: a card's action goes to it.
    @Published private(set) var sessionID: String?

    static let pageLimit = 50
    static let maxMessageBytes = 16 * 1024

    let topicID: String
    private let store: TopicStore
    private var olderCursor: String?
    /// False once the screen goes away: a send that finishes later still shows, but nothing reads on for the reply.
    private var visible = true

    init(topicID: String, store: TopicStore) {
        self.topicID = topicID
        self.store = store
        let saved = store.draft(for: topicID)
        draft = saved.text
        computerOffline = store.computerOnline() == false
    }

    var canRead: Bool { store.scopes().contains("chat:read") }
    var canSend: Bool { store.scopes().contains("chat:write") }

    /// Reads the newest page. Rows already on screen stay while it reads, and earlier pages stay above it.
    func load() async {
        visible = true
        guard canRead else {
            state = .notAllowed
            return
        }
        if store.support == .unknown { await store.refresh() }
        if store.support == .unsupported {
            state = .unsupported
            return
        }
        let current = store.generation
        if items.isEmpty, state != .loaded { state = .loading }
        do {
            let page = try await store.hub.topic(id: topicID, cursor: nil, limit: Self.pageLimit)
            guard current == store.generation else { return }
            computerOffline = false
            applyNewest(page)
            state = .loaded
        } catch {
            guard current == store.generation else { return }
            readFailed(error)
        }
    }

    /// A turn in this main chat may have added a reply to the topic.
    func receiveTurn(_ event: MobileChatTurnEvent) async {
        guard visible, event.chatId == sessionID else { return }
        await load()
    }

    /// The page before the earliest row on screen.
    func loadEarlier() async {
        guard canRead, hasMore, let cursor = olderCursor, !loadingEarlier else { return }
        let current = store.generation
        loadingEarlier = true
        defer { if current == store.generation { loadingEarlier = false } }
        do {
            let page = try await store.hub.topic(id: topicID, cursor: cursor, limit: Self.pageLimit)
            guard current == store.generation else { return }
            computerOffline = false
            let shown = Set(items.map(\.id))
            items = page.items.filter { !shown.contains($0.id) } + items
            olderCursor = page.nextCursor
            hasMore = page.hasMore && page.nextCursor != nil
        } catch {
            guard current == store.generation else { return }
            if case HubRequestError.rejected(400, _) = error {
                // The cursor no longer matches what the computer holds: start again from the newest page.
                items = []
                await load()
                return
            }
            readFailed(error)
        }
    }

    /// What the user types in the box.
    func edit(_ text: String) {
        guard text != draft else { return }
        setDraft(text)
    }

    private func setDraft(_ text: String) {
        draft = text
        store.saveDraft(TopicStore.Draft(text: draft), for: topicID)
    }

    /// Sends the box to the chat with this topic attached. True once the computer took it.
    @discardableResult
    func send() async -> Bool {
        let typed = draft
        let text = typed.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !sending else { return false }
        guard canSend else {
            notice = TopicCopy.sendNotAllowed
            return false
        }
        guard text.utf8.count <= Self.maxMessageBytes else {
            notice = TopicCopy.tooLong
            return false
        }
        let current = store.generation
        sending = true
        notice = nil
        defer { if current == store.generation { sending = false } }
        if computerOffline {
            // Ask again first: the computer may be back. Still no answer, and the words wait in the box.
            await load()
            guard current == store.generation else { return false }
            guard !computerOffline else {
                notice = TopicCopy.waitsForComputer
                return false
            }
        }
        let topicID = topicID
        let hub = store.hub
        // The same words to the same topic reuse the key the journal kept, so the computer sends them once.
        let outcome = await store.writer.send(.topicMessage, target: topicID, choice: "message",
                                              fingerprint: WriteFingerprint.make([topicID, text])) { key in
            try await hub.sendTopicMessage(topicID: topicID, text: text, idempotencyKey: key)
        }
        guard current == store.generation else { return false }
        switch outcome {
        case .sent(let accepted):
            computerOffline = false
            pending = Pending(text: text, seq: accepted.seq)
            // Words typed while it was sending stay in the box.
            if draft == typed { setDraft("") }
            if visible {
                awaitingReply = true
                await load()
            }
            return true
        case .notSaved:
            notice = TopicCopy.saveFailed
        case .cancelled:
            break
        case .failed(let error, let disposition):
            sendFailed(error, disposition: disposition)
        }
        return false
    }

    /// Leaving the screen stops reading for the reply. The box stays for the next visit.
    func stop() {
        visible = false
        awaitingReply = false
    }

    // MARK: Pages

    private func applyNewest(_ page: MobileTopicPage) {
        if !page.sessionId.isEmpty, Identifiers.isSafePathID(page.sessionId) { sessionID = page.sessionId }
        if page.items.isEmpty, page.hasMore, !items.isEmpty {
            // The newest rows are all ones the phone does not show; the rows on screen are still the topic's.
            return
        }
        if let first = page.items.first, let overlap = items.firstIndex(where: { $0.id == first.id }) {
            // Earlier pages read before stay above the newest one; their cursor still applies.
            items = Array(items[..<overlap]) + page.items
        } else {
            items = page.items
            olderCursor = page.nextCursor
            hasMore = page.hasMore && page.nextCursor != nil
        }
        if let pending, items.contains(where: { Self.shows(pending, $0) }) {
            self.pending = nil
        }
        if awaitingReply, hasReply {
            awaitingReply = false
        }
    }

    /// A stored user row is the pending message: by its seq when the computer gave one, otherwise by its words.
    private static func shows(_ pending: Pending, _ item: MobileTopicItem) -> Bool {
        guard let message = item.message, message.role == .user else { return false }
        if let seq = pending.seq, seq > 0 { return message.seq == seq }
        return message.text.trimmingCharacters(in: .whitespacesAndNewlines) == pending.text
    }

    /// Isoform answered after the last message from this phone.
    private var hasReply: Bool {
        guard let index = items.lastIndex(where: { $0.message?.role == .user }) else { return false }
        return items[(index + 1)...].contains { $0.message?.role == .assistant }
    }

    // MARK: Failures

    private func readFailed(_ error: Error) {
        switch error {
        case is CancellationError:
            return
        case HubRequestError.forbidden:
            state = .notAllowed
        case let error as HubRequestError where error.isNotFound:
            // Topic buttons come only from computers with the routes, so a 404 means the topic went, unless the
            // computer said it has none.
            state = store.support == .unsupported ? .unsupported : .gone
        case let error as HubRequestError where error.isObservationPaused:
            state = .paused
        default:
            if Self.isUnanswered(error) {
                computerOffline = true
                if items.isEmpty { state = .offline } else { notice = TopicCopy.showingSaved }
            } else if items.isEmpty {
                state = .failed
            } else {
                notice = TopicCopy.refreshFailed
            }
        }
    }

    private func sendFailed(_ error: Error, disposition: WriteDisposition) {
        switch error {
        case ChatHubError.busy:
            notice = TopicCopy.busy
        case HubRequestError.forbidden:
            notice = TopicCopy.sendNotAllowed
        case let error as HubRequestError where error.isObservationPaused:
            notice = TopicCopy.paused
        case let error as HubRequestError where error.isNotFound:
            notice = TopicCopy.gone
        default:
            switch disposition {
            case .keep:
                // The journal keeps the key either way, so sending the same words again is applied once.
                if Self.isUnanswered(error) {
                    computerOffline = true
                    notice = TopicCopy.noAnswer
                } else {
                    notice = TopicCopy.notSent
                }
            case .changed:
                notice = TopicCopy.changed
                Task { await load() }
            case .drop:
                notice = TopicCopy.notSent
            }
        }
    }

    /// The request may not have reached the computer: it is asleep, offline, or its queue is full.
    private static func isUnanswered(_ error: Error) -> Bool {
        switch error {
        case HubRequestError.noAnswer, HubRequestError.queueFull, RelayClientError.offline, RelayClientError.timedOut:
            return true
        default:
            return false
        }
    }
}

enum TopicCopy {
    static var showingSaved: String {
        String(localized: "Your computer is offline. This shows what Isoform read before.")
    }
    static var refreshFailed: String { String(localized: "Isoform could not refresh this topic. Try again.") }
    static var waitsForComputer: String {
        String(localized: "Your computer is offline. Your message waits here until it is back.")
    }
    static var noAnswer: String {
        String(localized: "Your computer has not answered yet. Your message is still here. Send it again when your computer is back; it is sent only once.")
    }
    static var busy: String {
        String(localized: "Isoform is still answering in your main chat. Your message is still here. Send it when the reply finishes.")
    }
    static var paused: String {
        String(localized: "Isoform is paused on your computer.")
    }
    static var changed: String {
        String(localized: "This topic changed on your computer. Your message is still here. Send it again to use the latest.")
    }
    static var gone: String {
        String(localized: "This topic is no longer on your computer. Your message is still here.")
    }
    static var notSent: String {
        String(localized: "Your computer did not take this message. It is still here, so you can send it again.")
    }
    static var sendNotAllowed: String {
        String(localized: "Pair this phone again in Settings to send messages from it.")
    }
    static var tooLong: String { String(localized: "That message is too long for Isoform. Shorten it and try again.") }
    static var saveFailed: String {
        String(localized: "Isoform could not save this on this phone, so nothing was sent to your computer. Try again.")
    }
}
