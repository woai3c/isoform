import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useEffect, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MAIN_CHAT_HELD_ACTIONS, resetMainChatHeldActions } from '@/lib/main-chat-held-action';
import i18n from "@/i18n";
import { MainChatProactiveCard } from "@/components/main-chat-proactive-card";
import { MainChatItemPanel } from "@/components/main-chat-item-panel";
import { useMainChatItemPanel } from "@/hooks/useMainChatItemPanel";
import { cardButtons, type CardButton } from "@/lib/main-chat-card-spec";
import type { MainChatProactivePayload, MainChatReportTarget } from "@/lib/stream-helpers";
import type { StreamItem } from "@/types";

const cardMocks = vi.hoisted(() => ({
    resolve: vi.fn(),
    recordReceipt: vi.fn(),
    loadItem: vi.fn(),
    openExternalURL: vi.fn(),
    openDeliverable: vi.fn(),
    returnToPlace: vi.fn(),
    playBrief: vi.fn(),
    findTime: vi.fn(),
    holdSettings: vi.fn(),
    proposeHold: vi.fn(),
    dayShape: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    LoadMainChatProactiveItem: (...args: unknown[]) => cardMocks.loadItem(...args),
    NativeClipboardSetText: vi.fn(),
    OpenDeliverableFile: (...args: unknown[]) => cardMocks.openDeliverable(...args),
    OpenExternalURL: (...args: unknown[]) => cardMocks.openExternalURL(...args),
    RecordMainChatProactiveReceipt: (...args: unknown[]) => cardMocks.recordReceipt(...args),
    ResolveMainChatProactiveAction: (...args: unknown[]) => cardMocks.resolve(...args),
    ReturnToMainChatPlace: (...args: unknown[]) => cardMocks.returnToPlace(...args),
    GetFindTimeDetails: async (...args: unknown[]) => ({ ranges: await cardMocks.findTime(...args), checkedCalendars: [], browserOnly: [] }),
    GetHoldSettings: (...args: unknown[]) => cardMocks.holdSettings(...args),
    ProposeHold: (...args: unknown[]) => cardMocks.proposeHold(...args),
    GetTodayDayShape: (...args: unknown[]) => cardMocks.dayShape(...args),
    GetHoldDayShape: (...args: unknown[]) => cardMocks.dayShape(...args),
}));
vi.mock("@/lib/wails-shim-runtime", () => ({ ClipboardSetText: vi.fn(), EventsOn: vi.fn(() => () => undefined) }));
vi.mock("@/lib/brief-audio", () => ({
    toggleBriefPlayback: (...args: unknown[]) => cardMocks.playBrief(...args),
}));
vi.mock("@/hooks/useMainChatProactiveReceipts", () => ({
    useMainChatProactiveReceipts: () => () => undefined,
}));
vi.mock("@/lib/engagement-settings-events", () => ({
    onEngagementSettingsChanged: () => () => undefined,
}));
vi.mock("@/lib/main-chat-view", () => ({ notifyMainChatViewChanged: vi.fn() }));
vi.mock("@/components/tasks/TaskThreadPane", () => ({
    TaskThreadPane: ({ id, renderSupplement }: { id: string; renderSupplement?: (detail: null) => ReactNode }) => <div data-testid="task-thread" data-task-id={id}>{renderSupplement?.(null)}</div>,
}));

const card = (
    kind: string,
    actionIds: string[],
    overrides: Partial<MainChatProactivePayload> = {},
): MainChatProactivePayload => ({
    momentId: "moment-1",
    kind,
    ...(kind === "meeting_prep" ? { event: { title: "Design review", start: "2099-01-01T10:00:00Z" } } : {}),
    headline: "A card headline.",
    body: "A specific card sentence.",
    draft: false,
    actionIds,
    actionLabels: Object.fromEntries(actionIds.map((id) => [id, id])),
    target: { kind: "today" },
    evidence: [{ kind: "record", id: "source-1", target: { kind: "today" } }],
    state: "pending",
    ...overrides,
});

const scenarios: [string, MainChatProactivePayload, string | undefined, string | undefined][] = [
    ["meeting prep", card("meeting_prep", ["open"]), "open_prep", "open_calendar"],
    ["calendar conflict", card("calendar_conflict", ["open", "keep_both"]), "show_both", "open_calendar"],
    ["thread with a place", card("decision_due", ["open"], { target: { kind: "thread", id: "thread-1" }, place: { app: "Editor", bundleId: "com.example.editor" } }), "take_me_there", "show_thread"],
    ["thread without a place", card("decision_due", ["open"], { target: { kind: "thread", id: "thread-1" } }), "show_thread", undefined],
    ["waiting on reply", card("waiting_on_reply", ["open", "not_now"]), "review", "not_now"],
    ["permission", card("decision_due", ["approve", "decline"], { target: { kind: "handoff", id: "work-1" }, permission: { requestId: "request-1", sessionId: "agent-1", handoffId: "work-1", toolName: "Bash", details: "Run a check." } }), "review", undefined],
    ["finished work", card("work_finished", ["review"], { work: { handoffId: "work-1", status: "completed", summary: "Ready.", result: "Result.", deliverables: [{ label: "Report", url: "/tmp/report.md" }] } }), "review", "open_file"],
    ["playbook result", card("playbook_result", ["review"], { work: { handoffId: "work-1", status: "completed", summary: "Ready.", result: "Result.", deliverables: [{ label: "Report", url: "/tmp/report.md" }] } }), "review", "open_file"],
    ["blocked work", card("work_blocked", ["review", "stop", "dismiss"]), "answer", "stop"],
    ["commitment", card("commitment_slipping", ["open", "not_now"], {
        actionLabels: { open: "Find time", not_now: "Not now" },
    }), "find_time", "not_now"],
    ["person", card("person_reappeared", ["open_person"], { target: { kind: "person", id: "person-1" }, personName: "Dana" }), "show_person", undefined],
    ["topic finds", card("topic_finds", ["open"]), "show_whats_new", undefined],
    ["playbook suggestion", card("playbook_suggested", ["see", "not_now"]), "review", "not_now"],
    ["suggestion", card("suggestion", ["do_once", "not_now"]), "do_once", "not_now"],
    ["brief ready", card("brief_ready", ["open"], { briefId: "brief-1" }), undefined, undefined],
    ["weekly recap", card("weekly_recap", ["open"], { briefId: "brief-1" }), undefined, undefined],
    ["first week", card("first_week", ["open"]), "read_it", undefined],
    ["missed brief", card("brief_missed", ["make_it_now"]), "make_it_now", undefined],
    ["missed schedule", card("schedule_missed", ["run_now", "open"]), "run_now", "show_why"],
    ["learned fact", card("learned_to_confirm", ["keep", "correct"]), "keep", "correct"],
    ["goal check-in", card("goal_check_in", ["update", "snooze", "dismiss"], {
        actionLabels: { update: "Update", snooze: "Snooze", dismiss: "Not now" },
    }), "update", "snooze"],
    ["paused capture", card("capture_paused", ["resume_capture"]), "resume_capture", undefined],
    ["reminder", card("reminder", ["done", "not_now"]), "done", "not_now"],
];

const behaviors: Record<string, [string | undefined, string | undefined]> = {
    "meeting prep": ["panel", "calendar"],
    "calendar conflict": ["panel", "calendar"],
    "thread with a place": ["place", "panel"],
    "thread without a place": ["panel", undefined],
    "waiting on reply": ["panel", "answer"],
    permission: ["panel", undefined],
    "finished work": ["answer", "file"],
    "playbook result": ["answer", "file"],
    "blocked work": ["answer", "answer"],
    commitment: ["panel", "answer"],
    person: ["panel", undefined],
    "topic finds": ["panel", undefined],
    "playbook suggestion": ["panel", "answer"],
    suggestion: ["answer", "answer"],
    // The brief card renders Play and Read all itself (not generic buttons).
    "brief ready": [undefined, undefined],
    // The week lives in the brief: Read your week opens it on Today
    // (polish sweep 2026-10-27; expanding showed an empty card).
    "weekly recap": [undefined, undefined],
    "first week": ["expand", undefined],
    "missed brief": ["answer", undefined],
    "missed schedule": ["answer", "panel"],
    "learned fact": ["answer", "correct"],
    "goal check-in": ["answer", "answer"],
    "paused capture": ["answer", undefined],
    reminder: ["answer", "answer"],
};

describe("main chat card button specification", () => {
    it.each(scenarios)("maps %s to its gated main and secondary buttons", (_name, message, main, second) => {
        const buttons = cardButtons(message);
        expect(buttons.main?.id).toBe(main);
        expect(buttons.second?.id).toBe(second);
        expect([buttons.main, buttons.second].filter(Boolean)).toHaveLength(
            Number(main !== undefined) + Number(second !== undefined),
        );
        expect(buttons.main?.does).toBe(behaviors[_name][0]);
        expect(buttons.second?.does).toBe(behaviors[_name][1]);
        for (const button of [buttons.main, buttons.second]) {
            if (!button) continue;
            const translation = i18n.t(button.labelKey as never, { ns: "chat", ...(button.labelParams ?? {}) } as never);
            expect(translation).not.toBe(button.labelKey);
        }
    });

    it("gates answers on the backend action ids", () => {
        expect(cardButtons(card("reminder", [])).main).toBeUndefined();
        expect(cardButtons(card("learned_to_confirm", ["keep"])).second).toBeUndefined();
        expect(cardButtons(card("brief_ready", ["open"])).main).toBeUndefined();
    });

    it("localizes the three named calendar and thread actions in both languages", async () => {
        for (const language of ["en", "zh-CN"] as const) {
            await i18n.changeLanguage(language);
            const openPrep = cardButtons(card("meeting_prep", ["open"])).main!;
            const openCalendar = cardButtons(card("calendar_conflict", ["open"])).second!;
            const thread = cardButtons(card("decision_due", ["open"], { target: { kind: "thread", id: "thread-1" } })).main!;
            expect(i18n.t(openPrep.labelKey as never, { ns: "chat" } as never)).not.toBe(openPrep.labelKey);
            expect(i18n.t(openCalendar.labelKey as never, { ns: "chat" } as never)).not.toBe(openCalendar.labelKey);
            expect(i18n.t(thread.labelKey as never, { ns: "chat" } as never)).not.toBe(thread.labelKey);
        }
    });
});

function itemFor(message: MainChatProactivePayload): StreamItem {
    return {
        id: `stream-${message.momentId}`,
        kind: "system",
        toolName: "main_chat_proactive",
        content: message.headline,
        toolInput: message,
    } as StreamItem;
}

function streamDataFor(message: MainChatProactivePayload) {
    return {
        id: `stream-${message.momentId}`,
        kind: "system",
        toolName: "main_chat_proactive",
        content: message.headline,
        toolInput: JSON.stringify(message),
        sessionId: "main-1",
        sortOrder: 1,
    };
}

function CardHarness({ message, onOpenTarget }: {
    message: MainChatProactivePayload;
    onOpenTarget?: (target: { kind: string; id?: string }) => void;
}) {
    const panel = useMainChatItemPanel("chat");
    useEffect(() => {
        panel.close();
        return () => panel.close();
    }, [panel.close]);
    const item = itemFor(message);
    return (
        <>
            <MainChatProactiveCard item={item} onOpenTarget={onOpenTarget} />
            <MainChatItemPanel surface="chat" onOpenTarget={onOpenTarget} />
        </>
    );
}

function buttonName(button: CardButton): string {
    return String(i18n.t(button.labelKey as never, {
        ns: "chat",
        ...(button.labelParams ?? {}),
    } as never));
}

function renderCard(message: MainChatProactivePayload, onOpenTarget?: (target: { kind: string; id?: string }) => void) {
    cardMocks.loadItem.mockResolvedValue(streamDataFor(message));
    return render(<CardHarness message={message} onOpenTarget={onOpenTarget} />);
}

const secondaryHandlerButtons = scenarios.flatMap(([name, message]) => {
    const button = cardButtons(message).second;
    return button && (button.does === "calendar" || button.does === "file")
        ? [{ name, message, button }]
        : [];
});

describe("rendered main chat card buttons", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
        cardMocks.resolve.mockReset().mockResolvedValue("acted");
        cardMocks.recordReceipt.mockReset().mockResolvedValue(undefined);
        cardMocks.loadItem.mockReset();
        cardMocks.openExternalURL.mockReset().mockResolvedValue(undefined);
        cardMocks.openDeliverable.mockReset().mockResolvedValue(undefined);
        cardMocks.returnToPlace.mockReset().mockResolvedValue(false);
        cardMocks.playBrief.mockReset().mockResolvedValue(undefined);
        cardMocks.findTime.mockReset().mockResolvedValue([]);
        cardMocks.holdSettings.mockReset();
        cardMocks.proposeHold.mockReset();
        cardMocks.dayShape.mockReset().mockResolvedValue(null);
    });

    afterEach(() => {
        cleanup();
        resetMainChatHeldActions();
    });

    it("answers Find time in the panel without offering a hold", async () => {
        cardMocks.findTime.mockResolvedValue([
            { start: "2026-10-30T09:30:00Z", end: "2026-10-30T11:00:00Z" },
            { start: "2026-11-02T09:00:00Z", end: "2026-11-02T11:00:00Z" },
        ]);
        renderCard(card("commitment_slipping", ["open", "not_now"]));
        fireEvent.click(screen.getByRole("button", { name: "Find time" }));
        const panel = await screen.findByTestId("main-chat-item-panel");
        expect(await within(panel).findByText(/You're free/)).toBeInTheDocument();
        expect(within(panel).queryByRole("button", { name: /hold/i })).not.toBeInTheDocument();
        expect(cardMocks.findTime).toHaveBeenCalledOnce();
    });

    it("opens a hold card for a commitment with a typed source", async () => {
        const source = { thing: { title: "Q4 plan", sourceKind: "promise", sourceId: "commitment-1" }, due: "2026-11-02T17:00:00Z" };
        cardMocks.holdSettings.mockResolvedValue({ granted: true, offerHolds: true, calendarId: "local", calendars: [{ id: "local", title: "Home", sourceId: "local", sourceTitle: "On My Mac", sourceType: "local", isDefault: true }] });
        cardMocks.proposeHold.mockResolvedValue({ id: "h", thing: source.thing, calendarId: "local", slots: [{ start: "2026-11-02T10:00:00Z", end: "2026-11-02T11:00:00Z" }] });
        renderCard(card("commitment_slipping", ["open", "not_now"], { holdSource: source }));
        fireEvent.click(screen.getByRole("button", { name: "Find time" }));
        expect(await screen.findByTestId("hold-card")).toBeInTheDocument();
        expect(cardMocks.proposeHold).toHaveBeenCalledWith(source.thing, source.due, 0);
        expect(cardMocks.findTime).not.toHaveBeenCalled();
    });

    // Brief ready and the week's card have their own brief card (Read all, Play), tested in the proactive card suite.
    it.each(scenarios.filter(([name]) => name !== "brief ready" && name !== "weekly recap"))("clicks the full-card main button for %s", async (_name, message) => {
        const main = cardButtons(message).main;
        expect(main).toBeDefined();
        const onOpenTarget = main!.does === "place" || main!.does === "target" ? vi.fn() : undefined;
        renderCard(message, onOpenTarget);
        const button = document.querySelector<HTMLButtonElement>(
            `[data-proactive-action="${main!.action ?? main!.id}"]`,
        );
        expect(button).not.toBeNull();
        if (main!.does === "expand") {
            expect(screen.queryByText(message.body)).not.toBeInTheDocument();
        }
        fireEvent.click(button!);

        if (main!.does === "panel") {
            const panel = await screen.findByTestId("main-chat-item-panel");
            expect(panel).toHaveAttribute("data-panel-open", message.momentId);
            expect(cardMocks.resolve).not.toHaveBeenCalled();
            expect(cardMocks.recordReceipt).toHaveBeenCalledWith(message.momentId, "opened");
            return;
        }
        if (main!.does === "answer" && MAIN_CHAT_HELD_ACTIONS.has(main!.action!)) {
            // Do it, Not now, Mute and a playbook offer wait 5 seconds with Undo (K5).
            expect(cardMocks.resolve).not.toHaveBeenCalled();
            expect(await screen.findByRole("button", { name: "Undo" })).toBeInTheDocument();
            return;
        }
        if (main!.does === "answer") {
            expect(main!.action).toBeDefined();
            expect(cardMocks.resolve).toHaveBeenCalledWith(message.momentId, main!.action);
            await waitFor(async () => {
                const statuses = await screen.findAllByRole("status");
                expect(statuses.some((status) => Boolean(status.textContent?.trim()))).toBe(true);
            });
            return;
        }
        if (main!.does === "place") {
            const panel = await screen.findByTestId("main-chat-item-panel");
            expect(cardMocks.returnToPlace).toHaveBeenCalledWith(message.momentId);
            expect(panel).toHaveAttribute("data-panel-open", message.momentId);
            const showThread = within(panel).getByRole("button", {
                name: i18n.t("mainChat.card.button.showThread", { ns: "chat" }),
            });
            fireEvent.click(showThread);
            expect(onOpenTarget).toHaveBeenCalledWith(message.target);
            expect(cardMocks.resolve).not.toHaveBeenCalled();
            return;
        }
        if (main!.does === "target") {
            // A brief card names its brief; Today opens at it.
            expect(onOpenTarget).toHaveBeenCalledWith(message.target.kind === "today" && !message.target.id && message.briefId
                ? { ...message.target, id: message.briefId }
                : message.target);
            expect(screen.queryByTestId("main-chat-item-panel")).not.toBeInTheDocument();
            expect(cardMocks.resolve).not.toHaveBeenCalled();
            return;
        }
        if (main!.does === "play") {
            expect(cardMocks.playBrief).toHaveBeenCalledWith(message.briefId);
            expect(screen.queryByTestId("main-chat-item-panel")).not.toBeInTheDocument();
            return;
        }
        expect(main!.does).toBe("expand");
        await waitFor(() => expect(button).toHaveAttribute("aria-expanded", "true"));
        expect(screen.getByText(message.body)).toBeInTheDocument();
        expect(screen.queryByTestId("main-chat-item-panel")).not.toBeInTheDocument();
        expect(cardMocks.resolve).not.toHaveBeenCalled();
    });

    it.each(secondaryHandlerButtons)("calls the matching handler for the %s secondary button", ({ message, button }) => {
        renderCard(message);
        fireEvent.click(screen.getByRole("button", { name: buttonName(button) }));
        if (button.does === "calendar") {
            expect(cardMocks.openExternalURL).toHaveBeenCalledWith("/System/Applications/Calendar.app");
        } else {
            const deliverable = message.work!.deliverables[0];
            expect(cardMocks.openDeliverable).toHaveBeenCalledWith("handoff", message.work!.handoffId, deliverable.url);
        }
    });

    it("opens the thread destination from the shared panel's Activity button", async () => {
        const target: MainChatReportTarget = { kind: "thread", id: "thread-1" };
        const message = card("decision_due", ["open"], { target });
        const onOpenTarget = vi.fn();
        renderCard(message, onOpenTarget);
        fireEvent.click(screen.getByRole("button", { name: buttonName(cardButtons(message).main!) }));
        const panel = await screen.findByTestId("main-chat-item-panel");
        fireEvent.click(within(panel).getByRole("button", { name: i18n.t("mainChat.card.button.openInActivity", { ns: "chat" }) }));
        expect(onOpenTarget).toHaveBeenCalledWith(target);
        expect(cardMocks.resolve).not.toHaveBeenCalled();
    });

    it("keeps the work result and guarded file action in the task thread panel", async () => {
        const message = scenarios.find(([name]) => name === "finished work")![1];
        cardMocks.openDeliverable.mockRejectedValueOnce(new Error("unavailable"));
        renderCard(message);
        fireEvent.click(screen.getByRole("button", { name: buttonName(cardButtons(message).main!) }));
        const thread = await screen.findByTestId("task-thread");
        expect(thread).toHaveAttribute("data-task-id", "work-1");
        expect(within(thread).getByText("Result.")).toBeInTheDocument();
        fireEvent.click(within(thread).getByRole("button", { name: /Report/ }));
        await waitFor(() => expect(cardMocks.openDeliverable).toHaveBeenCalledWith("handoff", "work-1", "/tmp/report.md"));
        expect(await within(thread).findByRole("alert")).toBeInTheDocument();
    });

    it("keeps the goal check-in's own answer labels", () => {
        const message = scenarios.find(([name]) => name === "goal check-in")![1];
        renderCard(message);
        expect(screen.getByRole("button", { name: "Update" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Snooze" })).toBeInTheDocument();
    });
});
