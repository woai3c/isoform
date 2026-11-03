import SwiftUI

/// A short sheet that asks for one line of reason before a correction or a removal goes to the Mac. The
/// action runs when the person confirms; the sheet closes only when the action reports success.
struct ReasonSheet: View {
    let title: String
    let detail: String
    let prompt: String
    let confirmTitle: String
    let required: Bool
    /// A notice from the caller's own store (the Feed's), shown in the sheet so a failure is never hidden behind it.
    var notice: String? = nil
    var dismissNotice: (() -> Void)? = nil
    let action: (String) async -> Bool

    @EnvironmentObject private var actions: ActionStore
    @Environment(\.dismiss) private var dismiss
    @State private var reason = ""
    @State private var sending = false
    @FocusState private var fieldFocused: Bool

    private var trimmed: String {
        reason.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private var canConfirm: Bool {
        !sending && (!required || !trimmed.isEmpty)
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    if let notice {
                        NoticeBanner(message: notice, dismiss: dismissNotice)
                            .accessibilityIdentifier("reason.notice")
                    } else if let notice = actions.notice {
                        NoticeBanner(message: notice, dismiss: actions.dismissNotice)
                            .accessibilityIdentifier("reason.notice")
                    }
                    Text(detail)
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Reason")
                            .font(.footnote)
                            .foregroundStyle(YansuTheme.tertiary)
                        TextField(prompt, text: $reason, axis: .vertical)
                            .font(.body)
                            .lineLimit(2...6)
                            .padding(12)
                            .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: YansuTheme.cornerRadius, style: .continuous))
                            .overlay {
                                RoundedRectangle(cornerRadius: YansuTheme.cornerRadius, style: .continuous)
                                    .strokeBorder(YansuTheme.border, lineWidth: 1)
                            }
                            .focused($fieldFocused)
                            .accessibilityIdentifier("reason.field")
                    }
                    Button(confirmTitle) {
                        Task { await confirm() }
                    }
                    .buttonStyle(YansuPrimaryButtonStyle())
                    .disabled(!canConfirm)
                    .accessibilityIdentifier("reason.confirm")
                }
                .padding(.horizontal, YansuTheme.gutter)
                .padding(.top, 8)
                .padding(.bottom, 32)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .yansuScreen()
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
            }
        }
        .presentationDetents([.medium, .large])
        .onAppear {
            actions.dismissNotice()
            // The sheet is for one reason, so the field takes the keyboard at once.
            fieldFocused = true
        }
    }

    private func confirm() async {
        sending = true
        defer { sending = false }
        if await action(trimmed) {
            dismiss()
        }
    }
}
