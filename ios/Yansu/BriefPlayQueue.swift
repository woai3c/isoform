import Foundation

/// A notice Play request is tied to the exact pairing and brief until playback starts.
@MainActor
final class BriefPlayQueue {
    static func shouldQueue(after error: Error) -> Bool {
        if let error = error as? HubRequestError {
            switch error {
            case .noAnswer: return true
            default: return false
            }
        }
        if let error = error as? RelayClientError {
            switch error {
            case .offline, .timedOut, .unavailable: return true
            default: return false
            }
        }
        if let error = error as? URLError {
            return [.notConnectedToInternet, .networkConnectionLost, .timedOut, .cannotConnectToHost].contains(error.code)
        }
        return false
    }

    struct Entry: Codable, Equatable {
        let hubID: String
        let deviceID: String
        let briefID: String
    }

    private let fileURL: URL
    init(fileURL: URL) { self.fileURL = fileURL }

    func load(pairing: TodayPairingIdentity?) -> Entry? {
        guard let data = try? Data(contentsOf: fileURL), let entry = try? JSONDecoder().decode(Entry.self, from: data),
              let pairing, entry.hubID == pairing.hubID, entry.deviceID == pairing.deviceID,
              Identifiers.isSafePathID(entry.briefID) else { return nil }
        return entry
    }

    func save(_ entry: Entry) throws {
        guard Identifiers.isSafePathID(entry.briefID) else { throw HubRequestError.invalidIdentifier }
        try FileManager.default.createDirectory(at: fileURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONEncoder().encode(entry).write(to: fileURL, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    func clear() throws {
        if FileManager.default.fileExists(atPath: fileURL.path) { try FileManager.default.removeItem(at: fileURL) }
    }
}
