import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { afterEach, describe, expect, it, vi } from "vitest";
import en from "@/i18n/locales/en/today.json";
import zh from "@/i18n/locales/zh-CN/today.json";
import type { TodayBeforeYouTalk } from "@/lib/wails-shim-app";
import { TodayBeforeYouTalkCard } from "./TodayBeforeYouTalkCard";
import type { TodayActionHandlers } from "./types";

const recordSection = vi.hoisted(() => vi.fn(async () => undefined));
const recordAction = vi.hoisted(() => vi.fn(async () => undefined));
const openURL = vi.hoisted(() => vi.fn());
vi.mock("@/lib/wails-shim-app", () => ({ RecordTodaySectionSeen: recordSection, RecordTodayAction: recordAction }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({ BrowserOpenURL: openURL }));
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); recordSection.mockClear(); recordAction.mockClear(); openURL.mockClear(); });

const card: TodayBeforeYouTalk = {
    meetingId: "coffee-2026-10-24",
    meetingTitle: "Coffee",
    startAt: new Date(2026, 9, 24, 15, 0).getTime(),
    endAt: new Date(2026, 9, 24, 15, 30).getTime(),
    personId: "mei",
    personName: "Mei Chen",
    youOwe: "The Halcyon launch memo outline.",
    theyAsked: "Whether the sync can move to Oct 30.",
    hasUnsentDraft: true,
    lastTime: "You agreed to a staging preview.",
    lastTimeAt: new Date(2026, 9, 14, 10, 0).getTime(),
    lastDurationMs: 40 * 60_000,
};

function handlers() {
    return {
        onStartChat: vi.fn(),
        onRunNow: vi.fn(),
        onOpenMemory: vi.fn(),
        onOpenAutomations: vi.fn(),
        onOpenActivity: vi.fn(),
        onOpenPeople: vi.fn(),
    } satisfies TodayActionHandlers;
}

async function renderCard(language = "en", value: TodayBeforeYouTalk = card, onDismiss = vi.fn(async () => undefined), onMute?: () => Promise<void>) {
    const i18n = createInstance();
    await i18n.init({ lng: language, fallbackLng: false, resources: { en: { today: en }, "zh-CN": { today: zh } } });
    const actions = handlers();
    render(<I18nextProvider i18n={i18n}><TodayBeforeYouTalkCard card={value} handlers={actions} onDismiss={onDismiss} onMute={onMute} /></I18nextProvider>);
    return { actions, onDismiss, copy: language === "en" ? en.beforeYouTalk : zh.beforeYouTalk };
}

describe("TodayBeforeYouTalkCard", () => {
    it.each([["en", "Open Mei Chen in Who you know"], ["zh-CN", "在「你认识的人」中打开 Mei Chen"]])("names the person destination in %s", async (language, label) => {
        const { actions } = await renderCard(language);
        fireEvent.click(screen.getByRole("button", { name: label }));
        expect(actions.onOpenPeople).toHaveBeenCalledWith("mei");
    });

    it("mutes the meeting prep kind from the menu", async () => {
        const mute = vi.fn(async () => undefined);
        await renderCard("en", card, vi.fn(async () => undefined), mute);
        fireEvent.pointerDown(screen.getByRole("button", { name: "More options" }), { button: 0, pointerType: "mouse" });
        fireEvent.click(await screen.findByRole("menuitem", { name: "Mute this kind" }));
        await waitFor(() => expect(mute).toHaveBeenCalledOnce());
        await waitFor(() => expect(recordAction).toHaveBeenCalledWith("today", "T44", "do", "done", expect.any(Number)));
    });

    it("keeps the meeting visible when muting fails", async () => {
        const mute = vi.fn(async () => { throw new Error("offline"); });
        await renderCard("en", card, vi.fn(async () => undefined), mute);
        fireEvent.pointerDown(screen.getByRole("button", { name: "More options" }), { button: 0, pointerType: "mouse" });
        fireEvent.click(await screen.findByRole("menuitem", { name: "Mute this kind" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Didn't save. Try again");
        expect(screen.getByTestId("before-you-talk")).toBeInTheDocument();
    });

    it("offers Join from ten minutes before until the meeting ends", async () => {
        vi.useFakeTimers();
        const now = new Date(2026, 9, 24, 14, 0);
        vi.setSystemTime(now);
        await renderCard("en", { ...card, startAt: now.getTime() + 11 * 60_000, endAt: now.getTime() + 41 * 60_000, conferenceUrl: "https://meet.halcyon.example/room-123" });
        expect(screen.queryByRole("button", { name: en.todayBrief.join })).not.toBeInTheDocument();
        act(() => vi.advanceTimersByTime(60_000));
        fireEvent.click(screen.getByRole("button", { name: en.todayBrief.join }));
        expect(openURL).toHaveBeenCalledWith("https://meet.halcyon.example/room-123");
        expect(recordAction).toHaveBeenCalledWith("today", "T20", "open", "opened", expect.any(Number));
        act(() => vi.advanceTimersByTime(40 * 60_000));
        expect(screen.queryByRole("button", { name: en.todayBrief.join })).not.toBeInTheDocument();
    });

    it("does not offer Join for an unsafe link", async () => {
        const now = Date.now();
        await renderCard("en", { ...card, startAt: now + 60_000, endAt: now + 30 * 60_000, conferenceUrl: "javascript:alert(1)" });
        expect(screen.queryByRole("button", { name: en.todayBrief.join })).not.toBeInTheDocument();
    });
    it("records the visible card as one Before you talk row", async () => {
        await renderCard();
        expect(recordSection).toHaveBeenCalledWith("before_you_talk", 1);
    });
    it("shows the meeting, person and all three lines, with draft state and no Send action", async () => {
        const { actions, copy } = await renderCard();
        const section = screen.getByTestId("before-you-talk");
        expect(section).toHaveTextContent("Coffee");
        expect(section).toHaveTextContent("Mei Chen");
        expect(section).toHaveTextContent(card.youOwe!);
        expect(section).toHaveTextContent("You have a draft reply you have not sent.");
        expect(section).toHaveTextContent("40 minutes");
        expect(section).toHaveTextContent(card.lastTime!);
        expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: copy.openPerson.replace("{{name}}", card.personName) }));
        expect(actions.onOpenPeople).toHaveBeenCalledWith("mei");
    });

    it("fills both requests with calendar context and never sends", async () => {
        const { actions, copy } = await renderCard();
        fireEvent.click(screen.getByRole("button", { name: copy.briefMe }));
        expect(actions.onStartChat).toHaveBeenLastCalledWith(expect.stringContaining("Mei Chen"), { sourceKind: "calendar", sourceId: card.meetingId });
        fireEvent.click(screen.getByRole("button", { name: copy.draftNote.replace("{{firstName}}", "Mei") }));
        expect(actions.onStartChat).toHaveBeenLastCalledWith(expect.stringContaining("Draft a short note"), { sourceKind: "calendar", sourceId: card.meetingId });
        expect(actions.onStartChat).toHaveBeenCalledTimes(2);
    });

    it("opens the attendee note in its draft panel instead of preparing the composer", async () => {
        const i18n = createInstance();
        await i18n.init({ lng: "en", resources: { en: { today: en } } });
        const base = handlers();
        const onDraftMeetingNote = vi.fn();
        render(<I18nextProvider i18n={i18n}><TodayBeforeYouTalkCard card={card} handlers={{ ...base, onDraftMeetingNote }} onDismiss={vi.fn(async () => undefined)} /></I18nextProvider>);
        fireEvent.click(screen.getByRole("button", { name: "Draft a note to Mei" }));
        expect(onDraftMeetingNote).toHaveBeenCalledWith(card, expect.stringContaining("Draft a short note"));
        expect(base.onStartChat).not.toHaveBeenCalled();
    });

    it("omits every empty line and shows no empty labels", async () => {
        await renderCard("en", { ...card, youOwe: undefined, theyAsked: undefined, hasUnsentDraft: false, lastTime: undefined });
        expect(screen.queryByText(en.beforeYouTalk.youOwe)).not.toBeInTheDocument();
        expect(screen.queryByText(en.beforeYouTalk.theyAsked)).not.toBeInTheDocument();
        expect(screen.queryByText(en.beforeYouTalk.lastTime)).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: en.beforeYouTalk.briefMe })).toBeInTheDocument();
    });

    it("keeps the card visible and reports a failed dismissal", async () => {
        const onDismiss = vi.fn(async () => { throw new Error("offline"); });
        await renderCard("en", card, onDismiss);
        fireEvent.click(screen.getByRole("button", { name: en.beforeYouTalk.notNow }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Didn't save. Try again");
        expect(zh.beforeYouTalk.dismissFailed).toBe("没有保存。重试");
        expect(screen.getByTestId("before-you-talk")).toBeInTheDocument();
        expect(onDismiss).toHaveBeenCalledWith(card.meetingId);
        expect(recordAction).toHaveBeenCalledWith("today", "T44", "do", "failed", expect.any(Number));
    });

    it("records a completed dismissal only after it saves", async () => {
        let finish!: () => void;
        await renderCard("en", card, vi.fn(() => new Promise<void>((resolve) => { finish = resolve; })));
        fireEvent.click(screen.getByRole("button", { name: en.beforeYouTalk.notNow }));
        expect(recordAction).not.toHaveBeenCalled();
        await act(async () => finish());
        expect(recordAction).toHaveBeenCalledOnce();
        expect(recordAction).toHaveBeenCalledWith("today", "T44", "do", "done", expect.any(Number));
    });

    it("locks Not now while dismissal is in flight", async () => {
        let finish!: () => void;
        const onDismiss = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
        await renderCard("en", card, onDismiss);
        const button = screen.getByRole("button", { name: en.beforeYouTalk.notNow });
        fireEvent.click(button);
        await waitFor(() => expect(button).toBeDisabled());
        expect(button).toHaveAttribute("aria-busy", "true");
        finish();
        await waitFor(() => expect(button).not.toBeDisabled());
    });

    it("renders its controls without the retired composer helper in zh-CN", async () => {
        const { copy } = await renderCard("zh-CN");
        expect(screen.getByRole("region", { name: copy.label })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: copy.briefMe })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: copy.draftNote.replace("{{firstName}}", "Mei") })).toBeInTheDocument();
        expect(screen.queryByText(/每个按钮都会填进右侧输入框/)).not.toBeInTheDocument();
    });
});
