import Foundation

/// The notification controls as the phone sees them. HubChannel conforms; tests substitute a stub.
protocol NotificationSettingsAPI: Sendable {
    func notificationSettings() async throws -> NotificationSettings
    func setNotificationKind(_ kind: String, enabled: Bool, idempotencyKey: String) async throws -> NotificationSettings
    func setNotificationPreferences(_ request: SetNotificationPreferencesRequest) async throws -> NotificationSettings
}

extension HubChannel: NotificationSettingsAPI {
    func notificationSettings() async throws -> NotificationSettings {
        try await perform(.notifications).payload(NotificationSettings.self)
    }

    func setNotificationKind(_ kind: String, enabled: Bool, idempotencyKey: String) async throws -> NotificationSettings {
        guard Identifiers.isSafePathID(kind) else { throw HubRequestError.invalidIdentifier }
        return try await post("/notifications/\(kind)",
                              SetNotificationKindRequest(idempotencyKey: idempotencyKey, enabled: enabled),
                              as: NotificationSettings.self)
    }

    func setNotificationPreferences(_ request: SetNotificationPreferencesRequest) async throws -> NotificationSettings {
        try await post("/notifications/preferences", request, as: NotificationSettings.self)
    }
}
