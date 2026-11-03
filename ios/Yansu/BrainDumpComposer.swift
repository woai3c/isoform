import SwiftUI

struct BrainDumpItem: Codable, Identifiable, Equatable, Sendable {
    let id: String
    let title: String
    let who: String
    var when: String? = nil
    var project: String? = nil
    var person: String? = nil
    var noRunner: Bool? = nil
}

struct BrainDumpParseRequest: Encodable { let idempotencyKey: String; let text: String }
struct BrainDumpParseResult: Decodable { let items: [BrainDumpItem] }
struct BrainDumpAddRequest: Encodable { let idempotencyKey: String; let items: [BrainDumpItem] }
struct BrainDumpAddResult: Decodable { let ids: [String] }

struct BrainDumpComposer: View {
    @ObservedObject var tasks: TasksStore
    @Environment(\.dismiss) private var dismiss
    @Environment(\.locale) private var locale
    @State private var text = ""
    @State private var items: [BrainDumpItem]?
    @State private var checked: Set<String> = []
    @State private var busy = false
    @State private var failed = false
    @State private var parseKey = UUID().uuidString
    @State private var addKey = UUID().uuidString
    @State private var addAttempted = false
    @FocusState private var textFocused: Bool

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    Color.clear.frame(height: 0).id("brainDump.top")
                    if failed {
                        Text(String(localized: "Couldn't reach your computer"))
                            .font(.footnote).foregroundStyle(YansuTheme.error)
                    }
                    if let items {
                        Text(String(localized: "Check these. Nothing starts until you add them."))
                            .foregroundStyle(YansuTheme.secondary)
                        VStack(spacing: 0) {
                            ForEach(items) { item in
                                Button {
                                    if checked.contains(item.id) { checked.remove(item.id) } else { checked.insert(item.id) }
                                } label: {
                                    HStack(alignment: .top, spacing: 14) {
                                        Image(systemName: checked.contains(item.id) ? "checkmark.square.fill" : "square")
                                            .font(.title3).foregroundStyle(YansuTheme.accent)
                                        VStack(alignment: .leading, spacing: 4) {
                                            Text(item.title).font(.body.weight(.semibold)).foregroundStyle(YansuTheme.ink)
                                            Text(who(item)).font(.subheadline).foregroundStyle(item.who == "reminder" ? YansuTheme.secondary : YansuTheme.accent)
                                                .fixedSize(horizontal: false, vertical: true)
                                        }
                                        Spacer(minLength: 0)
                                    }.padding(14).contentShape(Rectangle())
                                }
                                .buttonStyle(.plain)
                                .disabled(busy || addAttempted)
                                .accessibilityAddTraits(checked.contains(item.id) ? .isSelected : [])
                                .accessibilityIdentifier("brainDump.item.\(item.id)")
                                if item.id != items.last?.id { Divider().padding(.leading, 48) }
                            }
                        }
                        .background(YansuTheme.surface, in: RoundedRectangle(cornerRadius: YansuTheme.cornerRadius))
                        .overlay { RoundedRectangle(cornerRadius: YansuTheme.cornerRadius).strokeBorder(YansuTheme.border) }
                        HStack {
                            Button(String(localized: "Edit the list")) { self.items = nil; failed = false; parseKey = UUID().uuidString }
                                .buttonStyle(BrainDumpButtonStyle(prominent: false)).disabled(busy || addAttempted)
                            Spacer()
                            Button(addTitle) { Task { await add(items) } }
                                .buttonStyle(BrainDumpButtonStyle(prominent: true)).disabled(checked.isEmpty || busy)
                                .accessibilityIdentifier("brainDump.add")
                        }
                    } else {
                        Text(String(localized: "Type or say everything on your mind, one thing per line. Isoform turns it into tasks for you to check."))
                            .foregroundStyle(YansuTheme.secondary).fixedSize(horizontal: false, vertical: true)
                        ZStack(alignment: .topLeading) {
                            if text.isEmpty {
                                Text(String(localized: "Reply to Lena about the appendix…"))
                                    .foregroundStyle(YansuTheme.secondary).padding(.horizontal, 14).padding(.vertical, 14)
                            }
                            TextEditor(text: $text).scrollContentBackground(.hidden)
                                .focused($textFocused)
                                .padding(8).frame(minHeight: 180)
                                .accessibilityLabel(String(localized: "Your list"))
                                .accessibilityIdentifier("brainDump.text")
                        }
                        .background(YansuTheme.surfaceWarm, in: RoundedRectangle(cornerRadius: YansuTheme.cornerRadius))
                        .overlay { RoundedRectangle(cornerRadius: YansuTheme.cornerRadius).strokeBorder(YansuTheme.border) }
                        HStack {
                            HoldToTalk(text: $text, showsLabel: true)
                            Spacer()
                            Button(busy ? String(localized: "Making tasks…") : String(localized: "Make tasks")) { Task { await parse() } }
                                .buttonStyle(BrainDumpButtonStyle(prominent: true))
                                .disabled(busy || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                                .accessibilityIdentifier("brainDump.make")
                        }
                    }
                }.padding(YansuTheme.gutter)
            }
            .onChange(of: textFocused) { _, focused in
                if !focused { withAnimation { proxy.scrollTo("brainDump.top", anchor: .top) } }
            }
            }
            .background(YansuTheme.surface)
            .navigationTitle(String(localized: "Hand Isoform anything"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button(String(localized: "Cancel")) { dismiss() } }
                ToolbarItemGroup(placement: .keyboard) {
                    Spacer()
                    Button(String(localized: "Done")) { textFocused = false }
                }
            }
        }
        .presentationDetents([.large])
        .onChange(of: text) { _, _ in parseKey = UUID().uuidString }
    }

    private var addTitle: String {
        checked.count == 1 ? String(localized: "Add 1 to Tasks") : String(format: String(localized: "Add %lld to Tasks"), Int64(checked.count))
    }

    private func who(_ item: BrainDumpItem) -> String {
        switch item.who {
        case "draft": return String(localized: "Isoform drafts it. You send it.")
        case "claude_code": return String(localized: "Claude Code on your computer does it. You review it.")
        case "codex": return String(localized: "Codex on your computer does it. You review it.")
        default:
            if item.noRunner == true { return String(localized: "No AI is set up on your computer, so this is a reminder.") }
            guard let raw = item.when, let date = RelayTime.parse(raw) else { return String(localized: "A reminder for you") }
            return String(format: String(localized: "A reminder for you · %@"), TaskThreadCopy.upcoming(date, locale: locale))
        }
    }

    private func parse() async {
        busy = true; failed = false
        defer { busy = false }
        do {
            let result = try await tasks.parseBrainDump(text, key: parseKey)
            items = result; checked = Set(result.map(\.id)); addKey = UUID().uuidString; addAttempted = false
        } catch { failed = true }
    }

    private func add(_ items: [BrainDumpItem]) async {
        busy = true; failed = false; addAttempted = true
        defer { busy = false }
        do { try await tasks.addBrainDump(items.filter { checked.contains($0.id) }, key: addKey); dismiss() }
        catch { failed = true }
    }
}

private struct BrainDumpButtonStyle: ButtonStyle {
    let prominent: Bool
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.subheadline.weight(.semibold))
            .padding(.horizontal, 18).frame(minHeight: 44)
            .foregroundStyle(prominent ? YansuTheme.onSolid : YansuTheme.secondary)
            .background(prominent ? YansuTheme.solid : YansuTheme.surface, in: Capsule())
            .overlay { if !prominent { Capsule().strokeBorder(YansuTheme.border) } }
            .opacity(!isEnabled ? 0.45 : configuration.isPressed ? 0.7 : 1)
    }
}
