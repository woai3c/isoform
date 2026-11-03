import Foundation
import WidgetKit

// The mapping from the brief lives here, not in YansuShared, because the widget extension compiles
// WidgetSnapshot.swift and cannot see MobileBriefV1. Call sites read the same: WidgetSnapshot.fromBrief(brief, now:).
extension WidgetSnapshot {
    static func fromBrief(_ brief: MobileBriefV1, now: Date) -> WidgetSnapshot {
        WidgetSnapshot(generatedAt: now, hubName: brief.hubName.flatMap(nonBlank), lastBriefAt: brief.asOfDate)
    }

    fileprivate static func nonBlank(_ text: String) -> String? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }
}

/// Keeps the widget snapshot file in step with the latest brief. The widget is a convenience, so every write is
/// best effort and nothing here throws or touches the network.
@MainActor
final class WidgetSnapshotPublisher {
    private let store: WidgetSnapshotStore
    private let reloadTimelines: @MainActor () -> Void
    private let removeItem: (URL) throws -> Void
    private var removalPending = false

    init(store: WidgetSnapshotStore = WidgetSnapshotStore(fileURL: WidgetSnapshotStore.defaultURL),
         reloadTimelines: @escaping @MainActor () -> Void = { WidgetCenter.shared.reloadAllTimelines() },
         removeItem: @escaping (URL) throws -> Void = { try FileManager.default.removeItem(at: $0) }) {
        self.store = store
        self.reloadTimelines = reloadTimelines
        self.removeItem = removeItem
    }

    /// Writes the Mac's name and the brief's time. A brief without a name or a time keeps the stored one, so a
    /// sparse brief never blanks what the widget already shows.
    func updateBrief(_ brief: MobileBriefV1, now: Date = Date()) {
        guard !removalPending || clear() else { return }
        var next = WidgetSnapshot.fromBrief(brief, now: now)
        let stored = store.read()
        next.hubName = next.hubName ?? stored?.hubName
        next.lastBriefAt = next.lastBriefAt ?? stored?.lastBriefAt
        save(next, previous: stored)
    }

    /// Publish one privacy-filtered snapshot from the phone's retained copy.
    func updateToday(_ today: MobileTodaySnapshot?, brief: MobileBriefV1?, tasks: MobileTasksPage?,
                     privateContent: Bool, isPlaying: Bool, now: Date = Date()) {
        guard !removalPending || clear() else { return }
        var next = WidgetSnapshot(generatedAt: now, hubName: brief?.hubName, lastBriefAt: brief?.asOfDate)
        next.briefID = brief?.identifier
        next.briefSlot = brief?.slot
        next.morningTime = today?.schedule.morning
        next.needsYouCount = max(0, tasks?.waitingCount ?? tasks?.counts.needsYou ?? today?.needsYouCount ?? 0)
        next.isPrivate = privateContent
        next.isPlaying = isPlaying
        if !privateContent {
            next.lead = brief?.narrative.first?.text.flatMap(WidgetSnapshot.nonBlank)
            let legacyTasks = tasks.map { page in
                TaskGroups.make(from: page.tasks, threads: page.threads == true).needsYou.map {
                    TodayNeed(id: $0.id, chatId: nil, card: nil, task: $0)
                }
            }
            if let first = (tasks?.waitingItems ?? legacyTasks ?? today?.needsYou ?? []).first {
                if let task = first.task {
                    next.firstItemTitle = task.title
                    next.firstItemDetail = task.blockedReason
                    next.firstItemID = task.id
                    next.firstItemKind = "task"
                } else if let card = first.card?.proactive {
                    let copy = card.copy(language: ChatPresentationLanguage(locale: .current).code)
                    next.firstItemTitle = copy.headline
                    next.firstItemDetail = copy.body
                    next.firstItemID = card.momentID
                    next.firstItemKind = "chat"
                }
            }
            if let event = today?.day.next, let start = RelayTime.parse(event.start), start > now {
                next.nextEventTitle = event.title
                next.nextEventAt = start
            }
        }
        save(next, previous: store.read())
    }

    /// Removes the snapshot, so the widget falls back to "Open Isoform to read your brief."
    @discardableResult
    func clear() -> Bool {
        removalPending = true
        let existed = FileManager.default.fileExists(atPath: store.fileURL.path)
        do {
            if existed { try removeItem(store.fileURL) }
        } catch {
            return false
        }
        removalPending = false
        if existed {
            reloadTimelines()
        }
        return true
    }

    private func save(_ next: WidgetSnapshot, previous: WidgetSnapshot?) {
        do {
            try store.write(next)
        } catch {
            return
        }
        // Reload only when the widget would draw something different. A refresh with the same content is silent.
        if previous?.hasSameContent(as: next) != true {
            reloadTimelines()
        }
    }
}
