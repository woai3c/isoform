import SwiftUI

/// The brief in Chat (design section 3): its title, opening sentence, Play and Read all. Read all opens the same brief
/// view as Today. Chat never shows a player for a brief with no words, so the caller passes only briefs with words.
struct ChatBriefCard: View {
    let brief: MobileBriefV1
    let hubID: String
    @ObservedObject var player: BriefAudioPlayer
    @EnvironmentObject private var store: BriefStore
    @EnvironmentObject private var router: NotificationRouter

    private var selected: Bool { player.matches(brief: brief, hubID: hubID) }
    private var playing: Bool { selected && player.state == .playing }
    private var loading: Bool { selected && player.state == .loading }
    private var startable: Bool {
        brief.audio != nil && player.canStart(brief: brief, hubID: hubID, computerOnline: store.hubOnline)
    }

    var body: some View {
        YansuCard {
            VStack(alignment: .leading, spacing: 10) {
                Text(verbatim: brief.titleText)
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(YansuTheme.ink)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("chat.brief.title")
                if let lead = PastBriefIndex.lead(brief) {
                    Text(verbatim: lead)
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                        .lineLimit(3)
                        .accessibilityIdentifier("chat.brief.lead")
                }
                HStack(spacing: 10) {
                    if brief.audio != nil {
                        Button {
                            Task { await player.toggle(brief: brief, hubID: hubID) }
                        } label: {
                            HStack(spacing: 6) {
                                if loading {
                                    ProgressView().controlSize(.small)
                                } else {
                                    Image(systemName: playing ? "pause.fill" : "play.fill")
                                        .accessibilityHidden(true)
                                }
                                Text(playing ? String(localized: "Pause") : String(localized: "Play"))
                            }
                        }
                        .buttonStyle(YansuPillButtonStyle(prominent: true))
                        .disabled(!startable || loading)
                        .opacity(startable ? 1 : 0.4)
                        .accessibilityLabel(playing ? String(localized: "Pause your brief") : String(localized: "Hear your brief"))
                        .accessibilityIdentifier("chat.brief.play")
                    }
                    Button(String(localized: "Read all")) {
                        router.openBrief(brief.identifier)
                    }
                    .buttonStyle(YansuPillButtonStyle(prominent: false))
                    .accessibilityIdentifier("chat.brief.readAll")
                }
                if brief.audio != nil, !startable {
                    Text("Plays when your computer is back")
                        .font(.footnote)
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityIdentifier("chat.brief.unavailable")
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("chat.brief")
    }
}
