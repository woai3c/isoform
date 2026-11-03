import Foundation
import XCTest
@testable import Yansu

// Brief files move from Application Support into the App Group container, once. Uses temporary folders only.
final class BriefCacheMigrationTests: XCTestCase {
    private func write(_ text: String, to directory: URL, name: String) throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data(text.utf8).write(to: directory.appendingPathComponent(name))
    }

    private func read(_ directory: URL, name: String) -> String? {
        (try? Data(contentsOf: directory.appendingPathComponent(name))).map { String(decoding: $0, as: UTF8.self) }
    }

    func testMovesBriefFilesAndRemovesTheEmptyLegacyFolder() throws {
        let root = try makeTemporaryDirectory()
        let legacy = root.appendingPathComponent("legacy", isDirectory: true)
        let destination = root.appendingPathComponent("container/briefs", isDirectory: true)
        try write("one", to: legacy, name: "a1.json")
        try write("two", to: legacy, name: "b2.json")

        BriefCacheMigration.moveLegacyFiles(from: legacy, to: destination)

        XCTAssertEqual(read(destination, name: "a1.json"), "one")
        XCTAssertEqual(read(destination, name: "b2.json"), "two")
        XCTAssertFalse(FileManager.default.fileExists(atPath: legacy.path))
    }

    func testAFileAlreadyInTheDestinationKeepsItsCopy() throws {
        let root = try makeTemporaryDirectory()
        let legacy = root.appendingPathComponent("legacy", isDirectory: true)
        let destination = root.appendingPathComponent("container", isDirectory: true)
        try write("old", to: legacy, name: "same.json")
        try write("new", to: destination, name: "same.json")

        BriefCacheMigration.moveLegacyFiles(from: legacy, to: destination)

        XCTAssertEqual(read(destination, name: "same.json"), "new")
        XCTAssertFalse(FileManager.default.fileExists(atPath: legacy.path))
    }

    func testNonBriefFilesStayWhereTheyAre() throws {
        let root = try makeTemporaryDirectory()
        let legacy = root.appendingPathComponent("legacy", isDirectory: true)
        let destination = root.appendingPathComponent("container", isDirectory: true)
        try write("keep", to: legacy, name: "notes.txt")
        try write("brief", to: legacy, name: "c3.json")

        BriefCacheMigration.moveLegacyFiles(from: legacy, to: destination)

        XCTAssertEqual(read(legacy, name: "notes.txt"), "keep")
        XCTAssertEqual(read(destination, name: "c3.json"), "brief")
    }

    func testTheSameFolderIsLeftAlone() throws {
        let root = try makeTemporaryDirectory()
        let folder = root.appendingPathComponent("briefs", isDirectory: true)
        try write("kept", to: folder, name: "d4.json")

        BriefCacheMigration.moveLegacyFiles(from: folder, to: folder)

        XCTAssertEqual(read(folder, name: "d4.json"), "kept")
    }

    func testAMissingLegacyFolderIsANoOp() throws {
        let root = try makeTemporaryDirectory()
        let destination = root.appendingPathComponent("container", isDirectory: true)

        BriefCacheMigration.moveLegacyFiles(from: root.appendingPathComponent("absent", isDirectory: true),
                                            to: destination)

        XCTAssertFalse(FileManager.default.fileExists(atPath: destination.path))
    }
}
