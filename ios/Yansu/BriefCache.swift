import CryptoKit
import Foundation

// The brief library on this iPhone (design section 6). One entry per brief id, holding the brief's whole
// package exactly as the Mac served it, the version that names those bytes, and the audio receipt. A new
// version replaces the entry as a whole, so the phone never mixes parts of two versions. Files are complete
// until first unlock, are named by a hash of the brief id (so an id can never escape the folder), and text
// older than 30 days is removed on every write. Audio keeps its own shorter interval (BriefAudioCache).
struct CachedBrief: Codable, Equatable, Sendable {
    var brief: MobileBriefV1
    var relayVersion: UInt64?
    /// When this phone received the brief.
    var storedAt: Date
    var audioReceipt: BriefAudioReceipt? = nil
    /// The Mac's version: the SHA-256 hex of `package` for a package saved from the Mac since 1.0, or the
    /// Today bodyVersion for a brief from a Mac before 1.0. Nil for an entry saved before versions existed, so
    /// it is fetched again when Today names a version.
    var version: String? = nil
    /// The package bytes the version was computed from. Nil for briefs from a Mac before 1.0.
    var package: Data? = nil
}

/// Why a package was not saved. The sync path retries; nothing is shown to the person.
enum BriefPackageError: Error, Equatable {
    /// The bytes do not hash to the version the Mac named.
    case versionMismatch
    /// The bytes are not a brief this phone can read.
    case unreadable
    /// The package is a different brief from the one that was asked for.
    case wrongBrief
}

final class BriefCache: @unchecked Sendable {
    /// How long the full text of a brief stays on this phone.
    static let textRetentionInterval: TimeInterval = 30 * 24 * 60 * 60

    private let directory: URL
    private let now: () -> Date
    private let removeItem: (URL) throws -> Void
    private let lock = NSLock()
    private var removalPending = false

    init(directory: URL, now: @escaping () -> Date = { Date() },
         removeItem: @escaping (URL) throws -> Void = { try FileManager.default.removeItem(at: $0) }) {
        self.directory = directory
        self.now = now
        self.removeItem = removeItem
    }

    // The App Group container, so a notification extension or widget can read the cache later. Without an
    // App Group container the cache stays in Application Support.
    static var defaultDirectory: URL {
        directory(in: AppGroup.containerURL ?? applicationSupport)
    }

    static var legacyDirectory: URL {
        directory(in: applicationSupport)
    }

    private static var applicationSupport: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
    }

    private static func directory(in root: URL) -> URL {
        root.appendingPathComponent("Isoform", isDirectory: true).appendingPathComponent("briefs", isDirectory: true)
    }

    private static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        encoder.outputFormatting = [.sortedKeys]
        return encoder
    }()

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }()

    /// The lowercase hex SHA-256 of the bytes: the version of a package (PROTOCOL.md 8.1).
    static func sha256Hex(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    /// Whether the text is a version the Mac can name: 64 lowercase hex digits.
    static func isVersion(_ value: String) -> Bool {
        value.utf8.count == 64 && value.utf8.allSatisfy { (48...57).contains($0) || (97...102).contains($0) }
    }

    /// The one current-brief rule (design section 1): the newest brief whose full text is on this phone. A
    /// brief whose text has not fully arrived, or that the Mac withdrew, is never current.
    static func current(in entries: [CachedBrief]) -> CachedBrief? {
        entries.filter { isCurrentCandidate($0.brief) }.reduce(nil) { best, candidate in
            guard let best else { return candidate }
            return (candidate.brief.asOfDate ?? .distantPast) > (best.brief.asOfDate ?? .distantPast) ? candidate : best
        }
    }

    static func isCurrentCandidate(_ brief: MobileBriefV1) -> Bool {
        guard brief.narrativeStatus == nil || brief.narrativeStatus == "ok" else { return false }
        return brief.yourWeek != nil || brief.narrative.contains {
            !($0.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
    }

    private func locked<T>(_ body: () throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try body()
    }

    private func fileURL(for identifier: String) -> URL {
        let digest = SHA256.hash(data: Data(identifier.utf8)).map { String(format: "%02x", $0) }.joined()
        return directory.appendingPathComponent(digest + ".json")
    }

    /// Saves a brief's whole package. The bytes must hash to `version`, the version the Mac named, and must
    /// be the brief `identifier` when one is given. Nothing is written when either check fails.
    @discardableResult
    func save(package: Data, version: String, identifier: String? = nil, relayVersion: UInt64? = nil,
              audioReceipt: BriefAudioReceipt? = nil) throws -> MobileBriefV1 {
        guard Self.isVersion(version), Self.sha256Hex(package) == version else { throw BriefPackageError.versionMismatch }
        let brief: MobileBriefV1
        do {
            brief = try JSONDecoder().decode(MobileBriefV1.self, from: package)
        } catch {
            throw BriefPackageError.unreadable
        }
        if let identifier, brief.identifier != identifier { throw BriefPackageError.wrongBrief }
        try locked {
            try writeLocked(brief, relayVersion: relayVersion, audioReceipt: audioReceipt, version: version, package: package)
        }
        return brief
    }

    /// Saves a brief without package bytes: a brief from a Mac before 1.0, or a brief whose text is already
    /// stored and whose receipt changes. An identical brief keeps the version and package it was saved with.
    @discardableResult
    func save(_ brief: MobileBriefV1, relayVersion: UInt64? = nil, audioReceipt: BriefAudioReceipt? = nil,
              version: String? = nil) throws -> MobileBriefV1 {
        try locked {
            try writeLocked(brief, relayVersion: relayVersion, audioReceipt: audioReceipt, version: version, package: nil)
        }
        return brief
    }

    func load(identifier: String) -> CachedBrief? {
        locked {
            guard (try? finishPendingRemovalLocked()) != nil else { return nil }
            guard let data = try? Data(contentsOf: fileURL(for: identifier)) else { return nil }
            return try? Self.decoder.decode(CachedBrief.self, from: data)
        }
    }

    // Newest first, by the time the Mac says the brief was written.
    func all() -> [CachedBrief] {
        locked {
            guard (try? finishPendingRemovalLocked()) != nil else { return [] }
            return entriesLocked().sorted { Self.sortDate($0) > Self.sortDate($1) }
        }
    }

    /// The current brief under the one rule (`current(in:)`).
    func current() -> CachedBrief? {
        Self.current(in: all())
    }

    func prune() throws {
        try locked {
            try finishPendingRemovalLocked()
            try pruneLocked()
        }
    }

    func removeAll() throws {
        try locked {
            removalPending = true
            try finishPendingRemovalLocked()
        }
    }

    func remove(identifier: String) throws {
        try locked {
            try finishPendingRemovalLocked()
            let url = fileURL(for: identifier)
            if FileManager.default.fileExists(atPath: url.path) { try removeItem(url) }
        }
    }

    // MARK: Helpers (callers hold the lock)

    private func writeLocked(_ brief: MobileBriefV1, relayVersion: UInt64?, audioReceipt: BriefAudioReceipt?,
                             version: String?, package: Data?) throws {
        try finishPendingRemovalLocked()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let prior = (try? Data(contentsOf: fileURL(for: brief.identifier)))
            .flatMap { try? Self.decoder.decode(CachedBrief.self, from: $0) }
        let same = prior?.brief == brief
        // Without new bytes, an identical brief keeps the version and package it was stored with.
        let keepsPackage = same && package == nil && version == nil
        let entry = CachedBrief(brief: brief, relayVersion: relayVersion ?? (same ? prior?.relayVersion : nil),
                                storedAt: now(), audioReceipt: audioReceipt ?? (same ? prior?.audioReceipt : nil),
                                version: version ?? (keepsPackage ? prior?.version : nil),
                                package: package ?? (keepsPackage ? prior?.package : nil))
        let data = try Self.encoder.encode(entry)
        try data.write(to: fileURL(for: brief.identifier),
                       options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        try pruneLocked()
    }

    // If clearing another Mac's cache fails, it must remain unreadable until deletion succeeds.
    private func finishPendingRemovalLocked() throws {
        guard removalPending else { return }
        if FileManager.default.fileExists(atPath: directory.path) {
            try removeItem(directory)
        }
        removalPending = false
    }

    private static func sortDate(_ entry: CachedBrief) -> Date {
        entry.brief.asOfDate ?? entry.storedAt
    }

    private func entriesLocked() -> [CachedBrief] {
        let files = (try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)) ?? []
        return files.filter { $0.pathExtension == "json" }.compactMap { url in
            guard let data = try? Data(contentsOf: url) else { return nil }
            return try? Self.decoder.decode(CachedBrief.self, from: data)
        }
    }

    private func pruneLocked() throws {
        let cutoff = now().addingTimeInterval(-Self.textRetentionInterval)
        let files = (try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)) ?? []
        for url in files where url.pathExtension == "json" {
            guard let data = try? Data(contentsOf: url),
                  let entry = try? Self.decoder.decode(CachedBrief.self, from: data) else {
                try? FileManager.default.removeItem(at: url)
                continue
            }
            if Self.sortDate(entry) < cutoff {
                try FileManager.default.removeItem(at: url)
            }
        }
    }
}

// Moves brief files from the Application Support folder into the App Group container, once. A file already
// in the destination keeps its copy, and the old folder is removed when it is empty.
enum BriefCacheMigration {
    static func moveLegacyFiles(from legacy: URL, to destination: URL,
                                fileManager: FileManager = .default) {
        guard legacy.standardizedFileURL != destination.standardizedFileURL,
              fileManager.fileExists(atPath: legacy.path) else { return }
        guard (try? fileManager.createDirectory(at: destination, withIntermediateDirectories: true)) != nil else { return }
        let files = (try? fileManager.contentsOfDirectory(at: legacy, includingPropertiesForKeys: nil)) ?? []
        for file in files where file.pathExtension == "json" {
            let target = destination.appendingPathComponent(file.lastPathComponent)
            if fileManager.fileExists(atPath: target.path) {
                try? fileManager.removeItem(at: file)
            } else {
                try? fileManager.moveItem(at: file, to: target)
            }
        }
        if ((try? fileManager.contentsOfDirectory(atPath: legacy.path)) ?? []).isEmpty {
            try? fileManager.removeItem(at: legacy)
        }
    }
}
