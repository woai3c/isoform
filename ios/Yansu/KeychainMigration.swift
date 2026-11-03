import Foundation

// Moves Isoform's Keychain items from the app's default access group into the shared group, so the
// notification service extension can read them. Runs once per install, before anything reads a secret.
// An item already in the shared group keeps its value. A legacy copy is removed only after the shared
// copy is written, so a failure leaves the pairing readable on the next launch.
enum KeychainMigration {
    static let flagKey = "yansu.keychain.shared.v1"

    // Returns true when the migration ran to the end on this launch.
    @discardableResult
    static func runIfNeeded(backend: KeychainBackend = SystemKeychainBackend(),
                            service: String = KeychainSecretStore.service,
                            sharedGroup: String? = SharedKeychain.accessGroup,
                            defaults: UserDefaults = .standard) -> Bool {
        guard !defaults.bool(forKey: flagKey), let sharedGroup else { return false }
        guard let entries = try? backend.entries(service: service) else { return false }
        var complete = true
        for entry in entries where entry.accessGroup != sharedGroup {
            do {
                if try backend.value(service: service, account: entry.account, accessGroup: sharedGroup) == nil {
                    try backend.write(service: service, account: entry.account, accessGroup: sharedGroup,
                                      value: entry.value, accessible: KeychainAccessibility.forAccount(entry.account))
                }
                try backend.remove(service: service, account: entry.account, accessGroup: entry.accessGroup)
            } catch {
                complete = false
            }
        }
        if complete {
            defaults.set(true, forKey: flagKey)
        }
        return complete
    }
}
