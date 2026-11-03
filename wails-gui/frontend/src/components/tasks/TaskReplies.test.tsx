import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setComputerClock } from "@/lib/brief-time";

import i18n from "@/i18n";
import { TaskThreadPane } from "./TaskThreadPane";
import { resetPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";

const mocks = vi.hoisted(() => ({ get: vi.fn(), action: vi.fn(), reply: vi.fn(), stop: vi.fn(), startAgain: vi.fn(), aiReady: vi.fn(), del: vi.fn(), emit: vi.fn(), reveal: vi.fn(), openURL: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ GetDesktopTaskThread: mocks.get, ActOnDesktopTask: mocks.action, ReplyToDesktopTask: mocks.reply, AbortHandoff: mocks.stop, StartHandoffAgain: mocks.startAgain, AIReady: mocks.aiReady, DeleteHandoff: mocks.del, RevealTaskStep: mocks.reveal }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({ EventsEmit: mocks.emit, EventsOn: () => () => undefined, BrowserOpenURL: mocks.openURL }));

const at = "2026-10-31T22:04:00Z";
const clockAt = (lang: string) => new Date(at).toLocaleTimeString(lang, { hour: "numeric", minute: "2-digit" });
const running = { id: "task", title: "Make the deck", group: "running", status: "in_progress", updatedAt: at, events: [], actions: ["rename", "stop"], steps: [] };
const reply = (id: string, text: string, extra: Record<string, unknown> = {}) => ({ id, text, sentAt: at, state: "waiting", ...extra });
const stoppedBySystem = (extra: Record<string, unknown> = {}) => ({ ...running, group: "needs_you", status: "blocked", stopCause: "restarted", actions: ["rename"],
    events: [{ id: "stop", kind: "isoform", at, code: "stopped", params: { cause: "restarted" } }], ...extra });
const stoppedByYou = (extra: Record<string, unknown> = {}) => ({ ...running, group: "done", status: "aborted", stoppedBy: "person", actions: ["rename"], outcome: { kind: "stopped", by: "you", at }, ...extra });

beforeEach(async () => { resetPagePrivacyWithdrawal(); vi.clearAllMocks(); await i18n.changeLanguage("en"); mocks.action.mockImplementation(async () => mocks.get()); });
afterEach(() => { vi.useRealTimers(); setComputerClock("12h"); });

it("posts a reply while the task runs as Waiting · Undo, and Undo puts its words back in the box (v1.0.5)", async () => {
    mocks.get.mockResolvedValue(running);
    render(<TaskThreadPane id="task" />);
    const box = await screen.findByRole("textbox", { name: "Add a note for its next step…" });
    expect(box).toHaveAttribute("placeholder", "Add a note for its next step…");
    mocks.reply.mockResolvedValue({ ...running, replies: [reply("r1", "Use the blue deck")] });
    fireEvent.change(box, { target: { value: "Use the blue deck" } });
    fireEvent.submit(box.closest("form")!);
    const bubble = await screen.findByTestId("task-reply");
    expect(bubble).toHaveTextContent("Use the blue deck");
    expect(bubble.className).toContain("opacity-60");
    const line = screen.getByTestId("reply-line");
    expect(line.textContent).toBe("Waiting · Undo");
    expect(box).toHaveValue("");
    fireEvent.change(box, { target: { value: "Also" } });
    mocks.action.mockResolvedValueOnce({ ...running, replies: [] });
    fireEvent.click(within(line).getByRole("button", { name: "Undo" }));
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("task", { action: "reply_undo", eventId: "r1" }));
    await waitFor(() => expect(box).toHaveValue("Also\nUse the blue deck"));
    expect(screen.queryByTestId("task-reply")).toBeNull();
});

it("says It was already picked up. when Undo comes too late", async () => {
    mocks.get.mockResolvedValue({ ...running, replies: [reply("r1", "Use the blue deck")] });
    render(<TaskThreadPane id="task" />);
    const undo = await screen.findByRole("button", { name: "Undo" });
    mocks.action.mockRejectedValueOnce(new Error("it was already picked up"));
    mocks.get.mockResolvedValue({ ...running, events: [{ id: "r1", kind: "you", at, text: "Use the blue deck" }], replies: [reply("r1", "Use the blue deck", { state: "picked_up", pickedUpAt: at })] });
    fireEvent.click(undo);
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("task", { action: "reply_undo", eventId: "r1" }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(await screen.findByText("It was already picked up.")).toBeInTheDocument();
});

it("shows Picked up at {time} at full strength only once the runner confirmed it", async () => {
    mocks.get.mockResolvedValue({ ...running, events: [{ id: "r1", kind: "you", at, text: "Use the blue deck" }], replies: [reply("r1", "Use the blue deck", { state: "picked_up", pickedUpAt: at })] });
    render(<TaskThreadPane id="task" />);
    const bubble = await screen.findByText("Use the blue deck");
    expect(bubble.className).not.toContain("opacity-60");
    expect(screen.getByTestId("reply-line").textContent).toBe(`Picked up at ${clockAt("en")}`);
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull();
});

it("keeps a reply that didn't save with Didn't send · Try again · Edit", async () => {
    mocks.get.mockResolvedValue(running);
    render(<TaskThreadPane id="task" />);
    const box = await screen.findByRole("textbox", { name: "Add a note for its next step…" });
    mocks.reply.mockRejectedValueOnce(new Error("store unavailable"));
    fireEvent.change(box, { target: { value: "Keep it short" } });
    fireEvent.submit(box.closest("form")!);
    const failed = await screen.findByTestId("reply-failed");
    expect(within(failed).getByText("Keep it short")).toBeInTheDocument();
    expect(within(failed).getByTestId("reply-line").textContent).toBe("Didn't send · Try again · Edit");
    expect(within(failed).getByTestId("reply-line").className).toContain("text-copper");
    fireEvent.click(within(failed).getByRole("button", { name: "Edit" }));
    expect(box).toHaveValue("Keep it short");
});

it("after Isoform stops it, the message says the reply is kept and Leave it out switches with Use it (v1.0.5 §4, v1.0.6)", async () => {
    mocks.get.mockResolvedValue(stoppedBySystem({ replies: [reply("r1", "Keep it short")] }));
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText(`I stopped at ${clockAt("en")} because Isoform restarted. Your reply is kept, and goes in when you start again.`)).toBeInTheDocument();
    expect(screen.getByTestId("reply-line").textContent).toBe("Goes in when you start again · Leave it out");
    mocks.action.mockResolvedValueOnce(stoppedBySystem({ replies: [reply("r1", "Keep it short", { leftOut: true })] }));
    fireEvent.click(screen.getByRole("button", { name: "Leave it out of the next run" }));
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("task", { action: "reply_leave_out", eventId: "r1" }));
    await waitFor(() => expect(screen.getByTestId("reply-line").textContent).toBe("Left out · Use it"));
    expect(screen.getByText("Keep it short")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start again" })).toBeEnabled();
    expect(document.body.textContent).not.toMatch(/Dismiss|may have been read/);
});

it("after you stop it, replies go in when you start over, and a picked-up one can be left out", async () => {
    mocks.get.mockResolvedValue(stoppedByYou({ events: [{ id: "r2", kind: "you", at, text: "Use the blue deck" }], replies: [reply("r1", "Keep it short", { sentAt: "2026-10-31T22:05:00Z" }), reply("r2", "Use the blue deck", { state: "picked_up", pickedUpAt: at })] }));
    render(<TaskThreadPane id="task" />);
    await screen.findByText("Keep it short");
    expect(screen.getAllByTestId("reply-line").map((line) => line.textContent)).toEqual([`Picked up at ${clockAt("en")} · Leave it out`, "Goes in when you start over · Leave it out"]);
    expect(within(screen.getByTestId("task-stopped-line")).getByRole("button", { name: "Start over" })).toBeEnabled();
});

it("asks about an uncertain reply with no choice made, and Start again waits for it and for the old run (v1.0.7)", async () => {
    const uncertain = (choice?: string) => stoppedBySystem({ repliesToChoose: choice ? 0 : 1, replies: [reply("r1", "Send the deck to Dan", { state: "uncertain", ...(choice ? { choice } : {}) })] });
    mocks.get.mockResolvedValue(uncertain());
    let finishCheck!: () => void;
    mocks.action.mockImplementation((_id: string, input: { action: string }) => input.action === "run_ended" ? new Promise((resolve) => { finishCheck = () => resolve(uncertain()); }) : Promise.resolve(mocks.get()));
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText(`I stopped at ${clockAt("en")} because Isoform restarted. I can't tell whether it acted on one of your replies before it stopped. Choose whether to use it again.`)).toBeInTheDocument();
    const card = screen.getByTestId("reply-uncertain");
    expect(within(card).getByText("It may have acted on this before it stopped.").closest("p")!.className).toContain("text-copper");
    expect(within(card).getAllByRole("button").map((button) => [button.textContent, button.getAttribute("aria-pressed")])).toEqual([["Leave it out", null], ["Use it again", null]]);
    expect(screen.getByRole("button", { name: "Start again" })).toBeDisabled();
    expect(screen.getByTestId("task-start-hint").textContent).toBe("Choose for each one above, then start again.");
    mocks.get.mockResolvedValue(uncertain("use_again"));
    fireEvent.click(within(card).getByRole("button", { name: "Use it again" }));
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("task", { action: "reply_use_again", eventId: "r1" }));
    await waitFor(() => expect(screen.getByTestId("reply-line").textContent).toBe("Using it again · Change"));
    expect(screen.getByTestId("task-run-check").textContent).toBe("Checking that the last run has ended…");
    expect(screen.getByRole("button", { name: "Start again" })).toBeDisabled();
    await act(async () => { finishCheck(); });
    await waitFor(() => expect(screen.getByRole("button", { name: "Start again" })).toBeEnabled());
    mocks.get.mockResolvedValue(uncertain());
    fireEvent.click(screen.getByRole("button", { name: "Change" }));
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("task", { action: "reply_change", eventId: "r1" }));
    expect(await screen.findByTestId("reply-uncertain")).toBeInTheDocument();
});

it("says Couldn't confirm the last run ended with Try again", async () => {
    mocks.get.mockResolvedValue(stoppedBySystem({ replies: [reply("r1", "Send it", { state: "uncertain", choice: "left_out" })] }));
    mocks.action.mockRejectedValue(new Error("couldn't confirm the last run ended"));
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText("Couldn't confirm the last run ended.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start again" })).toBeDisabled();
    expect(screen.getByTestId("reply-line").textContent).toBe("Left out · Change");
});

it("after you stop it with an uncertain reply, the last line asks you to choose first", async () => {
    mocks.get.mockResolvedValue(stoppedByYou({ replies: [reply("r1", "Send it", { state: "uncertain" })] }));
    render(<TaskThreadPane id="task" />);
    expect((await screen.findByTestId("task-stopped-line")).textContent).toBe("Stopped · Choose for each reply above, then start over.");
    expect(screen.queryByRole("button", { name: "Start over" })).toBeNull();
});

it("a left-out reply reads Left out of this run once the new run starts", async () => {
    mocks.get.mockResolvedValue({ ...running, replies: [reply("r1", "Keep it short", { leftOut: true, leftOutOfRun: true })] });
    render(<TaskThreadPane id="task" />);
    await screen.findByText("Keep it short");
    expect(screen.getByTestId("reply-line").textContent).toBe("Left out of this run");
});

it("asks about a step with no proof, with where to check, and Start again waits for an answer (v1.0.2 §1)", async () => {
    const steps = (answer?: string) => [
        { id: "done", kind: "write_file", target: "/Users/alex/Sites/faq.md", at, app: "Finder", proof: "done" },
        { id: "push", kind: "run_command", name: "Push the branch", target: "git push origin main", at, proof: "unknown", ...(answer ? { answer } : {}) },
        { id: "save", kind: "write_file", target: "/Users/alex/Sites/pricing.md", at, app: "Finder", proof: "unknown", ...(answer ? { answer } : {}) },
    ];
    mocks.get.mockResolvedValue(stoppedBySystem({ effectSteps: steps() }));
    render(<TaskThreadPane id="task" />);
    expect((await screen.findByTestId("step-already-done")).textContent).toBe("Already done: faq.md · Check in Finder");
    expect(screen.getByText("2 steps may already have happened before I stopped. I couldn't confirm them, so starting again could do them twice.")).toBeInTheDocument();
    const cards = screen.getAllByTestId("effect-step-card");
    expect(within(cards[0]).getByText("Push the branch")).toBeInTheDocument();
    expect(within(cards[1]).getByText(clockAt("en"), { exact: false }).textContent).toBe(`${clockAt("en")} · Check in Finder`);
    expect(within(cards[1]).getAllByRole("button").map((button) => [button.textContent, button.getAttribute("aria-pressed")])).toEqual([["Check in Finder", null], ["Skip it", "false"], ["Do it again", "false"]]);
    const finder = within(cards[1]).getByRole("button", { name: "Check in Finder" });
    expect(finder).toHaveClass("text-accent");
    fireEvent.click(finder);
    await waitFor(() => expect(mocks.reveal).toHaveBeenCalledWith("task", "save"));
    expect(screen.getByRole("button", { name: "Start again" })).toBeDisabled();
    mocks.get.mockResolvedValue(stoppedBySystem({ effectSteps: steps("skip") }));
    fireEvent.click(within(cards[0]).getByRole("button", { name: "Skip it" }));
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("task", { action: "step_skip", eventId: "push" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Start again" })).toBeEnabled());
});

it("names a skipped step in the Started again line and the finished outcome", async () => {
    mocks.get.mockResolvedValue({ ...running, group: "done", status: "completed", outcome: { kind: "completed", by: "isoform", at, params: { summary: "Posted the FAQ." } },
        events: [{ id: "again", kind: "line", at, code: "started_again", params: { skipped: "Push the branch", skippedCount: "1" } }],
        skipped: [{ id: "push", kind: "run_command", name: "Push the branch", at, proof: "unknown", answer: "skip" }] });
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText(`Started again · ${clockAt("en")} · skipping: Push the branch`)).toBeInTheDocument();
    expect(screen.getByTestId("task-skipped").textContent).toBe("Skipped, as you chose: Push the branch");
});

it("words the reply lines and step cards in Chinese", async () => {
    setComputerClock("24h");
    await i18n.changeLanguage("zh-CN");
    mocks.get.mockResolvedValue(stoppedBySystem({ replies: [reply("r1", "发给 Dan", { state: "uncertain" }), reply("r2", "短一点", { sentAt: "2026-10-31T22:05:00Z" })],
        effectSteps: [{ id: "save", kind: "write_file", target: "/Users/alex/pricing.md", at, app: "Finder", proof: "unknown" }] }));
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText("停下之前，它可能已经照这条做了。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "再用一次" })).toBeInTheDocument();
    expect(screen.getAllByTestId("reply-line").map((line) => line.textContent)).toContain("重新开始时会用上 · 不用这条");
    expect(screen.getByText("停下之前，有一步可能已经完成了。我没法确认，所以重新开始可能会做第二次。")).toBeInTheDocument();
    expect(screen.getByText(/去访达看看/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "跳过" })).toBeInTheDocument();
    expect(screen.getByTestId("task-start-hint").textContent).toBe("先把上面的逐条选好，再重新开始。");
});

it("opens the site for a step it can't see, with Check on {place} (v1.0.12)", async () => {
    mocks.get.mockResolvedValue(stoppedBySystem({ effectSteps: [{ id: "post", kind: "post", name: "Post the FAQ to the help site", target: "https://help.example.com/faq", at, app: "the help site", proof: "unknown" }] }));
    render(<TaskThreadPane id="task" />);
    fireEvent.click(await screen.findByRole("button", { name: "Check on the help site" }));
    expect(mocks.openURL).toHaveBeenCalledWith("https://help.example.com/faq");
});

it("reads as one timeline: steps by when they finished, replies by when they were picked up, waiting replies last (v1.0.12)", async () => {
    const t = (min: number) => new Date(Date.parse(at) + min * 60_000).toISOString();
    mocks.get.mockResolvedValue({ ...running, progress: { done: 2, total: 3 },
        events: [{ id: "ask", kind: "you", at: t(0), text: "Make the deck" }, { id: "ack", kind: "isoform", at: t(1), text: "On it." }, { id: "early", kind: "you", at: t(2), text: "Early note" }, { id: "late", kind: "you", at: t(4), text: "Late note" }, { id: "wait", kind: "you", at: t(5), text: "Waiting note" }],
        replies: [reply("early", "Early note", { sentAt: t(2), state: "picked_up", pickedUpAt: t(2) }), reply("late", "Late note", { sentAt: t(4), state: "picked_up", pickedUpAt: t(6) }), reply("wait", "Waiting note", { sentAt: t(5) })],
        steps: [{ text: "Outline", state: "done" }, { text: "Market slide", state: "done", at: t(3) }, { text: "Team slide", state: "current" }] });
    render(<TaskThreadPane id="task" />);
    await screen.findByText("Waiting note");
    const order = ["Make the deck", "On it.", "Outline", "Early note", "Market slide", "Late note", "Team slide", "Waiting note"].map((text) => screen.getAllByText(text)[0]);
    for (let i = 1; i < order.length; i++) expect(order[i - 1].compareDocumentPosition(order[i]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getAllByTestId("task-step-done")).toHaveLength(2);
});
