import SwiftUI

/// "New one-time work" from the Chat tab: a title, what to do, and an optional folder on the Mac. Chat offers it only
/// when the pairing grants one-time work. The sheet closes once the computer has saved the work without starting it,
/// and hands the notice back to the list that opened it.
struct OneTimeWorkComposer: View {
    static let folderPlaceholder = "/Users/you/src/notes"

    let started: (String?) -> Void

    @EnvironmentObject private var actions: ActionStore
    @Environment(\.dismiss) private var dismiss
    @State private var draft = OneTimeWorkDraft()
    @State private var starting = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    if let notice = actions.notice {
                        NoticeBanner(message: notice, dismiss: actions.dismissNotice)
                    }
                    Text("Isoform does this once on your computer. Check Tasks for its status.")
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    field("Title", placeholder: "Draft the Q3 outline", text: $draft.title, lines: 1...2)
                    field("What to do", placeholder: "Summarize the notes from Sam's review", text: $draft.brief,
                          lines: 2...6)
                    field("Folder on your computer (optional)", placeholder: Self.folderPlaceholder, text: $draft.workDir,
                          lines: 1...2)
                    Button("Start one-time work") {
                        Task { await start() }
                    }
                    .buttonStyle(YansuPrimaryButtonStyle())
                    .disabled(!draft.canStart || starting)
                    .accessibilityIdentifier("oneTimeWork.start")
                }
                .padding(.horizontal, YansuTheme.gutter)
                .padding(.top, 8)
                .padding(.bottom, 32)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .yansuScreen()
            .navigationTitle("Give Isoform a task")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
            }
        }
        .presentationDetents([.large])
        .onAppear {
            actions.dismissNotice()
        }
    }

    private func field(_ title: String, placeholder: String, text: Binding<String>,
                       lines: ClosedRange<Int>) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(.footnote)
                .foregroundStyle(YansuTheme.tertiary)
            TextField(placeholder, text: text, axis: .vertical)
                .font(.body)
                .lineLimit(lines)
                .padding(12)
                .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: YansuTheme.cornerRadius, style: .continuous))
                .overlay {
                    RoundedRectangle(cornerRadius: YansuTheme.cornerRadius, style: .continuous)
                        .strokeBorder(YansuTheme.border, lineWidth: 1)
                }
                .accessibilityLabel(title)
        }
    }

    private func start() async {
        starting = true
        defer { starting = false }
        if await actions.startOneTimeWork(draft) {
            started(actions.notice)
            dismiss()
        }
    }
}
