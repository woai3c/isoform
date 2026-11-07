import SafariServices
import SwiftUI

// The Brief tab became the Feed (FeedView). Its text helpers keep this name because Chat shows times with them.
enum BriefView {
    // MARK: Text

    static func dayTitle(_ brief: MobileBriefV1) -> String {
        guard let localDate = brief.localDate, let day = BriefDates.day.date(from: localDate) else {
            return "Today"
        }
        let formatter = DateFormatter()
        // localDate is a calendar day in the Mac's timezone. Format it in UTC so it never shifts a day.
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        formatter.locale = .current
        formatter.setLocalizedDateFormatFromTemplate("EEEEMMMMd")
        return formatter.string(from: day)
    }

    static func asOfText(_ date: Date) -> String {
        if Calendar.current.isDateInToday(date) {
            return timeText(date)
        }
        return BriefClock.stamp(date)
    }

    /// The time in brief copy (`BriefClock`): the design's zh-Hans form, the system's short time elsewhere.
    static func timeText(_ date: Date) -> String {
        BriefClock.text(date)
    }
}

// Shared with History through BriefContentView. Missing sections from older Macs stay hidden.
struct BriefPurposeSections: View {
    let brief: MobileBriefV1
    var now: Date = Date()
    let showEvidence: (EvidenceRequest) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            if !brief.sections.workingToward.isEmpty {
                section("What you're working toward", items: Array(brief.sections.workingToward.prefix(5)))
            }
            if !brief.sections.commitments.isEmpty {
                section("Commitments due or slipping", items: brief.sections.commitments)
            }
            if !brief.sections.waitingOnYou.isEmpty {
                section("People waiting on you", items: Array(brief.sections.waitingOnYou.prefix(3)))
            }
            if (brief.limits?.excludedSensitiveItems ?? 0) > 0 {
                Text("Sensitive, private, or paused activity is excluded from this brief.")
                    .font(.footnote)
                    .foregroundStyle(YansuTheme.tertiary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func section(_ title: String, items: [BriefItem]) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            SectionLabel(title: title)
                .fixedSize(horizontal: false, vertical: true)
            VStack(alignment: .leading, spacing: 16) {
                ForEach(Array(items.enumerated()), id: \.offset) { index, item in
                    if index > 0 {
                        Divider().overlay(YansuTheme.border)
                    }
                    itemRow(item)
                }
            }
        }
    }

    private func itemRow(_ item: BriefItem) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            BriefItemTitle(item: item)
            if let detail = item.detail, !detail.isEmpty {
                Text(detail)
                    .font(.subheadline)
                    .foregroundStyle(YansuTheme.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if item.evidenceUnavailable == true {
                if let status = item.status, !status.isEmpty {
                    Text(BriefContentView.humanize(status))
                        .font(.caption)
                        .foregroundStyle(YansuTheme.secondary)
                }
            } else {
                if let nextStep = item.nextStep, !nextStep.isEmpty {
                    Text("Next: \(nextStep)")
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.ink)
                        .fixedSize(horizontal: false, vertical: true)
                }
                VStack(alignment: .leading, spacing: 4) {
                    let dueLabel = item.dueLabel(now: now, timezone: brief.timezone)
                    if let dueLabel {
                        Text(dueLabel)
                    } else if item.dueDate == nil, item.status == "slipping" {
                        Text("Slipping")
                    } else if item.dueDate == nil, item.status == "due_today" {
                        Text("Due today")
                    }
                    if let lastTouch = item.lastTouchDate {
                        Text("Last touch: \(Self.dateTime(lastTouch, timezone: brief.timezone, includeTime: dueLabel == nil))")
                    } else {
                        Text("No recorded touch")
                    }
                    if dueLabel == nil, let dueDate = item.dueDate {
                        Text("Due: \(Self.dateTime(dueDate, timezone: brief.timezone))")
                    }
                }
                .font(.caption)
                .foregroundStyle(YansuTheme.secondary)
                .fixedSize(horizontal: false, vertical: true)
                if item.hasMainChatReference {
                    BriefMomentReference(item: item)
                }
                let sources = brief.purposeSources(for: item)
                if !sources.isEmpty {
                    EvidenceChip(title: sources.count == 1 ? "1 source" : "\(sources.count) sources") {
                        showEvidence(EvidenceRequest(title: item.title ?? "Sources", sources: sources))
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .contain)
    }

    static func dateTime(_ date: Date, timezone: String?, locale: Locale = .current, includeTime: Bool = true) -> String {
        let formatter = DateFormatter()
        formatter.locale = locale
        formatter.timeZone = timezone.flatMap(TimeZone.init(identifier:)) ?? .current
        formatter.dateStyle = .medium
        formatter.timeStyle = includeTime ? .short : .none
        return formatter.string(from: date)
    }
}

// Keep the control's destination explicit so cached reports with no session ID still lead to Chat.
enum BriefChatReferenceDestination: Equatable {
    case conversation(String)
    case chatTab
    case macOnly

    @MainActor
    static func resolve(item: BriefItem, scopes: [String]) -> Self {
        guard SettingsView.allowsChat(scopes) else { return .macOnly }
        guard let chatID = item.chatSessionId, !chatID.isEmpty else { return .chatTab }
        return .conversation(chatID)
    }
}

// A reference opens the stored conversation; it never sends the moment again.
private struct BriefMomentReference: View {
    let item: BriefItem
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var router: NotificationRouter

    var body: some View {
        Group {
            switch BriefChatReferenceDestination.resolve(item: item, scopes: pairing.paired?.scopes ?? []) {
            case let .conversation(chatID):
                NavigationLink {
                    ChatView(chatID: chatID)
                } label: {
                    Text("Already in your chat")
                }
            case .chatTab:
                Button("Already in your chat") {
                    router.openMainChat()
                }
            case .macOnly:
                Text("Already in your main chat on your computer")
            }
        }
        .font(.footnote.weight(.medium))
        .foregroundStyle(YansuTheme.accent)
        .buttonStyle(.plain)
        .frame(minHeight: 44, alignment: .leading)
        .fixedSize(horizontal: false, vertical: true)
        .accessibilityIdentifier("brief.existingMoment")
    }
}

// Shared by the Brief and History item rows. Unsupported links stay ordinary titles.
struct BriefItemTitle: View {
    let item: BriefItem
    @State private var destination: BriefLinkDestination?

    var body: some View {
        Group {
            if let url = item.webURL {
                Button {
                    destination = BriefLinkDestination(url: url)
                } label: {
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text(item.title ?? "Open link")
                            .multilineTextAlignment(.leading)
                        Image(systemName: "arrow.up.right")
                            .font(.footnote.weight(.semibold))
                            .accessibilityHidden(true)
                    }
                    .frame(minHeight: 44, alignment: .leading)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundStyle(YansuTheme.accent)
                .accessibilityHint("Opens a web page in Isoform")
                .accessibilityIdentifier("brief.openLink")
            } else {
                Text(item.title ?? "Untitled")
                    .foregroundStyle(YansuTheme.ink)
            }
        }
        .font(.body.weight(.semibold))
        .fixedSize(horizontal: false, vertical: true)
        .sheet(item: $destination) { destination in
            BriefSafariView(url: destination.url)
        }
    }
}

struct BriefLinkDestination: Identifiable {
    let url: URL
    var id: String { url.absoluteString }
}

struct BriefSafariView: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> SFSafariViewController {
        SFSafariViewController(url: url)
    }

    func updateUIViewController(_ controller: SFSafariViewController, context: Context) {}
}
