import Foundation
import Security

// Removes what the LAN and agent-service builds left on this iPhone. Runs once per install,
// guarded by the yansu.purge.v2 flag, so later launches never touch current data.
enum LegacyPurge {
    static let flagKey = "yansu.purge.v2" // gitleaks:allow
    static let legacyFileNames = ["companion.json", "agent-service.json", "device-actions.json"]
    static let legacyDefaultsKeys = ["yansu.chat.compose-drafts"]
    static let legacyKeychainPrefixes = ["provider-", "hub-", "agent-service-"]
    static let keychainService = "com.yansu.companion"

    struct Targets {
        var supportDirectory: URL
        var defaults: UserDefaults
        var keychainAccounts: () -> [String]
        var deleteKeychainAccount: (String) -> Void
    }

    // Returns true when the purge ran on this launch.
    @discardableResult
    static func runIfNeeded(_ targets: Targets) -> Bool {
        guard !targets.defaults.bool(forKey: flagKey) else { return false }
        for name in legacyFileNames {
            try? FileManager.default.removeItem(at: targets.supportDirectory.appendingPathComponent(name))
        }
        for key in legacyDefaultsKeys {
            targets.defaults.removeObject(forKey: key)
        }
        for account in targets.keychainAccounts() where legacyKeychainPrefixes.contains(where: { account.hasPrefix($0) }) {
            targets.deleteKeychainAccount(account)
        }
        targets.defaults.set(true, forKey: flagKey)
        return true
    }

    static var appTargets: Targets {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Isoform", isDirectory: true)
        return Targets(supportDirectory: support, defaults: .standard,
                       keychainAccounts: keychainAccounts, deleteKeychainAccount: deleteKeychainAccount)
    }

    static func keychainAccounts() -> [String] {
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService, kSecReturnAttributes as String: true,
            kSecMatchLimit as String: kSecMatchLimitAll]
        var result: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &result) == errSecSuccess,
              let items = result as? [[String: Any]] else { return [] }
        return items.compactMap { $0[kSecAttrAccount as String] as? String }
    }

    static func deleteKeychainAccount(_ account: String) {
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService, kSecAttrAccount as String: account]
        SecItemDelete(query as CFDictionary)
    }
}
