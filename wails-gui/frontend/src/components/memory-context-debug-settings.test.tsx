import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import "@/i18n";
import { MemoryContextDebugSettings } from "./memory-context-debug-settings";

const mocks = vi.hoisted(() => ({
    getSnapshot: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", async (importOriginal) => {
    const original = await importOriginal<typeof import("@/lib/wails-shim-app")>();
    return {
        ...original,
        GetMemoryContextDebugSnapshot: mocks.getSnapshot,
    };
});

function snapshot(overrides: Record<string, unknown> = {}) {
    return {
        version: "memory-context-debug-v1",
        query: "Plan the next Isoform milestone",
        minScore: 0.35,
        characterBudget: 48_000,
        detailPayloadBudget: 128_000,
        counts: {
            discovered: 9,
            correctedExcluded: 1,
            afterCorrection: 8,
            belowRelevance: 3,
            empty: 1,
            relevant: 4,
            selected: 3,
            selectedMemory: 2,
            selectedKnowledge: 1,
            selectedOther: 0,
            selectedChars: 12_400,
            truncated: 0,
            budgetOmitted: 1,
            detailItemsOmitted: 0,
        },
        selected: [
            {
                path: "/Users/test/.yansu-agent/memory/project-direction.md",
                source: "memory",
                startLine: 12,
                endLine: 16,
                score: 0.91,
                preview: "Isoform should learn through silent shadow comparisons.",
            },
            {
                path: "/Users/test/.yansu-agent/knowledge/authority.md",
                source: "knowledge",
                startLine: 3,
                endLine: 3,
                score: 0.82,
                preview: "Learning never grants permission to act.",
            },
        ],
        countsReconcile: true,
        readOnly: true,
        generatedAt: 1_800_000_000_000,
        durationMs: 24,
        ...overrides,
    };
}

describe("MemoryContextDebugSettings", () => {
    beforeEach(() => {
        mocks.getSnapshot.mockReset();
    });

    it("leaves its own header to a host card when asked", () => {
        const { container } = render(<MemoryContextDebugSettings hideHeader />);

        expect(screen.queryByRole("heading", { name: "Preview chat context" })).not.toBeInTheDocument();
        expect(screen.getByRole("region", { name: "Preview chat context" })).toBeInTheDocument();
        expect(container.querySelector("section")).not.toHaveClass("border-t");
        expect(screen.getByLabelText("Chat message")).toBeInTheDocument();
    });

    it("starts as a read-only explanation without querying the backend", () => {
        render(<MemoryContextDebugSettings />);

        expect(screen.getByRole("heading", { name: "Preview chat context" })).toBeInTheDocument();
        expect(screen.getByText("Nothing is sent, changed, or started.")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Preview context" })).toBeDisabled();
        expect(mocks.getSnapshot).not.toHaveBeenCalled();
    });

    it("shows the exact production selection and expandable source details", async () => {
        mocks.getSnapshot.mockResolvedValue(snapshot());
        render(<MemoryContextDebugSettings />);

        fireEvent.change(screen.getByLabelText("Chat message"), {
            target: { value: "  Plan the next Isoform milestone  " },
        });
        fireEvent.click(screen.getByRole("button", { name: "Preview context" }));

        await waitFor(() => {
            expect(mocks.getSnapshot).toHaveBeenCalledWith("Plan the next Isoform milestone");
        });
        expect(await screen.findByText("Isoform should learn through silent shadow comparisons.")).toBeInTheDocument();
        expect(screen.getByText("12,400 of 48,000 characters")).toBeInTheDocument();
        expect(screen.getByText("91% match")).toBeInTheDocument();

        const summary = screen.getByText("Isoform should learn through silent shadow comparisons.").closest("summary");
        expect(summary).not.toBeNull();
        const details = summary?.closest("details");
        expect(details).not.toHaveAttribute("open");
        fireEvent.click(summary!);
        expect(details).toHaveAttribute("open");
        expect(screen.getByText("/Users/test/.yansu-agent/memory/project-direction.md")).toBeInTheDocument();
        expect(screen.getByLabelText("Read-only")).toBeInTheDocument();
    });

    it("surfaces a failed lookup and lets the same message be retried", async () => {
        mocks.getSnapshot
            .mockRejectedValueOnce(new Error("memory index is rebuilding"))
            .mockResolvedValueOnce(snapshot());
        render(<MemoryContextDebugSettings />);

        fireEvent.change(screen.getByLabelText("Chat message"), {
            target: { value: "Plan the next Isoform milestone" },
        });
        fireEvent.click(screen.getByRole("button", { name: "Preview context" }));

        expect(await screen.findByRole("alert")).toHaveTextContent("memory index is rebuilding");
        fireEvent.click(screen.getByRole("button", { name: "Preview context" }));

        await waitFor(() => expect(mocks.getSnapshot).toHaveBeenCalledTimes(2));
        expect(await screen.findByText("Context selected for this message")).toBeInTheDocument();
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });

    it("explains when no context is relevant enough", async () => {
        mocks.getSnapshot.mockResolvedValue(snapshot({
            counts: {
                discovered: 4,
                correctedExcluded: 0,
                afterCorrection: 4,
                belowRelevance: 4,
                empty: 0,
                relevant: 0,
                selected: 0,
                selectedMemory: 0,
                selectedKnowledge: 0,
                selectedOther: 0,
                selectedChars: 0,
                truncated: 0,
                budgetOmitted: 0,
                detailItemsOmitted: 0,
            },
            selected: [],
        }));
        render(<MemoryContextDebugSettings />);

        fireEvent.change(screen.getByLabelText("Chat message"), {
            target: { value: "Unrelated question" },
        });
        fireEvent.click(screen.getByRole("button", { name: "Preview context" }));

        expect(await screen.findByText(
            "Nothing from About you or reusable know-how was relevant enough to add for this message.",
        )).toBeInTheDocument();
    });
});
