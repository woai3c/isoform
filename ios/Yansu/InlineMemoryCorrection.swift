import SwiftUI

/// Keeps a correction beside the learned item until it is sent or cancelled.
struct InlineMemoryCorrection: View {
    let cancel: () -> Void
    let submit: (String) async -> Bool
    @EnvironmentObject private var router: NotificationRouter
    @State private var reason: String
    @State private var sending = false
    @State private var failed: Bool
    @FocusState private var focused: Bool

    init(initialReason: String = "", initiallyFailed: Bool = false, cancel: @escaping () -> Void,
         submit: @escaping (String) async -> Bool) {
        self.cancel = cancel
        self.submit = submit
        _reason = State(initialValue: initialReason)
        _failed = State(initialValue: initiallyFailed)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("What is not right").font(.footnote.weight(.medium)).foregroundStyle(YansuTheme.secondary)
            TextField(String(localized: "What is not right"), text: $reason, axis: .vertical)
                .focused($focused)
                .padding(10)
                .background(YansuTheme.composer, in: RoundedRectangle(cornerRadius: 10))
                .overlay { RoundedRectangle(cornerRadius: 10).strokeBorder(YansuTheme.composerBorder) }
                .accessibilityIdentifier("brief.correction.field")
            if failed { Text("Didn't save. Try again.").font(.footnote).foregroundStyle(YansuTheme.secondary) }
            HStack(spacing: 8) {
                Button("Send to your computer") {
                    sending = true
                    failed = false
                    Task {
                        if await submit(reason) { cancel() } else { failed = true }
                        sending = false
                    }
                }
                .buttonStyle(YansuPrimaryButtonStyle())
                .disabled(sending || reason.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityIdentifier("brief.correction.send")
                Button("Cancel", action: cancel)
                    .buttonStyle(YansuSecondaryButtonStyle())
                    .disabled(sending)
                    .accessibilityIdentifier("brief.correction.cancel")
            }
        }
        .onAppear { router.isComposing = true; focused = true }
        .onDisappear { router.isComposing = false }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("brief.correction.inline")
    }
}
