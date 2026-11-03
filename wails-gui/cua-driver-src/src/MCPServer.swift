import CommonCrypto
import Darwin
import Foundation
import Network
import Security

/// Local MCP (Model Context Protocol) server — Codex parity for the
/// "wrap Alma CUA as an MCP tool" feature. Exposes Alma's computer-use
/// verbs over loopback HTTP so other LLM harnesses (Codex, Claude
/// Desktop, Cursor, etc.) can call them as MCP tools without needing to
/// speak our private unix-socket protocol.
///
/// **Scope of v1 (this file, 2026-04-23):**
/// - Loopback HTTP on 127.0.0.1 with an ephemeral or caller-chosen port.
/// - MCP methods: `initialize`, `tools/list`, `tools/call`,
///   `notifications/initialized`. Other MCP methods (prompts/*,
///   resources/*) return method-not-found.
/// - Auth: static bearer token written to a 0600 file (default
///   `~/.alma/cu/mcp-token`). Every request must send
///   `Authorization: Bearer <token>` or gets 401.
/// - Tool registry: a curated subset of Daemon verbs — ping, apps,
///   list_apps, windows, snap, get_app_state, click, type_text,
///   press_key, scroll, shot, open_url, intent, raise, pip_open,
///   pip_close, pip_status.
///
/// **Deferred to v2** (documented in the project memory and TODO
/// comments below):
/// - OAuth 2.1 + PKCE (S256) authorisation server at
///   `/.well-known/oauth-authorization-server`.
/// - `SecCodeCopyGuestWithAttributes(auditToken:)` peer verification
///   against a bundle-id whitelist.
/// - Server-Sent-Events / streaming tool results.
/// - `prompts/*` and `resources/*` method surface.
///
/// Thread model: NWListener and all NWConnections run on
/// `MCPServer.queue`. Tool calls dispatch synchronously into
/// `Daemon.dispatch(_:)`, which has its own internal main-thread
/// trampolines for AppKit work. The server holds a strong reference to
/// the `Daemon` so its lifetime is bounded by the process.
final class MCPServer {
    static let shared = MCPServer()

    private let queue = DispatchQueue(label: "com.yetone.alma.cu.mcp", qos: .userInitiated)
    private var listener: NWListener?
    private weak var daemon: Daemon?
    private var bearerToken: String = ""
    private var tokenPath: String = ""
    private var boundPort: UInt16 = 0
    private var connections: [ObjectIdentifier: NWConnection] = [:]

    // MARK: - v2 OAuth + SecCode state
    //
    // OAuth 2.1 + PKCE S256. Flow:
    //   1. client generates code_verifier (43-128 unreserved chars),
    //      computes code_challenge = BASE64URL(SHA256(code_verifier))
    //   2. GET /authorize?response_type=code&client_id=X&redirect_uri=Y
    //      &code_challenge=Z&code_challenge_method=S256&state=S
    //      → we auto-approve (no consent UI) and redirect to Y?code=C&state=S
    //      storing {C → (code_challenge, expires_at, arm_pid?)}
    //   3. client POSTs /token with code + code_verifier
    //      → we verify SHA256(verifier) matches stored challenge, issue
    //      access_token (1h TTL), drop the code
    //   4. /mcp accepts Authorization: Bearer <access_token>
    //
    // Codex's approach is the same shape, with audit_token-based peer
    // verification as an additional gate. Our peer verification uses
    // SecCode + a bundle/team whitelist; see `verifyPeerSigning` below.
    //
    // Legacy bearer (`bearerToken` above) stays valid too so existing
    // v1 callers keep working. Clients that want the stronger posture
    // go through the OAuth flow.
    private struct PendingCode {
        let codeChallenge: String
        let redirectUri: String
        let clientId: String
        let armPid: pid_t?
        let expiresAt: Date
    }
    private struct AccessGrant {
        let token: String
        let clientId: String
        let peerPid: pid_t?
        let peerTeamId: String?
        let peerBundleId: String?
        let expiresAt: Date
    }
    private var pendingCodes: [String: PendingCode] = [:]
    private var accessGrants: [String: AccessGrant] = [:]      // keyed by access_token
    private var oauthClientId: String = ""

    // Whitelist entries. Either `bundle_id` or `team_id` is set; an
    // entry with both must match both. `mcp_arm_peer` inserts an
    // ephemeral (TTL) entry with a specific pid.
    struct WhitelistEntry {
        let teamId: String?
        let bundleId: String?
        let pid: pid_t?              // if set, only this pid matches
        let expiresAt: Date?         // nil = persistent

        func matches(teamId t: String?, bundleId b: String?, pid p: pid_t?) -> Bool {
            if let exp = expiresAt, Date() > exp { return false }
            if let pinnedPid = self.pid, pinnedPid != p { return false }
            if let want = self.teamId, want != (t ?? "") { return false }
            if let want = self.bundleId, want != (b ?? "") { return false }
            return (self.teamId != nil) || (self.bundleId != nil) || (self.pid != nil)
        }
    }
    private var whitelist: [WhitelistEntry] = []
    private let stateLock = NSLock()

    private init() {}

    /// Start the MCP server. Idempotent: a second call with the same
    /// daemon returns the existing endpoint rather than re-binding.
    /// Throws if the port is taken or the token file can't be written.
    @discardableResult
    func start(daemon: Daemon, port: UInt16?, tokenFilePath: String?) throws -> [String: Any?] {
        if listener != nil {
            return statusSnapshot()
        }
        self.daemon = daemon

        // Token: 32 random bytes, hex encoded. Same strength as GitHub PATs.
        self.bearerToken = MCPServer.generateToken()
        self.tokenPath = tokenFilePath ?? MCPServer.defaultTokenPath()
        try MCPServer.writeTokenFile(path: tokenPath, token: bearerToken)

        // OAuth client_id persists across mcp_stop/start within this
        // daemon's lifetime. (A proper DCR /register endpoint is TODO;
        // for now the first-class "public" client is Alma itself and
        // any MCP client uses its issued client_id string.)
        if oauthClientId.isEmpty { oauthClientId = String(MCPServer.generateToken().prefix(32)) }

        let params = NWParameters.tcp
        // Restrict to loopback — we never want MCP on the LAN. Even if a
        // caller asks for 0.0.0.0 we refuse. This matches Codex's
        // `allowLoopbackHTTPForAuthorizationServer` posture.
        params.requiredInterfaceType = .loopback
        let portArg: NWEndpoint.Port = port.flatMap { NWEndpoint.Port(rawValue: $0) } ?? .any
        let l = try NWListener(using: params, on: portArg)
        self.listener = l

        l.newConnectionHandler = { [weak self] conn in
            self?.accept(conn)
        }
        l.stateUpdateHandler = { [weak self] state in
            switch state {
            case .ready:
                if let p = l.port?.rawValue {
                    self?.boundPort = p
                    FileHandle.standardError.write(Data(
                        "[alma-cu] MCP server listening on 127.0.0.1:\(p)\n".utf8
                    ))
                }
            case .failed(let err):
                FileHandle.standardError.write(Data(
                    "[alma-cu] MCP listener failed: \(err)\n".utf8
                ))
                self?.listener = nil
            default: break
            }
        }
        l.start(queue: queue)

        // Block briefly until the listener reaches .ready so the caller's
        // response carries the real bound port (NWListener.start is async).
        let deadline = Date().addingTimeInterval(2.0)
        while boundPort == 0, Date() < deadline {
            Thread.sleep(forTimeInterval: 0.01)
        }
        if boundPort == 0 {
            listener?.cancel()
            listener = nil
            throw CUError.internalError("MCP listener failed to bind within 2s")
        }

        return statusSnapshot()
    }

    /// Stop the server. Drops all open connections. Idempotent.
    func stop() {
        queue.sync {
            for (_, c) in connections { c.cancel() }
            connections.removeAll()
        }
        listener?.cancel()
        listener = nil
        boundPort = 0
    }

    func statusSnapshot() -> [String: Any?] {
        stateLock.lock()
        let grants = accessGrants.count
        let pending = pendingCodes.count
        let wl = whitelist.count
        let oid = oauthClientId
        stateLock.unlock()
        return [
            "running": listener != nil,
            "port": Int(boundPort),
            "url": boundPort == 0 ? nil : "http://127.0.0.1:\(boundPort)/mcp",
            "oauth_base": boundPort == 0 ? nil : "http://127.0.0.1:\(boundPort)",
            "oauth_client_id": oid.isEmpty ? nil : oid,
            "token_path": tokenPath.isEmpty ? nil : tokenPath,
            "tool_count": MCPServer.tools.count,
            "active_grants": grants,
            "pending_codes": pending,
            "whitelist_size": wl,
        ]
    }

    // MARK: - Connection lifecycle

    private func accept(_ conn: NWConnection) {
        let key = ObjectIdentifier(conn)
        connections[key] = conn
        conn.stateUpdateHandler = { [weak self] state in
            switch state {
            case .failed, .cancelled:
                self?.connections.removeValue(forKey: key)
            default: break
            }
        }
        conn.start(queue: queue)
        readRequest(conn)
    }

    /// Parse a single HTTP/1.1 request, answer it, close.
    /// v1 does NOT implement keep-alive — MCP clients reconnect per call
    /// in practice and it spares us the request-pipelining complexity.
    private func readRequest(_ conn: NWConnection, accumulated: Data = Data()) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, isComplete, error in
            guard let self = self else { return }
            if let error = error {
                FileHandle.standardError.write(Data("[alma-cu] MCP recv err: \(error)\n".utf8))
                conn.cancel(); return
            }
            var buf = accumulated
            if let d = data { buf.append(d) }

            // Look for end of headers.
            guard let headerEnd = buf.range(of: Data("\r\n\r\n".utf8)) else {
                if isComplete { conn.cancel(); return }
                self.readRequest(conn, accumulated: buf)
                return
            }
            let headerBytes = buf.subdata(in: 0..<headerEnd.lowerBound)
            guard let header = String(data: headerBytes, encoding: .utf8) else {
                self.sendError(conn, status: 400, body: "Bad headers"); return
            }
            let (reqLine, headers) = MCPServer.parseHeaderBlock(header)
            let contentLength = headers["content-length"].flatMap { Int($0) } ?? 0
            let bodyStart = headerEnd.upperBound
            let haveBodyBytes = buf.count - bodyStart
            if haveBodyBytes < contentLength {
                if isComplete { conn.cancel(); return }
                self.readRequest(conn, accumulated: buf); return
            }
            let body = buf.subdata(in: bodyStart..<(bodyStart + contentLength))

            // Route.
            self.handleRequest(conn: conn, requestLine: reqLine, headers: headers, body: body)
        }
    }

    private static func parseHeaderBlock(_ block: String) -> (String, [String: String]) {
        let lines = block.split(separator: "\r\n", omittingEmptySubsequences: false)
        let reqLine = lines.first.map(String.init) ?? ""
        var headers: [String: String] = [:]
        for line in lines.dropFirst() {
            if let idx = line.firstIndex(of: ":") {
                let key = line[..<idx].trimmingCharacters(in: .whitespaces).lowercased()
                let val = line[line.index(after: idx)...].trimmingCharacters(in: .whitespaces)
                headers[key] = val
            }
        }
        return (reqLine, headers)
    }

    // MARK: - Routing

    private func handleRequest(conn: NWConnection, requestLine: String, headers: [String: String], body: Data) {
        let parts = requestLine.split(separator: " ", maxSplits: 2, omittingEmptySubsequences: true).map(String.init)
        guard parts.count >= 2 else {
            sendError(conn, status: 400, body: "Bad request line"); return
        }
        let method = parts[0]
        let path = parts[1]

        // Health + OAuth discovery + OAuth endpoints need no auth so
        // deployment tooling and clients can bootstrap.
        if method == "GET" && path == "/healthz" {
            sendJSON(conn, status: 200, body: ["ok": true, "server": "alma-cu-mcp", "version": "0.2"]); return
        }
        if method == "GET" && path == "/.well-known/oauth-authorization-server" {
            serveAuthServerMetadata(conn); return
        }
        // OAuth authorise endpoint — can be GET (query string) or POST
        // (form-encoded body). We support GET because that's what browser-
        // redirect flows do; POST-with-form-body is unusual but allowed.
        if path.hasPrefix("/oauth/authorize") {
            if method == "GET" {
                let (_, query) = splitQuery(path)
                serveAuthorize(conn, params: query); return
            }
            if method == "POST" {
                serveAuthorize(conn, params: parseForm(body)); return
            }
            sendError(conn, status: 405, body: "Method Not Allowed"); return
        }
        if path == "/oauth/token" && method == "POST" {
            serveToken(conn, params: parseForm(body)); return
        }

        // Auth gate for /mcp. Accept EITHER static bearer (v1 legacy)
        // OR an issued OAuth access_token.
        guard let authz = headers["authorization"], authz.hasPrefix("Bearer ") else {
            sendError(conn, status: 401, body: "Missing bearer token"); return
        }
        let presented = String(authz.dropFirst("Bearer ".count))
        if presented != bearerToken && !isValidAccessToken(presented) {
            sendError(conn, status: 401, body: "Invalid or expired bearer token"); return
        }

        guard method == "POST" && path == "/mcp" else {
            sendError(conn, status: 404, body: "Not found"); return
        }
        guard !body.isEmpty else {
            sendError(conn, status: 400, body: "Empty body"); return
        }

        // Parse JSON-RPC 2.0 request.
        let obj: Any
        do { obj = try JSONSerialization.jsonObject(with: body, options: []) }
        catch {
            sendJSONRPCError(conn, id: nil, code: -32700, message: "Parse error: \(error.localizedDescription)"); return
        }
        guard let dict = obj as? [String: Any] else {
            sendJSONRPCError(conn, id: nil, code: -32600, message: "Invalid request"); return
        }
        let rpcId = dict["id"] // may be number, string, or absent (notification)
        guard let methodName = dict["method"] as? String else {
            sendJSONRPCError(conn, id: rpcId, code: -32600, message: "Missing method"); return
        }
        let params = dict["params"] as? [String: Any] ?? [:]

        do {
            let result = try self.dispatchMCP(method: methodName, params: params)
            if rpcId == nil && methodName.hasPrefix("notifications/") {
                // Notifications don't get a response body per JSON-RPC 2.0.
                sendEmptyOK(conn); return
            }
            sendJSONRPCResult(conn, id: rpcId, result: result)
        } catch let e as MCPError {
            sendJSONRPCError(conn, id: rpcId, code: e.code, message: e.message)
        } catch let e as CUError {
            // Map our own daemon errors into JSON-RPC errors.
            sendJSONRPCError(conn, id: rpcId, code: -32000, message: e.localizedDescription)
        } catch {
            sendJSONRPCError(conn, id: rpcId, code: -32603, message: "Internal error: \(error.localizedDescription)")
        }
    }

    // MARK: - MCP methods

    private func dispatchMCP(method: String, params: [String: Any]) throws -> Any {
        switch method {
        case "initialize":
            // Return server capabilities + protocol version.
            // Per spec: "2024-11-05" is the stable MCP protocol version as
            // of this writing; clients negotiate down if they're older.
            return [
                "protocolVersion": "2024-11-05",
                "capabilities": [
                    "tools": ["listChanged": false],
                    "prompts": ["listChanged": false],
                    "resources": ["listChanged": true, "subscribe": false],
                ],
                "serverInfo": [
                    "name": "alma-computer-use",
                    "version": "0.2",
                ],
            ]

        case "notifications/initialized":
            // Client signals it's done with initialization handshake.
            return [:]

        case "tools/list":
            return ["tools": MCPServer.tools.map { $0.wireDescriptor() }]

        case "tools/call":
            guard let name = params["name"] as? String else {
                throw MCPError(code: -32602, message: "missing tool name")
            }
            let arguments = params["arguments"] as? [String: Any] ?? [:]
            return try callTool(name: name, arguments: arguments)

        case "prompts/list":
            return ["prompts": MCPServer.prompts]
        case "prompts/get":
            guard let name = params["name"] as? String,
                  let tmpl = MCPServer.prompts.first(where: { ($0["name"] as? String) == name })
            else { throw MCPError(code: -32602, message: "unknown prompt name") }
            // `arguments` are string-keyed; we do plain-text substitution.
            let args = params["arguments"] as? [String: Any] ?? [:]
            let text = MCPServer.renderPrompt(name: name, args: args)
            var result: [String: Any] = tmpl
            result["messages"] = [
                ["role": "user", "content": ["type": "text", "text": text]],
            ]
            return result

        case "resources/list":
            // Expose the last snapshot + screenshot as resources. Their
            // contents are served via `resources/read`.
            return ["resources": MCPServer.resources()]
        case "resources/read":
            guard let uri = params["uri"] as? String else {
                throw MCPError(code: -32602, message: "missing uri")
            }
            return try MCPServer.readResource(uri: uri)

        default:
            throw MCPError(code: -32601, message: "Method not found: \(method)")
        }
    }

    // MARK: - Tool call → Daemon

    /// Translate an MCP `tools/call` into our internal `Request` and
    /// dispatch through the Daemon's existing handler chain. The MCP
    /// result shape is `{ content: [{ type: "text", text: "…" }] }` —
    /// we JSON-encode the Daemon's reply into a text block. Tools that
    /// naturally return files (e.g. `shot`) include the file path in
    /// that text; the client is expected to read the file itself
    /// (same UX as Codex's `skyshotImageFiles`).
    private func callTool(name: String, arguments: [String: Any]) throws -> Any {
        guard let tool = MCPServer.tools.first(where: { $0.name == name }) else {
            throw MCPError(code: -32602, message: "Unknown tool: \(name)")
        }
        guard let daemon = self.daemon else {
            throw MCPError(code: -32000, message: "Daemon not attached")
        }

        // Wrap the plain-JSON arguments in our internal Request shape so
        // the existing handler chain (forbidden-target gate, arg coercion)
        // behaves identically to a unix-socket caller.
        let argsDict: [String: AnyCodable] = arguments.mapValues { AnyCodable($0) }
        let req = Request(id: "mcp-\(UUID().uuidString.prefix(8))", cmd: tool.daemonCmd, args: argsDict)

        let data: Any? = try daemon.dispatch(req)
        let wire: Any = data ?? [:]

        let jsonText: String
        do {
            let d = try JSONSerialization.data(withJSONObject: MCPServer.jsonSanitise(wire), options: [.prettyPrinted, .withoutEscapingSlashes])
            jsonText = String(data: d, encoding: .utf8) ?? "{}"
        } catch {
            jsonText = "{}"
        }

        return [
            "content": [
                [
                    "type": "text",
                    "text": jsonText,
                ],
            ],
            "isError": false,
        ]
    }

    /// Recursively convert `Any?`, `AnyCodable`, nested arrays, nested
    /// dicts and NSNull into values `JSONSerialization` accepts. The
    /// Daemon's handlers return rich Swift types (nested arrays of
    /// heterogeneous dicts) that aren't natively JSON-serialisable; this
    /// mirrors what the unix-socket path does via `JSONEncoder` +
    /// `AnyCodable` but without a round trip through Data.
    private static func jsonSanitise(_ value: Any) -> Any {
        if value is NSNull { return NSNull() }
        if let v = value as? AnyCodable {
            if let inner = v.value { return jsonSanitise(inner) }
            return NSNull()
        }
        if let arr = value as? [Any] { return arr.map { jsonSanitise($0) } }
        if let dict = value as? [String: Any?] {
            var out: [String: Any] = [:]
            for (k, v) in dict {
                if let v = v { out[k] = jsonSanitise(v) } else { out[k] = NSNull() }
            }
            return out
        }
        if let dict = value as? [String: Any] {
            var out: [String: Any] = [:]
            for (k, v) in dict { out[k] = jsonSanitise(v) }
            return out
        }
        return value
    }

    // MARK: - HTTP response helpers

    private func sendEmptyOK(_ conn: NWConnection) {
        send(conn, status: 200, contentType: "application/json", body: Data("{}".utf8))
    }

    private func sendJSON(_ conn: NWConnection, status: Int, body: Any) {
        let data = (try? JSONSerialization.data(withJSONObject: body, options: [])) ?? Data("{}".utf8)
        send(conn, status: status, contentType: "application/json", body: data)
    }

    private func sendError(_ conn: NWConnection, status: Int, body: String) {
        send(conn, status: status, contentType: "text/plain; charset=utf-8",
             body: Data((body + "\n").utf8))
    }

    private func sendJSONRPCResult(_ conn: NWConnection, id: Any?, result: Any) {
        var resp: [String: Any] = [
            "jsonrpc": "2.0",
            "result": MCPServer.jsonSanitise(result),
        ]
        if let id = id { resp["id"] = id } else { resp["id"] = NSNull() }
        sendJSON(conn, status: 200, body: resp)
    }

    private func sendJSONRPCError(_ conn: NWConnection, id: Any?, code: Int, message: String) {
        var resp: [String: Any] = [
            "jsonrpc": "2.0",
            "error": ["code": code, "message": message],
        ]
        if let id = id { resp["id"] = id } else { resp["id"] = NSNull() }
        sendJSON(conn, status: 200, body: resp)
    }

    private func send(_ conn: NWConnection, status: Int, contentType: String, body: Data) {
        let statusText: String = {
            switch status {
            case 200: return "OK"
            case 400: return "Bad Request"
            case 401: return "Unauthorized"
            case 404: return "Not Found"
            default: return "Error"
            }
        }()
        var head = ""
        head += "HTTP/1.1 \(status) \(statusText)\r\n"
        head += "Content-Type: \(contentType)\r\n"
        head += "Content-Length: \(body.count)\r\n"
        head += "Connection: close\r\n"
        head += "\r\n"
        var data = Data(head.utf8)
        data.append(body)
        conn.send(content: data, completion: .contentProcessed { _ in
            conn.cancel()
        })
    }

    // MARK: - Token mgmt

    private static func defaultTokenPath() -> String {
        let home = ProcessInfo.processInfo.environment["HOME"] ?? NSHomeDirectory()
        return (home as NSString).appendingPathComponent(".alma/cu/mcp-token")
    }

    private static func generateToken() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }

    private static func writeTokenFile(path: String, token: String) throws {
        let url = URL(fileURLWithPath: path)
        try FileManager.default.createDirectory(
            at: url.deletingLastPathComponent(), withIntermediateDirectories: true, attributes: nil
        )
        try token.data(using: .utf8)!.write(to: url, options: .atomic)
        // 0600 — readable only by the owning user; a world-readable token
        // on a shared machine would be a local privilege-escalation gift.
        chmod(path, 0o600)
    }

    // MARK: - Tool registry

    fileprivate struct Tool {
        let name: String
        let daemonCmd: String
        let description: String
        let inputSchema: [String: Any]

        func wireDescriptor() -> [String: Any] {
            return [
                "name": name,
                "description": description,
                "inputSchema": inputSchema,
            ]
        }
    }

    /// Curated v1 tool set. Not every Daemon verb is here — we expose
    /// the ones that model harnesses ask for most. More can be added
    /// incrementally without protocol changes.
    fileprivate static let tools: [Tool] = [
        Tool(
            name: "ping",
            daemonCmd: "ping",
            description: "Liveness check. Returns daemon version and uptime.",
            inputSchema: jsonObject(properties: [:], required: [])
        ),
        Tool(
            name: "list_running_apps",
            daemonCmd: "apps",
            description: "List all AX-trusted running applications on the system.",
            inputSchema: jsonObject(properties: [:], required: [])
        ),
        Tool(
            name: "list_windows",
            daemonCmd: "windows",
            description: "List the windows of a running application. Pass `pid` or `bundle`.",
            inputSchema: jsonObject(properties: [
                "pid": ["type": "integer", "description": "Process id of the target app"],
                "bundle": ["type": "string", "description": "Bundle identifier (e.g. com.apple.finder)"],
            ], required: [])
        ),
        Tool(
            name: "snapshot_window",
            daemonCmd: "get_app_state",
            description: "Return the AX tree + window geometry + screenshot path for the app's focused window. This is the canonical way to see what's on screen before acting.",
            inputSchema: jsonObject(properties: [
                "pid": ["type": "integer"],
                "bundle": ["type": "string"],
                "wakeup_seconds": ["type": "number", "description": "Seconds to wait for AX tree to populate (0.5-2.0)"],
                "include_screenshot": ["type": "boolean"],
            ], required: [])
        ),
        Tool(
            name: "click",
            daemonCmd: "click",
            description: "Click an AX element by ref (from snapshot_window) or a screen-point (x, y). Provide `ref` OR (`x`, `y`, `pid`). For navigation clicks on Electron apps (Slack channels, Discord servers, Notion pages, browser tabs): MUST pass `activate: true` — backgrounded Electron windows half-commit sidebar clicks (Redux updates, main panel doesn't refresh). See WEB_APPS.md.",
            inputSchema: jsonObject(properties: [
                "ref": ["type": "string", "description": "AX element ref e.g. 'e3' from snapshot_window"],
                "x": ["type": "number"],
                "y": ["type": "number"],
                "pid": ["type": "integer"],
                "bundle": ["type": "string"],
                "button": ["type": "string", "enum": ["left", "right", "middle"]],
                "click_count": ["type": "integer", "minimum": 1, "maximum": 3],
                "coord_space": ["type": "string", "enum": ["screen", "screenshot"], "description": "Which coord system x/y are in"],
                "activate": ["type": "boolean", "description": "Briefly bring the target app to system frontmost (≈200ms visible flicker) before clicking, then restore the user's prior frontmost app. REQUIRED for navigation clicks on Electron apps (channel/page/tab switch) — backgrounded clicks half-commit. Default false."],
            ], required: [])
        ),
        Tool(
            name: "type_text",
            daemonCmd: "type_text",
            description: "Type a string into the currently-focused element of an app. Auto mode uses AX insertion for refs, physical key events, and Unicode fallback.",
            inputSchema: jsonObject(properties: [
                "text": ["type": "string"],
                "pid": ["type": "integer"],
                "bundle": ["type": "string"],
                "ref": ["type": "string", "description": "Optional AX ref of the target text field"],
                "input_method": ["type": "string", "enum": ["auto", "physical", "unicode", "ax"], "description": "auto, physical, unicode, or ax"],
                "activate": ["type": "boolean", "description": "Briefly bring the target app to system frontmost before typing. Use when typing into a search/switcher modal that was just opened via a click with `activate: true` — the modal often needs the renderer to stay unthrottled to accept input. Default false."],
            ], required: ["text"])
        ),
        Tool(
            name: "press_key",
            daemonCmd: "press_key",
            description: "Press a single key combo (xdotool syntax: 'cmd+s', 'return', 'shift+tab'). FORBIDDEN for Electron-app global shortcuts (cmd+K Switch Channel, cmd+T new tab, cmd+P command palette) without `activate: true` — Chromium gates these on frontmost status; the cmd modifier is silently stripped on backgrounded targets, leaving the bare key (e.g. 'k') in the focused text composer, polluting user data. Use `click` on the toolbar Switcher button with `activate: true` instead.",
            inputSchema: jsonObject(properties: [
                "combo": ["type": "string"],
                "pid": ["type": "integer"],
                "bundle": ["type": "string"],
                "activate": ["type": "boolean", "description": "Briefly bring the target app to system frontmost before pressing. REQUIRED for global keyboard shortcuts on Electron apps (cmd+K / cmd+T / cmd+P / etc.) — backgrounded press_key drops the cmd modifier and types the bare letter into the focused field. Default false."],
            ], required: ["combo"])
        ),
        Tool(
            name: "scroll",
            daemonCmd: "scroll",
            description: "Scroll an AX element by whole pages in a given direction.",
            inputSchema: jsonObject(properties: [
                "ref": ["type": "string"],
                "direction": ["type": "string", "enum": ["up", "down", "left", "right"]],
                "pages": ["type": "integer", "minimum": 1],
            ], required: ["ref", "direction"])
        ),
        Tool(
            name: "screenshot",
            daemonCmd: "shot",
            description: "Capture a single window to a JPEG file. Returns the path; caller reads the file.",
            inputSchema: jsonObject(properties: [
                "window_id": ["type": "integer"],
                "pid": ["type": "integer"],
                "bundle": ["type": "string"],
                "out": ["type": "string", "description": "Absolute output path for the JPEG"],
                "max_width": ["type": "integer"],
            ], required: ["out"])
        ),
        Tool(
            name: "open_url",
            daemonCmd: "open_url",
            description: "Open a URL via LaunchServices. Routes a URL-scheme or document URL to the handler app without simulating a click (Layer 1).",
            inputSchema: jsonObject(properties: [
                "url": ["type": "string"],
            ], required: ["url"])
        ),
        Tool(
            name: "intent",
            daemonCmd: "intent",
            description: "Invoke a pre-registered app-native intent (Layer 1 app-command dispatch). Example: intent='play_song', bundle='com.netease.163music', args={'id': 12345}.",
            inputSchema: jsonObject(properties: [
                "bundle": ["type": "string"],
                "intent": ["type": "string"],
                "args": ["type": "object"],
            ], required: ["bundle", "intent"])
        ),
        Tool(
            name: "raise_window",
            daemonCmd: "raise",
            description: "Raise (front-most) a window. Use sparingly — most computer-use flows should keep the target backgrounded.",
            inputSchema: jsonObject(properties: [
                "pid": ["type": "integer"],
                "bundle": ["type": "string"],
                "window_id": ["type": "integer"],
            ], required: [])
        ),
        Tool(
            name: "pip_open",
            daemonCmd: "pip_open",
            description: "Open a Picture-in-Picture mirror of a target window so the user can watch the agent's work without raising the app.",
            inputSchema: jsonObject(properties: [
                "window_id": ["type": "integer"],
                "pid": ["type": "integer"],
                "bundle": ["type": "string"],
                "title": ["type": "string"],
                "width": ["type": "number"],
                "height": ["type": "number"],
            ], required: [])
        ),
        Tool(
            name: "pip_close",
            daemonCmd: "pip_close",
            description: "Close one (by window_id) or all (all: true) PIP mirrors.",
            inputSchema: jsonObject(properties: [
                "window_id": ["type": "integer"],
                "all": ["type": "boolean"],
            ], required: [])
        ),
        Tool(
            name: "pip_status",
            daemonCmd: "pip_status",
            description: "List currently-open PIP sessions and armed minimise-hijacks.",
            inputSchema: jsonObject(properties: [:], required: [])
        ),
    ]

    /// Build a JSON-schema object descriptor for an MCP tool input. This
    /// is just sugar over a dict literal — MCP expects `type: object`
    /// with `properties` and `required`.
    private static func jsonObject(properties: [String: [String: Any]], required: [String]) -> [String: Any] {
        return [
            "type": "object",
            "properties": properties,
            "required": required,
        ]
    }
}

// MARK: - OAuth 2.1 + PKCE flow

extension MCPServer {
    /// Serve RFC 8414 authorization-server metadata. Advertises the
    /// two relative endpoints (`/oauth/authorize`, `/oauth/token`),
    /// PKCE S256 support, and the single supported grant type.
    fileprivate func serveAuthServerMetadata(_ conn: NWConnection) {
        let base = "http://127.0.0.1:\(boundPort)"
        let doc: [String: Any] = [
            "issuer": base,
            "authorization_endpoint": "\(base)/oauth/authorize",
            "token_endpoint": "\(base)/oauth/token",
            "response_types_supported": ["code"],
            "grant_types_supported": ["authorization_code"],
            "code_challenge_methods_supported": ["S256"],
            "token_endpoint_auth_methods_supported": ["none"],
        ]
        sendJSON(conn, status: 200, body: doc)
    }

    /// `GET /oauth/authorize` — for a headless local server, we
    /// auto-approve the request (no consent UI). Returns a 302 redirect
    /// to `redirect_uri` with `?code=<code>&state=<state>`. The code is
    /// a one-time one-minute-TTL string bound to the client's PKCE
    /// challenge.
    fileprivate func serveAuthorize(_ conn: NWConnection, params: [String: String]) {
        let clientId = params["client_id"] ?? ""
        let redirect = params["redirect_uri"] ?? ""
        let responseType = params["response_type"] ?? ""
        let codeChallenge = params["code_challenge"] ?? ""
        let method = params["code_challenge_method"] ?? ""
        let state = params["state"] ?? ""
        let armPid = params["alma_arm_pid"].flatMap { pid_t($0) }

        if responseType != "code" {
            sendError(conn, status: 400, body: "response_type must be 'code'"); return
        }
        if codeChallenge.isEmpty || method != "S256" {
            sendError(conn, status: 400, body: "PKCE S256 code_challenge required"); return
        }
        if redirect.isEmpty {
            sendError(conn, status: 400, body: "redirect_uri required"); return
        }
        // A real authorization server would also validate client_id
        // against a registered-clients database. Here we accept any
        // client_id — the PKCE challenge is what actually binds the
        // token exchange. This matches the Codex "any local harness
        // can talk to us" posture as long as it can complete PKCE and
        // survive the SecCode check.

        let code = MCPServer.generateToken()
        stateLock.lock()
        pendingCodes[code] = PendingCode(
            codeChallenge: codeChallenge, redirectUri: redirect,
            clientId: clientId, armPid: armPid,
            expiresAt: Date().addingTimeInterval(60)
        )
        stateLock.unlock()

        let stateFrag = state.isEmpty ? "" : "&state=\(MCPServer.urlEncode(state))"
        let sep = redirect.contains("?") ? "&" : "?"
        let location = "\(redirect)\(sep)code=\(code)\(stateFrag)"
        let head = "HTTP/1.1 302 Found\r\nLocation: \(location)\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
        conn.send(content: Data(head.utf8), completion: .contentProcessed { _ in conn.cancel() })
    }

    /// `POST /oauth/token` — exchange an authorization code + PKCE
    /// verifier for an access token. One-time use: the code is deleted
    /// on success OR failure.
    fileprivate func serveToken(_ conn: NWConnection, params: [String: String]) {
        let grantType = params["grant_type"] ?? ""
        let code = params["code"] ?? ""
        let verifier = params["code_verifier"] ?? ""
        let clientId = params["client_id"] ?? ""
        let redirect = params["redirect_uri"] ?? ""

        if grantType != "authorization_code" {
            sendJSON(conn, status: 400, body: ["error": "unsupported_grant_type"]); return
        }
        stateLock.lock()
        let pending = pendingCodes.removeValue(forKey: code)
        stateLock.unlock()
        guard let p = pending else {
            sendJSON(conn, status: 400, body: ["error": "invalid_grant"]); return
        }
        if Date() > p.expiresAt {
            sendJSON(conn, status: 400, body: ["error": "invalid_grant", "error_description": "code expired"]); return
        }
        if p.redirectUri != redirect {
            sendJSON(conn, status: 400, body: ["error": "invalid_grant", "error_description": "redirect_uri mismatch"]); return
        }
        if !clientId.isEmpty, clientId != p.clientId {
            sendJSON(conn, status: 400, body: ["error": "invalid_client"]); return
        }
        // PKCE S256: BASE64URL(SHA256(verifier)) == code_challenge
        let derived = MCPServer.pkceS256(verifier: verifier)
        if derived != p.codeChallenge {
            sendJSON(conn, status: 400, body: ["error": "invalid_grant", "error_description": "code_verifier mismatch"]); return
        }

        // SecCode peer check — if an arm pid was supplied at /authorize
        // time, resolve its signing info and verify against the
        // whitelist. A matching entry is required; missing arm pid OR
        // empty whitelist = allow (legacy posture, logged to stderr so
        // operators can tighten later).
        var peerTeam: String? = nil
        var peerBundle: String? = nil
        if let pid = p.armPid {
            let info = SecCodePeer.verifySigning(pid: pid)
            peerTeam = info.teamId; peerBundle = info.bundleId
            stateLock.lock()
            let wl = whitelist
            stateLock.unlock()
            let allowed = wl.isEmpty || wl.contains(where: {
                $0.matches(teamId: peerTeam, bundleId: peerBundle, pid: pid)
            })
            if !allowed {
                FileHandle.standardError.write(Data(
                    "[alma-cu] MCP peer pid=\(pid) team=\(peerTeam ?? "?") bundle=\(peerBundle ?? "?") rejected by whitelist\n".utf8
                ))
                sendJSON(conn, status: 403, body: [
                    "error": "access_denied",
                    "error_description": "peer not in whitelist",
                    "peer_team_id": peerTeam ?? "",
                    "peer_bundle_id": peerBundle ?? "",
                ]); return
            }
        }

        let token = MCPServer.generateToken()
        let grant = AccessGrant(
            token: token, clientId: p.clientId,
            peerPid: p.armPid, peerTeamId: peerTeam, peerBundleId: peerBundle,
            expiresAt: Date().addingTimeInterval(3600)
        )
        stateLock.lock()
        accessGrants[token] = grant
        stateLock.unlock()

        sendJSON(conn, status: 200, body: [
            "access_token": token,
            "token_type": "Bearer",
            "expires_in": 3600,
            "peer_team_id": peerTeam ?? NSNull(),
            "peer_bundle_id": peerBundle ?? NSNull(),
        ] as [String: Any])
    }

    fileprivate func isValidAccessToken(_ token: String) -> Bool {
        stateLock.lock(); defer { stateLock.unlock() }
        guard let g = accessGrants[token] else { return false }
        if Date() > g.expiresAt {
            accessGrants.removeValue(forKey: token)
            return false
        }
        return true
    }

    // MARK: - Whitelist CRUD (for the `mcp_allow` daemon verbs)

    func addWhitelist(teamId: String?, bundleId: String?, pid: pid_t?, ttlSeconds: TimeInterval?) {
        stateLock.lock(); defer { stateLock.unlock() }
        let exp = ttlSeconds.map { Date().addingTimeInterval($0) }
        whitelist.append(WhitelistEntry(teamId: teamId, bundleId: bundleId, pid: pid, expiresAt: exp))
    }

    func clearWhitelist() {
        stateLock.lock(); defer { stateLock.unlock() }
        whitelist.removeAll()
    }

    func whitelistList() -> [[String: Any]] {
        stateLock.lock(); defer { stateLock.unlock() }
        return whitelist.map { w in
            [
                "team_id": w.teamId ?? NSNull(),
                "bundle_id": w.bundleId ?? NSNull(),
                "pid": w.pid.map { Int($0) } ?? NSNull(),
                "expires_at": w.expiresAt.map { $0.timeIntervalSince1970 } ?? NSNull(),
            ]
        }
    }
}

// MARK: - Query / form parsing + PKCE helpers

extension MCPServer {
    fileprivate static func urlEncode(_ s: String) -> String {
        return s.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? s
    }

    fileprivate func splitQuery(_ pathAndQuery: String) -> (path: String, params: [String: String]) {
        guard let qIdx = pathAndQuery.firstIndex(of: "?") else {
            return (pathAndQuery, [:])
        }
        let path = String(pathAndQuery[..<qIdx])
        let query = String(pathAndQuery[pathAndQuery.index(after: qIdx)...])
        return (path, MCPServer.parseQueryString(query))
    }

    fileprivate func parseForm(_ body: Data) -> [String: String] {
        guard let s = String(data: body, encoding: .utf8) else { return [:] }
        return MCPServer.parseQueryString(s)
    }

    fileprivate static func parseQueryString(_ s: String) -> [String: String] {
        var out: [String: String] = [:]
        for pair in s.split(separator: "&") {
            let parts = pair.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)
            let k = parts[0].removingPercentEncoding ?? String(parts[0])
            let v = parts.count > 1 ? (parts[1].removingPercentEncoding ?? String(parts[1])) : ""
            out[k] = v
        }
        return out
    }

    /// BASE64URL(SHA256(verifier)) — the PKCE S256 derivation.
    fileprivate static func pkceS256(verifier: String) -> String {
        guard let data = verifier.data(using: .utf8) else { return "" }
        var digest = [UInt8](repeating: 0, count: Int(CC_SHA256_DIGEST_LENGTH))
        data.withUnsafeBytes { buf in
            _ = CC_SHA256(buf.baseAddress, CC_LONG(data.count), &digest)
        }
        let b64 = Data(digest).base64EncodedString()
        // base64url: + → -, / → _, strip =
        return b64.replacingOccurrences(of: "+", with: "-")
                  .replacingOccurrences(of: "/", with: "_")
                  .replacingOccurrences(of: "=", with: "")
    }
}

// MARK: - SecCode peer signing-info lookup

/// Thin wrapper around `SecCodeCopyGuestWithAttributes(kSecGuestAttributePid:)`
/// + `SecCodeCopySigningInformation`. Given a pid, returns the process's
/// `kSecCodeInfoTeamIdentifier` + `kSecCodeInfoIdentifier`. Used by the
/// MCP server to verify that an armed peer is who it claims to be.
///
/// Note: peer-pid-from-TCP-connection auto-resolution (walking
/// `proc_pidinfo(PROC_PIDFDSOCKETINFO)` to find who owns the ephemeral
/// client port) is NOT yet wired. For v2 we require the caller to
/// explicitly supply `alma_arm_pid` at authorize time; the daemon
/// `mcp_arm_peer` verb is how a human operator or another agent can
/// authorise a specific peer before it completes the OAuth handshake.
enum SecCodePeer {
    static func verifySigning(pid: pid_t) -> (teamId: String?, bundleId: String?) {
        let attrs: [String: Any] = [
            kSecGuestAttributePid as String: NSNumber(value: Int(pid)),
        ]
        var codeRef: SecCode?
        let status = SecCodeCopyGuestWithAttributes(nil, attrs as CFDictionary, [], &codeRef)
        guard status == errSecSuccess, let code = codeRef else { return (nil, nil) }

        var info: CFDictionary?
        let infoStatus = SecCodeCopySigningInformation(
            code as! SecStaticCode,
            SecCSFlags(rawValue: kSecCSSigningInformation),
            &info
        )
        guard infoStatus == errSecSuccess, let dict = info as? [String: Any] else {
            return (nil, nil)
        }
        let team = dict[kSecCodeInfoTeamIdentifier as String] as? String
        let bid = dict[kSecCodeInfoIdentifier as String] as? String
        return (team, bid)
    }
}

// MARK: - Prompts + resources content

extension MCPServer {
    /// Small set of prompt templates. MCP clients render these in the
    /// user's turn-composer so people can kick off common Alma flows
    /// without remembering the exact tool sequence.
    fileprivate static let prompts: [[String: Any]] = [
        [
            "name": "snapshot_then_click",
            "description": "Snapshot the focused window of an app, then click an element by its label text.",
            "arguments": [
                ["name": "bundle", "description": "App bundle id (e.g. com.apple.finder)", "required": true],
                ["name": "label", "description": "Visible text of the element to click", "required": true],
            ],
        ],
        [
            "name": "list_running_and_snapshot",
            "description": "Enumerate running apps, then snapshot the first window of the most recently used one.",
            "arguments": [],
        ],
    ]

    fileprivate static func renderPrompt(name: String, args: [String: Any]) -> String {
        switch name {
        case "snapshot_then_click":
            let bundle = args["bundle"] as? String ?? "<bundle>"
            let label = args["label"] as? String ?? "<label>"
            return """
            Call `snapshot_window` with bundle=\(bundle). In the returned AX tree, find the element whose visible label or title contains "\(label)". Then call `click` with its ref (e.g. `e7`).
            """
        case "list_running_and_snapshot":
            return """
            Call `list_running_apps`. Pick the most recently used app (largest `last_used_seconds_ago`). Call `snapshot_window` with its `pid`.
            """
        default:
            return "Unknown prompt: \(name)"
        }
    }

    /// Resources = read-only JSON/document URIs backed by daemon data.
    /// Today we expose one: `alma-cu://tool-catalog` describing the
    /// same tool set tools/list returns, in a self-documenting form.
    fileprivate static func resources() -> [[String: Any]] {
        return [
            [
                "uri": "alma-cu://tool-catalog",
                "name": "Alma CUA tool catalog",
                "description": "Machine-readable description of every tool the Alma MCP server exposes.",
                "mimeType": "application/json",
            ],
        ]
    }

    fileprivate static func readResource(uri: String) throws -> Any {
        switch uri {
        case "alma-cu://tool-catalog":
            let catalog: [String: Any] = [
                "version": "0.2",
                "tools": MCPServer.tools.map { $0.wireDescriptor() },
            ]
            let data = try JSONSerialization.data(withJSONObject: catalog, options: [.prettyPrinted])
            let txt = String(data: data, encoding: .utf8) ?? "{}"
            return [
                "contents": [
                    ["uri": uri, "mimeType": "application/json", "text": txt],
                ],
            ]
        default:
            throw MCPError(code: -32602, message: "Unknown resource uri: \(uri)")
        }
    }
}

// MARK: - MCP error

fileprivate struct MCPError: Error {
    let code: Int
    let message: String
}
