import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { TodayNewMessageDraft } from "./TodayNewMessageDraft";

const mocks = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn(), ask: vi.fn(), stop: vi.fn(), chat: { items: [] as Array<{ id: string; kind: string; content: string }>, pending: null, waiting: false, stopped: false, lastUserItemId: "", runId: "" } }));
vi.mock("@/lib/wails-shim-app", () => ({ GetTodayBriefDraft: mocks.get, SendTodayBriefDraft: mocks.send }));
vi.mock("@/hooks/useMainChatPanel", () => ({ useMainChatPanel: () => ({ ...mocks.chat, send: mocks.ask, stop: mocks.stop }) }));
const request = { id: 1, briefId: "b", cardId: "c", label: "Draft the update to Priya", message: "Draft the plan update for Priya.", newMessage: true };
const answer = { question: request.message, answer: "Hi Priya, here is the plan.", userItemId: "u", answerItemId: "a" };
const route = { personId: "p-priya", name: "Priya Raman", address: "priya@example.com", source: "mail" as const, sourceAt: new Date(2026, 9, 21, 10).getTime(), subject: "Q4 plan" };
beforeEach(async () => {
    await i18n.changeLanguage("en");
    mocks.get.mockReset(); mocks.ask.mockReset().mockResolvedValue("sent"); mocks.send.mockReset().mockResolvedValue(undefined); mocks.stop.mockReset().mockResolvedValue(undefined);
    mocks.chat.items = []; mocks.chat.lastUserItemId = ""; mocks.chat.waiting = false; mocks.chat.runId = "";
});
afterEach(() => vi.useRealTimers());

it.each([
    ["en", "To Priya Raman · priya@example.com · from your Mail, Oct 21", "Subject: Q4 plan"],
    ["zh-CN", "收件人 Priya Raman · priya@example.com · 来自你的邮件，10月21日", "Subject: Q4 plan"],
])("shows the proven address with its source line and Isoform's subject, ready to send, in %s", async (language, line, subject) => {
    await i18n.changeLanguage(language);
    mocks.get.mockResolvedValue({ answer, state: "ready", route, subject: "Q4 plan" });
    render(<TodayNewMessageDraft request={request} />);
    expect(await screen.findByText(line)).toBeInTheDocument();
    if (language === "en") expect(screen.getByText(subject)).toBeInTheDocument();
    expect(screen.getByText("Hi Priya, here is the plan.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: language === "en" ? "Send" : "发送" })).toBeEnabled();
    // The old form asked for a channel and an address; it is gone.
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});

it("names a Contacts source without a date", async () => {
    mocks.get.mockResolvedValue({ answer, state: "ready", route: { ...route, source: "contacts", sourceAt: undefined }, subject: "Q4 plan" });
    render(<TodayNewMessageDraft request={request} />);
    expect(await screen.findByText("To Priya Raman · priya@example.com · from your Contacts")).toBeInTheDocument();
});

it("sends only after Send, with the edited subject and text, and Undo cancels", async () => {
    mocks.get.mockResolvedValue({ answer, state: "ready", route, subject: "Q4 plan" });
    render(<TodayNewMessageDraft request={request} />);
    expect(await screen.findByText("Hi Priya, here is the plan.")).toBeInTheDocument();
    expect(mocks.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Subject" }), { target: { value: "Q4 plan, revised" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Note text" }), { target: { value: "My revision" } });
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await act(async () => vi.advanceTimersByTime(5000));
    expect(mocks.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await act(async () => vi.advanceTimersByTime(5000));
    expect(mocks.send).toHaveBeenCalledWith(expect.objectContaining({ briefDraft: true, sourceId: "c" }), "a", "Q4 plan, revised", "My revision");
    expect(mocks.ask).not.toHaveBeenCalled();
});

it("writes the draft from the card when none is saved, without sending", async () => {
    mocks.get.mockResolvedValueOnce(null).mockResolvedValueOnce({ answer: { ...answer, answerItemId: "a2" }, state: "ready", route, subject: "Q4 plan" });
    mocks.chat.lastUserItemId = "u";
    mocks.chat.items = [{ id: "u", kind: "user", content: request.message }, { id: "a2", kind: "assistant", content: "Hi Priya, here is the plan." }];
    render(<TodayNewMessageDraft request={request} />);
    await waitFor(() => expect(mocks.ask).toHaveBeenCalledWith(request.message, expect.objectContaining({ sourceKind: "brief_card", briefId: "b", sourceId: "c", briefDraft: true }), true));
    expect(await screen.findByText("To Priya Raman · priya@example.com · from your Mail, Oct 21")).toBeInTheDocument();
    expect(mocks.send).not.toHaveBeenCalled();
});
