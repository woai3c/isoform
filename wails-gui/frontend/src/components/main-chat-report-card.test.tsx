import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { computerClock } from "@/lib/brief-time";
import en from "@/i18n/locales/en/chat.json";
import zh from "@/i18n/locales/zh-CN/chat.json";
import {
    MAIN_CHAT_REPORT_TOOL,
    MAIN_CHAT_TEXT_REPLY_TOOL,
    SIDE_CHAT_ORIGIN_TOOL,
    UPCOMING_CONFIRM_TOOL,
    getTextedReply,
    isStandaloneSystemItem,
} from "@/lib/stream-helpers";
import type { StreamItem } from "@/types";
import {
    MainChatReportCard,
    SideChatOriginCard,
    TextedReplyCard,
    UpcomingConfirmCard,
    upcomingApprovalQuestion,
} from "./main-chat-report-card";
import { StreamCard } from "./stream-card";

// The wailsjs App module is aliased to the shim, so this one mock covers the
// card's ResolveUpcomingConfirm binding too.
const resolveUpcomingConfirm = vi.hoisted(() => vi.fn());

vi.mock("../../wailsjs/go/main/App", () => ({
    OpenAutomationDeliverable: vi.fn(),
    ResolveUpcomingConfirm: (...args: unknown[]) => resolveUpcomingConfirm(...args),
}));
vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: vi.fn(() => () => {}),
    EventsEmit: vi.fn(),
}));

function report(overrides: Record<string, unknown> = {}, content = "Finished \"Draft the deck\"."): StreamItem {
    return {
        id: "r1",
        kind: "system",
        content,
        toolName: MAIN_CHAT_REPORT_TOOL,
        toolInput: {
            source: "handoff",
            key: "handoff:hf_1:completed",
            action: "review",
            target: { kind: "session", id: "chat-1" },
            title: "Draft the deck",
            status: "completed",
            ...overrides,
        },
    };
}

describe("MainChatReportCard", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("renders the localized sentence and its one action", () => {
        const onOpen = vi.fn();
        render(<MainChatReportCard item={report()} onOpenTarget={onOpen} />);
        expect(screen.getByText(en.mainChat.report.handoff.completed.replace("{{title}}", "Draft the deck"))).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.action.review }));
        expect(onOpen).toHaveBeenCalledWith({ kind: "session", id: "chat-1", runId: undefined });
    });

    it.each([
        ["en", "mail"],
        ["zh-CN", "slack"],
    ] as const)("renders a localized sent receipt with its channel and time in %s", async (language, channel) => {
        await i18n.changeLanguage(language);
        const sentAt = new Date(2026, 9, 24, 14, 10).getTime();
        const copy = language === "en" ? en : zh;
        render(<MainChatReportCard item={report({
            source: "message_send", action: "open", target: { kind: "person", id: "person-dana" },
            channel, sentAt, principalId: "person-dana",
        }, "Stored fallback")} onOpenTarget={vi.fn()} />);
        const time = computerClock(new Date(sentAt), language);
        const expected = copy.mainChat.report.message_send.sent
            .replace("{{channel}}", copy.mainChat.channel[channel])
            .replace("{{time}}", time);
        expect(screen.getByText(expected)).toBeInTheDocument();
        expect(document.querySelector("[data-main-chat-report='message_send']")).not.toBeNull();
        expect(screen.queryByRole("button")).toBeNull();
        expect(screen.queryByText("Stored fallback")).toBeNull();
    });

    it("covers every producer's sentence in both locales", () => {
        const keys = ["handoff.active", "handoff.in_progress", "handoff.completed", "handoff.needs_review", "handoff.aborted", "handoff.blocked", "handoff.stale", "schedule.ok", "schedule.error", "meeting.ready", "side_chat.brought_back"];
        for (const key of keys) {
            const [source, status] = key.split(".");
            const reportSource = source as "handoff" | "schedule" | "meeting" | "side_chat";
            const enText = (en.mainChat.report[reportSource] as Record<string, string>)[status];
            const zhText = (zh.mainChat.report[reportSource] as Record<string, string>)[status];
            expect(enText, key).toContain("{{title}}");
            expect(zhText, key).toContain("{{title}}");
        }
        expect(Object.keys(zh.mainChat.action)).toEqual(Object.keys(en.mainChat.action));
        expect(Object.keys(zh.mainChat.origin)).toEqual(Object.keys(en.mainChat.origin));
    });

    it.each(["en", "zh-CN"])("renders all saved one-time work states in %s without a false completion", async (language) => {
        await i18n.changeLanguage(language);
        const copy = language === "en" ? en : zh;
        for (const status of ["active", "in_progress", "needs_review", "blocked", "completed", "aborted", "stale"] as const) {
            const view = render(<MainChatReportCard item={report({ status })} />);
            expect(screen.getByText(copy.mainChat.report.handoff[status].replace("{{title}}", "Draft the deck"))).toBeInTheDocument();
            if (status === "needs_review") {
                expect(screen.queryByText(/is done|已完成/)).toBeNull();
            }
            view.unmount();
        }
    });

    it("localizes the subject when the payload carries no title", () => {
        render(<MainChatReportCard item={report({ title: "" })} onOpenTarget={vi.fn()} />);
        expect(screen.getByText(en.mainChat.report.handoff.completed.replace("{{title}}", en.mainChat.fallback.handoff))).toBeInTheDocument();
        expect(Object.keys(zh.mainChat.fallback)).toEqual(Object.keys(en.mainChat.fallback));
    });

    it("hides the action for lines that point nowhere and falls back to the stored text", () => {
        const onOpen = vi.fn();
        render(
            <MainChatReportCard
                item={report({ source: "engagement", action: "", target: { kind: "none" }, status: "future_kind" }, "How is the launch going?")}
                onOpenTarget={onOpen}
            />,
        );
        expect(screen.getByText("How is the launch going?")).toBeInTheDocument();
        expect(screen.queryByRole("button")).toBeNull();
    });

    it.each(["en", "zh-CN"])("localizes engagement kinds and keeps goal subjects in %s", async (language) => {
        await i18n.changeLanguage(language);
        const copy = language === "en" ? en : zh;
        const { unmount } = render(
            <MainChatReportCard
                item={report({ source: "engagement", status: "goal_check_in", action: "", target: { kind: "none" }, params: { title: "Launch" } })}
            />,
        );
        expect(screen.getByText(copy.mainChat.report.engagement.goal_check_in.detail.replace("{{title}}", "Launch"))).toBeInTheDocument();
        expect(screen.queryByRole("button")).toBeNull();
        unmount();

        // Stored reports from before structured parameters use a localized
        // summary, never interpolate the old, already-rendered English title.
        for (const [kind, text] of Object.entries(copy.mainChat.report.engagement)) {
            const view = render(<MainChatReportCard item={report({ source: "engagement", status: kind })} />);
            const expected = kind === 'brief_ready' ? i18n.t('today:todayBrief.slot.on_request') : text.summary;
            expect(screen.getByText(expected), kind).toBeInTheDocument();
            view.unmount();
        }
    });

    it("uses generic mail copy even if a stored payload contains a subject", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<MainChatReportCard item={report({ source: "engagement", status: "waiting_on_reply", params: { name: "Alice", subject: "Private subject" } })} />);
        expect(screen.getByText(zh.mainChat.report.engagement.waiting_on_reply.summary)).toBeInTheDocument();
        expect(screen.queryByText(/Private subject/)).toBeNull();
    });

    it("keeps a meeting's title and time when location and attendees are absent", () => {
        render(<MainChatReportCard item={report({ source: "engagement", status: "meeting_prep", params: { title: "Planning", minutes: "5" } })} />);
        expect(screen.getByText(/Planning starts in 5 min/)).toBeInTheDocument();
        expect(screen.queryByText(/\{\{/)).toBeNull();
    });

    it("preserves the subject of legacy engagement reports under the localized heading", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<MainChatReportCard item={report({ source: "engagement", status: "goal_check_in", action: "", target: { kind: "none" } }, "How is the launch going?")} />);
        expect(screen.getByText(zh.mainChat.report.engagement.goal_check_in.summary)).toBeInTheDocument();
        expect(screen.getByText("How is the launch going?")).toBeInTheDocument();
    });

    it.each([['morning', '早间简报'], ['evening', '晚间简报'], ['on-request', '简报']])("localizes the %s brief slot without the retired ready line", async (slot, translated) => {
        await i18n.changeLanguage("zh-CN");
        render(<MainChatReportCard item={report({ source: "engagement", status: "brief_ready", params: { slot } })} />);
        expect(screen.getByText(translated)).toBeInTheDocument();
        expect(screen.queryByText(/已经准备好了/)).toBeNull();
    });

    it('does not replay a retired legacy brief headline from its stored summary', () => {
        render(<MainChatReportCard item={report({ source: 'engagement', status: 'brief_ready', summary: 'Your brief is ready.' }, 'Your week is ready.')} />);
        expect(screen.getByText('Brief')).toBeInTheDocument();
        expect(screen.queryByText(/Your (?:brief|week) is ready/)).toBeNull();
    });

    it('does not replay a retired weekly recap headline from an older report', () => {
        render(<MainChatReportCard item={report({ source: 'engagement', status: 'weekly_recap', target: { kind: 'today', id: '2026-10-30-evening' } }, 'Your week is ready.')} />);
        expect(screen.getByText('Evening brief')).toBeInTheDocument();
        expect(screen.queryByText('Your week is ready.')).toBeNull();
    });

    it("routes schedule and meeting reports to their targets", () => {
        const onOpen = vi.fn();
        const { unmount } = render(
            <MainChatReportCard
                item={report({ source: "schedule", action: "open", status: "ok", title: "Weekly digest", target: { kind: "automation", id: "job-1", runId: "run-1" } })}
                onOpenTarget={onOpen}
            />,
        );
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.action.open }));
        expect(onOpen).toHaveBeenLastCalledWith({ kind: "automation", id: "job-1", runId: "run-1" });
        unmount();
        render(
            <MainChatReportCard
                item={report({ source: "meeting", action: "open", status: "ready", title: "Zoom", target: { kind: "knowledge", id: "meeting-abc" } })}
                onOpenTarget={onOpen}
            />,
        );
        expect(screen.getByText(en.mainChat.report.meeting.ready.replace("{{title}}", "Zoom"))).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.action.open }));
        expect(onOpen).toHaveBeenLastCalledWith({ kind: "knowledge", id: "meeting-abc", runId: undefined });
    });

    it('places a report source after its words and before its action', () => {
        render(<MainChatReportCard item={report({ source: 'schedule', action: 'open', status: 'created', title: 'Daily News', target: { kind: 'automation', id: 'news' } })} onOpenTarget={vi.fn()} />);
        const sentence = screen.getByText('Added “Daily News” to your schedules.');
        const source = screen.getByText('From Tasks');
        const action = screen.getByRole('button', { name: 'Open' });
        expect(sentence.compareDocumentPosition(source) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(source.compareDocumentPosition(action) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    it("is what StreamCard renders for a report item", () => {
        render(<StreamCard item={report()} onOpenReportTarget={vi.fn()} />);
        expect(document.querySelector("[data-main-chat-report='handoff']")).not.toBeNull();
    });
});

describe("SideChatOriginCard", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("shows the branched message and opens the main chat", () => {
        const onOpen = vi.fn();
        const item: StreamItem = {
            id: "o1",
            kind: "system",
            content: "Here is the pricing table I drafted.",
            toolName: SIDE_CHAT_ORIGIN_TOOL,
            toolInput: { parentId: "main-1", itemId: "a-9" },
        };
        render(<SideChatOriginCard item={item} onOpenTarget={onOpen} />);
        expect(screen.getByText(en.mainChat.origin.title)).toBeInTheDocument();
        expect(screen.getByText("Here is the pricing table I drafted.")).toBeInTheDocument();
        expect(screen.getByText("Here is the pricing table I drafted.").compareDocumentPosition(screen.getByRole("button", { name: en.mainChat.origin.openMain })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.origin.openMain }));
        expect(onOpen).toHaveBeenCalledWith({ kind: "session", id: "main-1" });
    });
});

describe("Upcoming record lines", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("names the brief itself and says the new schedule in words", () => {
        const onOpen = vi.fn();
        render(
            <MainChatReportCard
                item={report(
                    { source: "schedule", action: "open", target: { kind: "upcoming" }, title: "", status: "changed", item: "brief_morning", schedule: "30 7 * * *" },
                    "Changed when the morning brief runs: every day at 07:30.",
                )}
                onOpenTarget={onOpen}
            />,
        );
        expect(screen.getByText("“Morning brief” is now set to Daily 07:30.")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.action.open }));
        expect(onOpen).toHaveBeenCalledWith({ kind: "upcoming", id: undefined, runId: undefined });
    });

    it("has a sentence for every change in both locales", () => {
        for (const status of ["changed", "changedWhen", "changedOnce", "paused", "resumed", "started", "removed"]) {
            const enText = (en.mainChat.report.schedule as Record<string, string>)[status];
            const zhText = (zh.mainChat.report.schedule as Record<string, string>)[status];
            expect(enText, status).toContain("{{title}}");
            expect(zhText, status).toContain("{{title}}");
        }
        expect(Object.keys(zh.mainChat.item)).toEqual(Object.keys(en.mainChat.item));
        expect(Object.keys(zh.mainChat.confirm)).toEqual(Object.keys(en.mainChat.confirm));
        render(
            <MainChatReportCard
                item={report({ source: "schedule", action: "open", target: { kind: "automation", id: "w" }, title: "Weekly update", status: "paused" })}
                onOpenTarget={vi.fn()}
            />,
        );
        expect(screen.getByText("Paused “Weekly update”.")).toBeInTheDocument();
    });

    it("says the new date and time of a one-time schedule", () => {
        const at = new Date(2026, 9, 16, 10, 0).getTime();
        render(
            <MainChatReportCard
                item={report({ source: "schedule", action: "open", target: { kind: "automation", id: "o" }, title: "Send the deck", status: "changed", schedule: `at:${at}` })}
                onOpenTarget={vi.fn()}
            />,
        );
        expect(screen.getByText(/^“Send the deck” will now run once, on .*10:00/)).toBeInTheDocument();
    });
});

describe("UpcomingConfirmCard", () => {
    const card = (overrides: Record<string, unknown> = {}): StreamItem => ({
        id: "c1",
        kind: "system",
        content: "Remove \"Weekly update\" from Upcoming? Confirm on the card on your computer.",
        toolName: UPCOMING_CONFIRM_TOOL,
        toolInput: { action: "remove", jobId: "weekly", title: "Weekly update", status: "pending", expiresAt: Date.now() + 60_000, ...overrides },
    });

    beforeEach(async () => {
        await i18n.changeLanguage("en");
        resolveUpcomingConfirm.mockReset();
    });

    it("removes only when the user presses Remove", async () => {
        resolveUpcomingConfirm.mockResolvedValue({ status: "removed" });
        render(<UpcomingConfirmCard item={card()} />);
        expect(screen.getByText("Remove the “Weekly update” schedule? Its run history goes with it.")).toBeInTheDocument();
        expect(resolveUpcomingConfirm).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.confirm.remove }));
        expect(resolveUpcomingConfirm).toHaveBeenCalledWith("c1", true);
        expect(await screen.findByText("Removed the “Weekly update” schedule.")).toBeInTheDocument();
        expect(screen.queryByRole("button")).toBeNull();
    });

    it("keeps on Keep, and an expired or answered card offers nothing", async () => {
        resolveUpcomingConfirm.mockResolvedValue({ status: "kept" });
        const { unmount } = render(<UpcomingConfirmCard item={card()} />);
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.confirm.keep }));
        expect(resolveUpcomingConfirm).toHaveBeenCalledWith("c1", false);
        expect(await screen.findByText("Kept the “Weekly update” schedule.")).toBeInTheDocument();
        unmount();

        const expired = render(<UpcomingConfirmCard item={card({ expiresAt: Date.now() - 1000 })} />);
        expect(screen.getByText(/expired/)).toBeInTheDocument();
        expect(screen.queryByRole("button")).toBeNull();
        expired.unmount();

        render(<StreamCard item={card({ status: "removed" })} onOpenReportTarget={vi.fn()} />);
        expect(document.querySelector("[data-upcoming-confirm='removed']")).not.toBeNull();
        expect(screen.queryByRole("button", { name: en.mainChat.confirm.remove })).toBeNull();
    });
});

describe("upcomingApprovalQuestion", () => {
    const tApp = () => i18n.getFixedT("en", "app");
    const tChat = () => i18n.getFixedT("en", "chat");

    it("names the schedule the approval is about", () => {
        expect(upcomingApprovalQuestion("UpcomingRunNow", { title: "Weekly update" }, tApp(), tChat())).toBe(
            "Run “Weekly update” now? It runs on its own, without asking again.",
        );
        expect(upcomingApprovalQuestion("UpcomingResume", { title: "Weekly update" }, tApp(), tChat())).toContain(
            "“Weekly update”",
        );
        expect(upcomingApprovalQuestion("UpcomingResume", { title: "Weekly update" }, tApp(), tChat())).toContain(
            "from its next scheduled time",
        );
    });

    it("names the brief and Daily News in the reader's language", () => {
        expect(upcomingApprovalQuestion("UpcomingRunNow", { item: "brief_morning" }, tApp(), tChat())).toContain(
            "“Morning brief”",
        );
        const question = upcomingApprovalQuestion(
            "UpcomingRunNow",
            { item: "news" },
            i18n.getFixedT("zh-CN", "app"),
            i18n.getFixedT("zh-CN", "chat"),
        );
        expect(question).toContain(`“${zh.mainChat.item.news}”`);
    });

    it("says the new schedule of a change that brings a run sooner", () => {
        const question = upcomingApprovalQuestion(
            "UpcomingChange",
            { title: "Post status", schedule: "31 14 * * *" },
            tApp(),
            tChat(),
        );
        expect(question).toMatch(/^Set “Post status” to .+\? Its next run comes sooner than planned/);
        expect(
            upcomingApprovalQuestion("UpcomingChange", { title: "Send the deck", schedule: "at:1760536260000" }, tApp(), tChat()),
        ).toMatch(/^Run “Send the deck” once, on .+\? That is sooner than planned/);
        expect(upcomingApprovalQuestion("UpcomingChange", { title: "Post status" }, tApp(), tChat())).toBe(
            "Change when “Post status” runs? Its next run comes sooner than planned, and it runs on its own.",
        );
    });

    it("leaves other tools to the caller", () => {
        expect(upcomingApprovalQuestion("Bash", undefined, tApp(), tChat())).toBeNull();
    });
});

describe("TextedReplyCard", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    const texted: StreamItem = {
        id: "t1",
        kind: "system",
        content: "Can the review wait until Friday?",
        toolName: MAIN_CHAT_TEXT_REPLY_TOOL,
        toolInput: { replyId: "6f1c1b2e-7a39-4d8a-9a51-1c2d3e4f5a6b", receivedAt: "2026-10-15T14:03:00Z" },
    };

    it("marks the words as a text, not the user's message, and offers to use them", () => {
        const onUse = vi.fn();
        render(<TextedReplyCard item={texted} onUse={onUse} />);
        expect(screen.getByText("Can the review wait until Friday?")).toBeInTheDocument();
        expect(screen.getByText(en.mainChat.texted.note)).toBeInTheDocument();
        expect(screen.getByText(/^From text/)).toBeInTheDocument();
        expect(screen.getByText("Can the review wait until Friday?").compareDocumentPosition(screen.getByText(/^From text/)) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(screen.getByText(en.mainChat.texted.note).compareDocumentPosition(screen.getByRole("button", { name: en.mainChat.texted.use })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.texted.use }));
        expect(onUse).toHaveBeenCalledWith("Can the review wait until Friday?");
    });

    it("is what StreamCard renders for a texted reply, after a reload too", () => {
        const onUse = vi.fn();
        render(<StreamCard item={texted} onUseTextedReply={onUse} />);
        expect(document.querySelector("[data-texted-reply]")).not.toBeNull();
        fireEvent.click(screen.getByRole("button", { name: en.mainChat.texted.use }));
        expect(onUse).toHaveBeenCalledWith("Can the review wait until Friday?");
    });

    it("hides the action without a handler and stands apart from user messages", () => {
        render(<TextedReplyCard item={texted} />);
        expect(screen.queryByRole("button")).toBeNull();
        expect(getTextedReply(texted)).toEqual({ text: "Can the review wait until Friday?", receivedAt: "2026-10-15T14:03:00Z" });
        expect(isStandaloneSystemItem(texted)).toBe(true);
        expect(getTextedReply({ ...texted, kind: "user" })).toBeNull();
        expect(getTextedReply({ ...texted, content: "  " })).toBeNull();
    });

    it("has every string in both locales", () => {
        expect(Object.keys(zh.mainChat.texted)).toEqual(Object.keys(en.mainChat.texted));
        expect(zh.mainChat.texted.labelAt).toContain("{{time}}");
    });
});
