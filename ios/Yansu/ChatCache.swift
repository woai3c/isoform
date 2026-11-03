import CryptoKit
import Foundation

/// One chat as the phone keeps it on disk: its header and the newest items. Permission requests are never
/// persisted, and the vault wipe removes the whole directory, including the main-chat marker.
struct CachedChat: Codable, Equatable, Sendable {
    var id: String
    var title: String
    var state: ChatState
    var items: [CachedChatItem]
    var storedAt: Date
}

struct CachedChatItem: Codable, Equatable, Sendable {
    var seq: Int
    var stableID: String?
    var role: ChatRole
    var kind: ChatItemKind
    var text: String
    var toolName: String
    var at: String
    var permissionRequestID: String?
    var permissionOptions: [CachedChatOption]
    var proactive: ChatProactiveView?
    var status: ChatStatusView?
    var report: ChatMessageReport?
    var askedAt: String? = nil

    init(_ item: ChatItemView) {
        seq = item.seq
        stableID = item.stableID
        role = item.role
        kind = item.kind
        text = item.text
        toolName = item.toolName
        at = item.at
        permissionRequestID = nil
        permissionOptions = []
        proactive = item.proactive
        status = item.status
        report = item.report
        askedAt = item.askedAt
    }

    var view: ChatItemView {
        // These fields remain decodable for old caches, but only a live Mac can supply permission authority.
        return ChatItemView(seq: seq, stableID: stableID, role: role, kind: kind, text: text, toolName: toolName, at: at,
                            permission: nil, proactive: proactive, status: status, report: report, askedAt: askedAt)
    }
}

struct CachedChatOption: Codable, Equatable, Sendable {
    var id: String
    var label: String
}

private struct CachedOutgoing: Codable {
    var chatId: String
    var messages: [ChatOutgoingMessage]
    var composer: ChatComposerDraft? = nil
    /// The companion target persisted these fields before source-bound drafts existed.
    var composerText: String? = nil
    var recoveredDraftIDs: Set<String>? = nil

    var resolvedComposer: ChatComposerDraft? {
        if let composer { return composer }
        guard composerText != nil || recoveredDraftIDs != nil else { return nil }
        let text = composerText ?? ""
        return ChatComposerDraft(text: text, offerReferences: text.isEmpty ? [] : [.unverifiedLegacy],
                                 recoveredDraftIDs: recoveredDraftIDs ?? [])
    }
}

private struct CachedChatIdentity: Decodable {
    var id: String
}

private struct CachedMainChat: Codable {
    var id: String
    var title: String
    var state: ChatState
    var updatedAt: String

    var summary: ChatSummaryView {
        ChatSummaryView(id: id, title: title, updatedAt: updatedAt, state: state, kind: .main)
    }
}

/// Keeps the newest items of each chat in the App Group container, so an open chat can still be read when the
/// Mac is offline. The main-chat marker makes that transcript discoverable before the first network request.
/// PairingVault has transport keys but no at-rest cache key, so files use complete file protection.
final class ChatCache: @unchecked Sendable {
    static let itemLimit = 50

    private let directory: URL
    private let now: () -> Date
    private let removeItem: (URL) throws -> Void
    private let writeData: (Data, URL) throws -> Void
    private let lock = NSLock()
    private var removalPending = false

    init(directory: URL, now: @escaping () -> Date = { Date() },
         removeItem: @escaping (URL) throws -> Void = { try FileManager.default.removeItem(at: $0) },
         writeData: @escaping (Data, URL) throws -> Void = { try $0.write(to: $1, options: [.atomic, .completeFileProtection]) }) {
        self.directory = directory
        self.now = now
        self.removeItem = removeItem
        self.writeData = writeData
    }

    static var defaultDirectory: URL {
        directory(in: AppGroup.containerURL ?? applicationSupport)
    }

    private static var applicationSupport: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
    }

    private static func directory(in root: URL) -> URL {
        root.appendingPathComponent("Isoform", isDirectory: true).appendingPathComponent("chats", isDirectory: true)
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

    private func locked<T>(_ body: () throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try body()
    }

    private func fileURL(for chatID: String, suffix: String = "") -> URL {
        let digest = SHA256.hash(data: Data(chatID.utf8)).map { String(format: "%02x", $0) }.joined()
        return directory.appendingPathComponent(digest + suffix + ".json")
    }

    private var mainChatURL: URL { directory.appendingPathComponent("main.json") }

    private var refusalURL: URL { directory.appendingPathComponent("refused.json") }

    /// Remembers across launches that the relay refused this pairing, so a cold start asks to pair again
    /// before any request instead of opening the cached chat. The vault wipe removes it with the rest.
    func rememberRefusal() throws {
        try locked {
            try finishPendingRemovalLocked()
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            try Data("{}".utf8).write(to: refusalURL, options: [.atomic, .completeFileProtection])
        }
    }

    func forgetRefusal() throws {
        try locked {
            try finishPendingRemovalLocked()
            guard FileManager.default.fileExists(atPath: refusalURL.path) else { return }
            try FileManager.default.removeItem(at: refusalURL)
        }
    }

    /// Only the file's existence counts, so this answers even while file protection keeps its contents locked.
    var refusalRemembered: Bool {
        locked {
            guard (try? finishPendingRemovalLocked()) != nil else { return false }
            return FileManager.default.fileExists(atPath: refusalURL.path)
        }
    }

    /// Records the Mac's main-chat identity independently of transcript loading. A header without a transcript
    /// is useful for navigation, but does not mean the chat is empty.
    func rememberMainChat(_ summary: ChatSummaryView) throws {
        guard summary.kind == .main, Identifiers.isSafePathID(summary.id) else { return }
        try locked {
            try finishPendingRemovalLocked()
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let entry = CachedMainChat(id: summary.id, title: summary.title, state: summary.state,
                                       updatedAt: summary.updatedAt)
            try Self.encoder.encode(entry).write(to: mainChatURL, options: [.atomic, .completeFileProtection])
        }
    }

    func loadMainChatSummary() -> ChatSummaryView? {
        locked {
            guard (try? finishPendingRemovalLocked()) != nil,
                  let data = try? Data(contentsOf: mainChatURL),
                  let entry = try? Self.decoder.decode(CachedMainChat.self, from: data),
                  Identifiers.isSafePathID(entry.id) else { return nil }
            return entry.summary
        }
    }

    func loadMainChat() -> CachedChat? {
        guard let summary = loadMainChatSummary() else { return nil }
        return load(chatID: summary.id)
    }

    /// Saves the header and the newest `itemLimit` items, ordered by seq.
    func save(_ header: ChatHeaderView, items: [ChatItemView]) throws {
        try locked {
            try finishPendingRemovalLocked()
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            // A first read is pairing-specific and must be checked against the
            // computer's current portrait sources before it is shown.
            let newest = items.filter { $0.kind != .permissionRequest && $0.proactive?.kind != "first_read" }
                .sorted { $0.seq < $1.seq }.suffix(Self.itemLimit)
            let entry = CachedChat(id: header.id, title: header.title, state: header.state,
                                   items: newest.map(CachedChatItem.init), storedAt: now())
            let data = try Self.encoder.encode(entry)
            try data.write(to: fileURL(for: header.id), options: [.atomic, .completeFileProtection])
        }
    }

    func load(chatID: String) -> CachedChat? {
        locked {
            guard (try? finishPendingRemovalLocked()) != nil else { return nil }
            let url = fileURL(for: chatID)
            // Upgrade older snapshots even when the Mac is offline and no refreshed save follows this read.
            guard (try? FileManager.default.setAttributes([.protectionKey: FileProtectionType.complete],
                                                          ofItemAtPath: url.path)) != nil,
                  let data = try? Data(contentsOf: url) else { return nil }
            guard var entry = try? Self.decoder.decode(CachedChat.self, from: data), entry.id == chatID else {
                return nil
            }
            // Old versions could encode permission rows. Never restore them as pending work or authority.
            entry.items = entry.items.filter { $0.kind != .permissionRequest && $0.proactive?.kind != "first_read" }
                .sorted { $0.seq < $1.seq }.suffix(Self.itemLimit).map { CachedChatItem($0.view) }
            return entry
        }
    }

    /// Messages sent from this iPhone that the Mac has not seen yet, and the newest one, so a relaunch still
    /// shows where each one got to and sends again the ones still on their way. The file names its chat, so a
    /// launch can find every chat with messages waiting. Like the transcript, the text is kept under complete
    /// file protection.
    func saveOutgoing(_ messages: [ChatOutgoingMessage], chatID: String, clearingComposer: Bool = false) throws {
        try locked {
            try finishPendingRemovalLocked()
            let current = try outgoingRecordLocked(chatID: chatID)
            let composer = clearingComposer
                ? ChatComposerDraft(recoveredDraftIDs: current?.composer?.recoveredDraftIDs ?? [])
                : current?.composer
            try saveOutgoingLocked(messages, composer: composer, chatID: chatID)
        }
    }

    private func outgoingRecordLocked(chatID: String) throws -> (chatId: String?, messages: [ChatOutgoingMessage], composer: ChatComposerDraft?)? {
        let url = fileURL(for: chatID, suffix: Self.outgoingSuffix)
        guard FileManager.default.fileExists(atPath: url.path) else { return nil }
        let data = try Data(contentsOf: url)
        guard let entry = Self.decodeOutgoing(data), entry.chatId == nil || entry.chatId == chatID else {
            throw CocoaError(.fileReadCorruptFile)
        }
        return entry
    }

    private func saveOutgoingLocked(_ messages: [ChatOutgoingMessage], composer: ChatComposerDraft?, chatID: String) throws {
        let url = fileURL(for: chatID, suffix: Self.outgoingSuffix)
        guard !messages.isEmpty || composer != nil else {
            if FileManager.default.fileExists(atPath: url.path) { try FileManager.default.removeItem(at: url) }
            return
        }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let file = CachedOutgoing(chatId: chatID, messages: messages, composer: composer)
        try writeData(Self.encoder.encode(file), url)
    }

    /// Moving a draft into the outgoing queue is one write: a crash cannot leave both sendable copies.
    func saveSubmission(_ messages: [ChatOutgoingMessage], chatID: String, composer: ChatComposerDraft) throws {
        try locked {
            try finishPendingRemovalLocked()
            _ = try outgoingRecordLocked(chatID: chatID)
            try saveOutgoingLocked(messages, composer: composer, chatID: chatID)
        }
    }

    /// Compatibility callers edit text while the same envelope retains its source dependencies.
    func loadComposerDraft(chatID: String) -> (text: String, recoveredDraftIDs: Set<String>)? {
        guard let composer = loadComposer(chatID: chatID) else { return nil }
        return (composer.text, composer.recoveredDraftIDs)
    }

    /// Recovery supplies the authoritative queue when a previous refusal could not reach disk.
    @discardableResult
    func saveComposerDraft(_ text: String, chatID: String, recoveredDraftIDs: Set<String>? = nil,
                           outgoingMessages: [ChatOutgoingMessage]? = nil) throws -> [ChatOutgoingMessage] {
        try locked {
            try finishPendingRemovalLocked()
            let current = try outgoingRecordLocked(chatID: chatID)
            var composer = current?.composer ?? ChatComposerDraft()
            composer.text = text
            if let recoveredDraftIDs { composer.recoveredDraftIDs = recoveredDraftIDs }
            let messages = outgoingMessages ?? current?.messages ?? []
            try saveOutgoingLocked(messages, composer: composer, chatID: chatID)
            return messages
        }
    }

    private var awayURL: URL { directory.appendingPathComponent("away-periods.json") }

    /// The stretches each chat's messages waited for the computer, by chat. Nil when the file cannot be read.
    func loadAwayPeriods() -> [String: [ChatAwayPeriod]]? {
        locked {
            guard FileManager.default.fileExists(atPath: awayURL.path) else { return [:] }
            guard let data = try? Data(contentsOf: awayURL) else { return nil }
            return (try? Self.decoder.decode([String: [ChatAwayPeriod]].self, from: data)) ?? [:]
        }
    }

    func saveAwayPeriods(_ periods: [String: [ChatAwayPeriod]]) throws {
        try locked {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            try writeData(Self.encoder.encode(periods), awayURL)
        }
    }

    /// Empty when there is no file; nil when one exists but cannot be read, for example while the phone is
    /// locked, so the caller does not mistake it for empty and write over it. A file that reads but does not
    /// decode is set aside under a `.damaged` name, kept rather than overwritten, and the chat starts empty.
    func loadOutgoing(chatID: String) -> [ChatOutgoingMessage]? {
        locked {
            guard (try? finishPendingRemovalLocked()) != nil else { return nil }
            let url = fileURL(for: chatID, suffix: Self.outgoingSuffix)
            guard FileManager.default.fileExists(atPath: url.path) else { return [] }
            guard let data = try? Data(contentsOf: url) else { return nil }
            if let messages = Self.decodeOutgoing(data)?.messages { return messages }
            let aside = url.deletingPathExtension().appendingPathExtension("damaged-\(Int(now().timeIntervalSince1970))")
            guard (try? FileManager.default.moveItem(at: url, to: aside)) != nil else { return nil }
            return []
        }
    }

    /// The chats that have messages kept on this iPhone. A first-build bare list gets its identity from the
    /// matching transcript, the main-chat marker, or pending journal targets supplied by the caller. Save that
    /// identity before resuming, so the messages stay discoverable after the transcript or journal is removed.
    func outgoingChatIDs(knownChatIDs: [String] = []) -> [String] {
        locked {
            guard (try? finishPendingRemovalLocked()) != nil,
                  let names = try? FileManager.default.contentsOfDirectory(atPath: directory.path) else { return [] }
            let outgoingNames = names.filter { $0.hasSuffix(Self.outgoingSuffix + ".json") }
            guard !outgoingNames.isEmpty else { return [] }
            var known = Set(knownChatIDs.filter(Identifiers.isSafePathID))
            for name in names where name.hasSuffix(".json") && !name.hasSuffix(Self.outgoingSuffix + ".json") {
                guard let data = try? Data(contentsOf: directory.appendingPathComponent(name)),
                      let identity = try? Self.decoder.decode(CachedChatIdentity.self, from: data),
                      Identifiers.isSafePathID(identity.id),
                      name == mainChatURL.lastPathComponent || name == fileURL(for: identity.id).lastPathComponent else {
                    continue
                }
                known.insert(identity.id)
            }
            let chatByName = Dictionary(uniqueKeysWithValues: known.map {
                (fileURL(for: $0, suffix: Self.outgoingSuffix).lastPathComponent, $0)
            })
            return outgoingNames.compactMap { name in
                let url = directory.appendingPathComponent(name)
                guard let data = try? Data(contentsOf: url), let decoded = Self.decodeOutgoing(data),
                      let chatID = decoded.chatId ?? chatByName[name], Identifiers.isSafePathID(chatID),
                      fileURL(for: chatID, suffix: Self.outgoingSuffix).lastPathComponent == name else { return nil }
                if decoded.chatId == nil {
                    let entry = CachedOutgoing(chatId: chatID, messages: decoded.messages, composer: decoded.composer)
                    guard let upgraded = try? Self.encoder.encode(entry),
                          (try? upgraded.write(to: url, options: [.atomic, .completeFileProtection])) != nil else { return nil }
                }
                return chatID
            }
        }
    }

    private static let outgoingSuffix = ".outgoing"

    /// The first build wrote a bare list of messages; later files name their chat.
    private static func decodeOutgoing(_ data: Data) -> (chatId: String?, messages: [ChatOutgoingMessage], composer: ChatComposerDraft?)? {
        if let file = try? decoder.decode(CachedOutgoing.self, from: data) { return (file.chatId, file.messages, file.resolvedComposer) }
        if let messages = try? decoder.decode([ChatOutgoingMessage].self, from: data) { return (nil, messages, nil) }
        return nil
    }

    /// Composer contents share the outgoing envelope so submission can move them atomically. Nil means an
    /// existing file is unreadable; it must never be overwritten as though it were an empty draft.
    func loadComposer(chatID: String) -> ChatComposerDraft? {
        locked {
            do {
                try finishPendingRemovalLocked()
                return try outgoingRecordLocked(chatID: chatID)?.composer ?? ChatComposerDraft()
            } catch { return nil }
        }
    }

    func saveComposer(_ draft: ChatComposerDraft, chatID: String) throws {
        try locked {
            try finishPendingRemovalLocked()
            let messages = try outgoingRecordLocked(chatID: chatID)?.messages ?? []
            try saveOutgoingLocked(messages, composer: draft, chatID: chatID)
        }
    }

    /// Handoffs may precede discovery of the chat. Their own ids survive that transition.
    func loadMainDrafts() -> [ChatOutgoingMessage]? {
        locked {
            guard (try? finishPendingRemovalLocked()) != nil else { return nil }
            let url = directory.appendingPathComponent("main-drafts.json")
            guard FileManager.default.fileExists(atPath: url.path) else { return [] }
            guard let data = try? Data(contentsOf: url) else { return nil }
            return try? Self.decoder.decode([ChatOutgoingMessage].self, from: data)
        }
    }

    func saveMainDrafts(_ drafts: [ChatOutgoingMessage]) throws {
        try locked {
            try finishPendingRemovalLocked()
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            try writeData(Self.encoder.encode(drafts), directory.appendingPathComponent("main-drafts.json"))
        }
    }

    func removeAll() throws {
        try locked {
            removalPending = true
            try finishPendingRemovalLocked()
        }
    }

    private func finishPendingRemovalLocked() throws {
        guard removalPending else { return }
        if FileManager.default.fileExists(atPath: directory.path) {
            try removeItem(directory)
        }
        removalPending = false
    }
}
