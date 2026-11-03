import Foundation
import SwiftUI
import WidgetKit
import XCTest
@testable import Yansu

@MainActor
final class WidgetSnapshotTests: XCTestCase {
    func testTimelineRedrawsAtEventAndMidnight() throws {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = try XCTUnwrap(TimeZone(secondsFromGMT: 0))
        let now = try XCTUnwrap(calendar.date(from: DateComponents(year: 2026, month: 10, day: 31, hour: 9)))
        let event = now.addingTimeInterval(3600)
        let midnight = try XCTUnwrap(calendar.date(from: DateComponents(year: 2026, month: 11, day: 1)))
        XCTAssertEqual(WidgetSnapshot.timelineDates(now: now, event: event, calendar: calendar), [now, event, midnight])
        XCTAssertEqual(WidgetSnapshot.timelineDates(now: now, event: now.addingTimeInterval(-1), calendar: calendar), [now, midnight])
        XCTAssertEqual(WidgetSnapshot.timelineDates(now: now, event: midnight, calendar: calendar), [now, midnight])
    }
    // Whole seconds, because the store writes ISO-8601 without fractional seconds.
    private let instant = Date(timeIntervalSince1970: 1_790_000_000)

    private final class ReloadCounter {
        var count = 0
    }

    private func makeStore() throws -> WidgetSnapshotStore {
        let root = try makeTemporaryDirectory()
        return WidgetSnapshotStore(fileURL: root.appendingPathComponent("nested/widget-snapshot.json"))
    }

    private func makeBrief(hubName: String? = "Alex's MacBook Pro",
                           asOf: Date? = Date(timeIntervalSince1970: 1_790_000_000)) -> MobileBriefV1 {
        var brief = MobileBriefV1()
        brief.briefId = "2026-09-21-morning"
        brief.hubName = hubName
        brief.asOf = asOf.map { ISO8601DateFormatter().string(from: $0) }
        return brief
    }

    private func makePublisher() throws -> (WidgetSnapshotPublisher, WidgetSnapshotStore, ReloadCounter) {
        let store = try makeStore()
        let counter = ReloadCounter()
        let publisher = WidgetSnapshotPublisher(store: store, reloadTimelines: { counter.count += 1 })
        return (publisher, store, counter)
    }

    // MARK: - Brief time

    func testABriefFromTodayShowsItsTimeAlone() {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .current
        let morning = calendar.startOfDay(for: instant).addingTimeInterval(8 * 60 * 60)
        let evening = morning.addingTimeInterval(10 * 60 * 60)
        XCTAssertTrue(WidgetSnapshot.briefIsFromToday(morning, now: evening, calendar: calendar))
        XCTAssertEqual(WidgetSnapshot.briefTimeText(morning, now: evening, calendar: calendar),
                       morning.formatted(date: .omitted, time: .shortened))
    }

    func testAnOlderBriefShowsItsDateSoItNeverReadsAsToday() {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .current
        let friday = calendar.startOfDay(for: instant).addingTimeInterval(8 * 60 * 60)
        let monday = friday.addingTimeInterval(3 * 24 * 60 * 60)
        XCTAssertFalse(WidgetSnapshot.briefIsFromToday(friday, now: monday, calendar: calendar))
        let text = WidgetSnapshot.briefTimeText(friday, now: monday, calendar: calendar)
        XCTAssertEqual(text, friday.formatted(date: .abbreviated, time: .shortened))
        XCTAssertNotEqual(text, friday.formatted(date: .omitted, time: .shortened))
    }

    // MARK: - Encoding

    func testEncodeThenDecodeKeepsEveryField() throws {
        let original = WidgetSnapshot(generatedAt: instant, hubName: "Test Mac",
                                      lastBriefAt: instant.addingTimeInterval(-3_600))

        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(WidgetSnapshot.self, from: data)

        XCTAssertEqual(decoded, original)
    }

    func testTheVersionIsThree() {
        XCTAssertEqual(WidgetSnapshot.currentVersion, 3)
    }

    func testDecodingAVersionThisBuildDoesNotKnowFails() throws {
        let newer = WidgetSnapshot(version: 4, generatedAt: instant)
        let data = try JSONEncoder().encode(newer)

        XCTAssertThrowsError(try JSONDecoder().decode(WidgetSnapshot.self, from: data))
    }

    func testAVersionOneFileFromTheDeskIsIgnoredNotMisread() throws {
        let store = try makeStore()
        try FileManager.default.createDirectory(at: store.fileURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let legacy = #"{"actingNowTitle":"Draft the notes","generatedAt":"2026-09-21T13:46:40Z","hubName":"Test Mac","lastBriefAt":"2026-09-21T12:46:40Z","needsYouCount":3,"version":1}"#
        try Data(legacy.utf8).write(to: store.fileURL)

        XCTAssertNil(store.read())
    }

    func testEmptySnapshotHasNothingToShow() {
        let empty = WidgetSnapshot.empty(now: instant)

        XCTAssertEqual(empty.version, WidgetSnapshot.currentVersion)
        XCTAssertEqual(empty.generatedAt, instant)
        XCTAssertNil(empty.hubName)
        XCTAssertNil(empty.lastBriefAt)
    }

    // MARK: - Store

    func testStoreKeepsEveryFieldThroughTheFile() throws {
        let store = try makeStore()
        let original = WidgetSnapshot(generatedAt: instant, hubName: "Test Mac",
                                      lastBriefAt: instant.addingTimeInterval(-600))

        try store.write(original)

        XCTAssertEqual(store.read(), original)
    }

    func testClearRemovesTheFileAndIsSafeWhenItIsMissing() throws {
        let store = try makeStore()
        try store.write(WidgetSnapshot(generatedAt: instant))

        try store.clear()

        XCTAssertNil(store.read())
        XCTAssertFalse(FileManager.default.fileExists(atPath: store.fileURL.path))
        XCTAssertNoThrow(try store.clear())
    }

    func testReadOfAMissingFileIsNil() throws {
        XCTAssertNil(try makeStore().read())
    }

    func testReadOfACorruptFileIsNil() throws {
        let store = try makeStore()
        try FileManager.default.createDirectory(at: store.fileURL.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        try Data("{not json".utf8).write(to: store.fileURL)

        XCTAssertNil(store.read())
    }

    func testReadOfAnUnknownVersionIsNil() throws {
        let store = try makeStore()
        try store.write(WidgetSnapshot(version: 4, generatedAt: instant, hubName: "Test Mac"))

        XCTAssertNil(store.read())
    }

    func testDefaultLocationIsTheYansuFolderInTheContainer() {
        let url = WidgetSnapshotStore.defaultURL

        XCTAssertEqual(url.lastPathComponent, "widget-snapshot.json")
        XCTAssertEqual(url.deletingLastPathComponent().lastPathComponent, "Isoform")
    }

    func testTimelineHidesYesterdayAtMidnight() throws {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(secondsFromGMT: 0)!
        let day = calendar.startOfDay(for: instant)
        var snapshot = WidgetSnapshot(generatedAt: day, lastBriefAt: day.addingTimeInterval(3600))
        snapshot.briefID = "today"
        let midnight = try XCTUnwrap(WidgetSnapshot.nextMidnight(after: day, calendar: calendar))
        XCTAssertTrue(snapshot.showsBrief(at: midnight.addingTimeInterval(-1), calendar: calendar))
        XCTAssertFalse(snapshot.showsBrief(at: midnight, calendar: calendar))
    }

    func testPrivateSnapshotNeverStoresBriefOrItemText() throws {
        let (publisher, store, _) = try makePublisher()
        var brief = makeBrief()
        brief.narrative = [BriefParagraph(text: "Private lead")]
        publisher.updateToday(nil, brief: brief, tasks: nil, privateContent: true, isPlaying: false, now: instant)
        let saved = try XCTUnwrap(store.read())
        XCTAssertTrue(saved.isPrivate)
        XCTAssertNil(saved.lead)
        XCTAssertNil(saved.firstItemTitle)
        XCTAssertNil(saved.nextEventTitle)
    }

    func testWidgetRendersBothSizesAndPrivacyInBothLanguages() throws {
        var snapshot = WidgetSnapshot(generatedAt: instant, lastBriefAt: instant)
        snapshot.briefID = "brief"
        snapshot.lead = "The launch plan is ready for review."
        snapshot.needsYouCount = 2
        snapshot.firstItemTitle = "Review the draft"
        snapshot.firstItemDetail = "From Mail"
        snapshot.firstItemID = "task-1"
        snapshot.firstItemKind = "task"
        snapshot.nextEventTitle = "Planning"
        snapshot.nextEventAt = instant.addingTimeInterval(3600)
        for (language, scheme) in [("en", ColorScheme.light), ("zh-Hans", .dark)] {
            for family in [WidgetFamily.systemSmall, .systemMedium] {
                for privateContent in [false, true] {
                    var value = snapshot
                    value.isPrivate = privateContent
                    if privateContent {
                        value.lead = nil; value.firstItemTitle = nil; value.firstItemDetail = nil
                        value.nextEventTitle = nil
                    }
                    let width: CGFloat = family == .systemSmall ? 158 : 338
                    let view = SnapshotWidgetView(entry: SnapshotEntry(date: instant, snapshot: value), previewFamily: family)
                        .padding(12)
                        .frame(width: width, height: 158)
                        .background(WidgetTheme.background)
                        .environment(\.locale, Locale(identifier: language))
                        .environment(\.colorScheme, scheme)
                    let renderer = ImageRenderer(content: view)
                    renderer.scale = 2
                    let png = try XCTUnwrap(renderer.uiImage?.pngData())
                    let attachment = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
                    attachment.name = "widget-\(family == .systemSmall ? "small" : "medium")-\(privateContent ? "private" : "ready")-\(language)"
                    attachment.lifetime = .keepAlways
                    add(attachment)
                }
            }
        }
    }

    // MARK: - Mapping from the brief

    func testFromBriefMapsTheTrimmedHubNameAndTheBriefTime() {
        let snapshot = WidgetSnapshot.fromBrief(makeBrief(hubName: "  Studio Mac  "), now: instant)

        XCTAssertEqual(snapshot.hubName, "Studio Mac")
        XCTAssertEqual(snapshot.lastBriefAt, instant)
        XCTAssertEqual(snapshot.generatedAt, instant)
        XCTAssertEqual(snapshot.version, WidgetSnapshot.currentVersion)
    }

    func testFromBriefLeavesABlankHubNameEmpty() {
        XCTAssertNil(WidgetSnapshot.fromBrief(makeBrief(hubName: "   "), now: instant).hubName)
        XCTAssertNil(WidgetSnapshot.fromBrief(makeBrief(hubName: nil), now: instant).hubName)
    }

    // MARK: - Publisher

    func testUpdateBriefWritesTheMacsNameAndTheBriefTime() throws {
        let (publisher, store, counter) = try makePublisher()

        publisher.updateBrief(makeBrief(hubName: "Studio Mac", asOf: instant.addingTimeInterval(-600)), now: instant)

        let stored = try XCTUnwrap(store.read())
        XCTAssertEqual(stored.hubName, "Studio Mac")
        XCTAssertEqual(stored.lastBriefAt, instant.addingTimeInterval(-600))
        XCTAssertEqual(stored.generatedAt, instant)
        XCTAssertEqual(counter.count, 1, "the first snapshot replaces the empty state")
    }

    func testABriefWithoutANameOrATimeKeepsTheStoredOnes() throws {
        let (publisher, store, _) = try makePublisher()
        publisher.updateBrief(makeBrief(hubName: "Studio Mac", asOf: instant.addingTimeInterval(-600)), now: instant)

        publisher.updateBrief(makeBrief(hubName: nil, asOf: nil), now: instant.addingTimeInterval(60))

        let stored = try XCTUnwrap(store.read())
        XCTAssertEqual(stored.hubName, "Studio Mac")
        XCTAssertEqual(stored.lastBriefAt, instant.addingTimeInterval(-600))
    }

    func testANewerBriefReplacesTheTime() throws {
        let (publisher, store, _) = try makePublisher()
        publisher.updateBrief(makeBrief(asOf: instant.addingTimeInterval(-600)), now: instant)

        publisher.updateBrief(makeBrief(asOf: instant), now: instant.addingTimeInterval(60))

        XCTAssertEqual(store.read()?.lastBriefAt, instant)
    }

    func testReloadsOnlyWhenTheContentChanges() throws {
        let (publisher, _, counter) = try makePublisher()

        publisher.updateBrief(makeBrief(asOf: instant), now: instant)
        XCTAssertEqual(counter.count, 1, "the first snapshot replaces the empty state")

        publisher.updateBrief(makeBrief(asOf: instant), now: instant.addingTimeInterval(60))
        XCTAssertEqual(counter.count, 1, "the same content with a new timestamp does not reload")

        publisher.updateBrief(makeBrief(asOf: instant.addingTimeInterval(3_600)), now: instant.addingTimeInterval(120))
        XCTAssertEqual(counter.count, 2)

        publisher.updateBrief(makeBrief(hubName: "Studio Mac", asOf: instant.addingTimeInterval(3_600)),
                              now: instant.addingTimeInterval(180))
        XCTAssertEqual(counter.count, 3)
    }

    func testClearRemovesTheSnapshotAndReloadsOnlyOnce() throws {
        let (publisher, store, counter) = try makePublisher()
        publisher.updateBrief(makeBrief(), now: instant)

        publisher.clear()

        XCTAssertNil(store.read())
        XCTAssertEqual(counter.count, 2)

        publisher.clear()
        XCTAssertEqual(counter.count, 2)
    }

    func testAFailedClearNeverMergesOldFieldsIntoANewBrief() throws {
        let store = try makeStore()
        defer { try? FileManager.default.removeItem(at: store.fileURL.deletingLastPathComponent()) }
        var blocked = false
        let publisher = WidgetSnapshotPublisher(store: store, reloadTimelines: {}, removeItem: { url in
            if blocked { throw YansuError.message("Injected removal failure") }
            try FileManager.default.removeItem(at: url)
        })
        publisher.updateBrief(makeBrief(hubName: "Old Mac", asOf: instant), now: instant)
        let old = store.read()
        blocked = true

        XCTAssertFalse(publisher.clear())
        publisher.updateBrief(makeBrief(hubName: nil, asOf: nil), now: instant.addingTimeInterval(60))
        XCTAssertEqual(store.read(), old, "a failed cleanup does not publish a new snapshot with stale fields")

        blocked = false
        publisher.updateBrief(makeBrief(hubName: nil, asOf: nil), now: instant.addingTimeInterval(120))
        let fresh = try XCTUnwrap(store.read())
        XCTAssertNil(fresh.hubName)
        XCTAssertNil(fresh.lastBriefAt)
        XCTAssertEqual(fresh.generatedAt, instant.addingTimeInterval(120))
    }

    func testAWriteFailureIsSwallowedAndDoesNotReload() throws {
        // A file where the snapshot folder should be makes every write fail.
        let root = try makeTemporaryDirectory()
        let blocker = root.appendingPathComponent("blocker")
        try Data("x".utf8).write(to: blocker)
        let counter = ReloadCounter()
        let publisher = WidgetSnapshotPublisher(
            store: WidgetSnapshotStore(fileURL: blocker.appendingPathComponent("widget-snapshot.json")),
            reloadTimelines: { counter.count += 1 }
        )

        publisher.updateBrief(makeBrief(), now: instant)

        XCTAssertEqual(counter.count, 0)
    }
}
