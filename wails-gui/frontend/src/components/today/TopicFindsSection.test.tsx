import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { TopicFind, TopicFindsView } from "@/lib/wails-shim-app";
import type { UseTopicFindsResult } from "@/hooks/useTopicFinds";
import { TopicFindsSection } from "./TopicFindsSection";

const mocks = vi.hoisted(() => ({ openURL: vi.fn() }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({ BrowserOpenURL: mocks.openURL, EventsOn: vi.fn(() => () => {}) }));
vi.mock("@/lib/wails-shim-app", () => ({ ReopenMainChatSetupSteps: vi.fn() }));

const NOW = new Date(2026, 9, 15, 9, 30); // a Thursday

const find: TopicFind = {
    id: "f1",
    topicId: "t1",
    topic: "Wails drag regions",
    title: "Wails 2.12.1 released",
    source: "GitHub",
    url: "https://github.com/wailsapp/wails/releases/tag/v2.12.1",
    whatsNew: "Changes how --wails-draggable is read",
    lookedAt: new Date(2026, 9, 13, 16, 0).toISOString(),
    publishedAt: "2026-10-14",
    score: 0.9,
};

function finds(view: Partial<TopicFindsView> | null, overrides: Partial<UseTopicFindsResult> = {}): UseTopicFindsResult {
    return {
        view: view === null ? null : { enabled: true, status: "ready", items: [find], topics: ["Wails drag regions"], ...view },
        loading: false,
        loadFailed: false,
        failure: null,
        findNew: vi.fn().mockResolvedValue(undefined),
        feedback: vi.fn().mockResolvedValue(true),
        refresh: vi.fn(),
        ...overrides,
    };
}

beforeEach(async () => {
    await i18n.changeLanguage("en");
    vi.clearAllMocks();
});

describe("TopicFindsSection", () => {
    it("draws the skeleton, not an empty result, while the saved view is loading", () => {
        const { container } = render(<TopicFindsSection finds={finds({ status: "loading", items: [] })} onOpenTopics={vi.fn()} now={NOW} />);
        expect(container.querySelectorAll(".animate-pulse").length).toBeGreaterThan(0);
        expect(screen.queryByTestId("topic-finds-row")).toBeNull();
        expect(screen.queryByText(/nothing new/i)).toBeNull();
    });

    it.each(["en", "zh-CN"])("keeps three title/source links in one compact row in %s", async (language) => {
        await i18n.changeLanguage(language);
        const items = [1, 2, 3, 4, 5].map((i) => ({ ...find, id: `f${i}`, title: `Find ${i}`, source: `Source ${i}` }));
        const state = finds({ items });
        render(<TopicFindsSection finds={state} onOpenTopics={vi.fn()} now={NOW} />);
        const row = screen.getByTestId("topic-finds-row");
        expect(row).toHaveClass("flex");
        expect(row).not.toHaveClass("flex-col", "flex-wrap");
        const links = within(row).getAllByRole("button");
        expect(links).toHaveLength(3);
        expect(within(row).getAllByRole("listitem")).toHaveLength(3);
        for (const i of [1, 2, 3]) {
            expect(links[i - 1]).toHaveTextContent(`Find ${i}`);
            expect(links[i - 1]).toHaveTextContent(`Source ${i}`);
        }
        for (const i of [4, 5]) expect(screen.queryByText(`Find ${i}`)).not.toBeInTheDocument();
        expect(screen.queryByText(find.whatsNew)).not.toBeInTheDocument();
        expect(screen.queryByText(find.publishedAt!)).not.toBeInTheDocument();
        expect(within(row).queryByRole("heading")).not.toBeInTheDocument();
        fireEvent.click(links[0]);
        expect(mocks.openURL).toHaveBeenCalledWith(find.url);
        expect(state.feedback).toHaveBeenCalledWith("f1", "open");
    });

    it("uses the link host when a source is missing", () => {
        render(<TopicFindsSection finds={finds({ items: [{ ...find, source: "" }] })} onOpenTopics={vi.fn()} now={NOW} />);
        expect(screen.getByTestId("topic-find")).toHaveTextContent("github.com");
    });

    it("keeps an unsafe link as plain text", () => {
        render(<TopicFindsSection finds={finds({ items: [{ ...find, url: "javascript:alert(1)" }] })} onOpenTopics={vi.fn()} now={NOW} />);
        const row = within(screen.getByTestId("topic-finds-row"));
        expect(row.getByText(find.title)).toBeInTheDocument();
        expect(row.queryByRole("button")).not.toBeInTheDocument();
        expect(mocks.openURL).not.toHaveBeenCalled();
    });

    it("finds new on a press and shows the search running", () => {
        const state = finds({ status: "none", items: [] });
        const { rerender } = render(<TopicFindsSection finds={state} onOpenTopics={vi.fn()} now={NOW} />);
        expect(screen.getByText("Nothing new on your topics today.")).toBeInTheDocument();
        expect(screen.getByText("Isoform is watching: Wails drag regions")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Find new" }));
        expect(state.findNew).toHaveBeenCalledTimes(1);

        rerender(<TopicFindsSection finds={finds({ status: "finding", items: [] })} onOpenTopics={vi.fn()} now={NOW} />);
        expect(screen.getByRole("status")).toHaveTextContent("Looking for new things on your topics...");
        expect(screen.getByRole("button", { name: "Find new" })).toBeDisabled();
    });

    it("tells a load failure and a failed search apart from off", () => {
        const refresh = vi.fn();
        const { rerender } = render(
            <TopicFindsSection finds={finds(null, { loadFailed: true, refresh })} onOpenTopics={vi.fn()} now={NOW} />,
        );
        expect(screen.getByText("Could not load new finds on your topics.")).toBeInTheDocument();
        expect(screen.queryByText("New on your topics is off.")).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(refresh).toHaveBeenCalledTimes(1);

        rerender(<TopicFindsSection finds={finds({ status: "none", items: [] }, { failure: "find" })} onOpenTopics={vi.fn()} now={NOW} />);
        expect(screen.getByRole("alert")).toHaveTextContent("Could not start a search. Try again.");
        rerender(<TopicFindsSection finds={finds({}, { failure: "answer" })} onOpenTopics={vi.fn()} now={NOW} />);
        expect(screen.getByRole("alert")).toHaveTextContent("Could not save that. Try again.");
    });

    it("says when the search could not reach the web, with what is missing", () => {
        const state = finds({ status: "unavailable", items: [] });
        render(<TopicFindsSection finds={state} onOpenTopics={vi.fn()} now={NOW} />);
        expect(screen.getByText(/could not search the web. Sign in to Claude Code on this computer, or open Chrome with the OpenCLI extension/)).toBeInTheDocument();
        expect(screen.queryByText("Nothing new on your topics today.")).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(state.findNew).toHaveBeenCalledTimes(1);
    });

    it("says when Find new works again after a press", () => {
        const until = new Date(NOW.getTime() + 8 * 60_000).toISOString();
        render(<TopicFindsSection finds={finds({ status: "none", items: [], cooldownUntil: until })} onOpenTopics={vi.fn()} now={NOW} />);
        expect(screen.getByRole("button", { name: "Find new" })).toBeDisabled();
        expect(screen.getByText(/You can look again at/)).toBeInTheDocument();
    });

    it("turns Find new back on when the cooldown ends", () => {
        vi.useFakeTimers();
        try {
            vi.setSystemTime(NOW);
            const until = new Date(NOW.getTime() + 60_000).toISOString();
            render(<TopicFindsSection finds={finds({ status: "none", items: [], cooldownUntil: until })} onOpenTopics={vi.fn()} />);
            expect(screen.getByRole("button", { name: "Find new" })).toBeDisabled();
            act(() => {
                vi.advanceTimersByTime(61_000);
            });
            expect(screen.getByRole("button", { name: "Find new" })).toBeEnabled();
            expect(screen.queryByText(/You can look again at/)).not.toBeInTheDocument();
        } finally {
            vi.useRealTimers();
        }
    });

    it("explains the off, unconfigured and no-topic states", () => {
        const onOpenTopics = vi.fn();
        const { rerender } = render(<TopicFindsSection finds={finds({ enabled: false, status: "off", items: [] })} onOpenTopics={onOpenTopics} now={NOW} />);
        expect(screen.getByText("New on your topics is off.")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Find new" })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Turn on" }));
        expect(onOpenTopics).toHaveBeenCalledTimes(1);

        rerender(<TopicFindsSection finds={finds({ status: "unconfigured", items: [] })} onOpenTopics={onOpenTopics} now={NOW} />);
        expect(screen.getByText(/Choose an assistant/)).toBeInTheDocument();

        rerender(<TopicFindsSection finds={finds({ status: "no_topics", items: [], topics: [] })} onOpenTopics={onOpenTopics} now={NOW} />);
        expect(screen.getByText(/Your topics show up after about an hour/)).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Your topics" }));
        expect(onOpenTopics).toHaveBeenCalledTimes(2);
    });

});
