import SwiftUI

/// The hold card on the phone (calendar holds §3), the same parts as the computer's: the block dashed, the
/// question, the why, the where, and Hold in one press with "Held · Undo".
struct HoldSheetView: View {
    let offer: PhoneHoldOffer
    @ObservedObject var holds: HoldsStore
    var onClose: () -> Void

    @Environment(\.locale) private var locale
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var slot: PhoneHoldSlot
    @State private var calendarID: String
    @State private var otherTimes = false
    @State private var calendarFailed = false

    init(offer: PhoneHoldOffer, holds: HoldsStore, onClose: @escaping () -> Void) {
        self.offer = offer
        self.holds = holds
        self.onClose = onClose
        let replaced = holds.states[offer.proposal.id].flatMap { status in
            status.state == "conflict" ? status.start.flatMap { start in status.end.map { PhoneHoldSlot(start: start, end: $0) } } : nil
        }
        _slot = State(initialValue: replaced ?? offer.proposal.slots.first ?? PhoneHoldSlot(start: "", end: ""))
        _calendarID = State(initialValue: offer.proposal.calendarId)
    }

    private var id: String { offer.proposal.id }
    private var status: PhoneHoldStatus? { holds.states[id] }
    private var checking: Bool { holds.checking.contains(id) || status?.state == "checking" }
    private var calendar: PhoneHoldCalendar? { offer.calendars.first { $0.id == calendarID } }
    private var fallback: PhoneHoldCalendar? {
        guard status?.state == "calendar_unavailable", let other = status?.calendarId else { return nil }
        return offer.calendars.first { $0.id == other }
    }
    private var asking: Bool { status == nil || ["conflict", "calendar_unavailable"].contains(status?.state ?? "") }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    block
                    Text(question)
                        .font(.title3.weight(.semibold))
                        .foregroundStyle(YansuTheme.ink)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("holds.sheet.question")
                    if asking, let why = offer.why, !why.isEmpty {
                        Text(why)
                            .font(.subheadline)
                            .foregroundStyle(YansuTheme.secondary)
                            .accessibilityIdentifier("holds.sheet.why")
                    }
                    if status?.state != "removed", let calendar { whereLine(calendar) }
                    stateLine
                    if asking && !checking { buttons }
                    if otherTimes && asking && !checking { chips }
                }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .background(YansuTheme.background.ignoresSafeArea())
            .navigationTitle(String(localized: "hold.findTime"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(String(localized: "Done")) { onClose() }
                        .accessibilityIdentifier("holds.sheet.done")
                }
            }
        }
        .presentationDetents([.medium, .large])
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("holds.sheet")
        .onChange(of: status) { _, status in
            if status?.state == "conflict", let start = status?.start, let end = status?.end {
                slot = PhoneHoldSlot(start: start, end: end)
            }
        }
    }

    private func when(_ slot: PhoneHoldSlot) -> String { PhoneHoldCopy.when(slot, locale: locale) ?? "" }

    private func format(_ key: String.LocalizationValue, _ arguments: [CVarArg]) -> String {
        String(format: String(localized: key, locale: locale), locale: locale, arguments: arguments)
    }

    private var held: Bool { ["pending_write", "held", "moved"].contains(status?.state ?? "") }

    /// The slice of the day: the block dashed while it is a question, solid once it is held.
    private var block: some View {
        HStack(spacing: 10) {
            Text(held ? String(localized: "hold.held") : String(localized: "hold.dashed"))
                .font(.footnote.weight(.semibold))
            Text(when(slot)).font(.footnote)
            Spacer()
        }
        .foregroundStyle(held ? YansuTheme.onSolid : YansuTheme.accent)
        .padding(.horizontal, 14)
        .padding(.vertical, 18)
        .background(RoundedRectangle(cornerRadius: 12).fill(held ? YansuTheme.solid : YansuTheme.accentWash))
        .overlay {
            if !held {
                RoundedRectangle(cornerRadius: 12)
                    .strokeBorder(YansuTheme.accent, style: StrokeStyle(lineWidth: 1.5, dash: [5, 4]))
            }
        }
        .animation(reduceMotion ? nil : .easeOut(duration: 0.2), value: held)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(held ? "holds.sheet.block.held" : "holds.sheet.block")
    }

    private var question: String {
        switch status?.state {
        case "held", "moved": return format("On your calendar: %@.", [when(slot)])
        case "removed": return String(localized: "hold.removed")
        case HoldsStore.queuedState: return String(localized: "hold.away")
        case HoldsStore.expiredState: return String(localized: "hold.passed")
        case "conflict":
            if let event = status?.conflictTitle, let start = status?.conflictStart.flatMap(RelayTime.parse) {
                return format("hold.filledUp", [event, start.formatted(Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)), when(slot)])
            }
            return format("hold.filledUpPlain", [when(slot)])
        case "calendar_unavailable":
            return format("hold.readOnly", [calendar?.title ?? "", fallback?.title ?? ""])
        default: return format("hold.question", [when(slot), offer.proposal.thing.title])
        }
    }

    private func whereLine(_ calendar: PhoneHoldCalendar) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 4) {
            Text(format("hold.onCalendar", [calendar.title]) + (calendar.meetingsAccount == true ? " " + String(localized: "hold.sharedBusy") : ""))
            if asking && offer.calendars.count > 1 {
                Menu {
                    ForEach(offer.calendars) { option in
                        Button(option.title) { change(to: option) }
                    }
                } label: { Text(String(localized: "hold.change")).foregroundStyle(YansuTheme.accent) }
                    .accessibilityIdentifier("holds.sheet.change")
            }
        }
        .font(.footnote)
        .foregroundStyle(YansuTheme.secondary)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("holds.sheet.where")
    }

    @ViewBuilder
    private var stateLine: some View {
        if checking {
            HStack(spacing: 8) {
                TaskProgressRing(progress: nil, reduceMotion: reduceMotion).frame(width: 18, height: 18)
                Text(String(localized: "hold.checking"))
            }
            .font(.footnote)
            .foregroundStyle(YansuTheme.accent)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("holds.sheet.checking")
        } else if let status {
            switch status.state {
            case "pending_write", HoldsStore.queuedState:
                HStack(spacing: 6) {
                    Text(status.state == "pending_write" ? String(localized: "hold.held") : String(localized: "hold.away"))
                    Text("·")
                    Button(String(localized: "Undo")) { Task { _ = await holds.undo(id: id) } }
                        .fontWeight(.semibold)
                        .disabled(!status.canUndoHold)
                        .accessibilityIdentifier("holds.sheet.undo")
                }
                .font(.footnote)
                .foregroundStyle(YansuTheme.accent)
                .accessibilityIdentifier(status.state == "pending_write" ? "holds.sheet.heldUndo" : "holds.sheet.away")
            case "held", "moved":
                Button(String(localized: "hold.remove")) { Task { _ = await holds.remove(id: id) } }
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(YansuTheme.accent)
                    .accessibilityIdentifier("holds.sheet.remove")
                failureLine
            case "removed":
                Button(String(localized: "Undo")) { Task { _ = await holds.undoRemove(id: id) } }
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(YansuTheme.accent)
                    .accessibilityIdentifier("holds.sheet.undoRemove")
                failureLine
            case HoldsStore.expiredState:
                Button(String(localized: "hold.otherTimes")) {
                    holds.clear(id: id)
                    otherTimes = true
                }
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(YansuTheme.accent)
                .accessibilityIdentifier("holds.sheet.passed")
            default:
                EmptyView()
            }
        } else {
            failureLine
        }
        if calendarFailed {
            Text(String(localized: "hold.calendarFailed"))
                .font(.footnote)
                .foregroundStyle(YansuTheme.warning)
        }
    }

    @ViewBuilder
    private var failureLine: some View {
        if let failure = holds.failures[id], let line = PhoneHoldCopy.failure(failure) {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(line)
                if failure.retries {
                    Button(String(localized: "hold.tryAgain")) { retry(failure) }
                        .fontWeight(.semibold)
                        .accessibilityIdentifier("holds.sheet.retry")
                }
            }
            .font(.footnote)
            .foregroundStyle(failure == .calendarAccessOff ? YansuTheme.secondary : YansuTheme.warning)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("holds.sheet.failure")
        }
    }

    private var buttons: some View {
        HStack(spacing: 10) {
            Button {
                hold()
            } label: {
                Text(fallback.map { format("hold.holdOn", [$0.title]) } ?? format("hold.holdWhen", [when(slot)]))
                    .font(.subheadline.weight(.semibold))
                    .padding(.horizontal, 16).padding(.vertical, 10)
                    .background(YansuTheme.solid, in: Capsule())
                    .foregroundStyle(YansuTheme.onSolid)
            }
            .accessibilityIdentifier("holds.sheet.hold")
            if fallback == nil && offer.proposal.slots.count > 1 {
                Button(String(localized: "hold.otherTimes")) { otherTimes.toggle() }
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(YansuTheme.accent)
                    .accessibilityIdentifier("holds.sheet.otherTimes")
            }
            Button(String(localized: "Not now")) { onClose() }
                .font(.subheadline)
                .foregroundStyle(YansuTheme.secondary)
                .accessibilityIdentifier("holds.sheet.notNow")
        }
    }

    private var chips: some View {
        ViewThatFits {
            HStack(spacing: 8) { chipList }
            VStack(alignment: .leading, spacing: 8) { chipList }
        }
    }

    private var chipList: some View {
        ForEach(Array(offer.proposal.slots.filter { $0 != slot }.prefix(3)), id: \.self) { option in
            Button(when(option)) {
                slot = option
                otherTimes = false
                if status != nil { holds.clear(id: id) }
            }
            .font(.footnote)
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(YansuTheme.chip, in: Capsule())
            .overlay(Capsule().strokeBorder(YansuTheme.chipBorder))
            .foregroundStyle(YansuTheme.ink)
            .accessibilityIdentifier("holds.sheet.slot")
        }
    }

    private func hold() {
        if let fallback { calendarID = fallback.id }
        let calendarID = calendarID
        Task { _ = await holds.place(offer, slot: slot, calendarID: calendarID) }
    }

    private func retry(_ failure: PhoneHoldFailure) {
        switch failure {
        case .hold: hold()
        case .remove: Task { _ = await holds.remove(id: id) }
        case .restore: Task { _ = await holds.undoRemove(id: id) }
        case .calendarAccessOff: break
        }
    }

    private func change(to option: PhoneHoldCalendar) {
        guard option.id != calendarID else { return }
        Task {
            calendarFailed = false
            if await holds.selectCalendar(offer, calendarID: option.id) {
                calendarID = option.id
                if status != nil { holds.clear(id: id) }
            } else {
                calendarFailed = true
            }
        }
    }
}
