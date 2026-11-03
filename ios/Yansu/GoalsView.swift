import SwiftUI

// Goals tab: what the user is tracking from the Purpose Map on the Mac, each with a round checkbox, the Mac's status
// sentence and a menu, then the ones done, then Create a goal: a category opens a sheet that hands off to the main
// chat, where Isoform refines the goal and the Mac adds it (docs/iphone-muse-design.md section 2.4).
struct GoalsView: View {
    @ObservedObject var goals: GoalStore
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var brief: BriefStore
    @EnvironmentObject private var chat: ChatStore
    @EnvironmentObject private var router: NotificationRouter
    @State private var showsAllTracking = false
    @State private var showsAllDone = false
    @State private var waitingFor: GoalView?
    @State private var waitingOn = ""
    @State private var creating: GoalCategory?
    /// The category whose sheet ended with Let's do it; the hand-off runs once the sheet is gone.
    @State private var handingOff: GoalCategory?
    /// The goal whose page is open.
    @State private var opened: GoalRoute?

    static let trackingShown = 5
    static let doneShown = 3

    private var scopes: [String] { pairing.paired?.scopes ?? [] }
    private var canRead: Bool { scopes.contains("goals:read") }
    private var canWrite: Bool { scopes.contains("goals:write") && brief.hubOnline != false }
    /// Creating happens in the chat, so it needs Chat, not the Mac online: the chat keeps the message.
    private var canCreate: Bool { SettingsView.allowsChat(scopes) }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                if !pairing.isPaired {
                    PairPromptCard(title: String(localized: "Keep track of your goals."),
                                   detail: String(localized: "Pair this phone with the Isoform app on your computer to see what you are working toward."))
                } else if !canRead {
                    EmptyStateCard(title: String(localized: "Pair this phone again to see your goals."),
                                   detail: String(localized: "Open Settings and choose Pair again. Your goals stay on your computer until then."))
                } else {
                    pairedContent
                }
            }
            .padding(.horizontal, YansuTheme.gutter)
            .padding(.top, 8)
            .padding(.bottom, 32)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .refreshable {
            await goals.refresh()
        }
        .task {
            guard pairing.isPaired, canRead, environment.refreshesBriefs else { return }
            await goals.refresh()
        }
        .onChange(of: brief.hubOnline) { _, online in
            // The Mac is back: read the list again, which also sends the moves it did not answer.
            guard online == true, pairing.isPaired, canRead, environment.refreshesBriefs else { return }
            Task { await goals.refresh() }
        }
        .yansuScreen()
        .navigationTitle("Goals")
        .navigationBarTitleDisplayMode(.large)
        .tabRootBar()
        .alert("Waiting on", isPresented: Binding(get: { waitingFor != nil }, set: { if !$0 { waitingFor = nil } })) {
            TextField("Who or what", text: $waitingOn)
            Button("Cancel", role: .cancel) { waitingFor = nil }
            Button("Save") {
                if let goal = waitingFor {
                    let on = waitingOn
                    Task { await goals.markWaiting(goal, on: on) }
                }
                waitingFor = nil
            }
        } message: {
            Text("Isoform shows this goal as waiting until you check it off.")
        }
        .sheet(item: $creating, onDismiss: handOff) { category in
            GoalCreateSheet(category: category) {
                handingOff = category
                creating = nil
            }
        }
        .navigationDestination(item: $opened) { route in
            GoalDetailView(goalID: route.id, goals: goals, canChange: canWrite)
        }
        .safeAreaInset(edge: .bottom) {
            if let undoable = goals.undoable {
                undoBanner(undoable)
                    .padding(.horizontal, YansuTheme.gutter)
                    .padding(.bottom, 8)
                    .task(id: undoable.at) {
                        try? await Task.sleep(for: .seconds(GoalStore.undoSeconds))
                        if goals.undoable?.at == undoable.at { goals.clearUndo() }
                    }
            }
        }
    }

    /// "Forgot Discovery. It won't come back on its own." with Undo, for ten seconds after a done or forget.
    private func undoBanner(_ undoable: GoalStore.Undoable) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(GoalCopy.undoMessage(undoable.action, title: undoable.title))
                .font(.subheadline)
                .foregroundStyle(YansuTheme.ink)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 8)
            Button("Undo") {
                Task { await goals.undo() }
            }
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(YansuTheme.accent)
            .accessibilityIdentifier("goals.undo")
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).stroke(YansuTheme.border, lineWidth: 1))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("goals.undoBanner")
    }

    @ViewBuilder
    private var pairedContent: some View {
        VStack(alignment: .leading, spacing: 16) {
            if let notice = goals.notice {
                NoticeBanner(message: notice, dismiss: goals.dismissNotice)
            }
            offlineBanner
        }
        .padding(.bottom, goals.notice == nil && !showsOfflineBanner ? 0 : 20)
        let tracking = goals.tracking
        let done = goals.done
        switch Self.emptyState(hasGoals: !goals.goals.isEmpty, loadState: goals.loadState, readAt: goals.readAt,
                               hubOnline: brief.hubOnline) {
        case .none:
            EmptyView()
        case .loading:
            ProgressView("Checking with your computer.")
                .frame(maxWidth: .infinity, alignment: .center)
                .padding(.vertical, 24)
        case .unreached:
            placeholder(title: String(localized: "Your goals show here once your computer answers."),
                        detail: brief.hubOnline == false
                            ? String(localized: "Your computer is offline. Isoform shows your goals as soon as it is back.")
                            : String(localized: "Isoform could not reach your computer. Pull down to try again."),
                        identifier: "goals.unreached")
        case .noGoals:
            placeholder(title: String(localized: "No goals yet."), detail: String(localized: "Pick an area below and Isoform will help you set one."),
                        identifier: "goals.empty")
        }
        if !tracking.isEmpty {
            // The subtitle under the page title names this list; it shows only over goals being tracked.
            Text("Tracking")
                .font(.headline)
                .foregroundStyle(YansuTheme.secondary)
                .accessibilityAddTraits(.isHeader)
                .padding(.bottom, 20)
            section(title: nil, items: tracking, limit: Self.trackingShown, expanded: $showsAllTracking,
                    identifier: "goals.tracking")
        }
        if !done.isEmpty {
            section(title: String(localized: "Done (\(goals.counts.done))"), items: done, limit: Self.doneShown,
                    expanded: $showsAllDone, identifier: "goals.done")
                .padding(.top, tracking.isEmpty ? 0 : 32)
        }
        if goals.counts.onMacOnly > 0 {
            Text(goals.counts.onMacOnly == 1
                 ? String(localized: "1 goal stays on your computer because it repeats one of your mail subjects.")
                 : String(localized: "\(goals.counts.onMacOnly) goals stay on your computer because they repeat your mail subjects."))
                .font(.footnote)
                .foregroundStyle(YansuTheme.tertiary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 20)
                .accessibilityIdentifier("goals.onMacOnly")
        }
        Rectangle()
            .fill(YansuTheme.border)
            .frame(height: 1)
            .padding(.vertical, 28)
            .accessibilityHidden(true)
        createSection
    }

    /// What stands where the list would be when there is nothing to list.
    enum EmptyState: Equatable {
        case none
        case loading
        /// The first read failed, or the Mac is offline, and this iPhone has no list yet: only that is said, never
        /// "No goals yet", which would claim the Mac has none.
        case unreached
        case noGoals
    }

    static func emptyState(hasGoals: Bool, loadState: GoalStore.LoadState, readAt: Date?, hubOnline: Bool?) -> EmptyState {
        guard !hasGoals else { return .none }
        guard readAt == nil else { return .noGoals }
        switch loadState {
        case .loading:
            return .loading
        case .failed:
            return .unreached
        case .idle, .loaded:
            return hubOnline == false ? .unreached : .noGoals
        }
    }

    private func placeholder(title: String, detail: String, identifier: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.body.weight(.semibold))
                .foregroundStyle(YansuTheme.ink)
            Text(detail)
                .font(.subheadline)
                .foregroundStyle(YansuTheme.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(identifier)
    }

    private func section(title: String?, items: [GoalView], limit: Int, expanded: Binding<Bool>,
                         identifier: String) -> some View {
        let shown = expanded.wrappedValue ? items : Array(items.prefix(limit))
        let hidden = items.count - shown.count
        return VStack(alignment: .leading, spacing: 24) {
            if let title {
                Text(title)
                    .font(.headline)
                    .foregroundStyle(YansuTheme.secondary)
                    .accessibilityAddTraits(.isHeader)
                    .padding(.bottom, -4)
            }
            ForEach(shown) { goal in
                GoalRow(goal: goal, canChange: canWrite, busy: goals.busy.contains(goal.id),
                        pending: goals.pending[goal.id] != nil) {
                    Task { await goals.toggle(goal) }
                } markWaiting: {
                    waitingOn = ""
                    waitingFor = goal
                } forget: {
                    Task { await goals.forget(goal) }
                } open: {
                    opened = GoalRoute(id: goal.id)
                }
            }
            if hidden > 0 {
                Button {
                    expanded.wrappedValue = true
                } label: {
                    Text("Show \(hidden) more")
                        .font(.subheadline.weight(.medium))
                        .foregroundStyle(YansuTheme.secondary)
                        .frame(minHeight: 32, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .padding(.leading, 44)
                .padding(.top, -8)
                .accessibilityIdentifier("\(identifier).more")
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier(identifier)
    }

    private var createSection: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "plus")
                    .font(.headline)
                    .accessibilityHidden(true)
                Text("Create a goal")
                    .font(.title3.weight(.semibold))
            }
            .foregroundStyle(YansuTheme.ink)
            .accessibilityElement(children: .combine)
            .accessibilityAddTraits(.isHeader)
            Text("Pick an area and Isoform will help you shape a goal in chat, then keep track of it here.")
                .font(.subheadline)
                .foregroundStyle(YansuTheme.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 6)
                .padding(.bottom, 8)
            ForEach(GoalCategory.allCases) { category in
                Button {
                    creating = category
                } label: {
                    HStack(spacing: 14) {
                        Image(systemName: category.systemImage)
                            .font(.body)
                            .foregroundStyle(YansuTheme.secondary)
                            .frame(width: 24)
                            .accessibilityHidden(true)
                        Text(category.title)
                            .font(.body)
                            .foregroundStyle(YansuTheme.ink)
                        Spacer(minLength: 8)
                        Image(systemName: "plus")
                            .font(.subheadline.weight(.semibold))
                            .foregroundStyle(YansuTheme.tertiary)
                            .accessibilityHidden(true)
                    }
                    .frame(minHeight: 52)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(!canCreate)
                .accessibilityLabel(category.title)
                .accessibilityIdentifier("goals.category.\(category.rawValue)")
            }
            if !canCreate {
                Text("Pair this phone again in Settings to create goals in Chat.")
                    .font(.footnote)
                    .foregroundStyle(YansuTheme.tertiary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 8)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("goals.create")
    }

    /// After Let's do it: the chat opens with the sentence to finish in its composer, unsent.
    private func handOff() {
        guard let category = handingOff else { return }
        handingOff = nil
        chat.prefillMainChat(category.chatPrefill)
        router.openMainChat()
    }

    /// The banner speaks for a list read earlier; with none, the empty state says the Mac has not answered.
    private var showsOfflineBanner: Bool {
        goals.readAt != nil && (brief.hubOnline == false || goals.loadState == .failed)
    }

    @ViewBuilder
    private var offlineBanner: some View {
        if let readAt = goals.readAt {
            if brief.hubOnline == false {
                NoticeBanner(message: String(localized: "Showing your goals from \(BriefView.asOfText(readAt)). Your computer is offline, so they cannot change now."),
                             systemImage: "wifi.slash")
            } else if goals.loadState == .failed {
                NoticeBanner(message: String(localized: "Showing your goals from \(BriefView.asOfText(readAt)). Isoform could not refresh them."),
                             systemImage: "arrow.triangle.2.circlepath")
            }
        }
    }
}

/// The sheet a category opens: how a goal is made, and one button that hands off to the chat.
struct GoalCreateSheet: View {
    let category: GoalCategory
    let start: () -> Void
    @Environment(\.dismiss) private var dismiss
    /// The sheet's content height, measured, so the sheet fits it.
    @State private var height: CGFloat = 260

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                Text("Create a new goal")
                    .font(.title3.weight(.semibold))
                    .foregroundStyle(YansuTheme.ink)
                    .accessibilityAddTraits(.isHeader)
                Spacer(minLength: 12)
                Button {
                    dismiss()
                } label: {
                    Image(systemName: "xmark")
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(YansuTheme.tertiary)
                        .frame(width: 44, height: 44)
                }
                .accessibilityLabel("Close")
                .padding(.trailing, -12)
            }
            Label(category.title, systemImage: category.systemImage)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(YansuTheme.secondary)
                .padding(.top, 2)
            Text("We'll refine it together in chat, then I'll track it here.")
                .font(.body)
                .foregroundStyle(YansuTheme.ink)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 16)
            Button("Let's do it", action: start)
                .buttonStyle(YansuPrimaryButtonStyle())
                .accessibilityIdentifier("goals.create.start")
                .padding(.top, 24)
        }
        .padding(.horizontal, 20)
        .padding(.top, 12)
        .padding(.bottom, 20)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        // Only as tall as what it says: never a blank band under the copy.
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
        .background(YansuTheme.background.ignoresSafeArea())
        .presentationDetents([.height(height)])
        .presentationDragIndicator(.visible)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("goals.create.sheet")
    }
}
