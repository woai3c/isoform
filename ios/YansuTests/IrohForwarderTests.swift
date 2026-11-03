import Darwin
import Foundation
import Network
import XCTest
@testable import Yansu

final class IrohForwarderTests: XCTestCase {
    func testLoopbackForwarderPumpsBytesAndFinishesOnTCPHalfClose() async throws {
        let tunnel = ForwarderTestTunnel()
        let forwarder = IrohForwarder(tunnel: tunnel, dialTimeout: .seconds(3), onReachability: { _ in })
        let port = try await forwarder.start(ticket: "endpointabcdefghijklmnopqrst")

        let response = try await Task.detached {
            try Self.socketRoundTrip(port: port, payload: Data("GET /health".utf8))
        }.value

        XCTAssertEqual(String(decoding: response, as: UTF8.self), "response")
        XCTAssertEqual(tunnel.snapshot.writes, [Data("hello".utf8), Data("GET /health".utf8)])
        XCTAssertTrue(tunnel.snapshot.finished)
        let reusedPort = try await forwarder.start(ticket: "endpointabcdefghijklmnopqrst")
        XCTAssertGreaterThan(reusedPort, 0)
        await forwarder.stop()
        await forwarder.stop()
        let restarted = try await forwarder.start(ticket: "endpointabcdefghijklmnopqrst", preferredPort: port)
        XCTAssertGreaterThan(restarted, 0)
        await forwarder.stop()
    }

    func testConnectFailureClosesTCPAndReportsUnreachable() async throws {
        let tunnel = ForwarderTestTunnel(failingConnects: 1)
        let statuses = ForwarderTestStatuses()
        let forwarder = IrohForwarder(tunnel: tunnel, dialTimeout: .seconds(3)) {
            statuses.append($0)
        }
        let port = try await forwarder.start(ticket: "endpointabcdefghijklmnopqrst")
        let response = try await Task.detached {
            try Self.socketRoundTrip(port: port, payload: Data("request".utf8))
        }.value

        XCTAssertTrue(response.isEmpty)
        XCTAssertTrue(statuses.values.contains(false))
        await forwarder.stop()
    }

    func testConnectTimeoutClosesTCPWithoutWaitingForUncooperativeTunnel() async throws {
        try await assertTimeoutClosesTCP(tunnel: ForwarderTestTunnel(hangsOnConnect: true))
    }

    func testOpenTimeoutClosesTCPWithoutWaitingForUncooperativeTunnel() async throws {
        try await assertTimeoutClosesTCP(tunnel: ForwarderTestTunnel(hangsOnOpen: true))
    }

    func testOpenFailureReconnectsOnce() async throws {
        let tunnel = ForwarderTestTunnel(failingOpens: 1)
        let forwarder = IrohForwarder(tunnel: tunnel, dialTimeout: .seconds(3), onReachability: { _ in })
        let port = try await forwarder.start(ticket: "endpointabcdefghijklmnopqrst")
        let response = try await Task.detached {
            try Self.socketRoundTrip(port: port, payload: Data("request".utf8))
        }.value

        XCTAssertEqual(String(decoding: response, as: UTF8.self), "response")
        XCTAssertEqual(tunnel.snapshot.connects, 2)
        XCTAssertEqual(tunnel.snapshot.opens, 2)
        await forwarder.stop()
    }

    func testConcurrentTCPClientsShareOneTunnelConnect() async throws {
        let tunnel = ConcurrentForwarderTestTunnel()
        let forwarder = IrohForwarder(tunnel: tunnel, dialTimeout: .seconds(3), onReachability: { _ in })
        let port = try await forwarder.start(ticket: "endpointabcdefghijklmnopqrst")
        let clients = (0..<3).map { index in
            Task.detached {
                try Self.socketRoundTrip(port: port, payload: Data("request-\(index)".utf8))
            }
        }
        var responses: [Data] = []
        for client in clients {
            responses.append(try await client.value)
        }

        XCTAssertEqual(responses, Array(repeating: Data("response".utf8), count: 3))
        XCTAssertEqual(tunnel.connects, 1)
        XCTAssertEqual(tunnel.opens, 3)
        await forwarder.stop()
    }

    func testFailedForwardedStreamDoesNotCloseTheSharedTunnel() async throws {
        let tunnel = StreamFailureIsolationTunnel()
        let forwarder = IrohForwarder(tunnel: tunnel, dialTimeout: .seconds(3), onReachability: { _ in })
        let port = try await forwarder.start(ticket: "endpointabcdefghijklmnopqrst")
        let clientCompletions = StreamFailureIsolationClientCompletions()
        let clients = (0..<2).map { index in
            Task.detached {
                let response = try Self.socketRoundTrip(port: port, payload: Data("request-\(index)".utf8))
                await clientCompletions.record(response)
                return response
            }
        }

        let bothRequestsArrived = await tunnel.waitForRequests(2, timeout: .seconds(2))
        let failedReadOccurred = await tunnel.waitForFailedRead(timeout: .seconds(2))
        let failedClientClosed = await clientCompletions.waitForEmpty(timeout: .seconds(2))
        tunnel.releaseResponses()
        XCTAssertTrue(bothRequestsArrived)
        XCTAssertTrue(failedReadOccurred)
        XCTAssertTrue(failedClientClosed)

        var responses: [Data] = []
        for client in clients {
            responses.append(try await client.value)
        }
        XCTAssertEqual(responses.filter { $0.isEmpty }.count, 1)
        XCTAssertEqual(responses.filter { $0 == Data("response".utf8) }.count, 1)
        XCTAssertEqual(tunnel.connects, 1)
        XCTAssertEqual(tunnel.opens, 2)
        XCTAssertEqual(tunnel.closes, 0)

        let thirdResponse = try await Task.detached {
            try Self.socketRoundTrip(port: port, payload: Data("third-request".utf8))
        }.value
        XCTAssertEqual(thirdResponse, Data("response".utf8))
        XCTAssertEqual(tunnel.connects, 1)
        XCTAssertEqual(tunnel.opens, 3)
        XCTAssertEqual(tunnel.closes, 0)
        await forwarder.stop()
    }

    func testStopCancelsPendingStartWithoutHanging() async throws {
        let ticket = "ticket-pending"
        let gate = ListenerStartGate()
        let forwarder = IrohForwarder(
            tunnel: ForwarderTestTunnel(), dialTimeout: .seconds(3),
            beforeListenerReady: { ticket in await gate.pause(ticket: ticket) },
            onReachability: { _ in }
        )
        let result = RemoteAccessTestResult<Result<UInt16, Error>>()
        Task {
            do {
                result.resolve(.success(try await forwarder.start(ticket: ticket)))
            } catch {
                result.resolve(.failure(error))
            }
        }

        try await gate.waitForEntry(ticket: ticket, timeout: .seconds(5))
        await forwarder.stop()
        await gate.release(ticket: ticket)

        let startResult = try await result.wait(timeout: .seconds(5))
        guard case .failure(let error) = startResult else {
            XCTFail("A pending start completed after stop.")
            return
        }
        XCTAssertTrue(error is CancellationError)
    }

    private func assertTimeoutClosesTCP(tunnel: ForwarderTestTunnel) async throws {
        let statuses = ForwarderTestStatuses()
        let forwarder = IrohForwarder(tunnel: tunnel, dialTimeout: .milliseconds(200)) {
            statuses.append($0)
        }
        let port = try await forwarder.start(ticket: "endpointabcdefghijklmnopqrst")
        let started = ProcessInfo.processInfo.systemUptime
        let response = try await Task.detached {
            try Self.socketRoundTrip(port: port, payload: Data("request".utf8), receiveTimeout: 2)
        }.value

        XCTAssertTrue(response.isEmpty)
        XCTAssertLessThan(ProcessInfo.processInfo.systemUptime - started, 2)
        let reportedUnreachable = await statuses.waitForFalse(timeout: .seconds(2))
        XCTAssertTrue(reportedUnreachable)
        await forwarder.stop()
    }

    private static func socketRoundTrip(port: UInt16, payload: Data, receiveTimeout: Int = 5) throws -> Data {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.EIO) }
        defer { close(descriptor) }

        var timeout = timeval(tv_sec: receiveTimeout, tv_usec: 0)
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

private final class ForwarderTestTunnel: RemoteTunnel, RemoteTunnelConnection, RemoteTunnelStream,
    @unchecked Sendable {
    struct Snapshot {
        let connects: Int
        let opens: Int
        let writes: [Data]
        let finished: Bool
    }

    private let lock = NSLock()
    private let failingConnects: Int
    private let failingOpens: Int
    private let hangsOnConnect: Bool
    private let hangsOnOpen: Bool
    private var connects = 0
    private var opens = 0
    private var writes: [Data] = []
    private var finished = false
    private var readReady = false
    private var readDelivered = false
    private var reader: CheckedContinuation<Data?, Error>?

    init(failingConnects: Int = 0, failingOpens: Int = 0,
         hangsOnConnect: Bool = false, hangsOnOpen: Bool = false) {
        self.failingConnects = failingConnects
        self.failingOpens = failingOpens
        self.hangsOnConnect = hangsOnConnect
        self.hangsOnOpen = hangsOnOpen
    }

    var snapshot: Snapshot {
        withLock { Snapshot(connects: connects, opens: opens, writes: writes, finished: finished) }
    }

    func connect(ticket: String) async throws -> RemoteTunnelConnection {
        let shouldFail = withLock { () -> Bool in
            connects += 1
            return connects <= failingConnects
        }
        if hangsOnConnect { await suspendForever() }
        if shouldFail { throw RelayClientError.offline }
        return self
    }

    func openStream() async throws -> RemoteTunnelStream {
        let shouldFail = withLock { () -> Bool in
            opens += 1
            return opens <= failingOpens
        }
        if hangsOnOpen { await suspendForever() }
        if shouldFail { throw RelayClientError.unavailable }
        return self
    }

    func close() async {}

    func write(_ data: Data) async throws {
        let continuation = withLock { () -> CheckedContinuation<Data?, Error>? in
            writes.append(data)
            if data != Data("hello".utf8) {
                readReady = true
                let continuation = reader
                reader = nil
                if continuation != nil { readDelivered = true }
                return continuation
            }
            return nil
        }
        continuation?.resume(returning: Data("response".utf8))
    }

    func finish() async throws {
        withLock { finished = true }
    }

    func read(maxLength: Int) async throws -> Data? {
        try await withCheckedThrowingContinuation { continuation in
            let immediate = withLock { () -> (Bool, Data?) in
                if readReady {
                    if readDelivered { return (true, nil) }
                    readDelivered = true
                    return (true, Data("response".utf8))
                }
                reader = continuation
                return (false, nil)
            }
            if immediate.0 { continuation.resume(returning: immediate.1) }
        }
    }

    func cancel() async {
        let continuation = withLock { () -> CheckedContinuation<Data?, Error>? in
            let continuation = reader
            reader = nil
            return continuation
        }
        continuation?.resume(returning: nil)
    }

    private func withLock<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }

    private func suspendForever() async {
        await withUnsafeContinuation { (_: UnsafeContinuation<Void, Never>) in }
    }
}

private final class ForwarderTestStatuses: @unchecked Sendable {
    private let lock = NSLock()
    private var valuesStorage: [Bool] = []

    var values: [Bool] {
        lock.lock()
        defer { lock.unlock() }
        return valuesStorage
    }

    func append(_ value: Bool) {
        lock.lock()
        defer { lock.unlock() }
        valuesStorage.append(value)
    }

    func waitForFalse(timeout: Duration) async -> Bool {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while !values.contains(false), clock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
        return values.contains(false)
    }
}

private final class ConcurrentForwarderTestTunnel: RemoteTunnel, @unchecked Sendable {
    private let lock = NSLock()
    private var connectCount = 0
    private var openCount = 0

    var connects: Int { locked { connectCount } }
    var opens: Int { locked { openCount } }

    func connect(ticket: String) async throws -> RemoteTunnelConnection {
        locked { connectCount += 1 }
        try await Task.sleep(for: .milliseconds(100))
        return ConcurrentForwarderTestConnection(tunnel: self)
    }

    func openedStream() {
        locked { openCount += 1 }
    }

    private func locked<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }
}

private final class ConcurrentForwarderTestConnection: RemoteTunnelConnection, @unchecked Sendable {
    private let tunnel: ConcurrentForwarderTestTunnel

    init(tunnel: ConcurrentForwarderTestTunnel) {
        self.tunnel = tunnel
    }

    func openStream() async throws -> RemoteTunnelStream {
        tunnel.openedStream()
        return ConcurrentForwarderTestStream()
    }

    func close() async {}
}

private final class ConcurrentForwarderTestStream: RemoteTunnelStream, @unchecked Sendable {
    private let lock = NSLock()
    private var responseReady = false
    private var responseDelivered = false
    private var reader: CheckedContinuation<Data?, Error>?

    func write(_ data: Data) async throws {
        guard data != Data("hello".utf8) else { return }
        let continuation = locked { () -> CheckedContinuation<Data?, Error>? in
            responseReady = true
            let current = reader
            reader = nil
            if current != nil { responseDelivered = true }
            return current
        }
        continuation?.resume(returning: Data("response".utf8))
    }

    func finish() async throws {}

    func read(maxLength: Int) async throws -> Data? {
        try await withCheckedThrowingContinuation { continuation in
            let immediate = locked { () -> (Bool, Data?) in
                if responseReady {
                    if responseDelivered { return (true, nil) }
                    responseDelivered = true
                    return (true, Data("response".utf8))
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

private final class StreamFailureIsolationTunnel: RemoteTunnel, RemoteTunnelConnection, @unchecked Sendable {
    private let lock = NSLock()
    private var connectCount = 0
    private var openCount = 0
    private var closeCount = 0
    private var requestCount = 0
    private var failedReadCount = 0
    private var isClosed = false
    private var responsesReleased = false
    private var streams: [StreamFailureIsolationStream] = []

    var connects: Int { locked { connectCount } }
    var opens: Int { locked { openCount } }
    var closes: Int { locked { closeCount } }
    var closed: Bool { locked { isClosed } }
    var released: Bool { locked { responsesReleased } }

    func connect(ticket: String) async throws -> RemoteTunnelConnection {
        locked { connectCount += 1 }
        try await Task.sleep(for: .milliseconds(100))
        return self
    }

    func openStream() async throws -> RemoteTunnelStream {
        let (index, closed) = locked { () -> (Int, Bool) in
            openCount += 1
            return (openCount, isClosed)
        }
        guard !closed else { throw RelayClientError.offline }
        let stream = StreamFailureIsolationStream(connection: self, failsOnRead: index == 1)
        locked { streams.append(stream) }
        return stream
    }

    func close() async {
        let current = locked { () -> [StreamFailureIsolationStream] in
            closeCount += 1
            isClosed = true
            return streams
        }
        for stream in current {
            await stream.connectionClosed()
        }
    }

    func recordRequest() {
        locked { requestCount += 1 }
    }

    func recordFailedRead() {
        locked { failedReadCount += 1 }
    }

    func waitForRequests(_ count: Int, timeout: Duration) async -> Bool {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while locked({ requestCount < count }), clock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
        return locked { requestCount >= count }
    }

    func waitForFailedRead(timeout: Duration) async -> Bool {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while locked({ failedReadCount == 0 }), clock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
        return locked { failedReadCount > 0 }
    }

    func releaseResponses() {
        let current = locked { () -> [StreamFailureIsolationStream] in
            responsesReleased = true
            return streams
        }
        for stream in current {
            stream.releaseResponseIfReady()
        }
    }

    private func locked<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }
}

private final class StreamFailureIsolationStream: RemoteTunnelStream, @unchecked Sendable {
    private let lock = NSLock()
    private weak var connection: StreamFailureIsolationTunnel?
    private let failsOnRead: Bool
    private var requestReceived = false
    private var responseDelivered = false
    private var cancelled = false
    private var reader: CheckedContinuation<Data?, Error>?

    init(connection: StreamFailureIsolationTunnel, failsOnRead: Bool) {
        self.connection = connection
        self.failsOnRead = failsOnRead
    }

    func write(_ data: Data) async throws {
        guard data != Data("hello".utf8) else { return }
        guard let connection else { throw RelayClientError.offline }
        connection.recordRequest()
        let (fails, continuation) = locked { () -> (Bool, CheckedContinuation<Data?, Error>?) in
            requestReceived = true
            guard failsOnRead else { return (false, nil) }
            defer { reader = nil }
            return (true, reader)
        }
        if fails {
            connection.recordFailedRead()
            continuation?.resume(throwing: RelayClientError.offline)
        } else {
            releaseResponseIfReady()
        }
    }

    func finish() async throws {}

    func read(maxLength: Int) async throws -> Data? {
        guard let connection else { throw RelayClientError.offline }
        return try await withCheckedThrowingContinuation { continuation in
            let (result, failedRead) = locked { () -> (Result<Data?, Error>?, Bool) in
                if cancelled { return (.success(nil), false) }
                if connection.closed { return (.failure(RelayClientError.offline), false) }
                if requestReceived && failsOnRead {
                    return (.failure(RelayClientError.offline), true)
                }
                if requestReceived && connection.released {
                    if responseDelivered { return (.success(nil), false) }
                    responseDelivered = true
                    return (.success(Data("response".utf8)), false)
                }
                reader = continuation
                return (nil, false)
            }
            if failedRead { connection.recordFailedRead() }
            if let result { continuation.resume(with: result) }
        }
    }

    func cancel() async {
        let continuation = locked { () -> CheckedContinuation<Data?, Error>? in
            cancelled = true
            defer { reader = nil }
            return reader
        }
        continuation?.resume(returning: nil)
    }

    func connectionClosed() async {
        let continuation = locked { () -> CheckedContinuation<Data?, Error>? in
            cancelled = true
            defer { reader = nil }
            return reader
        }
        continuation?.resume(throwing: RelayClientError.offline)
    }

    func releaseResponseIfReady() {
        guard let connection, connection.released else { return }
        let continuation = locked { () -> CheckedContinuation<Data?, Error>? in
            guard !cancelled, requestReceived, !responseDelivered, let reader else { return nil }
            self.reader = nil
            responseDelivered = true
            return reader
        }
        continuation?.resume(returning: Data("response".utf8))
    }

    private func locked<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }
}

private actor StreamFailureIsolationClientCompletions {
    private var emptyResponseCount = 0

    func record(_ response: Data) {
        if response.isEmpty { emptyResponseCount += 1 }
    }

    func waitForEmpty(timeout: Duration) async -> Bool {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while emptyResponseCount == 0, clock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
        return emptyResponseCount > 0
    }
}
