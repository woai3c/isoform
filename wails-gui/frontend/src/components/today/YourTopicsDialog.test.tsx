import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { UserTopicsView } from "@/lib/wails-shim-app";
import { YourTopicsDialog, renameProblem } from "./YourTopicsDialog";

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), setEnabled: vi.fn(), pin: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({
    GetUserTopics: mocks.get,
    PinTopicToSidebar: mocks.pin,
    UpdateUserTopic: mocks.update,
    SetTopicFindsEnabled: mocks.setEnabled,
}));

const topicsView: UserTopicsView = {
    enabled: true,
    interests: true,
    topics: [
        {
            id: "t1",
            label: "wearable AI camera hats",
            pinned: true,
            sources: ["activity", "goal"],
            why: "You are prototyping a smart cap with a camera.",
            kind: "project",
            lastSeenAt: new Date(Date.now() - 2 * 86_400_000).toISOString(),
        },
        {
            id: "t2",
            label: "Pricing pages",
            original: "pricing review",
            pinned: false,
            sources: ["chat"],
            why: "You compared pricing tiers.",
        },
        // No why yet: hidden until Isoform can say why.
        { id: "t4", label: "silent speech EMG wearable", pinned: false, sources: ["activity"] },
    ],
    removed: [{ id: "t3", label: "Rust async", pinned: false, sources: [] }],
};

beforeEach(async () => {
    await i18n.changeLanguage("en");
    vi.clearAllMocks();
    mocks.get.mockResolvedValue(topicsView);
    mocks.pin.mockResolvedValue(undefined);
    mocks.update.mockResolvedValue(topicsView);
    mocks.setEnabled.mockResolvedValue({ ...topicsView, enabled: false });
});

describe("YourTopicsDialog", () => {
    it("lists the topics, says what leaves the Mac, and pins, renames, removes and brings back", async () => {
        const onChanged = vi.fn();
        render(<YourTopicsDialog open onOpenChange={vi.fn()} onChanged={onChanged} />);
        const rows = await screen.findAllByTestId("topic-row");
        expect(rows).toHaveLength(2);
        // One short line per topic; the rest opens on More.
        expect(rows[0]).toHaveTextContent("wearable AI camera hats");
        expect(rows[0]).not.toHaveTextContent("You are prototyping a smart cap with a camera.");
        fireEvent.click(within(rows[0]).getByRole("button", { name: "More" }));
        expect(rows[0]).toHaveTextContent("You are prototyping a smart cap with a camera.");
        expect(rows[0]).toHaveTextContent("Pinned");
        expect(rows[0]).toHaveTextContent("Project");
        expect(rows[0]).toHaveTextContent("Last seen 2 days ago");
        fireEvent.click(within(rows[0]).getByRole("button", { name: "Less" }));
        expect(rows[0]).not.toHaveTextContent("You are prototyping a smart cap with a camera.");
        fireEvent.click(within(rows[1]).getByRole("button", { name: "More" }));
        expect(rows[1]).toHaveTextContent("Found as pricing review");
        expect(
            screen.getByText(
                /only each topic's name and a few short search phrases made from it are sent to a web search: the web search of your Claude Code when it is signed in on this computer, otherwise Grok, Gemini or Doubao/,
            ),
        ).toBeInTheDocument();
        expect(screen.getByText(/it sends those summaries to the same assistant that writes them/)).toBeInTheDocument();

        fireEvent.click(within(rows[0]).getByRole("button", { name: "Unpin" }));
        await waitFor(() => expect(mocks.pin).toHaveBeenCalledWith("t1", false));
        await waitFor(() => expect(onChanged).toHaveBeenCalled());

        fireEvent.click(within(rows[1]).getByRole("button", { name: "Rename" }));
        const input = screen.getByRole("textbox", { name: "Topic name" });
        fireEvent.change(input, { target: { value: "Pricing that converts" } });
        fireEvent.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() =>
            expect(mocks.update).toHaveBeenCalledWith({ id: "t2", action: "rename", label: "Pricing that converts" }),
        );

        const fresh = await screen.findAllByTestId("topic-row");
        fireEvent.click(within(fresh[1]).getByRole("button", { name: "Remove" }));
        await waitFor(() => expect(mocks.update).toHaveBeenCalledWith({ id: "t2", action: "remove", label: "" }));

        fireEvent.click(screen.getByRole("button", { name: "Bring back" }));
        await waitFor(() => expect(mocks.update).toHaveBeenCalledWith({ id: "t3", action: "restore", label: "" }));
    });

    it("refreshes the visible pin when it changes from the topic panel", async () => {
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        const rows = await screen.findAllByTestId("topic-row");
        expect(within(rows[1]).getByRole("button", { name: "Pin" })).toBeInTheDocument();
        mocks.get.mockResolvedValue({ ...topicsView, topics: topicsView.topics.map((topic) => topic.id === "t2" ? { ...topic, pinned: true } : topic) });
        window.dispatchEvent(new Event("isoform:sidebar-topics-changed"));
        await waitFor(() => expect(within(screen.getAllByTestId("topic-row")[1]).getByRole("button", { name: "Unpin" })).toBeInTheDocument());
    });

    it("hides a topic Isoform cannot say why about until it can", async () => {
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        const rows = await screen.findAllByTestId("topic-row");
        expect(rows).toHaveLength(2);
        expect(screen.queryByText("silent speech EMG wearable")).not.toBeInTheDocument();
    });

    it("still lists phrases before Isoform has named any interests", async () => {
        mocks.get.mockResolvedValue({
            enabled: true,
            interests: false,
            topics: [{ id: "p1", label: "wails drag regions", pinned: false, sources: ["activity"] }],
            removed: [],
        });
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        const rows = await screen.findAllByTestId("topic-row");
        expect(rows).toHaveLength(1);
        expect(rows[0]).toHaveTextContent("wails drag regions");
    });

    it("says when a topic was last seen in relative words, in Chinese too", async () => {
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        const rows = await screen.findAllByTestId("topic-row");
        fireEvent.click(within(rows[0]).getByRole("button", { name: "More" }));
        expect(rows[0]).toHaveTextContent("Last seen 2 days ago");
        await i18n.changeLanguage("zh-CN");
        expect(rows[0]).toHaveTextContent("2 天前");
    });

    it("checks a new name before saving it", async () => {
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        const rows = await screen.findAllByTestId("topic-row");
        fireEvent.click(within(rows[0]).getByRole("button", { name: "Rename" }));
        fireEvent.change(screen.getByRole("textbox", { name: "Topic name" }), { target: { value: "https://example.com/page" } });
        fireEvent.click(screen.getByRole("button", { name: "Save" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("A topic cannot be a link, a path or an address.");
        expect(mocks.update).not.toHaveBeenCalled();
        expect(renameProblem("one two three four five six seven")).toBe("topics.renameTooLong");
        expect(renameProblem("Wails windows")).toBeNull();
    });

    it("turns the feature off with its switch", async () => {
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        const toggle = await screen.findByRole("switch", { name: "Find new things on my topics" });
        await waitFor(() => expect(toggle).toBeChecked());
        fireEvent.click(toggle);
        await waitFor(() => expect(mocks.setEnabled).toHaveBeenCalledWith(false));
        await waitFor(() => expect(toggle).not.toBeChecked());
    });

    it("says when a change could not be saved", async () => {
        mocks.pin.mockRejectedValueOnce(new Error("store busy"));
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        const rows = await screen.findAllByTestId("topic-row");
        fireEvent.click(within(rows[1]).getByRole("button", { name: "Pin" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not save that. Try again.");
    });

    it("says when the topics could not be loaded and tries again", async () => {
        mocks.get.mockRejectedValueOnce(new Error("store busy"));
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not load your topics.");
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findAllByTestId("topic-row")).toHaveLength(2);
    });

    it("says there are no topics yet", async () => {
        mocks.get.mockResolvedValue({ enabled: true, interests: false, topics: [], removed: [] });
        render(<YourTopicsDialog open onOpenChange={vi.fn()} />);
        expect(await screen.findByText(/Your topics show up after about an hour/)).toBeInTheDocument();
    });
});
