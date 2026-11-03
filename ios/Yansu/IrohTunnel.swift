import Foundation
import IrohLib

actor IrohTunnel: RemoteTunnelLifecycle {
    private static let dumbpipeALPN = Data("DUMBPIPEV0".utf8)

    private var endpoint: Endpoint?
    private var endpointTask: Task<Endpoint, Error>?

    func connect(ticket: String) async throws -> RemoteTunnelConnection {
        let endpointTicket = try EndpointTicket.fromString(str: ticket)
        let endpoint = try await localEndpoint()
        let connection = try await endpoint.connect(addr: endpointTicket.endpointAddr(), alpn: Self.dumbpipeALPN)
        return IrohTunnelConnection(connection: connection)
    }

    func close() async {
        endpointTask?.cancel()
        endpointTask = nil
        guard let endpoint else { return }
        self.endpoint = nil
        try? await endpoint.close()
    }

    private func localEndpoint() async throws -> Endpoint {
        if let endpoint, !endpoint.isClosed() { return endpoint }
        if let endpointTask { return try await endpointTask.value }

        let task = Task {
            try await Endpoint.bind(options: EndpointOptions(preset: presetN0()))
        }
        endpointTask = task
        do {
            let bound = try await task.value
            endpoint = bound
            endpointTask = nil
            return bound
        } catch {
            endpointTask = nil
            throw error
        }
    }
}

private final class IrohTunnelConnection: RemoteTunnelConnection, @unchecked Sendable {
    private let connection: Connection

    init(connection: Connection) {
        self.connection = connection
    }

    func openStream() async throws -> RemoteTunnelStream {
        let stream = try await connection.openBi()
        return IrohTunnelStream(send: stream.send(), receive: stream.recv())
    }

    func close() async {
        try? connection.close(errorCode: 0, reason: Data())
    }
}

private final class IrohTunnelStream: RemoteTunnelStream, @unchecked Sendable {
    private let send: SendStream
    private let receive: RecvStream

    init(send: SendStream, receive: RecvStream) {
        self.send = send
        self.receive = receive
    }

    func write(_ data: Data) async throws {
        try await send.writeAll(buf: data)
    }

    func finish() async throws {
        try await send.finish()
    }

    func read(maxLength: Int) async throws -> Data? {
        let count = UInt32(clamping: maxLength)
        let data = try await receive.read(sizeLimit: count)
        return data.isEmpty ? nil : data
    }

    func cancel() async {
        try? await send.reset(errorCode: 0)
        try? await receive.stop(errorCode: 0)
    }
}
