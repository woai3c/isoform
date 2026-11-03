import SwiftUI

/// One goal: a round checkbox, the title on one line, the Mac's status sentence under it, and a menu with the other
/// moves (Mark done, Waiting on someone, Forget this goal). Tapping the title opens the goal's page. A slipping goal's
/// sentence is the page's one status color (warning).
struct GoalRow: View {
    let goal: GoalView
    /// The checkbox and the menu work: the Mac is reachable and the pairing allows writes.
    let canChange: Bool
    let busy: Bool
    /// The move shown here is saved on this iPhone and has not reached the Mac yet.
    let pending: Bool
    let toggle: () -> Void
    let markWaiting: () -> Void
    let forget: () -> Void
    /// Opens the goal's page.
    let open: () -> Void

    private var enabled: Bool { canChange && !busy && !pending }
    private var title: String { goal.title ?? String(localized: "Untitled") }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Button(action: toggle) {
                Image(systemName: goal.isDone ? "checkmark.circle.fill" : "circle")
                    .font(.title2)
                    .foregroundStyle(goal.isDone ? YansuTheme.secondary : YansuTheme.tertiary)
                    .frame(width: 32, height: 32)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(!enabled)
            .opacity(busy ? 0.5 : 1)
            // VoiceOver reads the control on its own, so its label names the goal.
            .accessibilityLabel(goal.isDone ? Text("Reopen, \(title)") : Text("Mark done, \(title)"))
            .accessibilityValue(goal.isDone ? Text(String(localized: "goal.row.doneValue", defaultValue: "Done"))
                                : Text(String(localized: "goal.row.notDoneValue", defaultValue: "Not done")))
            .accessibilityIdentifier("goals.check")
            // The title and status open the goal's own page. A tap, not a button, so the texts stay readable on
            // their own.
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(.body.weight(.semibold))
                    .foregroundStyle(goal.isDone ? YansuTheme.secondary : YansuTheme.ink)
                    .lineLimit(1)
                    .accessibilityHint("Opens this goal")
                if let status = goal.status, !status.isEmpty {
                    Text(status)
                        .font(.subheadline)
                        .foregroundStyle(goal.slipping ? YansuTheme.warning : YansuTheme.secondary)
                        .lineLimit(3)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("goals.status")
                }
                if pending {
                    Text("Not on your computer yet.")
                        .font(.footnote)
                        .foregroundStyle(YansuTheme.tertiary)
                        .accessibilityIdentifier("goals.pending")
                }
            }
            .padding(.top, 5)
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .onTapGesture(perform: open)
            Menu {
                if goal.isDone {
                    Button(action: toggle) {
                        Label("Reopen", systemImage: "arrow.uturn.backward")
                    }
                } else {
                    Button(action: toggle) {
                        Label("Mark done", systemImage: "checkmark.circle")
                    }
                    if goal.state != "waiting" {
                        Button(action: markWaiting) {
                            Label("Waiting on someone", systemImage: "hourglass")
                        }
                    }
                    Button(role: .destructive, action: forget) {
                        Label("Forget this goal", systemImage: "eye.slash")
                    }
                }
            } label: {
                Image(systemName: "ellipsis")
                    .rotationEffect(.degrees(90))
                    .font(.body.weight(.semibold))
                    .foregroundStyle(YansuTheme.tertiary)
                    .frame(width: 32, height: 32)
                    .contentShape(Rectangle())
            }
            .disabled(!enabled)
            .accessibilityLabel("More for \(title)")
            .accessibilityIdentifier("goals.more")
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("goals.row")
    }
}

/// Pushes one goal's page on the Goals tab.
struct GoalRoute: Hashable, Identifiable {
    let id: String
}
