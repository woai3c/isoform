import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { MobileBriefCard, MobileBriefWeek } from "@/lib/wails-shim-app";
import { YourWeekCard, yourWeekRange } from "./YourWeekCard";
import { notifyHoldSettingsChanged } from "@/lib/hold-settings-events";

const mocks = vi.hoisted(() => ({ setKindEnabled: vi.fn(), openDeliverable: vi.fn(), readDeclined: vi.fn(), saveDeclined: vi.fn(), record: vi.fn(async () => undefined), holdSettings: vi.fn(), holdShape: vi.fn(), propose: vi.fn(), hold: vi.fn(), holdStatus: vi.fn(), undoHold: vi.fn(), dismiss: vi.fn(), undoDismiss: vi.fn(), toast: Object.assign(vi.fn(), { error: vi.fn() }) }));
vi.mock("sonner", () => ({ toast: mocks.toast }));
vi.mock("@/lib/wails-shim-app", () => ({
    SetEngagementKindEnabled: mocks.setKindEnabled,
    OpenDeliverableFile: mocks.openDeliverable,
    GetTodayYourWeekDeclined: mocks.readDeclined,
    DeclineTodayYourWeek: mocks.saveDeclined,
    RecordTodayAction: mocks.record,
    GetHoldSettings: mocks.holdSettings,
    GetHoldDayShape: mocks.holdShape,
    ProposeHold: mocks.propose,
    HoldTime: mocks.hold, GetHoldStatus: mocks.holdStatus, UndoHold: mocks.undoHold,
    DismissHoldOffer: mocks.dismiss,
    UndoDismissHoldOffer: mocks.undoDismiss,
}));

function handlers() {
    return {
        onStartChat: vi.fn(),
        onRunNow: vi.fn(),
        onOpenMemory: vi.fn(),
        onOpenAutomations: vi.fn(),
        onOpenActivity: vi.fn(),
        onOpenPeople: vi.fn(),
        onOpenTopic: vi.fn(),
        onPrepareInTopic: vi.fn(() => true),
    };
}

function fullWeek(): MobileBriefWeek {
    return {
        start: "2026-10-19",
        end: "2026-10-23",
        lead: "A Halcyon week: most of your time went there, then to Q4 plan.",
        time: [
            { topicId: "t-halcyon", name: "Halcyon", hours: 9 },
            { topicId: "t-q4", name: "Q4 plan", hours: 6 },
            { topicId: "t-hiring", name: "Hiring", hours: 4 },
            { other: true, hours: 3 },
        ],
        done: [
            { id: "outcome:1", kind: "outcome", title: "Reply to Priya", at: "2026-10-20T17:00:00Z" },
            { id: "meetings:2026-10-19", kind: "meetings", count: 4 },
            { id: "deliverable:a", kind: "deliverable", title: "Pricing page copy", at: "2026-10-22T17:00:00Z", source: "chat", sourceId: "a", path: "/tmp/a.md" },
        ],
        doneMore: 9,
        open: [
            { itemKey: "dan", cardId: "item:dan", title: "Dan is waiting on the SOW redlines", since: "2026-10-21T17:00:00Z", label: "Draft the reply to Dan" },
            { itemKey: "standup", title: "Standup notes", since: "2026-10-22T17:00:00Z" },
        ],
        learned: [
            { id: "memory:m1", kind: "memory", text: "You answer Priya within the hour, so her mail now comes first.", memoryId: "m1" },
            { id: "mute:topic_finds", kind: "mute", text: "You muted “New on your topics”, so I'll stay quiet about them.", engagementKind: "topic_finds" },
        ],
        ask: { day: "2026-10-26", text: "Start next week on Halcyon?", message: "Help me plan how to start Monday on Halcyon. Don't write to my calendar.", topicId: "t-halcyon" },
    };
}

const danCard: MobileBriefCard = { id: "item:dan", kind: "item", mode: "send", message: "Dan is waiting on my reply. What do they need?", refs: ["e1"], itemKey: "dan" };

function renderCard(week: MobileBriefWeek, cards: MobileBriefCard[] = [danCard], h = handlers()) {
    const onCard = vi.fn();
    const view = render(<YourWeekCard week={week} cards={cards} timeZone="America/Los_Angeles" handlers={h} onCard={onCard} />);
    return { h, onCard, unmount: view.unmount };
}

beforeEach(async () => {
    await i18n.changeLanguage("en");
    vi.clearAllMocks();
    mocks.readDeclined.mockResolvedValue(false);
    mocks.saveDeclined.mockResolvedValue(undefined);
    mocks.holdShape.mockResolvedValue(null);
});

describe("YourWeekCard", () => {
    it("shows the parts in the decided order", async () => {
        renderCard(fullWeek());
        const card = screen.getByTestId("brief-your-week");
        expect(within(card).getByRole("heading", { level: 2 })).toHaveTextContent("Your week · Oct 19 to 23");
        await screen.findByTestId("your-week-ask");
        const order = ["your-week-lead", "your-week-time", "your-week-done", "your-week-open", "your-week-learned", "your-week-ask"].map((id) => screen.getByTestId(id));
        for (let i = 1; i < order.length; i++) {
            expect(order[i - 1].compareDocumentPosition(order[i]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        }
        const time = screen.getByTestId("your-week-time");
        expect(within(time).getByText("Where your time went")).toBeInTheDocument();
        expect(within(time).getAllByRole("listitem").map((row) => row.textContent)).toEqual(["Halcyon 9 h", "Q4 plan 6 h", "Hiring 4 h", "Other 3 h"]);
        expect(within(time).getByText("Time on this computer, by topic.")).toBeInTheDocument();
        expect(screen.getAllByTestId("your-week-done-row").map((row) => row.textContent))
            .toEqual(["Pricing page copy · Thu"]);
        expect(screen.getByRole("button", { name: "9 more in Activity" })).toBeInTheDocument();
        expect(screen.getAllByTestId("your-week-open-row").map((row) => row.querySelector("p")?.textContent))
            .toEqual(["Dan is waiting on the SOW redlines · since Wed", "Standup notes · since Thu"]);
    });

    it("hides every empty part and never shows a zero", () => {
        renderCard({ start: "2026-10-19", end: "2026-10-23", lead: "A quiet week. I saw you on this computer 2 of 5 days." }, []);
        expect(screen.getByTestId("your-week-lead")).toHaveTextContent("A quiet week. I saw you on this computer 2 of 5 days.");
        for (const id of ["your-week-time", "your-week-done", "your-week-open", "your-week-learned", "your-week-ask"]) {
            expect(screen.queryByTestId(id)).not.toBeInTheDocument();
        }
        expect(screen.getByTestId("brief-your-week").textContent).not.toMatch(/(^|\D)0(\D|$)/);
        expect(screen.queryByText(/more in Activity/)).not.toBeInTheDocument();
    });

    it("gives a still-open row its one chip, only through its card", () => {
        const { onCard, unmount } = renderCard(fullWeek());
        const chips = screen.getAllByTestId("your-week-chip");
        expect(chips.map((chip) => chip.textContent)).toEqual(["Draft the reply to Dan"]);
        fireEvent.click(chips[0]);
        expect(onCard).toHaveBeenCalledWith(danCard);
        unmount();
        // The row's card left (its source can no longer be read): no chip,
        // never a generic one.
        renderCard(fullWeek(), []);
        expect(screen.queryByTestId("your-week-chip")).not.toBeInTheDocument();
        expect(screen.getAllByTestId("your-week-open-row")).toHaveLength(2);
    });

    it("routes only a commitment's Find time chip to the hold card", () => {
        const key = "commitments.1234567890abcdef";
        const action = { ...danCard, itemKey: key };
        const h = { ...handlers(), onFindTime: vi.fn() };
        const week = fullWeek();
        week.open = [{ itemKey: key, cardId: action.id, title: "Q4 plan", label: "Draft the reply to Dan" }];
        const { onCard, unmount } = renderCard(week, [action], h);
        fireEvent.click(screen.getByTestId("your-week-chip"));
        expect(onCard).toHaveBeenCalledWith(action);
        expect(h.onFindTime).not.toHaveBeenCalled();
        unmount();
        week.open[0].label = "Find time for Q4 plan";
        renderCard(week, [action], h);
        fireEvent.click(screen.getByTestId("your-week-chip"));
        expect(h.onFindTime).toHaveBeenCalledWith({ thing: { title: "Q4 plan", sourceKind: "week", sourceId: key } });
    });

    it("offers the next-workday morning only from a checked commitment proposal", async () => {
        const key = "commitments.1234567890abcdef";
        const askCard = { ...danCard, id: `week:ask:${key}`, itemKey: key };
        const week = fullWeek();
        week.open = [{ itemKey: key, cardId: "item:q4", title: "Q4 plan", label: "Find time for Q4 plan" }];
        week.ask = { day: "2026-10-26", cardId: askCard.id, text: "Start next week on Q4 plan?", message: "Help plan." };
        const h = { ...handlers(), onFindTime: vi.fn() };
        mocks.holdSettings.mockResolvedValue({ granted: true, offerHolds: true, calendarId: "local" });
        mocks.propose.mockResolvedValue({ slots: [{ start: "2026-10-26T17:00:00Z", end: "2026-10-26T18:00:00Z" }] });
        renderCard(week, [askCard], h);
        mocks.hold.mockResolvedValue({ id: "h", state: "pending_write", writeAfter: new Date(Date.now() + 5000).toISOString() });
        mocks.holdShape.mockResolvedValue({ rangeStart: "2026-10-26T15:00:00Z", rangeEnd: "2026-10-27T02:00:00Z", now: "2026-10-23T15:00:00Z", calendarConnected: true, events: [], hold: null });
        await screen.findByRole("button", { name: "Open hold offer for Q4 plan" });
        expect(screen.getByTestId("today-day-hold")).toHaveAttribute("data-state", "proposed");
        fireEvent.click(await screen.findByRole("button", { name: /Hold Monday/ }));
        await waitFor(() => expect(mocks.hold).toHaveBeenCalledOnce());
        expect(screen.getByTestId("your-week-ask")).toHaveTextContent("Held · Undo");
        expect(screen.getByTestId("today-day-hold")).toHaveAttribute("data-state", "held");
        expect(mocks.hold).toHaveBeenCalledOnce();
        expect(h.onFindTime).not.toHaveBeenCalled();
        fireEvent.click(within(screen.getByTestId("your-week-ask")).getByRole("button", { name: "Undo" }));
        await waitFor(() => expect(mocks.undoHold).toHaveBeenCalledWith("h"));
        expect(mocks.propose).toHaveBeenCalledWith({ title: "Q4 plan", sourceKind: "week", sourceId: key }, "", 0);
    });

    it("keeps the planning ask when holds are turned off", async () => {
        const key = "commitments.1234567890abcdef";
        const askCard = { ...danCard, id: `week:ask:${key}`, itemKey: key };
        const week = fullWeek();
        week.open = [{ itemKey: key, title: "Q4 plan", label: "Find time for Q4 plan" }];
        week.ask = { day: "2026-10-26", cardId: askCard.id, text: "Start next week on Q4 plan?", message: "Help plan." };
        const h = { ...handlers(), onFindTime: vi.fn() };
        mocks.holdSettings.mockResolvedValue({ granted: true, offerHolds: false, calendarId: "local" });
        renderCard(week, [askCard], h);
        await waitFor(() => expect(mocks.holdSettings).toHaveBeenCalled());
        expect(mocks.propose).not.toHaveBeenCalled();
        expect(screen.getByRole("button", { name: "Plan Mon" })).toBeInTheDocument();
    });

    it("withdraws an open weekly offer when holds are turned off", async () => {
        const key = "commitments.1234567890abcdef";
        const askCard = { ...danCard, id: `week:ask:${key}`, itemKey: key };
        const week = fullWeek();
        week.open = [{ itemKey: key, title: "Q4 plan", label: "Find time for Q4 plan" }];
        week.ask = { day: "2026-10-26", cardId: askCard.id, text: "Start next week on Q4 plan?", message: "Help plan." };
        const h = { ...handlers(), onFindTime: vi.fn() };
        mocks.holdSettings.mockResolvedValueOnce({ granted: true, offerHolds: true, calendarId: "local" }).mockResolvedValue({ granted: true, offerHolds: false, calendarId: "local" });
        mocks.propose.mockResolvedValue({ slots: [{ start: "2026-10-26T17:00:00Z", end: "2026-10-26T18:00:00Z" }] });
        renderCard(week, [askCard], h);
        await screen.findByRole("button", { name: /Hold Monday/ });
        notifyHoldSettingsChanged();
        expect(await screen.findByRole("button", { name: "Plan Mon" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Hold Monday/ })).not.toBeInTheDocument();
    });

    it("withdraws the offer as soon as its source leaves the brief", async () => {
        const key = "commitments.1234567890abcdef";
        const askCard = { ...danCard, id: `week:ask:${key}`, itemKey: key };
        const week = fullWeek();
        week.open = [{ itemKey: key, title: "Q4 plan", label: "Find time for Q4 plan" }];
        week.ask = { day: "2026-10-26", cardId: askCard.id, text: "Start next week?", message: "Help plan." };
        const h = { ...handlers(), onFindTime: vi.fn() };
        mocks.holdSettings.mockResolvedValue({ granted: true, offerHolds: true, calendarId: "local" });
        mocks.propose.mockResolvedValue({ slots: [{ start: "2026-10-26T17:00:00Z", end: "2026-10-26T18:00:00Z" }] });
        const props = { cards: [askCard], timeZone: "America/Los_Angeles", handlers: h, onCard: vi.fn(), onTurnedOff: vi.fn() };
        const { rerender } = render(<YourWeekCard {...props} week={week} />);
        await screen.findByRole("button", { name: /Hold Monday/ });
        rerender(<YourWeekCard {...props} week={{ ...week, open: [] }} />);
        expect(screen.queryByText(/I can hold/)).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Hold Monday/ })).not.toBeInTheDocument();
    });

    it("dismisses the weekly offer for the source and can undo it", async () => {
        const key = "commitments.1234567890abcdef";
        const askCard = { ...danCard, id: `week:ask:${key}`, itemKey: key };
        const week = fullWeek();
        week.open = [{ itemKey: key, title: "Q4 plan", label: "Find time for Q4 plan" }];
        week.ask = { day: "2026-10-26", cardId: askCard.id, text: "Start next week on Q4 plan?", message: "Help plan." };
        const h = { ...handlers(), onFindTime: vi.fn() };
        mocks.holdSettings.mockResolvedValue({ granted: true, offerHolds: true, calendarId: "local" });
        mocks.propose.mockResolvedValue({ slots: [{ start: "2026-10-26T17:00:00Z", end: "2026-10-26T18:00:00Z" }] });
        mocks.dismiss.mockResolvedValue("dismissal");
        mocks.undoDismiss.mockResolvedValue(undefined);
        renderCard(week, [askCard], h);
        await screen.findByRole("button", { name: /Hold Monday/ });
        fireEvent.click(within(screen.getByTestId("your-week-ask")).getByRole("button", { name: "Not now" }));
        fireEvent.click(within(await screen.findByRole("status")).getByRole("button", { name: "Undo" }));
        await waitFor(() => expect(mocks.undoDismiss).toHaveBeenCalledWith("dismissal"));
        expect(mocks.dismiss).toHaveBeenCalledWith({ title: "Q4 plan", sourceKind: "week", sourceId: key });
        expect(h.onFindTime).not.toHaveBeenCalled();
    });

    it("routes weekly chips to the answer host even when their label mentions a draft", () => {
        const onCard = vi.fn();
        const onAskCard = vi.fn();
        const week = fullWeek();
        const props = { cards: [danCard], handlers: handlers(), onCard, onAskCard, onTurnedOff: vi.fn() };
        const view = render(<YourWeekCard week={week} {...props} />);
        fireEvent.click(screen.getByTestId("your-week-chip"));
        expect(onCard).not.toHaveBeenCalled();
        expect(onAskCard).toHaveBeenCalledWith(danCard);
        onAskCard.mockClear();
        view.rerender(<YourWeekCard week={{ ...week, open: [{ ...week.open![0], label: "What does Dan need?" }] }} {...props} />);
        fireEvent.click(screen.getByTestId("your-week-chip"));
        expect(onAskCard).toHaveBeenCalledWith(danCard);
    });

    it("sends non-Find-time commitment chips to the answer host and retains hold sources for Find time", () => {
        const key = "commitments.1234567890abcdef";
        const card = { ...danCard, itemKey: key };
        const week = fullWeek();
        week.open = [{ itemKey: key, cardId: card.id, title: "Q4 plan", label: "Draft the reply" }];
        const onAskCard = vi.fn();
        const onFindTime = vi.fn();
        const props = { cards: [card], handlers: { ...handlers(), onFindTime }, onCard: vi.fn(), onAskCard, onTurnedOff: vi.fn() };
        const view = render(<YourWeekCard week={week} {...props} />);
        fireEvent.click(screen.getByTestId("your-week-chip"));
        expect(onAskCard).toHaveBeenCalledWith(card);
        expect(onFindTime).not.toHaveBeenCalled();
        view.rerender(<YourWeekCard week={{ ...week, open: [{ ...week.open[0], label: "Find time for Q4 plan" }] }} {...props} />);
        fireEvent.click(screen.getByTestId("your-week-chip"));
        expect(onFindTime).toHaveBeenCalledWith({ thing: { title: "Q4 plan", sourceKind: "week", sourceId: key } });
    });

    it("opens each thing it did for you", () => {
        const { h } = renderCard(fullWeek());
        const rows = screen.getAllByTestId("your-week-done-row");
        fireEvent.click(rows[0]);
        expect(mocks.openDeliverable).toHaveBeenCalledWith("chat", "a", "/tmp/a.md");
        fireEvent.click(screen.getByRole("button", { name: "9 more in Activity" }));
        expect(h.onOpenActivity).toHaveBeenCalledTimes(1);
        fireEvent.click(screen.getByRole("button", { name: "Halcyon 9 h" }));
        expect(h.onOpenTopic).toHaveBeenCalledWith("t-halcyon");
    });

    it("gives each learned line its fix", async () => {
        mocks.setKindEnabled.mockResolvedValue({});
        const { h } = renderCard(fullWeek());
        expect(screen.queryByRole("button", { name: "Not right" })).not.toBeInTheDocument();
        expect(h.onOpenMemory).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole("button", { name: "Undo" }));
        expect(mocks.setKindEnabled).toHaveBeenCalledWith("topic_finds", true);
        await waitFor(() => expect(screen.queryByText(/You muted/)).not.toBeInTheDocument());
    });

    it("brings an undone mute back when the computer refuses", async () => {
        mocks.setKindEnabled.mockRejectedValue(new Error("unavailable"));
        renderCard(fullWeek());
        fireEvent.click(screen.getByRole("button", { name: "Undo" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not undo that. Try again in Settings.");
        expect(screen.getByText(/You muted/)).toBeInTheDocument();
    });

    it("accepts the next-workday recommendation in one click, in the topic's box", async () => {
        const { h } = renderCard(fullWeek());
        fireEvent.click(await screen.findByRole("button", { name: "Plan Mon" }));
        expect(h.onPrepareInTopic).toHaveBeenCalledWith("t-halcyon", "Help me plan how to start Monday on Halcyon. Don't write to my calendar.");
        expect(screen.queryByText("Ready in the box on the right.")).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Not now" }));
        await waitFor(() => expect(screen.queryByTestId("your-week-ask")).not.toBeInTheDocument());
    });

    it("hides a hold offer from an older brief", () => {
        const week = fullWeek();
        week.ask = {
            ...week.ask!,
            hold: { start: "2026-10-27T00:30:00Z", end: "2026-10-27T02:30:00Z", label: "Halcyon" },
        };
        renderCard(week);
        expect(screen.queryByTestId("your-week-ask")).not.toBeInTheDocument();
    });

    it("saves Not now for the week and keeps it hidden after reopening", async () => {
        const view = renderCard(fullWeek());
        fireEvent.click(await screen.findByRole("button", { name: "Not now" }));
        await waitFor(() => expect(mocks.saveDeclined).toHaveBeenCalledWith("2026-10-19"));
        await waitFor(() => expect(screen.queryByTestId("your-week-ask")).not.toBeInTheDocument());
        view.unmount();
        mocks.readDeclined.mockResolvedValue(true);
        renderCard(fullWeek());
        await waitFor(() => expect(mocks.readDeclined).toHaveBeenCalledTimes(2));
        expect(screen.queryByTestId("your-week-ask")).not.toBeInTheDocument();
    });

    it("does not show a declined recommendation while its saved state cannot be read", async () => {
        mocks.readDeclined.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(true);
        renderCard(fullWeek());
        const retry = await screen.findByRole("alert");
        expect(retry).toHaveTextContent("Couldn't check your choice. Try again");
        expect(screen.queryByTestId("your-week-ask")).not.toBeInTheDocument();
        fireEvent.click(retry);
        await waitFor(() => expect(mocks.readDeclined).toHaveBeenCalledTimes(2));
        expect(screen.queryByTestId("your-week-ask")).not.toBeInTheDocument();
    });

    it("brings the recommendation back with an inline line if saving fails", async () => {
        mocks.saveDeclined.mockRejectedValueOnce(new Error("offline"));
        renderCard(fullWeek());
        fireEvent.click(await screen.findByRole("button", { name: "Not now" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Your Not now didn't save, so it's back.");
        expect(screen.getByTestId("your-week-ask")).toBeInTheDocument();
    });

    it("shows no recommendation it cannot act on", () => {
        renderCard({ ...fullWeek(), ask: { ...fullWeek().ask!, topicId: undefined, cardId: "week:ask:gone" } });
        expect(screen.queryByTestId("your-week-ask")).not.toBeInTheDocument();
    });

    it("turns off from its menu, which the text never mentions", async () => {
        mocks.setKindEnabled.mockResolvedValue({});
        renderCard(fullWeek());
        expect(screen.getByTestId("brief-your-week").textContent).not.toContain("Turn off");
        fireEvent.pointerDown(screen.getByTestId("your-week-menu"), { button: 0, pointerType: "mouse" });
        fireEvent.click(await screen.findByRole("menuitem", { name: "Turn off Your week" }));
        expect(await screen.findByText("Your week is off")).toBeInTheDocument();
        expect(mocks.setKindEnabled).toHaveBeenCalledWith("weekly_recap", false);
        fireEvent.click(screen.getByRole("button", { name: "Undo" }));
        await waitFor(() => expect(mocks.setKindEnabled).toHaveBeenCalledWith("weekly_recap", true));
        expect(await screen.findByTestId("your-week-lead")).toBeInTheDocument();
    });

    it("keeps the card and reports a failed turn off inline", async () => {
        mocks.setKindEnabled.mockRejectedValueOnce(new Error("offline"));
        renderCard(fullWeek());
        fireEvent.pointerDown(screen.getByTestId("your-week-menu"), { button: 0, pointerType: "mouse" });
        fireEvent.click(await screen.findByRole("menuitem", { name: "Turn off Your week" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not turn off Your week. Try again.");
        expect(screen.getByTestId("your-week-lead")).toBeInTheDocument();
        expect(mocks.record).toHaveBeenCalledWith("today", "T78", "do", "failed", expect.any(Number));
    });

    it("shows a file-open failure in the card", async () => {
        mocks.openDeliverable.mockRejectedValueOnce(new Error("missing"));
        renderCard(fullWeek());
        fireEvent.click(screen.getAllByTestId("your-week-done-row")[0]);
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not open that file.");
    });

    it("reads in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        renderCard(fullWeek());
        expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("你的一周 · 10月19日至23日");
        await screen.findByTestId("your-week-ask");
        expect(screen.queryByText("4 个通话的笔记")).not.toBeInTheDocument();
        for (const text of ["时间花在哪了", "按话题统计的这台电脑上的时间。", "替你做完的", "Activity 里还有 9 项", "还没收尾的", "我学到的", "撤销", "规划周一", "暂不"]) {
            expect(screen.getByText(text)).toBeInTheDocument();
        }
    });
});

describe("yourWeekRange", () => {
    it("names the month once, or twice across a month end", () => {
        expect(yourWeekRange("2026-10-26", "2026-10-30", "en", i18n.getFixedT("en", "today"))).toBe("Oct 26 to 30");
        expect(yourWeekRange("2026-10-26", "2026-11-01", "en", i18n.getFixedT("en", "today"))).toBe("Oct 26 to Nov 1");
        expect(yourWeekRange("2026-10-19", "2026-10-23", "zh-CN", i18n.getFixedT("zh-CN", "today"))).toBe("10月19日至23日");
    });
});


it("opens only an exact weekly task or file from Today", () => {
    const onOpenWeekTask = vi.fn();
    const h = { ...handlers(), onOpenWeekTask };
    const week = fullWeek();
    week.done = [{ id: "exact", kind: "outcome", title: "Prepared", taskId: "task-1" }, { id: "unknown", kind: "outcome", title: "Unknown source" }, { id: "file", kind: "deliverable", title: "File", source: "chat", sourceId: "a", path: "/tmp/a.md" }];
    render(<YourWeekCard week={week} cards={[]} handlers={h} onCard={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Prepared" }));
    expect(onOpenWeekTask).toHaveBeenCalledWith("task-1");
    expect(screen.queryByText("Unknown source")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "File" }));
    expect(mocks.openDeliverable).toHaveBeenCalledWith("chat", "a", "/tmp/a.md");
    expect(h.onOpenActivity).not.toHaveBeenCalled();
});


it("routes the weekly closing ask to next week's bounded availability", async () => {
    const h = { ...handlers(), onFindTimeRange: vi.fn() };
    renderCard(fullWeek(), [], h);
    expect(await screen.findByTestId("your-week-ask")).toHaveTextContent("When can I start Halcyon next week?");
    fireEvent.click(screen.getByRole("button", { name: "Ask" }));
    expect(h.onFindTimeRange).toHaveBeenCalledWith({ first: "2026-10-26", last: "2026-10-30", minutes: 60, nextWeek: true });
});


it("translates the weekly availability question in Chinese", async () => {
    await i18n.changeLanguage("zh-CN");
    const h = { ...handlers(), onFindTimeRange: vi.fn() };
    renderCard(fullWeek(), [], h);
    expect(await screen.findByTestId("your-week-ask")).toHaveTextContent("下周什么时候可以开始做 Halcyon？");
});
