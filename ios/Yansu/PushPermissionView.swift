import SwiftUI

// Shown after the first brief. Its list comes from the same computer preferences as Settings.
struct PushPermissionView: View {
    @EnvironmentObject private var push: PushRegistration
    @EnvironmentObject private var notifications: NotificationSettingsStore
    @EnvironmentObject private var keepAwake: KeepAwakeStore
    @State private var working = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                VStack(alignment: .leading, spacing: 8) {
                    Text("notifications.permission.title")
                        .font(.title2.weight(.semibold))
                        .foregroundStyle(YansuTheme.ink)
                        .accessibilityAddTraits(.isHeader)
                    Text("notifications.permission.detail")
                        .font(.body)
                        .foregroundStyle(YansuTheme.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                    if !keepAwake.instantPush {
                        Text("Your phone checks your computer every so often, so a notice can come a little late.")
                            .font(.body)
                            .foregroundStyle(YansuTheme.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                VStack(alignment: .leading, spacing: 10) {
                    SectionLabel(title: String(localized: "notifications.permission.enabled"))
                    YansuCard {
                        VStack(alignment: .leading, spacing: 12) {
                            if let settings = notifications.settings, settings.available {
                                ForEach(settings.enabledPhoneKinds) { kind in
                                    Text(kind.phoneTitle)
                                        .font(.body)
                                        .foregroundStyle(YansuTheme.ink)
                                }
                                if settings.enabledPhoneKinds.isEmpty {
                                    Text("notifications.permission.none")
                                        .foregroundStyle(YansuTheme.secondary)
                                }
                            } else if notifications.loadState == .loading || notifications.loadState == .idle {
                                Text("notifications.loading")
                                    .foregroundStyle(YansuTheme.secondary)
                            } else {
                                Text("notifications.permission.unavailable")
                                    .foregroundStyle(YansuTheme.secondary)
                            }
                        }
                    }
                }
                Text("notifications.permission.settings")
                    .font(.footnote)
                    .foregroundStyle(YansuTheme.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                VStack(spacing: 10) {
                    Button("notifications.permission.accept") {
                        working = true
                        Task {
                            await push.acceptPermissionExplanation()
                            working = false
                        }
                    }
                    .buttonStyle(YansuPrimaryButtonStyle())
                    .disabled(working)
                    .accessibilityIdentifier("push.permission.accept")
                    Button("notifications.permission.decline") {
                        push.declinePermissionExplanation()
                    }
                    .buttonStyle(YansuSecondaryButtonStyle())
                    .accessibilityIdentifier("push.permission.decline")
                }
            }
            .padding(YansuTheme.gutter)
        }
        .background(YansuTheme.background)
        .presentationDragIndicator(.visible)
        .task { await notifications.refresh() }
        .onDisappear { push.declinePermissionExplanation() }
    }
}
