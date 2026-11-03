import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import "@/i18n";
import { ActivityAskPanel } from "./activity-ask-panel";

const mocks = vi.hoisted(() => ({ prepare: vi.fn(), panel: vi.fn() }));
vi.mock("@/lib/today-chat-prepared", () => ({ prepareTodayChatDraftNow: mocks.prepare }));
vi.mock("@/components/today/MainChatPanel", () => ({
    MainChatPanel: (props: unknown) => { mocks.panel(props); return <div data-testid="chat-panel" />; },
}));

const handlers = { onStartChat: vi.fn(), onRunNow: vi.fn(), onOpenMemory: vi.fn(), onOpenAutomations: vi.fn(), onOpenActivity: vi.fn(), onOpenPeople: vi.fn() };
beforeEach(() => { mocks.prepare.mockReset(); mocks.panel.mockReset(); });

it("sends a date question in Activity's panel without filling its composer", async () => {
    render(<ActivityAskPanel request={{ id: 1, text: "What happened?" }} handlers={handlers} onClose={vi.fn()} />);
    await waitFor(() => expect(mocks.panel).toHaveBeenLastCalledWith(expect.objectContaining({
        draft: "", starterRequest: { id: 1, text: "What happened?" },
    })));
    expect(mocks.prepare).not.toHaveBeenCalled();
});

it("prepares and binds an Activity moment before posting it", async () => {
    mocks.prepare.mockResolvedValue({ status: "ready", draft: { prefill: { kind: "activity", title: "Current title" }, counts: {}, fingerprint: "current" } });
    const context = { sourceKind: "activity", sourceId: "moment", dayStartMs: 42 };
    render(<ActivityAskPanel request={{ id: 2, text: "Old title", context }} handlers={handlers} onClose={vi.fn()} />);
    await waitFor(() => expect(mocks.panel).toHaveBeenLastCalledWith(expect.objectContaining({
        starterRequest: expect.objectContaining({ id: 2, text: expect.stringContaining("Current title"), context: expect.objectContaining({ ...context, generatedFingerprint: "current" }) }),
    })));
});

it("answers a day using its current summary while preserving the date question", async () => {
    mocks.prepare.mockResolvedValue({ status: "ready", draft: { prefill: { kind: "summary", title: "Summary" }, counts: {}, fingerprint: "current" } });
    const context = { sourceKind: "summary", sourceId: "2026-10-12" };
    render(<ActivityAskPanel request={{ id: 5, text: "What happened on October 12?", context }} handlers={handlers} onClose={vi.fn()} />);
    await waitFor(() => expect(mocks.panel).toHaveBeenLastCalledWith(expect.objectContaining({
        starterRequest: expect.objectContaining({ text: "What happened on October 12?", context: expect.objectContaining({ ...context, generatedFingerprint: "current" }) }),
    })));
});

it("keeps the weekly question while binding the selected week", async () => {
    mocks.prepare.mockResolvedValue({ status: "ready", draft: { prefill: { kind: "activity", title: "Summary" }, counts: {}, fingerprint: "week" } });
    const context = { sourceKind: "summary_week", sourceId: "2026-10-13" };
    render(<ActivityAskPanel request={{ id: 6, text: "What happened this week?", context }} handlers={handlers} onClose={vi.fn()} />);
    await waitFor(() => expect(mocks.panel).toHaveBeenLastCalledWith(expect.objectContaining({
        starterRequest: expect.objectContaining({ text: "What happened this week?", context: expect.objectContaining({ ...context, generatedFingerprint: "week" }) }),
    })));
});

it("does not post a moment when its source cannot be prepared", async () => {
    mocks.prepare.mockResolvedValue({ status: "failed", unavailable: true });
    render(<ActivityAskPanel request={{ id: 3, text: "Private stale title", context: { sourceKind: "activity", sourceId: "moment" } }} handlers={handlers} onClose={vi.fn()} />);
    await screen.findByRole("alert");
    expect(mocks.panel).toHaveBeenLastCalledWith(expect.objectContaining({ starterRequest: null }));
});

it("does not substitute new card words for the action the person clicked", async () => {
    mocks.prepare.mockResolvedValue({ status: "ready", draft: { prefill: { kind: "brief_card", title: "Card" }, counts: {}, fingerprint: "fresh", message: "A different action" } });
    render(<ActivityAskPanel request={{ id: 4, text: "Original action", context: { sourceKind: "brief_card", sourceId: "card", briefId: "brief", generatedMessage: "Original action" } }} handlers={handlers} onClose={vi.fn()} />);
    await screen.findByRole("alert");
    expect(mocks.panel).toHaveBeenLastCalledWith(expect.objectContaining({ starterRequest: null }));
});
