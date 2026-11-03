import Foundation

struct AgentNameCheck: Decodable, Equatable, Sendable {
    let name: String
    let available: Bool
    let reason: String?
    let suggestion: String?
}

enum AgentsError: Error, Equatable {
    case taken(suggestion: String?)
    case notAllowed, invalidName, invalidEmail, token, rate, none, failed, unreachable
    case rateUntil(Date)
    /// Verify: the code is not the one out.
    case wrongCode
    /// Verify or choose: the hold ran out, or no code is out.
    case expired
}

protocol AgentsAPI: Sendable {
    func check(name: String, copy: YourAgentCopy) async throws -> AgentNameCheck
    func reserve(name: String, email: String, copy: YourAgentCopy) async throws -> AgentReservation
    func reservation(copy: YourAgentCopy) async throws -> AgentReservation?
    func verify(code: String, copy: YourAgentCopy) async throws -> AgentReservation
    func resend(copy: YourAgentCopy) async throws -> AgentReservation
    func choose(keep: Bool, copy: YourAgentCopy) async throws -> AgentReservation
    func cancel(copy: YourAgentCopy) async throws -> AgentReservation?
}

struct AgentsClient: AgentsAPI {
    private struct Failure: Decodable { let error: String; let suggestion: String?; let retryAt: String? }
    private struct CancelAnswer: Decodable { let reservation: AgentReservation? }
    let session: URLSession
    let baseURL: URL
    let language: String

    static var defaultBaseURL: URL {
        #if DEBUG && targetEnvironment(simulator)
        if let value = ProcessInfo.processInfo.environment["ISOFORM_AGENTS_API"], let url = URL(string: value) { return url }
        #endif
        return URL(string: "https://www.isoform.ai/api/agents")!
    }

    init(session: URLSession = .shared, baseURL: URL = Self.defaultBaseURL,
         language: String = Bundle.main.preferredLocalizations.first ?? "en") {
        self.session = session
        self.baseURL = baseURL
        self.language = language.hasPrefix("zh") ? "zh-CN" : "en"
    }

    func check(name: String, copy: YourAgentCopy) async throws -> AgentNameCheck {
        let result: AgentNameCheck = try await request("check", body: ["name": YourAgentRules.normalize(name),
            "candidates": candidates(name: name, copy: copy), "hub": copy.hub, "token": copy.token])
        guard result.available || ["taken", "not_allowed", "invalid"].contains(result.reason ?? "") else {
            throw AgentsError.unreachable
        }
        return result
    }

    func reserve(name: String, email: String, copy: YourAgentCopy) async throws -> AgentReservation {
        try await request("reserve", body: ["hub": copy.hub, "token": copy.token,
            "name": YourAgentRules.normalize(name), "email": YourAgentRules.normalize(email),
            "lang": language, "client": "iphone", "candidates": candidates(name: name, copy: copy)])
    }

    func verify(code: String, copy: YourAgentCopy) async throws -> AgentReservation {
        try await request("verify", body: ["hub": copy.hub, "token": copy.token, "code": code])
    }

    func resend(copy: YourAgentCopy) async throws -> AgentReservation {
        try await request("resend", body: ["hub": copy.hub, "token": copy.token])
    }

    func choose(keep: Bool, copy: YourAgentCopy) async throws -> AgentReservation {
        try await request("choose", body: ["hub": copy.hub, "token": copy.token, "keep": keep])
    }

    func cancel(copy: YourAgentCopy) async throws -> AgentReservation? {
        let answer: CancelAnswer = try await request("cancel", body: ["hub": copy.hub, "token": copy.token])
        return answer.reservation
    }

    private func candidates(name: String, copy: YourAgentCopy) -> [String] {
        copy.nameCandidates.map(YourAgentRules.normalize).filter { !$0.isEmpty } + [YourAgentRules.normalize(name)]
    }

    func reservation(copy: YourAgentCopy) async throws -> AgentReservation? {
        do { return try await request("reservation", copy: copy) }
        catch AgentsError.none { return nil }
    }

    private func request<T: Decodable>(_ path: String, body: [String: Any]? = nil,
                                       copy: YourAgentCopy? = nil) async throws -> T {
        var components = URLComponents(url: baseURL.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if let copy { components.queryItems = [URLQueryItem(name: "hub", value: copy.hub)] }
        var request = URLRequest(url: components.url!, timeoutInterval: 10)
        if let body {
            request.httpMethod = "POST"
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        if let copy { request.setValue("Bearer \(copy.token)", forHTTPHeaderField: "Authorization") }
        let data: Data
        let response: URLResponse
        do { (data, response) = try await session.data(for: request) }
        catch { throw AgentsError.unreachable }
        guard let http = response as? HTTPURLResponse else { throw AgentsError.unreachable }
        if http.statusCode == 200 {
            guard let value = try? JSONDecoder().decode(T.self, from: data) else { throw AgentsError.unreachable }
            return value
        }
        guard let failure = try? JSONDecoder().decode(Failure.self, from: data) else { throw AgentsError.unreachable }
        switch (path, http.statusCode, failure.error) {
        case (_, 409, "taken"): throw AgentsError.taken(suggestion: failure.suggestion)
        case ("reserve", 422, "not_allowed"): throw AgentsError.notAllowed
        case ("reserve", 422, "invalid_name"): throw AgentsError.invalidName
        case ("reserve", 422, "invalid_email"): throw AgentsError.invalidEmail
        case ("verify", 400, "wrong_code"): throw AgentsError.wrongCode
        case (_, 410, "expired"): throw AgentsError.expired
        case (_, 403, "token"): throw AgentsError.token
        case (_, 429, "rate"):
            if let text = failure.retryAt,
               let date = ISO8601DateFormatter().date(from: text) ?? Self.fractionalDate(text) {
                throw AgentsError.rateUntil(date)
            }
            throw AgentsError.rate
        case ("reservation", 404, "none"), ("resend", 404, "none"): throw AgentsError.none
        case (_, 400..<600, _): throw AgentsError.failed
        default: throw AgentsError.unreachable
        }
    }
    private static func fractionalDate(_ text: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: text)
    }
}
