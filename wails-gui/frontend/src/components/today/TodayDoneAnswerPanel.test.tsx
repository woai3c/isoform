import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { beforeEach, expect, it, vi } from "vitest";
import en from "@/i18n/locales/en/today.json";
import common from "@/i18n/locales/en/common.json";
import type { TodayDone } from "@/lib/wails-shim-app";
const mocks = vi.hoisted(() => ({ read: vi.fn(), mark: vi.fn(), withdraw: null as (() => void) | null }));
vi.mock("@/lib/wails-shim-app", () => ({ ReadTodayDoneAnswer: mocks.read, MarkTodayDoneOpened: mocks.mark }));
vi.mock("@/lib/page-privacy-withdrawal", () => ({ onPagePrivacyWithdrawal: (fn: () => void) => { mocks.withdraw = fn; return () => { mocks.withdraw = null; }; } }));
vi.mock("@/components/markdown-content", () => ({ MarkdownContent: ({ content }: { content: string }) => <div>{content}</div> }));
import { TodayDoneAnswerPanel } from "./TodayDoneAnswerPanel";
const item: TodayDone = { id: "answer:one", kind: "answer_ready", sentence: "An answer", at: 0, targetKind: "chat_item", targetId: "one" };
async function show() {
    const i18n = createInstance(); await i18n.init({ lng: "en", resources: { en: { today: en, common } }, defaultNS: "today" });
    const close = vi.fn(); render(<I18nextProvider i18n={i18n}><TodayDoneAnswerPanel item={item} onClose={close} /></I18nextProvider>); return close;
}
beforeEach(() => { mocks.read.mockReset(); mocks.mark.mockReset().mockResolvedValue(undefined); mocks.withdraw = null; });
it("reads the exact answer before marking it opened", async () => {
    mocks.read.mockResolvedValue({ id: item.id, chatId: "chat", content: "The full answer." });
    await show();
    expect(await screen.findByText("The full answer.")).toBeInTheDocument();
    await waitFor(() => expect(mocks.mark).toHaveBeenCalledWith(item.id));
    expect(mocks.read).toHaveBeenCalledWith(item.id);
});
it("shows an in-place retry and does not mark a failed read", async () => {
    mocks.read.mockRejectedValueOnce(new Error("unavailable")).mockResolvedValueOnce({ id: item.id, chatId: "chat", content: "Recovered." });
    await show();
    fireEvent.click(await screen.findByRole("button", { name: "Couldn’t open the answer. Try again" }));
    expect(await screen.findByText("Recovered.")).toBeInTheDocument();
    expect(mocks.mark).toHaveBeenCalledTimes(1);
});
it("withdraws the panel before a late response can be shown", async () => {
    let finish!: (value: unknown) => void;
    mocks.read.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    const close = await show();
    act(() => mocks.withdraw?.());
    await act(async () => finish({ id: item.id, content: "Withdrawn words" }));
    expect(close).toHaveBeenCalledOnce();
    expect(screen.queryByText("Withdrawn words")).toBeNull();
    expect(mocks.mark).not.toHaveBeenCalled();
});
