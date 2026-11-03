import Foundation
import XCTest
@testable import Yansu

final class IrohRemoteAccessE2ETests: XCTestCase {
    func testRealIrohTunnelReachesRelayHealthEndpoint() async throws {
        guard let ticket = ProcessInfo.processInfo.environment["ISOFORM_E2E_IROH_TICKET"],
              !ticket.isEmpty else {
            throw XCTSkip("Set ISOFORM_E2E_IROH_TICKET (TEST_RUNNER_ISOFORM_E2E_IROH_TICKET via xcodebuild) to run the live iroh test.")
        }

        let forwarder = IrohForwarder(tunnel: IrohTunnel(), dialTimeout: .seconds(20), onReachability: { _ in })
        do {
            let port = try await forwarder.start(ticket: ticket)
            let url = try XCTUnwrap(URL(string: "http://127.0.0.1:\(port)/v1/health"))
            try await assertHealth(url)
            await forwarder.dropConnection()
            try await assertHealth(url)
            await forwarder.stop()
        } catch {
            await forwarder.stop()
            throw error
        }
    }

    private func assertHealth(_ url: URL) async throws {
        let session = URLSession(configuration: .ephemeral)
        defer { session.invalidateAndCancel() }
        var request = URLRequest(url: url)
        request.setValue("close", forHTTPHeaderField: "Connection")
        let (_, response) = try await session.data(for: request)
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
    }
}
