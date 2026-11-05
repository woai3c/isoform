import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { MainChatProactiveCard } from "@/components/main-chat-proactive-card";
import type { MainChatProactivePayload } from "@/lib/stream-helpers";
import type { StreamItem } from "@/types";

vi.mock("@/lib/wails-shim-app", () => ({
    LoadMainChatProactiveItem: vi.fn(),
    NativeClipboardSetText: vi.fn(),
    OpenDeliverableFile: vi.fn(),
    OpenExternalURL: vi.fn(),
    RecordMainChatProactiveReceipt: vi.fn(),
    ResolveMainChatProactiveAction: vi.fn(),
    ReturnToMainChatPlace: vi.fn(),
    GetFindTimeDetails: vi.fn(),
    GetHoldSettings: vi.fn(),
    ProposeHold: vi.fn(),
    GetTodayDayShape: vi.fn(),
    GetHoldDayShape: vi.fn(),
}));
vi.mock("@/lib/wails-shim-runtime", () => ({ ClipboardSetText: vi.fn(), EventsOn: vi.fn(() => () => undefined) }));
vi.mock("@/hooks/useMainChatProactiveReceipts", () => ({ useMainChatProactiveReceipts: () => () => undefined }));
vi.mock("@/lib/engagement-settings-events", () => ({ onEngagementSettingsChanged: () => () => undefined }));

// Invented row shaped like a refreshed thread card (chat v1.0.12).
function refreshedCard(overrides: Partial<MainChatProactivePayload> = {}): StreamItem {
    const message: MainChatProactivePayload = {
        momentId: "moment-refreshed",
        kind: "decision_due",
        headline: "Ship the release",
        body: "Your assistant was waiting on the merge. Next: Confirm the merge, then ship.",
        draft: false,
        actionIds: ["open"],
        actionLabels: { open: "Open" },
        target: { kind: "thread", id: "thread-1" },
        evidence: [],
        state: "pending",
        updatedAt: Date.UTC(2026, 10, 4, 6, 47, 22),
        ...overrides,
    };
    return { id: "stream-refreshed", kind: "system", toolName: "main_chat_proactive", content: message.headline, toolInput: message, sortOrder: 1, createdAt: message.updatedAt } as unknown as StreamItem;
}

describe("a refreshed card keeps its place and says what changed (chat v1.0.12)", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });
    afterEach(() => cleanup());

    it("shows the refresh time and the newer outcome on the same card", () => {
        const { container } = render(<MainChatProactiveCard item={refreshedCard()} />);
        const line = container.querySelector("[data-main-chat-updated]");
        expect(line).not.toBeNull();
        expect(line!.textContent).toMatch(/^Updated /);
        expect(screen.getByText("Ship the release")).toBeInTheDocument();
    });

    it("says a refresh that finished the thread is done, with its time", () => {
        const { container } = render(<MainChatProactiveCard item={refreshedCard({ state: "done" })} />);
        expect(container.querySelector("[data-main-chat-updated]")!.textContent).toMatch(/^Done · /);
    });

    it("shows no refresh line on a card that was never refreshed", () => {
        const { container } = render(<MainChatProactiveCard item={refreshedCard({ updatedAt: undefined })} />);
        expect(container.querySelector("[data-main-chat-updated]")).toBeNull();
    });
});
