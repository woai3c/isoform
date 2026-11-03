import AppIntents
import SwiftUI
import WidgetKit

struct SnapshotEntry: TimelineEntry {
    let date: Date
    let snapshot: WidgetSnapshot?
}

struct SnapshotProvider: TimelineProvider {
    func placeholder(in context: Context) -> SnapshotEntry {
        SnapshotEntry(date: Date(), snapshot: WidgetSnapshot.empty(now: Date()))
    }
    func getSnapshot(in context: Context, completion: @escaping (SnapshotEntry) -> Void) {
        completion(SnapshotEntry(date: Date(), snapshot: WidgetSnapshotStore(fileURL: WidgetSnapshotStore.defaultURL).read()))
    }
    func getTimeline(in context: Context, completion: @escaping (Timeline<SnapshotEntry>) -> Void) {
        let now = Date()
        let snapshot = WidgetSnapshotStore(fileURL: WidgetSnapshotStore.defaultURL).read()
        let dates = WidgetSnapshot.timelineDates(now: now, event: snapshot?.nextEventAt)
        completion(Timeline(entries: dates.map { SnapshotEntry(date: $0, snapshot: snapshot) },
                            policy: .after(dates[dates.count - 1])))
    }
}

struct YansuSnapshotWidget: Widget {
    static let kind = "YansuSnapshot"
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: Self.kind, provider: SnapshotProvider()) { SnapshotWidgetView(entry: $0) }
            .configurationDisplayName("widget.gallery.name")
            .description("widget.gallery.description")
            .supportedFamilies([.systemSmall, .systemMedium, .accessoryRectangular])
    }
}

struct SnapshotWidgetView: View {
    @Environment(\.widgetFamily) private var systemFamily
    @Environment(\.locale) private var locale
    let entry: SnapshotEntry
    var previewFamily: WidgetFamily? = nil
    private var family: WidgetFamily { previewFamily ?? systemFamily }
    private var snapshot: WidgetSnapshot { entry.snapshot ?? .empty(now: entry.date) }
    private var ready: Bool { snapshot.showsBrief(at: entry.date) }

    var body: some View {
        Group {
            if family == .accessoryRectangular { lockScreen }
            else if family == .systemMedium { medium }
            else { small }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .containerBackground(WidgetTheme.background, for: .widget)
        .widgetURL(ready ? WidgetLink.brief(id: snapshot.briefID) : WidgetLink.settings)
    }

    private var small: some View {
        VStack(alignment: .leading, spacing: 6) {
            header
            if ready {
                lead
                Spacer(minLength: 0)
                HStack { count; Spacer(); play }
            } else { emptyState }
        }
    }

    private var medium: some View {
        HStack(alignment: .top, spacing: 16) {
            VStack(alignment: .leading, spacing: 6) {
                header
                if ready { lead; Spacer(minLength: 0); play }
                else { emptyState }
            }.frame(maxWidth: .infinity, alignment: .leading)
            if ready && !snapshot.isPrivate {
                VStack(alignment: .leading, spacing: 5) {
                    count
                    if let title = snapshot.firstItemTitle, let id = snapshot.firstItemID,
                       let url = WidgetLink.item(kind: snapshot.firstItemKind, id: id) {
                        Link(destination: url) {
                            VStack(alignment: .leading, spacing: 3) {
                                Text(verbatim: title).font(.caption.weight(.semibold)).lineLimit(2)
                                if let detail = snapshot.firstItemDetail { Text(verbatim: detail).font(.caption2).lineLimit(2) }
                            }.foregroundStyle(WidgetTheme.ink).privacySensitive()
                        }
                    }
                    Spacer(minLength: 0)
                    if let title = snapshot.nextEventTitle, let at = snapshot.nextEventAt, at > entry.date {
                        Link(destination: WidgetLink.day) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(verbatim: title).lineLimit(1)
                                Text(at, style: .time)
                            }.font(.caption2).foregroundStyle(WidgetTheme.secondary).privacySensitive()
                        }
                    }
                }.frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private var lockScreen: some View {
        VStack(alignment: .leading) {
            if ready, let at = snapshot.lastBriefAt { Text("widget.lock.brief \(at, style: .time)") }
            else { Text("widget.gallery.name") }
            if snapshot.needsYouCount > 0 { Text("widget.need \(snapshot.needsYouCount)") }
        }
    }

    private var header: some View {
        Group {
            if ready, let at = snapshot.lastBriefAt {
                if family == .systemMedium && snapshot.briefSlot == "morning" { Text("widget.morning.time \(at, style: .time)") }
                else { Text("widget.brief.time \(at, style: .time)") }
            }
            else { Text("widget.gallery.name") }
        }.font(.caption.weight(.semibold)).foregroundStyle(WidgetTheme.secondary)
    }

    private var lead: some View {
        Group {
            if snapshot.isPrivate {
                VStack(alignment: .leading) { Text("widget.private.ready"); Text("widget.private.read") }
            } else if let text = snapshot.lead {
                Text(verbatim: text).privacySensitive()
            }
        }.font(.subheadline.weight(.medium)).foregroundStyle(WidgetTheme.ink).lineLimit(4)
    }

    @ViewBuilder private var count: some View {
        if snapshot.needsYouCount > 0 {
            HStack(spacing: 4) {
                Circle().fill(WidgetTheme.warning).frame(width: 6, height: 6)
                Text("widget.need \(snapshot.needsYouCount)")
            }.font(.caption.weight(.semibold)).foregroundStyle(WidgetTheme.ink)
        }
    }

    private var play: some View {
        Button(intent: WidgetPlayIntent()) {
            Label(snapshot.isPlaying ? "widget.pause" : "widget.play", systemImage: snapshot.isPlaying ? "pause.fill" : "play.fill")
                .labelStyle(.iconOnly)
        }.buttonStyle(.plain).foregroundStyle(WidgetTheme.accent)
    }

    private var emptyState: some View {
        Group {
            if let time = WidgetSnapshot.morningTimeText(snapshot.morningTime, now: entry.date, locale: locale) { Text("widget.comes \(time)") }
            else { Text("widget.turnOn") }
        }.font(.subheadline).foregroundStyle(WidgetTheme.secondary).padding(.top, 8)
    }
}
