import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { beforeEach, expect, it, vi } from "vitest";
import en from "@/i18n/locales/en/today.json";
import zh from "@/i18n/locales/zh-CN/today.json";
import { TodayPauseLine } from "./TodayPauseLine";

const mocks = vi.hoisted(() => ({ since: vi.fn(), resume: vi.fn(), record: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ GetTodayObservationPauseSince: mocks.since, ResumeObservation: mocks.resume, RecordTodayAction: mocks.record }));
beforeEach(() => { vi.clearAllMocks(); mocks.since.mockResolvedValue(new Date(2026, 9, 30, 9, 12).getTime()); mocks.resume.mockResolvedValue(undefined); mocks.record.mockResolvedValue(undefined); });
async function show(language: string) {
    const i18n = createInstance();
    await i18n.init({ lng: language, resources: { en: { today: en }, "zh-CN": { today: zh } }, defaultNS: "today" });
    render(<I18nextProvider i18n={i18n}><TodayPauseLine /></I18nextProvider>);
}
it("shows the pause time and resumes in place with content-free telemetry", async () => {
    await show("en");
    expect(await screen.findByText(/Paused since/)).toHaveTextContent("9:12");
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(mocks.resume).toHaveBeenCalledOnce());
    await waitFor(() => expect(mocks.record).toHaveBeenCalledWith("today", "T87", "do", "done", expect.any(Number)));
});
it("shows a retry in Chinese when resuming fails", async () => {
    mocks.resume.mockRejectedValueOnce(new Error("failed"));
    await show("zh-CN");
    expect(await screen.findByText(/自.*起暂停/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "恢复" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("没能恢复观察。重试");
    expect(mocks.record).toHaveBeenCalledWith("today", "T87", "do", "failed", expect.any(Number));
    fireEvent.click(screen.getByRole("button", { name: "恢复" }));
    await waitFor(() => expect(mocks.resume).toHaveBeenCalledTimes(2));
});
it('uses the unadorned Today link style', async () => {
    await show('en');
    const resume = screen.getByRole('button', { name: 'Resume' });
    expect(resume).toHaveClass('text-accent');
    expect(resume).not.toHaveClass('underline');
});

it.each([["en", "Observing is paused", "Resume"], ["zh-CN", "观察已暂停", "恢复"]])("shows an unknown pause without inventing a time in %s", async (language, message, resume) => {
    mocks.since.mockResolvedValue(0);
    await show(language);
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: resume })).toBeInTheDocument();
});
