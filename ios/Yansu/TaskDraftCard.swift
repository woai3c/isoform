import SwiftUI

/// A prepared reply in a task's thread (Phone Threads §4): why this draft, who gets it, whose voice, and Send on the
/// card. Its fields come from the computer's structured draft, never from the model's prose.
struct MobileTaskDraft: Codable, Equatable, Hashable, Sendable {
    /// mail or slack.
    let channel: String
    var why: IdeaEvidence? = nil
    let to: String
    var subject: String? = nil
    let body: String
    var attachment: MobileTaskAttachment? = nil
    var recipients: MobileTaskRecipients? = nil
    var voice: MobileTaskVoice? = nil
    /// ready, sent or not_sent.
    let state: String
    var sentAt: String? = nil
    var revision: String? = nil
}

struct MobileTaskAttachment: Codable, Equatable, Hashable, Sendable {
    let name: String
    var size: Int? = nil
}

/// Who gets it, and who is on the thread but not copied.
struct MobileTaskRecipients: Codable, Equatable, Hashable, Sendable {
    let to: [String]
    var cc: [String]? = nil
    var notIncluded: [String]? = nil
}

/// Present only with three or more of your own messages to that person.
struct MobileTaskVoice: Codable, Equatable, Hashable, Sendable {
    let sampleCount: Int
    let person: String
    /// short, no_greeting, greeting, long.
    let traits: [String]
}

enum TaskDraftCopy {
    static func header(_ draft: MobileTaskDraft) -> String {
        draft.channel == "slack" ? String(localized: "draft.header.slack") : String(localized: "draft.header.mail")
    }

    /// "Only to Priya. Dan is on the thread and isn't copied." or "To Priya, copying Dan."
    static func who(_ draft: MobileTaskDraft, locale: Locale) -> String? {
        guard let recipients = draft.recipients, !recipients.to.isEmpty else { return nil }
        let to = list(recipients.to, locale: locale)
        if let cc = recipients.cc, !cc.isEmpty {
            return String.localizedStringWithFormat(String(localized: "To %1$@, copying %2$@."), to, list(cc, locale: locale))
        }
        let others = recipients.notIncluded ?? []
        switch others.count {
        case 0:
            return String.localizedStringWithFormat(String(localized: "Only to %@."), to)
        case 1:
            return String.localizedStringWithFormat(String(localized: "Only to %1$@. %2$@ is on the thread and isn't copied."),
                                                    to, others[0])
        default:
            return String.localizedStringWithFormat(String(localized: "Only to %1$@. %2$@ are on the thread and aren't copied."),
                                                    to, list(others, locale: locale))
        }
    }

    /// "Written like your last 6 replies to Priya: short, no greeting." Shown only with three or more samples.
    static func voice(_ draft: MobileTaskDraft, locale: Locale) -> String? {
        guard let voice = draft.voice, voice.sampleCount >= 3 else { return nil }
        let traits = voice.traits.compactMap(trait)
        guard !traits.isEmpty else { return nil }
        let separator = ChatPresentationLanguage(locale: locale).code == "zh-CN" ? "，" : ", "
        return String.localizedStringWithFormat(String(localized: "Written like your last %1$d replies to %2$@: %3$@."),
                                                voice.sampleCount, voice.person, traits.joined(separator: separator))
    }

    static func trait(_ code: String) -> String? {
        switch code {
        case "short": return String(localized: "voice.short")
        case "long": return String(localized: "voice.long")
        case "no_greeting": return String(localized: "voice.noGreeting")
        case "greeting": return String(localized: "voice.greeting")
        default: return nil
        }
    }

    static func size(_ bytes: Int?) -> String? {
        guard let bytes, bytes > 0 else { return nil }
        return ByteCountFormatter.string(fromByteCount: Int64(bytes), countStyle: .file)
    }

    private static func list(_ names: [String], locale: Locale) -> String {
        names.formatted(.list(type: .and).locale(locale))
    }
}

/// The draft card. Send folds into a ring, then "Sent to Priya · 10:12 AM · Undo" for five seconds while nothing has
/// gone yet; after that the computer sends it.
struct TaskDraftCard: View {
    let draft: MobileTaskDraft
    /// The person the reply goes to, by first name, for "Sent to Priya".
    let person: String
    /// The send in its five seconds of Undo: when it was tapped.
    let pendingSince: Date?
    let failed: Bool
    let canSend: Bool
    let send: (_ body: String?) -> Void
    let undo: () -> Void
    let notNow: (() -> Void)?
    var failedText: String? = nil

    @Environment(\.locale) private var locale
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var editing = false
    @State private var edited = ""
    @FocusState private var editorFocused: Bool

    var body: some View {
        Group {
            if let pendingSince {
                sentLine(at: pendingSince, undoable: true)
            } else if draft.state == "sent" {
                sentLine(at: draft.sentAt.flatMap(RelayTime.parse) ?? Date(), undoable: false)
            } else if draft.state == "not_sent" {
                notSentLine
            } else {
                card
            }
        }
        .animation(reduceMotion ? .easeOut(duration: 0.2) : .spring(duration: 0.4), value: pendingSince)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("tasks.thread.draft")
        .onChange(of: failed, initial: true) { _, didFail in
            if didFail, let failedText {
                edited = failedText
                editing = true
            }
        }
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(TaskDraftCopy.header(draft))
                    .font(.caption.weight(.semibold))
                    .tracking(0.8)
                    .textCase(.uppercase)
                    .foregroundStyle(YansuTheme.secondary)
                Spacer()
                if let notNow {
                    Menu {
                        Button(String(localized: "upnext.notNow"), action: notNow)
                    } label: {
                        Image(systemName: "ellipsis")
                            .font(.headline)
                            .foregroundStyle(YansuTheme.secondary)
                            .frame(width: 32, height: 32)
                            .contentShape(Circle())
                    }
                    .accessibilityLabel(String(localized: "More"))
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 12)
            .padding(.bottom, 8)

            if let why = draft.why {
                VStack(alignment: .leading, spacing: 6) {
                    Text(String(localized: "Why this draft"))
                        .font(.caption.weight(.semibold))
                        .tracking(0.8)
                        .textCase(.uppercase)
                        .foregroundStyle(YansuTheme.secondary)
                    Text(why.text(locale: locale))
                        .font(.body)
                        .foregroundStyle(YansuTheme.ink)
                        .fixedSize(horizontal: false, vertical: true)
                    let source = TaskThreadCopy.sourceLine(why, locale: locale)
                    HStack(spacing: 7) {
                        Image(systemName: TaskThreadCopy.sourceIcon(why.source)).font(.footnote)
                        Text(source)
                    }
                    .font(.subheadline)
                    .foregroundStyle(YansuTheme.secondary)
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(source)
                }
                .padding(13)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(YansuTheme.surfaceWarm, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                .padding(.horizontal, 12)
                .padding(.bottom, 4)
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("tasks.draft.why")
            }

            field(String(localized: "To"), draft.to)
            if let subject = draft.subject, !subject.isEmpty {
                Divider().overlay(YansuTheme.border)
                field(String(localized: "Subject"), subject)
            }
            Divider().overlay(YansuTheme.border)

            VStack(alignment: .leading, spacing: 14) {
                if editing {
                    TextEditor(text: $edited)
                        .font(.body)
                        .frame(minHeight: 160)
                        .scrollContentBackground(.hidden)
                        .focused($editorFocused)
                        .accessibilityIdentifier("tasks.draft.editor")
                } else {
                    Text(draft.body)
                        .font(.body)
                        .foregroundStyle(YansuTheme.ink)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                        .accessibilityIdentifier("tasks.draft.body")
                }
                if let attachment = draft.attachment {
                    HStack(spacing: 10) {
                        Image(systemName: "paperclip").foregroundStyle(YansuTheme.secondary)
                        Text(attachment.name).foregroundStyle(YansuTheme.ink).lineLimit(1)
                        Spacer()
                        if let size = TaskDraftCopy.size(attachment.size) {
                            Text(size).foregroundStyle(YansuTheme.secondary)
                        }
                    }
                    .font(.subheadline)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 11)
                    .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).stroke(YansuTheme.border, lineWidth: 1))
                }
                if let who = TaskDraftCopy.who(draft, locale: locale) {
                    note("person", who).accessibilityIdentifier("tasks.draft.who")
                }
                if let voice = TaskDraftCopy.voice(draft, locale: locale) {
                    note("pencil", voice).accessibilityIdentifier("tasks.draft.voice")
                }
                if failed {
                    Text(String(localized: "Didn't send."))
                        .font(.subheadline)
                        .foregroundStyle(YansuTheme.warning)
                        .accessibilityIdentifier("tasks.draft.failed")
                }
                HStack(spacing: 10) {
                    Button(failed ? String(localized: "Try again") : String(localized: "Send")) {
                        send(editing ? edited : nil)
                        editing = false
                    }
                    .buttonStyle(DraftButtonStyle(prominent: true))
                    .disabled(!canSend || (editing && edited.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty))
                    .accessibilityIdentifier("tasks.draft.send")
                    Button(editing ? String(localized: "Cancel") : String(localized: "Edit")) {
                        if editing {
                            editing = false
                        } else {
                            edited = draft.body
                            editing = true
                            editorFocused = true
                        }
                    }
                    .buttonStyle(DraftButtonStyle(prominent: false))
                    .accessibilityIdentifier("tasks.draft.edit")
                }
                .padding(.top, 2)
            }
            .padding(16)
        }
        .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 22, style: .continuous).strokeBorder(YansuTheme.border, lineWidth: 1))
    }

    private func field(_ label: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 0) {
            Text(label)
                .foregroundStyle(YansuTheme.secondary)
                .frame(width: 78, alignment: .leading)
            Text(value)
                .foregroundStyle(YansuTheme.ink)
                .lineLimit(2)
            Spacer(minLength: 0)
        }
        .font(.body)
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .accessibilityElement(children: .combine)
    }

    private func note(_ icon: String, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Image(systemName: icon)
                .font(.subheadline)
                .foregroundStyle(YansuTheme.accent)
                .accessibilityHidden(true)
            Text(text)
                .font(.subheadline)
                .foregroundStyle(YansuTheme.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func sentLine(at date: Date, undoable: Bool) -> some View {
        HStack(spacing: 10) {
            Image(systemName: "checkmark.circle.fill")
                .foregroundStyle(YansuTheme.onSolid, YansuTheme.solid)
            (Text(String.localizedStringWithFormat(String(localized: "Sent to %@"), person))
                .foregroundStyle(YansuTheme.accent).fontWeight(.medium)
             + Text(" · " + TaskThreadCopy.clock(date, locale: locale)).foregroundStyle(YansuTheme.secondary))
            Spacer()
            if undoable {
                Button(String(localized: "Undo"), action: undo)
                    .fontWeight(.semibold)
                    .foregroundStyle(YansuTheme.accent)
                    .accessibilityIdentifier("tasks.draft.undo")
            }
        }
        .font(.body)
        .padding(.horizontal, 16)
        .padding(.vertical, 14)
        .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).strokeBorder(YansuTheme.border, lineWidth: 1))
        .transition(reduceMotion ? .opacity : .scale(scale: 0.96).combined(with: .opacity))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("tasks.draft.sent")
    }

    private var notSentLine: some View {
        HStack(spacing: 10) {
            Image(systemName: draft.channel == "slack" ? "bubble.left" : "envelope")
                .foregroundStyle(YansuTheme.secondary)
            Text(String.localizedStringWithFormat(String(localized: "Draft to %@ · not sent"), person))
                .foregroundStyle(YansuTheme.secondary)
            Spacer()
        }
        .font(.body)
        .padding(.horizontal, 16)
        .padding(.vertical, 14)
        .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).strokeBorder(YansuTheme.border, lineWidth: 1))
    }
}

private struct DraftButtonStyle: ButtonStyle {
    let prominent: Bool
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .padding(.horizontal, prominent ? 28 : 20)
            .frame(minHeight: 44)
            .foregroundStyle(prominent ? YansuTheme.onSolid : YansuTheme.ink)
            .background(prominent ? YansuTheme.solid : YansuTheme.chip, in: Capsule())
            .opacity(!isEnabled ? 0.45 : configuration.isPressed ? 0.75 : 1)
    }
}
