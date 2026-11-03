import Foundation

/// The writes ActionStore makes, as the phone sees them. HubChannel conforms; tests substitute a stub.
protocol ActionHubAPI: Sendable {
    func rejectAttribution(memoryID: String, reason: String, idempotencyKey: String) async throws -> Bool
    func startOneTimeWork(_ request: StartOneTimeWorkRequest) async throws -> String
    func runOneTimeWork(id: String, idempotencyKey: String) async throws
}

extension ActionHubAPI {
    func runOneTimeWork(id: String, idempotencyKey: String) async throws {
        throw HubRequestError.noAnswer
    }
}

extension HubChannel: ActionHubAPI {
    func runOneTimeWork(id: String, idempotencyKey: String) async throws {
        guard Identifiers.isSafePathID(id) else { throw HubRequestError.invalidIdentifier }
        _ = try await post("/handoffs/\(id)/start", MobileTaskStopRequest(idempotencyKey: idempotencyKey),
                           as: StartedOneTimeWorkPayload.self)
    }
    func rejectAttribution(memoryID: String, reason: String, idempotencyKey: String) async throws -> Bool {
        guard Identifiers.isSafePathID(memoryID) else { throw HubRequestError.invalidIdentifier }
        let removed = try await post("/memory/\(memoryID)/reject-attribution",
                                     RejectAttributionRequest(idempotencyKey: idempotencyKey, reason: reason),
                                     as: RemovedPayload.self)
        return removed.removed
    }

    func startOneTimeWork(_ request: StartOneTimeWorkRequest) async throws -> String {
        try await post("/handoffs", request, as: CreatedRecordPayload.self).id
    }
}

private struct StartedOneTimeWorkPayload: Decodable {
    let id: String
    let sessionId: String
}
