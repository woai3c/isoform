import { act, render, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { pageQueryCache } from "@/lib/query-cache";
import { resetPageQueries } from "@/lib/page-queries";
import type { ContactsAccessStatus, PeopleOverview, PersonDetail } from "@/lib/wails-shim-app";

const fixture = vi.hoisted(() => ({ bindings: new Map<string, ReturnType<typeof vi.fn>>() }));
vi.mock("@/lib/wails-shim-app", () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === "then") return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue({}));
        return fixture.bindings.get(name);
    },
}));
vi.mock("../../wailsjs/runtime/runtime", () => ({ EventsOn: () => () => undefined }));

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
