import SwiftUI

/// One Feed card: the illustration of its kind, the kind, a bold title, two lines of context, and a quiet row with
/// Like, Not right, the card's age and its sources.
struct FeedCard: View {
    let card: FeedCardModel
    let choice: FeedStore.Choice?
    /// The pairing allows feedback.
    let canGiveFeedback: Bool
    /// The Mac is reachable; feedback is disabled while it is offline.
    let canSend: Bool
    let now: Date
    let like: () -> Void
    let notRight: () -> Void
    let showSources: () -> Void
    var isSelected = false
    var actionsAtTop = false
    var showsActions = true
    var briefFeedback = false
    var correction: AnyView? = nil
    var onSelect: (() -> Void)? = nil

    @State private var link: BriefLinkDestination?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if actionsAtTop && showsActions { actionRow }
            HStack(alignment: .top, spacing: 14) {
                if !briefFeedback { FeedArt(category: card.category) }
                VStack(alignment: .leading, spacing: 4) {
                    if !briefFeedback {
                        Text(verbatim: card.category.label.uppercased())
                            .font(.caption2.weight(.semibold))
                            .tracking(0.6)
                            .foregroundStyle(YansuTheme.secondary)
                    }
                    title
                    if let status = card.statusText {
                        Text(verbatim: status)
                            .font(.caption.weight(.medium))
                            .foregroundStyle(YansuTheme.secondary)
                            .accessibilityIdentifier("feed.card.status")
                    }
                    if let detail = card.item.detail, !detail.isEmpty {
                        Text(verbatim: detail)
                            .font(.subheadline)
                            .foregroundStyle(YansuTheme.secondary)
                            .lineLimit(card.detailLineLimit)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            if !actionsAtTop && showsActions { actionRow }
            if let correction { correction }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(isSelected ? YansuTheme.accentWash : YansuTheme.surface,
                    in: RoundedRectangle(cornerRadius: YansuTheme.cornerRadius, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: YansuTheme.cornerRadius, style: .continuous)
                .strokeBorder(YansuTheme.border, lineWidth: 1)
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("feed.card")
        .contentShape(Rectangle())
        .modifier(FeedCardSelectionGesture(onSelect: onSelect))
        .sheet(item: $link) { destination in
            BriefSafariView(url: destination.url)
        }
    }

    @ViewBuilder
    private var title: some View {
        let text = card.item.title ?? String(localized: "Untitled")
        if let url = card.item.webURL {
            Button {
                link = BriefLinkDestination(url: url)
            } label: {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(verbatim: text)
                        .multilineTextAlignment(.leading)
                    Image(systemName: "arrow.up.right")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(YansuTheme.secondary)
                        .accessibilityHidden(true)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .font(.body.weight(.semibold))
            .foregroundStyle(YansuTheme.ink)
            .lineLimit(2)
            .accessibilityHint("Opens a web page in Isoform")
            .accessibilityIdentifier("feed.card.link")
        } else {
            Text(verbatim: text)
                .font(.body.weight(.semibold))
                .foregroundStyle(YansuTheme.ink)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var actionRow: some View {
        Group {
            if briefFeedback {
                briefActionRow
            } else {
                feedActionRow
            }
        }
    }

    private var briefActionRow: some View {
        HStack(spacing: 8) {
            if !card.sources.isEmpty {
                Button(String(localized: "Sources"), action: showSources)
                    .buttonStyle(YansuChipButtonStyle())
                    .accessibilityIdentifier("feed.sources")
            }
            if canGiveFeedback {
                if card.memoryID == nil {
                    Button(String(localized: "Useful"), action: like)
                        .buttonStyle(YansuChipButtonStyle())
                        .disabled(choice != nil || !canSend)
                        .accessibilityIdentifier("feed.like")
                }
                Button(card.memoryID == nil ? String(localized: "Not for me") : String(localized: "Not quite"), action: notRight)
                    .buttonStyle(YansuChipButtonStyle())
                    .disabled(choice != nil || !canSend)
                    .accessibilityIdentifier("feed.notRight")
            }
        }
    }

    private var feedActionRow: some View {
        HStack(spacing: 2) {
            if canGiveFeedback {
                actionButton(choice?.action == .like ? String(localized: "Liked") : String(localized: "Like"),
                             systemImage: choice?.action == .like ? "hand.thumbsup.fill" : "hand.thumbsup",
                             selected: choice?.action == .like, identifier: "feed.like", action: like)
                    .disabled(choice != nil || !canSend)
                actionButton(String(localized: "Not right"),
                             systemImage: choice?.action == .notRight || choice?.action == .notRelevant ? "hand.thumbsdown.fill" : "hand.thumbsdown",
                             selected: choice?.action == .notRight || choice?.action == .notRelevant, identifier: "feed.notRight", action: notRight)
                    .disabled(choice != nil || !canSend)
            }
            Spacer(minLength: 8)
            if let age = Self.age(of: card.item.atDate, now: now) {
                Text(verbatim: age)
                    .font(.caption)
                    .foregroundStyle(YansuTheme.secondary)
                    .accessibilityLabel(Self.ageDescription(of: card.item.atDate, now: now) ?? age)
            }
            if !card.sources.isEmpty {
                Button(action: showSources) {
                    Image(systemName: "info.circle")
                        .font(.footnote)
                        .foregroundStyle(YansuTheme.tertiary)
                        .frame(width: 36, height: 32)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Sources")
                .accessibilityIdentifier("feed.sources")
            }
        }
        .padding(.leading, 58)
    }

    private func actionButton(_ title: String, systemImage: String, selected: Bool, identifier: String,
                              action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Label { Text(verbatim: title) } icon: { Image(systemName: systemImage) }
                .font(.caption.weight(.medium))
                // The choice made stays in ink and the other in secondary ink, so the two differ in both themes.
                .foregroundStyle(selected ? YansuTheme.ink : YansuTheme.secondary)
                .padding(.horizontal, 8)
                .frame(minHeight: 32)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityValue(selected && choice?.delivery == .pending ? String(localized: "Waiting for your computer") : "")
        .accessibilityIdentifier(identifier)
    }

    /// "15m", "2h", "3d": how long ago the item happened, the way a feed shows it.
    static func age(of date: Date?, now: Date) -> String? {
        guard let date else { return nil }
        let minutes = max(0, Int(now.timeIntervalSince(date) / 60))
        if minutes < 1 { return String(localized: "Now") }
        if minutes < 60 { return String(localized: "\(minutes)m") }
        if minutes < 24 * 60 { return String(localized: "\(minutes / 60)h") }
        return String(localized: "\(minutes / (24 * 60))d")
    }

    static func ageDescription(of date: Date?, now: Date) -> String? {
        guard let date else { return nil }
        return RelativeDateTimeFormatter().localizedString(for: date, relativeTo: now)
    }
}

/// The illustration for a kind of card: a vector image from the asset catalog, with its own light and dark art.
private struct FeedCardSelectionGesture: ViewModifier {
    let onSelect: (() -> Void)?

    @ViewBuilder
    func body(content: Content) -> some View {
        if let onSelect {
            content.simultaneousGesture(TapGesture().onEnded { onSelect() })
        } else {
            content
        }
    }
}

struct FeedArt: View {
    let category: FeedCategory
    var size: CGFloat = 44

    var body: some View {
        Image(category.artName)
            .resizable()
            .interpolation(.high)
            .aspectRatio(contentMode: .fit)
            .frame(width: size, height: size)
            .accessibilityHidden(true)
    }
}
