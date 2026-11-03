import Darwin
import Foundation
import XCTest
@testable import Yansu

// kNotice derivation, storage and the relay context the extension reads.
final class SharedNoticeStoreTests: XCTestCase {
    func testBothSidesOfThePairingDeriveTheSameNoticeKey() throws {
        let device = RelayCrypto.makeDeviceKeyPair()
        let hub = RelayCrypto.makeDeviceKeyPair()

        let fromDevice = try RelayCrypto.noticeKey(devicePrivate: device.privateKey, hubPublic: hub.publicKey,
                                                   hubID: "hub-k", deviceID: "dev-k")
        let fromHub = try RelayCrypto.noticeKey(devicePrivate: hub.privateKey, hubPublic: device.publicKey,
                                                hubID: "hub-k", deviceID: "dev-k")

        XCTAssertEqual(fromDevice, fromHub)
        XCTAssertEqual(fromDevice.count, 32)
        let otherDevice = try RelayCrypto.noticeKey(devicePrivate: device.privateKey, hubPublic: hub.publicKey,
                                                    hubID: "hub-k", deviceID: "dev-other")
        XCTAssertNotEqual(fromDevice, otherDevice, "the salt binds the key to the device")
    }

    func testProvisionStoresTheKeyAndTheContextForTheExtension() throws {
        let device = RelayCrypto.makeDeviceKeyPair()
        let hub = RelayCrypto.makeDeviceKeyPair()
        let secrets = InMemorySecretStore()

        try SharedNoticeStore.provision(hubID: "hub-p", deviceID: "dev-p", relayOrigin: "https://relay.example.test",
                                        hubPublicKey: RelayCrypto.base64URLEncode(hub.publicKey),
                                        devicePrivateKey: device.privateKey, secrets: secrets)

        let loaded = try XCTUnwrap(SharedNoticeStore.load(secrets: secrets))
        XCTAssertEqual(loaded.context, NoticeRelayContext(origin: "https://relay.example.test", hubID: "hub-p", deviceID: "dev-p"))
        XCTAssertEqual(loaded.key, try RelayCrypto.noticeKey(devicePrivate: device.privateKey, hubPublic: hub.publicKey,
                                                             hubID: "hub-p", deviceID: "dev-p"))
        XCTAssertTrue(SharedNoticeStore.matches(hubID: "hub-p", deviceID: "dev-p",
                                                relayOrigin: "https://relay.example.test", secrets: secrets))
        XCTAssertFalse(SharedNoticeStore.matches(hubID: "hub-p", deviceID: "dev-p",
                                                 relayOrigin: "https://other.example.test", secrets: secrets))
    }

    func testMalformedHubKeyWritesNothing() {
        let secrets = InMemorySecretStore()
        XCTAssertThrowsError(try SharedNoticeStore.provision(
            hubID: "hub-m", deviceID: "dev-m", relayOrigin: "https://relay.example.test",
            hubPublicKey: "short", devicePrivateKey: Data(repeating: 1, count: 32), secrets: secrets))
        XCTAssertTrue(secrets.accounts.isEmpty)
    }

    func testRelayCredentialsNeedTheBearerAndAnOrigin() throws {
        let secrets = InMemorySecretStore()
        try SharedNoticeStore.save(key: Data(repeating: 9, count: 32),
                                   context: NoticeRelayContext(origin: "https://relay.example.test", hubID: "h", deviceID: "d"),
                                   secrets: secrets)
        XCTAssertNil(SharedNoticeStore.relayCredentials(secrets: secrets))

        try secrets.save("bearer-abc", account: PairingSecretAccount.deviceBearer)
        let credentials = try XCTUnwrap(SharedNoticeStore.relayCredentials(secrets: secrets))
        XCTAssertEqual(credentials.origin.absoluteString, "https://relay.example.test")
        XCTAssertEqual(credentials.bearer, "bearer-abc")
    }

    func testRelayCredentialsFollowTheGenerationInTheNoticeContext() throws {
        let secrets = InMemorySecretStore()
        try secrets.save("legacy-bearer", account: PairingSecretAccount.deviceBearer)
        try secrets.save("old-bearer", account: PairingSecretAccount.deviceBearer(secretID: "old"))
        try secrets.save("new-bearer", account: PairingSecretAccount.deviceBearer(secretID: "new"))
        let context = NoticeRelayContext(origin: "https://relay.example.test", hubID: "h", deviceID: "d",
                                         secretID: "new")
        try SharedNoticeStore.save(key: Data(repeating: 9, count: 32), context: context, secrets: secrets)

        XCTAssertEqual(SharedNoticeStore.relayCredentials(secrets: secrets)?.bearer, "new-bearer")
        XCTAssertTrue(SharedNoticeStore.matches(hubID: "h", deviceID: "d", relayOrigin: context.origin,
                                                secretID: "new", secrets: secrets))
        XCTAssertFalse(SharedNoticeStore.matches(hubID: "h", deviceID: "d", relayOrigin: context.origin,
                                                 secretID: "old", secrets: secrets))
        try secrets.delete(PairingSecretAccount.deviceBearer(secretID: "new"))
        XCTAssertNil(SharedNoticeStore.relayCredentials(secrets: secrets), "never fall back to another pairing")
    }

    func testClearRemovesTheNoticeSecretsAndLeavesTheRest() throws {
        let secrets = InMemorySecretStore()
        try SharedNoticeStore.save(key: Data(repeating: 9, count: 32),
                                   context: NoticeRelayContext(origin: "https://relay.example.test", hubID: "h", deviceID: "d"),
                                   secrets: secrets)
        try secrets.save("bearer", account: PairingSecretAccount.deviceBearer)

        SharedNoticeStore.clear(secrets: secrets)

        XCTAssertNil(SharedNoticeStore.load(secrets: secrets))
        XCTAssertEqual(try secrets.read(PairingSecretAccount.deviceBearer), "bearer")
    }

    func testAKeyThatDoesNotDecodeIsTreatedAsMissing() throws {
        let secrets = InMemorySecretStore()
        try secrets.save("not-a-key", account: SharedNoticeAccount.key)
        try secrets.save(#"{"origin":"https://relay.example.test","hubID":"h","deviceID":"d"}"#,
                         account: SharedNoticeAccount.context)
        XCTAssertNil(SharedNoticeStore.load(secrets: secrets))
    }
}

// Observes the transaction boundaries without replacing the actual atomic record write.
private final class ObservedPairingSecrets: SecretStore, @unchecked Sendable {
    let base = InMemorySecretStore()
    var beforeRead: ((String) throws -> Void)?
    var afterSave: ((String) throws -> Void)?
    var beforeDelete: ((String) throws -> Void)?

    func accountNames() throws -> [String] { try base.accountNames() }
    func read(_ account: String) throws -> String? {
        try beforeRead?(account)
        return try base.read(account)
    }
    func save(_ value: String, account: String) throws {
        try base.save(value, account: account)
        try afterSave?(account)
    }
    func delete(_ account: String) throws {
        try beforeDelete?(account)
        try base.delete(account)
    }

    func snapshot() throws -> InMemorySecretStore {
        let snapshot = InMemorySecretStore()
        for account in base.accounts {
            if let value = try base.read(account) { try snapshot.save(value, account: account) }
        }
        return snapshot
    }
}

final class PairingVaultReplacementTests: XCTestCase {
    func testStagingKeepsOldSessionReadableAndDeletesOldSecretsOnlyAfterCommit() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = ObservedPairingSecrets()
        let vault = PairingVault(directory: directory, secrets: secrets)
        let oldKey = RelayCrypto.makeDeviceKeyPair().privateKey
        let newKey = RelayCrypto.makeDeviceKeyPair().privateKey
        let paired = RelayCryptoTests.sampleRecord()
        try vault.store(paired, devicePrivateKey: RelayCrypto.base64URLEncode(oldKey), bearer: "old-bearer")
        let oldRecord = try XCTUnwrap(vault.current())
        let oldSession = try vault.session()
        let oldNotices = SharedNoticeStore.load(secrets: secrets)
        let oldPrivateAccount = PairingSecretAccount.devicePrivateKey(secretID: oldRecord.secretID)
        let oldBearerAccount = PairingSecretAccount.deviceBearer(secretID: oldRecord.secretID)
        var observedStaging = false
        var observedCleanup = 0
        secrets.afterSave = { account in
            guard PairingSecretAccount.isDeviceBearer(account) else { return }
            observedStaging = true
            // A separate snapshot simulates a restart; its startup sweep must not race the
            // live transaction whose staging boundary this callback observes.
            let reopened = PairingVault(directory: directory, secrets: try secrets.snapshot())
            XCTAssertEqual(try reopened.session().credentials, oldSession.credentials)
            XCTAssertEqual(try reopened.session().keys, oldSession.keys)
            XCTAssertEqual(SharedNoticeStore.load(secrets: secrets), oldNotices)
        }
        secrets.beforeDelete = { account in
            guard account == oldPrivateAccount || account == oldBearerAccount else { return }
            observedCleanup += 1
            let reopened = PairingVault(directory: directory, secrets: try secrets.snapshot())
            XCTAssertEqual(try reopened.session().credentials.bearer, "new-bearer")
            XCTAssertEqual(try reopened.devicePrivateKey(), newKey)
        }

        // Even a caller supplying the same device ID must not overwrite a live account.
        try vault.store(paired, devicePrivateKey: RelayCrypto.base64URLEncode(newKey), bearer: "new-bearer")

        XCTAssertTrue(observedStaging)
        XCTAssertEqual(observedCleanup, 2)
        XCTAssertNotEqual(vault.current()?.secretID, oldRecord.secretID)
        XCTAssertNil(try secrets.read(oldPrivateAccount))
        XCTAssertNil(try secrets.read(oldBearerAccount))
        XCTAssertEqual(SharedNoticeStore.relayCredentials(secrets: secrets)?.bearer, "new-bearer")
        XCTAssertEqual(SharedNoticeStore.load(secrets: secrets)?.context.secretID, vault.current()?.secretID)
    }

    func testFailedSecretWriteDiscardsStagedAccountsAndReopensTheOldSession() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = ObservedPairingSecrets()
        let vault = PairingVault(directory: directory, secrets: secrets)
        try vault.store(RelayCryptoTests.sampleRecord(),
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "old-bearer")
        let oldSession = try vault.session()
        let oldRecord = vault.current()
        let oldAccounts = secrets.base.accounts
        secrets.afterSave = { account in
            if PairingSecretAccount.isDeviceBearer(account) {
                throw YansuError.message("Injected staging failure")
            }
        }
        var replacement = RelayCryptoTests.sampleRecord()
        replacement.deviceID = "replacement-device"

        XCTAssertThrowsError(try vault.store(replacement,
            devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
            bearer: "new-bearer"))

        XCTAssertEqual(vault.current(), oldRecord)
        XCTAssertEqual(secrets.base.accounts, oldAccounts)
        XCTAssertTrue(try vault.pendingDeviceRevocations().isEmpty)
        let reopened = PairingVault(directory: directory, secrets: secrets)
        XCTAssertEqual(try reopened.session().credentials, oldSession.credentials)
        XCTAssertEqual(try reopened.session().keys, oldSession.keys)
    }

    func testFailedRecordCommitLeavesThePersistedPairingAndSecretsTogether() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        var failBeforeWrite = false
        let vault = PairingVault(directory: directory, secrets: secrets, writeRecord: { data, url in
            if failBeforeWrite { throw YansuError.message("Failure before atomic replacement") }
            try data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        })
        try vault.store(RelayCryptoTests.sampleRecord(),
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "old-bearer")
        let oldSession = try vault.session()
        let oldRecord = vault.current()
        let oldAccounts = secrets.accounts
        failBeforeWrite = true
        var replacement = RelayCryptoTests.sampleRecord()
        replacement.deviceID = "replacement-device"

        XCTAssertThrowsError(try vault.store(replacement,
            devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
            bearer: "new-bearer"))

        XCTAssertEqual(vault.current(), oldRecord)
        XCTAssertNil(vault.loadError)
        XCTAssertEqual(secrets.accounts, oldAccounts)
        XCTAssertTrue(try vault.pendingDeviceRevocations().isEmpty)
        let reopened = PairingVault(directory: directory, secrets: secrets)
        XCTAssertEqual(try reopened.session().credentials, oldSession.credentials)
        XCTAssertEqual(try reopened.session().keys, oldSession.keys)
    }

    func testInterruptedCleanupReopensCommittedGenerationInsteadOfStaleSecrets() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = ObservedPairingSecrets()
        let vault = PairingVault(directory: directory, secrets: secrets)
        try vault.store(RelayCryptoTests.sampleRecord(),
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "old-bearer")
        let oldRecord = try XCTUnwrap(vault.current())
        secrets.beforeDelete = { _ in throw YansuError.message("Cleanup interrupted") }
        let newKey = RelayCrypto.makeDeviceKeyPair().privateKey

        try vault.store(RelayCryptoTests.sampleRecord(), devicePrivateKey: RelayCrypto.base64URLEncode(newKey),
                        bearer: "new-bearer")

        XCTAssertEqual(try secrets.read(PairingSecretAccount.deviceBearer(secretID: oldRecord.secretID)), "old-bearer")
        secrets.beforeDelete = nil
        let reopened = PairingVault(directory: directory, secrets: secrets)
        XCTAssertEqual(try reopened.session().credentials.bearer, "new-bearer")
        XCTAssertEqual(try reopened.devicePrivateKey(), newKey)
        XCTAssertEqual(SharedNoticeStore.relayCredentials(secrets: secrets)?.bearer, "new-bearer")
        XCTAssertNil(try secrets.read(PairingSecretAccount.deviceBearer(secretID: oldRecord.secretID)))
    }

    func testLegacyRecordReadsFixedSecretsUntilAReplacementCommits() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        let oldKey = RelayCrypto.makeDeviceKeyPair().privateKey
        let paired = RelayCryptoTests.sampleRecord()
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        try encoder.encode(paired).write(to: directory.appendingPathComponent("pairing.json"))
        try secrets.save(RelayCrypto.base64URLEncode(oldKey), account: PairingSecretAccount.devicePrivateKey)
        try secrets.save("legacy-bearer", account: PairingSecretAccount.deviceBearer)
        let vault = PairingVault(directory: directory, secrets: secrets)

        XCTAssertNil(vault.current()?.secretID)
        XCTAssertEqual(try vault.session().credentials.bearer, "legacy-bearer")
        XCTAssertEqual(try vault.devicePrivateKey(), oldKey)
        vault.reconcileNoticeSecrets()
        XCTAssertEqual(SharedNoticeStore.relayCredentials(secrets: secrets)?.bearer, "legacy-bearer")
        try vault.store(paired,
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "new-bearer")

        XCTAssertNotNil(vault.current()?.secretID)
        XCTAssertNil(try secrets.read(PairingSecretAccount.devicePrivateKey))
        XCTAssertNil(try secrets.read(PairingSecretAccount.deviceBearer))
        let reopened = PairingVault(directory: directory, secrets: secrets)
        XCTAssertEqual(try reopened.session().credentials.bearer, "new-bearer")
    }

    func testOldDeviceCannotReadOrChangeReplacementSequenceCounters() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let vault = PairingVault(directory: directory, secrets: InMemorySecretStore())
        var paired = RelayCryptoTests.sampleRecord()
        let oldDeviceID = paired.deviceID
        paired.deviceID = "replacement-device"
        try vault.store(paired,
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "new-bearer")
        let oldRecord = vault.current()
        let recordURL = directory.appendingPathComponent("pairing.json")
        let oldData = try Data(contentsOf: recordURL)

        XCTAssertThrowsError(try vault.lastAcceptedMessage(deviceID: oldDeviceID)) {
            XCTAssertEqual($0 as? HubRequestError, .notPaired)
        }
        XCTAssertThrowsError(try vault.nextSendSeq(deviceID: oldDeviceID)) {
            XCTAssertEqual($0 as? HubRequestError, .notPaired)
        }
        XCTAssertThrowsError(try vault.acceptMessage(seq: 50, deviceID: oldDeviceID)) {
            XCTAssertEqual($0 as? HubRequestError, .notPaired)
        }
        XCTAssertThrowsError(try vault.acceptBrief(seq: 50, deviceID: oldDeviceID)) {
            XCTAssertEqual($0 as? HubRequestError, .notPaired)
        }
        XCTAssertEqual(vault.current(), oldRecord)
        XCTAssertEqual(try Data(contentsOf: recordURL), oldData)
        XCTAssertEqual(try vault.lastAcceptedMessage(deviceID: paired.deviceID), 0)
        XCTAssertEqual(try vault.nextSendSeq(deviceID: paired.deviceID), 1)
        try vault.acceptMessage(seq: 2, deviceID: paired.deviceID)
        XCTAssertEqual(try vault.lastAcceptedMessage(deviceID: paired.deviceID), 2)
        XCTAssertEqual(try vault.acceptBrief(seq: 3, deviceID: paired.deviceID), .fresh)
    }

    func testWipeRemovesTheCurrentGenerationAndNoticeSecrets() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        let vault = PairingVault(directory: directory, secrets: secrets)
        try vault.store(RelayCryptoTests.sampleRecord(),
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "bearer")

        try vault.wipe()

        XCTAssertTrue(secrets.accounts.isEmpty)
        XCTAssertNil(PairingVault(directory: directory, secrets: secrets).current())
    }

    func testStartupSweepsUncommittedGenerationsAndRetainsOnlyTheCurrentPairing() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        let vault = PairingVault(directory: directory, secrets: secrets)
        try vault.store(RelayCryptoTests.sampleRecord(),
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "current-bearer")
        let committed = try XCTUnwrap(vault.current())
        let expectedSession = try vault.session()
        try seedUnusedSecrets(secrets)
        try secrets.save("keep", account: "unrelated-account")

        let reopened = PairingVault(directory: directory, secrets: secrets)

        XCTAssertEqual(try reopened.session().credentials, expectedSession.credentials)
        XCTAssertEqual(try reopened.session().keys, expectedSession.keys)
        let pairingAccounts = secrets.accounts.filter {
            PairingSecretAccount.isDevicePrivateKey($0) || PairingSecretAccount.isDeviceBearer($0)
        }
        XCTAssertEqual(Set(pairingAccounts), Set([
            PairingSecretAccount.devicePrivateKey(secretID: committed.secretID),
            PairingSecretAccount.deviceBearer(secretID: committed.secretID)]))
        XCTAssertEqual(try secrets.read("unrelated-account"), "keep")
        XCTAssertTrue(reopened.hasNoticeSecrets())
    }

    func testMissingAndReinstalledRecordsSweepAllPairingGenerations() throws {
        for state in ["missing", "reinstalled"] {
            let directory = try makeTemporaryDirectory()
            defer { try? FileManager.default.removeItem(at: directory) }
            let secrets = InMemorySecretStore()
            if state != "missing" {
                let vault = PairingVault(directory: directory, secrets: secrets)
                try vault.store(RelayCryptoTests.sampleRecord(),
                    devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                    bearer: "old-bearer")
            }
            try seedUnusedSecrets(secrets)
            try secrets.save("keep", account: "unrelated-account")
            if state == "reinstalled" {
                try FileManager.default.removeItem(at: directory)
            }

            let reopened = PairingVault(directory: directory, secrets: secrets)
            XCTAssertNil(reopened.current(), state)
            XCTAssertNil(reopened.loadError, state)
            XCTAssertEqual(secrets.accounts, ["unrelated-account"], state)
        }
    }

    func testCorruptAndDowngradeIncompatibleRecordsPreserveSecretsUntilRecovery() throws {
        for state in ["corrupt", "newer-schema"] {
            let directory = try makeTemporaryDirectory()
            defer { try? FileManager.default.removeItem(at: directory) }
            let secrets = InMemorySecretStore()
            let initial = PairingVault(directory: directory, secrets: secrets)
            let paired = RelayCryptoTests.sampleRecord()
            try initial.store(paired,
                devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                bearer: "current-bearer")
            let session = try initial.session()
            let recordURL = directory.appendingPathComponent("pairing.json")
            let readableData = try Data(contentsOf: recordURL)
            let unreadableData: Data
            if state == "corrupt" {
                unreadableData = Data("broken record".utf8)
            } else {
                var object = try XCTUnwrap(JSONSerialization.jsonObject(with: readableData) as? [String: Any])
                object["counters"] = ["sendSeq": "newer-counter-format"]
                unreadableData = try JSONSerialization.data(withJSONObject: object)
            }
            try unreadableData.write(to: recordURL)
            try seedUnusedSecrets(secrets)
            let before = try secretValues(secrets)
            let notices = SharedNoticeStore.load(secrets: secrets)
            let reopened = PairingVault(directory: directory, secrets: secrets)

            XCTAssertNil(reopened.current(), state)
            XCTAssertEqual(reopened.loadError, .unreadableRecord, state)
            XCTAssertThrowsError(try reopened.session()) {
                XCTAssertEqual($0 as? PairingVaultError, .unreadableRecord)
            }
            XCTAssertThrowsError(try reopened.pendingDeviceRevocations())
            XCTAssertThrowsError(try reopened.devicePrivateKey())
            XCTAssertThrowsError(try reopened.nextSendSeq(deviceID: paired.deviceID))
            XCTAssertThrowsError(try reopened.acceptMessage(seq: 1, deviceID: paired.deviceID))
            XCTAssertThrowsError(try reopened.acceptBrief(seq: 1, deviceID: paired.deviceID))
            XCTAssertThrowsError(try reopened.store(RelayCryptoTests.sampleRecord(),
                devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                bearer: "replacement-bearer")) {
                XCTAssertEqual($0 as? PairingVaultError, .unreadableRecord)
            }
            reopened.reconcileNoticeSecrets()
            XCTAssertEqual(try secretValues(secrets), before, state)
            XCTAssertEqual(SharedNoticeStore.load(secrets: secrets), notices, state)
            XCTAssertEqual(try Data(contentsOf: recordURL), unreadableData, state)

            try readableData.write(to: recordURL)
            XCTAssertEqual(reopened.retryLoading()?.deviceID, paired.deviceID, state)
            XCTAssertNil(reopened.loadError, state)
            XCTAssertEqual(try reopened.session().credentials, session.credentials, state)
            XCTAssertEqual(try reopened.session().keys, session.keys, state)
        }
    }

    func testTransientRecordReadFailureKeepsAllSecretsAndRecoversOnlyOnExplicitRetry() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        let initial = PairingVault(directory: directory, secrets: secrets)
        let paired = RelayCryptoTests.sampleRecord()
        try initial.store(paired,
            devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
            bearer: "current-bearer")
        let session = try initial.session()
        try seedUnusedSecrets(secrets)
        let before = try secretValues(secrets)
        let recordURL = directory.appendingPathComponent("pairing.json")
        let originalData = try Data(contentsOf: recordURL)
        var inaccessible = true
        var readCount = 0
        let reopened = PairingVault(directory: directory, secrets: secrets, readRecord: { url in
            readCount += 1
            if inaccessible { throw NSError(domain: NSPOSIXErrorDomain, code: Int(EIO)) }
            return try Data(contentsOf: url)
        })

        XCTAssertNil(reopened.current())
        XCTAssertEqual(reopened.loadError, .unreadableRecord)
        XCTAssertThrowsError(try reopened.session())
        XCTAssertEqual(readCount, 1)
        XCTAssertEqual(try secretValues(secrets), before)
        XCTAssertEqual(try Data(contentsOf: recordURL), originalData)

        inaccessible = false
        XCTAssertEqual(reopened.retryLoading()?.deviceID, paired.deviceID)
        XCTAssertEqual(readCount, 2)
        XCTAssertNil(reopened.loadError)
        XCTAssertEqual(try reopened.session().credentials, session.credentials)
        XCTAssertEqual(try reopened.session().keys, session.keys)
    }

    func testOnlyExplicitMissingFileErrorsPermitOrphanSweeps() throws {
        let denied = NSError(domain: NSPOSIXErrorDomain, code: Int(EACCES))
        let cases: [(NSError, Bool)] = [
            (NSError(domain: NSPOSIXErrorDomain, code: Int(ENOENT)), true),
            (NSError(domain: NSCocoaErrorDomain, code: CocoaError.Code.fileReadNoSuchFile.rawValue), true),
            (NSError(domain: NSCocoaErrorDomain, code: CocoaError.Code.fileNoSuchFile.rawValue), true),
            (NSError(domain: NSCocoaErrorDomain, code: CocoaError.Code.fileReadNoPermission.rawValue), false),
            (NSError(domain: NSCocoaErrorDomain, code: CocoaError.Code.fileReadNoSuchFile.rawValue,
                     userInfo: [NSUnderlyingErrorKey: denied]), false),
            (denied, false),
            (NSError(domain: NSPOSIXErrorDomain, code: Int(EIO)), false),
            (NSError(domain: NSPOSIXErrorDomain, code: Int(ENOTDIR)), false),
        ]
        for (error, maySweep) in cases {
            let directory = try makeTemporaryDirectory()
            defer { try? FileManager.default.removeItem(at: directory) }
            let secrets = InMemorySecretStore()
            try seedUnusedSecrets(secrets)
            try SharedNoticeStore.save(key: Data(repeating: 1, count: 32),
                context: NoticeRelayContext(origin: "https://relay.example", hubID: "hub", deviceID: "device"),
                secrets: secrets)
            let before = try secretValues(secrets)
            // fileExists is false in every case. Only a definite missing-file error allows cleanup.
            let vault = PairingVault(directory: directory, secrets: secrets, readRecord: { _ in throw error })

            XCTAssertNil(vault.current())
            if maySweep {
                XCTAssertNil(vault.loadError)
                XCTAssertTrue(secrets.accounts.isEmpty)
            } else {
                XCTAssertEqual(vault.loadError, .unreadableRecord)
                XCTAssertEqual(try secretValues(secrets), before)
            }
        }
    }

    func testMissingFileErrorPreservesAllSecretsWhenTheRecordExists() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        let initial = PairingVault(directory: directory, secrets: secrets)
        let paired = RelayCryptoTests.sampleRecord()
        try initial.store(paired,
            devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
            bearer: "current-bearer")
        try seedUnusedSecrets(secrets)
        let before = try secretValues(secrets)
        let notices = SharedNoticeStore.load(secrets: secrets)
        let recordURL = directory.appendingPathComponent("pairing.json")
        let originalData = try Data(contentsOf: recordURL)
        var reportMissing = true
        let reopened = PairingVault(directory: directory, secrets: secrets, readRecord: { url in
            if reportMissing { throw NSError(domain: NSPOSIXErrorDomain, code: Int(ENOENT)) }
            return try Data(contentsOf: url)
        })

        XCTAssertNil(reopened.current())
        XCTAssertEqual(reopened.loadError, .unreadableRecord)
        XCTAssertThrowsError(try reopened.session())
        XCTAssertThrowsError(try reopened.store(paired, devicePrivateKey: "replacement-key", bearer: "replacement-bearer"))
        XCTAssertEqual(try secretValues(secrets), before)
        XCTAssertEqual(SharedNoticeStore.load(secrets: secrets), notices)
        XCTAssertEqual(try Data(contentsOf: recordURL), originalData)

        reportMissing = false
        XCTAssertEqual(reopened.retryLoading()?.deviceID, paired.deviceID)
        XCTAssertEqual(try reopened.session().credentials.bearer, "current-bearer")
    }

    func testUncertainCommitKeepsBothGenerationsAndBlocksRevocationUntilTheRecordCanBeRead() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        var failureAfterWrite = false
        var readUnavailable = false
        let vault = PairingVault(directory: directory, secrets: secrets, readRecord: { url in
            if readUnavailable { throw NSError(domain: NSPOSIXErrorDomain, code: Int(ENOENT)) }
            return try Data(contentsOf: url)
        }, writeRecord: { data, url in
            try data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            if failureAfterWrite {
                readUnavailable = true
                throw YansuError.message("Failure after durable replacement")
            }
        })
        var paired = RelayCryptoTests.sampleRecord()
        try vault.store(paired,
            devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
            bearer: "old-bearer")
        let oldRecord = try XCTUnwrap(vault.current())
        let notices = SharedNoticeStore.load(secrets: secrets)
        paired.deviceID = "replacement-device"
        failureAfterWrite = true

        XCTAssertThrowsError(try vault.store(paired,
            devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
            bearer: "new-bearer"))
        XCTAssertEqual(vault.loadError, .unreadableRecord)
        XCTAssertThrowsError(try vault.session())
        XCTAssertThrowsError(try vault.pendingDeviceRevocations())
        XCTAssertEqual(SharedNoticeStore.load(secrets: secrets), notices)
        XCTAssertEqual(try secrets.read(PairingSecretAccount.deviceBearer(secretID: oldRecord.secretID)), "old-bearer")
        XCTAssertEqual(secrets.accounts.filter(PairingSecretAccount.isDeviceBearer).count, 2)
        XCTAssertEqual(secrets.accounts.filter(PairingSecretAccount.isDevicePrivateKey).count, 2)

        readUnavailable = false
        failureAfterWrite = false
        XCTAssertEqual(vault.retryLoading()?.deviceID, paired.deviceID)
        XCTAssertNil(vault.loadError)
        XCTAssertEqual(try vault.session().credentials.bearer, "new-bearer")
        XCTAssertEqual(try vault.pendingDeviceRevocations().map(\.deviceID), [oldRecord.deviceID])
    }

    func testAtomicReplacementThatReportsFailureKeepsCommittedSecretsAndPublishesPairing() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        var failAfterWrite = false
        let vault = PairingVault(directory: directory, secrets: secrets, writeRecord: { data, url in
            try data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            if failAfterWrite { throw YansuError.message("Failure after durable replacement") }
        })
        var paired = RelayCryptoTests.sampleRecord()
        let oldDeviceID = paired.deviceID
        try vault.store(paired,
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "old-bearer")
        let newKey = RelayCrypto.makeDeviceKeyPair().privateKey
        paired.deviceID = "replacement-device"
        failAfterWrite = true

        XCTAssertNoThrow(try vault.store(paired, devicePrivateKey: RelayCrypto.base64URLEncode(newKey),
                                        bearer: "new-bearer"))

        XCTAssertEqual(vault.current()?.deviceID, paired.deviceID)
        XCTAssertEqual(try vault.session().credentials.bearer, "new-bearer")
        let reopened = PairingVault(directory: directory, secrets: secrets)
        XCTAssertEqual(try reopened.devicePrivateKey(), newKey)
        XCTAssertEqual(try reopened.session().credentials.bearer, "new-bearer")
        XCTAssertEqual(try reopened.pendingDeviceRevocations().map(\.deviceID), [oldDeviceID])
    }

    func testReplacementQueuesOldBearerEvenWhenItsPrivateKeyCannotOpenASession() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        let first = PairingVault(directory: directory, secrets: secrets)
        var paired = RelayCryptoTests.sampleRecord()
        let oldDeviceID = paired.deviceID
        try first.store(paired,
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "old-bearer")
        let oldRecord = try XCTUnwrap(first.current())
        try secrets.delete(PairingSecretAccount.devicePrivateKey(secretID: oldRecord.secretID))
        let vault = PairingVault(directory: directory, secrets: secrets)
        XCTAssertThrowsError(try vault.session())
        paired.deviceID = "replacement-device"
        try vault.store(paired,
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "new-bearer")

        let reopened = PairingVault(directory: directory, secrets: secrets)
        let pending = try XCTUnwrap(reopened.pendingDeviceRevocations().first)
        XCTAssertNil(reopened.current()?.previousDeviceRemovalNeeded)
        XCTAssertEqual(pending.deviceID, oldDeviceID)
        XCTAssertEqual(pending.credentials.bearer, "old-bearer")
        XCTAssertEqual(pending.credentials.origin.absoluteString, oldRecord.relayOrigin)
        let json = try String(contentsOf: directory.appendingPathComponent("pairing.json"), encoding: .utf8)
        XCTAssertFalse(json.contains("old-bearer"))
        try reopened.completeDeviceRevocation(deviceID: oldDeviceID)
        XCTAssertTrue(try reopened.pendingDeviceRevocations().isEmpty)
        XCTAssertNil(try secrets.read(PairingSecretAccount.pendingRevocations))
    }

    func testMissingOrEmptyOldBearerAllowsReplacementAndPersistsManualRemovalGuidance() throws {
        for missingBearer in [nil, ""] as [String?] {
            let directory = try makeTemporaryDirectory()
            defer { try? FileManager.default.removeItem(at: directory) }
            let secrets = InMemorySecretStore()
            let initial = PairingVault(directory: directory, secrets: secrets)
            var paired = RelayCryptoTests.sampleRecord()
            try initial.store(paired,
                devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                bearer: "old-bearer")
            let original = try XCTUnwrap(initial.current())
            let oldBearerAccount = PairingSecretAccount.deviceBearer(secretID: original.secretID)
            try secrets.delete(PairingSecretAccount.devicePrivateKey(secretID: original.secretID))
            if let missingBearer {
                try secrets.save(missingBearer, account: oldBearerAccount)
            } else {
                try secrets.delete(oldBearerAccount)
            }
            let vault = PairingVault(directory: directory, secrets: secrets)
            XCTAssertThrowsError(try vault.session())
            paired.deviceID = "replacement-device"
            let replacementKey = RelayCrypto.makeDeviceKeyPair().privateKey

            try vault.store(paired, devicePrivateKey: RelayCrypto.base64URLEncode(replacementKey), bearer: "new-bearer")

            XCTAssertEqual(vault.current()?.deviceID, paired.deviceID)
            XCTAssertEqual(vault.current()?.previousDeviceRemovalNeeded, true)
            XCTAssertEqual(try vault.session().credentials.bearer, "new-bearer")
            XCTAssertTrue(try vault.pendingDeviceRevocations().isEmpty)
            let reopened = PairingVault(directory: directory, secrets: secrets)
            XCTAssertEqual(reopened.current()?.previousDeviceRemovalNeeded, true)
            XCTAssertEqual(try reopened.devicePrivateKey(), replacementKey)
            XCTAssertEqual(try reopened.session().credentials.bearer, "new-bearer")
            XCTAssertTrue(try reopened.pendingDeviceRevocations().isEmpty)
            XCTAssertEqual(SharedNoticeStore.relayCredentials(secrets: secrets)?.bearer, "new-bearer")
        }
    }

    func testOldBearerReadFailurePreservesPairingAndSecretsWithoutTreatingItAsMissing() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = ObservedPairingSecrets()
        let vault = PairingVault(directory: directory, secrets: secrets)
        var paired = RelayCryptoTests.sampleRecord()
        try vault.store(paired,
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "old-bearer")
        let original = try XCTUnwrap(vault.current())
        let originalSession = try vault.session()
        let originalSecrets = try secretValues(secrets.base)
        let recordURL = directory.appendingPathComponent("pairing.json")
        let originalData = try Data(contentsOf: recordURL)
        let readError = YansuError.message("Injected Keychain read failure")
        secrets.beforeRead = { account in
            if account == PairingSecretAccount.deviceBearer(secretID: original.secretID) { throw readError }
        }
        paired.deviceID = "replacement-device"

        XCTAssertThrowsError(try vault.store(paired,
            devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
            bearer: "new-bearer")) {
            XCTAssertEqual($0 as? YansuError, readError)
        }
        XCTAssertEqual(vault.current(), original)
        XCTAssertEqual(try Data(contentsOf: recordURL), originalData)
        XCTAssertEqual(try secretValues(secrets.base), originalSecrets)
        XCTAssertTrue(try vault.pendingDeviceRevocations().isEmpty)
        secrets.beforeRead = nil
        let reopened = PairingVault(directory: directory, secrets: secrets)
        XCTAssertNil(reopened.current()?.previousDeviceRemovalNeeded)
        XCTAssertEqual(try reopened.session().credentials, originalSession.credentials)
        XCTAssertEqual(try reopened.session().keys, originalSession.keys)
    }

    func testPendingRevokesSurviveWipeButNeverIncludeTheCurrentDevice() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        let vault = PairingVault(directory: directory, secrets: secrets)
        let paired = RelayCryptoTests.sampleRecord()
        try vault.store(paired,
                        devicePrivateKey: RelayCrypto.base64URLEncode(RelayCrypto.makeDeviceKeyPair().privateKey),
                        bearer: "current-bearer")
        try vault.enqueueDeviceRevocation(deviceID: paired.deviceID, credentials: vault.session().credentials)
        try vault.enqueueDeviceRevocation(deviceID: "abandoned-device",
            credentials: RelayCredentials(origin: URL(string: paired.relayOrigin)!, bearer: "abandoned-bearer"))
        XCTAssertEqual(try vault.pendingDeviceRevocations().map(\.deviceID), ["abandoned-device"])

        try vault.wipe()

        let reopened = PairingVault(directory: directory, secrets: secrets)
        XCTAssertNil(reopened.current())
        XCTAssertEqual(Set(try reopened.pendingDeviceRevocations().map(\.deviceID)),
                       [paired.deviceID, "abandoned-device"])
        XCTAssertEqual(secrets.accounts, [PairingSecretAccount.pendingRevocations])
    }

    func testUnreadableRevocationQueuePropagatesFailureWithoutOverwritingIt() throws {
        let directory = try makeTemporaryDirectory()
        defer { try? FileManager.default.removeItem(at: directory) }
        let secrets = InMemorySecretStore()
        try secrets.save("broken queue", account: PairingSecretAccount.pendingRevocations)
        let vault = PairingVault(directory: directory, secrets: secrets)

        XCTAssertThrowsError(try vault.pendingDeviceRevocations())
        XCTAssertThrowsError(try vault.enqueueDeviceRevocation(deviceID: "abandoned-device",
            credentials: RelayCredentials(origin: URL(string: "https://relay.example")!, bearer: "bearer")))
        XCTAssertEqual(try secrets.read(PairingSecretAccount.pendingRevocations), "broken queue")
    }

    private func seedUnusedSecrets(_ secrets: InMemorySecretStore) throws {
        try secrets.save("staged-key", account: PairingSecretAccount.devicePrivateKey(secretID: "uncommitted"))
        try secrets.save("staged-bearer", account: PairingSecretAccount.deviceBearer(secretID: "uncommitted"))
        try secrets.save("legacy-key", account: PairingSecretAccount.devicePrivateKey)
        try secrets.save("legacy-bearer", account: PairingSecretAccount.deviceBearer)
    }

    private func secretValues(_ secrets: InMemorySecretStore) throws -> [String: String] {
        var values: [String: String] = [:]
        for account in secrets.accounts { values[account] = try secrets.read(account) }
        return values
    }
}
