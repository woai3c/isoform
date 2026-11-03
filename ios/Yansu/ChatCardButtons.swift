import Foundation

enum ChatCardButtonBehavior: Equatable, Sendable {
    case panel
    case answer
    case correct
    case place
    case calendar
    case activity
    case expand
    case play
    case file
}

struct ChatCardButton: Equatable, Sendable, Identifiable {
    var id: String
    var labelKey: String
    var labelValue: String?
    var behavior: ChatCardButtonBehavior
    var actionID: String?

    func label(locale: Locale = .current) -> String {
        let key = "mainChat.card.button.\(labelKey)"
        let template = Bundle.main.localizedString(forKey: key, value: key, table: nil)
        guard let labelValue else { return template }
        return String(format: template, locale: locale, arguments: [labelValue])
    }
}

enum ChatCardButtons {
    typealias Slots = (main: ChatCardButton?, second: ChatCardButton?)

    static func make(for item: ChatProactiveView, availableBriefIDs: Set<String> = [],
                     excludingPanelButtons: Bool = false) -> Slots {
        let actions = Set(item.actionIDs)
        func open(_ id: String, _ key: String) -> ChatCardButton? {
            actions.contains("open") ? button(id, key, .panel) : nil
        }
        func answer(_ action: String, _ key: String) -> ChatCardButton? {
            actions.contains(action) ? button(action, key, .answer, actionID: action) : nil
        }
        func calendar() -> ChatCardButton? {
            guard actions.contains("open"), eventStart(item) != nil else { return nil }
            return button("open_calendar", "openInCalendar", .calendar)
        }
        let slots: Slots
        switch item.kind {
        case "meeting_prep":
            slots = (open("open_prep", "openPrep"), calendar())
        case "calendar_conflict":
            slots = (open("show_both", "showBoth"), calendar())
        case "decision_due":
            if item.target.kind == "thread" {
                let hasPlace = !(item.place?.bundleID?.isEmpty ?? true)
                slots = (
                    hasPlace && actions.contains("open") ? button("take_me_there", "takeMeThere", .place) : open("show_thread", "showThread"),
                    hasPlace && actions.contains("open") ? button("show_thread", "showThread", .panel) : nil
                )
            } else {
                slots = (actions.contains("approve") || actions.contains("decline")
                         ? button("review", "review", .panel) : nil, nil)
            }
        case "waiting_on_reply":
            slots = (open("review", "review"), answer("not_now", "notNow"))
        case "work_finished", "playbook_result":
            slots = (actions.contains("review") ? button("review", "review", .answer, actionID: "review") : nil, nil)
        case "work_blocked":
            slots = (
                actions.contains("review") ? button("answer", "answer", .answer, actionID: "review") : nil,
                answer("stop", "stop")
            )
        case "commitment_slipping":
            slots = (open("find_time", "findTime"), answer("not_now", "notNow"))
        case "person_reappeared":
            slots = (actions.contains("open_person")
                     ? button("show_person", "showName", .panel, labelValue: item.personName ?? item.headline) : nil, nil)
        case "topic_finds":
            slots = (open("show_whats_new", "showWhatsNew"), nil)
        case "playbook_suggested":
            slots = (actions.contains("see") ? button("review", "review", .panel) : nil, answer("not_now", "notNow"))
        case "suggestion":
            slots = (answer("do_once", "doIt"), answer("not_now", "notNow"))
        case "brief_ready":
            slots = (
                item.briefID.map(availableBriefIDs.contains) == true ? button("play", "play", .play) : nil,
                open("read_it", "readIt").map { ChatCardButton(id: $0.id, labelKey: $0.labelKey, labelValue: nil, behavior: .expand, actionID: nil) }
            )
        case "weekly_recap":
            slots = (
                button("read_week", "readWeek", .expand),
                item.briefID.map(availableBriefIDs.contains) == true ? button("play", "play", .play) : nil
            )
        case "first_week":
            slots = (open("read_it", "readIt").map { ChatCardButton(id: $0.id, labelKey: $0.labelKey, labelValue: nil, behavior: .expand, actionID: nil) }, nil)
        case "brief_missed":
            slots = (answer("make_it_now", "makeItNow"), nil)
        case "schedule_missed":
            slots = (answer("run_now", "runItNow"), open("show_why", "showWhy"))
        case "learned_to_confirm":
            slots = (answer("keep", "looksRight"), nil)
        case "capture_paused":
            slots = (answer("resume_capture", "resume"), nil)
        case "reminder":
            slots = (answer("done", "done"), answer("not_now", "notNow"))
        case "goal_check_in":
            let visible = item.actionIDs.filter { $0 != "mute_kind" && $0 != "dismiss" }.prefix(2)
            let buttons = visible.map { button($0, "answer", .answer, actionID: $0) }
            slots = (buttons.first, buttons.dropFirst().first)
        default:
            slots = (open("review", "review"), nil)
        }
        let visibleMain = supported(slots.main, item: item, availableBriefIDs: availableBriefIDs)
        let visibleSecond = supported(slots.second, item: item, availableBriefIDs: availableBriefIDs)
        let visibleButtons = [visibleMain, visibleSecond].compactMap { $0 }
        let offeredButtons = excludingPanelButtons
            ? visibleButtons.filter { $0.behavior != .panel }
            : visibleButtons
        return (offeredButtons.first, offeredButtons.dropFirst().first)
    }

    static func eventStart(_ item: ChatProactiveView) -> String? {
        guard let start = item.event?.start ?? item.events.first?.start,
              RelayTime.parse(start) != nil else { return nil }
        return start
    }

    private static func button(_ id: String, _ key: String, _ behavior: ChatCardButtonBehavior,
                               labelValue: String? = nil, actionID: String? = nil) -> ChatCardButton {
        ChatCardButton(id: id, labelKey: key, labelValue: labelValue, behavior: behavior, actionID: actionID)
    }

    private static func supported(_ button: ChatCardButton?, item: ChatProactiveView,
                                  availableBriefIDs: Set<String>) -> ChatCardButton? {
        guard let button else { return nil }
        switch button.behavior {
        case .place, .correct, .activity, .file:
            return nil
        case .calendar:
            guard eventStart(item) != nil else { return nil }
        case .play:
            guard let briefID = item.briefID, availableBriefIDs.contains(briefID) else { return nil }
        case .panel, .answer, .expand:
            break
        }
        return button
    }
}
