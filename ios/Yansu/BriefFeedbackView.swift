import SwiftUI

/// The full brief keeps the feedback and evidence controls that used to be in Today.
struct BriefFeedbackView: View {
    let brief: MobileBriefV1
    let now: Date
    @ObservedObject var feed: FeedStore
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var briefStore: BriefStore
    @EnvironmentObject private var today: TodayStore
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var holds = DecisionHoldCoordinator()
    @State private var held: Set<String> = []
    @State private var correcting: FeedCardModel?
    @State private var correctionDraft = ""
    @State private var correctionFailed = false
    @State private var evidence: EvidenceRequest?

    var body: some View {
        let cards = FeedAssembly.cards(in: brief)
        if !cards.isEmpty {
            VStack(alignment: .leading, spacing: 12) {
                SectionLabel(title: "Cards")
                if let notice = feed.notice { NoticeBanner(message: notice, dismiss: feed.dismissNotice) }
                ForEach(cards) { card in
                    if let hold = holds.holds.first(where: { $0.id == card.key }) {
                        folded(card, label: hold.label) {
                            if holds.undo(hold.id) { held.remove(hold.id) }
                        }
                    } else if let pending = feed.pendingToday.first(where: { $0.todayFeedback?.cardKey == card.key }) {
                        folded(card, label: String(localized: "Sending when your computer is back"),
                               undo: pending.todayFeedback?.dispatchedAt == nil ? { Task { await feed.undoToday(pending.key) } } : nil)
                    } else if let choice = feed.choices[card.key], choice.action == .notRelevant {
                        folded(card, label: choice.delivery == .pending ? String(localized: "Sending when your computer is back") : String(localized: "Fewer like this"))
                    } else {
                        FeedCard(card: card, choice: feed.choices[card.key],
                             canGiveFeedback: SettingsView.allowsFeedFeedback(pairing.paired?.scopes ?? []),
                             canSend: true, now: now,
                             like: { Task { await hold(.like, card: card) } },
                             notRight: {
                                 if card.memoryID != nil {
                                     if SettingsView.allowsMemoryCorrection(pairing.paired?.scopes ?? []) {
                                         correctionDraft = ""
                                         correctionFailed = false
                                         correcting = card
                                     }
                                     else { feed.notice = String(localized: "Choose Pair again in Settings to correct what Isoform remembers.") }
                                 } else { Task { await hold(.notRelevant, card: card) } }
                             },
                             showSources: { evidence = EvidenceRequest(title: card.item.title ?? String(localized: "Sources"),
                                                                         sources: card.sources) },
                             briefFeedback: true,
                             correction: correcting?.key == card.key ? AnyView(InlineMemoryCorrection(initialReason: correctionDraft,
                                                                                                      initiallyFailed: correctionFailed,
                                                                                                      cancel: { correcting = nil }) { reason in
                                 await hold(.notRight, card: card, reason: reason)
                             }) : nil)
                    }
                }
            }
            .sheet(item: $evidence) { EvidenceSheet(request: $0) }
            .onDisappear { Task { await holds.flushAll() } }
            .onChange(of: scenePhase) { _, phase in
                if DecisionHoldCoordinator.shouldFlush(when: phase) { Task { await holds.flushAll() } }
            }
        }
    }

    private func folded(_ card: FeedCardModel, label: String, undo: (() -> Void)? = nil) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "xmark.circle.fill").foregroundStyle(YansuTheme.secondary).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 4) {
                Text(verbatim: card.item.title ?? String(localized: "Untitled")).font(.subheadline.weight(.semibold))
                HStack(spacing: 4) {
                    Text(verbatim: label)
                    if let undo {
                        Text(verbatim: "·")
                        Button("Undo", action: undo).accessibilityIdentifier("brief.feedback.undo")
                    }
                }.font(.footnote)
            }
            Spacer(minLength: 0)
        }
        .foregroundStyle(YansuTheme.secondary)
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: YansuTheme.cornerRadius))
        .overlay { RoundedRectangle(cornerRadius: YansuTheme.cornerRadius).strokeBorder(YansuTheme.border) }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("brief.feedback.folded")
    }

    private func hold(_ action: FeedFeedbackAction, card: FeedCardModel, reason: String? = nil) async -> Bool {
        guard let approval = await feed.authorizeFeedback(action, for: card, reason: reason) else { return false }
        if today.hubUnreachable || briefStore.hubOnline == false {
            let version = today.snapshot?.briefs.first { $0.id == card.briefID }?.bodyVersion
            return await feed.queueToday(approval, bodyVersion: version)
        }
        held.insert(card.key)
        let label = action == .notRelevant ? String(localized: "Fewer like this") :
            action == .notRight ? String(localized: "Not quite") : String(localized: "Useful")
        holds.begin(.init(id: card.key, label: label,
                          detail: card.item.title ?? String(localized: "Untitled"))) {
            let saved = await feed.commitFeedback(approval)
            held.remove(card.key)
            if !saved, action == .notRight, let reason {
                correctionDraft = reason
                correctionFailed = true
                correcting = card
            }
        }
        return true
    }
}
