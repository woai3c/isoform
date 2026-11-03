import SwiftUI

/// The Chat tab is the chat, always: the phone shows no list of chats and starts none (2026-10-16). Other chats
/// on the Mac stay data the phone reads for the chat's identity. Before the chat is known the tab says
/// why: not paired, a pairing to renew, or the Mac still being asked.
struct ChatTabView: View {
    @EnvironmentObject private var chat: ChatStore
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var router: NotificationRouter
    @EnvironmentObject private var environment: AppEnvironment
    var showsProactiveSheet = true
    @State private var showsSees = false

    private var chatScopeGranted: Bool {
        SettingsView.allowsChat(pairing.paired?.scopes ?? [])
    }

    /// The chat the Mac lists (PROTOCOL.md 15.4), or the one this iPhone remembers from its last list.
    private var mainChatID: String? {
        chat.chats.first { $0.kind == .main }?.id
    }

    var body: some View {
        Group {
            if !pairing.isPaired {
                statusPage { unpairedContent }
            } else if !chatScopeGranted {
                statusPage {
                    ChatRepairCard(message: ChatCopy.legacyPairing)
                }
            } else if chat.repairNeeded {
                statusPage { ChatRepairCard(message: ChatRepairCard.refusedMessage) }
            } else if let route = router.topicFilter {
                TopicView(route: route, topics: environment.topics, embedded: true)
                    .id(route.id)
            } else if let mainChatID {
                ChatView(chatID: mainChatID, usesTabTitle: true)
                    .id(mainChatID)
                    .safeAreaInset(edge: .top, spacing: 0) {
                        // What Isoform is doing, only while true (Phone Threads §5).
                        IsoformPresenceLine(presence: chat.activity)
                    }
            } else {
                statusPage { findingMainChat }
                    .refreshable {
                        await chat.refreshList()
                    }
            }
        }
        .navigationTitle(String(localized: "Chat"))
        .navigationBarTitleDisplayMode(.large)
        .toolbar {
            if pairing.isPaired {
                ToolbarItem(placement: .topBarTrailing) {
                    IsoformMarkButton(working: chat.activity?.isFresh() == true) { showsSees = true }
                }
            }
        }
        .sheet(isPresented: $showsSees) {
            IsoformSeesSheet(hub: environment.seesHub)
                .presentationDragIndicator(.visible)
        }
        .sheet(item: Binding(
            get: { showsProactiveSheet ? chat.selectedProactive : nil },
            set: { if $0 == nil { chat.closeProactiveItem() } }
        )) { item in
            ChatProactiveItemView(item: item, chatID: chat.selectedProactiveChatID ?? "") {
                chat.closeProactiveItem()
            }
            .environmentObject(environment)
            .presentationDetents([.medium, .large])
            .presentationDragIndicator(.visible)
        }
        .task(id: pairing.paired?.deviceID) {
            guard pairing.isPaired, chatScopeGranted else { return }
            // The remembered chat shows at once, from its cached rows; the list confirms it, or finds it on a
            // first launch.
            chat.prepareMainChat()
            await chat.refreshList()
        }
    }

    private func statusPage<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                content()
            }
            .padding(.horizontal, YansuTheme.gutter)
            .padding(.top, 8)
            .padding(.bottom, 32)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .yansuScreen()
        .toolbar(.hidden, for: .tabBar)
    }

    private var unpairedContent: some View {
        YansuCard {
            VStack(alignment: .leading, spacing: 16) {
                HStack(alignment: .center, spacing: 14) {
                    YansuMark(size: 48)
                    Text(ChatCopy.introduction)
                        .font(.headline)
                        .foregroundStyle(YansuTheme.ink)
                }
                Text(ChatCopy.pairingDetail)
                    .font(.subheadline)
                    .foregroundStyle(YansuTheme.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button(ChatCopy.pair) {
                    router.openPairing()
                }
                .buttonStyle(YansuPrimaryButtonStyle())
                .accessibilityIdentifier("chat.pair")
            }
        }
    }

    /// Paired, with no chat known yet: the Mac is being asked, could not be asked, or has none.
    @ViewBuilder
    private var findingMainChat: some View {
        if case .failed(let message) = chat.listState {
            NoticeBanner(message: message, systemImage: "exclamationmark.circle")
            EmptyStateCard(title: ChatCopy.unavailableTitle, detail: ChatCopy.unavailableDetail)
            Button(String(localized: "Try again")) { Task { await chat.refreshList() } }
                .buttonStyle(YansuPrimaryButtonStyle())
                .accessibilityIdentifier("chat.list.retry")
        } else if chat.listState == .loaded {
            EmptyStateCard(title: ChatCopy.noMainChatTitle, detail: ChatCopy.noMainChatDetail)
        } else {
            ChatLoadingSkeleton()
        }
    }
}

/// Asks for Pair again in Settings: for a pairing from before Chat existed, or
/// one the relay no longer accepts.
struct ChatRepairCard: View {
    static let refusedMessage = ChatCopy.pairingRefused

    let message: String
    @EnvironmentObject private var router: NotificationRouter

    var body: some View {
        YansuCard {
            VStack(alignment: .leading, spacing: 12) {
                Text(ChatCopy.pairAgain)
                    .font(.headline)
                    .foregroundStyle(YansuTheme.ink)
                Text(message)
                    .font(.subheadline)
                    .foregroundStyle(YansuTheme.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button(ChatCopy.openSettings) {
                    router.selectedTab = .settings
                }
                .buttonStyle(YansuPrimaryButtonStyle())
            }
        }
        .accessibilityIdentifier("chat.repair")
    }
}
