import Foundation

/// Chat models read from the Mac over the relay. Every field decodes tolerantly: a missing or unknown value
/// becomes an empty string, zero, or an `.unknown` case, and a malformed item is dropped rather than failing
/// the whole transcript. The enums keep the wire values as raw values so the phone cache can store them.

enum ChatState: String, Codable, Equatable, Sendable {
    case idle
    case running
    case waiting
    case unknown

    init(wire: String?) {
        self = ChatState(rawValue: wire ?? "") ?? .unknown
    }

    var isBusy: Bool {
        self == .running
    }

    var label: String {
        switch self {
        case .running: return "Working"
        case .waiting: return "Waiting on you"
        case .idle, .unknown: return "Ready"
        }
    }
}

enum ChatKind: String, Codable, Equatable, Sendable {
    /// The one chat the user keeps talking in; background work reports into it.
    case main
    /// A side chat split off from the chat.
    case side
    case chat
    case handoff
    case unknown

    init(wire: String?) {
        self = ChatKind(rawValue: wire ?? "") ?? .unknown
    }

    var label: String {
        switch self {
        case .main: return "Chat"
        case .side: return "Side chat"
        case .handoff: return "One-time work"
        case .chat, .unknown: return "Chat"
        }
    }
}

enum ChatRole: String, Codable, Equatable, Sendable {
    case user
    case assistant
    case tool
    case system
    case unknown

    init(wire: String?) {
        self = ChatRole(rawValue: wire ?? "") ?? .unknown
    }
}

enum ChatItemKind: String, Codable, Equatable, Sendable {
    case text
    case proactive
    case status
    case toolUse = "tool_use"
    case toolResult = "tool_result"
    case permissionRequest = "permission_request"
    case error
    case unknown

    init(wire: String?) {
        self = ChatItemKind(rawValue: wire ?? "") ?? .unknown
    }
}

struct ChatSummaryView: Equatable, Sendable, Identifiable, Decodable {
    var id: String
    var title: String
    var updatedAt: String
    var state: ChatState
    var kind: ChatKind

    var displayTitle: String {
        title.isEmpty ? "Untitled chat" : title
    }

    var updatedDate: Date? {
        RelayTime.parse(updatedAt)
    }
}

extension ChatSummaryView {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        id = container.chatText("id")
        title = container.chatText("title")
        updatedAt = container.chatText("updatedAt")
        state = ChatState(wire: container.chatOptionalText("state"))
        kind = ChatKind(wire: container.chatOptionalText("kind"))
    }
}

struct ChatHeaderView: Decodable, Equatable, Sendable {
    var id: String
    var title: String
    var state: ChatState

    var displayTitle: String {
        title.isEmpty ? "Chat" : title
    }
}

extension ChatHeaderView {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        id = container.chatText("id")
        title = container.chatText("title")
        state = ChatState(wire: container.chatOptionalText("state"))
    }
}

struct ChatPermissionOption: Decodable, Equatable, Sendable, Identifiable {
    var id: String
    var label: String
}

extension ChatPermissionOption {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        id = container.chatText("id")
        label = container.chatText("label")
    }
}

struct ChatPermissionView: Decodable, Equatable, Sendable {
    var requestID: String
    var options: [ChatPermissionOption]
}

extension ChatPermissionView {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        requestID = container.chatText("requestId")
        options = container.chatItems(ChatPermissionOption.self, "options").filter { !$0.id.isEmpty }
    }
}

struct ChatItemView: Decodable, Equatable, Sendable, Identifiable {
    var seq: Int
    var stableID: String? = nil
    var role: ChatRole
    var kind: ChatItemKind
    var text: String
    var toolName: String
    var at: String
    var permission: ChatPermissionView?
    var proactive: ChatProactiveView? = nil
    var status: ChatStatusView? = nil
    var report: ChatMessageReport? = nil
    /// On an answer: when the user asked the question it answers. Optional; older computers do not send it.
    var askedAt: String? = nil

    /// Stored rows can move to a later cursor position. Older hubs and transient prompts have no row id.
    var id: String {
        guard let stableID, !stableID.isEmpty else { return "seq:\(seq)" }
        return "id:\(stableID)"
    }

    var atDate: Date? {
        RelayTime.parse(at)
    }

    /// A permission card is actionable only when the Mac sent a request id and at least one option.
    var isPermissionPrompt: Bool {
        guard kind == .permissionRequest, let permission else { return false }
        return !permission.requestID.isEmpty && !permission.options.isEmpty
    }
}

extension ChatItemView {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        seq = container.chatInt("seq")
        stableID = container.chatOptionalText("id")
        role = ChatRole(wire: container.chatOptionalText("role"))
        kind = ChatItemKind(wire: container.chatOptionalText("kind"))
        text = container.chatText("text")
        toolName = container.chatText("toolName")
        at = container.chatText("at")
        permission = try? container.decodeIfPresent(ChatPermissionView.self, forKey: ChatCodingKeys("permission"))
        proactive = try? container.decodeIfPresent(ChatProactiveView.self, forKey: ChatCodingKeys("proactive"))
        status = try? container.decodeIfPresent(ChatStatusView.self, forKey: ChatCodingKeys("status"))
        report = try? container.decodeIfPresent(ChatMessageReport.self, forKey: ChatCodingKeys("report"))
        askedAt = try? container.decodeIfPresent(String.self, forKey: ChatCodingKeys("askedAt"))
    }
}

struct ChatMessageReport: Codable, Equatable, Sendable {
    var source: String
    var channel: String
    var sentAt: Int64
    var principalId: String?

    var sentDate: Date { Date(timeIntervalSince1970: TimeInterval(sentAt) / 1000) }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        source = container.chatText("source")
        channel = container.chatText("channel")
        sentAt = (try? container.decodeIfPresent(Int64.self, forKey: ChatCodingKeys("sentAt"))) ?? 0
        principalId = container.chatOptionalText("principalId")
        guard source == "message_send", ["mail", "slack"].contains(channel), sentAt > 0 else {
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath,
                                                    debugDescription: "Invalid message send report"))
        }
    }

    func presentation(locale: Locale) -> String {
        let copy = ChatPresentationLanguage(locale: locale)
        let channelName = channel == "mail" ? "Mail" : "Slack"
        let time = sentDate.formatted(.dateTime.hour().minute().locale(locale))
        let chinese = channel == "mail" ? "已通过邮件发送，\(time)" : "已通过 Slack 发送，\(time)"
        return copy.text(en: "Sent via \(channelName), \(time)", zhCN: chinese)
    }
}

/// Display data from the Mac, never a grant to execute an action. The Mac rechecks the stored card on every tap.
struct ChatProactiveView: Codable, Equatable, Sendable, Identifiable {
    var momentID: String
    var kind: String
    var headline: String
    var body: String
    var draft: Bool
    var draftContent: ChatProactiveDraft?
    var actionIDs: [String]
    var actionLabels: [String: String]
    var target: ChatMessageTarget
    var evidence: [ChatEvidenceView]
    var state: String
    var items: [ChatProactiveView]
    var localized: [String: ChatProactiveCopy]
    var work: ChatProactiveWork?
    var permission: ChatProactivePermission?
    var event: ChatProactiveEvent?
    var events: [ChatProactiveEvent]
    var place: ChatProactivePlace?
    var personName: String?
    var briefID: String?
    var kindMuted: Bool?
    var expiresAt: Int64?
    var sentences: [String]
    var helps: [String]
    var offerCount: Int
    var firstReadConfirmedAt: Int64?

    var id: String { momentID }
    var canAct: Bool { state == "pending" || state == "opened" }
    /// A thread update whose source the computer can no longer read. It is routine and has nothing to answer.
    var isWithdrawnThreadUpdate: Bool { kind == "thread_update_unavailable" }
    func statusLabel(language: String) -> String {
        let copy = ChatPresentationLanguage(language: language)
        switch state {
        case "pending": return copy.text(en: "Waiting on you", zhCN: "等待你回应")
        case "opened": return copy.text(en: "Opened", zhCN: "已打开")
        case "acted": return copy.text(en: "Done", zhCN: "已完成")
        case "dismissed": return copy.text(en: "Dismissed", zhCN: "已忽略")
        case "deferred": return copy.text(en: "Saved for later", zhCN: "已留待稍后处理")
        case "expired": return copy.text(en: "No longer waiting", zhCN: "无需再回应")
        default: return copy.text(en: "Status unavailable", zhCN: "状态不可用")
        }
    }

    var readableDraft: ChatProactiveDraft? {
        guard draft, let draftContent, draftContent.isComplete else { return nil }
        return draftContent
    }

    private enum CodingKeys: String, CodingKey {
        case momentID = "momentId", actionIDs = "actionIds"
        case kind, headline, body, draft, draftContent, actionLabels, target, evidence, state, items, localized, work, permission
        case event, events, place, personName, briefID = "briefId", kindMuted, expiresAt
        case sentences, helps, offerCount, firstReadConfirmedAt
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        momentID = container.chatText("momentId")
        // Work-report identities can include a human-readable blocked reason. They travel in JSON, not a path.
        guard !momentID.isEmpty, momentID.utf8.count <= 48 * 1024 else {
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath,
                                                    debugDescription: "Missing proactive identity"))
        }
        kind = container.chatText("kind")
        headline = container.chatText("headline")
        body = container.chatText("body")
        draft = (try? container.decode(Bool.self, forKey: ChatCodingKeys("draft"))) ?? false
        draftContent = try? container.decodeIfPresent(ChatProactiveDraft.self, forKey: ChatCodingKeys("draftContent"))
        actionIDs = container.chatItems(String.self, "actionIds")
        actionLabels = (try? container.decode([String: String].self, forKey: ChatCodingKeys("actionLabels"))) ?? [:]
        target = (try? container.decode(ChatMessageTarget.self, forKey: ChatCodingKeys("target"))) ?? .none
        evidence = container.chatItems(ChatEvidenceView.self, "evidence")
        state = container.chatText("state")
        var identities: Set<String> = [momentID]
        items = Array(container.chatItems(ChatProactiveView.self, "items")
            .filter { identities.insert($0.id).inserted }.prefix(5))
        localized = (try? container.decode([String: ChatProactiveCopy].self, forKey: ChatCodingKeys("localized"))) ?? [:]
        work = try? container.decodeIfPresent(ChatProactiveWork.self, forKey: ChatCodingKeys("work"))
        permission = try? container.decodeIfPresent(ChatProactivePermission.self, forKey: ChatCodingKeys("permission"))
        event = try? container.decodeIfPresent(ChatProactiveEvent.self, forKey: ChatCodingKeys("event"))
        events = container.chatItems(ChatProactiveEvent.self, "events")
        place = try? container.decodeIfPresent(ChatProactivePlace.self, forKey: ChatCodingKeys("place"))
        personName = container.chatOptionalText("personName")
        briefID = container.chatOptionalText("briefId")
        kindMuted = try? container.decodeIfPresent(Bool.self, forKey: ChatCodingKeys("kindMuted"))
        expiresAt = try? container.decodeIfPresent(Int64.self, forKey: ChatCodingKeys("expiresAt"))
        sentences = Array(container.chatItems(String.self, "sentences").prefix(2))
        helps = Array(container.chatItems(String.self, "helps").prefix(3))
        offerCount = min(max(container.chatInt("offerCount"), 0), 3)
        firstReadConfirmedAt = try? container.decodeIfPresent(Int64.self, forKey: ChatCodingKeys("firstReadConfirmedAt"))
    }

    func copy(language: String) -> ChatProactiveCopy {
        localized[language] ?? ChatProactiveCopy(headline: headline, body: body, actionLabels: actionLabels)
    }

    /// Unknown actions stay hidden. A future hub cannot turn an arbitrary label into a phone-side operation.
    func actions(language: String) -> [ChatProactiveAction] {
        let labels = copy(language: language).actionLabels
        let chrome = ChatPresentationLanguage(language: language)
        var seen: Set<String> = []
        return actionIDs.compactMap { id in
            if ["approve", "decline"].contains(id), permission == nil { return nil }
            guard ChatProactiveAction.knownIDs.contains(id), seen.insert(id).inserted else { return nil }
            if id == "send" {
                guard readableDraft != nil else { return nil }
                return ChatProactiveAction(id: id, label: chrome.text(en: "Send the reply", zhCN: "发送回复"))
            }
            guard let label = labels[id] ?? actionLabels[id],
                  !label.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
            return ChatProactiveAction(id: id, label: label)
        }
    }
}

struct ChatProactiveEvent: Codable, Equatable, Sendable {
    var title: String?
    var start: String?
    var end: String?
    var place: String?
    var people: String?
    var isPrivate: Bool?
    var minutes: Int?

    enum CodingKeys: String, CodingKey {
        case title, start, end, place, people, minutes
        case isPrivate = "private"
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        title = container.chatOptionalText("title")
        start = container.chatOptionalText("start")
        end = container.chatOptionalText("end")
        place = container.chatOptionalText("place")
        people = container.chatOptionalText("people")
        isPrivate = try? container.decodeIfPresent(Bool.self, forKey: ChatCodingKeys("private"))
        minutes = try? container.decodeIfPresent(Int.self, forKey: ChatCodingKeys("minutes"))
    }
}

struct ChatProactivePlace: Codable, Equatable, Sendable {
    var app: String?
    var bundleID: String?
    var window: String?
    var at: String?

    enum CodingKeys: String, CodingKey {
        case app, window, at
        case bundleID = "bundleId"
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        app = container.chatOptionalText("app")
        bundleID = container.chatOptionalText("bundleId")
        window = container.chatOptionalText("window")
        at = container.chatOptionalText("at")
    }
}

struct ChatProactiveDraft: Codable, Equatable, Sendable {
    var to: String
    var subject: String
    var text: String
    var channel: String?
    var recipient: String?
    var principalId: String?
    var revision: String?

    var isComplete: Bool {
        let required = [to, text, recipient ?? "", principalId ?? "", revision ?? ""]
        guard channel == "mail" || channel == "slack", required.allSatisfy({
            !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }) else { return false }
        return channel != "mail" || !subject.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        to = container.chatText("to")
        subject = container.chatText("subject")
        text = container.chatText("text")
        channel = container.chatOptionalText("channel")
        recipient = container.chatOptionalText("recipient")
        principalId = container.chatOptionalText("principalId")
        revision = container.chatOptionalText("revision")
    }
}

struct ChatProactiveCopy: Codable, Equatable, Sendable {
    var headline: String
    var body: String
    var actionLabels: [String: String]
    var helps: [String]? = nil
}

struct ChatProactiveWork: Codable, Equatable, Sendable {
    var handoffId: String
    var status: String
    var summary: String
    var result: String
}

struct ChatProactivePermission: Codable, Equatable, Sendable {
    var requestId: String
    var toolName: String
    var details: String
    var approveOptionId: String
    var declineOptionId: String
}

struct ChatProactiveAction: Equatable, Sendable, Identifiable {
    var id: String
    var label: String

    static let knownIDs: Set<String> = ["open", "answer", "review", "open_settings", "see", "open_person",
                                       "add_to_upcoming", "do_once", "not_now", "dismiss", "stop", "approve", "decline", "send",
                                       "mute_kind"]
    var opensDestination: Bool {
        ["open", "answer", "review", "open_settings", "see", "open_person"].contains(id)
    }
}

struct ChatMessageTarget: Codable, Equatable, Sendable {
    var kind: String
    var id: String?
    var runId: String?

    static let none = ChatMessageTarget(kind: "none")
}

struct ChatEvidenceView: Codable, Equatable, Sendable {
    var kind: String
    var id: String
    var at: String?
    var target: ChatMessageTarget?

    func sourceLabel(language: String) -> String {
        let copy = ChatPresentationLanguage(language: language)
        switch kind {
        case "brief": return copy.text(en: "Brief", zhCN: "简报")
        case "mail", "commitment": return copy.text(en: "Mail", zhCN: "邮件")
        case "calendar": return copy.text(en: "Calendar", zhCN: "日历")
        case "schedule": return copy.text(en: "Schedule", zhCN: "计划")
        case "capture": return copy.text(en: "Activity", zhCN: "活动")
        case "suggestion": return copy.text(en: "Suggestion", zhCN: "建议")
        case "topic": return copy.text(en: "Topic", zhCN: "主题")
        case "playbook": return copy.text(en: "Playbook", zhCN: "工作流程")
        default: return "Isoform"
        }
    }
}

struct ChatStatusView: Codable, Equatable, Sendable {
    var period: String
    var entries: [ChatStatusEntry]

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        period = container.chatText("period")
        var identities: Set<String> = []
        entries = container.chatItems(ChatStatusEntry.self, "entries")
            .filter { !$0.id.isEmpty && !$0.text.isEmpty && identities.insert($0.id).inserted }
    }

    func summary(language: String, fallback: String) -> String {
        guard let first = entries.first else { return fallback }
        if entries.count == 1 { return first.displayText(language: language) }
        return ChatPresentationLanguage(language: language)
            .text(en: "\(entries.count) updates from Isoform", zhCN: "来自 Isoform 的 \(entries.count) 条更新")
    }
}

/// Presentation chrome follows the same en / zh-CN choice as the card's server-provided content.
struct ChatPresentationLanguage {
    let code: String

    init(language: String) {
        code = language == "zh-CN" || language == "zh" ? "zh-CN" : "en"
    }

    init(locale: Locale) {
        self.init(language: locale.language.languageCode?.identifier ?? "en")
    }

    func text(en: String, zhCN: String) -> String { code == "zh-CN" ? zhCN : en }
}

struct ChatStatusEntry: Codable, Equatable, Sendable, Identifiable {
    var id: String
    var kind: String
    var text: String
    var target: ChatMessageTarget?
    var localized: [String: String]?

    func displayText(language: String) -> String { localized?[language] ?? text }
}

/// The body of `GET /chats/{id}`: the chat's header and a page of items in ascending seq order.
/// One page of a chat from `GET /chats/{id}`.
///
/// `items` are the stored rows of the page, oldest first. `cursor` is the seq of the last row the Mac
/// consumed for this page, including rows it did not send, so the next read continues from `cursor` and
/// not from the last item's seq. `hasMore` means rows after `cursor` are waiting. `pendingPermission` is
/// the Mac's transient permission prompt: it is never stored, never part of `items`, and never moves
/// the cursor.
struct ChatDetail: Decodable, Equatable, Sendable {
    var chat: ChatHeaderView?
    var items: [ChatItemView]
    var pendingPermission: ChatItemView?
    var cursor: Int
    var hasMore: Bool
    var live: ChatLiveAnswer? = nil
}

struct ChatLiveAnswer: Decodable, Equatable, Sendable {
    let turnId: String
    let text: String
}

extension ChatDetail {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        chat = try? container.decodeIfPresent(ChatHeaderView.self, forKey: ChatCodingKeys("chat"))
        let rows = container.chatItems(ChatItemView.self, "items").sorted { $0.seq < $1.seq }
        let stored = rows.filter { $0.kind != .permissionRequest }
        items = stored
        pendingPermission = rows.last(where: { $0.kind == .permissionRequest && $0.isPermissionPrompt })
        let declared = try? container.decodeIfPresent(Int.self, forKey: ChatCodingKeys("cursor"))
        cursor = declared ?? stored.last?.seq ?? 0
        hasMore = (try? container.decodeIfPresent(Bool.self, forKey: ChatCodingKeys("hasMore"))) ?? false
        live = try? container.decodeIfPresent(ChatLiveAnswer.self, forKey: ChatCodingKeys("live"))
    }
}

struct ChatListPayload: Decodable, Equatable, Sendable {
    var chats: [ChatSummaryView]
}

extension ChatListPayload {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        chats = container.chatItems(ChatSummaryView.self, "chats")
    }
}

/// A turn event pushed on the existing poll (`{"event":"chat_turn", ...}`).
struct MobileChatTurnEvent: Equatable, Sendable, Decodable {
    var event: String
    var chatId: String
    var turnId: String
    var state: String
    var seq: Int
    var at: String
}

extension MobileChatTurnEvent {
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: ChatCodingKeys.self)
        event = container.chatText("event")
        chatId = container.chatText("chatId")
        turnId = container.chatText("turnId")
        state = container.chatText("state")
        seq = container.chatInt("seq")
        at = container.chatText("at")
    }
}

// MARK: - Request and response bodies

struct CreateChatRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let title: String
}

/// A computer-issued binding to an idea and the source snapshot that produced its prose.
struct ChatOfferReference: Codable, Equatable, Hashable, Sendable {
    let id: String
    let version: String

    /// Old unsent files cannot prove whether their text came from an idea. Never silently promote it.
    static let unverifiedLegacy = ChatOfferReference(id: "unverified-legacy-draft", version: "unverified")
}

/// The entire composer retains all source dependencies, including across deleting text and Undo.
struct ChatComposerDraft: Codable, Equatable, Sendable {
    var text = ""
    var offerReferences: [ChatOfferReference] = []
    var briefReference: ChatBriefReference? = nil
    /// Makes the durable transfer from returned messages crash-safe without duplicating their text.
    var recoveredDraftIDs: Set<String> = []

    func accepts(brief: ChatBriefReference?, offers: [ChatOfferReference]) -> Bool {
        guard briefReference == nil || brief == nil || briefReference == brief else { return false }
        return (briefReference == nil && brief == nil) || (offerReferences.isEmpty && offers.isEmpty)
    }

    mutating func retain(_ references: [ChatOfferReference]) {
        for reference in references where !offerReferences.contains(reference) { offerReferences.append(reference) }
    }
}

struct SendChatMessageRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let text: String
    var offerReferences: [ChatOfferReference] = []
    /// When the phone queued a message that waited for the computer. Optional.
    var queuedAt: String? = nil
    /// How many waiting messages the phone took back since its last message. A count, no words.
    var undone: Int? = nil
}

/// What the phone tells the computer about a message that waited: when it was queued and how many were undone.
struct ChatWaitingFields: Equatable, Sendable {
    var queuedAt: Date?
    var undone: Int
}

struct AnswerChatPermissionRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let optionId: String
}

struct CancelChatRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    var turnId: String? = nil
}

struct CreatedRecordPayload: Decodable, Equatable, Sendable {
    let id: String
}

/// The 202 for a message. `seq` names the stored user item; a Mac that predates it leaves it out.
struct ChatMessageAccepted: Decodable, Equatable, Sendable {
    let turnId: String
    var seq: Int? = nil
}

struct ChatPermissionAnswered: Decodable, Equatable, Sendable {
    let answered: Bool
}

struct ChatCancelled: Decodable, Equatable, Sendable {
    let cancelled: Bool
}

/// The error body the hub sends with a 409 when a turn is already running.
struct ChatErrorBody: Decodable, Equatable, Sendable {
    let error: String?
}

// MARK: - Lenient decoding helpers

/// A dynamic key so the lenient decoders can read the hub's camel-case names without a CodingKeys enum per type.
struct ChatCodingKeys: CodingKey {
    let stringValue: String
    let intValue: Int? = nil

    init(_ value: String) {
        stringValue = value
    }

    init?(stringValue: String) {
        self.stringValue = stringValue
    }

    init?(intValue: Int) {
        return nil
    }
}

/// Drops one element that does not decode instead of failing the whole array. Topics read with it too.
struct ChatLenientItem<Value: Decodable>: Decodable {
    let value: Value?

    init(from decoder: Decoder) throws {
        value = try? Value(from: decoder)
    }
}

extension KeyedDecodingContainer where Key == ChatCodingKeys {
    func chatText(_ name: String) -> String {
        (try? decodeIfPresent(String.self, forKey: ChatCodingKeys(name))) ?? ""
    }

    func chatOptionalText(_ name: String) -> String? {
        try? decodeIfPresent(String.self, forKey: ChatCodingKeys(name))
    }

    func chatInt(_ name: String) -> Int {
        (try? decodeIfPresent(Int.self, forKey: ChatCodingKeys(name))) ?? 0
    }

    func chatItems<Value: Decodable>(_ type: Value.Type, _ name: String) -> [Value] {
        guard let wrapped = try? decodeIfPresent([ChatLenientItem<Value>].self, forKey: ChatCodingKeys(name)) else {
            return []
        }
        return wrapped.compactMap(\.value)
    }
}
