import notify
import Foundation

/// Drives the Simulator's biometric emulation the way the Simulator app's Features menu does, through the
/// same Darwin notifications. A Simulator without a passcode still presents the owner check, so a live test
/// enrolls Face ID for its run and answers the prompt with a matching face. On a device these do nothing.
enum SimulatorBiometrics {
    private static let enrollment = "com.apple.BiometricKit.enrollmentChanged"
    private static let matches = ["com.apple.BiometricKit_Sim.pearl.match", "com.apple.BiometricKit_Sim.fingerTouch.match"]

    static func setEnrolled(_ enrolled: Bool) {
        var token: Int32 = 0
        guard notify_register_check(enrollment, &token) == UInt32(NOTIFY_STATUS_OK) else { return }
        notify_set_state(token, enrolled ? 1 : 0)
        notify_post(enrollment)
        notify_cancel(token)
    }

    /// Answers a pending Face ID or Touch ID prompt with a match. Harmless when nothing is pending.
    static func matchOwner() {
        for name in matches {
            notify_post(name)
        }
    }
}
