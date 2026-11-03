import Foundation

enum RemoteStatus {
    /// `false` (Mac asleep or offline) only when the phone has no route to the relay. A request that timed out
    /// says nothing about the Mac, so it is unknown (`nil`), never asleep.
    static func offline(for error: Error, remote: Bool) -> Bool? {
        guard remote, let error = error as? RelayClientError else { return nil }
        switch error {
        case .offline:
            return false
        default:
            return nil
        }
    }
}
