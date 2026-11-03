import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { BriefScheduleDetail } from "@/lib/wails-shim-app";
import { BriefDetailPane } from "./BriefDetailPane";

const mocks = vi.hoisted(() => ({
    detail: vi.fn(), make: vi.fn(),
    events: new Map<string, (...args: unknown[]) => void>(),
}));
vi.mock("@/lib/wails-shim-app", () => ({ GetBriefScheduleDetail: mocks.detail, GenerateTodayMissedBrief: mocks.make }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({
    EventsOn: vi.fn((name: string, fn: (...args: unknown[]) => void) => { mocks.events.set(name, fn); return () => mocks.events.delete(name); }),
}));

const at = (dayOffset: number, hour = 7, minute = 30) => {
    const d = new Date(); d.setDate(d.getDate() + dayOffset); d.setHours(hour, minute, 0, 0); return d;
};
function detail(extra: Partial<BriefScheduleDetail> = {}): BriefScheduleDetail {
    return {
        slot: "morning", enabled: true, time: "07:30", nextRunAt: at(1).getTime(),
        runs: [
            { briefId: "b-today", status: "ok", scheduledAt: at(0).toISOString(), at: at(0).toISOString() },
            { briefId: "b-yesterday", status: "missed", scheduledAt: at(-1).toISOString(), at: at(-1).toISOString(), reason: "asleep" },
            { briefId: "b-before", status: "missed", scheduledAt: at(-2).toISOString(), at: at(-2).toISOString() },
        ],
        ...extra,
    };
}

beforeEach(async () => {
    await i18n.changeLanguage("en");
    mocks.detail.mockReset().mockResolvedValue(detail());
    mocks.make.mockReset().mockResolvedValue({ status: "started", nextAllowedAt: "" });
});
afterEach(async () => { await i18n.changeLanguage("en"); });

describe("BriefDetailPane (K3)", () => {
    it("shows the brief's schedule, what it is and the foot, with no schedule controls", async () => {
        const onChangeTimes = vi.fn();
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} onChangeTimes={onChangeTimes} />);
        expect(screen.getByRole("heading", { name: "Morning brief" })).toBeInTheDocument();
        expect(await screen.findByTestId("brief-detail-schedule")).toHaveTextContent("Every day, 7:30 AM · Next tomorrow");
        expect(screen.getByText("What moved, what's next and what needs you, ready on Today.")).toBeInTheDocument();
        expect(screen.getByTestId("brief-detail-foot")).toHaveTextContent("Change times · In Settings › Brief");
        for (const name of [/pause/i, /run now/i, /edit in chat/i, /delete/i, /history/i]) expect(screen.queryByRole("button", { name })).toBeNull();
        fireEvent.click(screen.getByRole("button", { name: "Change times" }));
        expect(onChangeTimes).toHaveBeenCalledOnce();
        expect(mocks.detail).toHaveBeenCalledWith("morning");
    });

    it("lists the last three runs, with the missed reason only when the record has one", async () => {
        const onOpenToday = vi.fn();
        mocks.detail.mockResolvedValue(detail({ runs: [...detail().runs.slice(0, 2), { briefId: "b-closed", status: "missed", scheduledAt: at(-3).toISOString(), at: at(-3).toISOString(), reason: "closed" }] }));
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} onOpenToday={onOpenToday} />);
        const list = await screen.findByRole("region", { name: "The last three" });
        await waitFor(() => expect(list.querySelectorAll("[data-brief-run]")).toHaveLength(3));
        const ok = list.querySelector('[data-brief-run="b-today"]') as HTMLElement;
        expect(ok).toHaveTextContent("Today, 7:30 AM");
        fireEvent.click(within(ok).getByRole("button", { name: "Open on Today" }));
        expect(onOpenToday).toHaveBeenCalledWith("b-today");
        expect(list.querySelector('[data-brief-run="b-yesterday"]')).toHaveTextContent("YesterdayDidn't run: your computer was asleep at 7:30 AM.");
        expect(list.querySelector('[data-brief-run="b-closed"]')).toHaveTextContent("Didn't run: Isoform was closed at 7:30 AM.");
    });

    it("says only that it didn't run when the record has no reason", async () => {
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        const row = await waitFor(() => { const el = document.querySelector('[data-brief-run="b-before"]'); expect(el).not.toBeNull(); return el as HTMLElement; });
        expect(row).toHaveTextContent("Didn't run at 7:30 AM.");
        expect(row).not.toHaveTextContent("asleep");
        expect(row).not.toHaveTextContent("closed");
    });

    it("uses the recorded closed cause for a late brief", async () => {
        mocks.detail.mockResolvedValue(detail({ runs: [
            { briefId: "b-late", status: "ok", scheduledAt: at(0, 7, 0).toISOString(), at: at(0, 9, 23).toISOString(), late: true, reason: "closed" },
        ] }));
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        const row = await waitFor(() => { const el = document.querySelector('[data-brief-run="b-late"]'); expect(el).not.toBeNull(); return el as HTMLElement; });
        expect(row).toHaveTextContent("Came at 9:23 AM, not 7:00 AM: Isoform wasn't open.");
    });

    it.each([["en", "Tried 3 times. It timed out."], ["zh-CN", "试了 3 次。超时了。"]])("shows the recorded failure cause in %s", async (language, line) => {
        await i18n.changeLanguage(language);
        mocks.detail.mockResolvedValue(detail({ runs: [{ briefId: "failed", status: "failed", scheduledAt: at(0, 7).toISOString(), at: "", attempts: 3, failureCause: "timeout" }] }));
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        expect(await screen.findByText(line)).toBeInTheDocument();
    });

    it("makes it now, then says Made at {time} · Open on Today", async () => {
        const onOpenToday = vi.fn();
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} onOpenToday={onOpenToday} />);
        fireEvent.click(await screen.findByRole("button", { name: "Make it now" }));
        await waitFor(() => expect(mocks.make).toHaveBeenCalledOnce());
        expect(screen.getByTestId("brief-detail-make")).toHaveTextContent("Making it now…");
        const token = mocks.make.mock.calls[0]?.[0];
        act(() => { mocks.events.get("today:brief-request-finished")?.("someone-else", true); });
        expect(screen.getByTestId("brief-detail-make")).toHaveTextContent("Making it now…");
        act(() => { mocks.events.get("today:brief-request-finished")?.(token, true); });
        expect(screen.getByTestId("brief-detail-make")).toHaveTextContent(/^Made at \d{1,2}:\d{2} [AP]M · Open on Today$/);
        fireEvent.click(within(screen.getByTestId("brief-detail-make")).getByRole("button", { name: "Open on Today" }));
        expect(onOpenToday).toHaveBeenCalledWith();
        await waitFor(() => expect(mocks.detail).toHaveBeenCalledTimes(2));
    });

    it("says a failed make in place, without a toast", async () => {
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        fireEvent.click(await screen.findByRole("button", { name: "Make it now" }));
        await waitFor(() => expect(mocks.make).toHaveBeenCalledOnce());
        act(() => { mocks.events.get("today:brief-request-finished")?.(mocks.make.mock.calls[0]?.[0], false); });
        expect(screen.getByRole("alert")).toHaveTextContent("Could not make your brief. Try again.");
        expect(screen.getByRole("button", { name: "Make it now" })).toBeInTheDocument();
    });

    it("shows a failed read in place", async () => {
        mocks.detail.mockRejectedValue(new Error("down"));
        render(<BriefDetailPane slot="evening" onClose={vi.fn()} />);
        expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load the run history. Try again");
        expect(screen.getByRole("heading", { name: "Evening brief" })).toBeInTheDocument();
    });

    it("closes on Esc and on its X", async () => {
        const onClose = vi.fn();
        render(<BriefDetailPane slot="morning" onClose={onClose} />);
        fireEvent.keyDown(window, { key: "Escape" });
        fireEvent.click(screen.getByRole("button", { name: "Close" }));
        expect(onClose).toHaveBeenCalledTimes(2);
    });

    it("says it all in zh-CN", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        expect(screen.getByRole("heading", { name: "早间简报" })).toBeInTheDocument();
        expect(await screen.findByTestId("brief-detail-schedule")).toHaveTextContent("每天上午7:30 · 下次：明天");
        expect(screen.getByText("变化、接下来的事和需要你的事，在“今日”上准备好。")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "现在生成" })).toBeInTheDocument();
        expect(screen.getByText("最近三次")).toBeInTheDocument();
        await waitFor(() => expect(document.querySelector('[data-brief-run="b-yesterday"]')).toHaveTextContent("没有运行：上午7:30时电脑在睡眠。"));
        expect(document.querySelector('[data-brief-run="b-before"]')).toHaveTextContent("上午7:30没有运行。");
        expect(screen.getByTestId("brief-detail-foot")).toHaveTextContent("更改时间 · 在“设置 › 简报”中");
    });
});

// tasks-review v1.0.3: today's run first, with what its record says.
describe("BriefDetailPane (v1.0.3)", () => {
    const today = (hour: number, minute = 0) => at(0, hour, minute).toISOString();
    beforeEach(() => {
        vi.useFakeTimers({ toFake: ["Date"] });
        vi.setSystemTime(new Date(2026, 9, 31, 17, 45));
    });
    afterEach(() => { vi.useRealTimers(); });

    it("says Next tomorrow in the afternoon, and a run still due today with its time", async () => {
        mocks.detail.mockResolvedValue(detail({ time: "07:00", nextRunAt: new Date(2026, 10, 1, 7, 0).getTime() }));
        const { unmount } = render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        expect(await screen.findByTestId("brief-detail-schedule")).toHaveTextContent("Every day, 7:00 AM · Next tomorrow");
        unmount();
        mocks.detail.mockResolvedValue(detail({ slot: "evening", time: "18:00", nextRunAt: new Date(2026, 9, 31, 18, 0).getTime() }));
        render(<BriefDetailPane slot="evening" onClose={vi.fn()} />);
        expect(await screen.findByTestId("brief-detail-schedule")).toHaveTextContent("Every day, 6:00 PM · Next today, 6:00 PM");
    });

    it("lists today's late run first with its reason, or without one", async () => {
        mocks.detail.mockResolvedValue(detail({ time: "07:00", runs: [
            { briefId: "b-late", status: "ok", scheduledAt: today(7), at: today(9, 16), late: true, reason: "asleep" },
            { briefId: "b-yesterday", status: "ok", scheduledAt: at(-1, 7, 0).toISOString(), at: at(-1, 7, 1).toISOString() },
        ] }));
        const { unmount } = render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        const late = await waitFor(() => { const el = document.querySelector('[data-brief-run="b-late"]'); expect(el).not.toBeNull(); return el as HTMLElement; });
        expect(document.querySelectorAll("[data-brief-run]")[0]).toBe(late);
        expect(late).toHaveTextContent("Today, 9:16 AM");
        expect(within(late).getByRole("button", { name: "Open on Today" })).toBeInTheDocument();
        expect(late.querySelector("[data-brief-run-line]")).toHaveTextContent("Came at 9:16 AM, not 7:00 AM: your computer was asleep.");
        expect(late.querySelector("[data-brief-run-line]")?.className).toContain("text-muted");
        unmount();
        mocks.detail.mockResolvedValue(detail({ time: "07:00", runs: [{ briefId: "b-late", status: "ok", scheduledAt: today(7), at: today(9, 16), late: true }] }));
        const plain = render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        await waitFor(() => expect(document.querySelector('[data-brief-run="b-late"] [data-brief-run-line]')).toHaveTextContent(/^Came at 9:16 AM, not 7:00 AM\.$/));
        plain.unmount();
        mocks.detail.mockResolvedValue(detail({ time: "07:00", runs: [{ briefId: "b-late", status: "ok", scheduledAt: today(7), at: today(9, 16), late: true, reason: "closed" }] }));
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        await waitFor(() => expect(document.querySelector('[data-brief-run="b-late"] [data-brief-run-line]')).toHaveTextContent("Came at 9:16 AM, not 7:00 AM: Isoform wasn't open."));
    });

    it("lists a brief made with Make it now, then the scheduled run with why it didn't run", async () => {
        const onOpenToday = vi.fn();
        mocks.detail.mockResolvedValue(detail({ time: "07:00", runs: [
            { briefId: "b-now", status: "ok", scheduledAt: today(7), at: today(9, 16), madeNow: true },
            { briefId: "b-slot", status: "missed", scheduledAt: today(7), at: "", reason: "closed" },
            { briefId: "b-yesterday", status: "ok", scheduledAt: at(-1, 7, 0).toISOString(), at: at(-1, 7, 1).toISOString() },
        ] }));
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} onOpenToday={onOpenToday} />);
        const made = await waitFor(() => { const el = document.querySelector('[data-brief-run="b-now"]'); expect(el).not.toBeNull(); return el as HTMLElement; });
        expect(made).toHaveTextContent("Today, 9:16 AM");
        expect(made.querySelector("[data-brief-run-line]")).toHaveTextContent("Made with Make it now at 9:16 AM.");
        fireEvent.click(within(made).getByRole("button", { name: "Open on Today" }));
        expect(onOpenToday).toHaveBeenCalledWith("b-now");
        expect(document.querySelector('[data-brief-run="b-slot"]')).toHaveTextContent("TodayDidn't run: Isoform was closed at 7:00 AM.");
        expect(Array.from(document.querySelectorAll("[data-brief-run]")).map((el) => el.getAttribute("data-brief-run"))).toEqual(["b-now", "b-slot", "b-yesterday"]);
    });

    it("says all of it in zh-CN", async () => {
        await i18n.changeLanguage("zh-CN");
        mocks.detail.mockResolvedValue(detail({ time: "07:00", nextRunAt: new Date(2026, 10, 1, 7, 0).getTime(), runs: [
            { briefId: "b-late", status: "ok", scheduledAt: today(7), at: today(9, 16), late: true, reason: "asleep" },
            { briefId: "b-plain", status: "ok", scheduledAt: at(-1, 7, 0).toISOString(), at: at(-1, 9, 16).toISOString(), late: true },
            { briefId: "b-now", status: "ok", scheduledAt: at(-2, 7, 0).toISOString(), at: at(-2, 9, 16).toISOString(), madeNow: true },
        ] }));
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        expect(await screen.findByTestId("brief-detail-schedule")).toHaveTextContent("每天上午7:00 · 下次：明天");
        await waitFor(() => expect(document.querySelector('[data-brief-run="b-late"] [data-brief-run-line]')).toHaveTextContent("上午9:16才生成，不是7:00：电脑在睡眠。"));
        expect(document.querySelector('[data-brief-run="b-plain"] [data-brief-run-line]')).toHaveTextContent("上午9:16才生成，不是7:00。");
        expect(document.querySelector('[data-brief-run="b-now"] [data-brief-run-line]')).toHaveTextContent("上午9:16用“现在生成”生成。");
        expect(i18n.t("app:briefDetail.nextTodayAt", { time: "下午6:00" })).toBe("下次：今天下午6:00");
        expect(i18n.t("app:briefDetail.lateReason.closed")).toBe("Isoform 没有打开");
    });

    it.each([
        ["en", 6, "Today, 7:30 AM", "Tried 6 times without finishing. Next try 8:37 AM."],
        ["en", 0, "Today, 7:30 AM", "Didn't finish. Next try 8:37 AM."],
        ["zh-CN", 6, "今天上午7:30", "试了 6 次都没完成。下次在上午8:37。"],
    ])("puts today's failed run first with what the record proves in %s (tasks-review v1.0.8 #2)", async (language, attempts, title, line) => {
        await i18n.changeLanguage(language);
        mocks.detail.mockResolvedValue(detail({ runs: [
            { briefId: "b-failed", status: "failed", scheduledAt: at(0).toISOString(), at: "", attempts, nextTryAt: at(0, 8, 37).toISOString() },
            ...detail().runs.slice(1),
        ] }));
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        const list = await screen.findByRole("region", { name: language === "en" ? "The last three" : undefined });
        await waitFor(() => expect(list.querySelectorAll("[data-brief-run]")).toHaveLength(3));
        const first = list.querySelector("[data-brief-run]") as HTMLElement;
        expect(first.dataset.briefRun).toBe("b-failed");
        expect(first.dataset.briefRunKind).toBe("failed");
        expect(first).toHaveTextContent(title);
        expect(first).toHaveTextContent(line);
        expect(first).not.toHaveTextContent(/score|rank|step/i);
    });

    it("says the tries without a next try once none is scheduled", async () => {
        mocks.detail.mockResolvedValue(detail({ runs: [
            { briefId: "b-failed", status: "failed", scheduledAt: at(0).toISOString(), at: "", attempts: 6 },
            { briefId: "b-kept-failing", status: "missed", scheduledAt: at(-1).toISOString(), at: at(-1).toISOString(), attempts: 3 },
        ] }));
        render(<BriefDetailPane slot="morning" onClose={vi.fn()} />);
        const list = await screen.findByRole("region", { name: "The last three" });
        await waitFor(() => expect(list.querySelectorAll("[data-brief-run]")).toHaveLength(2));
        expect(list.querySelector('[data-brief-run="b-failed"]')).toHaveTextContent("Tried 6 times without finishing.");
        expect(list.querySelector('[data-brief-run="b-failed"]')).not.toHaveTextContent("Next try");
        expect(list.querySelector('[data-brief-run="b-kept-failing"]')).toHaveTextContent("Tried 3 times without finishing.");
    });
});
