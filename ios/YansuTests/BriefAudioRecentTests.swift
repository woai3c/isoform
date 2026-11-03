import CryptoKit
import XCTest
@testable import Yansu

// The last 7 days of briefs keep their audio on this phone, fetched when the brief arrives, and play offline from the
// cache (handoff 2026-10-28, section 6).
@MainActor
final class BriefAudioRecentTests: XCTestCase {
    private let hub = "11111111-2222-4333-8444-555555555555"
    private let now = RelayTime.parse("2026-10-28T12:00:00Z")!

    private func makeCache() throws -> BriefAudioCache {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        return BriefAudioCache(directory: directory)
    }

    private func brief(_ id: String, asOf: String) -> MobileBriefV1 {
        var value = MobileBriefV1(briefId: id, slot: "morning", localDate: "2026-10-28", asOf: asOf, kind: "morning",
                                  narrativeStatus: "ok", narrative: [BriefParagraph(text: "Three meetings today.")])
        value.audio = metadata
        return value
    }

    private let metadata = BriefAudio(mime: "audio/wav", bytes: 3, durationSeconds: 60)

    private func resource(_ selected: MobileBriefV1) -> BriefAudioResource {
        BriefAudioResource(briefId: selected.identifier, version: BriefAudioResource.versionKey(for: selected), audio: metadata)
    }

    func testRecentAudioFetchesEachBriefOfTheLastSevenDaysNewestFirst() async throws {
        let cache = try makeCache()
        let yesterday = brief("yesterday", asOf: "2026-10-27T06:50:00Z")
        let threeDays = brief("three-days", asOf: "2026-10-25T18:00:00Z")
        let elevenDays = brief("eleven-days", asOf: "2026-10-17T06:50:00Z")
        var requested: [String] = []
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            requested.append(selected.identifier)
            return self.resource(selected)
        }, downloadAudio: { _ in Data([1, 2, 3]) }, managesSystemPlayback: false)
        player.fetchRecentAudio([elevenDays, threeDays, yesterday], hubID: hub, now: now)
        await player.finishRecentAudio()
        XCTAssertEqual(requested, ["yesterday", "three-days"], "newest first, and nothing older than 7 days")
        XCTAssertNotNil(cache.cached(brief: yesterday, hubID: hub))
        XCTAssertNotNil(cache.cached(brief: threeDays, hubID: hub))
        XCTAssertNil(cache.cached(brief: elevenDays, hubID: hub))
    }

    func testRecentAudioSkipsBriefsWhoseAudioIsAlreadyOnThePhone() async throws {
        let cache = try makeCache()
        let stored = brief("stored", asOf: "2026-10-27T06:50:00Z")
        _ = try cache.store(Data([9, 9, 9]), brief: stored, hubID: hub, audio: metadata)
        let missing = brief("missing", asOf: "2026-10-26T06:50:00Z")
        var requested: [String] = []
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            requested.append(selected.identifier)
            return self.resource(selected)
        }, downloadAudio: { _ in Data([1, 2, 3]) }, managesSystemPlayback: false)
        player.fetchRecentAudio([stored, missing], hubID: hub, now: now)
        await player.finishRecentAudio()
        XCTAssertEqual(requested, ["missing"])
    }

    func testAClipOfAnEarlierRecordingIsNotServedForARemadeBrief() throws {
        let cache = try makeCache()
        let data = Data([1, 2, 3])
        let hash = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
        var first = brief("remade", asOf: "2026-10-27T06:50:00Z")
        first.audio?.sha256 = hash
        _ = try cache.store(data, brief: first, hubID: hub, audio: metadata)
        XCTAssertNotNil(cache.cached(brief: first, hubID: hub))

        // The computer remade the recording at the same time: same id and asOf, a new SHA-256.
        var remade = first
        remade.audio?.sha256 = String(repeating: "a", count: 64)
        XCTAssertNil(cache.cached(brief: remade, hubID: hub), "the old clip must not play under the new brief's words")
        XCTAssertThrowsError(try cache.store(data, brief: remade, hubID: hub, audio: metadata))
    }

    func testRecentWindowKeepsEachBriefOnceNewestFirst() throws {
        let yesterday = brief("yesterday", asOf: "2026-10-27T06:50:00Z")
        let threeDays = brief("three-days", asOf: "2026-10-25T18:00:00Z")
        let eightDays = brief("eight-days", asOf: "2026-10-20T06:50:00Z")
        let window = BriefAudioPlayer.recent([threeDays, eightDays, yesterday, yesterday], now: now)
        XCTAssertEqual(window.map(\.identifier), ["yesterday", "three-days"])
    }

    func testEarlierBriefPlaysFromTheCacheWhenTheComputerIsOffline() async throws {
        let cache = try makeCache()
        let older = brief("three-days", asOf: "2026-10-25T18:00:00Z")
        let fetcher = BriefAudioPlayer(cache: cache, requestAudio: { self.resource($0) },
                                       downloadAudio: { _ in Data([1, 2, 3]) },
                                       makeEngine: { _ in OfflineTestEngine() }, managesSystemPlayback: false)
        fetcher.fetchRecentAudio([older], hubID: hub, now: now)
        await fetcher.finishRecentAudio()
        XCTAssertNotNil(cache.cached(brief: older, hubID: hub))

        // A phone in airplane mode: the computer cannot be reached, so any request would be a failure.
        let offline = BriefAudioPlayer(cache: cache, requestAudio: { _ in
            XCTFail("A cached brief must play without the computer")
            throw BriefAudioError.computerOffline
        }, downloadAudio: { _ in
            XCTFail("A cached brief must play without the relay")
            return Data()
        }, makeEngine: { _ in OfflineTestEngine() }, managesSystemPlayback: false)
        await offline.prepare(brief: older, hubID: hub)
        XCTAssertEqual(offline.state, .ready)
        XCTAssertTrue(offline.canStart(brief: older, hubID: hub, computerOnline: false))
        await offline.toggle(brief: older, hubID: hub)
        XCTAssertEqual(offline.state, .playing)
        offline.stop()
    }

    func testAPlayTapPlaysAnUnfetchedBriefOnlyWhenItsAudioIsOnThePhone() throws {
        let cache = try makeCache()
        let older = brief("three-days", asOf: "2026-10-25T18:00:00Z")
        let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in throw BriefAudioError.computerOffline },
                                      downloadAudio: { _ in Data() }, managesSystemPlayback: false)
        XCTAssertFalse(player.canStart(brief: older, hubID: hub, computerOnline: false),
                       "Play is dimmed when the audio is not here and the computer cannot send it")
        XCTAssertTrue(player.canStart(brief: older, hubID: hub, computerOnline: true))
    }
}

/// A playback engine with a fixed clip, so the player can start without an audio session.
@MainActor
private final class OfflineTestEngine: BriefAudioPlayback {
    var duration: TimeInterval = 60
    var currentTime: TimeInterval = 0
    var rate: Float = 1
    private(set) var playing = false
    func play() -> Bool { playing = true; return true }
    func pause() { playing = false }
    func stop() { playing = false }
}
