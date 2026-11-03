import { Profiler, useState } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { computerClock, setComputerClock } from "@/lib/brief-time";

import i18n from "@/i18n";
import { TopicPanel } from "./TopicPanel";
import type { TodayTopic, TopicChatItem, TopicChatPage } from "./topics";

const mocks = vi.hoisted(() => ({
    list: vi.fn(),
    send: vi.fn(),
    pauseSince: vi.fn(),
    listeners: new Map<string, (value: unknown) => void>(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    ListMainChatTopicItems: mocks.list,
    SendMainChatWithTopic: mocks.send,
    GetTodayObservationPauseSince: mocks.pauseSince,
}));
vi.mock("../../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, callback: (value: unknown) => void) => {
        mocks.listeners.set(name, callback);
        return () => mocks.listeners.delete(name);
    },
}));

const topic: TodayTopic = { id: "topic-halcyon", name: "Halcyon ERP sync", kind: "work", status: "waiting", people: { names: ["Dana", "Lee"], count: 5 }, pinned: false };
const now = Date.now();
const event = (id: string, text: string, at = now - 60_000): TopicChatItem => ({ id: `event:${id}`, kind: "event", at, actor: "user", text });
const message = (id: string, kind: string, content: string, at = now - 30_000, sortOrder = 1): TopicChatItem => ({
    id: `message:${id}`, kind: "message", at, message: { id, kind, content, sortOrder },
});
const page = (items: TopicChatItem[], more: Partial<TopicChatPage> = {}): TopicChatPage => ({
    sessionId: "main", topicId: topic.id, items, hasMore: false, nextCursor: "", ...more,
});

function handlers() {
    return {
        onStartChat: vi.fn(), onRunNow: vi.fn(), onOpenMemory: vi.fn(), onOpenAutomations: vi.fn(),
        onOpenActivity: vi.fn(), onOpenPeople: vi.fn(), onOpenMainChat: vi.fn(),
    };
}

function Harness({ initial = "", prepared = false, paused = false, actions = handlers() }: { initial?: string; prepared?: boolean; paused?: boolean; actions?: ReturnType<typeof handlers> }) {
    const [state, setState] = useState({ draft: initial, prepared });
    return (
        <TopicPanel
            topic={topic}
            variant="pinned"
            draft={state.draft}
            prepared={state.prepared}
            onDraftChange={(draft, isPrepared) => setState({ draft, prepared: isPrepared })}
            handlers={actions}
            observationPaused={paused}
        />
    );
}

beforeEach(async () => {
    setComputerClock("12h");
    await i18n.changeLanguage("en");
    vi.clearAllMocks();
    mocks.listeners.clear();
    mocks.list.mockResolvedValue(page([]));
    mocks.send.mockResolvedValue({ sessionId: "main", userItemId: "u1", sortOrder: 9 });
    mocks.pauseSince.mockResolvedValue(new Date(2026, 9, 30, 9, 15).getTime());
});

describe("TopicPanel (RC 1)", () => {
    it("shows the topic's newest find in the panel", () => {
        render(<TopicPanel topic={{ ...topic, latestFind: { text: "Q3 results" } }} variant="pinned" draft="" prepared={false} onDraftChange={vi.fn()} handlers={handlers()} observationPaused={false} />);
        expect(screen.getByText("New: Q3 results")).toBeInTheDocument();
    });
    it("names the topic and says it is the chat, filtered, with Show everything", async () => {
        const actions = handlers();
        render(<Harness actions={actions} />);
        const panel = screen.getByRole("complementary", { name: "Halcyon ERP sync in your chat" });
        expect(within(panel).getByText("Project")).toBeInTheDocument();
        expect(within(panel).getByText("Waiting on you")).toHaveClass("text-warning");
        expect(within(panel).getByRole("heading", { name: "Halcyon ERP sync" })).toBeInTheDocument();
        expect(within(panel).getByText("Dana, Lee and 3 others")).toBeInTheDocument();
        expect(within(panel).getByText("Your chat, showing only Halcyon ERP sync.")).toBeInTheDocument();
        fireEvent.click(within(panel).getByRole("button", { name: "Open in Chat" }));
        expect(actions.onOpenMainChat).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(mocks.list).toHaveBeenCalledWith(topic.id, "", 50));
    });

    it("draws grey lines while it reads, then the events, the user's words and Isoform's replies", async () => {
        let resolve: (value: TopicChatPage) => void = () => {};
        mocks.list.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
        render(<Harness />);
        expect(screen.getByTestId("topic-panel-loading")).toHaveAttribute("aria-busy", "true");
        await act(async () => resolve(page([
            event("e1", "pushed ERP connector v0.3 to staging", now - 120_000),
            message("u1", "user", "Could we hit Oct 30?", now - 60_000),
            message("t1", "tool", "tool output never shown here", now - 50_000),
            message("a1", "assistant", "Oct 30 would skip the **staging preview**.", now - 40_000),
        ])));
        expect(screen.queryByTestId("topic-panel-loading")).not.toBeInTheDocument();
        expect(screen.getByTestId("topic-panel-event")).toHaveTextContent("You pushed ERP connector v0.3 to staging");
        expect(screen.getByTestId("topic-panel-event").firstElementChild).toHaveClass("bg-ink");
        expect(screen.getByTestId("topic-panel-user")).toHaveTextContent("Could we hit Oct 30?");
        const reply = screen.getByTestId("topic-panel-isoform");
        expect(reply).toHaveTextContent(/^Isoform · /);
        expect(within(reply).getByText("staging preview").tagName).toBe("STRONG");
        expect(screen.queryByText("tool output never shown here")).not.toBeInTheDocument();
    });

    it("says when the topic has nothing yet", async () => {
        render(<Harness />);
        expect(await screen.findByTestId("topic-panel-empty")).toHaveTextContent("Nothing about Halcyon ERP sync in your chat yet. Ask below to start.");
    });

    it("says it could not load, with Try again and Show everything", async () => {
        mocks.list.mockRejectedValueOnce(new Error("TODAY_CHAT_ITEM_UNAVAILABLE"));
        const actions = handlers();
        render(<Harness actions={actions} />);
        const failed = await screen.findByTestId("topic-panel-failed");
        expect(within(failed).getByRole("alert")).toHaveTextContent("Could not load this topic. Your chat still has everything.");
        mocks.list.mockResolvedValueOnce(page([message("u1", "user", "Back again")]));
        fireEvent.click(within(failed).getByRole("button", { name: "Try again" }));
        expect(await screen.findByText("Back again")).toBeInTheDocument();
    });

    it("recovers a deferred topic read when publication finishes", async () => {
        mocks.list.mockRejectedValueOnce(new Error("TODAY_CHAT_ITEM_UNAVAILABLE"));
        render(<Harness initial="Where do I stand?" prepared />);
        expect(await screen.findByTestId("topic-panel-failed")).toBeInTheDocument();
        mocks.list.mockResolvedValueOnce(page([message("u1", "user", "Where do I stand?")]));
        await act(async () => { mocks.listeners.get("topics:updated")?.(undefined); });
        expect(await screen.findByTestId("topic-panel-user")).toHaveTextContent("Where do I stand?");
        expect(screen.queryByTestId("topic-panel-failed")).not.toBeInTheDocument();
        expect(screen.getByRole("textbox", { name: "Message Isoform about this topic" })).toHaveValue("Where do I stand?");
        expect(mocks.send).not.toHaveBeenCalled();
    });

    it("clears a withdrawn topic and explains why it cannot be opened", async () => {
        mocks.list.mockResolvedValueOnce(page([event("private", "opened a private document")]));
        render(<Harness initial="Where do I stand?" prepared />);
        await screen.findByText(/opened a private document/);
        mocks.list.mockRejectedValueOnce(new Error("TODAY_CHAT_CONTEXT_UNAVAILABLE"));
        await act(async () => { mocks.listeners.get("topics:withdrawn")?.(undefined); });
        expect(await screen.findByTestId("topic-panel-withdrawn")).toHaveTextContent("This topic was withdrawn for privacy. Your chat still has everything.");
        expect(screen.queryByText(/opened a private document/)).not.toBeInTheDocument();
        expect(screen.queryByRole("textbox", { name: "Message Isoform about this topic" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
        expect(mocks.send).not.toHaveBeenCalled();
    });

    it("shows earlier rows above on Show earlier", async () => {
        mocks.list.mockResolvedValueOnce(page([message("u2", "user", "Newer words")], { hasMore: true, nextCursor: "cursor-1" }));
        render(<Harness />);
        await screen.findByText("Newer words");
        mocks.list.mockResolvedValueOnce(page([message("u1", "user", "Older words", now - 600_000)]));
        fireEvent.click(screen.getByRole("button", { name: "Show earlier" }));
        await screen.findByText("Older words");
        expect(mocks.list).toHaveBeenLastCalledWith(topic.id, "cursor-1", 50);
        const rows = screen.getAllByTestId("topic-panel-user").map((row) => row.textContent);
        expect(rows).toEqual(["Older words", "Newer words"]);
        expect(screen.queryByRole("button", { name: "Show earlier" })).not.toBeInTheDocument();
    });

    it("sends only on Send, with the topic attached, and says Isoform is working until the reply ends", async () => {
        render(<Harness />);
        await screen.findByTestId("topic-panel-empty");
        const box = screen.getByRole("textbox", { name: "Message Isoform about this topic" });
        fireEvent.change(box, { target: { value: "Make the reply warmer" } });
        expect(mocks.send).not.toHaveBeenCalled();
        expect(screen.getByTestId("topic-panel-note")).toHaveTextContent("Sends to your chat with this topic attached.");
        mocks.list.mockResolvedValue(page([message("u1", "user", "Make the reply warmer")]));
        await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send" })); });
        expect(mocks.send).toHaveBeenCalledWith("Make the reply warmer", topic.id);
        expect(box).toHaveValue("");
        expect(screen.getByRole("status")).toHaveTextContent("Isoform is working on this.");
        mocks.list.mockResolvedValue(page([message("u1", "user", "Make the reply warmer"), message("a1", "assistant", "Here is a warmer reply.")]));
        // A scheduled run ending in its own session is not this reply.
        await act(async () => { mocks.listeners.get("agent:event")?.({ type: "done", tag: "cron-session" }); });
        expect(screen.getByRole("status")).toHaveTextContent("Isoform is working on this.");
        expect(screen.queryByText("Here is a warmer reply.")).not.toBeInTheDocument();
        await act(async () => { mocks.listeners.get("agent:event")?.({ type: "done", tag: "main" }); });
        expect(await screen.findByText("Here is a warmer reply.")).toBeInTheDocument();
        expect(screen.queryByRole("status")).not.toBeInTheDocument();
    });

    it("sends with Enter and keeps Shift+Enter for a new line", async () => {
        render(<Harness initial="First line" />);
        await screen.findByTestId("topic-panel-empty");
        const box = screen.getByRole("textbox", { name: "Message Isoform about this topic" });
        fireEvent.keyDown(box, { key: "Enter", shiftKey: true });
        expect(mocks.send).not.toHaveBeenCalled();
        await act(async () => { fireEvent.keyDown(box, { key: "Enter" }); });
        expect(mocks.send).toHaveBeenCalledWith("First line", topic.id);
    });

    it("puts the words back in the box and offers Try again when they were not sent", async () => {
        mocks.send.mockRejectedValueOnce(new Error("TODAY_CHAT_ITEM_UNAVAILABLE"));
        render(<Harness initial="Could we hit Oct 30?" />);
        await screen.findByTestId("topic-panel-empty");
        await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send" })); });
        const pending = screen.getByTestId("topic-panel-pending");
        expect(within(pending).getByRole("alert")).toHaveTextContent("Not sent.");
        expect(screen.getByRole("textbox", { name: "Message Isoform about this topic" })).toHaveValue("Could we hit Oct 30?");
        await act(async () => { fireEvent.click(within(pending).getByRole("button", { name: "Try again" })); });
        expect(mocks.send).toHaveBeenCalledTimes(2);
        expect(mocks.send).toHaveBeenLastCalledWith("Could we hit Oct 30?", topic.id);
    });

    it("marks prepared words and says nothing is sent until Send", async () => {
        render(<Harness initial="Could we hit Oct 30?" prepared />);
        await screen.findByTestId("topic-panel-empty");
        expect(screen.getByTestId("topic-panel-note")).toHaveTextContent("Prepared from your brief. Nothing is sent until you press Send.");
        expect(screen.getByRole("textbox", { name: "Message Isoform about this topic" }).closest("[data-topic-composer-surface]")).toHaveClass("border-border-focus");
        expect(mocks.send).not.toHaveBeenCalled();
        // Typing makes the words the user's own.
        fireEvent.change(screen.getByRole("textbox", { name: "Message Isoform about this topic" }), { target: { value: "Could we hit Oct 29?" } });
        expect(screen.getByTestId("topic-panel-note")).toHaveTextContent("Sends to your chat with this topic attached.");
    });

    it("while observing is paused, sends in the topic with the pause time", async () => {
        const actions = handlers();
        render(<Harness initial="Could we hit Oct 30?" paused actions={actions} />);
        await screen.findByTestId("topic-panel-empty");
        await waitFor(() => expect(screen.getByTestId("topic-panel-note")).toHaveTextContent("Observing is paused, so this uses what Isoform saw until 9:15 AM."));
        await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Send" })); });
        expect(mocks.send).toHaveBeenCalledWith("Could we hit Oct 30?", topic.id);
        expect(actions.onStartChat).not.toHaveBeenCalled();
    });

    it("shows the paused topic line in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<Harness paused />);
        const time = computerClock(new Date(2026, 9, 30, 9, 15), "zh-CN");
        await waitFor(() => expect(screen.getByTestId("topic-panel-note")).toHaveTextContent(`观察已暂停，这次用的是 Isoform 截至${time}看到的内容。`));
    });

    it("reads again when observing pauses or resumes", async () => {
        render(<Harness />);
        await screen.findByTestId("topic-panel-empty");
        expect(mocks.list).toHaveBeenCalledTimes(1);
        await act(async () => { mocks.listeners.get("closed-eyes:changed")?.(true); });
        expect(mocks.list).toHaveBeenCalledTimes(2);
    });

    it.each([
        ["Waiting on you", "waiting", "等你处理"],
        ["Waiting on them", "waitingOnThem", "等对方回复"],
        ["3 new", "news", "3 条新内容"],
    ] as const)("reads %s in Chinese too", async (_en, status, translated) => {
        await i18n.changeLanguage("zh-CN");
        render(
            <TopicPanel topic={{ ...topic, status, newCount: 3 }} variant="pinned" draft="" prepared={false} onDraftChange={vi.fn()} handlers={handlers()} observationPaused={false} />,
        );
        expect(screen.getByText(translated)).toBeInTheDocument();
        expect(screen.getByText("你的聊天，只显示「Halcyon ERP sync」。")).toBeInTheDocument();
        await screen.findByTestId("topic-panel-empty");
    });
});

describe("TopicPanel review fixes", () => {
    it("drops older rows when observing pauses: every read starts from the latest page", async () => {
        mocks.list.mockResolvedValueOnce(page([message("u2", "user", "Newer words")], { hasMore: true, nextCursor: "c1" }));
        render(<Harness />);
        await screen.findByText("Newer words");
        mocks.list.mockResolvedValueOnce(page([event("e1", "opened a private document", now - 900_000)]));
        fireEvent.click(screen.getByRole("button", { name: "Show earlier" }));
        await screen.findByText(/opened a private document/);
        mocks.list.mockResolvedValueOnce(page([message("u2", "user", "Newer words")]));
        await act(async () => { mocks.listeners.get("closed-eyes:changed")?.(true); });
        expect(screen.queryByText(/opened a private document/)).not.toBeInTheDocument();
        expect(screen.getByText("Newer words")).toBeInTheDocument();
    });

    it("keeps a send with its topic when the panel moves to another one before it returns", async () => {
        let finish: (value: unknown) => void = () => {};
        mocks.send.mockReturnValueOnce(new Promise((done) => { finish = done; }));
        const onDraftChange = vi.fn();
        const props = { variant: "pinned" as const, prepared: false, onDraftChange, handlers: handlers(), observationPaused: false };
        const { rerender } = render(<TopicPanel key={topic.id} topic={topic} draft="Could we hit Oct 30?" {...props} />);
        await screen.findByTestId("topic-panel-empty");
        fireEvent.click(screen.getByRole("button", { name: "Send" }));
        expect(mocks.send).toHaveBeenCalledWith("Could we hit Oct 30?", topic.id);
        const halcyonLaunch: TodayTopic = { id: "topic-halcyon-launch", name: "Halcyon launch memo", kind: "work", pinned: false };
        rerender(<TopicPanel key={halcyonLaunch.id} topic={halcyonLaunch} draft="" {...props} />);
        onDraftChange.mockClear();
        await act(async () => finish(Promise.reject(new Error("TODAY_CHAT_ITEM_UNAVAILABLE"))));
        // The launch memo's box and rows are its own: no ERP words, no "Not sent".
        expect(onDraftChange).not.toHaveBeenCalled();
        expect(screen.queryByTestId("topic-panel-pending")).not.toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Halcyon launch memo" })).toBeInTheDocument();
    });

    it("does not say nothing is there while earlier rows remain", async () => {
        mocks.list.mockResolvedValueOnce(page([], { hasMore: true, nextCursor: "c1" }));
        render(<Harness />);
        expect(await screen.findByRole("button", { name: "Show earlier" })).toBeInTheDocument();
        expect(screen.queryByTestId("topic-panel-empty")).not.toBeInTheDocument();
    });
});


it('does not commit, mutate the layout or move the reader on identical background detail reads', async () => {
    const rows = [
        event('e1', 'reviewed "salesrabbit-isoform"'),
        message('u1', 'user', 'Keep this draft in view'),
        message('a1', 'assistant', 'Prepared notes\n\n```\nlong_unbroken_source_name\n```\n\n| Source |\n| --- |\n| Notes |'),
    ];
    mocks.list.mockImplementation(async () => structuredClone(page(rows)));
    let commits = 0;
    const host = render(<Profiler id="detail" onRender={() => commits++}><Harness initial="Unsent draft" /></Profiler>);
    await screen.findByText('Keep this draft in view');
    await act(async () => {});
    const initialCommits = commits;
    const panel = screen.getByTestId('topic-panel');
    const list = screen.getByTestId('topic-panel-list');
    const activity = screen.getByTestId('topic-panel-event');
    expect(activity).toHaveClass('min-w-0', 'max-w-full');
    expect(activity.firstElementChild).toHaveClass('bg-ink');
    expect(activity.querySelector('.flex-1')).toHaveClass('min-w-0', '[overflow-wrap:anywhere]');
    const reply = screen.getByTestId('topic-panel-isoform');
    expect(reply).toHaveClass('min-w-0', 'max-w-full', '[overflow-wrap:anywhere]');
    expect(reply.firstElementChild).toHaveClass('text-accent');
    expect(reply.querySelector('pre')?.parentElement).toHaveClass('min-w-0', '[&_pre]:whitespace-pre-wrap', '[&_table]:w-full', '[&_table]:table-fixed');
    list.scrollTop = 37;
    const markup = panel.outerHTML;
    const mutations: MutationRecord[] = [];
    const observer = new MutationObserver((entries) => mutations.push(...entries));
    observer.observe(host.container, { attributes: true, childList: true, characterData: true, subtree: true });
    try {
        for (let read = 0; read < 10; read++) await act(async () => { window.dispatchEvent(new Event('focus')); });
        expect(mocks.list).toHaveBeenCalledTimes(11);
        expect(commits).toBe(initialCommits);
        expect(mutations).toHaveLength(0);
        expect(screen.getByTestId('topic-panel')).toBe(panel);
        expect(screen.getByTestId('topic-panel-event')).toBe(activity);
        expect(screen.getByTestId('topic-panel-isoform')).toBe(reply);
        expect(panel.outerHTML).toBe(markup);
        expect(list.scrollTop).toBe(37);
        expect(screen.getByRole('textbox')).toHaveValue('Unsent draft');
    } finally { observer.disconnect(); }
    // A real edit or withdrawal must still replace the old rows.
    mocks.list.mockResolvedValue(page([event('e1', 'updated thread summary')]));
    await act(async () => { window.dispatchEvent(new Event('focus')); });
    expect(screen.queryByText('Keep this draft in view')).not.toBeInTheDocument();
    expect(screen.queryByTestId('topic-panel-isoform')).not.toBeInTheDocument();
    expect(screen.getByTestId('topic-panel-event')).toHaveTextContent('updated thread summary');
    expect(commits).toBeGreaterThan(initialCommits);
});

it('keeps the latest detail response when two completed reads share a React batch', async () => {
    const original = page([event('e1', 'Original summary')]);
    mocks.list.mockResolvedValue(original);
    render(<Harness />);
    await screen.findByText(/Original summary/);
    await act(async () => {
        mocks.list.mockResolvedValue(page([event('e1', 'Intermediate summary')]));
        window.dispatchEvent(new Event('focus'));
        await Promise.resolve();
        mocks.list.mockResolvedValue(original);
        window.dispatchEvent(new Event('focus'));
        await Promise.resolve();
    });
    expect(screen.getByTestId('topic-panel-event')).toHaveTextContent('Original summary');
    expect(screen.queryByText(/Intermediate summary/)).not.toBeInTheDocument();
});
