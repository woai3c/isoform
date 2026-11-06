import { act, render, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { pageQueryCache } from "@/lib/query-cache";
import { resetPageQueries } from "@/lib/page-queries";
import type { ContactsAccessStatus, PeopleOverview, PersonDetail, PrincipalDTO, PrincipalWithStatsDTO } from "@/lib/wails-shim-app";
import { readSavedPeopleView, savePeopleView } from "@/lib/people-saved-view";

const fixture = vi.hoisted(() => ({ bindings: new Map<string, ReturnType<typeof vi.fn>>(), events: new Map<string, Set<(payload?: unknown) => void>>() }));
vi.mock("@/lib/wails-shim-app", () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === "then") return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue({}));
        return fixture.bindings.get(name);
    },
}));
vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, handler: (payload?: unknown) => void) => {
        const handlers = fixture.events.get(name) ?? new Set();
        handlers.add(handler);
        fixture.events.set(name, handlers);
        return () => handlers.delete(handler);
    },
}));

import { PeoplePage, PEOPLE_RESYNC_MS } from "./people-page";

const binding = (name: string) => fixture.bindings.get(name)!;

const OVERVIEW: PeopleOverview = {
    headline: { repliesOwed: 0, thingsOwed: 0, peopleWaitingOnYou: 0 },
    open: [],
    everyone: [{ id: "person", kind: "human", name: "Fixture person", relationship: "", photo: "", loops: [], lastSeenAt: 0, lastSeenApp: "" }],
    duplicates: [],
    knownCount: 1,
};
const ACCESS = { status: "not_requested" } as unknown as ContactsAccessStatus;
const DETAIL: PersonDetail = { id: "person", name: "Fixture person", relationship: "", notes: "", researchSummary: "", loops: [], together: [], sharedTopics: [], seenIn: [], messageCount: 0 };

function mockReads(overview: PeopleOverview = OVERVIEW) {
    fixture.bindings.set("ListPeopleOverview", vi.fn().mockResolvedValue(overview));
    fixture.bindings.set("ListPrincipalsWithStats", vi.fn().mockResolvedValue([]));
    fixture.bindings.set("GetContactsAccessStatus", vi.fn().mockResolvedValue(ACCESS));
    fixture.bindings.set("IsClosedEyesModeEnabled", vi.fn().mockResolvedValue(false));
    fixture.bindings.set("GetPersonDetail", vi.fn().mockResolvedValue(DETAIL));
    fixture.bindings.set("ListPrincipalActivitySessions", vi.fn().mockResolvedValue([]));
    fixture.bindings.set("GetPersonResearch", vi.fn().mockResolvedValue(null));
    fixture.bindings.set("GetVoiceEnrollmentStatus", vi.fn().mockResolvedValue(null));
    fixture.bindings.set("ListVoiceSamples", vi.fn().mockResolvedValue([]));
}

function seedCache(overview: PeopleOverview = OVERVIEW) {
    pageQueryCache.set("ListPeopleOverview", [], overview);
    pageQueryCache.set("ListPrincipalsWithStats", [], []);
    pageQueryCache.set("GetContactsAccessStatus", [], ACCESS);
}

const mainOf = (container: HTMLElement) => container.querySelector("main") as HTMLElement;

beforeEach(async () => {
    await i18n.changeLanguage("en");
    resetPageQueries();
    fixture.bindings.clear();
    fixture.events.clear();
    mockReads();
});
afterEach(() => resetPageQueries());

describe("Who you know page query cache", () => {
    it("paints the cached overview on reopen with no skeleton and no network reads", async () => {
        seedCache();
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(within(main).getByText("Fixture person")).toBeInTheDocument();
        expect(main.querySelector('div[aria-busy="true"]')).toBeNull();
        expect(main.querySelector(".animate-pulse")).toBeNull();
        // The seeded reads are still fresh, so the reopen revalidates nothing.
        await act(async () => {});
        expect(binding("ListPeopleOverview")).not.toHaveBeenCalled();
        expect(binding("ListPrincipalsWithStats")).not.toHaveBeenCalled();
        expect(binding("GetContactsAccessStatus")).not.toHaveBeenCalled();
    });

    it("shows the skeleton on a true first load, then the list", async () => {
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(main.querySelector('div[aria-busy="true"]')).not.toBeNull();
        expect(main.querySelector(".animate-pulse")).not.toBeNull();
        expect(within(main).queryByText("Fixture person")).toBeNull();
        expect(await within(main).findByText("Fixture person")).toBeInTheDocument();
        expect(main.querySelector('div[aria-busy="true"]')).toBeNull();
        expect(binding("ListPeopleOverview")).toHaveBeenCalled();
    });

    it("keeps the stale paint on invalidation and swaps in the changed payload", async () => {
        seedCache();
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(within(main).getByText("Fixture person")).toBeInTheDocument();
        const updated: PeopleOverview = { ...OVERVIEW, everyone: [{ ...OVERVIEW.everyone[0], name: "Renamed person" }] };
        binding("ListPeopleOverview").mockResolvedValue(updated);
        await act(async () => {
            pageQueryCache.invalidate(["ListPeopleOverview"]);
        });
        expect(await within(main).findByText("Renamed person")).toBeInTheDocument();
        expect(within(main).queryByText("Fixture person")).toBeNull();
        // The invalidation re-read the overview once; the still-fresh
        // principals and contacts reads stayed on the cache.
        expect(binding("ListPeopleOverview")).toHaveBeenCalledTimes(1);
        expect(binding("ListPrincipalsWithStats")).not.toHaveBeenCalled();
        expect(main.querySelector('div[aria-busy="true"]')).toBeNull();
    });
});

// Evening walk N40: the page failed on its first open while Isoform was busy.
// It paints at once from the saved view; only a failure with nothing to show
// says so, in copper, with a working Try again in the accent.
describe("Who you know first open (N40)", () => {
    const never = () => new Promise<never>(() => {});
    const ENTRY = {
        principal: { id: "person", kind: "human", displayName: "Fixture person", relationship: "", notes: "", aliases: [], appHandles: [] } as unknown as PrincipalDTO,
        stats: { principalId: "person", totalMessages: 1, firstSeenAt: 0, lastSeenAt: 0, byApp: [], recentMessages: [{ app: "wechat", seenAt: 1, preview: "the private message text" }] },
    } as unknown as PrincipalWithStatsDTO;

    it("paints the saved view at once while a slow first read runs", async () => {
        savePeopleView(OVERVIEW, []);
        binding("ListPeopleOverview").mockImplementation(never);
        binding("ListPrincipalsWithStats").mockImplementation(never);
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(within(main).getByText("Fixture person")).toBeInTheDocument();
        expect(main.querySelector('div[aria-busy="true"]')).toBeNull();
        await act(async () => {});
        expect(binding("ListPeopleOverview")).toHaveBeenCalled();
        expect(within(main).queryByRole("alert")).toBeNull();
    });

    it("paints and saves the overview while the directory statistics are still loading", async () => {
        binding("ListPrincipalsWithStats").mockImplementation(never);
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(await within(main).findByText("Fixture person")).toBeInTheDocument();
        expect(readSavedPeopleView()?.overview.everyone[0].name).toBe("Fixture person");
        expect(main.querySelector('div[aria-busy="true"]')).toBeNull();
    });

    it("keeps the overview when the directory statistics fail", async () => {
        binding("ListPrincipalsWithStats").mockRejectedValue(new Error("busy"));
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(await within(main).findByText("Fixture person")).toBeInTheDocument();
        expect(within(main).queryByRole("alert")).toBeNull();
    });

    it("keeps a known self visible while the directory read fails", async () => {
        const self = { ...OVERVIEW.everyone[0], id: "self", kind: "self", name: "Bo" };
        binding("ListPeopleOverview").mockResolvedValue({ ...OVERVIEW, everyone: [...OVERVIEW.everyone, self] });
        binding("ListPrincipalsWithStats").mockRejectedValue(new Error("busy"));
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(await within(main).findByText("Bo · You")).toBeInTheDocument();
        expect(within(main).queryByRole("button", { name: "Name yourself" })).toBeNull();
    });

    it("keeps the sender sort-out line and sheet when directory statistics fail", async () => {
        const sender = { ...OVERVIEW.everyone[0], id: "sender", kind: "anonymous", name: "Sender" };
        binding("ListPeopleOverview").mockResolvedValue({ ...OVERVIEW, everyone: [...OVERVIEW.everyone, sender] });
        binding("ListPrincipalsWithStats").mockRejectedValue(new Error("busy"));
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(await within(main).findByText("Isoform couldn't tell who 1 sender is.")).toBeInTheDocument();
        await act(async () => { within(main).getByRole("button", { name: "Sort them out" }).click(); });
        expect(within(document.body).getByRole("dialog", { name: "Senders to sort out" })).toHaveTextContent("Sender");
    });

    it("keeps the saved view and says nothing when the read behind it fails", async () => {
        savePeopleView(OVERVIEW, []);
        binding("ListPeopleOverview").mockRejectedValue(new Error("busy"));
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        await act(async () => { await new Promise((resolve) => setTimeout(resolve, 300)); });
        expect(binding("ListPeopleOverview")).toHaveBeenCalled();
        expect(within(main).getByText("Fixture person")).toBeInTheDocument();
        expect(within(main).queryByRole("alert")).toBeNull();
    });

    it("with nothing saved, a failed read says so in copper and Try again in the accent loads the page", async () => {
        binding("ListPeopleOverview").mockRejectedValue(new Error("busy"));
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        const failure = await within(main).findByRole("alert");
        expect(failure).toHaveTextContent("Couldn't load who you know.");
        expect(failure.className).toContain("text-copper");
        const retry = within(failure).getByRole("button", { name: "Try again" });
        expect(retry.className).toContain("text-accent-2");
        binding("ListPeopleOverview").mockResolvedValue(OVERVIEW);
        await act(async () => { retry.click(); });
        expect(await within(main).findByText("Fixture person")).toBeInTheDocument();
        expect(within(main).queryByRole("alert")).toBeNull();
    });

    it("says so in zh-CN too", async () => {
        await i18n.changeLanguage("zh-CN");
        binding("ListPeopleOverview").mockRejectedValue(new Error("busy"));
        const { container } = render(<PeoplePage />);
        const failure = await within(mainOf(container)).findByRole("alert");
        expect(failure).toHaveTextContent("没能加载你认识的人。");
        expect(within(failure).getByRole("button", { name: "重试" })).toBeInTheDocument();
    });

    it("saves what it read without message text, and a privacy change drops it", async () => {
        binding("ListPrincipalsWithStats").mockResolvedValue([ENTRY]);
        const { container } = render(<PeoplePage />);
        expect(await within(mainOf(container)).findByText("Fixture person")).toBeInTheDocument();
        const saved = readSavedPeopleView();
        expect(saved?.overview.everyone[0].name).toBe("Fixture person");
        expect(saved?.entries[0].stats.recentMessages).toEqual([]);
        expect(JSON.stringify(localStorage)).not.toContain("the private message text");
        // The withdrawal drops it at once; only the sweep's own re-read may
        // save again.
        let afterWithdrawal: unknown = "unset";
        act(() => {
            for (const handler of fixture.events.get("closed-eyes:changed") ?? []) handler(true);
            afterWithdrawal = readSavedPeopleView();
        });
        expect(afterWithdrawal).toBeNull();
    });
});

// Snappy 1.0: Who you know no longer re-reads the contacts status every 60 s.
// The sync announces itself; the safety resync waits 15 minutes and never
// runs while the window is hidden.
describe("Who you know refresh", () => {
    afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

    it("reads the contacts status on its 15-minute resync, not every minute, and not while hidden", async () => {
        vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
        seedCache();
        render(<PeoplePage active />);
        await act(async () => { await vi.advanceTimersByTimeAsync(0); });
        const reads = () => binding("GetContactsAccessStatus").mock.calls.length;
        const mounted = reads();
        await act(async () => { await vi.advanceTimersByTimeAsync(5 * 60_000); });
        expect(reads()).toBe(mounted);
        await act(async () => { await vi.advanceTimersByTimeAsync(PEOPLE_RESYNC_MS); });
        expect(reads()).toBeGreaterThan(mounted);
        const resynced = reads();
        const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
        act(() => { document.dispatchEvent(new Event("visibilitychange")); });
        await act(async () => { await vi.advanceTimersByTimeAsync(PEOPLE_RESYNC_MS * 4); });
        expect(reads()).toBe(resynced);
        visibility.mockRestore();
    });
});


describe("Who you know self read (N52)", () => {
    const empty: PeopleOverview = { ...OVERVIEW, everyone: [], knownCount: 0 };
    const noSelfPrompt = (main: HTMLElement) => {
        expect(within(main).getByRole("heading", { name: "You" })).toBeInTheDocument();
        expect(within(main).queryByRole("button", { name: "Name yourself" })).toBeNull();
        expect(within(main).queryByText(/Name yourself so Isoform/)).toBeNull();
    };

    it("holds the You row while the reads are pending", async () => {
        binding("ListPeopleOverview").mockImplementation(() => new Promise(() => {}));
        binding("ListPrincipalsWithStats").mockImplementation(() => new Promise(() => {}));
        const { container } = render(<PeoplePage initialSelection="self" />);
        noSelfPrompt(mainOf(container));
        expect(within(document.body).queryByRole("dialog")).toBeNull();
    });

    it("holds the You row after the directory fails twice", async () => {
        binding("ListPeopleOverview").mockResolvedValue(empty);
        binding("ListPrincipalsWithStats").mockRejectedValue(new Error("busy"));
        const { container } = render(<PeoplePage initialSelection="self" />);
        await waitFor(() => expect(binding("ListPrincipalsWithStats").mock.calls.length).toBeGreaterThanOrEqual(2));
        noSelfPrompt(mainOf(container));
        expect(within(document.body).queryByRole("dialog")).toBeNull();
    });

    it("retries once and shows the known name after an initial failure", async () => {
        const entry = { principal: { id: "self", kind: "self", displayName: "Mira Vale", aliases: [], appHandles: [] }, stats: { principalId: "self", totalMessages: 0, firstSeenAt: 0, lastSeenAt: 0, byApp: [], recentMessages: [] } };
        binding("ListPeopleOverview").mockResolvedValue(empty);
        binding("ListPrincipalsWithStats").mockRejectedValueOnce(new Error("busy")).mockResolvedValueOnce([entry]);
        const { container } = render(<PeoplePage />);
        noSelfPrompt(mainOf(container));
        expect(await within(mainOf(container)).findByText("Mira Vale · You")).toBeInTheDocument();
        expect(binding("ListPrincipalsWithStats")).toHaveBeenCalledTimes(2);
        noSelfPrompt(mainOf(container));
    });

    it("offers naming only after both reads confirm an empty self", async () => {
        binding("ListPeopleOverview").mockResolvedValue(empty);
        binding("ListPrincipalsWithStats").mockResolvedValue([]);
        const { container } = render(<PeoplePage />);
        expect(await within(mainOf(container)).findByRole("button", { name: "Name yourself" })).toBeInTheDocument();
    });

    it("does not treat a stale cached empty result as a successful revalidation", async () => {
        seedCache(empty);
        pageQueryCache.state("ListPrincipalsWithStats")!.at = Date.now() - 61_000;
        binding("ListPrincipalsWithStats").mockRejectedValue(new Error("busy"));
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        noSelfPrompt(main);
        await waitFor(() => expect(binding("ListPrincipalsWithStats").mock.calls.length).toBeGreaterThanOrEqual(2));
        noSelfPrompt(main);
    });

    it("holds the You row if the overview fails even when the directory is empty", async () => {
        savePeopleView(empty, []);
        binding("ListPeopleOverview").mockRejectedValue(new Error("busy"));
        binding("ListPrincipalsWithStats").mockResolvedValue([]);
        const { container } = render(<PeoplePage />);
        await waitFor(() => expect(binding("ListPeopleOverview").mock.calls.length).toBeGreaterThanOrEqual(2));
        noSelfPrompt(mainOf(container));
    });

    it("withdraws a previous empty answer during and after a failed refresh", async () => {
        binding("ListPeopleOverview").mockResolvedValue(empty);
        const { container } = render(<PeoplePage />);
        const main = mainOf(container);
        expect(await within(main).findByRole("button", { name: "Name yourself" })).toBeInTheDocument();
        binding("ListPrincipalsWithStats").mockRejectedValue(new Error("busy"));
        await act(async () => { pageQueryCache.invalidate(["ListPrincipalsWithStats"]); });
        noSelfPrompt(main);
        await waitFor(() => expect(binding("ListPrincipalsWithStats").mock.calls.length).toBeGreaterThanOrEqual(3));
        noSelfPrompt(main);
    });
});
