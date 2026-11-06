import SwiftUI

struct TasksView: View {
    var wide = false
    var navigation: DuoNavigation?
    var foldX: CGFloat?
    var windowWidth: CGFloat = 0
    @ObservedObject var tasks: TasksStore
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var brief: BriefStore
    @EnvironmentObject private var chat: ChatStore
    @EnvironmentObject private var router: NotificationRouter
    @Environment(\.locale) private var locale
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.tabChromeHeight) private var tabChromeHeight

    @State private var deck = TaskDeck()
    @State private var showsAllDone = false
    @State private var dragOffset: CGFloat = 0
    @State private var deckCardWidth: CGFloat = 1
    @State private var lastDragX: CGFloat = 0
    @State private var thresholdCrossings = 0
    @State private var actionHaptics = 0
    @State private var notNowHaptics = 0
    @State private var completionHaptics = 0
    @State private var refreshHaptics = 0
    @State private var isRefreshing = false
    @State private var showsComposer = false
    @Namespace private var threadNamespace
    @Namespace private var completionNamespace
    @State private var selectedTask: MobileTask?
    @State private var loadingNoticeTask: String?
    @State private var restoresTaskDetail = false
    @State private var restoredOnce = false
    @StateObject private var undoHolds = DecisionHoldCoordinator()
    @State private var heldOffers: [String: IdeaOffer] = [:]
    @State private var deckBusy = false
    @State private var findingMore = false
    @State private var findMoreLine: String?
    /// Offers whose Do it is in its five seconds of Undo; they show in Running.
    @State private var startingOffers: [IdeaOffer] = []
    @State private var startFailed: String?
    /// An Up next hold that waited for the computer until its time passed; Other times opens its card.
    @State private var passedHold: PhoneHoldOffer?
    @State private var holdSheet: PhoneHoldOffer?
    @State private var calendarHoldStates: [String: PhoneHoldStatus] = [:]
    var fullScreenItem = false

    private var scopes: [String] { pairing.paired?.scopes ?? [] }
    private var canRead: Bool { scopes.contains("brief:read") }
    private var canDecide: Bool { scopes.contains("offers:decide") && brief.hubOnline != false }
    private var canChat: Bool { SettingsView.allowsChat(scopes) }
    private var canStartWork: Bool { pairing.isPaired && SettingsView.allowsOneTimeWork(scopes) }
    private var page: MobileTasksPage? { tasks.page }
    private var counts: MobileTaskCounts {
        MobileTaskCounts(needsYou: tasks.waitingCount, running: page?.counts.running ?? 0,
                         doneThisWeek: page?.counts.doneThisWeek ?? 0)
    }
    private var groups: TaskGroups { TaskGroups.make(from: page?.tasks ?? [], threads: page?.threads == true) }
    private var runningTasks: [MobileTask] {
        let startingIDs = Set(startingOffers.compactMap { $0.hold.flatMap { calendarHoldStates[$0.proposal.id]?.taskId } })
        return groups.running.filter { !startingIDs.contains($0.id) }
    }
    private var visibleSections: [TaskDeck.Section] {
        var sections = TaskDeck.visibleSections(groups: groups, hasOffers: deck.current != nil,
                                                hasScheduled: !(page?.scheduled ?? []).isEmpty)
        sections.removeAll { $0 == .needsYou }
        if !tasks.waitingItems.isEmpty { sections.insert(.needsYou, at: 0) }
        if !startingOffers.isEmpty, !sections.contains(.running) {
            sections.insert(.running, at: sections.firstIndex(of: .scheduled) ?? sections.firstIndex(of: .done) ?? sections.endIndex)
        }
        return sections
    }

    var body: some View {
        ScrollViewReader { proxy in
        ScrollView {
            VStack(alignment: .leading, spacing: wide ? 24 : 14) {
                if !pairing.isPaired {
                    PairPromptCard(title: String(localized: "Pair your phone with your computer to see tasks."),
                                   detail: String(localized: "Your work stays on your computer until you pair."))
                } else if !canRead || tasks.repairNeeded {
                    EmptyStateCard(title: String(localized: "Pair this phone again to see tasks."),
                                   detail: String(localized: "Open Settings and choose Pair again. Your tasks stay on your computer until then."))
                        .accessibilityIdentifier("tasks.repair")
                } else {
                    if tasks.firstCopyPending {
                        FirstCopyShapes(tasks: true).transition(.opacity)
                    } else {
                        pairedContent
                            .transition(reduceMotion ? .opacity : .offset(y: 8).combined(with: .opacity))
                    }
                }
            }
            .padding(.horizontal, YansuTheme.gutter)
            .padding(.top, 8)
            .padding(.bottom, 24 + (wide ? 0 : tabChromeHeight))
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .tabBarScrollVisibility()
        .refreshable { await refresh(caught: true) }
        .task(id: router.upNextRequest) {
            guard let request = router.upNextRequest, router.selectedTab == .tasks else { return }
            await refresh()
            await Task.yield()
            proxy.scrollTo("tasks.upNext", anchor: .top)
            if router.upNextRequest == request { router.upNextRequest = nil }
        }
        .onAppear {
            deck.reconcile(with: environment.ideas.ideas)
            openPendingNotice()
            // Reopen a saved thread only on the first appearance: coming back from a thread must not reopen it.
            guard !restoredOnce else { Task { await refresh() }; return }
            restoredOnce = true
            if case .task(let id) = navigation?.path(for: .tasks).last,
               let task = tasks.page?.tasks.first(where: { $0.id == id }) {
                selectedTask = task
                restoresTaskDetail = !wide
            }
            Task { await refresh() }
        }
        .onChange(of: environment.ideas.ideas) { _, offers in
            withAnimation(deckAnimation) { deck.reconcile(with: offers) }
        }
        .onReceive(environment.holds.$states) { states in
            calendarHoldStates = states
            for offer in startingOffers {
                guard let id = offer.hold?.proposal.id, let state = states[id],
                      !["pending_write", "checking", HoldsStore.queuedState].contains(state.state) else { continue }
                startingOffers.removeAll { $0.id == offer.id }
                if state.state != "held" && state.state != "moved" && state.state != "removed" {
                    deck.cancelStart()
                    deck.restore(offer)
                    if state.state == HoldsStore.expiredState {
                        passedHold = offer.hold
                        startFailed = String(localized: "hold.passed")
                    } else {
                        startFailed = PhoneHoldCopy.failure(environment.holds.failures[id] ?? .hold(1))
                    }
                }
            }
        }
        .onChange(of: scenePhase) { _, phase in
            guard DecisionHoldCoordinator.shouldFlush(when: phase) else { return }
            Task { await undoHolds.flushAll() }
        }
        .onChange(of: tasks.page) { oldPage, newPage in
            openPendingNotice()
            guard let oldPage, let newPage else { return }
            let diff = TaskStatusDiff.between(previous: oldPage.tasks, current: newPage.tasks)
            if !diff.newlyFinished.isEmpty { completionHaptics += 1 }
        }
        .onChange(of: router.pendingTaskID) { _, _ in openPendingNotice() }
        .onChange(of: brief.hubOnline) { _, online in
            guard online == true, pairing.isPaired, canRead else { return }
            Task { await refresh() }
        }
        .yansuScreen()
        .navigationTitle(String(localized: "Tasks"))
        .navigationBarTitleDisplayMode(wide ? .large : .inline)
        .animation(deckAnimation, value: tasks.page)
        .animation(reduceMotion ? .easeOut(duration: 0.18) : .easeOut(duration: 0.25), value: tasks.firstCopyPending)
        .toolbar {
            if #available(iOS 26.0, *) {
                ToolbarItemGroup(placement: .topBarTrailing) {
                    addTaskButton
                    OwnerProfileButton()
                }
                .sharedBackgroundVisibility(.hidden)
            } else {
                ToolbarItemGroup(placement: .topBarTrailing) {
                    addTaskButton
                    OwnerProfileButton()
                }
            }
        }
        .toolbar(.hidden, for: .tabBar)
        .navigationDestination(for: MobileTask.self) { task in
            threadPage(task)
                .onAppear {
                    if navigation?.path(for: .tasks).last != .task(task.id) {
                        navigation?.push(.task(task.id), on: .tasks)
                    }
                }
                .onDisappear {
                    if navigation?.path(for: .tasks).last == .task(task.id) {
                        navigation?.close(on: .tasks)
                    }
                }
        }
        .navigationDestination(isPresented: $restoresTaskDetail) {
            if let selectedTask {
                TaskThreadView(task: selectedTask, tasks: tasks, onClose: {
                    restoresTaskDetail = false
                    navigation?.close(on: .tasks)
                })
            }
        }
        .sheet(item: $holdSheet, onDismiss: { Task { await environment.ideas.refresh(); await environment.tasks.refresh() } }) { offer in
            HoldSheetView(offer: offer, holds: environment.holds) { holdSheet = nil }
        }
        .sheet(isPresented: $showsComposer) {
            if tasks.brainDumpAvailable {
                BrainDumpComposer(tasks: tasks)
            } else {
                OneTimeWorkComposer { notice in tasks.notice = notice }
                    .environmentObject(environment.actions)
            }
        }
        .sensoryFeedback(.selection, trigger: thresholdCrossings)
        .sensoryFeedback(.success, trigger: actionHaptics)
        .sensoryFeedback(.success, trigger: completionHaptics)
        .sensoryFeedback(.impact(weight: .light), trigger: notNowHaptics)
        .sensoryFeedback(.selection, trigger: refreshHaptics)
        .accessibilityIdentifier("tasks.screen")
        }
    }

    private func openPendingNotice() {
        guard let id = router.pendingTaskID else { return }
        if let task = tasks.page?.tasks.first(where: { $0.id == id }) {
            selectedTask = task
            restoresTaskDetail = !wide
            router.consumePendingTaskID(id)
        } else if loadingNoticeTask != id {
            loadingNoticeTask = id
            Task {
                let result = await tasks.loadDetail(id: id)
                guard router.pendingTaskID == id else { loadingNoticeTask = nil; return }
                if case .success(let detail)? = result {
                    selectedTask = detail.task
                    restoresTaskDetail = !wide
                    router.consumePendingTaskID(id)
                }
                loadingNoticeTask = nil
            }
        }
    }

    @ViewBuilder
    private var pairedContent: some View {
        if wide {
            widePairedContent
        } else {
            narrowPairedContent
        }
    }

    @ViewBuilder
    private var narrowPairedContent: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(TasksCopy.subtitle(counts,
                                    showsDone: page?.threads != true))
                .font(.subheadline)
                .foregroundStyle(YansuTheme.secondary)
                .accessibilityIdentifier("tasks.subtitle")
            if isRefreshing {
                RefreshingMark()
                    .transition(reduceMotion ? .opacity : .scale(scale: 0.7).combined(with: .opacity))
                    .scaleEffect(isRefreshing ? 1 : 0.7)
            }
        }

        if let notice = tasks.notice {
            NoticeBanner(message: notice, dismiss: { tasks.notice = nil })
        }
        if brief.hubOnline == false {
            NoticeBanner(message: String(localized: "Your computer is offline. Tasks saved on this phone are still here."),
                         systemImage: "wifi.slash")
        }

        taskSection("section.needsYou", count: tasks.waitingCount, section: .needsYou, identifier: "tasks.needsYou") {
            if tasks.waitingItems.isEmpty {
                    EmptyStateCard(title: String(localized: "Nothing needs you right now"),
                                   detail: String(localized: "New tasks will appear here when they need your attention."))
            } else {
                VStack(spacing: 10) {
                    ForEach(tasks.waitingItems) { need in
                        if let task = need.task {
                            zoomSource(TaskRow(task: task), task)
                        } else {
                            PhoneWaitingProactiveRow(need: need)
                        }
                    }
                }
            }
        }

        upNextSection.id("tasks.upNext")

        taskSection("Running", count: runningTasks.count + startingOffers.count, section: .running,
                    identifier: "tasks.running") {
            // Running is one card with hairlines, as Done is (still 12).
            groupedRows(startingOffers.map { AnyView(startingRow($0)) } + runningTasks.map { AnyView(completionRow($0)) })
            .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
            .animation(reduceMotion ? .easeOut(duration: 0.25) : .spring(duration: 0.45), value: groups.running)
            .animation(reduceMotion ? .easeOut(duration: 0.25) : .spring(duration: 0.45), value: startingOffers)
        }

        taskSection("section.scheduled", count: scheduled.count, section: .scheduled, identifier: "tasks.scheduled") {
            VStack(spacing: 10) {
                ForEach(scheduled) { item in
                    zoomSource(TaskRow(task: item.task.titled(TaskThreadCopy.scheduledTitle(item))), item.task)
                }
            }
        }

        taskSection("section.doneThisWeek", count: groups.done.count, section: .done, identifier: "tasks.done") {
            VStack(alignment: .leading, spacing: 10) {
                let rows = showsAllDone ? groups.done : Array(groups.done.prefix(3))
                groupedRows(rows.map { task in AnyView(zoomSource(TaskRow(task: task, grouped: true), task)) })
                if groups.done.count > 3 {
                    Button(showsAllDone ? String(localized: "Show less")
                         : String.localizedStringWithFormat(String(localized: "See all %d"), groups.done.count)) {
                        showsAllDone.toggle()
                    }
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(YansuTheme.accent)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .accessibilityIdentifier("tasks.seeAllDone")
                }
            }
            .transition(reduceMotion ? .opacity : .move(edge: .bottom).combined(with: .opacity))
            .animation(reduceMotion ? .easeOut(duration: 0.25) : .spring(duration: 0.45), value: groups.done)
        }
    }

    /// Rows in one card, divided by a hairline that starts at the text.
    private func groupedRows(_ rows: [AnyView]) -> some View {
        VStack(spacing: 0) {
            ForEach(Array(rows.enumerated()), id: \.offset) { index, row in
                row
                if index < rows.count - 1 {
                    Divider().overlay(YansuTheme.border).padding(.leading, 56)
                }
            }
        }
        .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).stroke(YansuTheme.border, lineWidth: 1))
        .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    private var scheduled: [MobileScheduledTask] { page?.scheduled ?? [] }

    /// A task you just started: in Running at once, with "Started · Undo" for five seconds (click rule v1.1.0).
    private func startingRow(_ offer: IdeaOffer) -> some View {
        let holdStatus = offer.hold.flatMap { calendarHoldStates[$0.proposal.id] }
        let task = MobileTask(id: holdStatus?.taskId ?? "starting." + offer.id, title: offer.displayTitle(locale: locale), status: "in_progress", group: "running",
                              blockedReason: nil, runner: nil, progress: nil, createdAt: "", updatedAt: "")
        return TaskRow(task: task, onSelect: holdStatus?.taskId == nil ? nil : {
            selectedTask = task
            if wide {
                if navigation?.path(for: .tasks).last != .task(task.id) { navigation?.push(.task(task.id), on: .tasks) }
            } else {
                restoresTaskDetail = true
            }
        }, grouped: true, trailing: AnyView(
            Button(String(localized: "Undo")) {
                if let id = offer.hold?.proposal.id {
                    let wasQueued = holdStatus?.state == HoldsStore.queuedState
                    Task {
                        if wasQueued, await environment.holds.undo(id: id) {
                            startingOffers.removeAll { $0.id == offer.id }
                            deck.cancelStart()
                            deck.restore(offer)
                        } else if await environment.holds.undo(id: id) {
                            startingOffers.removeAll { $0.id == offer.id }
                            await environment.ideas.refresh()
                            deck.cancelStart()
                            if let refreshed = environment.ideas.ideas.first(where: { $0.id == offer.id }),
                               refreshed.hold?.proposal.id != id {
                                deck.restore(refreshed)
                            } else {
                                deck.commitHold(offer)
                                startFailed = String(localized: "hold.failed")
                            }
                        }
                    }
                } else { undoStart(offer) }
            }
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(YansuTheme.accent)
                .disabled(offer.hold != nil && holdStatus?.canUndoHold != true)
                .accessibilityIdentifier("tasks.starting.undo")
        ), lineOverride: offer.hold == nil ? String(localized: "Started") :
            (holdStatus?.state == "pending_write" ? String(localized: "hold.held")
                : holdStatus?.state == HoldsStore.queuedState ? String(localized: "hold.away") : String(localized: "hold.checking")))
        .transition(reduceMotion ? .opacity : .move(edge: .top).combined(with: .opacity))
    }

    @ViewBuilder
    private var widePairedContent: some View {
        if fullScreenItem, let selectedTask {
            TaskThreadView(task: selectedTask, tasks: tasks, wide: true) {
                withAnimation(reduceMotion ? .easeOut(duration: 0.2) : .spring(duration: 0.48)) {
                    self.selectedTask = nil
                    navigation?.close(on: .tasks)
                }
            }
            .frame(maxWidth: 680)
            .frame(maxWidth: .infinity, alignment: .center)
            .padding(.top, 20)
        } else {
            HStack(alignment: .top, spacing: DuoLayout.gutter) {
            VStack(alignment: .leading, spacing: 20) {
                VStack(alignment: .leading, spacing: 5) {
                    Text(TasksCopy.subtitle(counts))
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                    if let notice = tasks.notice { NoticeBanner(message: notice, dismiss: { tasks.notice = nil }) }
                }
                taskSection("Needs you", count: tasks.waitingCount, showsCount: true, section: .needsYou,
                            identifier: "tasks.needsYou") {
                    ForEach(tasks.waitingItems) { need in
                        if let task = need.task { taskRows([task]) }
                        else { PhoneWaitingProactiveRow(need: need) }
                    }
                }
                taskSection("Running", count: groups.running.count, section: .running, identifier: "tasks.running") {
                    taskRows(groups.running)
                }
                taskSection("Done this week", count: groups.done.count, section: .done,
                            identifier: "tasks.done") {
                    taskRows(groups.done)
                }
            }
            .frame(width: 360, alignment: .leading)

            Group {
                if let selectedTask {
                    TaskThreadView(task: selectedTask, tasks: tasks, wide: true,
                                   onClose: {
                                       withAnimation(reduceMotion ? .easeOut(duration: 0.2) : .spring(duration: 0.48)) {
                                           self.selectedTask = nil
                                           navigation?.close(on: .tasks)
                                       }
                                   }, foldX: foldX, windowWidth: windowWidth)
                    .matchedGeometryEffect(id: "task.\(selectedTask.id)", in: taskDetailNamespace)
                } else {
                    upNextSection.id("tasks.upNext")
                        .frame(maxWidth: .infinity, alignment: .center)
                }
            }
            .frame(maxWidth: .infinity, alignment: .topLeading)
        }
        .padding(.top, 8)
        }
    }

    @Namespace private var taskDetailNamespace

    @ViewBuilder
    private func taskRows(_ rows: [MobileTask]) -> some View {
        if rows.isEmpty {
            Text(String(localized: "Nothing here right now"))
                .font(.subheadline)
                .foregroundStyle(YansuTheme.secondary)
                .padding(.vertical, 10)
        } else {
            VStack(spacing: 10) {
                ForEach(rows) { task in
                    TaskRow(task: task, onSelect: {
                        withAnimation(reduceMotion ? .easeOut(duration: 0.2) : .spring(duration: 0.48)) {
                            selectedTask = task
                            if navigation?.path(for: .tasks).last != .task(task.id) {
                                navigation?.push(.task(task.id), on: .tasks)
                            }
                        }
                    })
                }
            }
        }
    }

    private var addTaskButton: some View {
        Button {
            Task {
                if !tasks.brainDumpAvailable { await tasks.refreshBrainDumpCapability() }
                showsComposer = true
            }
        } label: {
            Image(systemName: "plus")
                .font(.system(size: 17, weight: .semibold))
                .foregroundStyle(YansuTheme.onSolid)
                .frame(width: 34, height: 34)
                .background(YansuTheme.solid, in: Circle())
                .frame(width: 44, height: 44)
        }
        .buttonStyle(.plain)
        .disabled(!(pairing.isPaired && SettingsView.allowsOneTimeWork(scopes)))
        .accessibilityLabel(String(localized: "Give Isoform a task"))
        .accessibilityIdentifier("tasks.addTask")
    }

    @ViewBuilder
    private func taskSection<Content: View>(_ title: LocalizedStringKey, count: Int,
                                             showsCount: Bool = false,
                                             section: TaskDeck.Section,
                                             identifier: String, @ViewBuilder content: () -> Content) -> some View {
        if visibleSections.contains(section) {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                HStack(spacing: 7) {
                    Text(title)
                        .font(.title3.weight(.semibold))
                        .foregroundStyle(YansuTheme.ink)
                        .accessibilityAddTraits(.isHeader)
                    if showsCount {
                        Text("\(count)")
                            .font(.caption.weight(.semibold).monospacedDigit())
                            .foregroundStyle(YansuTheme.warning)
                            .padding(.horizontal, 7)
                            .padding(.vertical, 3)
                            .background(YansuTheme.warningWash, in: Capsule())
                            .contentTransition(.numericText())
                    }
                }
                Spacer()
            }
            .accessibilityIdentifier("\(identifier).header")
            content()
        }
        .accessibilityIdentifier(identifier)
        }
    }

    private var deckAnimation: Animation { reduceMotion ? .easeOut(duration: 0.2) : .spring(duration: 0.45) }

    private var findMoreButton: some View {
        Button(String(localized: "Find more")) { findMore() }
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(YansuTheme.accent)
            .disabled(findingMore || deckBusy || environment.ideas.loadState == .loading)
            .accessibilityIdentifier("tasks.findMore")
    }

    private var upNextSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            if visibleSections.contains(.upNext) || findingMore {
            HStack {
                Text("Up next")
                    .font(.title3.weight(.semibold))
                    .foregroundStyle(YansuTheme.ink)
                    .accessibilityAddTraits(.isHeader)
                Spacer()
                findMoreButton
            }
            } else {
                findMoreButton
            }
            ForEach(undoHolds.holds.filter { !$0.id.hasPrefix(Self.startHoldPrefix) }) { hold in
                HStack {
                    Text(hold.label + " · " + hold.detail)
                        .lineLimit(1)
                        .foregroundStyle(YansuTheme.secondary)
                        .font(.subheadline)
                    Spacer()
                    Button(String(localized: "Undo")) {
                        undo(hold.id)
                    }
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(YansuTheme.accent)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 13)
                .background(YansuTheme.surfaceWarm, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                .transition(reduceMotion ? .opacity : .move(edge: .top).combined(with: .opacity))
                .accessibilityIdentifier("tasks.undo.\(hold.id)")
            }
            if findingMore {
                YansuCard {
                    VStack(alignment: .leading, spacing: 16) {
                        Text("Finding more tasks…").font(.headline)
                        RoundedRectangle(cornerRadius: 8).fill(YansuTheme.secondary.opacity(0.12)).frame(height: 20)
                        RoundedRectangle(cornerRadius: 8).fill(YansuTheme.secondary.opacity(0.08)).frame(height: 44)
                    }
                    .frame(maxWidth: .infinity, minHeight: 180, alignment: .leading)
                    .redacted(reason: .placeholder)
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(String(localized: "Finding more tasks…"))
                .accessibilityIdentifier("tasks.findMore.placeholder")
            } else if let offer = deck.current {
                offerCard(offer)
                        .id(offer.id)
                        .transition(reduceMotion ? .opacity : .offset(y: 18).combined(with: .opacity))
                        .background(alignment: .bottom) {
                            if deck.next != nil {
                                // The next card's edge rises and widens as you drag, so you see what is under it.
                                let rise = reduceMotion ? 0 : motion(for: dragOffset).progress
                                RoundedRectangle(cornerRadius: 22, style: .continuous)
                                    .fill(YansuTheme.surface)
                                    .overlay {
                                        RoundedRectangle(cornerRadius: 22, style: .continuous)
                                            .strokeBorder(YansuTheme.border, lineWidth: 1)
                                    }
                                    .scaleEffect(x: 0.92 + 0.08 * rise, y: 1, anchor: .bottom)
                                    .offset(y: 10 - 10 * rise)
                                    .accessibilityHidden(true)
                            }
                        }
                    .padding(.bottom, deck.next == nil ? 0 : 10)
                    .accessibilityElement(children: .contain)
                    .accessibilityIdentifier("tasks.offer")
            }
            if let startFailed {
                HStack {
                    Text(startFailed).foregroundStyle(passedHold == nil ? YansuTheme.warning : YansuTheme.secondary)
                    Spacer()
                    if let passed = passedHold {
                        Button(String(localized: "hold.otherTimes")) {
                            Task {
                                environment.holds.clear(id: passed.proposal.id)
                                if let fresh = await environment.holds.proposal(kind: passed.proposal.thing.sourceKind, id: passed.proposal.thing.sourceId) {
                                    passedHold = nil
                                    self.startFailed = nil
                                    holdSheet = fresh
                                }
                            }
                        }
                        .fontWeight(.semibold)
                        .foregroundStyle(YansuTheme.accent)
                        .accessibilityIdentifier("tasks.offer.otherTimes")
                    } else {
                        Button(String(localized: "Try again")) {
                            self.startFailed = nil
                            if let offer = deck.current { start(offer) }
                        }
                        .fontWeight(.semibold)
                        .foregroundStyle(YansuTheme.accent)
                    }
                }
                .font(.subheadline)
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("tasks.offer.startFailed")
            }
            if let findMoreLine { Text(findMoreLine).font(.footnote).foregroundStyle(YansuTheme.secondary) }
            if let notice = environment.ideas.notice {
                Text(notice).font(.footnote).foregroundStyle(YansuTheme.secondary)
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("tasks.upNext")
    }

    private func findMore() {
        findingMore = true
        findMoreLine = nil
        let previous = Set(deck.offers.map(\.id))
        Task {
            let result = await environment.ideas.findMore()
            withAnimation(deckAnimation) {
                deck.findMore(offers: environment.ideas.ideas)
                findingMore = false
                if let time = result?.nextAllowedAt, let date = ISO8601DateFormatter().date(from: time) {
                    findMoreLine = String.localizedStringWithFormat(String(localized: "More arrive after %@"), date.formatted(date: .omitted, time: .shortened))
                } else if result == nil {
                    findMoreLine = String(localized: "Couldn't find more tasks. Try again.")
                } else if Set(deck.offers.map(\.id)).subtracting(previous).isEmpty {
                    findMoreLine = String(localized: "Nothing new right now. Try again later.")
                }
            }
        }
    }

    private func offerCard(_ offer: IdeaOffer) -> some View {
        let drag = motion(for: dragOffset)
        return VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .center, spacing: 8) {
                if let kind = TaskThreadCopy.deckKind(offer.deckKind) {
                    Text(kind)
                        .font(.caption.weight(.semibold))
                        .tracking(0.8)
                        .textCase(.uppercase)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("tasks.offer.kind")
                }
                Spacer()
                Menu {
                    Button(String(localized: "Open in Chat")) { _ = open(offer: offer) }
                        .disabled(!canChat)
                    Button(String(localized: "Not for me"), role: .destructive) { notForMe(offer) }
                        .accessibilityIdentifier("tasks.offer.notForMe")
                } label: {
                    Image(systemName: "ellipsis")
                        .font(.headline)
                        .foregroundStyle(YansuTheme.secondary)
                        .frame(width: 32, height: 32)
                        .contentShape(Circle())
                }
                .accessibilityLabel(String(localized: "More"))
                .accessibilityIdentifier("tasks.offer.more")
            }
            Text(offer.displayTitle(locale: locale))
                .font(.title3.weight(.semibold))
                .foregroundStyle(YansuTheme.ink)
                .fixedSize(horizontal: false, vertical: true)
                .lineLimit(wide ? nil : 3)
            if let evidence = offer.evidence {
                Text(evidence.text(locale: locale))
                    .font(.body)
                    .foregroundStyle(YansuTheme.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("tasks.offer.evidence")
                let sourceLine = TaskThreadCopy.sourceLine(evidence, locale: locale)
                HStack(spacing: 7) {
                    Image(systemName: TaskThreadCopy.sourceIcon(evidence.source)).font(.footnote)
                    Text(sourceLine)
                }
                .font(.subheadline)
                .foregroundStyle(YansuTheme.secondary)
                .padding(.top, 2)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(sourceLine)
                .accessibilityIdentifier("tasks.offer.source")
            } else if !offer.why.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                // An older computer sends only the model's reason.
                Text(offer.explanation(locale: locale))
                    .font(.body)
                    .foregroundStyle(YansuTheme.secondary)
                    .lineLimit(wide ? nil : 3)
            }
            foldSafeControl(width: 236) {
                HStack(spacing: 10) {
                    Spacer(minLength: 0)
                    Button(String(localized: "upnext.notNow")) { notNow(offer) }
                        .buttonStyle(UpNextButtonStyle(prominent: false))
                        .disabled(!canDecide || environment.ideas.pending.contains(offer.id))
                        .accessibilityIdentifier("tasks.offer.notNow")
                    Button(String(localized: "Do it")) { start(offer) }
                        .buttonStyle(UpNextButtonStyle(prominent: true))
                        .disabled(!canStartWork || deckBusy)
                        .accessibilityIdentifier("tasks.offer.doIt")
                }
                .padding(.top, 6)
            }
        }
        .padding(.horizontal, 18)
        .padding(.top, 14)
        .padding(.bottom, 16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background {
            RoundedRectangle(cornerRadius: 22, style: .continuous)
                .fill(YansuTheme.surface)
            RoundedRectangle(cornerRadius: 22, style: .continuous)
                .fill(YansuTheme.accentWash.opacity(drag.doItTint))
        }
        .overlay { RoundedRectangle(cornerRadius: 22, style: .continuous).strokeBorder(YansuTheme.border, lineWidth: 1) }
        .overlay(alignment: .topLeading) {
            // The word fills as you near the 35% line and turns solid at it.
            UpNextDragPill(title: String(localized: "Do it"), progress: drag.doItTint, prominent: true)
                .padding(14)
                .opacity(reduceMotion ? 0 : min(1, drag.doItTint * 2))
                .allowsHitTesting(false)
        }
        .overlay(alignment: .topTrailing) {
            UpNextDragPill(title: String(localized: "upnext.notNow"), progress: drag.notNowTint, prominent: false)
                .padding(14)
                .opacity(reduceMotion ? 0 : min(1, drag.notNowTint * 2))
                .allowsHitTesting(false)
        }
        .offset(x: reduceMotion ? 0 : dragOffset)
        .opacity(reduceMotion && deckBusy ? 0.25 : 1)
        .animation(reduceMotion ? .easeOut(duration: 0.2) : nil, value: deckBusy)
        .rotationEffect(.degrees(reduceMotion ? 0 : drag.rotationDegrees))
        .background {
            GeometryReader { geometry in
                Color.clear.onAppear { deckCardWidth = geometry.size.width }
                    .onChange(of: geometry.size.width) { _, width in deckCardWidth = width }
            }
        }
        .contextMenu {
            Button(String(localized: "Do it")) { start(offer) }
            Button(String(localized: "Open in Chat")) { _ = open(offer: offer) }
            Button(String(localized: "upnext.notNow")) { notNow(offer) }
            Button(String(localized: "Not for me"), role: .destructive) { notForMe(offer) }
        }
        .gesture(
            DragGesture(minimumDistance: 20)
                .onChanged { value in
                    guard !deckBusy else { return }
                    let x = value.translation.width
                    if TaskDeckMotion.crossedThreshold(from: Double(lastDragX), to: Double(x),
                                                      width: Double(deckCardWidth)) {
                        thresholdCrossings += 1
                    }
                    lastDragX = x
                    dragOffset = x
                }
                .onEnded { value in
                    guard !deckBusy else { return }
                    let current = offer
                    let result = deck.swipe(translation: Double(value.translation.width),
                                            width: Double(deckCardWidth))
                    lastDragX = 0
                    switch result {
                    case .start:
                        start(current)
                    case .notNow:
                        notNow(current)
                    case .none:
                        withAnimation(reduceMotion ? .easeOut(duration: 0.25) : .spring(duration: 0.45)) {
                            dragOffset = 0
                        }
                    }
                }
        )
        .task(id: offer.id) {
            guard !wide, TaskDeck.claimFirstLean(reduceMotion: reduceMotion) else { return }
            do { try await Task.sleep(for: .milliseconds(200)) } catch { return }
            withAnimation(.easeOut(duration: 0.25)) { dragOffset = deckCardWidth * 0.12 }
            do { try await Task.sleep(for: .milliseconds(450)) } catch { return }
            if !deckBusy { withAnimation(deckAnimation) { dragOffset = 0 } }
        }
    }

    private func refresh(caught: Bool = false) async {
        isRefreshing = true
        defer {
            isRefreshing = false
            if caught { refreshHaptics += 1 }
        }
        await tasks.refresh()
        await environment.ideas.refresh()
        if deck.current == nil { deck.replace(environment.ideas.ideas) }
    }

    static let startHoldPrefix = "start."

    /// Do it (click rule v1.1.0): the card moves into Running at once with "Started · Undo"; after five seconds the
    /// work is sent to the computer. Undo puts the card back in Up next, untouched. If it can't start, the card comes
    /// back with "Couldn't start it." and Try again.
    private func start(_ offer: IdeaOffer) {
        if let hold = offer.hold {
            startCalendarHold(offer, hold: hold)
            return
        }
        guard !deckBusy, canStartWork else { return }
        deckBusy = true
        startFailed = nil
        passedHold = nil
        deck.prepareStart(offer)
        withAnimation(reduceMotion ? .easeOut(duration: 0.25) : .spring(duration: 0.45)) {
            dragOffset = reduceMotion ? 0 : deckCardWidth * 1.2
        }
        Task {
            try? await Task.sleep(for: .milliseconds(reduceMotion ? 150 : 350))
            withAnimation(reduceMotion ? .easeOut(duration: 0.2) : .spring(duration: 0.45)) {
                _ = deck.start()
                dragOffset = 0
                startingOffers.insert(offer, at: 0)
            }
            deckBusy = false
            actionHaptics += 1
            undoHolds.begin(.init(id: Self.startHoldPrefix + offer.id, label: String(localized: "Started"),
                                  detail: offer.title)) {
                await commitStart(offer)
            }
        }
    }

    private func startCalendarHold(_ offer: IdeaOffer, hold: PhoneHoldOffer) {
        guard !deckBusy, canStartWork, let slot = hold.proposal.slots.first else { return }
        deckBusy = true
        startFailed = nil
        deck.prepareStart(offer)
        withAnimation(deckAnimation) {
            _ = deck.start()
            startingOffers.insert(offer, at: 0)
            dragOffset = 0
        }
        Task {
            let status = await environment.holds.place(hold, slot: slot, calendarID: hold.proposal.calendarId)
            deckBusy = false
            guard let status, ["pending_write", "held", HoldsStore.queuedState].contains(status.state) else {
                startingOffers.removeAll { $0.id == offer.id }
                deck.cancelStart()
                deck.restore(offer)
                if status?.state == HoldsStore.expiredState {
                    passedHold = hold
                    startFailed = String(localized: "hold.passed")
                } else if ["conflict", "calendar_unavailable"].contains(status?.state ?? "") {
                    holdSheet = hold
                } else {
                    startFailed = PhoneHoldCopy.failure(environment.holds.failures[hold.proposal.id] ?? .hold(1))
                }
                return
            }
            actionHaptics += 1
        }
    }

    private func commitStart(_ offer: IdeaOffer) async {
        var draft = OneTimeWorkDraft()
        draft.title = offer.title
        draft.brief = offer.prompt
        if tasks.page?.threads == true {
            draft.origin = StartOneTimeWorkOrigin(kind: "up_next")
        }
        let started = await environment.actions.runOneTimeWork(draft, offerID: offer.id)
        if started { await tasks.refresh() }
        withAnimation(deckAnimation) {
            startingOffers.removeAll { $0.id == offer.id }
            if !started {
                deck.cancelStart()
                deck.restore(offer)
                startFailed = String(localized: "Couldn't start it.")
            }
        }
    }

    private func undoStart(_ offer: IdeaOffer) {
        guard undoHolds.undo(Self.startHoldPrefix + offer.id) else { return }
        notNowHaptics += 1
        withAnimation(deckAnimation) {
            startingOffers.removeAll { $0.id == offer.id }
            deck.cancelStart()
            deck.restore(offer)
        }
    }

    private func open(offer: IdeaOffer) -> Bool {
        guard IdeaHandoff.open(offer, chat: chat, router: router) else {
            environment.ideas.notice = IdeasCopy.handoffFailed(locale: locale)
            return false
        }
        if wide, navigation?.path(for: .chat).last != .draft("main") {
            navigation?.push(.draft("main"), on: .chat)
        }
        return true
    }

    private func openChatDraft() {
        if wide, navigation?.path(for: .chat).last != .draft("main") {
            navigation?.push(.draft("main"), on: .chat)
        }
        router.openMainChat()
    }

    @ViewBuilder
    private func foldSafeControl<Content: View>(width: CGFloat,
                                                 @ViewBuilder content: () -> Content) -> some View {
        if wide {
            DuoFoldSafeControl(foldX: foldX, windowWidth: windowWidth,
                               width: width, height: 44, content: content)
        } else {
            content()
        }
    }

    private func notNow(_ offer: IdeaOffer) {
        guard !deckBusy else { return }
        deckBusy = true
        notNowHaptics += 1
        Task {
            defer { deckBusy = false }
            guard let approval = await environment.ideas.authorizeDecision(offer, decision: "not_now") else {
                withAnimation(deckAnimation) { dragOffset = 0 }
                return
            }
            withAnimation(deckAnimation) { dragOffset = reduceMotion ? 0 : -deckCardWidth * 1.2 }
            try? await Task.sleep(for: .milliseconds(450))
            withAnimation(deckAnimation) {
                guard deck.notNow() != nil else { return }
                dragOffset = 0
                heldOffers[offer.id] = offer
                undoHolds.begin(.init(id: offer.id, label: String(localized: "upnext.notNow"),
                                      detail: offer.title)) {
                    let committed = await environment.ideas.commitDecision(approval)
                    if committed {
                        deck.commitHold(offer)
                    } else if let restored = heldOffers[offer.id] {
                        withAnimation(deckAnimation) { deck.restore(restored) }
                    }
                    heldOffers.removeValue(forKey: offer.id)
                }
            }
        }
    }

    private func notForMe(_ offer: IdeaOffer) {
        Task {
            guard let approval = await environment.ideas.authorizeDecision(offer, decision: "dismiss") else { return }
            withAnimation(deckAnimation) {
                guard deck.dismiss() != nil else { return }
                heldOffers[offer.id] = offer
                undoHolds.begin(.init(id: offer.id, label: String(localized: "Sent to your computer"),
                                      detail: offer.title)) {
                    let committed = await environment.ideas.commitDecision(approval)
                    if committed {
                        deck.commitHold(offer)
                    } else if let restored = heldOffers[offer.id] {
                        withAnimation(deckAnimation) { deck.restore(restored) }
                    }
                    heldOffers.removeValue(forKey: offer.id)
                }
            }
        }
    }

    private func undo(_ id: String) {
        guard undoHolds.undo(id), let offer = heldOffers.removeValue(forKey: id) else { return }
        withAnimation(deckAnimation) { deck.restore(offer) }
    }

    /// A thread zooms out of its row (iOS 18 and later; a plain push before, or with Reduce Motion).
    @ViewBuilder
    private func threadPage(_ task: MobileTask) -> some View {
        if #available(iOS 18.0, *), !reduceMotion {
            TaskThreadView(task: task, tasks: tasks)
                .navigationTransition(.zoom(sourceID: task.id, in: threadNamespace))
        } else {
            TaskThreadView(task: task, tasks: tasks)
        }
    }

    @ViewBuilder
    private func zoomSource<Row: View>(_ row: Row, _ task: MobileTask) -> some View {
        if #available(iOS 18.0, *) {
            row.matchedTransitionSource(id: task.id, in: threadNamespace)
        } else {
            row
        }
    }

    @ViewBuilder
    private func completionRow(_ task: MobileTask) -> some View {
        if reduceMotion {
            zoomSource(TaskRow(task: task, grouped: true), task).transition(.opacity)
        } else {
            zoomSource(TaskRow(task: task, grouped: true), task)
                .matchedGeometryEffect(id: task.id, in: completionNamespace, isSource: task.group == "running")
                .transition(.move(edge: task.group == "done" ? .top : .bottom).combined(with: .opacity))
        }
    }

    private func motion(for translation: CGFloat) -> TaskDeckMotion {
        TaskDeckMotion.presentation(translation: Double(translation), width: Double(deckCardWidth))
    }
}

struct TaskRow: View {
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.locale) private var locale
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    let task: MobileTask
    var onSelect: (() -> Void)?
    var directTasks: TasksStore?
    var source: TaskThreadSource = .tasks
    /// A row inside a grouped card (Done this week) draws no card of its own.
    var grouped = false
    /// A trailing control in place of the chevron, such as Undo on a task that just started.
    var trailing: AnyView?
    var lineOverride: String?

    init(task: MobileTask, onSelect: (() -> Void)? = nil, compact: Bool = false, directTasks: TasksStore? = nil,
         source: TaskThreadSource = .tasks, grouped: Bool = false, trailing: AnyView? = nil,
         lineOverride: String? = nil) {
        self.task = task
        self.onSelect = onSelect
        self.compact = compact
        self.directTasks = directTasks
        self.source = source
        self.grouped = grouped
        self.trailing = trailing
        self.lineOverride = lineOverride
    }

    private var compact: Bool

    @ViewBuilder
    var body: some View {
        if trailing != nil {
            rowContent
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("tasks.row.\(task.id)")
        } else if let onSelect {
            Button(action: onSelect) {
                rowContent
            }
            .buttonStyle(.plain)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("tasks.row.\(task.id)")
        } else if let directTasks {
            NavigationLink {
                TaskThreadView(task: task, tasks: directTasks, source: source)
            } label: { rowContent }
            .buttonStyle(.plain)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("tasks.row.\(task.id)")
        } else {
            NavigationLink(value: task) {
                rowContent
            }
            .buttonStyle(.plain)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("tasks.row.\(task.id)")
        }
    }

    @ViewBuilder
    private var mark: some View {
        switch task.group {
        case "running":
            TaskProgressRing(progress: task.progress, reduceMotion: reduceMotion)
                .frame(width: 22, height: 22)
        case "done":
            TaskCompletionMark(reduceMotion: reduceMotion, byYou: task.outcome?.byYou ?? true,
                               filled: task.outcome != nil)
                .frame(width: 24, height: 24)
        case "scheduled":
            Image(systemName: "clock")
                .font(.title3)
                .foregroundStyle(YansuTheme.secondary)
        default:
            EmptyView()
        }
    }

    private var rowContent: some View {
        return HStack(alignment: .center, spacing: 14) {
            if task.group != "needs_you" {
                mark.frame(width: 26)
            }
            if trailing != nil, let onSelect {
                Button(action: onSelect) { rowText }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("tasks.row.open.\(task.id)")
            } else {
                rowText
            }
            if let trailing {
                trailing
            } else {
                Image(systemName: "chevron.right")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(YansuTheme.tertiary)
                    .accessibilityHidden(true)
            }
        }
        .padding(.horizontal, compact ? 12 : 16)
        .padding(.vertical, compact ? 10 : 14)
        .background {
            if !grouped {
                RoundedRectangle(cornerRadius: 18, style: .continuous).fill(YansuTheme.surface)
            }
        }
        .overlay {
            if !grouped {
                RoundedRectangle(cornerRadius: 18, style: .continuous).stroke(YansuTheme.border, lineWidth: 1)
            }
        }
        .contentShape(Rectangle())
    }

    private var rowText: some View {
        let line = TaskThreadCopy.rowLine(task, locale: locale)
        return VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    // The unread dot leads the title at every size, so a wrapped title never pushes it away.
                    if task.unread == true {
                        Circle().fill(YansuTheme.accent).frame(width: 8, height: 8)
                            .accessibilityLabel(String(localized: "New"))
                            .accessibilityIdentifier("tasks.row.unread.\(task.id)")
                    }
                    Text(task.title)
                        .font(compact ? .subheadline.weight(.semibold) : .body.weight(.semibold))
                        .foregroundStyle(YansuTheme.ink)
                        .lineLimit(dynamicTypeSize.isAccessibilitySize ? nil : (compact ? 1 : 2))
                        .fixedSize(horizontal: false, vertical: true)
                }
                if !compact {
                    Group {
                        if let lineOverride {
                            Text(lineOverride).foregroundStyle(YansuTheme.accent).fontWeight(.medium)
                        } else if let lead = line.lead {
                            Text(lead).foregroundStyle(YansuTheme.warning).fontWeight(.semibold)
                                + Text(line.rest).foregroundStyle(YansuTheme.secondary)
                        } else {
                            Text(line.rest).foregroundStyle(YansuTheme.secondary)
                        }
                    }
                    .font(.subheadline)
                    .fixedSize(horizontal: false, vertical: true)
                    .contentTransition(.numericText())
                }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct TaskProgressRing: View {
    let progress: MobileTaskProgress?
    let reduceMotion: Bool

    var body: some View {
        TimelineView(.animation(minimumInterval: 1 / 20, paused: progress != nil || reduceMotion)) { timeline in
            let rotation = reduceMotion ? 0 : timeline.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 4) * 90
            ZStack {
                Circle()
                    .stroke(YansuTheme.accent.opacity(0.14), lineWidth: 3)
                Circle()
                    .trim(from: 0, to: progress.map { $0.total > 0 ? min(1, max(0, Double($0.done) / Double($0.total))) : 0 } ?? 0.28)
                    .stroke(YansuTheme.accent, style: StrokeStyle(lineWidth: 3, lineCap: .round))
                    .rotationEffect(.degrees(-90 + rotation))
            }
        }
        .animation(.easeInOut(duration: 0.6), value: progress?.done)
        .accessibilityLabel(progress.map {
            String.localizedStringWithFormat(String(localized: "%d of %d"), $0.done, $0.total)
        } ?? String(localized: "In progress"))
    }
}

private struct TaskCompletionMark: View {
    let reduceMotion: Bool
    var byYou = true
    var filled = false
    @State private var drawn = false

    var body: some View {
        Group {
            if filled {
                Image(systemName: "checkmark.circle.fill")
                    .font(.title2)
                    .foregroundStyle(YansuTheme.onSolid, byYou ? YansuTheme.secondary : YansuTheme.solid)
                    .scaleEffect(drawn || reduceMotion ? 1 : 0.6)
            } else if #available(iOS 26.0, *), !reduceMotion {
                Image(systemName: "checkmark.circle")
                    .symbolEffect(.drawOn, isActive: !drawn)
                    .foregroundStyle(YansuTheme.secondary)
            } else {
                ZStack {
                    Circle().stroke(lineWidth: 1.5)
                    Path { path in
                        path.move(to: CGPoint(x: 5, y: 11))
                        path.addLine(to: CGPoint(x: 9, y: 15))
                        path.addLine(to: CGPoint(x: 17, y: 7))
                    }
                    .trim(from: 0, to: drawn || reduceMotion ? 1 : 0)
                    .stroke(style: StrokeStyle(lineWidth: 1.5, lineCap: .round, lineJoin: .round))
                }
                .foregroundStyle(YansuTheme.secondary)
            }
        }
        .onAppear { withAnimation(reduceMotion ? nil : .spring(duration: 0.45)) { drawn = true } }
    }
}

/// Not now and Do it on an Up next card: capsules at the card's foot, Do it solid.
private struct UpNextButtonStyle: ButtonStyle {
    let prominent: Bool
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .padding(.horizontal, 20)
            .frame(minHeight: 44)
            .foregroundStyle(prominent ? YansuTheme.onSolid : YansuTheme.ink)
            .background(prominent ? YansuTheme.solid : YansuTheme.chip, in: Capsule())
            .opacity(!isEnabled ? 0.45 : configuration.isPressed ? 0.75 : 1)
    }
}

/// The word over a dragged card: Do it in violet on the left, Not now in gray on the right.
private struct UpNextDragPill: View {
    let title: String
    let progress: Double
    let prominent: Bool

    var body: some View {
        let solid = progress >= 1
        let tint = prominent ? YansuTheme.solid : YansuTheme.secondary
        Text(title)
            .font(.subheadline.weight(.bold))
            .foregroundStyle(solid ? (prominent ? YansuTheme.onSolid : YansuTheme.surface) : tint)
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .background(Capsule().fill(tint.opacity(solid ? 1 : 0.12 + 0.3 * progress)))
            .scaleEffect(solid ? 1.06 : 0.9 + 0.1 * progress)
            .animation(.spring(duration: 0.25), value: solid)
    }
}
