import SwiftUI

struct FirstRunView: View {
    @ObservedObject var firstRun: FirstRunStore
    @EnvironmentObject private var router: NotificationRouter

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                YansuMark(size: 64)
                Text("Isoform works on your computer and answers to you here.")
                    .font(.largeTitle.weight(.semibold))
                    .foregroundStyle(YansuTheme.ink)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("welcome.title")
                Text("Keep up with your day, talk to Isoform, and choose what happens next from this phone.")
                    .font(.body)
                    .foregroundStyle(YansuTheme.secondary)
                YansuCard {
                    VStack(alignment: .leading, spacing: 20) {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("1. Get Isoform on your computer")
                                .font(.headline)
                            Link("Get the computer app at isoform.ai", destination: FirstRunStore.computerAppURL)
                                .accessibilityIdentifier("welcome.download")
                        }
                        Text("2. Open Settings > Phone on your computer and show a pairing code")
                            .font(.headline)
                        Text("3. Scan the code with this phone")
                            .font(.headline)
                    }
                    .fixedSize(horizontal: false, vertical: true)
                }
                VStack(spacing: 12) {
                    Button("Pair with your computer") { firstRun.pair(using: router, scanCode: false) }
                        .buttonStyle(YansuPrimaryButtonStyle())
                        .accessibilityIdentifier("welcome.pair")
                    Button("Scan a code I already have") { firstRun.pair(using: router, scanCode: true) }
                        .buttonStyle(YansuSecondaryButtonStyle())
                        .accessibilityIdentifier("welcome.scan")
                    Button("Not now") { firstRun.complete() }
                        .frame(minHeight: 44)
                        .accessibilityIdentifier("welcome.skip")
                }
            }
            .padding(.horizontal, YansuTheme.gutter)
            .padding(.vertical, 24)
        }
        .yansuScreen()
    }
}
