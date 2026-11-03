import Combine
import Foundation
import Network

@MainActor
final class YourAgentStore: ObservableObject {
    enum Status: Equatable {
        case clear, taken(String?), notAllowed, rules, failed
    }
    /// The card the page shows for the request.
    enum Step: Equatable {
        case form, code, choice, yours, confirm
    }
    /// A line under the code boxes.
    enum CodeStatus: Equatable {
        case wrong, expired
    }
    @Published var name = "" {
        didSet {
            let filtered = YourAgentRules.filterName(name)
            if name != filtered { name = filtered }
            clearNameCheck()
        }
    }
    @Published var email = "" { didSet { emailInvalid = false } }
    /// Digits only, up to six. The sixth digit checks the code.
    @Published var code = "" {
        didSet {
            let digits = String(code.filter { ("0"..."9").contains($0) }.prefix(6))
            if code != digits { code = digits }
            if digits.count == 6 { verificationTask = Task { [weak self] in await self?.verify() } }
        }
    }
    @Published private(set) var emailInvalid = false
    @Published private(set) var copy: YourAgentCopy?
    @Published private(set) var reservation: AgentReservation?
    @Published private(set) var status: Status = .clear
    @Published private(set) var codeStatus: CodeStatus?
    @Published private(set) var verifying = false
    @Published private(set) var sent = false
    @Published private(set) var ranOut = false
    @Published private(set) var reachable = true
    @Published private(set) var inFlight = false
    @Published private(set) var retryAt: Date?
    private let hub: YourAgentHubAPI
    private let api: AgentsAPI
    private let cache: YourAgentCache
    private let hubID: () -> String?
    private let canRead: () -> Bool
    private let networkAvailable: (() -> Bool)?
    private var checkTask: Task<Void, Never>?
    private var cooldownTask: Task<Void, Never>?
    private var expiryTask: Task<Void, Never>?
    private var sentTask: Task<Void, Never>?
    private(set) var verificationTask: Task<Void, Never>?
    private var generation = UUID()
    private var sessionGeneration = UUID()
    private var monitor: NWPathMonitor?

    init(hub: YourAgentHubAPI, api: AgentsAPI = AgentsClient(), cache: YourAgentCache,
         hubID: @escaping () -> String?, canRead: @escaping () -> Bool,
         monitorNetwork: Bool = true, networkAvailable: (() -> Bool)? = nil) {
        self.hub = hub; self.api = api; self.cache = cache
        self.hubID = hubID; self.canRead = canRead; self.networkAvailable = networkAvailable
        if monitorNetwork {
            let monitor = NWPathMonitor()
            self.monitor = monitor
            monitor.pathUpdateHandler = { [weak self] path in
                let satisfied = path.status == .satisfied
                Task { @MainActor [weak self] in self?.setReachable(satisfied) }
            }
            monitor.start(queue: DispatchQueue(label: "your-agent.reachability"))
        }
    }

    deinit {
        monitor?.cancel(); checkTask?.cancel(); cooldownTask?.cancel()
        expiryTask?.cancel(); sentTask?.cancel(); verificationTask?.cancel()
    }

    var canReserve: Bool {
        YourAgentRules.validName(YourAgentRules.normalize(name)) && YourAgentRules.validEmail(YourAgentRules.normalize(email))
            && copy?.hub == hubID() && !(copy?.token.isEmpty ?? true) && reachable && !inFlight
            && status != .notAllowed && !isTaken && retryAt == nil
    }
    var isStillLearning: Bool { copy?.stillLearning == true }
    private var isTaken: Bool { if case .taken = status { return true }; return false }

    var step: Step {
        guard let reservation else { return .form }
        switch reservation.state {
        case .choice: return .choice
        case .verified: return .yours
        case .pending, .unconfirmed: return reservation.codeIsOut ? .code : .confirm
        }
    }

    func open() async {
        setReachable(networkAvailable?() ?? monitor.map { $0.currentPath.status == .satisfied } ?? true)
        guard let id = hubID() else { return }
        let session = sessionGeneration
        if let cached = try? cache.load(hubID: id) { apply(cached) }
        else if let reservation { setReservation(reservation, persist: false) }
        let hadCredentials = !(copy?.token.isEmpty ?? true)
        async let refresh: Void = refreshCopy(id: id, session: session)
        if hadCredentials { await refreshReservation(id: id, session: session) }
        await refresh
        if !hadCredentials, copy != nil { await refreshReservation(id: id, session: session) }
    }

    private func refreshCopy(id: String, session: UUID) async {
        guard canRead() else { return }
        do {
            var value = try await HubRequestScope.$hubID.withValue(id) { try await hub.yourAgent() }
            guard hubID() == id, sessionGeneration == session, value.hub == id, canRead() else { return }
            if copy != nil { value.reservation = reservation }
            apply(value)
            try? cache.save(value)
        } catch { }
    }

    private func refreshReservation(id: String, session: UUID) async {
        guard !ranOut, let copy, !copy.token.isEmpty else { return }
        let held = reservation
        do {
            let value = try await api.reservation(copy: copy)
            guard hubID() == id, sessionGeneration == session, reservation == held else { return }
            if let held, held.state != .unconfirmed, held.holdsName,
               let expiry = held.expiryDate, expiry <= Date() { lapse(held); return }
            if value == nil, let held, held.holdsName { lapse(held); return }
            setReservation(value)
        } catch {
            guard hubID() == id, sessionGeneration == session else { return }
            if error as? AgentsError == .unreachable { setReachable(false) }
            else if let error = error as? AgentsError { applyCooldown(error) }
        }
    }

    func close() {
        expiryTask?.cancel()
        expiryTask = nil
    }

    func apply(_ value: YourAgentCopy) {
        guard value.hub == hubID() else { return }
        copy = value
        setReservation(value.reservation, persist: false)
    }

    func setReachable(_ value: Bool) {
        reachable = value
    }

    /// Sets the request the page shows, and starts the timer that lets the hold go when expiresAt passes.
    private func setReservation(_ value: AgentReservation?, persist shouldPersist: Bool = true) {
        reservation = value
        expiryTask?.cancel()
        expiryTask = nil
        if let value, value.holdsName, let date = value.expiryDate {
            expiryTask = Task { [weak self] in
                do { try await Task.sleep(for: .seconds(max(0, date.timeIntervalSinceNow))) }
                catch { return }
                self?.expire(value)
            }
        }
        if shouldPersist {
            copy?.reservation = value
            persist()
        }
    }

    private func expire(_ held: AgentReservation) {
        guard reservation == held else { return }
        if held.state == .unconfirmed {
            // The code ran out, but the earlier request keeps its name: back to "Confirm it's you".
            var confirm = held
            confirm.expiresAt = nil
            codeStatus = nil; code = ""
            setReservation(confirm)
        } else {
            lapse(held)
        }
    }

    /// The hold ran out and the name was let go. The form comes back filled in, and the name is checked again.
    private func lapse(_ held: AgentReservation) {
        setReservation(nil)
        codeStatus = nil; code = ""; sent = false; sentTask?.cancel()
        name = held.name; email = held.email
        ranOut = true
        nameDidLoseFocus(force: true)
    }

    /// An earlier request whose name someone else confirmed: the name is let go and the form comes back with a Taken line.
    private func letGo(_ held: AgentReservation, suggestion: String?) {
        setReservation(nil)
        codeStatus = nil; code = ""
        name = held.name; email = held.email
        status = .taken(suggestion)
    }

    private func clearNameCheck() {
        checkTask?.cancel()
        generation = UUID()
        status = .clear
    }

    func nameDidLoseFocus(force: Bool = false) {
        guard (force || !inFlight), reservation == nil else { return }
        clearNameCheck()
        let ticket = generation
        validateName()
        let candidate = YourAgentRules.normalize(name)
        guard YourAgentRules.validName(candidate), let copy, !copy.token.isEmpty, reachable else { return }
        let id = hubID()
        checkTask = Task { [weak self, api] in
            do {
                let result = try await api.check(name: candidate, copy: copy)
                guard let self, self.generation == ticket, self.hubID() == id else { return }
                if result.available { self.status = .clear }
                else if result.reason == "taken" { self.status = .taken(result.suggestion) }
                else { self.status = .notAllowed }
            } catch {
                guard let self, self.generation == ticket, self.hubID() == id else { return }
                if error as? AgentsError == .unreachable { self.setReachable(false) }
                else if let error = error as? AgentsError { self.applyCooldown(error) }
            }
        }
    }

    func useSuggestion() {
        if case .taken(let suggestion) = status, let suggestion { name = suggestion; nameDidLoseFocus() }
    }
    func emailDidLoseFocus() {
        emailInvalid = !email.isEmpty && !YourAgentRules.validEmail(YourAgentRules.normalize(email))
    }

    private func validateName() {
        if YourAgentRules.needsNameRules(name) { status = .rules }
        else if YourAgentRules.reserved.contains(name) { status = .notAllowed }
    }

    private func applyCooldown(_ error: AgentsError) {
        let date: Date
        switch error {
        case .rate: date = Date(timeIntervalSince1970: (floor(Date().timeIntervalSince1970 / 3600) + 1) * 3600)
        case .rateUntil(let value): date = value
        default: return
        }
        cooldownTask?.cancel()
        guard date > Date() else { retryAt = nil; return }
        retryAt = date
        cooldownTask = Task { [weak self] in
            do { try await Task.sleep(for: .seconds(max(0, date.timeIntervalSinceNow))) }
            catch { return }
            self?.retryAt = nil
        }
    }

    /// Shows "Sent" on the resend link for 1.6 s.
    private func flashSent() {
        sentTask?.cancel()
        sent = true
        sentTask = Task { [weak self] in
            do { try await Task.sleep(for: .milliseconds(1600)) } catch { return }
            self?.sent = false
        }
    }

    /// Runs a call with the device's credentials. A rejected token is refreshed once, as Send request does.
    private func withCredentials<T>(_ operation: (YourAgentCopy) async throws -> T) async throws -> T {
        guard let copy else { throw AgentsError.failed }
        do { return try await operation(copy) }
        catch let error as AgentsError where error == .token && reachable {
            let session = sessionGeneration
            await refreshCopy(id: copy.hub, session: session)
            guard session == sessionGeneration, let current = self.copy else { throw AgentsError.token }
            return try await operation(current)
        }
    }

    /// Records a failure from a call that only changes a line on the card.
    private func absorb(_ error: Error) {
        guard let error = error as? AgentsError else { status = .failed; return }
        switch error {
        case .unreachable: setReachable(false)
        case .rate, .rateUntil: applyCooldown(error)
        default: status = .failed
        }
    }

    func reserve() async {
        validateName()
        guard canReserve, let copy else { return }
        checkTask?.cancel(); generation = UUID()
        ranOut = false
        inFlight = true
        let session = sessionGeneration
        defer { if session == sessionGeneration { inFlight = false } }
        do {
            let requestName = name
            let requestEmail = email
            let value: AgentReservation
            do {
                value = try await api.reserve(name: requestName, email: requestEmail, copy: copy)
            } catch {
                guard session == sessionGeneration, hubID() == copy.hub else { return }
                switch error as? AgentsError {
                case .token:
                    guard reachable else { throw error }
                    await refreshCopy(id: copy.hub, session: session)
                case .failed: break
                default: throw error
                }
                guard session == sessionGeneration, hubID() == copy.hub, let current = self.copy else { return }
                value = try await api.reserve(name: requestName, email: requestEmail, copy: current)
            }
            guard session == sessionGeneration, hubID() == copy.hub else { return }
            codeStatus = nil; code = ""
            setReservation(value); status = .clear; emailInvalid = false
        } catch {
            guard session == sessionGeneration, hubID() == copy.hub else { return }
            switch error as? AgentsError {
            case .taken(let suggestion): status = .taken(suggestion)
            case .notAllowed, .invalidName: status = .notAllowed
            case .invalidEmail: emailInvalid = true
            case .unreachable: setReachable(false)
            case .rate, .rateUntil:
                if let error = error as? AgentsError { applyCooldown(error) }
                status = .clear
            default: status = .failed
            }
        }
    }

    /// Enters the sixth digit's check. Boxes dim while it runs; a wrong code clears them.
    func verify() async {
        guard let held = reservation, held.codeIsOut, code.count == 6, !verifying, !inFlight, retryAt == nil else { return }
        let entered = code
        let session = sessionGeneration
        codeStatus = nil
        verifying = true
        defer { if session == sessionGeneration { verifying = false } }
        do {
            let value = try await withCredentials { try await api.verify(code: entered, copy: $0) }
            guard session == sessionGeneration, reservation == held else { return }
            code = ""
            status = .clear
            setReservation(value)
        } catch {
            guard session == sessionGeneration, reservation == held else { return }
            switch error as? AgentsError {
            case .wrongCode?: code = ""; codeStatus = .wrong
            case .expired?: code = ""; codeStatus = .expired
            case .taken(let suggestion)?: letGo(held, suggestion: suggestion)
            case .rate?, .rateUntil?: code = ""; absorb(error)
            case .unreachable?: setReachable(false)
            default: code = ""; status = .failed
            }
        }
    }

    /// "Send it again" on the code step, and "Send code" on an earlier request. A 404 means the hold ran out, so the request is sent again.
    func sendCode() async {
        guard let held = reservation, held.state == .pending || held.state == .unconfirmed,
              !inFlight, !verifying, retryAt == nil else { return }
        inFlight = true
        let session = sessionGeneration
        defer { if session == sessionGeneration { inFlight = false } }
        do {
            let value = try await withCredentials { try await api.resend(copy: $0) }
            guard session == sessionGeneration, reservation == held else { return }
            code = ""; codeStatus = nil; status = .clear
            setReservation(value)
            flashSent()
        } catch AgentsError.none {
            guard session == sessionGeneration, reservation == held else { return }
            inFlight = false
            setReservation(nil)
            name = held.name; email = held.email
            await reserve()
            if reservation?.codeIsOut == true { flashSent() }
        } catch {
            guard session == sessionGeneration, reservation == held else { return }
            switch error as? AgentsError {
            case .taken(let suggestion)?: letGo(held, suggestion: suggestion)
            default: absorb(error)
            }
        }
    }

    /// "Change the name or email" on the code step: lets the hold go and brings the form back with both fields filled in.
    func cancelRequest() async {
        guard let held = reservation, held.codeIsOut, !inFlight, !verifying else { return }
        inFlight = true
        let session = sessionGeneration
        defer { if session == sessionGeneration { inFlight = false } }
        do {
            let remaining = try await withCredentials { try await api.cancel(copy: $0) }
            guard session == sessionGeneration, reservation == held else { return }
            setReservation(nil, persist: false)
            copy?.reservation = remaining
            persist()
            codeStatus = nil; code = ""; ranOut = false
            name = held.name; email = held.email
        } catch {
            guard session == sessionGeneration, reservation == held else { return }
            absorb(error)
        }
    }

    /// "Keep" or "Switch" on the choice card.
    func choose(keep: Bool) async {
        guard let held = reservation, held.state == .choice, !inFlight else { return }
        inFlight = true
        let session = sessionGeneration
        defer { if session == sessionGeneration { inFlight = false } }
        do {
            let value = try await withCredentials { try await api.choose(keep: keep, copy: $0) }
            guard session == sessionGeneration, reservation == held else { return }
            status = .clear
            setReservation(value)
        } catch {
            guard session == sessionGeneration, reservation == held else { return }
            if error as? AgentsError == .expired {
                inFlight = false
                lapse(held)
            } else {
                absorb(error)
            }
        }
    }

    /// "Change" on the verified and earlier-request cards: fills the form from the request, without touching the saved copy.
    func change() {
        guard let reservation else { return }
        name = reservation.name; email = reservation.email
        setReservation(nil, persist: false)
    }
    private func persist() { if let copy { try? cache.save(copy) } }

    @discardableResult
    func clearAfterUnpair() -> Bool {
        sessionGeneration = UUID(); generation = UUID(); checkTask?.cancel(); cooldownTask?.cancel(); retryAt = nil
        copy = nil; name = ""; email = ""; status = .clear; inFlight = false
        setReservation(nil, persist: false)
        code = ""; codeStatus = nil; verifying = false; ranOut = false; sent = false; sentTask?.cancel()
        verificationTask?.cancel()
        do { try cache.clear(); return true } catch { return false }
    }
}
