import Foundation
import Network

private final class ListenerReadiness: @unchecked Sendable {
    private let lock = NSLock()
    private let listener: NWListener
    private var continuation: CheckedContinuation<UInt16, Error>?

    init(listener: NWListener, continuation: CheckedContinuation<UInt16, Error>) {
        self.listener = listener
        self.continuation = continuation
    }

    func resume(with result: Result<UInt16, Error>) {
        lock.lock()
        let continuation = self.continuation
        self.continuation = nil
        lock.unlock()
        guard let continuation else { return }
        listener.stateUpdateHandler = nil
        continuation.resume(with: result)
    }
}

final class TimeoutRace<Value: Sendable>: @unchecked Sendable {
    private enum Winner {
        case operation
        case timer
        case cancellation
    }

    private let lock = NSLock()
    private var continuation: CheckedContinuation<Value, Error>?
    private var result: Result<Value, Error>?
    private var operationTask: Task<Void, Never>?
    private var timerTask: Task<Void, Never>?

    func start(continuation: CheckedContinuation<Value, Error>, timeout: Duration,
               operation: @escaping @Sendable () async throws -> Value) {
        let completed = locked { () -> Result<Value, Error>? in
            if let result { return result }
            self.continuation = continuation
            return nil
        }
        if let completed {
            continuation.resume(with: completed)
            return
        }

        let operationTask = Task {
            do {
                self.finish(.success(try await operation()), winner: .operation)
            } catch {
                self.finish(.failure(error), winner: .operation)
            }
        }
        set(operationTask, winner: .operation)

        let timerTask = Task {
            do {
                try await Task.sleep(for: timeout)
                self.finish(.failure(RelayClientError.timedOut), winner: .timer)
            } catch {}
        }
        set(timerTask, winner: .timer)
    }

    func cancel() {
        finish(.failure(CancellationError()), winner: .cancellation)
    }

    private func set(_ task: Task<Void, Never>, winner: Winner) {
        let shouldCancel = locked {
            guard result == nil else { return true }
            switch winner {
            case .operation:
                operationTask = task
            case .timer:
                timerTask = task
            case .cancellation:
                break
            }
            return false
        }
        if shouldCancel { task.cancel() }
    }

    private func finish(_ result: Result<Value, Error>, winner: Winner) {
        let completed = locked { () -> (CheckedContinuation<Value, Error>?, [Task<Void, Never>])? in
            guard self.result == nil else { return nil }
            self.result = result
            let continuation = self.continuation
            self.continuation = nil
            let losers: [Task<Void, Never>]
            switch winner {
            case .operation:
                losers = timerTask.map { [$0] } ?? []
            case .timer:
                losers = operationTask.map { [$0] } ?? []
            case .cancellation:
                losers = [operationTask, timerTask].compactMap { $0 }
            }
            operationTask = nil
            timerTask = nil
            return (continuation, losers)
        }
        guard let completed else { return }
        completed.0?.resume(with: result)
        completed.1.forEach { $0.cancel() }
    }

    private func locked<T>(_ body: () -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body()
    }
}

actor IrohForwarder {
    private struct ConnectAttempt {
        let id: UUID
        let ticket: String
        let task: Task<RemoteTunnelConnection, Error>
    }

    private let tunnel: RemoteTunnel
    private let dialTimeout: Duration
    private let beforeListenerReady: (@Sendable (String) async -> Void)?
    private let onReachability: @Sendable (Bool) -> Void
    private let queue = DispatchQueue(label: "com.isoform.remote-forwarder")

    private var listener: NWListener?
    private var port: UInt16?
    private var ticket: String?
    private var tunnelConnection: RemoteTunnelConnection?
    private var tunnelTicket: String?
    private var connectAttempt: ConnectAttempt?
    private var tcpConnections: [UUID: NWConnection] = [:]
    private var streams: [UUID: RemoteTunnelStream] = [:]

    init(tunnel: RemoteTunnel, dialTimeout: Duration = .seconds(10),
         beforeListenerReady: (@Sendable (String) async -> Void)? = nil,
         onReachability: @escaping @Sendable (Bool) -> Void) {
        self.tunnel = tunnel
        self.dialTimeout = dialTimeout
        self.beforeListenerReady = beforeListenerReady
        self.onReachability = onReachability
    }

    func start(ticket: String, preferredPort: UInt16? = nil) async throws -> UInt16 {
        if self.ticket == ticket, listener != nil, let port { return port }
        if self.ticket != ticket {
            await stop()
            guard self.ticket == nil else { throw CancellationError() }
        }
        self.ticket = ticket
        do {
            return try await startListener(ticket: ticket, port: preferredPort)
        } catch {
            guard preferredPort != nil, !(error is CancellationError),
                  self.ticket == ticket, listener == nil else { throw error }
            return try await startListener(ticket: ticket, port: nil)
        }
    }

    func dropConnection() async {
        await closeTunnelConnection()
        for stream in streams.values {
            await stream.cancel()
        }
        streams.removeAll()
        for connection in tcpConnections.values {
            connection.cancel()
        }
        tcpConnections.removeAll()
    }

    func stop() async {
        let activeListener = listener
        activeListener?.newConnectionHandler = nil
        activeListener?.cancel()
        listener = nil
        port = nil
        ticket = nil
        for connection in tcpConnections.values {
            connection.cancel()
        }
        tcpConnections.removeAll()
        for stream in streams.values {
            await stream.cancel()
        }
        streams.removeAll()
        await closeTunnelConnection()
        if let lifecycle = tunnel as? RemoteTunnelLifecycle {
            await lifecycle.close()
        }
    }

    private func startListener(ticket: String, port preferredPort: UInt16?) async throws -> UInt16 {
        let parameters = NWParameters.tcp
        let localPort = preferredPort.flatMap { NWEndpoint.Port(rawValue: $0) } ?? .any
        parameters.requiredLocalEndpoint = .hostPort(host: NWEndpoint.Host("127.0.0.1"), port: localPort)
        let listener = try NWListener(using: parameters)
        self.listener = listener
        listener.newConnectionHandler = { [weak self] connection in
            Task { await self?.handle(connection) }
        }
        let queue = self.queue
        let beforeListenerReady = self.beforeListenerReady
        let readyPort: UInt16
        do {
            readyPort = try await withCheckedThrowingContinuation {
                (continuation: CheckedContinuation<UInt16, Error>) in
                let readiness = ListenerReadiness(listener: listener, continuation: continuation)
                listener.stateUpdateHandler = { state in
                    switch state {
                    case .ready:
                        guard let port = listener.port?.rawValue else {
                            readiness.resume(with: .failure(RelayClientError.invalidResponse))
                            return
                        }
                        if let beforeListenerReady {
                            Task {
                                await beforeListenerReady(ticket)
                                readiness.resume(with: .success(port))
                            }
                        } else {
                            readiness.resume(with: .success(port))
                        }
                    case .failed(let error):
                        readiness.resume(with: .failure(error))
                    case .cancelled:
                        readiness.resume(with: .failure(CancellationError()))
                    default:
                        break
                    }
                }
                listener.start(queue: queue)
            }
        } catch {
            listener.cancel()
            if self.listener === listener {
                self.listener = nil
                self.port = nil
            }
            throw error
        }
        guard self.listener === listener, self.ticket == ticket else {
            listener.cancel()
            throw CancellationError()
        }
        port = readyPort
        return readyPort
    }

    private func handle(_ tcp: NWConnection) async {
        let id = UUID()
        tcpConnections[id] = tcp
        tcp.stateUpdateHandler = nil
        do {
            try await waitUntilReady(tcp)
            guard let ticket else { throw CancellationError() }
            let (_, stream) = try await openStream(ticket: ticket)
            streams[id] = stream
            onReachability(true)
            try await stream.write(Data("hello".utf8))
            let sender = Task { try await Self.forwardTCPToTunnel(tcp, stream: stream) }
            do {
                try await Self.forwardTunnelToTCP(stream, tcp: tcp)
                tcp.cancel()
                _ = await sender.result
            } catch {
                sender.cancel()
                tcp.cancel()
                await stream.cancel()
                throw error
            }
        } catch {
            onReachability(false)
            tcp.cancel()
        }
        if let stream = streams.removeValue(forKey: id) {
            await stream.cancel()
        }
        tcp.stateUpdateHandler = nil
        tcp.cancel()
        tcpConnections[id] = nil
    }

    private func openStream(ticket: String) async throws -> (RemoteTunnelConnection, RemoteTunnelStream) {
        if tunnelTicket != ticket {
            await closeTunnelConnection()
            tunnelTicket = ticket
        }
        let connection = try await connectedTunnel(ticket: ticket)
        do {
            let stream = try await withTimeout { try await connection.openStream() }
            return (connection, stream)
        } catch {
            await discardTunnelConnection(connection)
            let replacement = try await connectedTunnel(ticket: ticket)
            do {
                let stream = try await withTimeout { try await replacement.openStream() }
                return (replacement, stream)
            } catch {
                await discardTunnelConnection(replacement)
                throw error
            }
        }
    }

    private func connectedTunnel(ticket: String) async throws -> RemoteTunnelConnection {
        if let tunnelConnection { return tunnelConnection }
        let attempt: ConnectAttempt
        if let current = connectAttempt, current.ticket == ticket {
            attempt = current
        } else {
            let id = UUID()
            let task = Task {
                try await self.withTimeout { try await self.tunnel.connect(ticket: ticket) }
            }
            attempt = ConnectAttempt(id: id, ticket: ticket, task: task)
            connectAttempt = attempt
        }
        do {
            let connection = try await attempt.task.value
            if let tunnelConnection, tunnelTicket == ticket { return tunnelConnection }
            guard tunnelTicket == ticket, connectAttempt?.id == attempt.id else {
                await connection.close()
                throw CancellationError()
            }
            tunnelConnection = connection
            connectAttempt = nil
            return connection
        } catch {
            if connectAttempt?.id == attempt.id { connectAttempt = nil }
            throw error
        }
    }

    private func discardTunnelConnection(_ connection: RemoteTunnelConnection) async {
        if tunnelConnection === connection {
            tunnelConnection = nil
        }
        await connection.close()
    }

    private func waitUntilReady(_ connection: NWConnection) async throws {
        try await withCheckedThrowingContinuation { continuation in
            connection.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    connection.stateUpdateHandler = nil
                    continuation.resume()
                case .failed(let error):
                    connection.stateUpdateHandler = nil
                    continuation.resume(throwing: error)
                case .cancelled:
                    connection.stateUpdateHandler = nil
                    continuation.resume(throwing: CancellationError())
                default:
                    break
                }
            }
            connection.start(queue: queue)
        }
    }

    private func closeTunnelConnection() async {
        let connection = tunnelConnection
        tunnelConnection = nil
        connectAttempt?.task.cancel()
        connectAttempt = nil
        tunnelTicket = nil
        await connection?.close()
    }

    private func withTimeout<T: Sendable>(
        _ operation: @escaping @Sendable () async throws -> T
    ) async throws -> T {
        let race = TimeoutRace<T>()
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                race.start(continuation: continuation, timeout: dialTimeout, operation: operation)
            }
        } onCancel: {
            race.cancel()
        }
    }

    private nonisolated static func forwardTCPToTunnel(_ tcp: NWConnection,
                                                       stream: RemoteTunnelStream) async throws {
        while true {
            let (data, complete) = try await receive(from: tcp, maximumLength: 32 * 1024)
            if let data { try await stream.write(data) }
            if complete { break }
        }
        try await stream.finish()
    }

    private nonisolated static func forwardTunnelToTCP(_ stream: RemoteTunnelStream,
                                                       tcp: NWConnection) async throws {
        while let data = try await stream.read(maxLength: 32 * 1024) {
            try await send(data, on: tcp)
        }
    }

    private nonisolated static func receive(from connection: NWConnection,
                                            maximumLength: Int) async throws -> (Data?, Bool) {
        try await withCheckedThrowingContinuation {
            (continuation: CheckedContinuation<(Data?, Bool), Error>) in
            connection.receive(minimumIncompleteLength: 1, maximumLength: maximumLength) {
                data, _, complete, error in
                if let error {
                    continuation.resume(throwing: error)
                } else if let data, !data.isEmpty {
                    continuation.resume(returning: (data, complete))
                } else {
                    continuation.resume(returning: (nil, complete))
                }
            }
        }
    }

    private nonisolated static func send(_ data: Data, on connection: NWConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            connection.send(content: data, completion: .contentProcessed { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            })
        }
    }
}
