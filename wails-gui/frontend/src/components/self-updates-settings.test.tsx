import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import i18n from "@/i18n";
import type { SelfDreamUpdate } from "@/lib/wails-shim-app";
import en from "@/i18n/locales/en/settings.json";
import zh from "@/i18n/locales/zh-CN/settings.json";
import componentSource from "./self-updates-settings.tsx?raw";
import {
    SelfUpdatesSettings,
    initialSelfUpdatesCardState,
    receiveSelfUpdates,
} from "./self-updates-settings";

const mocks = vi.hoisted(() => ({
    list: vi.fn(),
    review: vi.fn(),
    listeners: new Map<string, () => void>(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    ListSelfDreamUpdates: mocks.list,
    ReviewSelfDreamOperation: mocks.review,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, callback: () => void) => {
        mocks.listeners.set(name, callback);
        return () => mocks.listeners.delete(name);
    },
}));

vi.mock("./settings-page", () => ({
    SettingsCard: ({ title, description, children }: {
        title: string;
        description?: string;
        children: ReactNode;
    }) => (
        <section>
            <h2>{title}</h2>
            {description && <p>{description}</p>}
            {children}
        </section>
    ),
}));

// Mirrors the operations SelfDreamUpdate carries in wails-gui/self_dream_product.go.
const OPERATIONS = ["merge", "split", "contextualize", "contradict", "supersede"];

function makeUpdate(overrides: Partial<SelfDreamUpdate> = {}): SelfDreamUpdate {
    return {
        id: "op-supersede",
        runId: "run-1",
        operation: "supersede",
        before: ["Prefer morning focus blocks for deep work."],
        after: ["Prefer morning focus blocks for deep work and planning."],
        scope: { kind: "project", key: "isoform" },
        evidenceCount: 2,
        uncertainty: "Planning is mentioned once.",
        createdAt: Date.UTC(2026, 9, 12, 9),
        ...overrides,
    };
}

async function renderReady(updates: SelfDreamUpdate[]) {
    mocks.list.mockResolvedValueOnce(updates);
    const result = render(<SelfUpdatesSettings />);
    await waitFor(() => {
        expect(screen.queryByText("Looking for changes to review...")).not.toBeInTheDocument();
    });
    return result;
}

describe("SelfUpdatesSettings component", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        mocks.listeners.clear();
        await i18n.changeLanguage("en");
        mocks.list.mockResolvedValue([]);
        mocks.review.mockRejectedValue(new Error("unexpected review"));
    });

    it("shows a proposal with what it changes and its evidence", async () => {
        await renderReady([
            makeUpdate(),
            makeUpdate({
                id: "op-merge",
                operation: "merge",
                before: ["Draft concise updates.", "Draft concise updates"],
                after: ["Draft concise updates."],
                scope: { kind: "global" },
                evidenceCount: 1,
                uncertainty: undefined,
            }),
        ]);
        expect(screen.getByRole("heading", { name: "Changes waiting for you" })).toBeInTheDocument();

        const supersede = screen.getByRole("article", { name: "Replace an outdated entry" });
        expect(within(supersede).getByText("Newer evidence gives a fuller picture.")).toBeInTheDocument();
        expect(within(supersede).getByText("Prefer morning focus blocks for deep work.")).toBeInTheDocument();
        expect(within(supersede).getByText("Prefer morning focus blocks for deep work and planning.")).toBeInTheDocument();
        expect(within(supersede).getByText("2 sources · Project: isoform")).toBeInTheDocument();
        expect(within(supersede).getByText("Planning is mentioned once.")).toBeInTheDocument();

        const merge = screen.getByRole("article", { name: "Combine entries that say the same thing" });
        expect(
            within(merge).getByText("Isoform found 2 entries that may say the same thing."),
        ).toBeInTheDocument();
        expect(within(merge).getByText("1 source")).toBeInTheDocument();
    });

    it("records a decision and reads the list again", async () => {
        await renderReady([makeUpdate()]);
        mocks.review.mockResolvedValueOnce({});
        mocks.list.mockResolvedValueOnce([]);
        fireEvent.click(screen.getByRole("button", { name: "Accept" }));
        await waitFor(() => {
            expect(screen.getByText("Nothing to review right now.")).toBeInTheDocument();
        });
        expect(mocks.review).toHaveBeenCalledTimes(1);
        const input = mocks.review.mock.calls[0][0];
        expect(input).toMatchObject({ operationId: "op-supersede", decision: "accept" });
        expect(input.idempotencyKey).toEqual(expect.any(String));
        expect(input.idempotencyKey).not.toBe("");
        expect(input).not.toHaveProperty("note");
    });

    it("sends the other decisions under their own names", async () => {
        await renderReady([makeUpdate()]);
        mocks.review.mockResolvedValue({});
        mocks.list.mockResolvedValue([makeUpdate()]);
        fireEvent.click(screen.getByRole("button", { name: "Keep as is" }));
        await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(screen.getByRole("button", { name: "Need more evidence" })).toBeEnabled());
        fireEvent.click(screen.getByRole("button", { name: "Need more evidence" }));
        await waitFor(() => expect(mocks.review).toHaveBeenCalledTimes(2));
        expect(mocks.review.mock.calls.map((call) => call[0].decision)).toEqual([
            "keep_separate",
            "not_enough_evidence",
        ]);
        expect(mocks.review.mock.calls[0][0].idempotencyKey).not.toBe(mocks.review.mock.calls[1][0].idempotencyKey);
    });

    it("needs the corrected understanding before saving a correction", async () => {
        await renderReady([makeUpdate()]);
        fireEvent.click(screen.getByRole("button", { name: "Correct" }));
        const save = screen.getByRole("button", { name: "Save correction" });
        expect(save).toBeDisabled();

        fireEvent.change(screen.getByLabelText("What should Isoform understand instead?"), {
            target: { value: "  Plan in the afternoon.  " },
        });
        mocks.review.mockResolvedValueOnce({});
        mocks.list.mockResolvedValueOnce([]);
        fireEvent.click(save);
        await waitFor(() => {
            expect(screen.getByText("Nothing to review right now.")).toBeInTheDocument();
        });
        expect(mocks.review.mock.calls[0][0]).toMatchObject({
            operationId: "op-supersede",
            decision: "correct",
            note: "Plan in the afternoon.",
        });
    });

    it("shows a failed decision inside its proposal and drops the error when the proposal leaves", async () => {
        const other = makeUpdate({ id: "op-other", operation: "contradict" });
        await renderReady([makeUpdate(), other]);
        mocks.list.mockResolvedValueOnce([makeUpdate(), other]);
        const contradict = screen.getByRole("article", { name: "Resolve a contradiction" });
        fireEvent.click(within(contradict).getByRole("button", { name: "Accept" }));
        await waitFor(() => {
            expect(within(contradict).getByRole("alert")).toHaveTextContent("Could not save your decision. Try again.");
        });
        const supersede = screen.getByRole("article", { name: "Replace an outdated entry" });
        expect(within(supersede).queryByRole("alert")).not.toBeInTheDocument();

        mocks.list.mockResolvedValueOnce([makeUpdate()]);
        mocks.listeners.get("self_dream:updated")?.();
        await waitFor(() => {
            expect(screen.queryByRole("article", { name: "Resolve a contradiction" })).not.toBeInTheDocument();
        });
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });

    it("explains a proposal that went stale where it was listed", async () => {
        const other = makeUpdate({ id: "op-other", operation: "contradict" });
        await renderReady([makeUpdate(), other]);
        mocks.review.mockRejectedValueOnce(
            new Error("stale Self Dream target self-1: canonical Self has revision 2"),
        );
        mocks.list.mockResolvedValueOnce([other]);
        const supersede = screen.getByRole("article", { name: "Replace an outdated entry" });
        fireEvent.click(within(supersede).getByRole("button", { name: "Accept" }));
        await waitFor(() => {
            expect(screen.getByRole("alert")).toHaveTextContent(/changed after this was proposed/);
        });
        const articles = screen.getAllByRole("article");
        expect(articles.map((article) => article.getAttribute("aria-label"))).toEqual([
            "Replace an outdated entry",
            "Resolve a contradiction",
        ]);
        expect(within(articles[0]).getByRole("alert")).toBeInTheDocument();
        expect(within(articles[0]).queryByRole("button", { name: "Accept" })).not.toBeInTheDocument();

        fireEvent.click(within(articles[0]).getByRole("button", { name: "Dismiss" }));
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
        expect(screen.getAllByRole("article")).toHaveLength(1);
    });

    it("keeps an open correction when another proposal is decided", async () => {
        const other = makeUpdate({ id: "op-other", operation: "contradict" });
        await renderReady([makeUpdate(), other]);
        const supersede = screen.getByRole("article", { name: "Replace an outdated entry" });
        fireEvent.click(within(supersede).getByRole("button", { name: "Correct" }));
        fireEvent.change(within(supersede).getByLabelText("What should Isoform understand instead?"), {
            target: { value: "Plan in the afternoon." },
        });

        mocks.review.mockResolvedValueOnce({});
        mocks.list.mockResolvedValueOnce([makeUpdate()]);
        const contradict = screen.getByRole("article", { name: "Resolve a contradiction" });
        fireEvent.click(within(contradict).getByRole("button", { name: "Keep as is" }));
        await waitFor(() => {
            expect(screen.queryByRole("article", { name: "Resolve a contradiction" })).not.toBeInTheDocument();
        });
        expect(mocks.review.mock.calls[0][0]).toMatchObject({ operationId: "op-other", decision: "keep_separate" });
        expect(screen.getByLabelText("What should Isoform understand instead?")).toHaveValue("Plan in the afternoon.");
    });

    it("keeps the typed correction and explains it when a refresh drops the proposal", async () => {
        await renderReady([makeUpdate()]);
        fireEvent.click(screen.getByRole("button", { name: "Correct" }));
        fireEvent.change(screen.getByLabelText("What should Isoform understand instead?"), {
            target: { value: "Plan in the afternoon." },
        });

        mocks.list.mockResolvedValueOnce([]);
        mocks.listeners.get("self_model:updated")?.();
        await waitFor(() => {
            expect(screen.getByRole("alert")).toHaveTextContent(/changed after this was proposed/);
        });
        const kept = screen.getByLabelText("Your correction, kept so you can copy it");
        expect(kept).toHaveValue("Plan in the afternoon.");
        expect(kept).toHaveAttribute("readonly");
        expect(screen.queryByRole("button", { name: "Save correction" })).not.toBeInTheDocument();
        expect(screen.getByText("Nothing to review right now.")).toBeInTheDocument();
        expect(mocks.review).not.toHaveBeenCalled();
    });

    it("reads the list again when Self Dream or Self changes", async () => {
        await renderReady([]);
        mocks.list.mockResolvedValueOnce([makeUpdate()]);
        mocks.listeners.get("self_dream:updated")?.();
        await waitFor(() => {
            expect(screen.getByRole("article", { name: "Replace an outdated entry" })).toBeInTheDocument();
        });
        expect(mocks.listeners.has("self_model:updated")).toBe(true);
    });

    it("offers a retry after a failed read", async () => {
        mocks.list.mockRejectedValueOnce(new Error("offline"));
        render(<SelfUpdatesSettings />);
        await waitFor(() => {
            expect(screen.getByRole("alert")).toHaveTextContent("Could not load the changes to review. Try again.");
        });
        mocks.list.mockResolvedValueOnce([makeUpdate()]);
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        await waitFor(() => {
            expect(screen.getByRole("article", { name: "Replace an outdated entry" })).toBeInTheDocument();
        });
    });

    it("reopens a kept correction when its proposal is listed again", () => {
        const update = makeUpdate();
        let state = receiveSelfUpdates(initialSelfUpdatesCardState, [update]);
        state = { ...state, corrections: { [update.id]: "Plan in the afternoon." } };
        state = receiveSelfUpdates(state, []);
        expect(state.corrections).toEqual({});
        expect(state.retired).toEqual([{ update, index: 0, correction: "Plan in the afternoon." }]);
        state = receiveSelfUpdates(state, [update]);
        expect(state.retired).toEqual([]);
        expect(state.corrections).toEqual({ [update.id]: "Plan in the afternoon." });
    });

    it("has every key it uses in English and Chinese", () => {
        const keys = new Set<string>();
        for (const match of componentSource.matchAll(/t\(\s*"selfUpdates\.([^"]+)"/g)) {
            keys.add(match[1]);
        }
        expect(keys.size).toBeGreaterThan(10);
        const lookup = (tree: Record<string, unknown>, path: string) =>
            path.split(".").reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], tree);
        for (const key of keys) {
            const plural = key === "sources" ? "sources_other" : key;
            expect(lookup(en.selfUpdates, plural), `en ${key}`).toBeTypeOf("string");
            expect(lookup(zh.selfUpdates, plural), `zh ${key}`).toBeTypeOf("string");
        }
        for (const key of ["saveFailed", "stale"]) {
            expect(lookup(en.selfUpdates, key), `en ${key}`).toBeTypeOf("string");
            expect(lookup(zh.selfUpdates, key), `zh ${key}`).toBeTypeOf("string");
        }
        expect(Object.keys(en.selfUpdates.operations).sort()).toEqual([...OPERATIONS].sort());
        expect(Object.keys(zh.selfUpdates.operations).sort()).toEqual([...OPERATIONS].sort());
        expect(Object.keys(zh.selfUpdates.scopeKinds).sort()).toEqual(Object.keys(en.selfUpdates.scopeKinds).sort());
    });

    it("keeps its wording apart from app updates and in plain words", () => {
        // Help and about checks for app updates; this card reviews changes.
        for (const key of ["loading", "loadFailed"] as const) {
            expect(en.selfUpdates[key]).not.toMatch(/update/i);
            expect(zh.selfUpdates[key]).not.toContain("更新");
        }
        const operations = en.selfUpdates.operations as Record<string, { title: string; summary: string }>;
        for (const operation of OPERATIONS) {
            expect(operations[operation].title, operation).not.toMatch(/understanding/i);
            expect(operations[operation].summary, operation).not.toMatch(/understanding/i);
        }
    });
});
