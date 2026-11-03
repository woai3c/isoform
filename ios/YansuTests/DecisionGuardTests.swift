import Foundation
import LocalAuthentication
import XCTest
@testable import Yansu

final class DecisionGuardTests: XCTestCase {
    private struct Cancelled: Error {}

    private final class FakeAuthenticator: OwnerAuthenticating, @unchecked Sendable {
        let state: OwnerAuthenticationAvailability
        let outcome: Result<Bool, Error>
        private let lock = NSLock()
        private var asked: [String] = []

        var prompts: [String] {
            lock.lock()
            defer { lock.unlock() }
            return asked
        }

        init(state: OwnerAuthenticationAvailability, outcome: Result<Bool, Error> = .success(true)) {
            self.state = state
            self.outcome = outcome
        }

        func availability() -> OwnerAuthenticationAvailability {
            state
        }

        private func record(_ reason: String) {
            lock.lock()
            defer { lock.unlock() }
            asked.append(reason)
        }

        func authenticate(reason: String) async throws -> Bool {
            record(reason)
            return try outcome.get()
        }
    }

    func testNoPasscodeProceedsWithoutAPrompt() async throws {
        let authenticator = FakeAuthenticator(state: .noPasscode)

        try await DecisionGuard(authenticator: authenticator).authorize(reason: "Approve this")

        XCTAssertTrue(authenticator.prompts.isEmpty)
    }

    func testAnAvailableOwnerCheckIsRequiredAndCarriesTheReason() async throws {
        let authenticator = FakeAuthenticator(state: .available, outcome: .success(true))

        try await DecisionGuard(authenticator: authenticator).authorize(reason: "Approve this")

        XCTAssertEqual(authenticator.prompts, ["Approve this"])
    }

    func testADeclinedConfirmationBlocksTheDecision() async {
        let authenticator = FakeAuthenticator(state: .available, outcome: .success(false))
        do {
            try await DecisionGuard(authenticator: authenticator).authorize(reason: "Approve this")
            XCTFail("a declined confirmation must block the decision")
        } catch {
            XCTAssertEqual(error as? DecisionGuardError, .notConfirmed)
        }
    }

    func testAnErrorDuringTheConfirmationAlsoBlocksTheDecision() async {
        let authenticator = FakeAuthenticator(state: .available, outcome: .failure(Cancelled()))
        do {
            try await DecisionGuard(authenticator: authenticator).authorize(reason: "Stop this")
            XCTFail("a cancelled confirmation must block the decision")
        } catch {
            XCTAssertEqual(error as? DecisionGuardError, .notConfirmed)
        }
    }

    func testAnUnavailablePolicyFailsClosedWithoutAPrompt() async {
        let authenticator = FakeAuthenticator(state: .unavailable)
        do {
            try await DecisionGuard(authenticator: authenticator).authorize(reason: "Approve this")
            XCTFail("an unavailable policy must not approve silently")
        } catch {
            XCTAssertEqual(error as? DecisionGuardError, .unavailable)
        }
        XCTAssertTrue(authenticator.prompts.isEmpty)
    }

    func testDeviceStateMapsToTheRightAvailability() {
        XCTAssertEqual(LocalOwnerAuthenticator.availability(canEvaluate: true, errorCode: nil), .available)
        XCTAssertEqual(LocalOwnerAuthenticator.availability(canEvaluate: false, errorCode: LAError.Code.passcodeNotSet.rawValue),
                       .noPasscode)
        XCTAssertEqual(LocalOwnerAuthenticator.availability(canEvaluate: false, errorCode: LAError.Code.biometryLockout.rawValue),
                       .unavailable)
        XCTAssertEqual(LocalOwnerAuthenticator.availability(canEvaluate: false, errorCode: nil), .unavailable)
    }

    func testLocalChangesDoNotDependOnAvailableOwnerAuthentication() async throws {
        for state: OwnerAuthenticationAvailability in [.available, .unavailable, .noPasscode] {
            let authenticator = FakeAuthenticator(state: state, outcome: .success(false))
            try await DecisionGuard(authenticator: authenticator).authorize(.localChange, reason: "Update a local record")
            XCTAssertTrue(authenticator.prompts.isEmpty)
        }
    }

    func testOutsideActionsStillRequireOwnerConfirmation() async {
        let authenticator = FakeAuthenticator(state: .available, outcome: .success(false))
        do {
            try await DecisionGuard(authenticator: authenticator).authorize(.outsideAction, reason: "Send a message")
            XCTFail("A declined owner check must prevent an outside action")
        } catch {
            XCTAssertEqual(error as? DecisionGuardError, .notConfirmed)
        }
        XCTAssertEqual(authenticator.prompts, ["Send a message"])
    }

    func testOnlyReviewedLocalCardActionsSkipOwnerAuthentication() {
        let localActions: Set<String> = ["open", "answer", "review", "open_settings", "see", "open_person",
                                         "not_now", "dismiss", "send"]
        for action in ChatProactiveAction.knownIDs.union(["pay", "delete", "future_action"]) {
            XCTAssertEqual(DecisionAuthenticationPolicy.requiresOwnerConfirmation(
                proactiveActionID: action, permissionRequest: false), !localActions.contains(action), action)
        }
    }

    func testPermissionMetadataCannotUseALocalActionToBypassOwnerAuthentication() {
        for action in ChatProactiveAction.knownIDs.union(["future_action"]) {
            XCTAssertTrue(DecisionAuthenticationPolicy.requiresOwnerConfirmation(
                proactiveActionID: action, permissionRequest: true), action)
        }
    }

    @MainActor
    func testFeedCorrectionsUseTheLocalGuardAndStillRequireAReasonAndAComputerReceipt() async throws {
        let directory = try makeTemporaryDirectory()
        let hub = LocalCorrectionHub()
        let journal = PendingWriteJournal(fileURL: directory.appendingPathComponent("writes.json"))
        let store = FeedStore(hub: hub, journal: journal, guardian: LocalDecisionGuard(), isPaired: { true },
                              fileURL: directory.appendingPathComponent("feed.json"))
        var brief = MobileBriefV1(version: 1, briefId: "2026-10-20-morning", slot: "morning", localDate: "2026-10-20")
        brief.evidence = [BriefEvidence(ref: "e1", sourceKind: "memory", sourceId: "memory-1")]
        brief.sections.learned = [BriefItem(id: "learned-1", key: FeedAssembly.itemKey(
            section: "learned", sourceKind: "memory", sourceID: "memory-1"), title: "A learned preference", refs: ["e1"])]
        let card = try XCTUnwrap(FeedAssembly.cards(in: brief).first)

        let blank = await store.send(.notRight, for: card, reason: " ")
        XCTAssertFalse(blank)
        let before = await hub.requests
        XCTAssertTrue(before.isEmpty)

        let corrected = await store.send(.notRight, for: card, reason: "That is not my preference")
        XCTAssertTrue(corrected)
        let requests = await hub.requests
        XCTAssertEqual(requests, ["That is not my preference"])
        XCTAssertEqual(store.choices[card.key]?.delivery, .sent)
        XCTAssertEqual(store.notice, FeedCopy.memoryRemoved)
        let remaining = await journal.entries()
        XCTAssertTrue(remaining.isEmpty)
    }
}

private actor LocalCorrectionHub: FeedHubAPI {
    private(set) var requests: [String] = []

    func sendFeedback(briefID: String, itemID: String, action: FeedFeedbackAction, reason: String?,
                      idempotencyKey: String) async throws -> BriefFeedbackPayload {
        requests.append(reason ?? "")
        return BriefFeedbackPayload(recorded: true, effect: "memory_removed")
    }
}
