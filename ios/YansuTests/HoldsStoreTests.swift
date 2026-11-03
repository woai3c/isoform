import XCTest
@testable import Yansu

private actor HoldsTestHub: HoldsHubAPI {
    private(set) var actions: [PhoneHoldAction] = []
    func holdProposal(kind: String, id: String) async throws -> PhoneHoldOffer { throw HubRequestError.invalidIdentifier }
    func holdStatus(id: String) async throws -> PhoneHoldStatus { PhoneHoldStatus(id: id, state: "pending_write") }
    func holdAction(_ action: PhoneHoldAction) async throws -> PhoneHoldStatus {
        actions.append(action)
        return PhoneHoldStatus(id: action.holdId ?? "hold-1", state: action.action == "hold" ? "pending_write" : "removed")
    }
    func recorded() -> [PhoneHoldAction] { actions }
}

@MainActor
final class HoldsStoreTests: XCTestCase {
    func testHoldUsesJournalAndUndoCallsTheSameHold() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let journal = PendingWriteJournal(fileURL: root.appendingPathComponent("writes.json"), hubID: { "hub" })
        let hub = HoldsTestHub()
        let store = HoldsStore(hub: hub, journal: journal, pairingID: { "hub/phone" })
        let slot = PhoneHoldSlot(start: "2030-01-02T14:00:00Z", end: "2030-01-02T16:00:00Z")
        let proposal = PhoneHoldProposal(id: "hold-1", thing: PhoneHoldThing(title: "Q4 plan", sourceKind: "up_next", sourceId: "offer"), calendarId: "work", slots: [slot])
        let offer = PhoneHoldOffer(proposal: proposal, calendars: [PhoneHoldCalendar(id: "work", title: "Work")], token: "signed")
        let result = await store.place(offer, slot: slot, calendarID: "work")
        XCTAssertEqual(result?.state, "pending_write")
        let undone = await store.undo(id: "hold-1")
        XCTAssertTrue(undone)
        let actions = await hub.recorded()
        XCTAssertEqual(actions.map(\.action), ["hold", "undo_hold"])
        XCTAssertEqual(actions.first?.slot, slot)
        XCTAssertEqual(actions.last?.holdId, "hold-1")
        XCTAssertNotEqual(actions.first?.idempotencyKey, actions.last?.idempotencyKey)
        let remaining = await journal.entries()
        XCTAssertTrue(remaining.isEmpty)
    }
}
