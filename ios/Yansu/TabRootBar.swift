import SwiftUI

enum PairingLineCopy {
    /// `lastHeard` is the time of the newest computer data the phone holds. With it, an unknown status says when the
    /// phone last heard from the computer (P144); without it, the unknown status says only that it cannot tell.
    static func connection(remote: Bool, online: Bool?, lastHeard: Date? = nil) -> String {
        switch online {
        case .some(true): String(localized: "Online")
        case .some(false):
            remote ? String(localized: "Your computer is asleep; this will send when it wakes.")
                : String(localized: "Offline")
        case .none:
            if let lastHeard {
                String(format: String(localized: "Can't tell right now · last heard %@"), BriefClock.text(lastHeard))
            } else {
                String(localized: "Can't tell right now")
            }
        }
    }
}

/// The height of the bottom chrome the tabs draw over their content (the floating bar and the mini player),
/// set by RootView. Tab pages do not receive that inset as a safe area, so a page with a bottom composer reads
/// it here and lifts the composer above the bar.
private struct TabChromeHeightKey: EnvironmentKey {
    static let defaultValue: CGFloat = 0
}

extension EnvironmentValues {
    var tabChromeHeight: CGFloat {
        get { self[TabChromeHeightKey.self] }
        set { self[TabChromeHeightKey.self] = newValue }
    }
}

extension View {
    func tabRootBar() -> some View {
        modifier(TabRootBar())
    }

    func tabBarScrollVisibility() -> some View {
        modifier(TabBarScrollVisibility())
    }

    /// P137: nothing under the title bar reads at any scroll position. The bar is solid in the page's color up to its
    /// edge, on every system; iOS 26 also gets the hard scroll-edge style.
    func hardTitleEdge() -> some View {
        modifier(HardTitleEdge())
    }
}

private struct HardTitleEdge: ViewModifier {
    @ViewBuilder
    func body(content: Content) -> some View {
        if #available(iOS 26.0, *) {
            content
                .scrollEdgeEffectStyle(.hard, for: .top)
                .toolbarBackground(YansuTheme.background, for: .navigationBar)
                .toolbarBackground(.visible, for: .navigationBar)
        } else {
            content
                .toolbarBackground(YansuTheme.background, for: .navigationBar)
                .toolbarBackground(.visible, for: .navigationBar)
        }
    }
}

private struct TabRootBar: ViewModifier {
    func body(content: Content) -> some View {
        content
            .hardTitleEdge()
            .toolbar {
                if #available(iOS 26.0, *) {
                    ToolbarItemGroup(placement: .topBarTrailing) {
                        OwnerProfileButton()
                    }
                    .sharedBackgroundVisibility(.hidden)
                } else {
                    ToolbarItemGroup(placement: .topBarTrailing) {
                        OwnerProfileButton()
                    }
                }
            }
            .toolbar(.hidden, for: .tabBar)
            .tint(YansuTheme.ink)
    }
}

struct OwnerProfileButton: View {
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var router: NotificationRouter

    private var ownerInitial: String? {
        guard let hubName = pairing.paired?.hubName,
              let suffix = hubName.range(of: "'s") else {
            return nil
        }
        let owner = String(hubName[..<suffix.lowerBound]).trimmingCharacters(in: .whitespaces)
        guard let initial = owner.first else { return nil }
        return String(initial).uppercased()
    }

    var body: some View {
        Button { router.openSettings() } label: {
            Group {
                if let ownerInitial {
                    Text(ownerInitial)
                        .font(.subheadline.weight(.semibold))
                } else {
                    Image(systemName: "person.fill")
                        .font(.system(size: 15, weight: .medium))
                }
            }
            .foregroundStyle(YansuTheme.accent)
            .frame(width: 34, height: 34)
            .background(YansuTheme.accentWash, in: Circle())
            .frame(width: 44, height: 44)
            .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(String(localized: "Settings"))
        .accessibilityIdentifier("root.settings")
    }
}

struct FloatingTabBar: View {
    @ObservedObject var router: NotificationRouter
    @ObservedObject var chat: ChatStore
    @ObservedObject var tasks: TasksStore
    @Environment(\.locale) private var locale
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Namespace private var selectedTabLens

    private var chatNeedsAttention: Bool {
        chat.chats.contains(where: { $0.state == .waiting }) ||
            chat.transcript?.pendingPermission?.isPermissionPrompt == true
    }

    static func titleColor(selected: Bool) -> Color {
        selected ? YansuTheme.accent : YansuTheme.secondary
    }

    var body: some View {
        HStack(spacing: 4) {
            tab(.today, title: RootView.tabTitle(.today, locale: locale), symbol: "sun.max")
            tab(.chat, title: RootView.tabTitle(.chat, locale: locale), symbol: "bubble.left",
                showsDot: chatNeedsAttention)
            tab(.tasks, title: RootView.tabTitle(.tasks, locale: locale), symbol: "checklist",
                count: tasks.firstCopyPending ? 0 : tasks.waitingCount)
        }
        .padding(router.isTabBarCompact ? 4 : 6)
        .background(.ultraThinMaterial, in: Capsule())
        .overlay(Capsule().stroke(YansuTheme.border, lineWidth: 1))
        .shadow(color: YansuTheme.ink.opacity(0.12), radius: 14, y: 5)
        .padding(.horizontal, 36)
        .padding(.top, 8)
        .padding(.bottom, 4)
        .animation(reduceMotion ? .easeInOut(duration: 0.15) : .spring(duration: 0.45),
                   value: router.selectedTab)
        .animation(reduceMotion ? .easeInOut(duration: 0.15) : .spring(duration: 0.45),
                   value: router.isTabBarCompact)
        .sensoryFeedback(.selection, trigger: router.selectedTab)
    }

    private func tab(_ tab: NotificationRouter.Tab, title: String, symbol: String,
                     showsDot: Bool = false, count: Int = 0) -> some View {
        let selected = router.selectedTab == tab
        return Button {
            router.selectedTab = tab
        } label: {
            VStack(spacing: router.isTabBarCompact ? 0 : 4) {
                ZStack(alignment: .topTrailing) {
                    Group {
                        if reduceMotion {
                            Image(systemName: symbol)
                        } else {
                            Image(systemName: symbol)
                                .symbolEffect(.bounce, value: selected)
                        }
                    }
                    .font(.system(size: 18, weight: selected ? .semibold : .regular))
                    if showsDot {
                        Circle()
                            .fill(YansuTheme.accent)
                            .frame(width: 7, height: 7)
                            .offset(x: 4, y: -3)
                    }
                    if count > 0 {
                        Text("\(count)")
                            .font(.system(size: 9, weight: .semibold))
                            .foregroundStyle(YansuTheme.surface)
                            .padding(.horizontal, 4)
                            .frame(minWidth: 16, minHeight: 16)
                            .background(YansuTheme.warning, in: Capsule())
                            .offset(x: 12, y: -7)
                    }
                }
                if !router.isTabBarCompact {
                    Text(title)
                        .font(.system(size: 10, weight: selected ? .semibold : .medium))
                        .lineLimit(1)
                        .transition(.opacity)
                }
            }
            .foregroundStyle(Self.titleColor(selected: selected))
            .frame(maxWidth: .infinity, minHeight: router.isTabBarCompact ? 36 : 48)
            .background {
                if selected {
                    RoundedRectangle(cornerRadius: 24, style: .continuous)
                        .fill(YansuTheme.accent.opacity(0.1))
                        .matchedGeometryEffect(id: "selected-tab-lens", in: selectedTabLens)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(title)
        .accessibilityValue(count > 0 ? String.localizedStringWithFormat(String(localized: "%d need you"), count) : "")
        .accessibilityIdentifier("tab.\(tab.accessibilityName)")
        .modifier(SelectedTabTrait(selected: selected))
    }
}

private struct TabBarScrollVisibility: ViewModifier {
    @EnvironmentObject private var router: NotificationRouter
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        if #available(iOS 18.0, *) {
            content
                .contentMargins(.bottom, 88, for: .scrollContent)
                .onScrollGeometryChange(for: CGFloat.self, of: { $0.contentOffset.y }) { oldValue, newValue in
                    guard abs(newValue - oldValue) > 2 else { return }
                    let compact = newValue > oldValue && newValue > 20
                    if router.isTabBarCompact != compact {
                        withAnimation(reduceMotion ? .easeInOut(duration: 0.15) : .spring(duration: 0.45)) {
                            router.isTabBarCompact = compact
                        }
                    }
                }
        } else {
            content.contentMargins(.bottom, 88, for: .scrollContent)
        }
    }
}

private struct SelectedTabTrait: ViewModifier {
    let selected: Bool

    @ViewBuilder
    func body(content: Content) -> some View {
        if selected {
            content.accessibilityAddTraits(.isSelected)
        } else {
            content
        }
    }
}

private extension NotificationRouter.Tab {
    var accessibilityName: String {
        switch self {
        case .today: "today"
        case .chat: "chat"
        case .tasks: "tasks"
        case .settings: "settings"
        }
    }
}
