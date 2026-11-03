import AppIntents
import Foundation

/// WidgetKit runs an audio playback intent in the app process.
struct WidgetPlayIntent: AudioPlaybackIntent {
    static let title: LocalizedStringResource = "Play brief"
    static var openAppWhenRun: Bool = false
    static var authenticationPolicy: IntentAuthenticationPolicy = .requiresAuthentication

    @MainActor
    func perform() async throws -> some IntentResult {
        #if !WIDGET_EXTENSION
        let app = AppEnvironment.shared
        app.brief.loadCached()
        if let brief = app.brief.current, let hub = app.vault.current()?.hubID,
           let date = brief.asOfDate, Calendar.current.isDateInToday(date) {
            await app.audioPlayer.toggle(brief: brief, hubID: hub)
            app.refreshWidget()
        }
        #endif
        return .result()
    }
}
