import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/chat.json";
import enToday from "@/i18n/locales/en/today.json";
import { MainChatBriefCard } from "./main-chat-brief-card";

const getByID = vi.hoisted(() => vi.fn());
vi.mock("@/lib/wails-shim-app", () => ({
    GetTodayBriefByID: (...args: unknown[]) => getByID(...args),
    GetMobileBriefAudio: vi.fn(),
    GetMobileBriefHealth: vi.fn(),
}));

function view(brief: Record<string, unknown> | null) {
    return { brief, building: false, enabled: true, nextAt: "", nextSlot: "", empty: false, missing: [] };
}

const withWords = {
    briefId: "brief-1", slot: "evening", asOf: "2026-10-28T18:00:00Z", timezone: "UTC", narrativeStatus: "ok",
    narrative: [{ text: "Quiet day. Priya is waiting on your review." }],
    audio: { mime: "audio/wav", bytes: 10, durationSeconds: 90 },
};

describe("MainChatBriefCard", () => {
    beforeEach(() => {
        getByID.mockReset();
        i18n.changeLanguage("en");
    });

    it('updates a Friday due line on Monday and does not play stale audio', async () => {
        vi.useFakeTimers({ toFake: ['Date'] });
        vi.setSystemTime(new Date('2026-11-02T12:00:00-08:00'));
        try {
            getByID.mockResolvedValue(view({
                ...withWords, briefId: '2026-10-30-evening', asOf: '2026-10-30T18:00:00-07:00', timezone: 'America/Los_Angeles',
                sections: { commitments: [{ refs: ['due'], dueAt: '2026-10-30T23:59:00-07:00' }] },
                narrative: [{ text: 'The commitment is due today.', refs: ['due'] }],
            }));
            render(<MainChatBriefCard briefId="2026-10-30-evening" onOpenBrief={vi.fn()} />);
            expect(await screen.findByText('The commitment was due Friday.')).toBeInTheDocument();
            expect(screen.queryByRole('button', { name: en.mainChat.card.button.play })).toBeNull();
            expect(screen.getByRole('button', { name: en.mainChat.card.button.readIt })).toBeInTheDocument();
        } finally {
            vi.useRealTimers();
        }
    });

    it("shows a title while the brief loads", async () => {
        let finish!: (result: ReturnType<typeof view>) => void;
        getByID.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
        render(<MainChatBriefCard briefId="brief-1" display={{ slot: 'evening', asOf: '2026-10-28T18:00:00Z', timezone: 'UTC' }} />);
        expect(screen.getByTestId("main-chat-brief-card")).toHaveAttribute("aria-busy", "true");
        expect(screen.getByRole("heading", { name: "Evening brief · 6:00 PM" })).toBeInTheDocument();
        await waitFor(() => expect(getByID).toHaveBeenCalled());
        finish(view(withWords));
        expect(await screen.findByRole("heading", { name: "Evening brief · 6:00 PM" })).toBeInTheDocument();
    });
    it('keeps the recorded title, source and Read it if the brief read fails', async () => {
        getByID.mockRejectedValue(new Error('offline'));
        render(<MainChatBriefCard briefId="brief-1" display={{ slot: 'morning', asOf: '2026-10-28T09:23:00Z', timezone: 'UTC' }} onOpenBrief={vi.fn()} meta={() => <span>From your brief</span>} />);
        expect(screen.getByRole('heading', { name: 'Morning brief · 9:23 AM' })).toBeInTheDocument();
        await screen.findByRole('alert');
        expect(screen.getByText('From your brief')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Read it' })).toBeInTheDocument();
        expect(screen.getByTestId('main-chat-brief-card')).not.toHaveClass('min-h-32');
    });
    it('recovers when a slow brief read finishes after its timeout', async () => {
        vi.useFakeTimers();
        try {
            let finish!: (result: ReturnType<typeof view>) => void;
            getByID.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
            render(<MainChatBriefCard briefId="brief-1" display={{ slot: 'evening', asOf: '2026-10-28T18:00:00Z', timezone: 'UTC' }} onOpenBrief={vi.fn()} />);
            await vi.advanceTimersByTimeAsync(10000);
            expect(screen.getByRole('alert')).toBeInTheDocument();
            expect(finish).toBeDefined();
            await act(async () => finish(view(withWords)));
            expect(screen.queryByRole('alert')).toBeNull();
            expect(screen.getByText('Quiet day. Priya is waiting on your review.')).toBeInTheDocument();
            expect(screen.getByRole('button', { name: 'Read it' })).toBeInTheDocument();
        } finally {
            vi.useRealTimers();
        }
    });

    it("shows the title and opening sentence, and Read it opens the brief in the card", async () => {
        getByID.mockResolvedValue(view(withWords));
        const onReadAll = vi.fn();
        render(<MainChatBriefCard briefId="brief-1" onOpenBrief={onReadAll} meta={() => <span>From brief</span>} />);
        expect(await screen.findByRole("heading", { name: "Evening brief · 6:00 PM" })).toBeInTheDocument();
        expect(screen.getByText("Quiet day. Priya is waiting on your review.")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: en.mainChat.card.button.play })).toBeInTheDocument();
        expect(screen.getByText("From brief").compareDocumentPosition(screen.getByRole("button", { name: en.mainChat.card.button.play })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.card.button.readIt }));
        expect(onReadAll).not.toHaveBeenCalled();
        expect(screen.queryByRole("button", { name: en.mainChat.card.button.readIt })).toBeNull();
    });

    it("hides Play when the brief has no recording", async () => {
        getByID.mockResolvedValue(view({ ...withWords, audio: undefined }));
        render(<MainChatBriefCard briefId="brief-1" onOpenBrief={vi.fn()} />);
        await screen.findByRole("heading", { name: "Evening brief · 6:00 PM" });
        expect(screen.queryByRole("button", { name: en.mainChat.card.button.play })).toBeNull();
        expect(screen.getByRole("button", { name: en.mainChat.card.button.readIt })).toBeInTheDocument();
    });

    it('does not carry a previous brief or its expanded state to another id', async () => {
        getByID.mockResolvedValueOnce(view(withWords)).mockReturnValueOnce(new Promise(() => {}));
        const component = render(<MainChatBriefCard briefId="brief-1" onOpenBrief={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Read it' }));
        component.rerender(<MainChatBriefCard briefId="brief-2" display={{ slot: 'morning', asOf: '2026-10-29T09:23:00Z', timezone: 'UTC' }} onOpenBrief={vi.fn()} />);
        expect(screen.queryByText('Quiet day. Priya is waiting on your review.')).toBeNull();
        expect(screen.getByRole('heading', { name: 'Morning brief · 9:23 AM' })).toBeInTheDocument();
    });

    it("never plays a recording without its words", async () => {
        getByID.mockResolvedValue(view({ ...withWords, narrative: [] }));
        render(<MainChatBriefCard briefId="brief-1" onOpenBrief={vi.fn()} />);
        await screen.findByRole("heading", { name: "Evening brief · 6:00 PM" });
        expect(screen.queryByRole("button", { name: en.mainChat.card.button.play })).toBeNull();
    });

    it("never shows another brief or a provisional headline when the id does not match", async () => {
        getByID.mockResolvedValue(view({ ...withWords, briefId: "brief-9" }));
        render(<MainChatBriefCard briefId="brief-1" onOpenBrief={vi.fn()} />);
        await waitFor(() => expect(getByID).toHaveBeenCalledWith("brief-1"));
        expect(screen.getByRole("heading", { name: "Brief" })).toBeInTheDocument();
        expect(await screen.findByRole("alert")).toHaveTextContent(enToday.todayBrief.pending.openFailed);
        getByID.mockResolvedValue(view(withWords));
        fireEvent.click(screen.getByRole("button", { name: enToday.todayBrief.pending.tryAgain }));
        expect(await screen.findByRole("heading", { name: "Evening brief · 6:00 PM" })).toBeInTheDocument();
    });
    it.each([
        ['en', 'Evening brief'],
        ['zh-CN', '晚间简报'],
    ])('keeps the known title, source and actions after a failed read in %s', async (language, title) => {
        await i18n.changeLanguage(language);
        getByID.mockRejectedValue(new Error('unavailable'));
        render(<MainChatBriefCard briefId="2026-10-30-evening" onOpenBrief={vi.fn()} meta={() => <span>source</span>} />);
        expect(screen.getByRole('heading', { name: title })).toBeInTheDocument();
        await screen.findByRole('alert');
        expect(screen.getByRole('heading', { name: title })).toBeInTheDocument();
        expect(screen.getByText('source')).toBeInTheDocument();
        expect(screen.getAllByRole('button').length).toBeGreaterThanOrEqual(2);
    });
    it("opens the whole saved brief at once when Read it is pressed while it loads (today-review v1.1.19 R27)", async () => {
        let finish!: (result: ReturnType<typeof view>) => void;
        getByID.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
        const open = vi.fn();
        render(<MainChatBriefCard briefId="brief-1" display={{ slot: 'evening', asOf: '2026-10-28T18:00:00Z', timezone: 'UTC' }} onOpenBrief={open} />);
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.card.button.readIt }));
        expect(screen.queryByRole("button", { name: en.mainChat.card.button.readIt })).toBeNull();
        await act(async () => finish(view({ ...withWords, narrative: [{ text: "Quiet day." }, { text: "Priya is waiting on your review." }] })));
        expect(screen.getByText("Quiet day.")).toBeInTheDocument();
        expect(screen.getByText("Priya is waiting on your review.")).toBeInTheDocument();
        expect(open).not.toHaveBeenCalled();
    });
    it.each(["en", "zh-CN"])("says in copper that the brief couldn't open, with Try again, in %s (today-review v1.1.19 R27)", async (language) => {
        await i18n.changeLanguage(language);
        getByID.mockRejectedValueOnce(new Error("unavailable")).mockResolvedValue(view(withWords));
        render(<MainChatBriefCard briefId="brief-1" onOpenBrief={vi.fn()} />);
        const alert = await screen.findByRole("alert");
        expect(alert).toHaveClass("text-copper");
        expect(alert).toHaveTextContent(i18n.t("todayBrief.pending.openFailed", { ns: "today" }));
        fireEvent.click(screen.getByRole("button", { name: i18n.t("todayBrief.pending.tryAgain", { ns: "today" }) }));
        expect(await screen.findByText(withWords.narrative[0].text)).toBeInTheDocument();
        expect(screen.queryByRole("alert")).toBeNull();
    });
    it("opens the week's brief on Today from Read it, since its sections are not in the card", async () => {
        getByID.mockResolvedValue(view({ ...withWords, yourWeek: { days: [] } } as never));
        const onOpenBrief = vi.fn();
        render(<MainChatBriefCard briefId="brief-1" onOpenBrief={onOpenBrief} />);
        await screen.findByText("Quiet day. Priya is waiting on your review.");
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.card.button.readIt }));
        expect(onOpenBrief).toHaveBeenCalledTimes(1);
    });
});
