import SwiftUI

/// A read-only index over briefs this phone already has permission to read. Files stay on the computer;
/// the brief's deliverable metadata is the only generated-work data this screen exposes.
struct LibraryView: View {
    @ObservedObject var library: LibraryStore
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var brief: BriefStore

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 24) {
                if pairing.isPaired {
                    Text("Briefs and deliverables saved on this phone.")
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.secondary)
                    if let notice = brief.historyNotice {
                        NoticeBanner(message: notice)
                    }
                    SectionLabel(title: "Past briefs")
                    if library.days.isEmpty {
                        EmptyStateCard(title: "No saved briefs yet.",
                                       detail: "Briefs appear here after your computer sends them to this phone.")
                    }
                    ForEach(library.days) { day in
                        VStack(alignment: .leading, spacing: 12) {
                            Text(day.entries.first?.entry.dayTitle ?? "Earlier")
                                .font(.headline)
                                .foregroundStyle(YansuTheme.ink)
                                .accessibilityAddTraits(.isHeader)
                            ForEach(day.entries) { brief in
                                NavigationLink {
                                    BriefDetailView(entry: brief.entry, player: environment.audioPlayer, readOnly: true)
                                } label: {
                                    YansuCard {
                                        VStack(alignment: .leading, spacing: 6) {
                                            Text(brief.entry.titleText)
                                                .font(.subheadline.weight(.semibold))
                                                .foregroundStyle(YansuTheme.ink)
                                            Text(brief.summary)
                                                .font(.body)
                                                .foregroundStyle(YansuTheme.ink)
                                                .lineLimit(3)
                                                .accessibilityIdentifier("library.summary")
                                            Text(brief.entry.detailLine(namingTheBrief: false))
                                                .font(.footnote)
                                                .foregroundStyle(YansuTheme.secondary)
                                        }
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                    }
                                }
                                .buttonStyle(.plain)
                                .accessibilityLabel("\(brief.entry.dayTitle), \(brief.entry.titleText). \(brief.summary) \(brief.entry.detailLine(namingTheBrief: false))")
                                .accessibilityHint("Opens the brief")
                                .accessibilityIdentifier("library.brief")
                            }
                        }
                    }
                    SectionLabel(title: "Deliverables and files",
                                 detail: "Deliverables mentioned in saved briefs. Open files on your computer.")
                    if library.artifacts.isEmpty {
                        EmptyStateCard(title: "Nothing created yet.",
                                       detail: "Deliverables appear here when Isoform includes them in a brief.")
                    }
                    ForEach(library.artifacts) { record in
                        LibraryRecordCard(record: record)
                            .accessibilityIdentifier("library.artifact")
                    }
                    if brief.historyLoading {
                        ProgressView("Updating saved briefs")
                    }
                } else {
                    EmptyStateCard(title: "Your Library starts with a paired computer.",
                                   detail: "Open Settings in the sidebar to pair this phone.")
                }
            }
            .padding(YansuTheme.gutter)
            .padding(.bottom, 16)
        }
        .yansuScreen()
        .navigationTitle("Library")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(.hidden, for: .tabBar)
        .task { await refresh() }
        .refreshable { await refresh() }
    }

    private func refresh() async {
        guard environment.refreshesBriefs else { return }
        await brief.refreshHistory()
    }
}

struct WorkHistoryView: View {
    @ObservedObject var library: LibraryStore
    @EnvironmentObject private var pairing: PairingStore
    @EnvironmentObject private var environment: AppEnvironment
    @EnvironmentObject private var brief: BriefStore

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                if pairing.isPaired {
                    SectionLabel(title: "One-time work", detail: "The latest updates in briefs saved on this phone.")
                    if library.work.isEmpty {
                        EmptyStateCard(title: "No recent work here yet.",
                                       detail: "One-time work appears after your computer includes an update in a brief.")
                    }
                    ForEach(library.work) { record in
                        LibraryRecordCard(record: record)
                            .accessibilityIdentifier("history.record")
                    }
                    SectionLabel(title: "Decisions", detail: "Answered and expired requests from recent chat history.")
                    if let notice = library.decisionNotice {
                        NoticeBanner(message: notice)
                    }
                    if library.decisionsLoading {
                        ProgressView("Reading recent decisions")
                    } else if library.decisions.isEmpty && library.decisionNotice == nil {
                        EmptyStateCard(title: "No recent decisions here yet.",
                                       detail: "Requests waiting for your answer stay in Chat.")
                    }
                    ForEach(library.decisions) { record in
                        LibraryRecordCard(record: record)
                            .accessibilityIdentifier("history.record")
                    }
                } else {
                    EmptyStateCard(title: "No history on this phone yet.",
                                   detail: "Open Settings in the sidebar to pair with your computer.")
                }
            }
            .padding(YansuTheme.gutter)
            .padding(.bottom, 16)
        }
        .yansuScreen()
        .navigationTitle("History")
        .navigationBarTitleDisplayMode(.inline)
        .task { await refresh() }
        .onDisappear { library.cancelDecisionRefresh() }
        .refreshable { await refresh() }
    }

    private func refresh() async {
        // Decision reads are independent of brief refreshes and use the same encrypted chat read routes.
        async let decisions: Void = library.refreshDecisions()
        if environment.refreshesBriefs { await brief.refreshHistory() }
        await decisions
    }
}

private struct LibraryRecordCard: View {
    let record: LibraryRecord

    var body: some View {
        YansuCard {
            VStack(alignment: .leading, spacing: 8) {
                Text(record.title)
                    .font(.headline)
                    .foregroundStyle(YansuTheme.ink)
                if !record.detail.isEmpty {
                    Text(record.detail)
                        .font(.body)
                        .foregroundStyle(YansuTheme.secondary)
                }
                if let status = record.status {
                    Text(BriefContentView.humanize(status))
                        .font(.subheadline.weight(.medium))
                        .foregroundStyle(YansuTheme.secondary)
                }
                if let date = record.date {
                    Text(date.formatted(date: .abbreviated, time: .shortened))
                        .font(.footnote)
                        .foregroundStyle(YansuTheme.secondary)
                }
            }
            .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
    }
}
