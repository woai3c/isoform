import Foundation

/// Runtime toggles driven by environment variables. Intentionally tiny —
/// we resist the urge to grow a config system. Each flag has a concrete
/// reason to exist and is checked lazily at the call site.
///
/// Convention: `ALMA_CU_<NAME>`; truthy values are `1 / true / yes / on`
/// (case-insensitive). Anything else is false.
enum FeatureFlags {
    /// Skip AX `PerformAction` and go straight to the synthetic-click
    /// path (`EventDispatch.click` → dlsym CGEventPostToPid). Primarily
    /// for debugging the HID path end-to-end on a target that would
    /// normally resolve via AX; also lets audit-sensitive deployments
    /// prove the dlsym fallback actually fires.
    static let forceSynth = readBool("ALMA_CU_FORCE_SYNTH")

    /// Emit one stderr line the first time the dlsym'd CGEventPostToPid
    /// resolves successfully. Useful during smoke tests to confirm the
    /// binary is on the opaque path rather than the static import.
    static let logDlsym = readBool("ALMA_CU_LOG_DLSYM")

    private static func readBool(_ key: String) -> Bool {
        guard let raw = ProcessInfo.processInfo.environment[key] else { return false }
        switch raw.lowercased() {
        case "1", "true", "yes", "on": return true
        default: return false
        }
    }
}
