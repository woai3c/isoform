import SwiftUI

// Which briefs are on screen now. The mini player stays away while the brief it plays is already in view (design
// section 4): the brief view and, when the Today card becomes the player, the card say so here. Reading is narrower:
// only the brief view counts, so a card that shows a brief does not hold it back from a newer one (section 2).
@MainActor
final class BriefOnScreen: ObservableObject {
    @Published private(set) var counts: [String: Int] = [:]
    @Published private(set) var readingCounts: [String: Int] = [:]

    func isShowing(_ briefID: String) -> Bool {
        (counts[briefID] ?? 0) > 0
    }

    /// Whether the person is reading the brief in its own view.
    func isReading(_ briefID: String) -> Bool {
        (readingCounts[briefID] ?? 0) > 0
    }

    func show(_ briefID: String, reading: Bool = false) {
        counts[briefID, default: 0] += 1
        if reading { readingCounts[briefID, default: 0] += 1 }
    }

    func hide(_ briefID: String, reading: Bool = false) {
        if let count = counts[briefID] {
            counts[briefID] = count > 1 ? count - 1 : nil
        }
        if reading, let count = readingCounts[briefID] {
            readingCounts[briefID] = count > 1 ? count - 1 : nil
        }
    }
}

extension View {
    /// Marks the brief as on screen while this view is shown.
    func briefOnScreen(_ briefID: String) -> some View {
        modifier(BriefOnScreenModifier(briefID: briefID))
    }
}

private struct BriefOnScreenModifier: ViewModifier {
    let briefID: String
    @EnvironmentObject private var onScreen: BriefOnScreen

    func body(content: Content) -> some View {
        content
            .onAppear { onScreen.show(briefID) }
            .onDisappear { onScreen.hide(briefID) }
            .onChange(of: briefID) { old, new in
                onScreen.hide(old)
                onScreen.show(new)
            }
    }
}

/// A request to open a brief's full view over the current screen: from the mini player, Chat or the wide dock.
struct BriefRoute: Identifiable, Equatable {
    let id = UUID()
    let briefID: String
    /// Open at the paragraph being spoken, rather than at the top.
    let landsOnSpoken: Bool
}
