import AppKit
import Foundation

/// Layer 1 of the three-layer CUA dispatch stack.
///
/// Reverse-engineered from Codex.app 2026-04-22: when Codex wants NetEase
/// Music to play, it doesn't synthesize a click and it doesn't use AX. It
/// calls `NSWorkspace.open(URL)` → LaunchServices → AppleEvents, letting
/// NetEase parse the URL scheme (`orpheus://…`) internally. Zero focus
/// change, zero synthesized input, zero dependency on the target's AX
/// tree (which for NetEase is empty). This is the mechanism we see as 4
/// paired `AESendMessage` calls in dtrace under `_LSOpenStuffWithPreSanitizedURLs`.
///
/// Dispatch order in Daemon:
///   1. `cmd: "intent"` → this registry (if a URL template exists) — no flicker.
///   2. `cmd: "click"` → `AXBridge.click(strategy: .auto)` — tries AX, then synth.
///   3. Pure coord synth via EventDispatch — last resort, may flicker on
///      Electron apps that reject non-key-window events.
///
/// The registry is hand-curated. To add an app: find its `CFBundleURLSchemes`
/// in `/Applications/<App>.app/Contents/Info.plist`, then reverse (or look
/// up public docs for) the specific URL shape its scheme accepts.
enum AppCommandRegistry {

    /// A single intent the registry can dispatch on an app.
    struct Intent {
        /// Short stable id used by callers.
        let name: String
        /// Human-readable — surfaced in `list_apps` output so the agent can
        /// pick the right one without trial and error.
        let description: String
        /// Required param names. Missing any → `invalid_arg`.
        let requiredParams: [String]
        /// Documented-but-optional param names.
        let optionalParams: [String]
        /// Builds the URL to open. Returning nil signals "params were
        /// syntactically present but produced no valid URL" (e.g. bad
        /// characters after percent-encoding).
        let build: (_ params: [String: String]) -> URL?
    }

    // MARK: - Seed registry
    //
    // Conservative: only intents we can produce deterministically from
    // params. "Play today's recommendation" on NetEase is NOT here because
    // the URL for it is not stable across NetEase versions — that kind of
    // goal is better handled by Layer 2 (AX click on the rec row) or by
    // teaching the agent the stable URL once it's reverse-engineered.

    private static let entries: [String: [Intent]] = [

        // NetEase Music — `orpheus://` — confirmed installed, confirmed
        // usable for song / playlist IDs. No flicker.
        "com.netease.163music": [
            Intent(
                name: "launch",
                description: "Open NetEase Music without changing frontmost app.",
                requiredParams: [],
                optionalParams: [],
                build: { _ in URL(string: "orpheus://") }
            ),
            Intent(
                name: "play_song",
                description: "Play a specific NetEase song by its numeric song_id.",
                requiredParams: ["song_id"],
                optionalParams: [],
                build: { p in
                    guard let id = p["song_id"] else { return nil }
                    return URL(string: "orpheus://song/?id=\(urlSafe(id))")
                }
            ),
            Intent(
                name: "open_playlist",
                description: "Open a NetEase playlist by its numeric playlist_id.",
                requiredParams: ["playlist_id"],
                optionalParams: [],
                build: { p in
                    guard let id = p["playlist_id"] else { return nil }
                    return URL(string: "orpheus://playlist/?id=\(urlSafe(id))")
                }
            ),
            // The next three intents come from reversing
            // `NeteaseMusic.app/Contents/Resources/resources.pack`
            // (a ZIP containing the Next.js-built web UI). The app's
            // router responds to `orpheus://route/<name>` where
            // `<name>` is one of the in-app route identifiers. We
            // confirmed `musicDesktop` as a literal route and
            // discovered the rest (`dailyRecommend`, etc.) from the
            // webfiles/hybrid bundle's route table. Runtime
            // verification requires either user observation or the
            // scripts/dtrace-netease-url.d capture (see that file).
            Intent(
                name: "play_daily_recommendation",
                description: "Open NetEase's '每日推荐' (daily recommendation) page. Discovered via webfiles/hybrid route '/dailyRecommend'.",
                requiredParams: [],
                optionalParams: [],
                build: { _ in URL(string: "orpheus://route/dailyRecommend") }
            ),
            Intent(
                name: "open_history_recommend",
                description: "Open the historical recommendations page. Route '/historyRecommend'.",
                requiredParams: [],
                optionalParams: [],
                build: { _ in URL(string: "orpheus://route/historyRecommend") }
            ),
            Intent(
                name: "open_route",
                description: "Generic NetEase route dispatcher. `route` param is one of: dailyRecommend, historyRecommend, historyPlaylist, styleRecommend, similarArtist, ranking, playlist, album, artist, albumlist, musicDesktop, localMusic, login. Other values may also work — this is the raw `orpheus://route/<name>` bridge.",
                requiredParams: ["route"],
                optionalParams: [],
                build: { p in
                    guard let r = p["route"], !r.isEmpty else { return nil }
                    return URL(string: "orpheus://route/\(urlSafe(r))")
                }
            ),
        ],

        // Apple Music — `music:` scheme plus plain https URLs.
        "com.apple.Music": [
            Intent(
                name: "launch",
                description: "Open Music.app in the background.",
                requiredParams: [],
                optionalParams: [],
                build: { _ in URL(string: "music:") }
            ),
            Intent(
                name: "open_url",
                description: "Open an Apple Music URL (music.apple.com/… or music://…).",
                requiredParams: ["url"],
                optionalParams: [],
                build: { p in p["url"].flatMap { URL(string: $0) } }
            ),
        ],

        // Spotify — `spotify:` URIs route to the desktop client.
        "com.spotify.client": [
            Intent(
                name: "launch",
                description: "Open Spotify in the background.",
                requiredParams: [],
                optionalParams: [],
                build: { _ in URL(string: "spotify:") }
            ),
            Intent(
                name: "play_uri",
                description: "Play a Spotify URI, e.g. spotify:track:4iV5W9uYEdYUVa79Axb7Rh or spotify:playlist:37i9dQZF1DXcBWIGoYBM5M.",
                requiredParams: ["uri"],
                optionalParams: [],
                build: { p in p["uri"].flatMap { URL(string: $0) } }
            ),
        ],

        // Mail.app — standard mailto:.
        "com.apple.mail": [
            Intent(
                name: "compose",
                description: "Open a new message. All params optional: to, subject, body, cc, bcc.",
                requiredParams: [],
                optionalParams: ["to", "subject", "body", "cc", "bcc"],
                build: { p in
                    var comps = URLComponents(string: "mailto:\(urlSafe(p["to"] ?? ""))")
                    var qs: [URLQueryItem] = []
                    for k in ["subject", "body", "cc", "bcc"] {
                        if let v = p[k] { qs.append(URLQueryItem(name: k, value: v)) }
                    }
                    if !qs.isEmpty { comps?.queryItems = qs }
                    return comps?.url
                }
            ),
        ],

        // Safari — http/https delegates.
        "com.apple.Safari": [
            Intent(
                name: "open_url",
                description: "Open a URL in Safari. Pass a full http/https URL.",
                requiredParams: ["url"],
                optionalParams: [],
                build: { p in p["url"].flatMap { URL(string: $0) } }
            ),
        ],

        // WeChat — only `launch` is reliably public. Chats/contacts are
        // handled inside the app.
        "com.tencent.xinWeChat": [
            Intent(
                name: "launch",
                description: "Open WeChat.",
                requiredParams: [],
                optionalParams: [],
                build: { _ in URL(string: "weixin://") }
            ),
        ],

        // Slack — `slack://` deep-links. Team + channel IDs look like
        // T02ABCDEF / C02ABCDEF.
        "com.tinyspeck.slackmacgap": [
            Intent(
                name: "launch",
                description: "Open Slack.",
                requiredParams: [],
                optionalParams: [],
                build: { _ in URL(string: "slack://") }
            ),
            Intent(
                name: "open_channel",
                description: "Open a Slack channel or DM. team_id like T02ABCDEF, channel_id like C02ABCDEF.",
                requiredParams: ["team_id", "channel_id"],
                optionalParams: [],
                build: { p in
                    guard let t = p["team_id"], let c = p["channel_id"] else { return nil }
                    return URL(string: "slack://channel?team=\(urlSafe(t))&id=\(urlSafe(c))")
                }
            ),
        ],
    ]

    // MARK: - Public API

    /// All intents registered for a bundle. Used by `list_apps` to advertise
    /// Layer 1 options to the agent. Returns [] for unregistered bundles.
    static func intents(for bundleId: String) -> [Intent] {
        entries[bundleId] ?? []
    }

    /// Dispatch an intent. Opens the resulting URL without activating the
    /// target app. Returns the URL opened (for logging / agent feedback).
    @discardableResult
    static func dispatch(
        bundleId: String,
        intent intentName: String,
        params: [String: String]
    ) throws -> URL {
        guard let list = entries[bundleId] else {
            throw CUError.invalidArg(
                "no intents registered for bundle \"\(bundleId)\" — either add it to AppCommandRegistry, or use `click`/`type_text` instead"
            )
        }
        guard let intent = list.first(where: { $0.name == intentName }) else {
            let known = list.map { $0.name }.joined(separator: ", ")
            throw CUError.invalidArg(
                "unknown intent \"\(intentName)\" for \(bundleId); known intents: [\(known)]"
            )
        }
        for req in intent.requiredParams where params[req] == nil {
            throw CUError.invalidArg(
                "intent \"\(intentName)\" requires param \"\(req)\""
            )
        }
        guard let url = intent.build(params) else {
            throw CUError.invalidArg(
                "intent \"\(intentName)\" could not build a valid URL from the given params"
            )
        }
        try openQuietly(url: url, preferredBundleId: bundleId)
        return url
    }

    /// Open a URL without activating the handling app.
    ///
    /// Two cases:
    ///   • `preferredBundleId` given → route to that specific app via its
    ///     app URL (so e.g. an `https://` URL can be force-opened in
    ///     Safari rather than the user's default browser).
    ///   • `preferredBundleId` nil → let LaunchServices pick the handler
    ///     by scheme.
    ///
    /// Both use `NSWorkspace.OpenConfiguration.activates = false`, which
    /// is what keeps Layer 1 flicker-free. The completion handler fires
    /// after LaunchServices returns — we wait up to 5s so callers get a
    /// real failure signal if the target app refuses to handle the URL.
    static func openQuietly(
        url: URL,
        preferredBundleId: String? = nil
    ) throws {
        let config = NSWorkspace.OpenConfiguration()
        // The whole point of Layer 1: don't bring the app forward. The
        // Codex dtrace trace confirms this: 4 `AESendMessage` calls on
        // NetEase, zero `AXApplicationActivated`, zero frontmost change.
        config.activates = false
        config.addsToRecentItems = false
        config.hidesOthers = false

        let sem = DispatchSemaphore(value: 0)
        var failure: Error?
        var didSignal = false

        let onComplete: (Error?) -> Void = { err in
            failure = err
            // NSWorkspace docs don't guarantee single completion; guard.
            if !didSignal {
                didSignal = true
                sem.signal()
            }
        }

        if let bid = preferredBundleId,
           let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bid)
        {
            NSWorkspace.shared.open([url], withApplicationAt: appURL, configuration: config) { _, err in
                onComplete(err)
            }
        } else {
            NSWorkspace.shared.open(url, configuration: config) { _, err in
                onComplete(err)
            }
        }

        // 5s cap: Codex trace shows NetEase dispatch completing in <100ms.
        // Anything longer means the handler app is hung or the URL is
        // un-openable. Better to surface a timeout than block the daemon.
        let r = sem.wait(timeout: .now() + 5.0)
        if r == .timedOut {
            throw CUError.internalError(
                "NSWorkspace.open(\(url.absoluteString)) timed out after 5s"
            )
        }
        if let err = failure {
            throw CUError.internalError(
                "NSWorkspace.open(\(url.absoluteString)) failed: \(err.localizedDescription)"
            )
        }
    }

    // MARK: - Helpers

    /// Percent-encode a URL path/query component so it survives URL(string:).
    /// We default to `.urlQueryAllowed` which is the least aggressive encoder
    /// that still escapes `&`, `=`, `#` — the characters that would break
    /// downstream URL parsing. Song/playlist IDs are always numeric so this
    /// is a no-op for them; only matters for free-form string params like
    /// mailto bodies.
    private static func urlSafe(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? s
    }
}
