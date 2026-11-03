import AppKit
import Foundation

/// Hard blocklist of apps the agent is not allowed to drive.
///
/// Mirrors Codex's `ComputerUseAllowForbiddenTargets` — a bundle-id
/// guard that fires BEFORE any AX or synth dispatch runs. The point
/// isn't that these apps can't be interacted with technically, it's
/// that auto-clicking in them either (a) cannot help the user no
/// matter what the agent intended, or (b) produces irreversible or
/// privacy-affecting side effects that demand a conscious human
/// decision.
///
/// Categories, in order of risk:
///   • Privacy & security UI — System Settings. An agent clicking
///     "Allow" on a TCC grant dialog, or toggling a firewall rule,
///     is a privilege-escalation bug waiting to happen.
///   • Credential entry surfaces — loginwindow, unlock prompts, the
///     password autofill UI. Any text going in is either useless or
///     actively harmful.
///   • OS chrome / compositor — WindowServer, Dock, LaunchPad. These
///     aren't apps you "use"; clicks in them rearrange the user's
///     own space in ways the agent has no business making.
///
/// Override: pass `force_forbidden: true` in the request args. This is
/// an escape hatch for integration flows where the agent genuinely has
/// to interact with these surfaces (e.g. guiding the user through an
/// accessibility grant). It's deliberately verbose so it can't be
/// accidentally left on.
enum ForbiddenTargets {

    /// Exact bundle-id match. Prefer exact strings over prefixes —
    /// suffix-matching sensitive bundles produces false positives on
    /// third-party apps that happen to contain "settings" in their id.
    static let bundles: Set<String> = [
        // Privacy / permissions / settings — the worst blast radius.
        "com.apple.systempreferences",           // System Settings.app on macOS 13+ (and legacy System Preferences)
        "com.apple.security.pluginkit",          // Security plugin kit host
        "com.apple.security-notification",       // System privacy notification pop-ups
        "com.apple.preference.general",

        // Login / unlock / credential entry.
        "com.apple.loginwindow",
        "com.apple.SecurityAgent",               // auth prompts (sudo-equivalent UI)
        "com.apple.PassphraseAgent",
        "com.apple.PasswordBreachAgent",

        // TCC — the permission-granting daemon itself.
        "com.apple.tccd",
        "com.apple.universalaccessAuthWarn",     // AX permission prompt UI

        // OS chrome / compositor — not an app in the usual sense.
        "com.apple.WindowServer",
        "com.apple.dock",                        // including Dock-launched LaunchPad
        "com.apple.launchpad",
        "com.apple.notificationcenterui",
        "com.apple.controlcenter",
        "com.apple.spotlight",

        // Screensaver — can't be meaningfully driven.
        "com.apple.ScreenSaver.Engine",
        "com.apple.ScreenSaverEngine",
    ]

    /// Returns true iff the bundle is on the blocklist. nil is not
    /// forbidden (unknown apps fall through to normal dispatch).
    static func isForbidden(bundleId: String?) -> Bool {
        guard let b = bundleId else { return false }
        return bundles.contains(b)
    }

    /// Resolve the bundle id for a pid and check it. `nil` pid is
    /// treated as not-forbidden (caller didn't target anyone specific).
    static func check(pid: pid_t?, allowOverride: Bool = false) throws {
        if allowOverride { return }
        guard let p = pid else { return }
        guard let app = NSRunningApplication(processIdentifier: p) else { return }
        let bundle = app.bundleIdentifier
        if isForbidden(bundleId: bundle) {
            throw CUError(
                code: "forbidden_target",
                message: "target app \(bundle ?? "(unknown)") is on the CUA blocklist for user-safety reasons",
                hint: "Pass `force_forbidden: true` to override. Only do this for flows that genuinely require interacting with the system-privacy UI — e.g. guiding the user through an Accessibility permission grant."
            )
        }
    }

    /// Variant when the caller has the bundle id directly. Used by
    /// Layer-1 dispatch so we block `intent` / `open_url` on forbidden
    /// bundles too — an agent should not be able to LSOpen a URL into
    /// System Settings.app without the explicit override.
    static func check(bundleId: String?, allowOverride: Bool = false) throws {
        if allowOverride { return }
        if isForbidden(bundleId: bundleId) {
            throw CUError(
                code: "forbidden_target",
                message: "target bundle \(bundleId ?? "(unknown)") is on the CUA blocklist for user-safety reasons",
                hint: "Pass `force_forbidden: true` to override."
            )
        }
    }
}
