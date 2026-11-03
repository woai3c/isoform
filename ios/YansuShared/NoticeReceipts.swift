import Foundation

// Receipts for a sealed notice: delivered (the extension), opened and dismissed (taps), acted (an action).
// Best effort. A receipt that cannot be sent is dropped, never queued, so it cannot block the user.
struct NoticeReceipts: Sendable {
    let relay: RelayClient
    let secrets: SecretStore
    let attempts: Int
    let retryDelayNanoseconds: UInt64
    let now: @Sendable () -> Date

    init(relay: RelayClient, secrets: SecretStore, attempts: Int = 2,
         retryDelayNanoseconds: UInt64 = 2_000_000_000, now: @escaping @Sendable () -> Date = { Date() }) {
        self.relay = relay
        self.secrets = secrets
        self.attempts = max(attempts, 1)
        self.retryDelayNanoseconds = retryDelayNanoseconds
        self.now = now
    }

    /// Returns true when the relay accepted the receipt.
    @discardableResult
    func post(noticeID: String, state: NoticeReceiptState, action: String = "") async -> Bool {
        guard NoticeIdentifier.isSafe(noticeID),
              let credentials = SharedNoticeStore.relayCredentials(secrets: secrets) else { return false }
        let at = ISO8601DateFormatter().string(from: now())
        guard let body = try? JSONEncoder().encode(NoticeReceiptBody(state: state.rawValue, action: action, at: at)) else {
            return false
        }
        for attempt in 1...attempts {
            do {
                try await relay.postNoticeReceipt(credentials, noticeID: noticeID, body: body)
                return true
            } catch is CancellationError {
                return false
            } catch {
                if attempt < attempts {
                    try? await Task.sleep(nanoseconds: retryDelayNanoseconds)
                }
            }
        }
        return false
    }
}
