import Foundation
import LocalAuthentication

/// Whether this phone can ask its owner to authenticate before a write that needs it.
enum OwnerAuthenticationAvailability: Equatable, Sendable {
    case available
    case noPasscode
    case unavailable
}

enum DecisionGuardError: LocalizedError, Equatable {
    case notConfirmed
    case unavailable

    var errorDescription: String? {
        switch self {
        case .notConfirmed:
            return "Nothing changed on your computer. Confirm with Face ID, Touch ID or your passcode to continue."
        case .unavailable:
            return "Isoform could not confirm it is you on this phone. Unlock this phone and try again."
        }
    }
}

/// The part of LocalAuthentication the guard needs, behind a protocol so tests can stub it.
protocol OwnerAuthenticating: Sendable {
    func availability() -> OwnerAuthenticationAvailability
    func authenticate(reason: String) async throws -> Bool
}

/// Only effects that can execute outside the phone and computer need an owner check. Local changes still
/// require the pairing's server-side permissions and the existing durable write journal.
enum DecisionAuthenticationPolicy {
    static func requiresOwnerConfirmation(proactiveActionID: String, permissionRequest: Bool) -> Bool {
        // Permission options are supplied by the running tool. Their labels and IDs do not establish risk.
        // Keep every permission answer protected, even when an action resembles a harmless card button.
        guard !permissionRequest else { return true }
        switch proactiveActionID {
        case "open", "answer", "review", "open_settings", "see", "open_person", "not_now", "dismiss", "send":
            return false
        default:
            // Includes execution, scheduling, approvals, payments, deletion, and future action IDs.
            return true
        }
    }
}

enum DecisionEffect: Sendable {
    case localChange
    case outsideAction
}

/// Gates an action whose effect may execute outside the phone and computer.
protocol DecisionGuarding: Sendable {
    func authorize(reason: String) async throws
}

extension DecisionGuarding {
    func authorize(_ effect: DecisionEffect, reason: String) async throws {
        guard effect == .outsideAction else { return }
        try await authorize(reason: reason)
    }
}

/// The Feed only records feedback and corrections. Keep its legacy guard call local while its implementation
/// is being developed separately; never inject this guard into a store that can execute work or answer permissions.
struct LocalDecisionGuard: DecisionGuarding {
    func authorize(reason: String) async throws {}
}

struct DecisionGuard: DecisionGuarding {
    private let authenticator: OwnerAuthenticating

    init(authenticator: OwnerAuthenticating = LocalOwnerAuthenticator()) {
        self.authenticator = authenticator
    }

    /// With a passcode or biometrics the owner must confirm. With no passcode there is nothing to confirm
    /// against, so the write proceeds without a prompt. Any other reason the device cannot evaluate the
    /// policy fails closed.
    func authorize(reason: String) async throws {
        switch authenticator.availability() {
        case .noPasscode:
            return
        case .unavailable:
            throw DecisionGuardError.unavailable
        case .available:
            let confirmed: Bool
            do {
                confirmed = try await authenticator.authenticate(reason: reason)
            } catch {
                throw DecisionGuardError.notConfirmed
            }
            guard confirmed else { throw DecisionGuardError.notConfirmed }
        }
    }
}

struct LocalOwnerAuthenticator: OwnerAuthenticating {
    func availability() -> OwnerAuthenticationAvailability {
        var error: NSError?
        let canEvaluate = LAContext().canEvaluatePolicy(.deviceOwnerAuthentication, error: &error)
        let code = error.flatMap { $0.domain == LAError.errorDomain ? $0.code : nil }
        return Self.availability(canEvaluate: canEvaluate, errorCode: code)
    }

    func authenticate(reason: String) async throws -> Bool {
        try await LAContext().evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)
    }

    static func availability(canEvaluate: Bool, errorCode: Int?) -> OwnerAuthenticationAvailability {
        if canEvaluate { return .available }
        if errorCode == LAError.Code.passcodeNotSet.rawValue { return .noPasscode }
        return .unavailable
    }
}
