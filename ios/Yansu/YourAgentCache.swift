import Foundation

struct YourAgentCache {
    static let defaultDirectory = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("Isoform/your-agent", isDirectory: true)
    let directory: URL
    let secrets: SecretStore
    private let prefix = "your-agent.token."
    private var file: URL { directory.appendingPathComponent("copy.json") }

    func load(hubID: String?) throws -> YourAgentCopy? {
        guard FileManager.default.fileExists(atPath: file.path) else { return nil }
        var copy = try JSONDecoder().decode(YourAgentCopy.self, from: Data(contentsOf: file))
        guard copy.hub == hubID else { try clear(); return nil }
        copy.token = try secrets.read(prefix + copy.hub) ?? ""
        return copy
    }

    func save(_ copy: YourAgentCopy) throws {
        try secrets.save(copy.token, account: prefix + copy.hub)
        var publicCopy = copy
        publicCopy.token = ""
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try JSONEncoder().encode(publicCopy).write(to: file,
            options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    func clear() throws {
        for account in try secrets.accountNames() where account.hasPrefix(prefix) { try secrets.delete(account) }
        if FileManager.default.fileExists(atPath: file.path) { try FileManager.default.removeItem(at: file) }
    }
}
