import { render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { FindTimeAnswer } from "./find-time-answer";

const getFindTime = vi.hoisted(() => vi.fn());
const getRange = vi.hoisted(() => vi.fn());
vi.mock("@/lib/wails-shim-app", () => ({ GetFindTimeDetails: getFindTime, GetFindTimeDetailsForRange: getRange }));

beforeEach(async () => {
    await i18n.changeLanguage("en");
    getFindTime.mockReset();
    getRange.mockReset();
});

it.each(["en", "zh-CN"])("answers in words without an action in %s", async (language) => {
    await i18n.changeLanguage(language);
    getFindTime.mockResolvedValue({ ranges: [
        { start: "2026-10-30T09:30:00Z", end: "2026-10-30T11:00:00Z" },
        { start: "2026-11-02T09:00:00Z", end: "2026-11-02T11:00:00Z" },
    ], checkedCalendars: [], browserOnly: [] });
    render(<FindTimeAnswer />);
    const answer = await screen.findByText(language === "en" ? /You're free/ : /你.*有空/);
    expect(answer).toHaveTextContent(language === "en" ? /Friday/ : /星期五/);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
});

it.each(["en", "zh-CN"])("names checked calendars and the browser-only calendar in %s", async (language) => {
    await i18n.changeLanguage(language);
    getFindTime.mockResolvedValue({ ranges: [{ start: "2026-10-30T09:30:00Z", end: "2026-10-30T11:00:00Z" }], checkedCalendars: ["Work", "Home"], browserOnly: ["Google"] });
    render(<FindTimeAnswer />);
    const answer = await screen.findByText(/Google/);
    expect(answer).toHaveTextContent("Work");
    expect(answer).toHaveTextContent("Home");
    expect(answer).toHaveTextContent("Google");
    expect(answer).toHaveTextContent(language === "en" ? "only in your browser" : "只在浏览器里");
});

it("does not claim free time when the read fails", async () => {
    getFindTime.mockRejectedValue(new Error("unavailable"));
    render(<FindTimeAnswer />);
    expect(await screen.findByText("Couldn't check your calendar. Try again later.")).toBeInTheDocument();
    expect(screen.queryByText(/You're free/)).not.toBeInTheDocument();
});


it.each(["en", "zh-CN"])("uses the requested week and reports an empty week in %s", async (language) => {
    await i18n.changeLanguage(language);
    getRange.mockResolvedValue({ ranges: [], checkedCalendars: [], browserOnly: [] });
    render(<FindTimeAnswer range={{ first: "2026-11-02", last: "2026-11-06", minutes: 60, nextWeek: true }} />);
    expect(await screen.findByText(language === "en" ? "Next week has no free block of an hour or more." : "下周没有一小时以上的空闲时间。")).toBeInTheDocument();
    expect(getRange).toHaveBeenCalledWith("2026-11-02", "2026-11-06", 60);
});

it("names the checked calendars even when the requested week has no free block", async () => {
    getRange.mockResolvedValue({ ranges: [], checkedCalendars: ["Work"], browserOnly: ["Google"] });
    render(<FindTimeAnswer range={{ first: "2026-11-02", last: "2026-11-06", minutes: 60, nextWeek: true }} />);
    expect(await screen.findByText(/Work/)).toHaveTextContent("Google");
});
