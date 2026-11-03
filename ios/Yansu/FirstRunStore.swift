import Foundation

@MainActor
final class FirstRunStore: ObservableObject {
    static let completionKey = "isoform.phone.welcome.completed"
    static let computerAppURL = URL(string: "https://isoform.ai")!

    @Published private(set) var completed: Bool
    private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        completed = defaults.bool(forKey: Self.completionKey)
    }

    func shouldPresent(hasPairing: Bool, needsPairingRecovery: Bool) -> Bool {
        !completed && !hasPairing && !needsPairingRecovery
    }

    func complete() {
        defaults.set(true, forKey: Self.completionKey)
        completed = true
    }

    func pair(using router: NotificationRouter, scanCode: Bool) {
        complete()
        router.openPairing(scanCode: scanCode)
    }
}
