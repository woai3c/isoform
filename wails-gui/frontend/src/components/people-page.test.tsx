import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useRef, useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { PeopleOverview, PersonCard, PersonDetail, PersonLoop, PrincipalDTO, PrincipalWithStatsDTO } from "@/lib/wails-shim-app";

const mocks = vi.hoisted(() => ({
    overview: vi.fn(),
    principals: vi.fn(),
    detail: vi.fn(),
    merge: vi.fn(),
    notSame: vi.fn(),
    undoNotSame: vi.fn(),
    undo: vi.fn(),
    access: vi.fn(),
    unlink: vi.fn(),
    cancel: vi.fn(),
    cleanup: vi.fn(),
    deletePrincipal: vi.fn(),
    closedEyes: vi.fn(),
    sessions: vi.fn(),
    research: vi.fn(),
    voiceStatus: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    ListPeopleOverview: mocks.overview,
    ListPrincipalsWithStats: mocks.principals,
    GetPersonDetail: mocks.detail,
    MergePeople: mocks.merge,
    UndoMerge: mocks.undo,
    NotSamePeople: mocks.notSame,
    UndoNotSamePeople: mocks.undoNotSame,
    GetContactsAccessStatus: mocks.access,
    UnlinkPersonContact: mocks.unlink,
    CancelContactsSync: mocks.cancel,
    CleanupAnonymousPrincipals: mocks.cleanup,
    DeletePrincipal: mocks.deletePrincipal,
    IsClosedEyesModeEnabled: mocks.closedEyes,
    DeletePersonResearch: vi.fn(),
    DeleteVoiceSample: vi.fn(),
    EnrollVoiceSample: vi.fn(),
    GetPersonResearch: mocks.research,
    GetVoiceEnrollmentStatus: mocks.voiceStatus,
    ListPrincipalActivitySessions: mocks.sessions,
    ListVoiceSamples: vi.fn().mockResolvedValue([]),
    StartPersonResearch: vi.fn(),
    TestVoiceMatch: vi.fn(),
}));

// The page query cache installs its own invalidation listeners for some of
// the same events the page listens to; the mock keeps every handler, as the
// real runtime does.
const events = vi.hoisted(() => ({ handlers: {} as Record<string, Set<(payload?: unknown) => void>> }));
vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: vi.fn((name: string, handler: (payload?: unknown) => void) => {
        const set = events.handlers[name] ?? new Set();
        set.add(handler);
        events.handlers[name] = set;
        return () => set.delete(handler);
    }),
}));

async function emitEvent(name: string, payload?: unknown) {
    await act(async () => {
        for (const handler of events.handlers[name] ?? []) await handler(payload);
    });
}

// The dialogs are their own components; here they only say what opened.
vi.mock("./contacts/contact-editor", () => ({
    ContactEditorDialog: ({ mode, open, initial, onSaved }: { mode: string; open: boolean; initial?: PrincipalDTO; onSaved?: (saved: PrincipalDTO) => void }) =>
        open ? (
            <div data-testid="contact-editor">
                {`${mode}:${initial?.id ?? ""}`}
                <button type="button" onClick={() => onSaved?.({ id: initial?.id ?? "new-person", displayName: "New Person" } as PrincipalDTO)}>
                    fixture save
                </button>
            </div>
        ) : null,
}));
vi.mock("./contacts/principal-picker", () => ({
    PrincipalPicker: ({ onSelect }: { onSelect: (p: PrincipalDTO) => void }) => (
        <button type="button" onClick={() => onSelect({ id: "sam", displayName: "Sam Rivera" } as PrincipalDTO)}>
            Pick Sam
        </button>
    ),
}));
vi.mock("./contacts/contacts-import-dialog", () => ({
    ContactsImportDialog: ({ open }: { open: boolean }) => (open ? <div data-testid="contacts-import" /> : null),
}));
vi.mock("./contacts/contact-link-dialog", () => ({
    ContactLinkDialog: ({ personID }: { personID: string }) => <div data-testid="contact-link">{personID}</div>,
}));

import { PeoplePage } from "./people-page";
import { resetPageQueries } from "@/lib/page-queries";
import { Dialog, DialogClose, DialogTitle } from "./ui/dialog";
import { RetainedDialogContent } from "./retained-dialog-content";

// The Who you know dialog as App wires it: Radix asks only the top layer about
// Escape, and the dialog asks the page before it closes.
function PeopleDialog() {
    const [open, setOpen] = useState(true);
    const escapeRef = useRef<(() => boolean) | null>(null);
    return (
        <>
            <output data-testid="dialog-state">{open ? "open" : "closed"}</output>
            <Dialog open={open} onOpenChange={setOpen}>
                <RetainedDialogContent
                    open={open}
                    retain={false}
                    onEscapeKeyDown={(e) => {
                        if (escapeRef.current?.()) e.preventDefault();
                    }}
                >
                    <DialogTitle>Who you know</DialogTitle>
                    <DialogClose asChild>
                        <button type="button" aria-label="Close" />
                    </DialogClose>
                    <PeoplePage escapeRef={escapeRef} />
                </RetainedDialogContent>
            </Dialog>
        </>
    );
}

// Tests that open several Radix menus and dialogs run slower on a busy machine.
const MENU_TEST_TIMEOUT = 15_000;
const NOW = Date.now();
const HOUR = 3_600_000;

function entry(id: string, kind: string, displayName: string, extra: Partial<PrincipalDTO> = {}, stats: Partial<PrincipalWithStatsDTO["stats"]> = {}): PrincipalWithStatsDTO {
    return {
        principal: { id, kind, displayName, relationship: "", notes: "", aliases: [], appHandles: [], ...extra } as PrincipalDTO,
        stats: { principalId: id, totalMessages: 0, firstSeenAt: 0, lastSeenAt: 0, byApp: [], recentMessages: [], ...stats },
    } as PrincipalWithStatsDTO;
}

function loop(kind: PersonLoop["kind"], extra: Partial<PersonLoop> = {}): PersonLoop {
    return { id: `${kind}-1`, kind, since: NOW - 9 * HOUR, title: "", dueAt: 0, ...extra };
}

function card(id: string, kind: string, name: string, loops: PersonLoop[] = [], extra: Partial<PersonCard> = {}): PersonCard {
    return { id, kind, name, relationship: "", photo: "", loops, lastSeenAt: 0, lastSeenApp: "", ...extra };
}

function detail(id: string, name: string, extra: Partial<PersonDetail> = {}): PersonDetail {
    return { id, name, relationship: "", notes: "", researchSummary: "", loops: [], together: [], sharedTopics: [], seenIn: [], messageCount: 0, ...extra };
}

const priyaLoops = [loop("reply_owed")];
const marcusLoops = [loop("you_owe", { id: "sow", title: "Countersign the SOW" })];

function world() {
    const entries = [
        entry("priya", "contact", "Priya Nair", { relationship: "colleague", aliases: ["P. Nair"], notes: "Prefers Slack." }, {
            totalMessages: 4,
            recentMessages: [{ app: "slack", seenAt: NOW - HOUR, preview: "Can we move the sync to Oct 30?" }] as never,
            byApp: [{ app: "slack", messageCount: 4, lastSeenAt: NOW - HOUR }] as never,
        }),
        entry("marcus", "contact", "Marcus Hale"),
        entry("sam", "contact", "Sam Rivera", { appHandles: [{ id: 7, app: "slack", handleId: "U0SAM42", displayHandle: "sam.r" }] as never }),
        entry("mei1", "anonymous", "Mei Chen ex-Stability", {}, { totalMessages: 12 }),
        entry("mei2", "anonymous", "陈美 Mei", {}, { totalMessages: 3 }),
        entry("me", "self", "Bo Yu"),
    ];
    const overview: PeopleOverview = {
        headline: { repliesOwed: 1, thingsOwed: 1, peopleWaitingOnYou: 1 },
        open: [
            card("priya", "contact", "Priya Nair", priyaLoops, { relationship: "colleague", lastSeenAt: NOW - HOUR, lastSeenApp: "slack" }),
            card("marcus", "contact", "Marcus Hale", marcusLoops),
        ],
        everyone: [
            card("sam", "contact", "Sam Rivera", [], { lastSeenAt: NOW - 30 * 24 * HOUR, lastSeenApp: "email" }),
            card("mei1", "anonymous", "Mei Chen ex-Stability"),
            card("mei2", "anonymous", "陈美 Mei"),
            card("me", "self", "Bo Yu"),
        ],
        duplicates: [{ id: "dup-1", memberIds: ["mei1", "mei2"], suggestedTargetId: "mei1", reason: "same_handle", app: "wechat" }],
        knownCount: entries.length,
    };
    const details: Record<string, PersonDetail> = {
        priya: detail("priya", "Priya Nair", {
            relationship: "colleague",
            researchSummary: "Your day-to-day contact at Halcyon.",
            loops: priyaLoops,
            together: [{ at: NOW - 5 * 24 * HOUR, kind: "meeting", title: "Kickoff call", app: "calendar", durationMs: 45 * 60_000 }],
            sharedTopics: [{ id: "t1", name: "Halcyon ERP sync" }],
            seenIn: ["email", "slack"],
        }),
        marcus: detail("marcus", "Marcus Hale", { loops: marcusLoops }),
        sam: detail("sam", "Sam Rivera"),
        mei1: detail("mei1", "Mei Chen ex-Stability"),
        mei2: detail("mei2", "陈美 Mei"),
        me: detail("me", "Bo Yu"),
    };
    mocks.overview.mockResolvedValue(overview);
    mocks.principals.mockResolvedValue(entries);
    mocks.detail.mockImplementation(async (id: string) => details[id]);
    return { overview, entries, details };
}

function openMenu(name: string | RegExp) {
    fireEvent.pointerDown(screen.getByRole("button", { name }), { button: 0, pointerType: "mouse" });
}

async function openPerson(name: string) {
    fireEvent.click(await screen.findByRole("button", { name: new RegExp(name) }));
    return screen.findByRole("heading", { level: 2, name });
}

beforeEach(async () => {
    vi.clearAllMocks();
    // The page's reads go through the shared page query cache; each test
    // starts cold so one test's reads never paint the next.
    resetPageQueries();
    mocks.access.mockResolvedValue(null);
    mocks.closedEyes.mockResolvedValue(false);
    mocks.sessions.mockResolvedValue([]);
    mocks.research.mockResolvedValue(null);
    mocks.voiceStatus.mockResolvedValue(null);
    mocks.unlink.mockResolvedValue(undefined);
    mocks.cancel.mockResolvedValue(undefined);
    await i18n.changeLanguage("en");
});

describe("People overview", () => {
    it("says what you owe and orders people by what is open", async () => {
        world();
        render(<PeoplePage />);

        expect(await screen.findByRole("heading", { level: 1, name: "You owe 1 reply, and 1 person is waiting on you" })).toBeInTheDocument();
        const open = screen.getByRole("region", { name: "Open between you" });
        const cards = within(open).getAllByRole("button");
        expect(cards.map((c) => c.textContent)).toEqual([expect.stringContaining("Priya Nair"), expect.stringContaining("Marcus Hale")]);
        expect(cards[0]).toHaveTextContent("You owe a reply · 9 h");
        expect(cards[0]).toHaveTextContent("Last: Slack,");
        expect(cards[1]).toHaveTextContent("You owe them");
        expect(cards[1]).toHaveTextContent("Countersign the SOW");
        const everyone = screen.getByRole("region", { name: "Everyone else" });
        expect(within(everyone).getByRole("button", { name: /Sam Rivera/ })).toBeInTheDocument();
        expect(within(everyone).queryByRole("button", { name: /Mei Chen/ })).not.toBeInTheDocument();
    });

    it("opens on the person Search asked for, and again on the next one", async () => {
        world();
        const { rerender } = render(<PeoplePage initialSelection={{ personId: "sam" }} />);
        expect(await screen.findByRole("heading", { level: 2, name: "Sam Rivera" })).toBeInTheDocument();
        expect(mocks.detail).toHaveBeenCalledWith("sam");
        // Retained page, new request: it moves to that person.
        rerender(<PeoplePage initialSelection={{ personId: "priya" }} />);
        expect(await screen.findByRole("heading", { level: 2, name: "Priya Nair" })).toBeInTheDocument();
    });

    it("falls back to the first open person when Search's person is gone", async () => {
        world();
        render(<PeoplePage initialSelection={{ personId: "merged-away" }} />);
        expect(await screen.findByRole("heading", { level: 2, name: "Priya Nair" })).toBeInTheDocument();
    });

    it("opens on the first open person with the detail from GetPersonDetail", async () => {
        const { details } = world();
        let releaseDetail!: () => void;
        mocks.detail.mockReturnValue(new Promise<PersonDetail>((resolve) => {
            releaseDetail = () => resolve(details.priya);
        }));
        render(<PeoplePage />);

        expect(await screen.findByRole("heading", { level: 2, name: "Priya Nair" })).toBeInTheDocument();
        expect(mocks.detail).toHaveBeenCalledWith("priya");
        const aside = screen.getByRole("complementary", { name: "Person detail" });
        queueMicrotask(releaseDetail);
        expect(await within(aside).findByText("Your day-to-day contact at Halcyon.")).toBeInTheDocument();
        expect(within(aside).getByText("Their email has waited 9 h for your reply")).toBeInTheDocument();
        expect(within(aside).getByText("Kickoff call · 45 min")).toBeInTheDocument();
        expect(within(aside).getByText("Halcyon ERP sync")).toBeInTheDocument();
        expect(within(aside).getByText("Seen in Email and Slack")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Priya Nair/, pressed: true })).toBeInTheDocument();
    });

    it("shows grey lines while loading and a retry when the first read fails", async () => {
        world();
        mocks.overview.mockRejectedValue(new Error("boom"));
        render(<PeoplePage />);
        expect(document.querySelector('[aria-busy="true"]')).not.toBeNull();

        expect(await screen.findByText("Could not load Who you know.")).toBeInTheDocument();
        // Each attempt is tried twice quietly before the page says so: the
        // mount read, plus the re-read the principals publication triggers.
        expect(mocks.overview).toHaveBeenCalledTimes(4);
        world();
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findByRole("heading", { level: 1, name: /You owe 1 reply/ })).toBeInTheDocument();
    });

    it("never lets a slower older read overwrite a newer one", async () => {
        const { overview } = world();
        let releaseOld!: (value: PeopleOverview) => void;
        mocks.overview.mockImplementationOnce(() => new Promise<PeopleOverview>((resolve) => (releaseOld = resolve)));
        render(<PeoplePage />);
        await waitFor(() => expect(events.handlers["contacts:updated"]?.size ?? 0).toBeGreaterThan(0));
        // A Contacts sync reads again while the first read is still out.
        await emitEvent("contacts:updated");
        expect(await screen.findByRole("heading", { level: 1, name: /You owe 1 reply/ })).toBeInTheDocument();
        await act(async () => releaseOld({ ...overview, headline: { repliesOwed: 5, thingsOwed: 0, peopleWaitingOnYou: 0 } }));
        expect(screen.getByRole("heading", { level: 1, name: /You owe 1 reply/ })).toBeInTheDocument();
    });

    it("reloads open loops when a send changes who is waiting", async () => {
        const { overview, details } = world();
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        const waiting = loop("waiting_on_them", { title: "Thursday works for me" });
        mocks.overview.mockResolvedValue({
            ...overview,
            headline: { repliesOwed: 0, thingsOwed: 1, peopleWaitingOnYou: 0 },
            open: [card("priya", "contact", "Priya Nair", [waiting]), overview.open[1]],
        });
        details.priya = { ...details.priya, loops: [waiting] };

        await emitEvent("people:loops-changed");

        const open = screen.getByRole("region", { name: "Open between you" });
        await waitFor(() => expect(within(open).getByText("Waiting on them")).toBeInTheDocument());
        expect(mocks.overview).toHaveBeenCalledTimes(2);
    });

    it("says the detail failed in words with a way to try again", async () => {
        world();
        mocks.detail.mockRejectedValue(new Error("boom"));
        render(<PeoplePage />);
        expect(await screen.findByText("Could not load this person.")).toBeInTheDocument();
        mocks.detail.mockResolvedValue(detail("priya", "Priya Nair"));
        fireEvent.click(within(screen.getByRole("complementary")).getByRole("button", { name: "Try again" }));
        expect(await screen.findByRole("heading", { level: 2, name: "Priya Nair" })).toBeInTheDocument();
    });

    it("keeps a person's actions when their detail fails to load", async () => {
        world();
        mocks.detail.mockRejectedValue(new Error("read changed"));
        mocks.deletePrincipal.mockResolvedValue(undefined);
        render(<PeoplePage />);
        expect(await screen.findByText("Could not load this person.")).toBeInTheDocument();
        expect(screen.getByRole("heading", { level: 2, name: "Priya Nair" })).toBeInTheDocument();
        openMenu("More actions for Priya Nair");
        fireEvent.click(await screen.findByText("Edit person"));
        expect(screen.getByTestId("contact-editor")).toHaveTextContent("edit:priya");
        openMenu("More actions for Priya Nair");
        fireEvent.click(await screen.findByText("Delete person"));
        fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
        await waitFor(() => expect(mocks.deletePrincipal).toHaveBeenCalledWith("priya"));
    }, MENU_TEST_TIMEOUT);

    it("says when Isoform has seen no messages from a person or from you", async () => {
        world();
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        expect(screen.queryByText(/hasn't seen messages from this person/)).not.toBeInTheDocument();
        await openPerson("Marcus Hale");
        expect(await screen.findByText("Isoform hasn't seen messages from this person in any chat yet. Make sure their handle bindings cover the apps they use.")).toBeInTheDocument();
        await openPerson("Bo Yu");
        expect(await screen.findByText(/Isoform hasn't seen any chat messages attributed to you yet/)).toBeInTheDocument();
    });

    it("keeps the empty Contacts hint, and no sort-out line when nobody is unidentified", async () => {
        const { entries, overview } = world();
        mocks.principals.mockResolvedValue(entries.filter((e) => e.principal.kind === "self"));
        mocks.overview.mockResolvedValue({ ...overview, open: [], duplicates: [], everyone: overview.everyone.filter((c) => c.kind === "self"), knownCount: 1 });
        render(<PeoplePage />);
        const everyone = await screen.findByRole("region", { name: "Everyone else" });
        expect(everyone).toHaveTextContent("Add people you chat with regularly so their messages aren't mistaken for yours.");
        expect(screen.queryByText(/Isoform couldn't tell who/)).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Sort them out" })).not.toBeInTheDocument();
    });

    it("says open loops are hidden while observing is paused", async () => {
        world();
        mocks.closedEyes.mockResolvedValue(true);
        render(<PeoplePage />);
        expect(await screen.findByRole("heading", { level: 1, name: "Observing is paused, so what’s open between you is hidden" })).toBeInTheDocument();
    });
});

// The list shows only people Isoform can name. Unidentified senders leave for
// the sort-out sheet; people seen only in content leave the list and stay
// in search.
describe("Who you know lists only people Isoform can name", () => {
    it("never shows an Unidentified or Seen in content group", async () => {
        const { entries, overview } = world();
        const ana = entry("ana", "content_subject", "Ana Lopez", {}, { totalMessages: 2 });
        mocks.principals.mockResolvedValue([...entries, ana]);
        mocks.overview.mockResolvedValue({
            ...overview,
            everyone: [...overview.everyone, card("ana", "content_subject", "Ana Lopez")],
            knownCount: entries.length + 1,
        });
        render(<PeoplePage />);
        const everyone = await screen.findByRole("region", { name: "Everyone else" });

        expect(screen.queryByRole("region", { name: "Unidentified" })).not.toBeInTheDocument();
        expect(screen.queryByRole("region", { name: "Seen in content" })).not.toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: /Unidentified|Seen in content/ })).not.toBeInTheDocument();
        expect(within(everyone).getByRole("button", { name: /Sam Rivera/ })).toBeInTheDocument();
        expect(within(everyone).queryByRole("button", { name: /Ana Lopez/ })).not.toBeInTheDocument();
        expect(within(everyone).queryByRole("button", { name: /Mei Chen/ })).not.toBeInTheDocument();

        // One line under the list says how many senders are left to sort out.
        expect(screen.getByText("Isoform couldn't tell who 2 senders are.")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Sort them out" })).toBeInTheDocument();

        // The person seen only in content is still found by search.
        fireEvent.change(screen.getByRole("searchbox", { name: "Search people" }), { target: { value: "ana" } });
        expect(within(screen.getByRole("region", { name: "Results" })).getByRole("button", { name: /Ana Lopez/ })).toBeInTheDocument();
    });

    it("keeps an unidentified sender with an open loop in Open, and sorts out only the rest", async () => {
        const { overview } = world();
        mocks.overview.mockResolvedValue({
            ...overview,
            headline: { repliesOwed: 2, thingsOwed: 1, peopleWaitingOnYou: 1 },
            open: [...overview.open, card("mei2", "anonymous", "陈美 Mei", [loop("reply_owed")])],
            everyone: overview.everyone.filter((c) => c.id !== "mei2"),
            duplicates: [],
        });
        render(<PeoplePage />);
        const open = await screen.findByRole("region", { name: "Open between you" });
        expect(within(open).getByRole("button", { name: /陈美 Mei.*Unidentified · 3 messages/ })).toBeInTheDocument();
        expect(within(open).getByRole("button", { name: /Priya Nair/ })).toBeInTheDocument();
        expect(screen.queryByRole("region", { name: "Unidentified" })).not.toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: /Unidentified|Seen in content/ })).not.toBeInTheDocument();

        // Only the sender with no open loop is left to sort out.
        expect(screen.getByText("Isoform couldn't tell who 1 sender is.")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Sort them out" }));
        const sheet = await screen.findByRole("dialog", { name: "Senders to sort out" });
        expect(within(sheet).getByRole("button", { name: /Mei Chen ex-Stability/ })).toBeInTheDocument();
        expect(within(sheet).queryByRole("button", { name: /陈美 Mei/ })).not.toBeInTheDocument();
    });

    it("keeps a person seen only in content in Open when something is open with them", async () => {
        const { entries, overview } = world();
        const ana = entry("ana", "content_subject", "Ana Lopez", {}, { totalMessages: 2 });
        mocks.principals.mockResolvedValue([...entries, ana]);
        mocks.overview.mockResolvedValue({
            ...overview,
            open: [...overview.open, card("ana", "content_subject", "Ana Lopez", [loop("you_owe", { id: "deck", title: "Send the deck" })])],
            knownCount: entries.length + 1,
        });
        render(<PeoplePage />);
        const open = await screen.findByRole("region", { name: "Open between you" });
        expect(within(open).getByRole("button", { name: /Ana Lopez/ })).toHaveTextContent("Send the deck");
        expect(screen.queryByRole("heading", { name: /Unidentified|Seen in content/ })).not.toBeInTheDocument();
        // Only the two unidentified senders with no open loop are left to sort out.
        expect(screen.getByText("Isoform couldn't tell who 2 senders are.")).toBeInTheDocument();
    });

    it("lists unidentified senders in a sheet, and a sender opens its detail beside the list", async () => {
        world();
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        expect(screen.queryByRole("dialog", { name: "Senders to sort out" })).not.toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Sort them out" }));
        const sheet = await screen.findByRole("dialog", { name: "Senders to sort out" });
        expect(within(sheet).getByRole("button", { name: /Mei Chen ex-Stability.*Unidentified · 12 messages/ })).toBeInTheDocument();
        expect(within(sheet).getByRole("button", { name: /陈美 Mei/ })).toBeInTheDocument();
        expect(within(sheet).queryByRole("button", { name: /Sam Rivera/ })).not.toBeInTheDocument();

        fireEvent.click(within(sheet).getByRole("button", { name: /Mei Chen ex-Stability/ }));
        expect(await screen.findByRole("heading", { level: 2, name: "Mei Chen ex-Stability" })).toBeInTheDocument();
        await waitFor(() => expect(screen.queryByRole("dialog", { name: "Senders to sort out" })).not.toBeInTheDocument());
    });

    it("the sheet's cleanup runs the same clean-up as the page menu", async () => {
        world();
        mocks.cleanup.mockResolvedValue({ scanned: 2, renamed: 0, merged: 0, crossAppMerged: 0, mergedIntoNamed: 0, prunedNonChat: 0, prunedImplausible: 0 });
        render(<PeoplePage />);
        fireEvent.click(await screen.findByRole("button", { name: "Sort them out" }));
        const sheet = await screen.findByRole("dialog", { name: "Senders to sort out" });
        fireEvent.click(within(sheet).getByRole("button", { name: "Clean up 2 unidentified senders" }));
        fireEvent.click(await screen.findByRole("button", { name: "Clean up" }));
        await waitFor(() => expect(mocks.cleanup).toHaveBeenCalledTimes(1));
    });
});

describe("Prepared requests", () => {
    it("Brief me and Draft a reply put words in the main chat box and send nothing", async () => {
        world();
        const onPrepareChat = vi.fn();
        render(<PeoplePage onPrepareChat={onPrepareChat} />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });

        fireEvent.click(screen.getByRole("button", { name: "Brief me before we talk" }));
        expect(onPrepareChat).toHaveBeenLastCalledWith(expect.stringContaining("Brief me before I talk with Priya Nair"));
        fireEvent.click(screen.getByRole("button", { name: "Draft a reply" }));
        expect(onPrepareChat).toHaveBeenLastCalledWith(expect.stringContaining("Draft a reply to Priya Nair"));
        expect(onPrepareChat).toHaveBeenCalledTimes(2);
        // Nothing else ran: no merge, delete or other write.
        expect(mocks.merge).not.toHaveBeenCalled();
        expect(mocks.deletePrincipal).not.toHaveBeenCalled();
    });

    it("are not offered on your own profile", async () => {
        world();
        render(<PeoplePage onPrepareChat={vi.fn()} initialSelection="self" />);
        expect(await screen.findByRole("heading", { level: 2, name: "Bo Yu" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Brief me before we talk" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Draft a reply" })).not.toBeInTheDocument();
    });
});

describe("Merge", () => {
    it("merges a duplicate group into one and undoes it", async () => {
        world();
        mocks.merge.mockResolvedValue({ mergeId: "m-1", target: card("mei1", "anonymous", "Mei Chen ex-Stability") });
        mocks.undo.mockResolvedValue(undefined);
        render(<PeoplePage />);

        const banner = await screen.findByRole("region", { name: "2 profiles look like one person: Mei Chen ex-Stability" });
        expect(banner).toHaveTextContent("Same WeChat account under different display names: “Mei Chen ex-Stability” and “陈美 Mei”.");
        fireEvent.click(within(banner).getByRole("button", { name: "Merge into one" }));
        await waitFor(() => expect(mocks.merge).toHaveBeenCalledWith("mei1", ["mei2"]));
        expect(await screen.findByText("Merged 2 profiles into Mei Chen ex-Stability.")).toBeInTheDocument();
        expect(await screen.findByRole("heading", { level: 2, name: "Mei Chen ex-Stability" })).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Undo" }));
        await waitFor(() => expect(mocks.undo).toHaveBeenCalledWith("m-1"));
        await waitFor(() => expect(screen.queryByText("Merged 2 profiles into Mei Chen ex-Stability.")).not.toBeInTheDocument());
    });

    it("says a failed merge changed nothing", async () => {
        world();
        mocks.merge.mockRejectedValue(new Error("busy"));
        render(<PeoplePage />);
        fireEvent.click(await screen.findByRole("button", { name: "Merge into one" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not merge these profiles. Nothing changed. Try again.");
    });

    it("keeps the store's reason when a linked profile blocks the merge", async () => {
        world();
        mocks.merge.mockRejectedValue("unlink the contact before merging these people");
        render(<PeoplePage />);
        fireEvent.click(await screen.findByRole("button", { name: "Merge into one" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("One of these profiles is linked to a contact.");
    });

    it("Review each lists the profiles and opens one", async () => {
        world();
        render(<PeoplePage />);
        const banner = await screen.findByRole("region", { name: /look like one person/ });
        fireEvent.click(within(banner).getByRole("button", { name: "Review each" }));
        fireEvent.click(within(banner).getByRole("button", { name: /陈美 Mei/ }));
        expect(await screen.findByRole("heading", { level: 2, name: "陈美 Mei" })).toBeInTheDocument();
    });

    it("the person's own Merge uses the reversible merge", async () => {
        world();
        mocks.merge.mockResolvedValue({ mergeId: "m-2", target: card("sam", "contact", "Sam Rivera") });
        render(<PeoplePage />);
        await openPerson("Marcus Hale");
        openMenu("More actions for Marcus Hale");
        fireEvent.click(await screen.findByText("Merge this person into another"));
        fireEvent.click(await screen.findByRole("button", { name: "Pick Sam" }));
        fireEvent.click(screen.getByRole("button", { name: "Merge" }));
        await waitFor(() => expect(mocks.merge).toHaveBeenCalledWith("sam", ["marcus"]));
        expect(await screen.findByText("Merged 2 profiles into Sam Rivera.")).toBeInTheDocument();
    }, MENU_TEST_TIMEOUT);
});

// Decision 11: nothing the page had before RC 2 disappears. Each of these
// stays one click away in the page More menu, a person's More menu or the
// detail's folded sections.
describe("Everything stays reachable", () => {
    it("search finds people by alias and chat handle", async () => {
        world();
        render(<PeoplePage />);
        const search = await screen.findByRole("searchbox", { name: "Search people" });
        fireEvent.change(search, { target: { value: "p. nair" } });
        const results = screen.getByRole("region", { name: "Results" });
        expect(within(results).getByRole("button", { name: /Priya Nair/ })).toBeInTheDocument();
        fireEvent.change(search, { target: { value: "sam.r" } });
        expect(within(screen.getByRole("region", { name: "Results" })).getByRole("button", { name: /Sam Rivera/ })).toBeInTheDocument();
        fireEvent.change(search, { target: { value: "nobody here" } });
        expect(screen.getByText("No one matches “nobody here”.")).toBeInTheDocument();
    });

    it("Escape closes a menu alone, then clears search, then closes the dialog", async () => {
        world();
        render(<PeopleDialog />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        openMenu("More Who you know actions");
        fireEvent.keyDown(await screen.findByRole("menuitem", { name: "Refresh" }), { key: "Escape" });
        await waitFor(() => expect(screen.queryByRole("menuitem", { name: "Refresh" })).not.toBeInTheDocument());
        expect(screen.getByTestId("dialog-state")).toHaveTextContent("open");

        const search = screen.getByRole("searchbox", { name: "Search people" });
        fireEvent.change(search, { target: { value: "sam" } });
        fireEvent.keyDown(search, { key: "Escape" });
        expect(search).toHaveValue("");
        expect(screen.getByTestId("dialog-state")).toHaveTextContent("open");

        fireEvent.keyDown(search, { key: "Escape" });
        expect(screen.getByTestId("dialog-state")).toHaveTextContent("closed");
    }, MENU_TEST_TIMEOUT);

    it("Escape closes the dialog with focus on its Close button", async () => {
        world();
        render(<PeopleDialog />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        const close = screen.getByRole("button", { name: "Close" });
        close.focus();
        fireEvent.keyDown(close, { key: "Escape" });
        expect(screen.getByTestId("dialog-state")).toHaveTextContent("closed");
    });

    it("keeps the You group", async () => {
        world();
        render(<PeoplePage />);
        const you = await screen.findByRole("region", { name: "You" });
        expect(within(you).getByRole("button", { name: /Bo Yu/ })).toBeInTheDocument();
    });

    it("offers to name you when there is no profile for you yet", async () => {
        const { entries, overview } = world();
        mocks.principals.mockResolvedValue(entries.filter((e) => e.principal.kind !== "self"));
        mocks.overview.mockResolvedValue({ ...overview, everyone: overview.everyone.filter((c) => c.kind !== "self") });
        render(<PeoplePage initialSelection="self" />);
        expect(await screen.findByTestId("contact-editor")).toHaveTextContent("self");
        expect(screen.getByRole("button", { name: "Name yourself" })).toBeInTheDocument();
    });

    it("the page More menu adds a person, opens Contacts, cleans up and refreshes", async () => {
        world();
        mocks.cleanup.mockResolvedValue({ scanned: 2, renamed: 0, merged: 0, crossAppMerged: 0, mergedIntoNamed: 0, prunedNonChat: 0, prunedImplausible: 0 });
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });

        openMenu("More Who you know actions");
        fireEvent.click(await screen.findByText("Add a person"));
        expect(screen.getByTestId("contact-editor")).toHaveTextContent("create");

        openMenu("More Who you know actions");
        fireEvent.click(await screen.findByText("Contacts on this computer"));
        expect(screen.getByTestId("contacts-import")).toBeInTheDocument();

        openMenu("More Who you know actions");
        fireEvent.click(await screen.findByText("Clean up 2 unidentified senders"));
        fireEvent.click(await screen.findByRole("button", { name: "Clean up" }));
        await waitFor(() => expect(mocks.cleanup).toHaveBeenCalled());

        const reads = mocks.overview.mock.calls.length;
        openMenu("More Who you know actions");
        fireEvent.click(await screen.findByText("Refresh"));
        await waitFor(() => expect(mocks.overview.mock.calls.length).toBeGreaterThan(reads));
    }, MENU_TEST_TIMEOUT);

    it("a person's More menu edits, links, unlinks and deletes", async () => {
        const { entries } = world();
        entries[2].principal.contactLinked = true;
        mocks.deletePrincipal.mockResolvedValue(undefined);
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });

        openMenu("More actions for Priya Nair");
        fireEvent.click(await screen.findByText("Edit person"));
        expect(screen.getByTestId("contact-editor")).toHaveTextContent("edit:priya");

        openMenu("More actions for Priya Nair");
        fireEvent.click(await screen.findByText("Link to contact"));
        expect(screen.getByTestId("contact-link")).toHaveTextContent("priya");

        openMenu("More actions for Priya Nair");
        fireEvent.click(await screen.findByText("Delete person"));
        fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
        await waitFor(() => expect(mocks.deletePrincipal).toHaveBeenCalledWith("priya"));

        await openPerson("Sam Rivera");
        openMenu("More actions for Sam Rivera");
        expect(screen.queryByText("Merge this person into another")).not.toBeInTheDocument();
        fireEvent.click(await screen.findByText("Unlink from contact"));
        await waitFor(() => expect(mocks.unlink).toHaveBeenCalledWith("sam"));
    }, MENU_TEST_TIMEOUT);

    it("an unidentified sender can be named from the sort-out sheet", async () => {
        world();
        render(<PeoplePage />);
        fireEvent.click(await screen.findByRole("button", { name: "Sort them out" }));
        const sheet = await screen.findByRole("dialog", { name: "Senders to sort out" });
        fireEvent.click(within(sheet).getByRole("button", { name: /陈美 Mei/ }));
        await screen.findByRole("heading", { level: 2, name: "陈美 Mei" });
        openMenu("More actions for 陈美 Mei");
        fireEvent.click(await screen.findByText("Name this person"));
        expect(screen.getByTestId("contact-editor")).toHaveTextContent("promote:mei2");
    });

    it("the detail keeps aliases, notes, relationship, previews, handles, sessions and the dossier", async () => {
        world();
        render(<PeoplePage />);
        const aside = await screen.findByRole("complementary", { name: "Person detail" });
        await within(aside).findByRole("heading", { level: 2, name: "Priya Nair" });
        expect(within(aside).getByText("Colleague")).toBeInTheDocument();
        expect(within(aside).getByText("Also known as P. Nair")).toBeInTheDocument();
        expect(within(aside).getByText("Prefers Slack.")).toBeInTheDocument();

        fireEvent.click(within(aside).getByRole("button", { name: /Recent messages/ }));
        expect(within(aside).getByText("Can we move the sync to Oct 30?")).toBeInTheDocument();

        fireEvent.click(within(aside).getByRole("button", { name: /Activity sessions/ }));
        await waitFor(() => expect(mocks.sessions).toHaveBeenCalledWith("priya", 50));

        fireEvent.click(within(aside).getByRole("button", { name: /Research dossier/ }));
        await waitFor(() => expect(mocks.research).toHaveBeenCalledWith("priya"));
        expect(within(aside).getByRole("button", { name: "Research" })).toBeInTheDocument();

        fireEvent.click(within(aside).getByRole("button", { name: /Technical details/ }));
        expect(within(aside).getByText("Person ID")).toBeInTheDocument();
        expect(within(aside).getByText("Where Isoform saw them")).toBeInTheDocument();

        await openPerson("Sam Rivera");
        fireEvent.click(within(aside).getByRole("button", { name: /Technical details/ }));
        expect(within(aside).getByText("Chat handles")).toBeInTheDocument();
        expect(within(aside).getByText("sam.r")).toBeInTheDocument();
    }, MENU_TEST_TIMEOUT);

    it("your own profile keeps the voice fingerprint", async () => {
        world();
        render(<PeoplePage initialSelection="self" />);
        await screen.findByRole("heading", { level: 2, name: "Bo Yu" });
        expect(screen.getByRole("button", { name: /Voice fingerprint/ })).toHaveAttribute("aria-expanded", "true");
        await waitFor(() => expect(mocks.voiceStatus).toHaveBeenCalledWith("me"));
        expect(screen.getByText("Not enrolled")).toBeInTheDocument();
    });
});

describe("People contacts", () => {
    beforeEach(() => {
        mocks.access.mockResolvedValue({ supported: true, consentAccepted: true, permission: "granted", syncState: "idle", lastImportAt: new Date(new Date().setHours(8, 0, 0, 0)).toISOString() });
    });

    it("shows a quiet header status and no manual import button", async () => {
        world();
        render(<PeoplePage />);
        expect(await screen.findByRole("status")).toHaveTextContent("Contacts synced this morning");
        expect(screen.queryByRole("button", { name: "Import from Contacts" })).not.toBeInTheDocument();
    });

    it("shows the contact name and photo and hides the identifier", async () => {
        const { entries } = world();
        const person = entries[2];
        person.principal.contactName = "Maya Chen";
        person.principal.contactPhoto = "data:image/jpeg;base64,aGVsbG8=";
        person.principal.contactLinked = true;
        person.principal.appHandles = [{ id: 1, app: "contacts", handleId: "synthetic-native-7F3A9C", displayHandle: "synthetic-native-7F3A9C" }] as never;
        const { container } = render(<PeoplePage />);
        expect(await openPerson("Maya Chen")).toBeInTheDocument();
        expect(container.querySelectorAll("img").length).toBeGreaterThanOrEqual(2);
        expect(container.innerHTML).not.toContain("synthetic-native-7F3A9C");
    });

    it("allows cancellation while syncing without a progress dialog", async () => {
        world();
        mocks.access.mockResolvedValue({ consentAccepted: true, permission: "granted", syncState: "syncing" });
        render(<PeoplePage />);
        expect(await screen.findByRole("status")).toHaveTextContent("Syncing Contacts quietly");
        fireEvent.click(screen.getByRole("button", { name: "Cancel Contacts sync" }));
        await waitFor(() => expect(mocks.cancel).toHaveBeenCalled());
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    it("does not ask for native access on a computer without Contacts sync", async () => {
        world();
        mocks.access.mockResolvedValue({ supported: false, permission: "not_applicable", consentAccepted: true });
        render(<PeoplePage />);
        expect(await screen.findByRole("status")).toHaveTextContent("Contacts sync is not available on this computer.");
    });
});

describe("Chinese", () => {
    it("localizes the headline, sections and the contacts status", async () => {
        await i18n.changeLanguage("zh-CN");
        world();
        mocks.access.mockResolvedValue({ supported: true, consentAccepted: true, permission: "granted", syncState: "idle", lastImportAt: new Date(new Date().setHours(8, 0, 0, 0)).toISOString() });
        render(<PeoplePage onPrepareChat={vi.fn()} />);
        expect(await screen.findByRole("heading", { level: 1, name: "你欠 1 条回复，还有 1 个人在等你" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { level: 2, name: "你们之间未完成的事" })).toBeInTheDocument();
        expect(screen.getByRole("status")).toHaveTextContent("通讯录已于今天上午同步");
        expect(await screen.findByRole("button", { name: "聊之前给我讲讲" })).toBeInTheDocument();
        await act(async () => {
            await i18n.changeLanguage("en");
        });
    });
});

// Every write must re-read the directory from the backend, never from the
// warm page cache: DeletePrincipal, MergePeople, UndoMerge, CleanupAnonymous-
// Principals and the editor's saves emit no contacts event.
describe("Writes re-read the directory", () => {
    it("a successful delete refreshes the directory and the row is gone", async () => {
        const w = world();
        mocks.deletePrincipal.mockImplementation(async () => {
            mocks.overview.mockResolvedValue({ ...w.overview, open: w.overview.open.filter((p) => p.id !== "priya") });
            mocks.principals.mockResolvedValue(w.entries.filter((p) => p.principal.id !== "priya"));
        });
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        const before = mocks.overview.mock.calls.length;
        openMenu("More actions for Priya Nair");
        fireEvent.click(await screen.findByText("Delete person"));
        fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
        await waitFor(() => expect(mocks.deletePrincipal).toHaveBeenCalledWith("priya"));
        await waitFor(() => expect(mocks.overview.mock.calls.length).toBeGreaterThan(before));
        await waitFor(() => expect(screen.queryByRole("button", { name: /Priya Nair/ })).not.toBeInTheDocument());
    }, MENU_TEST_TIMEOUT);

    it("a banner merge re-reads (the source leaves the list) and undo re-reads again", async () => {
        const w = world();
        mocks.merge.mockImplementation(async () => {
            mocks.overview.mockResolvedValue({ ...w.overview, duplicates: [], everyone: w.overview.everyone.filter((p) => p.id !== "mei2") });
            mocks.principals.mockResolvedValue(w.entries.filter((p) => p.principal.id !== "mei2"));
            return { mergeId: "m-9", target: card("mei1", "anonymous", "Mei Chen ex-Stability") };
        });
        mocks.undo.mockImplementation(async () => {
            mocks.overview.mockResolvedValue(w.overview);
            mocks.principals.mockResolvedValue(w.entries);
        });
        render(<PeoplePage />);
        await screen.findByRole("region", { name: /look like one person/ });
        const before = mocks.overview.mock.calls.length;
        fireEvent.click(screen.getByRole("button", { name: "Merge into one" }));
        await waitFor(() => expect(mocks.merge).toHaveBeenCalledWith("mei1", ["mei2"]));
        await waitFor(() => expect(mocks.overview.mock.calls.length).toBeGreaterThan(before));
        // The re-read directory no longer carries the duplicate group.
        await waitFor(() => expect(screen.queryByRole("region", { name: /look like one person/ })).not.toBeInTheDocument());

        const afterMerge = mocks.overview.mock.calls.length;
        fireEvent.click(screen.getByRole("button", { name: "Undo" }));
        await waitFor(() => expect(mocks.undo).toHaveBeenCalledWith("m-9"));
        await waitFor(() => expect(mocks.overview.mock.calls.length).toBeGreaterThan(afterMerge));
        expect(await screen.findByRole("region", { name: /look like one person/ })).toBeInTheDocument();
    }, MENU_TEST_TIMEOUT);

    it("a banner merge lands the person in the list with the wash", async () => {
        const w = world();
        mocks.overview.mockResolvedValue({ ...w.overview, duplicates: [{ id: "dup-2", memberIds: ["sam", "mei2"], suggestedTargetId: "sam", reason: "same_email", app: "email" }] });
        mocks.merge.mockImplementation(async () => {
            mocks.overview.mockResolvedValue({ ...w.overview, duplicates: [] });
            return { mergeId: "m-9", target: card("sam", "contact", "Sam Rivera") };
        });
        render(<PeoplePage />);
        await screen.findByRole("region", { name: /look like one person/ });
        fireEvent.click(screen.getByRole("button", { name: "Merge into one" }));
        await waitFor(() => expect(document.querySelector('[data-landed="true"]')).not.toBeNull());
        const landed = document.querySelector('[data-landed="true"]')!;
        expect(landed).toHaveTextContent("Sam Rivera");
        expect(landed).toHaveClass("answer-wash");
    }, MENU_TEST_TIMEOUT);

    it("Not the same: the suggestion goes for good, with Undo until the page closes", async () => {
        const w = world();
        mocks.notSame.mockImplementation(async () => {
            mocks.overview.mockResolvedValue({ ...w.overview, duplicates: [] });
        });
        mocks.undoNotSame.mockImplementation(async () => {
            mocks.overview.mockResolvedValue(w.overview);
        });
        render(<PeoplePage />);
        const banner = await screen.findByRole("region", { name: /look like one person/ });
        fireEvent.click(within(banner).getByRole("button", { name: "Not the same" }));
        await waitFor(() => expect(mocks.notSame).toHaveBeenCalledWith(["mei1", "mei2"]));
        const line = await screen.findByText("Isoform won’t suggest these again.");
        await waitFor(() => expect(screen.queryByRole("region", { name: /look like one person/ })).not.toBeInTheDocument());
        expect(mocks.merge).not.toHaveBeenCalled();
        fireEvent.click(within(line.closest('[role="status"]') as HTMLElement).getByRole("button", { name: "Undo" }));
        await waitFor(() => expect(mocks.undoNotSame).toHaveBeenCalledWith(["mei1", "mei2"]));
        expect(await screen.findByRole("region", { name: /look like one person/ })).toBeInTheDocument();
    }, MENU_TEST_TIMEOUT);

    it("a create re-reads and the new person shows", async () => {
        const w = world();
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        const before = mocks.overview.mock.calls.length;
        openMenu("More Who you know actions");
        fireEvent.click(await screen.findByText("Add a person"));
        mocks.overview.mockResolvedValue({ ...w.overview, everyone: [...w.overview.everyone, card("new-person", "contact", "New Person")] });
        mocks.principals.mockResolvedValue([...w.entries, entry("new-person", "contact", "New Person")]);
        fireEvent.click(screen.getByRole("button", { name: "fixture save" }));
        await waitFor(() => expect(mocks.overview.mock.calls.length).toBeGreaterThan(before));
        await waitFor(() => expect(screen.getAllByText("New Person").length).toBeGreaterThan(0));
    }, MENU_TEST_TIMEOUT);

    it("an edit re-reads and the renamed person shows", async () => {
        const w = world();
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        await openPerson("Marcus Hale");
        openMenu("More actions for Marcus Hale");
        fireEvent.click(await screen.findByText("Edit person"));
        const before = mocks.overview.mock.calls.length;
        mocks.overview.mockResolvedValue({
            ...w.overview,
            open: w.overview.open.map((p) => (p.id === "marcus" ? { ...p, name: "Marcus H. Hale" } : p)),
        });
        mocks.principals.mockResolvedValue(w.entries.map((p) => (p.principal.id === "marcus" ? { ...p, principal: { ...p.principal, displayName: "Marcus H. Hale" } } : p)));
        fireEvent.click(screen.getByRole("button", { name: "fixture save" }));
        await waitFor(() => expect(mocks.overview.mock.calls.length).toBeGreaterThan(before));
        expect(await screen.findByRole("button", { name: /Marcus H\. Hale/ })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /^Open Marcus Hale$/ })).not.toBeInTheDocument();
    }, MENU_TEST_TIMEOUT);
});

describe("Privacy withdrawal", () => {
    it("a privacy sweep withdraws the hidden retained page before the re-read lands, then a permitted read recovers", async () => {
        world();
        const view = render(<PeoplePage active={false} />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });

        // The sweep's re-read hangs: nothing may paint pre-transition text.
        mocks.overview.mockImplementation(() => new Promise(() => {}));
        mocks.principals.mockImplementation(() => new Promise(() => {}));
        mocks.access.mockImplementation(() => new Promise(() => {}));
        await emitEvent("memory:updated");
        expect(screen.queryByRole("button", { name: /Priya Nair/ })).not.toBeInTheDocument();

        // Reopening while the reads are still held shows the skeleton, never
        // the withdrawn directory.
        view.rerender(<PeoplePage active={true} />);
        expect(screen.queryByRole("button", { name: /Priya Nair/ })).not.toBeInTheDocument();

        // A later ordinary change re-reads: what is still permitted returns.
        world();
        await emitEvent("contacts:updated");
        expect(await screen.findByRole("button", { name: /Priya Nair/ })).toBeInTheDocument();
    });

    it("a privacy sweep with failing reads shows the retry, never the withdrawn directory", async () => {
        world();
        render(<PeoplePage />);
        await screen.findByRole("heading", { level: 2, name: "Priya Nair" });
        mocks.overview.mockRejectedValue(new Error("gone"));
        mocks.principals.mockRejectedValue(new Error("gone"));
        mocks.access.mockRejectedValue(new Error("gone"));
        await emitEvent("closed-eyes:changed");
        expect(screen.queryByRole("button", { name: /Priya Nair/ })).not.toBeInTheDocument();
        expect(await screen.findByRole("button", { name: "Try again" })).toBeInTheDocument();
    });
});
