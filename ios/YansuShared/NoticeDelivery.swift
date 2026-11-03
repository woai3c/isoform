import Foundation
import UserNotifications

/// A notice whose sealed copy was opened and checked against its push.
struct OpenedNotice: Equatable, Sendable {
    let plaintext: NoticePlaintext
    let json: String
}

// Opens a push's sealed notice and applies it to the notification content. Used by the notification
// service extension before the banner is shown. Every check must pass; any miss returns nil and the caller
// keeps the fallback the relay sent.
enum NoticeDelivery {
    static func open(_ push: NoticePush, secrets: SecretStore) -> OpenedNotice? {
        guard let ct = push.ct,
              let stored = SharedNoticeStore.load(secrets: secrets),
              stored.context.hubID == push.hub,
              stored.context.deviceID == push.dev else { return nil }
        guard let json = try? NoticeCrypto.open(ct, key: stored.key, hub: push.hub, dev: push.dev, noticeID: push.id),
              let plaintext = try? NoticeCrypto.decode(json),
              plaintext.id == push.id,
              plaintext.kind == push.kind,
              NoticeCategory(rawValue: plaintext.category) != nil,
              let text = String(data: json, encoding: .utf8) else { return nil }
        return OpenedNotice(plaintext: plaintext, json: text)
    }

    /// Sets the sealed title, body and category, and attaches the JSON under `yansu.notice` for the app.
    /// The thread identifier stays the hub id the relay already set.
    static func apply(_ notice: OpenedNotice, to content: UNMutableNotificationContent) {
        content.title = notice.plaintext.title
        content.body = notice.plaintext.body ?? ""
        content.categoryIdentifier = notice.plaintext.category
        var userInfo = content.userInfo
        userInfo[NoticePush.noticeUserInfoKey] = notice.json
        content.userInfo = userInfo
    }
}
