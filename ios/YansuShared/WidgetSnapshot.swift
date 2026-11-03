import Foundation

/// What the Home Screen widget shows: the Mac's name and when its last brief arrived. The app writes it whenever the
/// brief changes and the widget only reads it.
///
/// This file is compiled into the widget extension, so it must stay free of app-target types (no MobileBriefV1)
/// and free of networking. The conversion from the brief lives in the app target.
struct WidgetSnapshot: Codable, Equatable, Sendable {
    /// Version 2 dropped the Desk's needs-you count and acting-now title. A version 1 file is ignored, not misread,
    /// until the app writes the next brief.
    static let currentVersion = 3

    var version: Int
    var generatedAt: Date
    var hubName: String?
    var lastBriefAt: Date?
    var briefID: String? = nil
    var briefSlot: String? = nil
    var lead: String? = nil
    var needsYouCount: Int = 0
    var firstItemTitle: String? = nil
    var firstItemDetail: String? = nil
    var firstItemID: String? = nil
    var firstItemKind: String? = nil
    var nextEventTitle: String? = nil
    var nextEventAt: Date? = nil
    var morningTime: String? = nil
    var isPrivate: Bool = false
    var isPlaying: Bool = false

    init(version: Int = WidgetSnapshot.currentVersion,
         generatedAt: Date,
         hubName: String? = nil,
         lastBriefAt: Date? = nil) {
        self.version = version
        self.generatedAt = generatedAt
        self.hubName = hubName
        self.lastBriefAt = lastBriefAt
    }

    /// A snapshot with nothing in it, used where the widget needs a stand-in (placeholder and gallery).
    static func empty(now: Date) -> WidgetSnapshot {
        WidgetSnapshot(generatedAt: now)
    }

    /// True when the brief arrived on the day the widget is drawn, so its time alone says when.
    static func briefIsFromToday(_ briefAt: Date, now: Date, calendar: Calendar = .current) -> Bool {
        calendar.isDate(briefAt, inSameDayAs: now)
    }

    /// When the last brief arrived, the way the Brief's "As of" line says it: the time alone for a brief from today,
    /// otherwise its date and time, so Friday's brief never reads as today on Monday.
    static func briefTimeText(_ briefAt: Date, now: Date, calendar: Calendar = .current) -> String {
        if briefIsFromToday(briefAt, now: now, calendar: calendar) {
            return briefAt.formatted(date: .omitted, time: .shortened)
        }
        return briefAt.formatted(date: .abbreviated, time: .shortened)
    }

    /// Decoding refuses any version this build does not understand, so the widget never misreads an older or newer file.
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let version = try container.decode(Int.self, forKey: .version)
        guard version == Self.currentVersion || version == 2 else {
            throw DecodingError.dataCorruptedError(forKey: .version, in: container,
                                                   debugDescription: "Unsupported widget snapshot version \(version)")
        }
        self.version = version
        generatedAt = try container.decode(Date.self, forKey: .generatedAt)
        hubName = try container.decodeIfPresent(String.self, forKey: .hubName)
        lastBriefAt = try container.decodeIfPresent(Date.self, forKey: .lastBriefAt)
        briefID = try container.decodeIfPresent(String.self, forKey: .briefID)
        briefSlot = try container.decodeIfPresent(String.self, forKey: .briefSlot)
        lead = try container.decodeIfPresent(String.self, forKey: .lead)
        needsYouCount = try container.decodeIfPresent(Int.self, forKey: .needsYouCount) ?? 0
        firstItemTitle = try container.decodeIfPresent(String.self, forKey: .firstItemTitle)
        firstItemDetail = try container.decodeIfPresent(String.self, forKey: .firstItemDetail)
        firstItemID = try container.decodeIfPresent(String.self, forKey: .firstItemID)
        firstItemKind = try container.decodeIfPresent(String.self, forKey: .firstItemKind)
        nextEventTitle = try container.decodeIfPresent(String.self, forKey: .nextEventTitle)
        nextEventAt = try container.decodeIfPresent(Date.self, forKey: .nextEventAt)
        morningTime = try container.decodeIfPresent(String.self, forKey: .morningTime)
        isPrivate = try container.decodeIfPresent(Bool.self, forKey: .isPrivate) ?? true
        isPlaying = try container.decodeIfPresent(Bool.self, forKey: .isPlaying) ?? false
    }

    /// True when the widget would draw the same content. `generatedAt` is ignored so that a refresh with no
    /// change does not reload the widget timelines. Dates are compared to whole seconds because the file stores
    /// ISO-8601 without fractions.
    func hasSameContent(as other: WidgetSnapshot) -> Bool {
        hubName == other.hubName
            && Self.wholeSeconds(lastBriefAt) == Self.wholeSeconds(other.lastBriefAt)
            && briefID == other.briefID && briefSlot == other.briefSlot && lead == other.lead && needsYouCount == other.needsYouCount
            && firstItemTitle == other.firstItemTitle && firstItemDetail == other.firstItemDetail
            && firstItemID == other.firstItemID && firstItemKind == other.firstItemKind && nextEventTitle == other.nextEventTitle
            && Self.wholeSeconds(nextEventAt) == Self.wholeSeconds(other.nextEventAt)
            && morningTime == other.morningTime && isPrivate == other.isPrivate && isPlaying == other.isPlaying
    }

    /// The timeline never presents yesterday's brief as today's.
    func showsBrief(at date: Date, calendar: Calendar = .current) -> Bool {
        guard let lastBriefAt, briefID != nil else { return false }
        return calendar.isDate(lastBriefAt, inSameDayAs: date)
    }

    static func nextMidnight(after date: Date, calendar: Calendar = .current) -> Date? {
        calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: date))
    }

    /// Redraw when the displayed event begins and again at the day boundary.
    static func timelineDates(now: Date, event: Date?, calendar: Calendar = .current) -> [Date] {
        let midnight = nextMidnight(after: now, calendar: calendar) ?? now.addingTimeInterval(3600)
        var dates = [now]
        if let event, event > now, event < midnight { dates.append(event) }
        dates.append(midnight)
        return dates
    }

    static func morningTimeText(_ value: String?, now: Date, locale: Locale = .current,
                                calendar: Calendar = .current) -> String? {
        guard let value else { return nil }
        let parts = value.split(separator: ":")
        guard parts.count == 2, let hour = Int(parts[0]), let minute = Int(parts[1]),
              (0...23).contains(hour), (0...59).contains(minute),
              let date = calendar.date(bySettingHour: hour, minute: minute, second: 0, of: now) else { return nil }
        return date.formatted(Date.FormatStyle(date: .omitted, time: .shortened).locale(locale))
    }

    private static func wholeSeconds(_ date: Date?) -> Double? {
        date.map { $0.timeIntervalSince1970.rounded(.down) }
    }
}

/// Reads and writes the widget snapshot file in the App Group container. Never throws on read: a missing,
/// malformed or unsupported file is the same as no snapshot.
struct WidgetSnapshotStore: Sendable {
    let fileURL: URL

    init(fileURL: URL) {
        self.fileURL = fileURL
    }

    static var defaultURL: URL {
        let root = AppGroup.containerURL
            ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return root.appendingPathComponent("Isoform", isDirectory: true)
            .appendingPathComponent("widget-snapshot.json")
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

    func read() -> WidgetSnapshot? {
        guard let data = try? Data(contentsOf: fileURL) else { return nil }
        return try? Self.decoder.decode(WidgetSnapshot.self, from: data)
    }

    func write(_ snapshot: WidgetSnapshot) throws {
        try FileManager.default.createDirectory(at: fileURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let data = try Self.encoder.encode(snapshot)
        try data.write(to: fileURL, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    func clear() throws {
        guard FileManager.default.fileExists(atPath: fileURL.path) else { return }
        try FileManager.default.removeItem(at: fileURL)
    }
}
