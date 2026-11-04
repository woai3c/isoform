import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { TodayLastBriefPanel } from "./TodayLastBriefPanel";

const mocks = vi.hoisted(() => ({ byId: vi.fn(), record: vi.fn(async () => undefined) }));
vi.mock("@/lib/wails-shim-app", () => ({ GetTodayBriefByID: mocks.byId, RecordTodayAction: mocks.record }));

const asOf = new Date(2026, 9, 31, 17, 50).toISOString();
const view = (briefId: string) => ({ brief: { briefId, slot: "evening", asOf, timezone: "", narrative: [{ text: "Good evening.", refs: [] }, { text: "Want me to plan it?", refs: [] }], cards: [{ id: "c", kind: "action", mode: "send", label: "Yes, plan it", message: "Yes", answerTo: "Want me to plan it?", refs: [] }] }, building: false, enabled: true, nextAt: "", nextSlot: "", empty: false, missing: [] });

beforeEach(async () => { await i18n.changeLanguage("en"); mocks.byId.mockReset(); });
afterEach(async () => { await i18n.changeLanguage("en"); });

describe("TodayLastBriefPanel (today-review v1.1.5 #1)", () => {
    it.each([["en", "Evening brief, Sat 5:50 PM"], ["zh-CN", "晚间简报，周六 17:50"]])("opens the earlier brief with its day and no question to answer in %s", async (language, title) => {
        await i18n.changeLanguage(language);
        mocks.byId.mockResolvedValue(view("2026-10-31-evening"));
        const onClose = vi.fn();
        render(<TodayLastBriefPanel briefId="2026-10-31-evening" onClose={onClose} />);
        expect(await screen.findByText("Good evening.")).toBeInTheDocument();
        expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent(title);
        expect(screen.queryByRole("button", { name: "Yes, plan it" })).not.toBeInTheDocument();
        expect(mocks.byId).toHaveBeenCalledWith("2026-10-31-evening");
        fireEvent.click(screen.getByRole("button", { name: language === "en" ? "Close" : "关闭" }));
        expect(onClose).toHaveBeenCalledOnce();
    });

    it("never shows a fallback brief in place of the one the link names", async () => {
        mocks.byId.mockResolvedValue(view("2026-11-01-morning"));
        render(<TodayLastBriefPanel briefId="2026-10-31-evening" onClose={vi.fn()} />);
        expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't open this brief. Try again");
        expect(screen.queryByText("Good evening.")).not.toBeInTheDocument();
    });

    it("opens at once with its title and time, says a failure in copper and never closes by itself (today-review v1.1.13 D4)", async () => {
        mocks.byId.mockRejectedValueOnce(new Error("busy"));
        const onClose = vi.fn();
        render(<TodayLastBriefPanel briefId="2026-10-31-evening" title="Evening brief, Sat 5:50 PM" onClose={onClose} />);
        expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("Evening brief, Sat 5:50 PM");
        const failed = await screen.findByTestId("last-brief-open-failed");
        expect(failed).toHaveTextContent("Couldn't open this brief. Try again");
        expect(failed).toHaveClass("text-copper");
        expect(onClose).not.toHaveBeenCalled();
        mocks.byId.mockResolvedValue(view("2026-10-31-evening"));
        fireEvent.click(within(failed).getByRole("button", { name: "Try again" }));
        expect(await screen.findByText("Good evening.")).toBeInTheDocument();
        expect(mocks.byId).toHaveBeenCalledTimes(2);
        expect(onClose).not.toHaveBeenCalled();
    });
});
