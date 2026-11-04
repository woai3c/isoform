import type { MainChatProactivePayload } from "@/lib/stream-helpers";

export type CardButtonBehavior = "panel" | "answer" | "correct" | "place" | "calendar" | "activity" | "target" | "expand" | "play" | "file";

export interface CardButton {
    id: string;
    labelKey: string;
    labelParams?: Record<string, string>;
    does: CardButtonBehavior;
    action?: string;
}

export interface MainChatCardButtons {
    main?: CardButton;
    second?: CardButton;
}

const label = (id: string, does: CardButtonBehavior, key: string, action?: string, labelParams?: Record<string, string>): CardButton => ({
    id,
    does,
    labelKey: `mainChat.card.button.${key}`,
    action,
    labelParams,
});

export function cardButtons(message: MainChatProactivePayload): MainChatCardButtons {
    const available = new Set(message.actionIds);
    const has = (action: string) => available.has(action);
    const open = (id: string, key: string) => has("open") ? label(id, "panel", key) : undefined;
    const answer = (action: string, key: string) => has(action) ? label(action, "answer", key, action) : undefined;

    switch (message.kind) {
        case "meeting_prep":
            return { main: open("open_prep", "openPrep"), second: has("open") ? label("open_calendar", "calendar", "openInCalendar") : undefined };
        case "calendar_conflict":
            return { main: open("show_both", "showBoth"), second: has("open") ? label("open_calendar", "calendar", "openInCalendar") : undefined };
        case "decision_due":
            if (message.target.kind === "thread") {
                return {
                    main: message.place?.bundleId && has("open")
                        ? label("take_me_there", "place", "takeMeThere")
                        : open("show_thread", "showThread"),
                    second: message.place?.bundleId && has("open") ? label("show_thread", "panel", "showThread") : undefined,
                };
            }
            return { main: has("approve") || has("decline") ? label("review", "panel", "review") : undefined };
        case "waiting_on_reply":
            return { main: open("review", "review"), second: answer("not_now", "notNow") };
        case "work_finished":
        case "playbook_result":
            return {
                main: has("review") ? label("review", "answer", "review", "review") : undefined,
                second: message.work?.deliverables[0] ? label("open_file", "file", "openFile", undefined, { file: message.work.deliverables[0].label }) : undefined,
            };
        case "work_blocked":
            return {
                main: has("review") ? label("answer", "answer", "answer", "review") : undefined,
                second: has("stop") ? label("stop", "answer", "stop", "stop") : undefined,
            };
        case "commitment_slipping":
            return { main: open("find_time", "findTime"), second: answer("not_now", "notNow") };
        case "person_reappeared":
            return {
                main: has("open_person")
                    ? label("show_person", "panel", "showName", undefined, { name: message.personName ?? message.headline })
                    : undefined,
            };
        case "topic_finds":
            return { main: open("show_whats_new", "showWhatsNew") };
        case "playbook_suggested":
            return { main: has("see") ? label("review", "panel", "review") : undefined, second: answer("not_now", "notNow") };
        case "suggestion":
            return { main: answer("do_once", "doIt"), second: answer("not_now", "notNow") };
        case "brief_ready":
            // A card that names its brief renders its own Play and Read it
            // (main-chat-brief-card.tsx). Without one, Read it opens Today.
            return message.briefId ? {} : {
                second: has("open") ? label("read_it", "target", "readIt") : undefined,
            };
        case "weekly_recap":
            // A card that names its brief renders the brief card
            // (main-chat-brief-card.tsx). Without one, the week lives in the
            // brief, so the button opens it on Today rather than expanding.
            return message.briefId ? {} : { main: label("read_week", "target", "readWeek") };
        case "first_week":
            // The first brief's card renders the brief card's own Read it.
            return message.briefId ? {} : { main: has("open") ? label("read_it", "expand", "readIt") : undefined };
        case "brief_missed":
            return { main: answer("make_it_now", "makeItNow") };
        case "schedule_missed":
            return {
                main: answer("run_now", "runItNow"),
                second: open("show_why", "showWhy"),
            };
        case "learned_to_confirm":
            // About you: That's me / Not quite; about anyone else: Right / Not quite (§5).
            return { main: answer("keep", message.personName ? "right" : "thatsMe"), second: has("correct") ? label("correct", "correct", "notQuite", "correct") : undefined };
        case "capture_paused":
            return { main: answer("resume_capture", "resume") };
        case "reminder":
            return { main: answer("done", "done"), second: answer("not_now", "notNow") };
        case "goal_check_in": {
            const actions = message.actionIds.filter((id) => id !== "mute_kind" && id !== "dismiss").slice(0, 2);
            return {
                main: actions[0] ? label(actions[0], "answer", "answer", actions[0]) : undefined,
                second: actions[1] ? label(actions[1], "answer", "answer", actions[1]) : undefined,
            };
        }
        default:
            return { main: open("review", "review") };
    }
}
