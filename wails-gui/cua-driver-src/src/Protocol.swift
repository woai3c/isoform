import ApplicationServices
import Foundation

// Wire protocol: line-delimited JSON over a Unix domain socket.
// One request per line; one response per line. Responses are correlated by `id`.

struct Request: Decodable {
    let id: String
    let cmd: String
    let args: [String: AnyCodable]?
}

struct Response: Encodable {
    let id: String
    let ok: Bool
    let data: AnyCodable?
    let error: ErrorPayload?

    static func success(_ id: String, _ data: Any?) -> Response {
        Response(id: id, ok: true, data: AnyCodable(data), error: nil)
    }
    static func failure(_ id: String, _ err: CUError) -> Response {
        Response(id: id, ok: false, data: nil, error: ErrorPayload(code: err.code, message: err.message, hint: err.hint))
    }
}

struct ErrorPayload: Encodable {
    let code: String
    let message: String
    let hint: String?
}

// MARK: - Errors

struct CUError: Error {
    let code: String
    let message: String
    let hint: String?

    static func invalidArg(_ msg: String) -> CUError {
        CUError(code: "invalid_arg", message: msg, hint: nil)
    }
    static func axDenied() -> CUError {
        CUError(
            code: "ax_not_granted",
            message: "Accessibility permission not granted to Alma Computer Use.",
            hint: "Open System Settings → Privacy & Security → Accessibility and enable \"Alma Computer Use\"."
        )
    }
    static func scDenied() -> CUError {
        CUError(
            code: "sc_not_granted",
            message: "Screen Recording permission not granted to Alma Computer Use.",
            hint: "Open System Settings → Privacy & Security → Screen & System Audio Recording and enable \"Alma Computer Use\"."
        )
    }
    static func appNotFound(_ q: String) -> CUError {
        CUError(code: "app_not_found", message: "No running app matched \(q).", hint: nil)
    }
    static func elementNotFound(_ ref: String) -> CUError {
        CUError(code: "element_not_found", message: "No element known for ref \(ref). Run `snap` first.", hint: nil)
    }
    static func refStale(_ ref: String) -> CUError {
        CUError(
            code: "ref_stale",
            message: "Element \(ref) is no longer valid (UI changed). Re-run `snap`.",
            hint: nil
        )
    }
    static func actionUnsupported(_ action: String) -> CUError {
        CUError(code: "action_unsupported", message: "Element does not support action \(action).", hint: nil)
    }
    /// Returned when a caller tries to send a backgrounded action to
    /// a Chromium/Electron app without `activate: true`. Two failure
    /// modes the guard prevents:
    ///   • press_key with cmd modifier — Chromium strips the cmd
    ///     modifier on non-frontmost targets; the bare letter (e.g.
    ///     "k" from cmd+K) lands in the focused text composer, which
    ///     auto-saves as a draft. User-visible data pollution.
    ///   • click — the bgclick path inserts maskCommand into the
    ///     CGEvent flags to bypass WindowServer's frontmost filter;
    ///     Electron click handlers also read that bit and interpret
    ///     it as cmd+click ("open in new window" on Slack/Discord
    ///     sidebar items). User sees a stranded duplicate window.
    /// Both classes share an `electron_shortcut_gated` code so callers
    /// (and the LLM) can pattern-match recovery: just add `activate:
    /// true` and retry.
    static func electronShortcutGated(bundle: String, op: String, detail: String) -> CUError {
        CUError(
            code: "electron_shortcut_gated",
            message: "\(op) on Electron app \(bundle) without `activate: true` is refused: \(detail)",
            hint: "Retry with `activate: true` — brings the target frontmost for the duration of the action (~200 ms visible flicker), then restores the user's prior frontmost app. The bgclick / cmd-modifier tricks that work on native apps cause user-visible side effects on Chromium-shell apps. See WEB_APPS.md → \"In-app navigation in Electron apps\"."
        )
    }
    static func axFailure(_ err: AXError, context: String) -> CUError {
        CUError(code: "ax_failure", message: "AX error \(axErrorName(err)) while \(context).", hint: nil)
    }
    static func internalError(_ msg: String) -> CUError {
        CUError(code: "internal", message: msg, hint: nil)
    }
}

func axErrorName(_ e: AXError) -> String {
    switch e {
    case .success: return "success"
    case .failure: return "failure"
    case .illegalArgument: return "illegalArgument"
    case .invalidUIElement: return "invalidUIElement"
    case .invalidUIElementObserver: return "invalidUIElementObserver"
    case .cannotComplete: return "cannotComplete"
    case .attributeUnsupported: return "attributeUnsupported"
    case .actionUnsupported: return "actionUnsupported"
    case .notificationUnsupported: return "notificationUnsupported"
    case .notImplemented: return "notImplemented"
    case .notificationAlreadyRegistered: return "notificationAlreadyRegistered"
    case .notificationNotRegistered: return "notificationNotRegistered"
    case .apiDisabled: return "apiDisabled"
    case .noValue: return "noValue"
    case .parameterizedAttributeUnsupported: return "parameterizedAttributeUnsupported"
    case .notEnoughPrecision: return "notEnoughPrecision"
    @unknown default: return "unknown(\(e.rawValue))"
    }
}

// MARK: - AnyCodable (small, enough for our needs)

struct AnyCodable: Codable {
    let value: Any?

    init(_ value: Any?) { self.value = value }

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { value = nil; return }
        if let b = try? c.decode(Bool.self) { value = b; return }
        if let i = try? c.decode(Int.self) { value = i; return }
        if let d = try? c.decode(Double.self) { value = d; return }
        if let s = try? c.decode(String.self) { value = s; return }
        if let a = try? c.decode([AnyCodable].self) { value = a.map { $0.value as Any? }; return }
        if let o = try? c.decode([String: AnyCodable].self) {
            var dict: [String: Any?] = [:]
            for (k, v) in o { dict[k] = v.value }
            value = dict
            return
        }
        value = nil
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        guard let v = value else { try c.encodeNil(); return }
        // Booleans bridge to NSNumber on Apple platforms, so we need to
        // distinguish Bool before falling into the generic numeric path.
        // CFBooleanGetTypeID gives us the definitive answer.
        if CFGetTypeID(v as CFTypeRef) == CFBooleanGetTypeID() {
            try c.encode((v as! NSNumber).boolValue); return
        }
        if let num = v as? NSNumber {
            // objCType discriminates integer vs. floating-point widths.
            switch String(cString: num.objCType) {
            case "c", "C", "s", "S", "i", "I", "l", "L", "q", "Q":
                try c.encode(num.intValue); return
            default:
                try c.encode(num.doubleValue); return
            }
        }
        switch v {
        case let b as Bool: try c.encode(b)
        case let i as Int: try c.encode(i)
        case let i as Int64: try c.encode(i)
        case let i as UInt32: try c.encode(i)
        case let d as Double: try c.encode(d)
        case let f as Float: try c.encode(Double(f))
        case let cg as CGFloat: try c.encode(Double(cg))
        case let s as String: try c.encode(s)
        case let arr as [Any?]: try c.encode(arr.map { AnyCodable($0) })
        case let arr as [Any]: try c.encode(arr.map { AnyCodable($0) })
        case let dict as [String: Any?]:
            var out: [String: AnyCodable] = [:]
            for (k, v) in dict { out[k] = AnyCodable(v) }
            try c.encode(out)
        case let dict as [String: Any]:
            var out: [String: AnyCodable] = [:]
            for (k, v) in dict { out[k] = AnyCodable(v) }
            try c.encode(out)
        default:
            // Fall back to string description for unknown types.
            try c.encode(String(describing: v))
        }
    }
}

// Convenience accessors on [String: AnyCodable] for handlers.
extension Optional where Wrapped == [String: AnyCodable] {
    func str(_ k: String) -> String? {
        guard case let .some(d) = self, let v = d[k]?.value else { return nil }
        return v as? String
    }
    func int(_ k: String) -> Int? {
        guard case let .some(d) = self, let v = d[k]?.value else { return nil }
        if let i = v as? Int { return i }
        if let d = v as? Double { return Int(d) }
        if let s = v as? String { return Int(s) }
        return nil
    }
    func double(_ k: String) -> Double? {
        guard case let .some(d) = self, let v = d[k]?.value else { return nil }
        if let d = v as? Double { return d }
        if let i = v as? Int { return Double(i) }
        if let s = v as? String { return Double(s) }
        return nil
    }
    func bool(_ k: String) -> Bool? {
        guard case let .some(d) = self, let v = d[k]?.value else { return nil }
        if let b = v as? Bool { return b }
        if let i = v as? Int { return i != 0 }
        return nil
    }
    /// Raw value without any decoding. Preserves `null` as `Any?.none` so
    /// callers can distinguish "absent" from "explicit null" if needed.
    func any(_ k: String) -> Any? {
        guard case let .some(d) = self else { return nil }
        return d[k]?.value
    }
}
