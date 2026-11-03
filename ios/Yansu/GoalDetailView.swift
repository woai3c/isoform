import SwiftUI

/// One goal's page, pushed from the Goals tab (PROTOCOL.md section 20.5, GET /goals/{id}): its status, the aim when
/// it says more than the title, what happened to it, and the moves the list's menu has, plus a hand-off to the main
/// chat. Moves go through the same GoalStore as the list, so both show the same state.
struct GoalDetailView: View {
    let goalID: String
    @ObservedObject var goals: GoalStore
    let canChange: Bool
    @EnvironmentObject private var chat: ChatStore
    @EnvironmentObject private var router: NotificationRouter
    @Environment(\.dismiss) private var dismiss
    @State private var detail: GoalDetail?
    @State private var failed = false
    @State private var waitingFor = false
    @State private var waitingOn = ""

    /// The list's copy wins over the read: it carries this phone's unanswered moves.
    private var goal: GoalView? { goals.goals.first(where: { $0.id == goalID }) ?? detail?.goal }
    private var busy: Bool { goals.busy.contains(goalID) || goals.pending[goalID] != nil }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                if let goal {
                    content(goal)
                } else if failed {
                    Text("Isoform could not read this goal from your computer. Try again.")
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("goal.detail.failed")
                } else {
                    ProgressView("Checking with your computer.")
                        .frame(maxWidth: .infinity, alignment: .center)
                        .padding(.vertical, 24)
                }
            }
            .padding(.horizontal, YansuTheme.gutter)
            .padding(.top, 8)
            .padding(.bottom, 32)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .refreshable { await load() }
        .task { await load() }
        .yansuScreen()
        .navigationBarTitleDisplayMode(.inline)
        .alert("Waiting on", isPresented: $waitingFor) {
            TextField("Who or what", text: $waitingOn)
            Button("Cancel", role: .cancel) {}
            Button("Save") {
                guard let goal else { return }
                let on = waitingOn
                Task { await goals.markWaiting(goal, on: on) }
            }
        } message: {
            Text("Isoform shows this goal as waiting until you check it off.")
        }
        .accessibilityIdentifier("goal.detail")
    }

    private func load() async {
        do {
            detail = try await goals.detail(goalID)
            failed = false
        } catch {
            failed = true
        }
    }

    @ViewBuilder
    private func content(_ goal: GoalView) -> some View {
        let title = goal.title ?? String(localized: "Untitled")
        Text(title)
            .font(.title2.weight(.semibold))
            .foregroundStyle(YansuTheme.ink)
            .fixedSize(horizontal: false, vertical: true)
            .accessibilityAddTraits(.isHeader)
        if let status = goal.status, !status.isEmpty {
            Text(status)
                .font(.subheadline)
                .foregroundStyle(goal.slipping ? YansuTheme.warning : YansuTheme.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 6)
        }
        if let outcome = detail?.outcome, !outcome.isEmpty {
            Text("The aim: \(outcome)")
                .font(.body)
                .foregroundStyle(YansuTheme.ink)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 16)
        }
        if let history = detail?.history, !history.isEmpty {
            Text("What happened")
                .font(.headline)
                .foregroundStyle(YansuTheme.secondary)
                .accessibilityAddTraits(.isHeader)
                .padding(.top, 28)
            VStack(alignment: .leading, spacing: 10) {
                ForEach(Array(history.enumerated()), id: \.offset) { _, entry in
                    HStack(alignment: .firstTextBaseline, spacing: 12) {
                        Text(entry.what)
                            .font(.subheadline)
                            .foregroundStyle(YansuTheme.ink)
                        Spacer(minLength: 8)
                        Text(Self.day(entry.at))
                            .font(.subheadline)
                            .foregroundStyle(YansuTheme.tertiary)
                    }
                    .accessibilityElement(children: .combine)
                }
            }
            .padding(.top, 10)
            .accessibilityIdentifier("goal.detail.history")
        }
        actions(goal, title: title)
            .padding(.top, 28)
    }

    private func actions(_ goal: GoalView, title: String) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            if !goal.isForgotten {
                Button(goal.isDone ? LocalizedStringKey("Reopen") : LocalizedStringKey("Mark done")) {
                    Task { await goals.toggle(goal) }
                }
                .buttonStyle(YansuPrimaryButtonStyle())
                .disabled(!canChange || busy)
                .accessibilityIdentifier("goal.detail.toggle")
                if !goal.isDone {
                    HStack(spacing: 20) {
                        if goal.state != "waiting" {
                            Button("Waiting on someone") {
                                waitingOn = ""
                                waitingFor = true
                            }
                            .disabled(!canChange || busy)
                        }
                        Button("Forget this goal", role: .destructive) {
                            Task {
                                if await goals.forget(goal) { dismiss() }
                            }
                        }
                        .disabled(!canChange || busy)
                        .accessibilityIdentifier("goal.detail.forget")
                    }
                    .font(.subheadline.weight(.medium))
                }
            }
            Button("Talk about this goal") {
                chat.prefillMainChat(String(localized: "Let's talk about my goal: \(title)"))
                router.openMainChat()
            }
            .font(.subheadline.weight(.medium))
            .foregroundStyle(YansuTheme.secondary)
            .accessibilityIdentifier("goal.detail.talk")
        }
    }

    /// A history entry's day, from the Mac's RFC 3339 time.
    static func day(_ at: String) -> String {
        let parser = ISO8601DateFormatter()
        parser.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let date = parser.date(from: at) ?? {
            parser.formatOptions = [.withInternetDateTime]
            return parser.date(from: at)
        }()
        return date?.formatted(date: .abbreviated, time: .omitted) ?? ""
    }
}
