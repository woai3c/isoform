import Foundation
import Security

enum YansuError: LocalizedError, Equatable {
    case message(String)

    var errorDescription: String? {
        switch self {
        case .message(let text): return text
        }
    }
}

// Secret storage is behind a protocol so pairing tests can run without the Keychain.
protocol SecretStore: Sendable {
    func accountNames() throws -> [String]
    func read(_ account: String) throws -> String?
    func save(_ value: String, account: String) throws
    func delete(_ account: String) throws
}

// One generic-password item as the Keychain reports it, with the access group it lives in.
struct KeychainEntry: Equatable, Sendable {
    let account: String
    let accessGroup: String?
    let value: String
}

// Raw Keychain operations. The migration in the app and its tests run against an in-memory copy of this.
protocol KeychainBackend: Sendable {
    /// Every item under the service that this process can see, in any access group, with its value.
    func entries(service: String) throws -> [KeychainEntry]
    func value(service: String, account: String, accessGroup: String?) throws -> String?
    func write(service: String, account: String, accessGroup: String?, value: String,
               accessible: CFString) throws
    func remove(service: String, account: String, accessGroup: String?) throws
}

// Which items the notification service extension needs while the phone is locked. The extension opens
// sealed notices and posts receipts on a locked phone, so those items unlock after the first unlock after
// a restart. The device private key stays readable only while the phone is unlocked.
enum KeychainAccessibility {
    static func forAccount(_ account: String) -> CFString {
        PairingSecretAccount.isDevicePrivateKey(account)
            ? kSecAttrAccessibleWhenUnlockedThisDeviceOnly
            : kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
    }
}

// The access group shared by the app and the notification service extension. The team prefix is expanded
// at build time into Info.plist (YansuKeychainAccessGroup), so the code never hard-codes a team id.
enum SharedKeychain {
    static let infoKey = "YansuKeychainAccessGroup"

    static var accessGroup: String? {
        guard let value = Bundle.main.object(forInfoDictionaryKey: infoKey) as? String else { return nil }
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty || trimmed.contains("$(") ? nil : trimmed
    }
}

struct SystemKeychainBackend: KeychainBackend {
    func entries(service: String) throws -> [KeychainEntry] {
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service, kSecReturnAttributes as String: true,
            kSecReturnData as String: true, kSecMatchLimit as String: kSecMatchLimitAll]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return [] }
        guard status == errSecSuccess, let items = result as? [[String: Any]] else {
            throw YansuError.message("Unlock your phone to use the saved pairing.")
        }
        return items.compactMap { item in
            guard let account = item[kSecAttrAccount as String] as? String,
                  let data = item[kSecValueData as String] as? Data,
                  let value = String(data: data, encoding: .utf8) else { return nil }
            return KeychainEntry(account: account, accessGroup: item[kSecAttrAccessGroup as String] as? String,
                                 value: value)
        }
    }

    func value(service: String, account: String, accessGroup: String?) throws -> String? {
        var query = baseQuery(service: service, account: account, accessGroup: accessGroup)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data,
              let value = String(data: data, encoding: .utf8) else {
            throw YansuError.message("Unlock your phone to use the saved pairing.")
        }
        return value
    }

    func write(service: String, account: String, accessGroup: String?, value: String,
               accessible: CFString) throws {
        let query = baseQuery(service: service, account: account, accessGroup: accessGroup)
        let attributes: [String: Any] = [kSecValueData as String: Data(value.utf8),
            kSecAttrAccessible as String: accessible]
        let update = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        if update == errSecItemNotFound {
            let status = SecItemAdd(query.merging(attributes) { _, new in new } as CFDictionary, nil)
            guard status == errSecSuccess else {
                throw YansuError.message("Could not save the pairing securely in the Keychain.")
            }
        } else if update != errSecSuccess {
            throw YansuError.message("Could not update the saved pairing.")
        }
    }

    func remove(service: String, account: String, accessGroup: String?) throws {
        let status = SecItemDelete(baseQuery(service: service, account: account, accessGroup: accessGroup) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw YansuError.message("Could not remove the saved pairing from the Keychain.")
        }
    }

    private func baseQuery(service: String, account: String, accessGroup: String?) -> [String: Any] {
        var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service, kSecAttrAccount as String: account]
        if let accessGroup {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        return query
    }
}

// Secrets for this app's pairing, kept in the shared access group so the notification service
// extension can read them. A nil access group falls back to the process default group.
struct KeychainSecretStore: SecretStore {
    static let service = "com.yansu.companion"

    let service: String
    let accessGroup: String?
    let backend: KeychainBackend

    init(service: String = KeychainSecretStore.service,
         accessGroup: String? = SharedKeychain.accessGroup,
         backend: KeychainBackend = SystemKeychainBackend()) {
        self.service = service
        self.accessGroup = accessGroup
        self.backend = backend
    }

    func read(_ account: String) throws -> String? {
        try backend.value(service: service, account: account, accessGroup: accessGroup)
    }

    func accountNames() throws -> [String] {
        try backend.entries(service: service)
            .filter { accessGroup == nil || $0.accessGroup == accessGroup }
            .map(\.account)
    }

    func save(_ value: String, account: String) throws {
        try backend.write(service: service, account: account, accessGroup: accessGroup, value: value,
                          accessible: KeychainAccessibility.forAccount(account))
    }

    func delete(_ account: String) throws {
        try backend.remove(service: service, account: account, accessGroup: accessGroup)
    }
}
