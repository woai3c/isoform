import SwiftUI

/// Observations proposed by the computer, never approval to execute the proposed work.
struct IdeaOffer: Codable, Equatable, Identifiable, Sendable {
    let id: String
    let kind: String
    let title: String
    let why: String
    let prompt: String
    /// Older caches remain readable, but cannot hand source prose to chat without this binding.
    var version: String? = nil
    var whyLocalized: [String: String]? = nil
    /// The fact Isoform saw, with its source and time (Phone Threads §2). Older computers send only `why`.
    var evidence: IdeaEvidence? = nil
    /// reply, your_day or claude_code; nil when the card has no kind.
    var deckKind: String? = nil
    /// Only a typed, computer-checked proposal can turn Do it into a hold.
    var hold: PhoneHoldOffer? = nil

    var isCalendarHoldOffer: Bool {
        title.range(of: #"^\s*(?:hold|reserve|block|set aside)\b.*\b(?:time|block|calendar|today|tomorrow|monday|tuesday|wednesday|thursday|friday|saturday|sunday|morning|afternoon|evening|\d{1,2}(?::\d{2})?\s*(?:am|pm))\b|^\s*(?:为.{0,40})?(?:留出|预留|占用).*(?:时间|日历|点|周[一二三四五六日天]|星期|上午|下午|早上|今天|明天)|^\s*(?:把)?(?:今天|明天|周[一二三四五六日天]|星期[一二三四五六日天]).*留给"#,
                    options: [.regularExpression, .caseInsensitive]) != nil
    }

    var chatReference: ChatOfferReference? {
        guard let version, version.utf8.count == 64,
              version.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }),
              Identifiers.isSafePathID(id), id.hasPrefix("suggestion.") || id.hasPrefix("find.") else { return nil }
        return ChatOfferReference(id: id, version: version)
    }

    func explanation(locale: Locale) -> String {
        whyLocalized?[ChatPresentationLanguage(locale: locale).code] ?? why
    }

    func displayTitle(locale: Locale) -> String {
        guard let hold, let slot = hold.proposal.slots.first,
              let when = PhoneHoldCopy.when(slot, locale: locale) else { return title }
        return String(format: String(localized: "hold.offer.title", locale: locale), locale: locale,
                      arguments: [when, hold.proposal.thing.title])
    }
}

struct IdeaEvidence: Codable, Equatable, Hashable, Sendable {
    let text: String
    var textLocalized: [String: String]? = nil
    let source: String
    let at: String

    func text(locale: Locale) -> String {
        textLocalized?[ChatPresentationLanguage(locale: locale).code] ?? text
    }
}

struct IdeasPage: Decodable, Equatable, Sendable {
    let offers: [IdeaOffer]
    var nextAllowedAt: String? = nil
}

private struct EmptyFindMoreRequest: Encodable {}

struct IdeaDeclineRequest: Encodable, Equatable, Sendable {
    let idempotencyKey: String
    let decision: String
}

struct IdeaDeclinePayload: Decodable, Equatable, Sendable {
    let declined: Bool
}

struct IdeaDecisionApproval: Sendable {
    let id: String
    let decision: String
    fileprivate let hubID: String
    fileprivate let generation: UUID
}

protocol IdeasHubAPI: Sendable {
    func offers() async throws -> IdeasPage
    func findMoreOffers() async throws -> IdeasPage
    func declineOffer(id: String, _ request: IdeaDeclineRequest) async throws -> IdeaDeclinePayload
    func supportsHolds() async throws -> Bool
    func offers(includeHolds: Bool) async throws -> IdeasPage
}

extension IdeasHubAPI {
    func findMoreOffers() async throws -> IdeasPage { try await offers() }
    func supportsHolds() async throws -> Bool { false }
    func offers(includeHolds: Bool) async throws -> IdeasPage { try await offers() }
}

extension HubChannel: IdeasHubAPI {
    func supportsHolds() async throws -> Bool { try await status().holds == true }
    func offers(includeHolds: Bool) async throws -> IdeasPage {
        try await perform(includeHolds ? .offersWithHolds : .offers).payload(IdeasPage.self)
    }
    func findMoreOffers() async throws -> IdeasPage {
        try await post("/offers/find-more", EmptyFindMoreRequest(), as: IdeasPage.self)
    }
    func offers() async throws -> IdeasPage {
        try await perform(.offers).payload(IdeasPage.self)
    }

    func declineOffer(id: String, _ request: IdeaDeclineRequest) async throws -> IdeaDeclinePayload {
        guard Identifiers.isSafePathID(id) else { throw HubRequestError.invalidIdentifier }
        return try await post("/offers/\(id)/decline", request, as: IdeaDeclinePayload.self)
    }
}

/// The last list reads offline. Declines require a durable journal key, and remain visible until
/// confirmed. The cache belongs to one computer; a revoked read permission clears it rather than showing it offline.
@MainActor
final class IdeasStore: ObservableObject {
    enum LoadState: Equatable { case idle, loading, loaded, failed }

    @Published private(set) var ideas: [IdeaOffer] = []
    @Published private(set) var readAt: Date?
    @Published private(set) var loadState: LoadState = .idle
    @Published private(set) var busy: Set<String> = []
    @Published private(set) var pending: Set<String> = []
    @Published private(set) var repairNeeded = false
    @Published var notice: String?

    private struct Saved: Codable {
        let hubID: String
        let ideas: [IdeaOffer]
        let readAt: Date?
        let pending: Set<String>
        let decisions: [String: String]?
        var holdsEnabled: Bool? = nil
    }

    private let hub: IdeasHubAPI
    private let journal: PendingWriteJournal
    private let isPaired: @MainActor () -> Bool
    private let fileURL: URL
    private let readData: @Sendable (URL) throws -> Data
    private let canRead: @MainActor () -> Bool
    private let canDecide: @MainActor () -> Bool
    private let hubID: @MainActor () -> String?
    private let guardian: DecisionGuarding
    private let now: @Sendable () -> Date
    private let writer: JournaledWriter
    private var generation = UUID()
    private var cacheUnavailable = false
    private var cacheHubID: String?
    private var decisions: [String: String] = [:]
    private(set) var holdsEnabled = false

    init(hub: IdeasHubAPI, journal: PendingWriteJournal,
         isPaired: @escaping @MainActor () -> Bool, fileURL: URL = IdeasStore.defaultFileURL,
         canRead: @escaping @MainActor () -> Bool = { true },
         canDecide: @escaping @MainActor () -> Bool = { true },
         hubID: @escaping @MainActor () -> String? = { nil },
         guardian: DecisionGuarding = LocalDecisionGuard(),
         readData: @escaping @Sendable (URL) throws -> Data = { try Data(contentsOf: $0) },
         now: @escaping @Sendable () -> Date = { Date() },
         makeKey: @escaping @Sendable () -> String = { UUID().uuidString.lowercased() }) {
        self.hub = hub
        self.journal = journal
        self.isPaired = isPaired
        self.fileURL = fileURL
        self.readData = readData
        self.canRead = canRead
        self.canDecide = canDecide
        self.hubID = hubID
        self.guardian = guardian
        self.now = now
        self.writer = JournaledWriter(journal: journal, makeKey: makeKey, now: now)
        self.cacheHubID = hubID()
        loadCached()
    }

    nonisolated static var defaultFileURL: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Isoform", isDirectory: true).appendingPathComponent("ideas.json")
    }

    func loadCached() {
        guard isPaired(), canRead(), let currentHub = hubID() else { return }
        guard FileManager.default.fileExists(atPath: fileURL.path) else { return }
        do {
            let saved = try Self.decoder.decode(Saved.self, from: readData(fileURL))
            guard saved.hubID == currentHub else {
                _ = clearAfterUnpair()
                return
            }
            holdsEnabled = saved.holdsEnabled == true
            ideas = saved.ideas.filter { !$0.isCalendarHoldOffer || (holdsEnabled && $0.hold != nil) }
            readAt = saved.readAt
            pending = saved.pending
            decisions = saved.decisions ?? Dictionary(uniqueKeysWithValues: saved.pending.map { ($0, "dismiss") })
            cacheUnavailable = false
            cacheHubID = currentHub
            if notice == IdeasCopy.saveFailed { notice = nil }
        } catch {
            // Do not overwrite an unreadable cache with an apparently empty list of confirmed decisions.
            cacheUnavailable = true
            notice = IdeasCopy.saveFailed
        }
    }

    func refresh() async {
        _ = await loadOffers(fresh: false)
    }

    func findMore() async -> IdeasPage? {
        await loadOffers(fresh: true)
    }

    private func loadOffers(fresh: Bool) async -> IdeasPage? {
        guard isPaired(), canRead(), let currentHub = hubID() else {
            _ = clearAfterUnpair()
            return nil
        }
        // A read started before a dismissal could otherwise put the dismissed card back.
        guard loadState != .loading, busy.isEmpty else { return nil }
        recoverCacheIfNeeded()
        let current = generation
        loadState = .loading
        do {
            let supportsHolds = (try? await hub.supportsHolds()) == true
            let page = try await (fresh ? hub.findMoreOffers() : hub.offers(includeHolds: supportsHolds))
            guard isCurrent(current, hub: currentHub) else { return nil }
            // The phone may have unlocked while the computer was answering.
            if cacheUnavailable { loadCached() }
            guard isCurrent(current, hub: currentHub) else { return nil }
            var seen: Set<String> = []
            holdsEnabled = supportsHolds
            ideas = page.offers.filter { (!$0.isCalendarHoldOffer || (supportsHolds && $0.hold != nil)) && !$0.title.isEmpty && !$0.prompt.isEmpty && seen.insert($0.id).inserted }
            readAt = now()
            loadState = .loaded
            repairNeeded = false
            _ = save()
            await retryPending()
            return page
        } catch {
            guard isCurrent(current, hub: currentHub) else { return nil }
            if Self.isReadRefusal(error) { refuseAccess() } else { loadState = .failed }
            return nil
        }
    }

    @discardableResult
    func decline(_ idea: IdeaOffer) async -> Bool {
        await decide(idea, decision: "dismiss")
    }

    @discardableResult
    func deferOffer(_ idea: IdeaOffer) async -> Bool {
        await decide(idea, decision: "not_now")
    }

    func authorizeDecision(_ idea: IdeaOffer, decision: String) async -> IdeaDecisionApproval? {
        recoverCacheIfNeeded()
        guard isPaired(), canRead(), canDecide(), let currentHub = hubID(),
              loadState != .loading, ideas.contains(where: { $0.id == idea.id }),
              !busy.contains(idea.id), !pending.contains(idea.id) else { return nil }
        let current = generation
        busy.insert(idea.id)
        defer { if current == generation { busy.remove(idea.id) } }
        do {
            let reason = decision == "not_now"
                ? String(localized: "Confirm to save this idea for later")
                : String(localized: "Confirm to dismiss this idea")
            if decision == "not_now" {
                try await LocalDecisionGuard().authorize(reason: reason)
            } else {
                try await guardian.authorize(reason: reason)
            }
        } catch {
            guard current == generation else { return nil }
            notice = error.localizedDescription
            return nil
        }
        guard isCurrent(current, hub: currentHub), canDecide() else { return nil }
        return IdeaDecisionApproval(id: idea.id, decision: decision,
                                    hubID: currentHub, generation: current)
    }

    @discardableResult
    func commitDecision(_ approval: IdeaDecisionApproval) async -> Bool {
        recoverCacheIfNeeded()
        guard approval.generation == generation, approval.hubID == hubID(),
              isPaired(), canRead(), canDecide(), loadState != .loading,
              ideas.contains(where: { $0.id == approval.id }),
              !busy.contains(approval.id), !pending.contains(approval.id) else { return false }
        return await recordDecision(approval.id, decision: approval.decision,
                                    generation: approval.generation, hub: approval.hubID)
    }

    private func decide(_ idea: IdeaOffer, decision: String) async -> Bool {
        guard let approval = await authorizeDecision(idea, decision: decision) else { return false }
        return await commitDecision(approval)
    }

    private func recordDecision(_ id: String, decision: String, generation current: UUID,
                                hub currentHub: String) async -> Bool {
        guard isCurrent(current, hub: currentHub), canDecide(), !busy.contains(id), !pending.contains(id) else {
            return false
        }
        busy.insert(id)
        defer { if current == generation { busy.remove(id) } }
        pending.insert(id)
        decisions[id] = decision
        guard save() else {
            pending.remove(id)
            decisions.removeValue(forKey: id)
            notice = IdeasCopy.saveFailed
            return false
        }
        return await deliver(id: id, decision: decision, generation: current, hub: currentHub, announces: true)
    }

    /// Only a saved decision with its original journal key retries, so the computer applies it once.
    func retryPending() async {
        recoverCacheIfNeeded()
        guard !cacheUnavailable, isPaired(), canRead(), canDecide(), let currentHub = hubID(),
              loadState != .loading else { return }
        let current = generation
        for id in pending.sorted() where !busy.contains(id) {
            let decision = decisions[id] ?? "dismiss"
            // Reserve this card before awaiting the journal, including against refresh and other retries.
            busy.insert(id)
            let saved = await journal.existing(operation: .offerDecision, target: id,
                                               fingerprint: Self.fingerprint(id: id, decision: decision))
            guard isCurrent(current, hub: currentHub) else { return }
            guard canDecide() else {
                busy.remove(id)
                return
            }
            guard saved != nil else {
                busy.remove(id)
                pending.remove(id)
                decisions.removeValue(forKey: id)
                notice = IdeasCopy.expired
                _ = save()
                continue
            }
            _ = await deliver(id: id, decision: decision, generation: current, hub: currentHub, announces: false)
            guard isCurrent(current, hub: currentHub) else { return }
            busy.remove(id)
        }
    }

    func dismissNotice() { notice = nil }

    private func recoverCacheIfNeeded() {
        guard cacheUnavailable, busy.isEmpty, loadState != .loading else { return }
        // File protection can lift after launch. Recover saved owner decisions before any new cache write;
        // a fresh server list alone cannot tell us which unanswered decisions this phone already made.
        loadCached()
    }

    /// A new pairing invalidates old replies even when the computer is unchanged.
    /// Only a same-computer pairing that still grants reading may retain its cached ideas.
    func pairingCommitted() {
        guard isPaired(), canRead(), let currentHub = hubID(), currentHub == cacheHubID else {
            _ = clearAfterUnpair()
            return
        }
        generation = UUID()
        holdsEnabled = false
        ideas.removeAll { $0.hold != nil || $0.isCalendarHoldOffer }
        busy = []
        loadState = .idle
        repairNeeded = false
        notice = nil
    }

    @discardableResult
    func clearAfterUnpair() -> Bool {
        generation = UUID()
        ideas = []
        readAt = nil
        pending = []
        decisions = [:]
        holdsEnabled = false
        busy = []
        loadState = .idle
        repairNeeded = false
        notice = nil
        cacheUnavailable = false
        cacheHubID = hubID()
        do {
            if FileManager.default.fileExists(atPath: fileURL.path) {
                try FileManager.default.removeItem(at: fileURL)
            }
            return true
        } catch {
            cacheUnavailable = true
            return false
        }
    }

    static func fingerprint(id: String, decision: String = "dismiss") -> String {
        WriteFingerprint.make([id, decision])
    }

    private func deliver(id: String, decision: String, generation current: UUID, hub currentHub: String,
                         announces: Bool) async -> Bool {
        let outcome = await writer.send(.offerDecision, target: id, choice: decision,
                                        fingerprint: Self.fingerprint(id: id, decision: decision)) { key in
            guard self.isCurrent(current, hub: currentHub), self.canDecide() else { throw CancellationError() }
            let result = try await self.hub.declineOffer(id: id, IdeaDeclineRequest(idempotencyKey: key,
                                                                                  decision: decision))
            guard result.declined else { throw HubRequestError.malformedResponse }
            return result
        }
        guard isCurrent(current, hub: currentHub), canDecide() else { return false }
        switch outcome {
        case .sent:
            pending.remove(id)
            decisions.removeValue(forKey: id)
            ideas.removeAll { $0.id == id }
            _ = save()
            return true
        case .failed(_, .keep):
            if announces { notice = IdeasCopy.waiting }
        case .failed(let error, .drop), .failed(let error, .changed):
            if Self.isReadRefusal(error) {
                refuseAccess()
            } else {
                pending.remove(id)
                decisions.removeValue(forKey: id)
                if case HubRequestError.rejected(let status, _) = error, status == 404 || status == 410 {
                    ideas.removeAll { $0.id == id }
                    notice = IdeasCopy.gone
                } else {
                    notice = IdeasCopy.generic
                }
                _ = save()
            }
        case .notSaved:
            pending.remove(id)
            decisions.removeValue(forKey: id)
            _ = save()
            notice = IdeasCopy.saveFailed
        case .cancelled:
            break
        }
        return false
    }

    private func isCurrent(_ current: UUID, hub currentHub: String) -> Bool {
        guard current == generation else { return false }
        guard isPaired(), canRead(), hubID() == currentHub else {
            _ = clearAfterUnpair()
            return false
        }
        return true
    }

    private static func isReadRefusal(_ error: Error) -> Bool {
        switch error {
        case HubRequestError.forbidden, HubRequestError.notPaired, HubRequestError.rejectedEnvelope:
            return true
        case HubRequestError.rejected(let status, _):
            return status == 401 || status == 403
        case RelayClientError.unauthorized, RelayClientError.forbidden:
            return true
        default:
            return false
        }
    }

    private func refuseAccess() {
        _ = clearAfterUnpair()
        repairNeeded = true
        loadState = .failed
    }

    @discardableResult
    private func save() -> Bool {
        guard !cacheUnavailable, isPaired(), canRead(), let currentHub = hubID() else { return false }
        do {
            let saved = Saved(hubID: currentHub, ideas: ideas, readAt: readAt, pending: pending,
                              decisions: decisions, holdsEnabled: holdsEnabled)
            let data = try Self.encoder.encode(saved)
            try FileManager.default.createDirectory(at: fileURL.deletingLastPathComponent(),
                                                     withIntermediateDirectories: true)
            try data.write(to: fileURL, options: [.atomic, .completeFileProtection])
            return true
        } catch {
            return false
        }
    }

    private static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return encoder
    }()

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }()
}

enum IdeasCopy {
    static func text(en: String, zhCN: String, locale: Locale = .current) -> String {
        ChatPresentationLanguage(locale: locale).text(en: en, zhCN: zhCN)
    }

    static var waiting: String { text(en: "Saved on this phone. Your computer has not confirmed yet. Pull down to try again.",
                                      zhCN: "已保存在手机上，电脑尚未确认。请下拉重试。") }
    static var expired: String { text(en: "A saved answer could not be confirmed. Refresh the ideas, then choose again.",
                                      zhCN: "无法确认已保存的选择。请刷新想法，然后重新选择。") }
    static var saveFailed: String { text(en: "Isoform could not save your answer on this phone. Unlock this phone and try again.",
                                         zhCN: "Isoform 无法在手机上保存你的选择。请解锁手机后重试。") }
    static func handoffFailed(locale: Locale = .current) -> String {
        text(en: "Isoform could not prepare this draft on this phone. Refresh Ideas and try again.",
             zhCN: "Isoform 无法在手机上准备这份草稿。请刷新想法后重试。", locale: locale)
    }
    static var generic: String { text(en: "Isoform could not dismiss this idea. Pull down to refresh and try again.",
                                      zhCN: "Isoform 无法忽略这个想法。请下拉刷新后重试。") }
    static var gone: String { text(en: "This idea is no longer waiting on your computer.",
                                   zhCN: "这个想法已不在电脑上等待处理。") }
    static var needsRefresh: String { text(en: "Refresh Ideas before sending this saved idea to chat.",
                                           zhCN: "请先刷新想法，再将这个已保存的想法带入聊天。") }
}

// MARK: Ideas presentation

/// Both calls affect local navigation and a composer draft. The user still has to tap Send.
@MainActor
enum IdeaHandoff {
    @discardableResult
    static func open(_ idea: IdeaOffer, chat: ChatStore, router: NotificationRouter) -> Bool {
        guard let reference = idea.chatReference else {
            chat.notice = IdeasCopy.needsRefresh
            return false
        }
        guard chat.prefillMainChat(idea.prompt, offerReferences: [reference]) else { return false }
        router.openMainChat()
        return true
    }
}

struct IdeasView: View {
    @ObservedObject var ideas: IdeasStore
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var brief: BriefStore
    @EnvironmentObject private var chat: ChatStore
    @EnvironmentObject private var router: NotificationRouter
    @Environment(\.locale) private var locale

    private var copy: ChatPresentationLanguage { ChatPresentationLanguage(locale: locale) }

    /// The "Why" line under a card's title, or nil when the explanation is empty or only whitespace.
    static func whyLine(for idea: IdeaOffer, locale: Locale) -> String? {
        let explanation = idea.explanation(locale: locale)
        guard !explanation.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return ChatPresentationLanguage(locale: locale).text(en: "Why: \(explanation)", zhCN: "原因：\(explanation)")
    }

    private var scopes: [String] { pairing.paired?.scopes ?? [] }
    private var canRead: Bool { scopes.contains("offers:read") }
    private var canDecide: Bool { scopes.contains("offers:decide") }
    private var canChat: Bool { SettingsView.allowsChat(scopes) }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text(copy.text(en: "Work I can take on, waiting for you", zhCN: "我能帮你做的事，等你决定"))
                    .font(.subheadline)
                    .foregroundStyle(YansuTheme.secondary)
                if !pairing.isPaired {
                    PairPromptCard(title: copy.text(en: "See what Isoform can take on.", zhCN: "看看 Isoform 能帮你做什么。"),
                                   detail: copy.text(en: "Pair this phone with the Isoform app on your computer. Nothing starts until you say so.",
                                                     zhCN: "将手机与电脑上的 Isoform 配对。只有你同意后，工作才会开始。"))
                } else if !canRead || ideas.repairNeeded {
                    EmptyStateCard(title: copy.text(en: "Pair this phone again to see your ideas.", zhCN: "重新配对手机以查看想法。"),
                                   detail: copy.text(en: "Open Settings and choose Pair again. Your ideas stay on your computer until then.",
                                                     zhCN: "打开设置，选择重新配对。在此之前，想法会保留在电脑上。"))
                        .accessibilityIdentifier("ideas.repair")
                } else {
                    pairedContent
                }
            }
            .padding(.horizontal, YansuTheme.gutter)
            .padding(.top, 8)
            .padding(.bottom, 32)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .refreshable { await ideas.refresh() }
        .task {
            guard pairing.isPaired, canRead, environment.refreshesBriefs else { return }
            await ideas.refresh()
        }
        .onChange(of: brief.hubOnline) { _, online in
            guard online == true, pairing.isPaired, canRead, environment.refreshesBriefs else { return }
            Task { await ideas.refresh() }
        }
        .onChange(of: scopes) { _, _ in
            guard !canRead else { return }
            _ = ideas.clearAfterUnpair()
        }
        .yansuScreen()
        .navigationTitle(copy.text(en: "Ideas", zhCN: "想法"))
        .navigationBarTitleDisplayMode(.large)
        .tabRootBar()
    }

    @ViewBuilder
    private var pairedContent: some View {
        if let notice = ideas.notice { NoticeBanner(message: notice, dismiss: ideas.dismissNotice) }
        if brief.hubOnline == false {
            NoticeBanner(message: copy.text(en: "Your computer is offline. Ideas saved on this phone are still here.",
                                            zhCN: "电脑已离线。保存在手机上的想法仍可查看。"),
                         systemImage: "wifi.slash")
        } else if ideas.loadState == .failed, !ideas.ideas.isEmpty {
            NoticeBanner(message: copy.text(en: "Showing ideas saved on this phone. Pull down to try your computer again.",
                                            zhCN: "正在显示手机上保存的想法。请下拉重试连接电脑。"))
        }
        if !canDecide || !canChat {
            Text(copy.text(en: "Choose Pair again in Settings to use both idea actions on this phone.",
                           zhCN: "请在设置中选择重新配对，以在手机上使用这两个操作。"))
                .font(.footnote)
                .foregroundStyle(YansuTheme.secondary)
        }
        if ideas.ideas.isEmpty {
            if ideas.loadState == .loading || ideas.loadState == .idle && ideas.readAt == nil {
                ProgressView(copy.text(en: "Checking with your computer.", zhCN: "正在向电脑获取想法。"))
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 24)
            } else if ideas.loadState == .failed, ideas.readAt == nil {
                EmptyStateCard(title: copy.text(en: "Your ideas show here once your computer answers.", zhCN: "电脑回应后，想法会显示在这里。"),
                               detail: copy.text(en: "Pull down to try again when your computer is online.", zhCN: "电脑上线后，请下拉重试。"))
                    .accessibilityIdentifier("ideas.unreached")
            } else {
                EmptyStateCard(title: copy.text(en: "No offers right now.", zhCN: "暂时没有新想法。"),
                               detail: copy.text(en: "When Isoform spots work it can help with, it asks here first.",
                                                 zhCN: "当 Isoform 发现能帮你做的事时，会先在这里征求你的意见。"))
                    .accessibilityIdentifier("ideas.empty")
            }
        } else {
            VStack(alignment: .leading, spacing: 12) {
                Text(copy.text(en: "WAITING FOR YOU (\(ideas.ideas.count))", zhCN: "等你决定（\(ideas.ideas.count)）"))
                    .font(.caption.weight(.semibold))
                    .tracking(0.6)
                    .foregroundStyle(YansuTheme.secondary)
                    .accessibilityAddTraits(.isHeader)
                ForEach(ideas.ideas) { idea in
                    IdeaCard(idea: idea, pending: ideas.pending.contains(idea.id),
                             busy: ideas.busy.contains(idea.id), canChat: canChat,
                             canDecide: canDecide && brief.hubOnline != false && ideas.loadState != .loading,
                             doIt: {
                                 if !IdeaHandoff.open(idea, chat: chat, router: router) {
                                     ideas.notice = IdeasCopy.handoffFailed(locale: locale)
                                 }
                             },
                             decline: { Task { await ideas.decline(idea) } })
                }
            }
        }
    }
}

private struct IdeaCard: View {
    @Environment(\.locale) private var locale
    let idea: IdeaOffer
    let pending: Bool
    let busy: Bool
    let canChat: Bool
    let canDecide: Bool
    let doIt: () -> Void
    let decline: () -> Void

    private var copy: ChatPresentationLanguage { ChatPresentationLanguage(locale: locale) }

    var body: some View {
        YansuCard {
            VStack(alignment: .leading, spacing: 16) {
                HStack(alignment: .top, spacing: 12) {
                    FeedArt(category: idea.kind == "topic_find" ? .watching : .ready, size: 48)
                    VStack(alignment: .leading, spacing: 6) {
                        Text(idea.title)
                            .font(.headline)
                            .foregroundStyle(YansuTheme.ink)
                            .fixedSize(horizontal: false, vertical: true)
                        if let why = IdeasView.whyLine(for: idea, locale: locale) {
                            Text(why)
                                .font(.subheadline)
                                .foregroundStyle(YansuTheme.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                                .accessibilityIdentifier("ideas.why")
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                if pending {
                    Text(copy.text(en: "Waiting for your computer to confirm.", zhCN: "正在等待电脑确认。"))
                        .font(.footnote)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("ideas.pending")
                }
                if idea.chatReference == nil {
                    Text(copy.text(en: "Refresh Ideas before sending this saved idea to chat.",
                                   zhCN: "请先刷新想法，再将这个已保存的想法带入聊天。"))
                        .font(.footnote)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("ideas.refreshBeforeChat")
                }
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 12) { actions }
                    VStack(spacing: 12) { actions }
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel(idea.title)
        .accessibilityIdentifier("ideas.card")
    }

    @ViewBuilder
    private var actions: some View {
        Button(copy.text(en: "Do it", zhCN: "就做这个"), action: doIt)
            .buttonStyle(YansuPrimaryButtonStyle())
            .disabled(!canChat || pending || busy || idea.chatReference == nil)
            .accessibilityLabel(copy.text(en: "Do it: \(idea.title)", zhCN: "就做这个：\(idea.title)"))
            .accessibilityHint(copy.text(en: "Opens Chat with this idea ready to send. Nothing is sent yet.",
                                         zhCN: "打开聊天，并填入这个想法。暂时不会发送。"))
            .accessibilityIdentifier("ideas.doIt")
        Button(copy.text(en: "Not for me", zhCN: "不适合我"), action: decline)
            .buttonStyle(YansuSecondaryButtonStyle())
            .disabled(!canDecide || pending || busy)
            .accessibilityLabel(copy.text(en: "Not for me: \(idea.title)", zhCN: "不适合我：\(idea.title)"))
            .accessibilityHint(copy.text(en: "Confirms it is you before dismissing this idea on your computer.",
                                         zhCN: "确认是你本人后，在电脑上忽略这个想法。"))
            .accessibilityIdentifier("ideas.notForMe")
    }
}
