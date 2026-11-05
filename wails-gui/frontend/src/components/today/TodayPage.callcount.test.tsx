import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { resetPageQueries } from "@/lib/page-queries";
import { resetMainChatWaitingStore } from "@/lib/main-chat-waiting-store";
import { TodayPage } from "./TodayPage";
import { HoldCard } from "./HoldCard";
import { renderHook } from "@testing-library/react";
import { useTodayNews } from "@/hooks/useTodayNews";
import { useTodaySuggestion } from "@/hooks/useTodaySuggestion";

// Whole-Today call counts under Bo's fetch rule (2026-11-04): one read on mount
// per binding, one read per real change event, no reads while hidden or locked,
// and no polling. Real hooks render; only the Wails boundary is faked.

const fixture = vi.hoisted(() => ({
    calls: new Map<string, number>(),
    listeners: new Map<string, Set<(payload: unknown) => void>>(),
}));

// A booked hold a HoldCard shows at mount (ProposeHold returns it as existing).
const holdStatus = { id: "hold-1", state: "held", start: "2099-01-01T10:00:00.000Z", end: "2099-01-01T10:30:00.000Z", calendarId: "cal", calendarTitle: "Work", writeAfter: "2000-01-01T00:00:00.000Z", removedAt: "" };

const fallback = (name: string): unknown => {
    switch (name) {
        case "GetLatestBrief": return { brief: null, building: false };
        case "GetTodayTopics": return { topics: [], refreshing: false, firstRun: false, hasTopics: false };
        case "GetNeedsYou": return { items: [], putOff: [] };
        case "GetHoldSettings": return { granted: true, offerHolds: true, calendarId: "cal", calendars: [{ id: "cal", title: "Work", isDefault: true, meetingsAccount: false }], browserOnly: [] };
        case "ProposeHold": return { dismissed: false, slots: [], existing: holdStatus };
        case "GetHoldStatus": return holdStatus;
        case "GetTodayQuietLines": return [];
        case "GetTopicFinds": return { enabled: false, status: "none", items: [], topics: [] };
        default: return null;
    }
};

function fakeBridge(prefix: string) {
    return new Proxy({}, {
        has: () => true,
        get: (_target, name: string) => {
            if (name === "then") return undefined;
            return (...args: unknown[]) => {
                const key = prefix + name;
                void args;
                fixture.calls.set(key, (fixture.calls.get(key) ?? 0) + 1);
                return Promise.resolve(fallback(name));
            };
        },
    });
}

vi.mock("@/lib/wails-shim-app", () => fakeBridge(""));
vi.mock("../../../wailsjs/go/main/App", () => fakeBridge(""));
vi.mock("../../../wailsjs/runtime/runtime", () => ({
    BrowserOpenURL: () => undefined,
    EventsOn: (name: string, handler: (payload: unknown) => void) => {
        const set = fixture.listeners.get(name) ?? new Set();
        set.add(handler);
        fixture.listeners.set(name, set);
        return () => { set.delete(handler); };
    },
}));

const count = (name: string) => fixture.calls.get(name) ?? 0;
const snapshot = () => new Map(fixture.calls);

function fire(event: string, payload: unknown = undefined) {
    for (const handler of [...(fixture.listeners.get(event) ?? [])]) handler(payload);
}

function setHidden(hidden: boolean) {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => (hidden ? "hidden" : "visible") });
    document.dispatchEvent(new Event("visibilitychange"));
}

const settle = (ms = 400) => act(async () => { await new Promise((resolve) => setTimeout(resolve, ms)); });

// Each change event and the Today binding it re-reads. Only bindings TodayPage
// renders are listed here. useTodayNews and useTodaySuggestion have no consumer
// on Today (counted at the hook below), and ActivityNoticed lives on Activity.
const CHANGE_READS: Array<[string, string]> = [
    ["today:needs-changed", "GetNeedsYou"],
    ["mobile:updated", "GetMobileBriefHealth"],
    ["mobile:updated", "GetEngagementWeekSummary"],
    ["brief:stored", "GetLatestBrief"],
    ["brief:stored", "GetTodayQuietLines"],
    ["today-dayshape:updated", "GetTodayDayShape"],
    ["today-before-you-talk:updated", "GetTodayBeforeYouTalk"],
    ["topics:updated", "GetTodayTopics"],
    ["topics:finds", "GetTopicFinds"],
    ["agency:updated", "GetGoalCheckIn"],
    ["portrait:updated", "GetPortraitTodayCard"],
];

describe("Today call counts (fetch rule)", () => {
    beforeEach(() => {
        resetPageQueries();
        resetMainChatWaitingStore();
        fixture.calls.clear();
        fixture.listeners.clear();
        setHidden(false);
    });

    it("reads each binding once at mount, once per change event, never while hidden, and never on a timer", async () => {
        await i18n.changeLanguage("en");
        render(<TodayPage handlers={{
            onStartChat: vi.fn(), onRunNow: vi.fn(), onOpenMemory: vi.fn(), onOpenAutomations: vi.fn(),
            onOpenActivity: vi.fn(), onOpenPeople: vi.fn(), onOpenTasks: vi.fn(),
        }} />);
        // Mount reads are coalesced (250 ms), and a card can enable a moment after
        // first paint (Meeting prep follows the page's active flag). Wait until the
        // reads go quiet, then take the mount snapshot.
        let last = "";
        for (let i = 0; i < 20; i++) {
            await settle(500);
            const now = JSON.stringify([...snapshot()]);
            if (now === last) break;
            last = now;
        }
        const mounted = snapshot();
        // Every Today binding the page reads at mount is read exactly once
        // (RecordTodaySectionSeen is a write, so it is exempt).
        for (const [name, calls] of mounted) {
            if (name !== "RecordTodaySectionSeen") expect(calls, name).toBe(1);
        }
        expect(mounted.size).toBeGreaterThan(0);

        // Idle: nothing polls. Five seconds of quiet adds no calls.
        await settle(5000);
        expect(snapshot()).toEqual(mounted);

        // Each change event adds exactly one read to the binding it owns.
        for (const [event, binding] of CHANGE_READS) {
            const before = count(binding);
            await act(async () => { fire(event, {}); });
            await settle(400);
            expect(count(binding) - before, `${event} -> ${binding}`).toBe(1);
        }

        // Hidden: no reads, even when every change event fires.
        setHidden(true);
        await settle(100);
        const hidden = snapshot();
        for (const [event] of CHANGE_READS) await act(async () => { fire(event, {}); });
        await settle(1000);
        expect(snapshot()).toEqual(hidden);

        // Shown again: the hidden changes are read once, not once per event.
        setHidden(false);
        await settle(1000);
        for (const [name, calls] of snapshot()) {
            expect(calls - (hidden.get(name) ?? 0), `shown ${name}`).toBeLessThanOrEqual(1);
        }
    }, 60_000);

    it("reads a HoldCard's own hold once per holds:updated for it, never while hidden, never on a timer", async () => {
        await i18n.changeLanguage("en");
        render(<HoldCard source={{ thing: { title: "Plan", sourceKind: "task", sourceId: "t1" } as never }} onClose={vi.fn()} />);
        await settle(1000);
        // The card shows the hold from ProposeHold (existing): no GetHoldStatus at mount.
        expect(count("GetHoldStatus")).toBe(0);

        await act(async () => { fire("holds:updated", { id: "other-hold" }); });
        await settle(400);
        expect(count("GetHoldStatus")).toBe(0);

        await act(async () => { fire("holds:updated", { id: "hold-1" }); });
        await settle(400);
        expect(count("GetHoldStatus")).toBe(1);

        await act(async () => { fire("holds:updated", {}); });
        await settle(400);
        expect(count("GetHoldStatus")).toBe(2);

        setHidden(true);
        await settle(100);
        await act(async () => { fire("holds:updated", { id: "hold-1" }); });
        await settle(1000);
        expect(count("GetHoldStatus")).toBe(2);

        setHidden(false);
        await settle(1000);
        expect(count("GetHoldStatus")).toBe(3);

        await settle(5000);
        expect(count("GetHoldStatus")).toBe(3);
    }, 60_000);
});

// Today's two hook-level bindings are not rendered by TodayPage (no component
// mounts useTodayNews or useTodaySuggestion), so they are counted at the hook.
describe("Today hook bindings not on the page (call counts)", () => {
    beforeEach(() => {
        resetPageQueries();
        fixture.calls.clear();
        fixture.listeners.clear();
        setHidden(false);
    });

    it("GetTodayNews: one read at mount, one per cron:run, none hidden, none on a timer", async () => {
        renderHook(() => useTodayNews(true));
        await settle(1000);
        expect(count("GetTodayNews")).toBe(1);
        await settle(5000);
        expect(count("GetTodayNews")).toBe(1);
        await act(async () => { fire("cron:run", {}); });
        await settle(400);
        expect(count("GetTodayNews")).toBe(2);
        setHidden(true);
        await settle(100);
        await act(async () => { fire("cron:run", {}); });
        await settle(1000);
        expect(count("GetTodayNews")).toBe(2);
        setHidden(false);
        await settle(1000);
        expect(count("GetTodayNews")).toBe(3);
    }, 30_000);

    it("GetTodaySuggestion: one read at mount, one per initiative:updated, none hidden, none on a timer", async () => {
        renderHook(() => useTodaySuggestion(true));
        await settle(1000);
        expect(count("GetTodaySuggestion")).toBe(1);
        await settle(5000);
        expect(count("GetTodaySuggestion")).toBe(1);
        await act(async () => { fire("initiative:updated", {}); });
        await settle(400);
        expect(count("GetTodaySuggestion")).toBe(2);
        setHidden(true);
        await settle(100);
        await act(async () => { fire("initiative:updated", {}); });
        await settle(1000);
        expect(count("GetTodaySuggestion")).toBe(2);
        setHidden(false);
        await settle(1000);
        expect(count("GetTodaySuggestion")).toBe(3);
    }, 30_000);
});
