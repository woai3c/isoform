import Combine
import Foundation

struct KeepAwakeSetting: Codable, Equatable, Sendable {
    let on: Bool
    let pairedPhones: Int
    let onPower: Bool
}

protocol KeepAwakeAPI: Sendable {
    func keepAwakeStatus() async throws -> HubStatus
    func keepAwake() async throws -> KeepAwakeSetting
    func setKeepAwake(_ on: Bool) async throws -> KeepAwakeSetting
}

extension HubChannel: KeepAwakeAPI {
    func keepAwakeStatus() async throws -> HubStatus { try await status() }

    func keepAwake() async throws -> KeepAwakeSetting {
        try await perform(.keepAwake).payload(KeepAwakeSetting.self)
    }

    func setKeepAwake(_ on: Bool) async throws -> KeepAwakeSetting {
        struct Request: Encodable { let on: Bool }
        return try await post("/settings/keep-awake", Request(on: on), as: KeepAwakeSetting.self)
    }
}

/// A setting shared with the Mac. Offline changes are persisted before they are shown.
@MainActor
final class KeepAwakeStore: ObservableObject {
    @Published private(set) var on: Bool?
    @Published private(set) var pending = false
    @Published private(set) var saveFailed = false
    @Published private(set) var instantPush = false
    @Published private(set) var capabilitiesKnown = false
    @Published private(set) var briefSchedule: [ScheduledPhoneBrief] = []
    private let hub: KeepAwakeAPI
    private let journal: PendingWriteJournal
    private let identity: @MainActor () -> TodayPairingIdentity?
    private let defaults: UserDefaults
    private static let cacheKey = "phone.keepAwake.capabilities"
    private var draining = false
    private var generation = UUID()

    init(hub: KeepAwakeAPI, journal: PendingWriteJournal,
         identity: @escaping @MainActor () -> TodayPairingIdentity?, defaults: UserDefaults = .standard) {
        self.hub = hub
        self.journal = journal
        self.identity = identity
        self.defaults = defaults
        if let pairing = identity(), let data = defaults.data(forKey: Self.cacheKey),
           let saved = try? JSONDecoder().decode(SavedCapabilities.self, from: data), saved.pairing == pairing {
            on = saved.on
            instantPush = saved.instantPush
            briefSchedule = saved.briefSchedule
            capabilitiesKnown = saved.capabilitiesKnown ?? false
        }
    }

    private struct SavedCapabilities: Codable {
        let pairing: TodayPairingIdentity
        let on: Bool?
        let instantPush: Bool
        let briefSchedule: [ScheduledPhoneBrief]
        let capabilitiesKnown: Bool?
    }

    private func save() {
        guard let pairing = identity(), let data = try? JSONEncoder().encode(
            SavedCapabilities(pairing: pairing, on: on, instantPush: instantPush, briefSchedule: briefSchedule,
                              capabilitiesKnown: capabilitiesKnown)) else { return }
        defaults.set(data, forKey: Self.cacheKey)
    }

    func accept(_ snapshot: MobileTodaySnapshot) {
        if !pending, let value = snapshot.keepAwake { on = value }
        if let value = snapshot.instantPush { instantPush = value; capabilitiesKnown = true }
        if let value = snapshot.briefSchedule { briefSchedule = value }
        save()
    }

    func accept(_ status: HubStatus) {
        if !pending { on = status.keepAwake }
        instantPush = status.instantPush ?? false
        capabilitiesKnown = true
        briefSchedule = status.briefSchedule ?? []
        save()
    }

    func refresh() async {
        guard let pairing = identity() else { clear(); return }
        await replay()
        let current = generation
        if let status = try? await hub.keepAwakeStatus(), generation == current, identity() == pairing {
            accept(status)
        }
        do {
            let result = try await hub.keepAwake()
            guard generation == current, identity() == pairing, !pending else { return }
            on = result.on
            save()
        } catch {
            // An older Mac may not have the route. Keep a previously known value.
        }
    }

    func set(_ value: Bool) async {
        guard let pairing = identity(), on != nil else { saveFailed = true; return }
        saveFailed = false
        let key = UUID().uuidString
        do {
            _ = try await journal.reserve(operation: .keepAwake, target: pairing.deviceID,
                                          choice: value ? "on" : "off", fingerprint: key,
                                          makeKey: { key }, createdAt: Date())
        } catch { saveFailed = true; return }
        guard identity() == pairing else { return }
        on = value
        pending = true
        await replay()
    }

    func replay() async {
        guard !draining, let pairing = identity() else { return }
        draining = true
        defer { draining = false }
        let current = generation
        let entries = await journal.entries().filter { $0.operation == .keepAwake && $0.target == pairing.deviceID }
            .sorted { $0.createdAt < $1.createdAt }
        if let last = entries.last {
            on = last.choice == "on"
            pending = true
        }
        var next = entries.first
        while let entry = next {
            guard generation == current, identity() == pairing else { return }
            do {
                let result = try await HubRequestScope.$hubID.withValue(entry.hubID) {
                    try await hub.setKeepAwake(entry.choice == "on")
                }
                guard generation == current, identity() == pairing else { return }
                await journal.remove(keys: [entry.key])
                on = result.on
            } catch { break }
            next = await journal.entries().filter { $0.operation == .keepAwake && $0.target == pairing.deviceID }
                .sorted { $0.createdAt < $1.createdAt }.first
        }
        guard generation == current, identity() == pairing else { return }
        let remaining = await journal.entries().filter { $0.operation == .keepAwake && $0.target == pairing.deviceID }
            .sorted { $0.createdAt < $1.createdAt }
        pending = !remaining.isEmpty
        if let last = remaining.last { on = last.choice == "on" }
        if !pending { save() }
    }

    func clear() {
        generation = UUID()
        on = nil
        pending = false
        saveFailed = false
        instantPush = false
        capabilitiesKnown = false
        briefSchedule = []
        defaults.removeObject(forKey: Self.cacheKey)
    }
}
