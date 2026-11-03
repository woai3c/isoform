import Darwin
import Foundation
import XCTest
@testable import Yansu

@MainActor
final class RemoteAccessTests: XCTestCase {
    func testSwitchingTicketsRoutesBytesOnlyThroughTheMatchingTunnel() async throws {
        let tunnel = TicketRecordingTunnel()
        let gate = ListenerStartGate()
        let access = RemoteAccess(
            tunnel: tunnel, dialTimeout: .seconds(3),
            beforeListenerReady: { ticket in
                if ticket == "ticket-B" { await gate.pause(ticket: ticket) }
            }
        )
        defer {
            access.deactivate()
            Task { await gate.release(ticket: "ticket-B") }
        }

        let firstResult = RemoteAccessTestResult<URL?>()
        Task { firstResult.resolve(await access.origin(ticket: "ticket-A")) }
        let firstValue = try await firstResult.wait(timeout: .seconds(5))
        let first = try XCTUnwrap(firstValue)
        let firstResponse = try await socketRoundTrip(origin: first, payload: Data("request-A".utf8))
        XCTAssertEqual(firstResponse, Data("response-ticket-A".utf8))
        let bytesOnA = tunnel.receivedBytes(for: "ticket-A")
        XCTAssertEqual(bytesOnA, Data("request-A".utf8))

        access.activate(ticket: "ticket-B")
        XCTAssertNil(access.localOrigin(ticket: "ticket-B"))
        try await gate.waitForEntry(ticket: "ticket-B", timeout: .seconds(5))
        XCTAssertNil(access.localOrigin(ticket: "ticket-B"))
        let portReservation = try await reservePort(first.port)
        defer { withExtendedLifetime(portReservation) {} }
        await gate.release(ticket: "ticket-B")

        let secondResult = RemoteAccessTestResult<URL?>()
        Task { secondResult.resolve(await access.origin(ticket: "ticket-B")) }
        let secondValue = try await secondResult.wait(timeout: .seconds(5))
        let second = try XCTUnwrap(secondValue)
        XCTAssertNotEqual(second.port, first.port)
        let secondResponse = try await socketRoundTrip(origin: second, payload: Data("request-B".utf8))

        XCTAssertEqual(secondResponse, Data("response-ticket-B".utf8))
        XCTAssertEqual(tunnel.connectTickets, ["ticket-A", "ticket-B"])
        XCTAssertEqual(tunnel.receivedBytes(for: "ticket-A"), bytesOnA)
        XCTAssertEqual(tunnel.receivedBytes(for: "ticket-B"), Data("request-B".utf8))
    }

    func testTwentyConcurrentOriginsForSameTicketShareOneListenerStart() async throws {
        let counter = ListenerStartCounter()
        let ticket = "ticket-A"
        let access = RemoteAccess(
            tunnel: TicketRecordingTunnel(), dialTimeout: .seconds(3),
            beforeListenerReady: { ticket in await counter.record(ticket: ticket) }
        )
        defer { access.deactivate() }

        let origins = try await withThrowingTaskGroup(of: URL?.self) { group in
            for _ in 0..<20 {
                group.addTask { await access.origin(ticket: ticket) }
            }
            var results: [URL?] = []
            for try await origin in group {
                results.append(origin)
            }
            return results
        }

        let nonNilOrigins = origins.compactMap { $0 }
        XCTAssertEqual(origins.count, 20)
        XCTAssertEqual(nonNilOrigins.count, 20)
        let origin = try XCTUnwrap(nonNilOrigins.first)
        XCTAssertTrue(nonNilOrigins.allSatisfy { $0 == origin })
        XCTAssertEqual(access.localOrigin(ticket: ticket), origin)
        let starts = await counter.count(ticket: ticket)
        XCTAssertEqual(starts, 1)
    }

    func testConcurrentOriginSwitchPublishesOnlyTheLatestTicket() async throws {
        let gate = ListenerStartGate()
        let access = RemoteAccess(
            tunnel: TicketRecordingTunnel(), dialTimeout: .seconds(3),
            beforeListenerReady: { ticket in await gate.pause(ticket: ticket) }
        )
        defer {
            access.deactivate()
            Task {
                await gate.release(ticket: "ticket-A")
                await gate.release(ticket: "ticket-B")
            }
        }

        let firstResult = RemoteAccessTestResult<URL?>()
        Task { firstResult.resolve(await access.origin(ticket: "ticket-A")) }
        try await gate.waitForEntry(ticket: "ticket-A", timeout: .seconds(5))

        let secondResult = RemoteAccessTestResult<URL?>()
        Task { secondResult.resolve(await access.origin(ticket: "ticket-B")) }
        try await gate.waitForEntry(ticket: "ticket-B", timeout: .seconds(5))

        XCTAssertNil(access.localOrigin(ticket: "ticket-B"))
        await gate.release(ticket: "ticket-A")
        let first = try await firstResult.wait(timeout: .seconds(5))
        XCTAssertNil(first)
        XCTAssertNil(access.localOrigin(ticket: "ticket-B"))

        await gate.release(ticket: "ticket-B")
        let secondValue = try await secondResult.wait(timeout: .seconds(5))
        let second = try XCTUnwrap(secondValue)
        XCTAssertEqual(access.localOrigin(ticket: "ticket-B"), second)
        XCTAssertNil(access.localOrigin(ticket: "ticket-A"))
    }

    func testActivatingAThenBPublishesOnlyTheBOrigin() async throws {
        let gate = ListenerStartGate()
        let access = RemoteAccess(
            tunnel: TicketRecordingTunnel(), dialTimeout: .seconds(3),
            beforeListenerReady: { ticket in await gate.pause(ticket: ticket) }
        )
        defer {
            access.deactivate()
            Task {
                await gate.release(ticket: "ticket-A")
                await gate.release(ticket: "ticket-B")
            }
        }

        access.activate(ticket: "ticket-A")
        access.activate(ticket: "ticket-B")
        XCTAssertNil(access.localOrigin(ticket: "ticket-B"))

        try await gate.waitForEntry(ticket: "ticket-B", timeout: .seconds(5))
        XCTAssertNil(access.localOrigin(ticket: "ticket-B"))

        await gate.release(ticket: "ticket-B")

        let origin = try await waitForOrigin(access, ticket: "ticket-B", timeout: .seconds(5))
        XCTAssertEqual(access.localOrigin(ticket: "ticket-B"), origin)
        XCTAssertNil(access.localOrigin(ticket: "ticket-A"))
    }

    private func waitForOrigin(_ access: RemoteAccess, ticket: String, timeout: Duration) async throws -> URL {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while clock.now < deadline {
            if let origin = access.localOrigin(ticket: ticket) { return origin }
            try await Task.sleep(for: .milliseconds(10))
        }
        throw RemoteAccessTestTimeout()
    }

    private func reservePort(_ value: Int?) async throws -> PortReservation {
        guard let value, let port = UInt16(exactly: value) else { throw POSIXError(.EINVAL) }
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: .seconds(2))
        while clock.now < deadline {
            do { return try PortReservation(port: port) }
            catch { try await Task.sleep(for: .milliseconds(20)) }
        }
        throw RemoteAccessTestTimeout()
    }

    private func socketRoundTrip(origin: URL, payload: Data) async throws -> Data {
        try await Task.detached {
            guard let originPort = origin.port, let port = UInt16(exactly: originPort) else {
                throw POSIXError(.EINVAL)
            }
            return try Self.socketRoundTrip(port: port, payload: payload)
        }.value
    }

    private nonisolated static func socketRoundTrip(port: UInt16, payload: Data) throws -> Data {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.EIO) }
        defer { close(descriptor) }

        var timeout = timeval(tv_sec: 5, tv_usec: 0)
        _ = withUnsafePointer(to: &timeout) {
            setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, $0,
                       socklen_t(MemoryLayout<timeval>.size))
        }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = in_port_t(port.bigEndian)
        address.sin_addr = in_addr(s_addr: inet_addr("127.0.0.1"))
        let connected = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard connected == 0 else { throw POSIXError(.ECONNREFUSED) }

        var sent = 0
        while sent < payload.count {
            let count = payload.withUnsafeBytes { bytes in
                Darwin.send(descriptor, bytes.baseAddress?.advanced(by: sent),
                            payload.count - sent, 0)
            }
            guard count > 0 else { throw POSIXError(.EPIPE) }
            sent += count
        }
        _ = shutdown(descriptor, SHUT_WR)

        var response = Data()
        var buffer = [UInt8](repeating: 0, count: 1024)
        while true {
            let count = recv(descriptor, &buffer, buffer.count, 0)
            if count == 0 { break }
            if count < 0 {
                if errno == EAGAIN || errno == EWOULDBLOCK { throw POSIXError(.ETIMEDOUT) }
                break
            }
            response.append(contentsOf: buffer.prefix(count))
        }
        return response
    }
}

private final class PortReservation {
    private let descriptor: Int32

    init(port: UInt16) throws {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.EIO) }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = in_port_t(port.bigEndian)
        address.sin_addr = in_addr(s_addr: inet_addr("127.0.0.1"))
        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(descriptor, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bound == 0 else {
            close(descriptor)
            throw POSIXError(.EADDRINUSE)
        }
        self.descriptor = descriptor
    }

    deinit { close(descriptor) }
}

actor ListenerStartGate {
    private var entryCounts: [String: Int] = [:]
    private var releaseContinuations: [String: CheckedContinuation<Void, Never>] = [:]

    func pause(ticket: String) async {
        entryCounts[ticket, default: 0] += 1
        await withCheckedContinuation { releaseContinuations[ticket] = $0 }
    }

    func waitForEntry(ticket: String, timeout: Duration) async throws {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while entryCounts[ticket, default: 0] == 0, clock.now < deadline {
            try await Task.sleep(for: .milliseconds(10))
        }
        guard entryCounts[ticket, default: 0] > 0 else { throw RemoteAccessTestTimeout() }
    }

    func release(ticket: String) {
        releaseContinuations.removeValue(forKey: ticket)?.resume()
    }
}

final class RemoteAccessTestResult<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var hasValue = false
    private var value: Value?

    func resolve(_ value: Value) {
        lock.lock()
        defer { lock.unlock() }
        guard !hasValue else { return }
        self.value = .some(value)
        hasValue = true
    }

    func wait(timeout: Duration) async throws -> Value {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while clock.now < deadline {
            let (hasValue, value) = snapshot()
            if hasValue { return value! }
            try await Task.sleep(for: .milliseconds(10))
        }
        throw RemoteAccessTestTimeout()
    }

    private func snapshot() -> (Bool, Value?) {
        lock.lock()
        defer { lock.unlock() }
        return (hasValue, value)
    }
}

struct RemoteAccessTestTimeout: Error {}

private actor ListenerStartCounter {
    private var starts: [String: Int] = [:]

    func record(ticket: String) {
        starts[ticket, default: 0] += 1
    }

    func count(ticket: String) -> Int {
        starts[ticket, default: 0]
    }
}

private final class TicketRecordingTunnel: RemoteTunnel, @unchecked Sendable {
    private let lock = NSLock()
    private var ticketStorage: [String] = []
    private var bytesByTicket: [String: Data] = [:]

    var connectTickets: [String] {
        locked { ticketStorage }
    }

    func receivedBytes(for ticket: String) -> Data {
        locked { bytesByTicket[ticket, default: Data()] }
    }

    func connect(ticket: String) async throws -> RemoteTunnelConnection {
        locked { ticketStorage.append(ticket) }
        return TicketRecordingConnection(tunnel: self, ticket: ticket)
    }

    func record(_ data: Data, ticket: String) {
        locked { bytesByTicket[ticket, default: Data()].append(data) }
    }

    private func locked<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }
}

private final class TicketRecordingConnection: RemoteTunnelConnection, @unchecked Sendable {
    private let tunnel: TicketRecordingTunnel
    private let ticket: String

    init(tunnel: TicketRecordingTunnel, ticket: String) {
        self.tunnel = tunnel
        self.ticket = ticket
    }

    func openStream() async throws -> RemoteTunnelStream {
        TicketRecordingStream(tunnel: tunnel, ticket: ticket)
    }

    func close() async {}
}

private final class TicketRecordingStream: RemoteTunnelStream, @unchecked Sendable {
    private let lock = NSLock()
    private let tunnel: TicketRecordingTunnel
    private let ticket: String
    private var responseDelivered = false
    private var responseReady = false
    private var reader: CheckedContinuation<Data?, Error>?

    init(tunnel: TicketRecordingTunnel, ticket: String) {
        self.tunnel = tunnel
        self.ticket = ticket
    }

    func write(_ data: Data) async throws {
        guard data != Data("hello".utf8) else { return }
        tunnel.record(data, ticket: ticket)
        let continuation = locked { () -> CheckedContinuation<Data?, Error>? in
            guard !responseReady else { return nil }
            responseReady = true
            let current = reader
            reader = nil
            if current != nil { responseDelivered = true }
            return current
        }
        continuation?.resume(returning: Data("response-\(ticket)".utf8))
    }

    func finish() async throws {}

    func read(maxLength: Int) async throws -> Data? {
        try await withCheckedThrowingContinuation { continuation in
            let immediate = locked { () -> (Bool, Data?) in
                if responseReady {
                    if responseDelivered { return (true, nil) }
                    responseDelivered = true
                    return (true, Data("response-\(ticket)".utf8))
                }
                reader = continuation
                return (false, nil)
            }
            if immediate.0 { continuation.resume(returning: immediate.1) }
        }
    }

    func cancel() async {
        let continuation = locked { () -> CheckedContinuation<Data?, Error>? in
            defer { reader = nil }
            return reader
        }
        continuation?.resume(returning: nil)
    }

    private func locked<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }
}
