import Foundation

// The App Group that the app and the notification service extension share. The brief cache lives in its
// container, so the extension (and a widget later) can read it.
enum AppGroup {
    static let identifier = "group.com.isoform.companion"

    static var containerURL: URL? {
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: identifier)
    }
}
