import CoreGraphics

/// Keeps an open chat on its newest message. The chat reports where its content sits after every layout; this
/// decides when to follow the bottom and when to leave someone reading further up where they are.
struct ChatScrollPolicy: Equatable {
    /// How close to the bottom still counts as reading the newest message.
    static let pinMargin: CGFloat = 48

    enum Action: Equatable {
        case none
        case scrollToBottom(animated: Bool)
    }

    /// Following the newest message. True until the reader scrolls away from the bottom.
    private(set) var pinned = true
    /// Newer rows arrived below while the reader was further up.
    private(set) var hasNewBelow = false
    private var lastContentTop: CGFloat?
    private var lastVisibleBottom: CGFloat?
    /// An animated scroll to the bottom is moving the content; its frames are not the reader scrolling away.
    private var followingDown = false
    private(set) var holdingFirstRead = false

    /// The chat came on screen, the first time or on returning to it: start at the newest message.
    mutating func appeared() -> Action {
        holdingFirstRead = false
        pinned = true
        hasNewBelow = false
        followingDown = true
        return .scrollToBottom(animated: false)
    }

    /// The reader sent a message or tapped Latest: show the bottom.
    mutating func jumpToLatest() -> Action {
        holdingFirstRead = false
        pinned = true
        hasNewBelow = false
        followingDown = true
        return .scrollToBottom(animated: true)
    }

    /// A newly arrived first read is one tall item. Start at its beginning,
    /// then let the reader move through it without layout following its end.
    mutating func readFirstRead() {
        holdingFirstRead = true
        pinned = false
        hasNewBelow = false
        followingDown = false
    }

    /// The keyboard finished rising. A reader at the bottom is put back at the newest message, now that the visible
    /// area has its final height (P161).
    mutating func keyboardRose() -> Action {
        guard pinned else { return .none }
        followingDown = true
        return .scrollToBottom(animated: false)
    }

    /// A real drag gives scrolling back to the reader.
    mutating func readerDragged() {
        holdingFirstRead = false
    }

    /// New rows or a new message were added at the bottom. A reader at the bottom is kept there by the layout
    /// that follows; one further up is told newer rows are waiting.
    mutating func newContentArrived() {
        if !pinned { hasNewBelow = true }
    }

    /// One layout of the chat, in screen coordinates: where the content's top sits, how tall it is, and where the
    /// visible area ends, above the composer.
    mutating func layout(contentTop: CGFloat, contentHeight: CGFloat, visibleBottom: CGFloat) -> Action {
        // Above zero, content continues below the visible area.
        let distance = contentTop + contentHeight - visibleBottom
        let last = lastContentTop
        lastContentTop = contentTop
        // The visible area changed, as when the keyboard rises or the composer grows. A reader at the bottom stays
        // there, even when the same layout moved the content: the move is the area changing, not the reader (P161).
        let areaChanged = lastVisibleBottom.map { abs($0 - visibleBottom) > 0.5 } ?? false
        lastVisibleBottom = visibleBottom
        if holdingFirstRead { return .none }
        if areaChanged, pinned, distance > 0.5 {
            followingDown = true
            return .scrollToBottom(animated: false)
        }
        guard let last else {
            return pinned && distance > 0.5 ? .scrollToBottom(animated: false) : .none
        }
        let moved = contentTop - last
        if abs(moved) > 0.5 {
            // The content moved: the reader scrolled, or a scroll to the bottom is on its way.
            if followingDown {
                if distance <= 0.5 {
                    followingDown = false
                } else if distance <= Self.pinMargin || moved < 0 {
                    // The bottom is still on its way, or stopped short of the newest message: keep following it.
                    // A scroll that stopped short inside the margin is not the reader's, and must not leave the
                    // newest message under the composer (P161).
                    return .scrollToBottom(animated: false)
                } else {
                    // The reader pulled back up while the chat was moving down.
                    followingDown = false
                    pinned = false
                }
                return .none
            }
            pinned = distance <= Self.pinMargin
            if pinned { hasNewBelow = false }
            return .none
        }
        // The content or the visible area changed size without a scroll: a new row, a longer reply, the keyboard.
        guard pinned, distance > 0.5 else { return .none }
        return .scrollToBottom(animated: false)
    }
}
