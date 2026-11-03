import XCTest
@testable import Yansu

/// The scopes Chat, corrections, one-time work and release 4 need. A pairing made before they existed lacks them and
/// shows the re-pair note instead of the features.
@MainActor
final class ChatScopeTests: XCTestCase {
    func testChatNeedsBothReadAndWrite() {
        XCTAssertFalse(SettingsView.allowsChat(["chat:read"]))
        XCTAssertFalse(SettingsView.allowsChat(["chat:write"]))
        XCTAssertTrue(SettingsView.allowsChat(["chat:read", "chat:write"]))
    }

    func testCorrectionsAndOneTimeWorkEachNeedTheirOwnScope() {
        XCTAssertTrue(SettingsView.allowsMemoryCorrection(["memory:correct"]))
        XCTAssertFalse(SettingsView.allowsMemoryCorrection(["handoff:create"]))
        XCTAssertTrue(SettingsView.allowsOneTimeWork(["handoff:create"]))
        XCTAssertFalse(SettingsView.allowsOneTimeWork(["memory:correct"]))
    }

    func testTheNewerFeaturesNeedEveryNewScope() {
        let original = ["brief:read", "brief:refresh", "presence:read", "push", "desk:read", "decide"]
        let releaseThree = ["chat:read", "chat:write", "memory:correct", "handoff:create"]
        XCTAssertFalse(SettingsView.allowsNewerFeatures(original))
        XCTAssertFalse(SettingsView.allowsNewerFeatures(original + ["chat:read", "chat:write", "memory:correct"]))
        XCTAssertFalse(SettingsView.allowsNewerFeatures(original + releaseThree), "release 4 asks for one more re-pair")
        XCTAssertFalse(SettingsView.allowsNewerFeatures(original + releaseThree + ["brief:feedback"]))
        XCTAssertTrue(SettingsView.allowsNewerFeatures(original + releaseThree + SettingsView.releaseFourScopes))
    }

    func testFeedFeedbackNeedsItsOwnScope() {
        XCTAssertTrue(SettingsView.allowsFeedFeedback(["brief:feedback"]))
        XCTAssertFalse(SettingsView.allowsFeedFeedback(["brief:read", "memory:correct"]))
    }

    func testThePhoneDeclaresEveryReleaseFourScopeAtOnce() {
        XCTAssertEqual(SettingsView.releaseFourScopes,
                       ["brief:feedback", "offers:read", "offers:decide", "goals:read", "goals:write", "library:read"])
        for scope in SettingsView.releaseFourScopes {
            XCTAssertTrue(PairingStore.requestedScopes.contains(scope), scope)
        }
        XCTAssertFalse(PairingStore.requestedScopes.contains("drafts:send"))
        XCTAssertEqual(SettingsView.permissionLabels(SettingsView.releaseFourScopes),
                       "Today feedback, Ideas, Answers to ideas, Goals, Goal changes, Library")
    }

    func testTheNewScopesAreShownWithProductNames() {
        let labels = SettingsView.permissionLabels(["chat:read", "chat:write", "memory:correct", "handoff:create"])
        XCTAssertEqual(labels, "Chat, Chat replies, Corrections, One-time work")
    }

    func testThePhoneAsksForTheNewScopesAtPairing() {
        XCTAssertTrue(PairingStore.requestedScopes.contains("chat:read"))
        XCTAssertTrue(PairingStore.requestedScopes.contains("chat:write"))
        XCTAssertTrue(PairingStore.requestedScopes.contains("memory:correct"))
        XCTAssertTrue(PairingStore.requestedScopes.contains("handoff:create"))
    }

    func testTheRetiredDeskScopesAreNeitherRequestedNorListed() {
        XCTAssertFalse(PairingStore.requestedScopes.contains("desk:read"))
        XCTAssertFalse(PairingStore.requestedScopes.contains("decide"))
        // A pairing made while the Desk existed still holds them; Settings lists the rest and every feature works.
        let older = ["brief:read", "push", "desk:read", "decide", "chat:read", "chat:write", "memory:correct",
                     "handoff:create"]
        XCTAssertEqual(SettingsView.permissionLabels(older),
                       "Briefs, Notifications, Chat, Chat replies, Corrections, One-time work")
        XCTAssertTrue(SettingsView.allowsNewerFeatures(older + SettingsView.releaseFourScopes))
        XCTAssertEqual(SettingsView.permissionLabels(["desk:read", "decide"]), "None")
    }

    /// The wait reads "Unpairing...", like "Turning on notifications...": a single "Unpairing." fails this.
    func testTheUnpairButtonNamesTheWaitWithThreeDots() {
        XCTAssertEqual(SettingsView.unpairTitle(unpairing: false), "Unpair this phone")
        XCTAssertEqual(SettingsView.unpairTitle(unpairing: true), "Unpairing...")
    }
}
