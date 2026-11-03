import Foundation

protocol RemoteTunnel: Sendable {
    func connect(ticket: String) async throws -> RemoteTunnelConnection
}

protocol RemoteTunnelLifecycle: RemoteTunnel {
    func close() async
}

protocol RemoteTunnelConnection: AnyObject, Sendable {
    func openStream() async throws -> RemoteTunnelStream
    func close() async
}

protocol RemoteTunnelStream: AnyObject, Sendable {
    func write(_ data: Data) async throws
    func finish() async throws
    func read(maxLength: Int) async throws -> Data?
    func cancel() async
}
