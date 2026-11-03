import Foundation
import XCTest
@testable import Yansu

final class BriefAudioTests: XCTestCase {
    private let hub = "11111111-2222-4333-8444-555555555555"
    private let attachmentID = "7a4f7d3e-1b2c-4d5e-8f90-123456789abc"

    func testOpensWhatTheMacSealedAndRefusesAnythingElse() throws {
        let key = RelayCrypto.randomBytes(32)
        let nonce = RelayCrypto.randomBytes(12)
        let plaintext = Data("RIFF....WAVEfmt spoken brief".utf8)
        let sealed = try RelayCrypto.seal(plaintext, key: key, nonce: nonce,
                                          aad: BriefAudioCrypto.aad(hub: hub, attachmentID: attachmentID))
        let attachment = BriefAudioAttachment(id: attachmentID, key: RelayCrypto.base64URLEncode(key),
                                              nonce: RelayCrypto.base64URLEncode(nonce))
        XCTAssertEqual(try BriefAudioCrypto.open(sealed, attachment: attachment, hub: hub), plaintext)
        XCTAssertEqual(BriefAudioCrypto.aad(hub: hub, attachmentID: attachmentID),
                       "yansu-attachment-v1|\(hub)|\(attachmentID)")

        XCTAssertThrowsError(try BriefAudioCrypto.open(sealed, attachment: attachment, hub: "other-hub"))
        var otherID = attachment
        otherID.id = "99999999-aaaa-4bbb-8ccc-dddddddddddd"
        XCTAssertThrowsError(try BriefAudioCrypto.open(sealed, attachment: otherID, hub: hub))
        var tampered = sealed
        tampered[5] ^= 0x01
        XCTAssertThrowsError(try BriefAudioCrypto.open(tampered, attachment: attachment, hub: hub))
        var shortKey = attachment
        shortKey.key = RelayCrypto.base64URLEncode(RelayCrypto.randomBytes(16))
        XCTAssertThrowsError(try BriefAudioCrypto.open(sealed, attachment: shortKey, hub: hub))
        var noID = attachment
        noID.id = nil
        XCTAssertThrowsError(try BriefAudioCrypto.open(sealed, attachment: noID, hub: hub))
        var pathID = attachment
        pathID.id = "../escape"
        XCTAssertThrowsError(try BriefAudioCrypto.open(sealed, attachment: pathID, hub: hub))
    }

    private func cache() throws -> (BriefAudioCache, URL) {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        return (BriefAudioCache(directory: directory), directory)
    }

    private func brief(asOf: String = "2026-10-17T12:00:00Z") -> MobileBriefV1 {
        MobileBriefV1(briefId: "2026-10-17-morning", asOf: asOf)
    }

    private func metadata(bytes: Int = 3) -> BriefAudio {
        BriefAudio(mime: "audio/wav", bytes: bytes, durationSeconds: 60)
    }

    @MainActor
    func testTapJoinsPrefetchAndReusesDownloadedFile() async throws {
        let (primary, _) = try cache()
        let (prefetched, _) = try self.cache()
        let selected = brief()
        var requests = 0
        var downloads = 0
        var continuation: CheckedContinuation<Data, Error>?
        let began = expectation(description: "download begins")
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: primary, prefetchCache: prefetched, requestAudio: { item in
            requests += 1
            return BriefAudioResource(briefId: item.identifier, version: BriefAudioResource.versionKey(for: item), audio: self.metadata())
        }, downloadAudio: { _ in
            downloads += 1
            return try await withCheckedThrowingContinuation { pending in continuation = pending; began.fulfill() }
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        let prefetch = Task { await player.prefetch(brief: selected, hubID: hub) }
        await fulfillment(of: [began], timeout: 5)
        let tap = Task { await player.toggle(brief: selected, hubID: hub, localFirst: true) }
        await Task.yield()
        XCTAssertEqual(player.state, .loading)
        continuation?.resume(returning: Data([1, 2, 3]))
        await prefetch.value
        await tap.value
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(downloads, 1)
        XCTAssertEqual(player.state, .playing)
        player.stop()
        await player.toggle(brief: selected, hubID: hub, localFirst: true)
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(downloads, 1)
        XCTAssertEqual(player.state, .playing)
        player.stop()
    }

    @MainActor
    func testTodayTapWithoutAudioDoesNotRequestTheMac() async throws {
        let (cache, _) = try cache()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in
            XCTFail("The Today tap must not ask the Mac for metadata")
            throw BriefAudioError.computerOffline
        }, downloadAudio: { _ in XCTFail("No authorized attachment"); return Data() }, managesSystemPlayback: false)
        await player.toggle(brief: brief(), hubID: hub, localFirst: true)
        XCTAssertEqual(player.state, .loading)
    }

    @MainActor
    func testWaitingTapStartsWhenLaterPrefetchArrives() async throws {
        let (cache, _) = try cache()
        let (prefetched, _) = try self.cache()
        let selected = brief()
        let engine = TestBriefAudioEngine()
        var requests = 0
        let player = BriefAudioPlayer(cache: cache, prefetchCache: prefetched, requestAudio: { item in
            requests += 1
            return BriefAudioResource(briefId: item.identifier, version: BriefAudioResource.versionKey(for: item), audio: self.metadata())
        }, downloadAudio: { _ in Data([1, 2, 3]) }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        await player.toggle(brief: selected, hubID: hub, localFirst: true)
        XCTAssertEqual(requests, 0)
        XCTAssertEqual(player.state, .loading)
        await player.prefetch(brief: selected, hubID: hub)
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(player.state, .playing)
        XCTAssertTrue(engine.playing)
        player.stop()
    }

    @MainActor
    func testBackgroundPrefetchCanUpgradeAConcurrentRelayOnlyAttempt() async throws {
        let (cache, _) = try cache()
        let selected = brief()
        let resolving = expectation(description: "relay authorization is resolving")
        var continuation: CheckedContinuation<BriefAudioReceipt?, Never>?
        var resolves = 0
        var requests = 0
        let player = BriefAudioPlayer(cache: cache, requestAudio: { item in
            requests += 1
            return BriefAudioResource(briefId: item.identifier, version: BriefAudioResource.versionKey(for: item), audio: self.metadata())
        }, downloadAudio: { _ in Data([1, 2, 3]) }, managesSystemPlayback: false,
        resolveAuthorizationReceipt: { _ in
            resolves += 1
            guard resolves == 1 else { return nil }
            return await withCheckedContinuation { pending in continuation = pending; resolving.fulfill() }
        })
        let relayOnly = Task { await player.prefetch(brief: selected, hubID: hub, allowHubRequest: false) }
        await fulfillment(of: [resolving], timeout: 5)
        let background = Task { await player.prefetch(brief: selected, hubID: hub) }
        await Task.yield()
        continuation?.resume(returning: nil)
        await relayOnly.value
        await background.value
        XCTAssertEqual(requests, 1)
        XCTAssertNotNil(cache.cached(brief: selected, hubID: hub))
    }

    func testCacheUsesHubAndBriefVersionAndNeverStoresAttachmentSecrets() throws {
        let (cache, directory) = try cache()
        var audio = metadata()
        audio.attachment = BriefAudioAttachment(id: attachmentID, key: "private-key", nonce: "private-nonce")
        let url = try cache.store(Data([1, 2, 3]), brief: brief(), hubID: hub, audio: audio)
        XCTAssertFalse(url.lastPathComponent.contains("2026-10-17"))
        XCTAssertEqual(cache.cached(brief: brief(), hubID: hub)?.url, url)
        XCTAssertNil(cache.cached(brief: brief(), hubID: "other-computer"))
        XCTAssertNil(cache.cached(brief: brief(asOf: "2026-10-17T12:05:00Z"), hubID: hub))
        XCTAssertNil(cache.cached(brief: brief(), hubID: hub)?.audio.attachment)
        let manifest = try String(contentsOf: directory.appendingPathComponent("clips.json"), encoding: .utf8)
        XCTAssertFalse(manifest.contains("private-key"))
        XCTAssertFalse(manifest.contains("private-nonce"))
        XCTAssertTrue(try directory.resourceValues(forKeys: [.isExcludedFromBackupKey]).isExcludedFromBackup == true)
    }

    func testCacheKeepsEachBriefsClipAndRejectsOversizeWithoutEvictingIt() throws {
        let (cache, directory) = try cache()
        let first = try cache.store(Data([1, 2, 3]), brief: brief(), hubID: hub, audio: metadata())
        let newer = brief(asOf: "2026-10-17T12:05:00Z")
        let second = try cache.store(Data([4, 5, 6]), brief: newer, hubID: hub, audio: metadata())
        // Each brief keeps its own clip for 7 days, so the older brief still plays offline.
        XCTAssertEqual(cache.cached(brief: brief(), hubID: hub)?.url, first)
        XCTAssertNotNil(cache.cached(brief: newer, hubID: hub))
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).count, 3)
        XCTAssertThrowsError(try cache.store(Data(count: BriefAudioCache.maximumAudioBytes + 1),
                                            brief: brief(), hubID: hub, audio: metadata())) {
            XCTAssertEqual($0 as? BriefAudioError, .tooLarge)
        }
        XCTAssertEqual(cache.cached(brief: newer, hubID: hub)?.url, second)
        XCTAssertThrowsError(try cache.store(Data([1]), brief: brief(), hubID: hub, audio: metadata()))
    }

    private func clock(_ start: Date) -> (now: () -> Date, advance: (TimeInterval) -> Void) {
        final class Box: @unchecked Sendable { var date: Date; init(_ date: Date) { self.date = date } }
        let box = Box(start)
        return ({ box.date }, { box.date = box.date.addingTimeInterval($0) })
    }

    func testClipsAreKeptSevenDaysAndThenEvicted() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        let start = Date(timeIntervalSince1970: 1_790_000_000)
        let (now, advance) = clock(start)
        let cache = BriefAudioCache(directory: directory, now: now)
        let older = try cache.store(Data([1, 2, 3]), brief: brief(), hubID: hub, audio: metadata())
        advance(6 * 24 * 60 * 60)
        XCTAssertNotNil(cache.cached(brief: brief(), hubID: hub), "A clip six days old still plays offline")
        advance(2 * 24 * 60 * 60)
        XCTAssertNil(cache.cached(brief: brief(), hubID: hub), "A clip past its 7 days is not played from the cache")
        // The next store also removes the expired clip from disk.
        _ = try cache.store(Data([4, 5, 6]), brief: brief(asOf: "2026-10-25T12:00:00Z"), hubID: hub, audio: metadata())
        XCTAssertFalse(FileManager.default.fileExists(atPath: older.path))
    }

    func testClipCountCapEvictsTheOldestClipFirst() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        let (now, advance) = clock(Date(timeIntervalSince1970: 1_790_000_000))
        let cache = BriefAudioCache(directory: directory, now: now, maximumClips: 2)
        let oldest = brief(asOf: "2026-10-17T06:00:00Z")
        let middle = brief(asOf: "2026-10-17T12:00:00Z")
        let newest = brief(asOf: "2026-10-17T18:00:00Z")
        _ = try cache.store(Data([1]), brief: oldest, hubID: hub, audio: metadata(bytes: 1))
        advance(60)
        _ = try cache.store(Data([2]), brief: middle, hubID: hub, audio: metadata(bytes: 1))
        advance(60)
        _ = try cache.store(Data([3]), brief: newest, hubID: hub, audio: metadata(bytes: 1))
        XCTAssertNil(cache.cached(brief: oldest, hubID: hub))
        XCTAssertNotNil(cache.cached(brief: middle, hubID: hub))
        XCTAssertNotNil(cache.cached(brief: newest, hubID: hub))
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).count, 3)
    }

    func testByteCapEvictsOldestClipsUntilTheRestFit() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        let (now, advance) = clock(Date(timeIntervalSince1970: 1_790_000_000))
        let cache = BriefAudioCache(directory: directory, now: now, maximumTotalBytes: 10)
        let oldest = brief(asOf: "2026-10-17T06:00:00Z")
        let middle = brief(asOf: "2026-10-17T12:00:00Z")
        let newest = brief(asOf: "2026-10-17T18:00:00Z")
        _ = try cache.store(Data([1, 2, 3, 4]), brief: oldest, hubID: hub, audio: metadata(bytes: 4))
        advance(60)
        _ = try cache.store(Data([5, 6, 7, 8]), brief: middle, hubID: hub, audio: metadata(bytes: 4))
        advance(60)
        _ = try cache.store(Data([9, 10, 11, 12]), brief: newest, hubID: hub, audio: metadata(bytes: 4))
        // Two 4-byte clips fit in 10 bytes; the oldest does not.
        XCTAssertNil(cache.cached(brief: oldest, hubID: hub))
        XCTAssertNotNil(cache.cached(brief: middle, hubID: hub))
        XCTAssertNotNil(cache.cached(brief: newest, hubID: hub))
    }

    func testCacheAcceptsExactCapAndClearsLegacyAndOrphanedFilesOnOpen() throws {
        let (cache, directory) = try cache()
        let data = Data(count: BriefAudioCache.maximumAudioBytes)
        let url = try cache.store(data, brief: brief(), hubID: hub, audio: metadata(bytes: data.count))
        try Data([1]).write(to: directory.appendingPathComponent("orphan.wav"))
        let reopened = BriefAudioCache(directory: directory)
        XCTAssertEqual(reopened.cached(brief: brief(), hubID: hub)?.url, url)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).count, 2)
        XCTAssertTrue(reopened.clear())
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data([1]).write(to: directory.appendingPathComponent("old-unscoped.wav"))
        _ = BriefAudioCache(directory: directory)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).count, 0)
    }

    func testResumeIsPersistedOnlyForItsHubAndVersionAndClearRemovesIt() throws {
        let (cache, directory) = try cache()
        try cache.store(Data([1, 2, 3]), brief: brief(), hubID: hub, audio: metadata())
        try cache.savePosition(23, brief: brief(), hubID: hub)
        try cache.savePosition(40, brief: brief(), hubID: "different-hub")
        try cache.savePosition(50, brief: brief(asOf: "newer"), hubID: hub)
        try cache.savePosition(.nan, brief: brief(), hubID: hub)
        let reopened = BriefAudioCache(directory: directory)
        XCTAssertEqual(reopened.cached(brief: brief(), hubID: hub)?.position, 23)
        XCTAssertTrue(reopened.clear())
        XCTAssertNil(cache.cached(brief: brief(), hubID: hub))
        XCTAssertFalse(FileManager.default.fileExists(atPath: directory.path))
    }

    func testReturningToVersionRestoresItsPositionAndNewRevisionStartsAtZero() throws {
        let (cache, directory) = try cache()
        let first = brief()
        let second = MobileBriefV1(briefId: "2026-10-17-evening", asOf: "2026-10-17T22:00:00Z")
        try cache.store(Data([1, 2, 3]), brief: first, hubID: hub, audio: metadata())
        try cache.savePosition(23, brief: first, hubID: hub)
        try cache.store(Data([1, 2, 3]), brief: second, hubID: hub, audio: metadata())
        try cache.savePosition(45, brief: second, hubID: hub)
        try cache.store(Data([1, 2, 3]), brief: first, hubID: hub, audio: metadata())
        XCTAssertEqual(cache.cached(brief: first, hubID: hub)?.position, 23)
        let revised = brief(asOf: "2026-10-17T12:05:00Z")
        try cache.store(Data([1, 2, 3]), brief: revised, hubID: hub, audio: metadata())
        XCTAssertEqual(cache.cached(brief: revised, hubID: hub)?.position, 0)
        // Three briefs keep their clips, with clips.json and positions.json beside them.
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).count, 5)
    }

    func testPositionStillAdvancesWhenAnotherBriefIsPrefetched() throws {
        let (cache, _) = try cache()
        let first = brief()
        let second = MobileBriefV1(briefId: "2026-10-17-evening", asOf: "2026-10-17T22:00:00Z")
        try cache.store(Data([1, 2, 3]), brief: first, hubID: hub, audio: metadata())
        try cache.store(Data([4, 5, 6]), brief: second, hubID: hub, audio: metadata())
        try cache.savePosition(37, brief: first, hubID: hub)
        try cache.store(Data([1, 2, 3]), brief: first, hubID: hub, audio: metadata())
        XCTAssertEqual(cache.cached(brief: first, hubID: hub)?.position, 37)
    }

    @MainActor
    func testPrefetchKeepsThePreviouslyCachedClipAndRespectsWithdrawal() async throws {
        let (primary, _) = try cache()
        let (prefetched, _) = try cache()
        var old = brief()
        old.narrativeStatus = "ok"
        old.audio = metadata()
        var newest = MobileBriefV1(briefId: "evening", asOf: "2026-10-17T22:00:00Z", narrativeStatus: "ok")
        newest.audio = metadata()
        try primary.store(Data([1, 2, 3]), brief: old, hubID: hub, audio: metadata())
        let player = BriefAudioPlayer(cache: primary, prefetchCache: prefetched, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier, version: BriefAudioResource.versionKey(for: selected), audio: self.metadata())
        }, downloadAudio: { _ in Data([4, 5, 6]) }, managesSystemPlayback: false)
        await player.prefetch(brief: newest, hubID: hub)
        XCTAssertNotNil(primary.cached(brief: old, hubID: hub))
        XCTAssertNotNil(prefetched.cached(brief: newest, hubID: hub))
        await player.prepare(brief: old, hubID: hub)
        XCTAssertEqual(player.state, .ready)
        var withdrawn = newest
        withdrawn.narrativeStatus = "unavailable"
        withdrawn.audio = nil
        player.receive(brief: withdrawn, hubID: hub)
        XCTAssertNil(prefetched.cached(brief: newest, hubID: hub))
        XCTAssertNotNil(primary.cached(brief: old, hubID: hub))
        XCTAssertTrue(player.clearCache())
        XCTAssertNil(primary.cached(brief: old, hubID: hub))
    }

    @MainActor
    func testWithdrawalDuringPrefetchCannotRestoreAudio() async throws {
        let (primary, _) = try cache()
        let (prefetched, _) = try cache()
        var readable = brief()
        readable.narrativeStatus = "ok"
        readable.audio = metadata()
        let began = expectation(description: "prefetch download started")
        var download: CheckedContinuation<Data, Error>?
        let player = BriefAudioPlayer(cache: primary, prefetchCache: prefetched, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier, version: BriefAudioResource.versionKey(for: selected), audio: self.metadata())
        }, downloadAudio: { _ in
            try await withCheckedThrowingContinuation { continuation in
                download = continuation
                began.fulfill()
            }
        }, managesSystemPlayback: false)
        let task = Task { await player.prefetch(brief: readable, hubID: hub) }
        await fulfillment(of: [began], timeout: 5)
        var withdrawn = readable
        withdrawn.narrativeStatus = "unavailable"
        withdrawn.audio = nil
        player.receive(brief: withdrawn, hubID: hub)
        download?.resume(returning: Data([1, 2, 3]))
        await task.value
        XCTAssertNil(prefetched.cached(brief: readable, hubID: hub))
    }

    func testResumeLedgerHasBoundedEntriesAndDoesNotCrossHubs() throws {
        let (cache, directory) = try cache()
        for index in 0..<(BriefAudioCache.positionLimit + 6) {
            let selected = brief(asOf: "version-\(index)")
            try cache.store(Data([1, 2, 3]), brief: selected, hubID: hub, audio: metadata())
            try cache.savePosition(Double(index), brief: selected, hubID: hub)
        }
        let data = try Data(contentsOf: directory.appendingPathComponent("positions.json"))
        let positions = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(positions.count, BriefAudioCache.positionLimit)
        let selected = brief(asOf: "version-19")
        try cache.store(Data([1, 2, 3]), brief: selected, hubID: "another-computer", audio: metadata())
        XCTAssertEqual(cache.cached(brief: selected, hubID: "another-computer")?.position, 0)
        XCTAssertTrue(cache.clear())
    }

    func testFailedClearBlocksReadsAndWritesUntilDeletionIsRetried() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        var failRemoval = true
        let cache = BriefAudioCache(directory: directory, removeItem: { url in
            if failRemoval { throw CocoaError(.fileWriteNoPermission) }
            try FileManager.default.removeItem(at: url)
        })
        try cache.store(Data([1, 2, 3]), brief: brief(), hubID: hub, audio: metadata())
        XCTAssertFalse(cache.clear())
        XCTAssertNil(cache.cached(brief: brief(), hubID: hub))
        XCTAssertThrowsError(try cache.store(Data([1, 2, 3]), brief: brief(), hubID: hub, audio: metadata()))
        failRemoval = false
        XCTAssertTrue(cache.clear())
        try cache.store(Data([1, 2, 3]), brief: brief(), hubID: hub, audio: metadata())
        XCTAssertNotNil(cache.cached(brief: brief(), hubID: hub))
    }

    func testCorruptOrOversizedCachedFileIsDiscarded() throws {
        let (cache, directory) = try cache()
        let url = try cache.store(Data([1, 2, 3]), brief: brief(), hubID: hub, audio: metadata())
        try Data([1, 2]).write(to: url)
        XCTAssertNil(cache.cached(brief: brief(), hubID: hub))
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: directory.path).count, 0)
    }

    func testVersionAndDurationFormattingAreStableAndBounded() {
        XCTAssertEqual(BriefAudioResource.versionKey(for: brief()), "2026-10-17-morning|2026-10-17T12:00:00Z")
        XCTAssertEqual(BriefAudioResource.versionKey(for: MobileBriefV1(briefId: "old")), "old|")
        XCTAssertNil(BriefAudio(durationSeconds: .infinity).durationLabel)
        XCTAssertNil(BriefAudio(durationSeconds: .nan).durationLabel)
        XCTAssertEqual(BriefAudio(durationSeconds: 95).durationLabel, "1:35")
    }

    @MainActor
    func testPlayerDownloadsOnceAndResumesAcrossRelaunchWhileOffline() async throws {
        let (cache, _) = try cache()
        let brief = brief()
        let metadata = metadata()
        var requests = 0
        var downloads = 0
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            requests += 1
            return BriefAudioResource(briefId: selected.identifier,
                                      version: BriefAudioResource.versionKey(for: selected), audio: metadata)
        }, downloadAudio: { _ in
            downloads += 1
            return Data([1, 2, 3])
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        await player.prepare(brief: brief, hubID: hub)
        XCTAssertEqual(player.state, .ready)
        XCTAssertEqual(player.duration, 60)
        player.seek(to: 7)
        await player.toggle()
        XCTAssertEqual(player.state, .playing)
        XCTAssertEqual(engine.currentTime, 7, "Seeking before the first download must survive cache creation")
        engine.currentTime = 19
        player.tick()
        await player.toggle()
        XCTAssertEqual(player.state, .paused)
        XCTAssertEqual(player.position, 19)
        XCTAssertEqual(cache.cached(brief: brief, hubID: hub)?.position, 19)
        await player.toggle()
        XCTAssertEqual(player.state, .playing)
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(downloads, 1)
        player.stop()

        let resumedEngine = TestBriefAudioEngine()
        let reopened = BriefAudioPlayer(cache: cache, requestAudio: { _ in
            XCTFail("Offline cached audio must not contact the computer")
            throw BriefAudioError.computerOffline
        }, downloadAudio: { _ in
            XCTFail("Offline cached audio must not contact the relay")
            return Data()
        }, makeEngine: { _ in resumedEngine }, managesSystemPlayback: false)
        await reopened.prepare(brief: brief, hubID: hub)
        XCTAssertEqual(reopened.position, 19)
        await reopened.toggle()
        XCTAssertEqual(resumedEngine.currentTime, 19)
        XCTAssertEqual(reopened.state, .playing)
        reopened.playbackFinished()
        XCTAssertEqual(reopened.state, .ready)
        XCTAssertEqual(cache.cached(brief: brief, hubID: hub)?.position, 0)
        reopened.stop()
    }

    @MainActor
    func testPreparingWithdrawnBriefCannotRestoreItsPreviouslyReadableCachedAudio() async throws {
        let (cache, _) = try cache()
        var readable = brief()
        readable.narrativeStatus = "ok"
        readable.audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
        var withdrawn = readable
        withdrawn.narrativeStatus = "unavailable"
        withdrawn.audio = nil
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in throw BriefAudioError.notAvailable },
                                      downloadAudio: { _ in
            XCTFail("A received withdrawal must not download the old recording")
            return Data()
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)

        await player.prepare(brief: withdrawn, hubID: hub)
        await player.toggle()

        XCTAssertFalse(engine.playing)
        XCTAssertNotEqual(player.state, .playing)
        XCTAssertNil(cache.cached(brief: withdrawn, hubID: hub))
        XCTAssertNil(cache.cached(brief: readable, hubID: hub), "An older detail view cannot restore withdrawn audio")
        player.stop()
    }

    @MainActor
    func testPreparingSameVersionWithdrawalStopsPlayingAndPausedSelections() async throws {
        for paused in [false, true] {
            let (cache, _) = try cache()
            var readable = brief()
            readable.narrativeStatus = "ok"
            readable.audio = metadata()
            try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
            let engine = TestBriefAudioEngine()
            let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in throw BriefAudioError.notAvailable },
                                          downloadAudio: { _ in Data() }, makeEngine: { _ in engine },
                                          managesSystemPlayback: false)
            await player.prepare(brief: readable, hubID: hub)
            await player.toggle()
            XCTAssertTrue(engine.playing, "A readable cached brief remains available offline")
            if paused { player.pause() }

            var withdrawn = readable
            withdrawn.narrativeStatus = "unavailable"
            withdrawn.audio = nil
            await player.prepare(brief: withdrawn, hubID: hub)

            XCTAssertFalse(engine.playing, "Receiving the same version without audio must stop its engine")
            XCTAssertNotEqual(player.state, .playing)
            await player.toggle()
            XCTAssertFalse(engine.playing, "The old engine must not resume after withdrawal")
            XCTAssertNil(cache.cached(brief: readable, hubID: hub))
            player.stop()
        }
    }

    @MainActor
    func testWithdrawalSurvivesCacheAndPlayerRecreationEvenWhenAudioDeletionFails() async throws {
        for failRemoval in [false, true] {
            let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
            addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
            var removalAttempts = 0
            let cache = BriefAudioCache(directory: directory, removeItem: { url in
                removalAttempts += 1
                if failRemoval { throw CocoaError(.fileWriteNoPermission) }
                try FileManager.default.removeItem(at: url)
            })
            var readable = brief()
            readable.narrativeStatus = "ok"
            readable.audio = metadata()
            try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
            var withdrawn = readable
            withdrawn.narrativeStatus = "unavailable"
            withdrawn.audio = nil
            let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in throw BriefAudioError.notAvailable },
                                          downloadAudio: { _ in Data() }, managesSystemPlayback: false)
            await player.prepare(brief: withdrawn, hubID: hub)
            player.stop()
            XCTAssertGreaterThan(removalAttempts, 0, "Exercise deletion failure as well as persisted denial")

            let reopenedCache = BriefAudioCache(directory: directory)
            XCTAssertNil(reopenedCache.cached(brief: readable, hubID: hub))
            let engine = TestBriefAudioEngine()
            let reopenedPlayer = BriefAudioPlayer(cache: reopenedCache,
                                                  requestAudio: { _ in throw BriefAudioError.computerOffline },
                                                  downloadAudio: { _ in Data() }, makeEngine: { _ in engine },
                                                  managesSystemPlayback: false)
            await reopenedPlayer.prepare(brief: readable, hubID: hub)
            await reopenedPlayer.toggle()
            XCTAssertFalse(engine.playing, "A stale readable value cannot undo a persisted withdrawal")
            XCTAssertNotEqual(reopenedPlayer.state, .playing)
            reopenedPlayer.stop()
        }
    }

    @MainActor
    func testReceivedWithdrawalStopsPlaybackWithoutReopeningAndPreventsInterruptionResume() async throws {
        for interrupted in [false, true] {
            let (cache, _) = try cache()
            var readable = brief()
            readable.narrativeStatus = "ok"
            readable.audio = metadata()
            try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
            let engine = TestBriefAudioEngine()
            let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in throw BriefAudioError.computerOffline },
                                          downloadAudio: { _ in Data() }, makeEngine: { _ in engine },
                                          managesSystemPlayback: false)
            await player.toggle(brief: readable, hubID: hub)
            XCTAssertTrue(engine.playing)
            if interrupted { player.interruptionBegan() }
            var withdrawn = readable
            withdrawn.audio = nil
            withdrawn.narrativeStatus = "unavailable"

            player.receive(brief: withdrawn, hubID: hub)

            XCTAssertFalse(engine.playing, "Receipt must stop playback even while Feed is offscreen")
            XCTAssertNotEqual(player.state, .playing)
            player.interruptionEnded(shouldResume: true)
            XCTAssertFalse(engine.playing, "System resume cannot revive a withdrawn recording")
            XCTAssertNil(cache.cached(brief: readable, hubID: hub))
            player.stop()
        }
    }

    @MainActor
    func testReceivedWithdrawalDiscardsPendingMetadataAndDownloadCompletions() async throws {
        for waitForDownload in [false, true] {
            let (cache, _) = try cache()
            var readable = brief()
            readable.narrativeStatus = "ok"
            readable.audio = metadata()
            let audio = metadata()
            let began = expectation(description: waitForDownload ? "download pending" : "metadata pending")
            var pendingMetadata: CheckedContinuation<BriefAudioResource, Error>?
            var pendingDownload: CheckedContinuation<Data, Error>?
            let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
                if !waitForDownload {
                    return try await withCheckedThrowingContinuation { continuation in
                        pendingMetadata = continuation
                        began.fulfill()
                    }
                }
                return BriefAudioResource(briefId: selected.identifier,
                                          version: BriefAudioResource.versionKey(for: selected), audio: audio)
            }, downloadAudio: { _ in
                guard waitForDownload else {
                    XCTFail("Metadata received before withdrawal must not start a later download")
                    return Data()
                }
                return try await withCheckedThrowingContinuation { continuation in
                    pendingDownload = continuation
                    began.fulfill()
                }
            }, makeEngine: { _ in
                XCTFail("An in-flight recording must never reach playback after withdrawal")
                return TestBriefAudioEngine()
            }, managesSystemPlayback: false)
            let task = Task { await player.toggle(brief: readable, hubID: hub) }
            await fulfillment(of: [began], timeout: 1)
            var withdrawn = readable
            withdrawn.audio = nil
            withdrawn.narrativeStatus = "unavailable"

            player.receive(brief: withdrawn, hubID: hub)
            pendingMetadata?.resume(returning: BriefAudioResource(briefId: readable.identifier,
                                                                  version: BriefAudioResource.versionKey(for: readable), audio: audio))
            pendingDownload?.resume(returning: Data([1, 2, 3]))
            await task.value

            XCTAssertNotEqual(player.state, .playing)
            XCTAssertNil(cache.cached(brief: readable, hubID: hub), "A completion cannot repopulate revoked audio")
            player.stop()
        }
    }

    @MainActor
    func testNilAudioWithReadableNarrativeKeepsOfflinePlaybackAvailable() async throws {
        let (cache, _) = try cache()
        var readable = brief()
        readable.narrativeStatus = "ok"
        readable.audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
        var rendering = readable
        rendering.audio = nil
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in
            XCTFail("An unwithdrawn cached recording remains usable while the computer is offline")
            throw BriefAudioError.computerOffline
        }, downloadAudio: { _ in
            XCTFail("An unwithdrawn cached recording does not need a new download")
            return Data()
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)

        player.receive(brief: rendering, hubID: hub)
        await player.toggle(brief: rendering, hubID: hub)

        XCTAssertTrue(engine.playing)
        XCTAssertEqual(player.state, .playing)
        XCTAssertNotNil(cache.cached(brief: readable, hubID: hub))
        player.stop()
    }

    @MainActor
    func testReceivedWithdrawalIsScopedToTheSelectedHubAndVersion() async throws {
        let (cache, _) = try cache()
        var readable = brief()
        readable.narrativeStatus = "ok"
        readable.audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in throw BriefAudioError.computerOffline },
                                      downloadAudio: { _ in Data() }, makeEngine: { _ in engine },
                                      managesSystemPlayback: false)
        await player.toggle(brief: readable, hubID: hub)
        var withdrawn = readable
        withdrawn.audio = nil
        withdrawn.narrativeStatus = "unavailable"

        player.receive(brief: withdrawn, hubID: "other-computer")
        XCTAssertTrue(engine.playing)
        withdrawn.asOf = "2026-10-17T12:05:00Z"
        player.receive(brief: withdrawn, hubID: hub)

        XCTAssertTrue(engine.playing)
        XCTAssertNotNil(cache.cached(brief: readable, hubID: hub))
        player.stop()
    }

    @MainActor
    func testWithdrawnCacheRequiresFreshAuthorizedMetadataAndDownloadBeforePlayback() async throws {
        let (cache, directory) = try cache()
        var readable = brief()
        readable.narrativeStatus = "ok"
        readable.audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
        var withdrawn = readable
        withdrawn.audio = nil
        withdrawn.narrativeStatus = "unavailable"
        cache.invalidate(brief: withdrawn, hubID: hub)
        let reopenedCache = BriefAudioCache(directory: directory)
        var requests = 0
        var downloads = 0
        let audio = metadata()
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: reopenedCache, requestAudio: { selected in
            requests += 1
            return BriefAudioResource(briefId: selected.identifier,
                                      version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in
            downloads += 1
            return Data([4, 5, 6])
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)

        await player.toggle(brief: readable, hubID: hub)

        XCTAssertEqual(requests, 1)
        XCTAssertEqual(downloads, 1, "Fresh metadata alone must not authorize the old cached bytes")
        XCTAssertEqual(player.state, .playing)
        let entry = try XCTUnwrap(reopenedCache.cached(brief: readable, hubID: hub))
        XCTAssertEqual(try Data(contentsOf: entry.url), Data([4, 5, 6]))
        player.stop()
    }

    @MainActor
    func testReauthorizedWithdrawnViewCanPauseAndResumeUntilAnotherWithdrawalArrives() async throws {
        let (cache, _) = try cache()
        var readable = brief()
        readable.narrativeStatus = "ok"
        readable.audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
        var withdrawn = readable
        withdrawn.audio = nil
        withdrawn.narrativeStatus = "unavailable"
        var requests = 0
        var downloads = 0
        let audio = metadata()
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            requests += 1
            return BriefAudioResource(briefId: selected.identifier,
                                      version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in
            downloads += 1
            return Data([4, 5, 6])
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        player.receive(brief: withdrawn, hubID: hub)
        await player.prepare(brief: withdrawn, hubID: hub)
        await player.toggle(brief: withdrawn, hubID: hub)
        XCTAssertEqual(player.state, .playing)
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(downloads, 1)

        await player.toggle(brief: withdrawn, hubID: hub)

        XCTAssertEqual(player.state, .paused, "Pause from the unchanged view must not restart the recording")
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(downloads, 1)
        XCTAssertNotNil(cache.cached(brief: withdrawn, hubID: hub))
        await player.toggle(brief: withdrawn, hubID: hub)
        XCTAssertEqual(player.state, .playing)
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(downloads, 1)

        player.receive(brief: withdrawn, hubID: hub)

        XCTAssertFalse(engine.playing, "A new authoritative receipt must still revoke the reauthorized recording")
        XCTAssertNotEqual(player.state, .playing)
        XCTAssertNil(cache.cached(brief: withdrawn, hubID: hub))
        player.stop()
    }

    @MainActor
    func testReauthorizedWithdrawnViewRestoresOfflineAfterCacheAndPlayerRecreation() async throws {
        let (cache, directory) = try cache()
        var readable = brief()
        readable.narrativeStatus = "ok"
        readable.audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: readable, hubID: hub, audio: metadata())
        var withdrawn = readable
        withdrawn.audio = nil
        withdrawn.narrativeStatus = "unavailable"
        let audio = metadata()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier,
                               version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in Data([4, 5, 6]) }, makeEngine: { _ in TestBriefAudioEngine() },
                                      managesSystemPlayback: false)
        player.receive(brief: withdrawn, hubID: hub)
        await player.toggle(brief: withdrawn, hubID: hub)
        XCTAssertEqual(player.state, .playing)
        player.seek(to: 19)
        player.stop()

        let reopenedCache = BriefAudioCache(directory: directory)
        let engine = TestBriefAudioEngine()
        let reopenedPlayer = BriefAudioPlayer(cache: reopenedCache, requestAudio: { _ in
            XCTFail("Reopening the stale view must retain the newer authorized offline recording")
            throw BriefAudioError.computerOffline
        }, downloadAudio: { _ in
            XCTFail("The reauthorized recording is already cached")
            return Data()
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        reopenedPlayer.receive(brief: withdrawn, hubID: hub, fromCache: true)
        await reopenedPlayer.toggle(brief: withdrawn, hubID: hub)

        XCTAssertEqual(reopenedPlayer.state, .playing)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(engine.currentTime, 19)
        XCTAssertNotNil(reopenedCache.cached(brief: withdrawn, hubID: hub))
        reopenedPlayer.receive(brief: withdrawn, hubID: hub)
        XCTAssertFalse(engine.playing)
        XCTAssertNil(reopenedCache.cached(brief: withdrawn, hubID: hub))
        reopenedPlayer.stop()
    }

    @MainActor
    func testWithdrawalReceiptsAreBoundedAndDoNotPersistBriefContent() throws {
        let (cache, directory) = try cache()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in throw BriefAudioError.computerOffline },
                                      downloadAudio: { _ in Data() }, managesSystemPlayback: false)
        for index in 0..<20 {
            var withdrawn = brief(asOf: "version-\(index)")
            withdrawn.narrativeStatus = "unavailable"
            player.receive(brief: withdrawn, hubID: hub,
                           receipt: BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: UInt64(index + 1)))
        }

        let data = try Data(contentsOf: directory.appendingPathComponent("withdrawals.json"))
        let records = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(records.count, 14)
        XCTAssertTrue(records.keys.allSatisfy { $0.count == 64 && $0.allSatisfy(\.isHexDigit) })
        let text = try XCTUnwrap(String(data: data, encoding: .utf8))
        XCTAssertFalse(text.contains(brief().identifier))
        XCTAssertFalse(text.contains(hub))
        XCTAssertFalse(text.contains("version-"))
        XCTAssertTrue(cache.clear())
        XCTAssertFalse(FileManager.default.fileExists(atPath: directory.path))
        player.stop()
    }

    @MainActor
    func testFailedWithdrawalReceiptPersistenceBlocksRecoveredAudio() async throws {
        let (cache, directory) = try cache()
        var withdrawn = brief()
        withdrawn.narrativeStatus = "unavailable"
        let audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: withdrawn, hubID: hub, audio: audio)
        try FileManager.default.createDirectory(at: directory.appendingPathComponent("withdrawals.json"),
                                               withIntermediateDirectories: true)
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier,
                               version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in Data([4, 5, 6]) }, makeEngine: { _ in engine },
                                      managesSystemPlayback: false)

        player.receive(brief: withdrawn, hubID: hub,
                       receipt: BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8))
        await player.toggle(brief: withdrawn, hubID: hub)

        XCTAssertFalse(engine.playing, "Recovery cannot persist authorization without its withdrawal receipt")
        XCTAssertNotEqual(player.state, .playing)
        XCTAssertNil(cache.cached(brief: withdrawn, hubID: hub))
        XCTAssertThrowsError(try cache.store(Data([4, 5, 6]), brief: withdrawn, hubID: hub, audio: audio))
        player.stop()
    }

    @MainActor
    func testDuplicateAndOlderWithdrawalReceiptsPreserveNewlyAuthorizedPlayback() async throws {
        let (cache, _) = try cache()
        var withdrawn = brief()
        withdrawn.narrativeStatus = "unavailable"
        let audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: withdrawn, hubID: hub, audio: audio)
        let receipt = BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8)
        var requests = 0
        var downloads = 0
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            requests += 1
            return BriefAudioResource(briefId: selected.identifier,
                                      version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in
            downloads += 1
            return Data([4, 5, 6])
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        player.receive(brief: withdrawn, hubID: hub, receipt: receipt)
        await player.toggle(brief: withdrawn, hubID: hub)
        player.seek(to: 19)
        let entry = try XCTUnwrap(cache.cached(brief: withdrawn, hubID: hub))

        for sequence in [UInt64(8), 7] {
            player.receive(brief: withdrawn, hubID: hub,
                           receipt: BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: sequence))

            XCTAssertEqual(player.state, .playing, "A repeated or older relay decision cannot revoke later audio authorization")
            XCTAssertTrue(engine.playing)
            XCTAssertEqual(engine.currentTime, 19)
            XCTAssertEqual(cache.cached(brief: withdrawn, hubID: hub)?.url, entry.url)
            XCTAssertEqual(try Data(contentsOf: entry.url), Data([4, 5, 6]))
        }
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(downloads, 1)
        player.stop()
    }

    @MainActor
    func testDuplicateWithdrawalReceiptPreservesAuthorizationAfterCacheAndPlayerRecreation() async throws {
        let (cache, directory) = try cache()
        var withdrawn = brief()
        withdrawn.narrativeStatus = "unavailable"
        let audio = metadata()
        try cache.store(Data([1, 2, 3]), brief: withdrawn, hubID: hub, audio: audio)
        let receipt = BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8)
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier,
                               version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in Data([4, 5, 6]) }, makeEngine: { _ in TestBriefAudioEngine() },
                                      managesSystemPlayback: false)
        player.receive(brief: withdrawn, hubID: hub, receipt: receipt)
        await player.toggle(brief: withdrawn, hubID: hub)
        player.seek(to: 19)
        player.stop()

        let reopenedCache = BriefAudioCache(directory: directory)
        let engine = TestBriefAudioEngine()
        let reopenedPlayer = BriefAudioPlayer(cache: reopenedCache, requestAudio: { _ in
            XCTFail("Refetching the old withdrawal must not require an offline computer")
            throw BriefAudioError.computerOffline
        }, downloadAudio: { _ in
            XCTFail("The newer authorized recording must remain cached")
            return Data()
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        reopenedPlayer.receive(brief: withdrawn, hubID: hub, receipt: receipt)
        await reopenedPlayer.toggle(brief: withdrawn, hubID: hub)

        XCTAssertEqual(reopenedPlayer.state, .playing)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(engine.currentTime, 19)
        XCTAssertNotNil(reopenedCache.cached(brief: withdrawn, hubID: hub))
        reopenedPlayer.stop()
    }

    @MainActor
    func testOffscreenWithdrawalReceiptWithoutAudioSurvivesRestartAndLaterRecovery() async throws {
        let (cache, directory) = try cache()
        var withdrawn = brief()
        withdrawn.narrativeStatus = "unavailable"
        let receipt = BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8)
        let initialPlayer = BriefAudioPlayer(cache: cache, requestAudio: { _ in
            XCTFail("Receiving a withdrawal must not request audio")
            throw BriefAudioError.computerOffline
        }, downloadAudio: { _ in Data() }, managesSystemPlayback: false)
        initialPlayer.receive(brief: withdrawn, hubID: hub, receipt: receipt)
        initialPlayer.stop()

        let reopenedCache = BriefAudioCache(directory: directory)
        let audio = metadata()
        let engine = TestBriefAudioEngine()
        let reopenedPlayer = BriefAudioPlayer(cache: reopenedCache, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier,
                               version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in Data([4, 5, 6]) }, makeEngine: { _ in engine },
                                              managesSystemPlayback: false)
        await reopenedPlayer.toggle(brief: withdrawn, hubID: hub)
        XCTAssertEqual(reopenedPlayer.state, .playing)

        reopenedPlayer.receive(brief: withdrawn, hubID: hub, receipt: receipt)

        XCTAssertEqual(reopenedPlayer.state, .playing, "Recovery must remember the receipt even when no recording existed at withdrawal")
        XCTAssertTrue(engine.playing)
        XCTAssertNotNil(reopenedCache.cached(brief: withdrawn, hubID: hub))
        reopenedPlayer.stop()
    }

    @MainActor
    func testNewerWithdrawalAndNewPairingEpochRevokeReauthorizedPlayback() async throws {
        let firstReceipt = BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8)
        let nextReceipts = [
            BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 9),
            BriefAudioReceipt(deviceID: "phone-b", pairingID: "pairing-a", sequence: 1),
            BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-b", sequence: 1)
        ]
        for receipt in nextReceipts {
            let (cache, _) = try cache()
            var withdrawn = brief()
            withdrawn.narrativeStatus = "unavailable"
            let audio = metadata()
            let engine = TestBriefAudioEngine()
            let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
                BriefAudioResource(briefId: selected.identifier,
                                   version: BriefAudioResource.versionKey(for: selected), audio: audio)
            }, downloadAudio: { _ in Data([4, 5, 6]) }, makeEngine: { _ in engine },
                                          managesSystemPlayback: false)
            player.receive(brief: withdrawn, hubID: hub, receipt: firstReceipt)
            await player.toggle(brief: withdrawn, hubID: hub)
            XCTAssertEqual(player.state, .playing)

            player.receive(brief: withdrawn, hubID: hub, receipt: receipt)

            XCTAssertEqual(player.state, .unavailable(.notAvailable))
            XCTAssertFalse(engine.playing)
            XCTAssertNil(cache.cached(brief: withdrawn, hubID: hub))
            player.stop()
        }
    }

    @MainActor
    func testAuthorizationCursorPreservesRecoveredLegacyWithdrawalAfterRestart() async throws {
        let (cache, directory) = try cache()
        var withdrawn = brief()
        withdrawn.narrativeStatus = "unavailable"
        let receipt = BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8)
        let audio = metadata()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier,
                               version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in Data([4, 5, 6]) }, makeEngine: { _ in TestBriefAudioEngine() },
                                      managesSystemPlayback: false, authorizationReceipt: { receipt })
        // Older brief-cache records do not include a receipt; the accepted pairing cursor still does.
        await player.toggle(brief: withdrawn, hubID: hub)
        XCTAssertEqual(player.state, .playing)
        player.stop()

        let reopenedCache = BriefAudioCache(directory: directory)
        let engine = TestBriefAudioEngine()
        let reopenedPlayer = BriefAudioPlayer(cache: reopenedCache,
                                              requestAudio: { _ in throw BriefAudioError.computerOffline },
                                              downloadAudio: { _ in Data() }, makeEngine: { _ in engine },
                                              managesSystemPlayback: false)
        reopenedPlayer.receive(brief: withdrawn, hubID: hub, fromCache: true, receipt: receipt)
        reopenedPlayer.receive(brief: withdrawn, hubID: hub, receipt: receipt)
        await reopenedPlayer.toggle(brief: withdrawn, hubID: hub)

        XCTAssertEqual(reopenedPlayer.state, .playing)
        XCTAssertTrue(engine.playing)
        XCTAssertNotNil(reopenedCache.cached(brief: withdrawn, hubID: hub))
        reopenedPlayer.receive(brief: withdrawn, hubID: hub,
                               receipt: BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 9))
        XCTAssertFalse(engine.playing)
        XCTAssertNil(reopenedCache.cached(brief: withdrawn, hubID: hub))
        reopenedPlayer.stop()
    }

    @MainActor
    func testRecoveryInNewPairingReplacesOldPairingWithdrawalCursor() async throws {
        let (cache, _) = try cache()
        var withdrawn = brief()
        withdrawn.narrativeStatus = "unavailable"
        let oldReceipt = BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8)
        let newReceipt = BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-b", sequence: 0)
        let audio = metadata()
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier,
                               version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in Data([4, 5, 6]) }, makeEngine: { _ in engine },
                                      managesSystemPlayback: false, authorizationReceipt: { newReceipt })
        player.receive(brief: withdrawn, hubID: hub, receipt: oldReceipt)
        await player.toggle(brief: withdrawn, hubID: hub)
        XCTAssertEqual(player.state, .playing)

        player.receive(brief: withdrawn, hubID: hub, fromCache: true, receipt: oldReceipt)
        XCTAssertEqual(player.state, .playing, "An old local pairing projection cannot revoke the current pairing authorization")
        XCTAssertTrue(engine.playing)
        XCTAssertNotNil(cache.cached(brief: withdrawn, hubID: hub))
        player.receive(brief: withdrawn, hubID: hub, receipt: newReceipt)

        XCTAssertEqual(player.state, .playing, "Sequence numbers must reset with the pairing epoch")
        XCTAssertTrue(engine.playing)
        XCTAssertNotNil(cache.cached(brief: withdrawn, hubID: hub))
        player.receive(brief: withdrawn, hubID: hub,
                       receipt: BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-b", sequence: 1))
        XCTAssertFalse(engine.playing)
        XCTAssertNil(cache.cached(brief: withdrawn, hubID: hub))
        player.stop()
    }

    @MainActor
    func testPersistedNewerWithdrawalBlocksOldAuthorizationBeforeManifestInvalidation() async throws {
        let (cache, directory) = try cache()
        var withdrawn = brief()
        withdrawn.narrativeStatus = "unavailable"
        let audio = metadata()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
            BriefAudioResource(briefId: selected.identifier,
                               version: BriefAudioResource.versionKey(for: selected), audio: audio)
        }, downloadAudio: { _ in Data([4, 5, 6]) }, makeEngine: { _ in TestBriefAudioEngine() },
                                      managesSystemPlayback: false)
        player.receive(brief: withdrawn, hubID: hub,
                       receipt: BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8))
        await player.toggle(brief: withdrawn, hubID: hub)
        XCTAssertEqual(player.state, .playing)
        player.stop()

        // Recreate the crash boundary between persisting the new decision and revoking the manifest.
        cache.recordWithdrawal(BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 9),
                               brief: withdrawn, hubID: hub)
        let reopenedCache = BriefAudioCache(directory: directory)
        XCTAssertNil(reopenedCache.cached(brief: withdrawn, hubID: hub))
        XCTAssertTrue(reopenedCache.withdrawalState(brief: withdrawn, hubID: hub).withdrawn)
        for index in 0..<14 {
            let otherBrief = brief(asOf: "other-version-\(index)")
            reopenedCache.recordWithdrawal(BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: UInt64(10 + index)),
                                           brief: otherBrief, hubID: hub)
        }
        XCTAssertNil(reopenedCache.cached(brief: withdrawn, hubID: hub), "Receipt ledger eviction must not revive bytes denied before the crash")
        XCTAssertTrue(reopenedCache.withdrawalState(brief: withdrawn, hubID: hub).withdrawn)
        let engine = TestBriefAudioEngine()
        let reopenedPlayer = BriefAudioPlayer(cache: reopenedCache,
                                              requestAudio: { _ in throw BriefAudioError.computerOffline },
                                              downloadAudio: { _ in
            XCTFail("A known newer withdrawal requires a fresh computer authorization")
            return Data()
        }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        await reopenedPlayer.toggle(brief: withdrawn, hubID: hub)

        XCTAssertFalse(engine.playing)
        XCTAssertNotEqual(reopenedPlayer.state, .playing)
        XCTAssertNil(reopenedCache.cached(brief: withdrawn, hubID: hub))
        reopenedPlayer.stop()
    }

    @MainActor
    func testDuplicateWithdrawalReceiptDoesNotCancelPendingAudioRecovery() async throws {
        for waitForDownload in [false, true] {
            let (cache, _) = try cache()
            var withdrawn = brief()
            withdrawn.narrativeStatus = "unavailable"
            let receipt = BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8)
            let audio = metadata()
            let began = expectation(description: waitForDownload ? "recovery download pending" : "recovery metadata pending")
            var pendingMetadata: CheckedContinuation<BriefAudioResource, Error>?
            var pendingDownload: CheckedContinuation<Data, Error>?
            var requests = 0
            var downloads = 0
            let engine = TestBriefAudioEngine()
            let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
                requests += 1
                if !waitForDownload {
                    return try await withCheckedThrowingContinuation { continuation in
                        pendingMetadata = continuation
                        began.fulfill()
                    }
                }
                return BriefAudioResource(briefId: selected.identifier,
                                          version: BriefAudioResource.versionKey(for: selected), audio: audio)
            }, downloadAudio: { _ in
                downloads += 1
                if waitForDownload {
                    return try await withCheckedThrowingContinuation { continuation in
                        pendingDownload = continuation
                        began.fulfill()
                    }
                }
                return Data([4, 5, 6])
            }, makeEngine: { _ in engine }, managesSystemPlayback: false)
            player.receive(brief: withdrawn, hubID: hub, receipt: receipt)
            let task = Task { await player.toggle(brief: withdrawn, hubID: hub) }
            await fulfillment(of: [began], timeout: 1)

            player.receive(brief: withdrawn, hubID: hub, receipt: receipt)
            pendingMetadata?.resume(returning: BriefAudioResource(briefId: withdrawn.identifier,
                                                                  version: BriefAudioResource.versionKey(for: withdrawn), audio: audio))
            pendingDownload?.resume(returning: Data([4, 5, 6]))
            await task.value

            XCTAssertEqual(player.state, .playing, "A duplicate receipt must not cancel authorization requested after it")
            XCTAssertTrue(engine.playing)
            XCTAssertEqual(requests, 1)
            XCTAssertEqual(downloads, 1)
            XCTAssertNotNil(cache.cached(brief: withdrawn, hubID: hub))
            player.stop()
        }
    }

    @MainActor
    func testNewerWithdrawalReceiptCancelsPendingAudioRecovery() async throws {
        for waitForDownload in [false, true] {
            let (cache, _) = try cache()
            var withdrawn = brief()
            withdrawn.narrativeStatus = "unavailable"
            let audio = metadata()
            let began = expectation(description: waitForDownload ? "recovery download pending" : "recovery metadata pending")
            var pendingMetadata: CheckedContinuation<BriefAudioResource, Error>?
            var pendingDownload: CheckedContinuation<Data, Error>?
            let player = BriefAudioPlayer(cache: cache, requestAudio: { selected in
                if !waitForDownload {
                    return try await withCheckedThrowingContinuation { continuation in
                        pendingMetadata = continuation
                        began.fulfill()
                    }
                }
                return BriefAudioResource(briefId: selected.identifier,
                                          version: BriefAudioResource.versionKey(for: selected), audio: audio)
            }, downloadAudio: { _ in
                guard waitForDownload else {
                    XCTFail("A newer withdrawal must cancel the metadata response before download")
                    return Data()
                }
                return try await withCheckedThrowingContinuation { continuation in
                    pendingDownload = continuation
                    began.fulfill()
                }
            }, makeEngine: { _ in
                XCTFail("A newer withdrawal must cancel recovery before playback")
                return TestBriefAudioEngine()
            }, managesSystemPlayback: false)
            player.receive(brief: withdrawn, hubID: hub,
                           receipt: BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 8))
            let task = Task { await player.toggle(brief: withdrawn, hubID: hub) }
            await fulfillment(of: [began], timeout: 1)

            player.receive(brief: withdrawn, hubID: hub,
                           receipt: BriefAudioReceipt(deviceID: "phone-a", pairingID: "pairing-a", sequence: 9))
            pendingMetadata?.resume(returning: BriefAudioResource(briefId: withdrawn.identifier,
                                                                  version: BriefAudioResource.versionKey(for: withdrawn), audio: audio))
            pendingDownload?.resume(returning: Data([4, 5, 6]))
            await task.value

            XCTAssertEqual(player.state, .unavailable(.notAvailable))
            XCTAssertNil(cache.cached(brief: withdrawn, hubID: hub))
            player.stop()
        }
    }

    @MainActor
    func testPlayerShowsUnavailableReasonAndRetriesOnPlay() async throws {
        let (cache, _) = try cache()
        let audio = metadata()
        var available = false
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { brief in
            guard available else { throw BriefAudioError.rendering }
            return BriefAudioResource(briefId: brief.identifier, version: BriefAudioResource.versionKey(for: brief), audio: audio)
        }, downloadAudio: { _ in Data([1, 2, 3]) }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        await player.prepare(brief: brief(), hubID: hub)
        XCTAssertEqual(player.state, .unavailable(.rendering))
        available = true
        await player.toggle()
        XCTAssertEqual(player.state, .playing)
        player.stop()
    }

    @MainActor
    func testPlayerRejectsWrongVersionBeforeDownload() async throws {
        let (cache, _) = try cache()
        let audio = metadata()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { brief in
            BriefAudioResource(briefId: brief.identifier, version: "stale", audio: audio)
        }, downloadAudio: { _ in
            XCTFail("A stale attachment must not be fetched")
            return Data()
        }, managesSystemPlayback: false)
        await player.prepare(brief: brief(), hubID: hub)
        XCTAssertEqual(player.state, .unavailable(.versionChanged))
        XCTAssertNil(cache.cached(brief: brief(), hubID: hub))
    }

    @MainActor
    func testPlayerDiscardsMetadataAfterPairingChanges() async throws {
        let (cache, _) = try cache()
        let selected = brief()
        let audio = metadata()
        let began = expectation(description: "metadata requested")
        var pending: CheckedContinuation<BriefAudioResource, Error>?
        let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in
            try await withCheckedThrowingContinuation { continuation in
                pending = continuation
                began.fulfill()
            }
        }, downloadAudio: { _ in Data([1, 2, 3]) }, managesSystemPlayback: false)
        let task = Task { await player.prepare(brief: selected, hubID: hub) }
        await fulfillment(of: [began], timeout: 1)
        XCTAssertTrue(player.resetForPairing(hubID: "new-computer"))
        pending?.resume(returning: BriefAudioResource(briefId: selected.identifier,
                                                     version: BriefAudioResource.versionKey(for: selected), audio: audio))
        await task.value
        XCTAssertEqual(player.state, .idle)
        XCTAssertNil(player.brief)
        XCTAssertNil(cache.cached(brief: selected, hubID: hub))
    }

    @MainActor
    func testPlayerDiscardsDownloadAfterSameComputerRePair() async throws {
        let (cache, _) = try cache()
        let selected = brief()
        let audio = metadata()
        let began = expectation(description: "download requested")
        var pending: CheckedContinuation<Data, Error>?
        let player = BriefAudioPlayer(cache: cache, requestAudio: { brief in
            BriefAudioResource(briefId: brief.identifier, version: BriefAudioResource.versionKey(for: brief), audio: audio)
        }, downloadAudio: { _ in
            try await withCheckedThrowingContinuation { continuation in
                pending = continuation
                began.fulfill()
            }
        }, makeEngine: { _ in
            XCTFail("Stale decrypted audio must never reach the engine")
            return TestBriefAudioEngine()
        }, managesSystemPlayback: false)
        await player.prepare(brief: selected, hubID: hub)
        let task = Task { await player.toggle() }
        await fulfillment(of: [began], timeout: 1)
        XCTAssertTrue(player.resetForPairing(hubID: hub))
        pending?.resume(returning: Data([1, 2, 3]))
        await task.value
        XCTAssertEqual(player.state, .idle)
        XCTAssertNil(cache.cached(brief: selected, hubID: hub))
    }

    @MainActor
    func testDelayedButtonActionCannotControlAnotherBriefOrANewerSelectionOfItself() async throws {
        for returnToFirstBrief in [false, true] {
            let (cache, _) = try cache()
            let first = brief()
            let second = MobileBriefV1(briefId: "2026-10-17-evening", asOf: "2026-10-17T22:00:00Z")
            let audio = metadata()
            let began = expectation(description: "first selection waiting for metadata")
            var pending: CheckedContinuation<BriefAudioResource, Error>?
            var requestsForFirst = 0
            let engine = TestBriefAudioEngine()
            let player = BriefAudioPlayer(cache: cache, requestAudio: { brief in
                if brief.identifier == first.identifier {
                    requestsForFirst += 1
                    if requestsForFirst == 1 {
                        return try await withCheckedThrowingContinuation { continuation in
                            pending = continuation
                            began.fulfill()
                        }
                    }
                }
                return BriefAudioResource(briefId: brief.identifier,
                                          version: BriefAudioResource.versionKey(for: brief), audio: audio)
            }, downloadAudio: { _ in Data([1, 2, 3]) }, makeEngine: { _ in engine }, managesSystemPlayback: false)
            let staleAction = Task { await player.toggle(brief: first, hubID: hub) }
            await fulfillment(of: [began], timeout: 1)
            await player.toggle(brief: second, hubID: hub)
            XCTAssertEqual(player.state, .playing)
            if returnToFirstBrief {
                await player.toggle(brief: first, hubID: hub)
                XCTAssertEqual(player.state, .playing)
            }
            pending?.resume(returning: BriefAudioResource(briefId: first.identifier,
                                                         version: BriefAudioResource.versionKey(for: first), audio: audio))
            await staleAction.value
            XCTAssertEqual(player.state, .playing, "The old action must not pause the newer selection")
            XCTAssertTrue(player.matches(brief: returnToFirstBrief ? first : second, hubID: hub))
            player.stop()
        }
    }

    @MainActor
    func testPlayerCancellationDoesNotPublishOrCacheAudio() async throws {
        let (cache, _) = try cache()
        let selected = brief()
        let audio = metadata()
        let began = expectation(description: "download requested")
        var pending: CheckedContinuation<Data, Error>?
        let player = BriefAudioPlayer(cache: cache, requestAudio: { brief in
            BriefAudioResource(briefId: brief.identifier, version: BriefAudioResource.versionKey(for: brief), audio: audio)
        }, downloadAudio: { _ in
            try await withCheckedThrowingContinuation { continuation in
                pending = continuation
                began.fulfill()
            }
        }, managesSystemPlayback: false)
        await player.prepare(brief: selected, hubID: hub)
        let task = Task { await player.toggle() }
        await fulfillment(of: [began], timeout: 1)
        task.cancel()
        pending?.resume(returning: Data([1, 2, 3]))
        await task.value
        XCTAssertEqual(player.state, .ready)
        XCTAssertNil(cache.cached(brief: selected, hubID: hub))
    }

    @MainActor
    func testPlayerSeeksSafelyAndHandlesInterruptionsAndHeadphones() async throws {
        let (cache, _) = try cache()
        let selected = brief()
        try cache.store(Data([1, 2, 3]), brief: selected, hubID: hub, audio: metadata())
        let engine = TestBriefAudioEngine()
        let player = BriefAudioPlayer(cache: cache, requestAudio: { _ in throw BriefAudioError.computerOffline },
                                      downloadAudio: { _ in Data() }, makeEngine: { _ in engine }, managesSystemPlayback: false)
        await player.prepare(brief: selected, hubID: hub)
        await player.toggle()
        player.seek(to: 200)
        XCTAssertEqual(player.position, 60)
        player.seek(to: -10)
        XCTAssertEqual(player.position, 0)
        player.seek(to: .nan)
        XCTAssertEqual(player.position, 0)
        player.seek(to: 15)
        player.interruptionBegan()
        XCTAssertEqual(player.state, .paused)
        player.interruptionEnded(shouldResume: true)
        XCTAssertEqual(player.state, .playing)
        player.headphonesDisconnected()
        XCTAssertEqual(player.state, .paused)
        player.interruptionBegan()
        player.interruptionEnded(shouldResume: true)
        XCTAssertEqual(player.state, .paused)
        XCTAssertEqual(cache.cached(brief: selected, hubID: hub)?.position, 15)
        XCTAssertTrue(player.clearCache())
        XCTAssertEqual(player.state, .idle)
        XCTAssertNil(cache.cached(brief: selected, hubID: hub))
    }
}

@MainActor
private final class TestBriefAudioEngine: BriefAudioPlayback {
    var duration: TimeInterval = 60
    var currentTime: TimeInterval = 0
    var rate: Float = 1
    private(set) var playing = false
    func play() -> Bool { playing = true; return true }
    func pause() { playing = false }
    func stop() { playing = false }
}
