import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setComputerClock } from "@/lib/brief-time";

import i18n from "@/i18n";
import { TaskThreadPane } from "./TaskThreadPane";
import { resetPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";

const mocks = vi.hoisted(() => ({ get: vi.fn(), action: vi.fn(), reply: vi.fn(), stop: vi.fn(), startAgain: vi.fn(), aiReady: vi.fn(), del: vi.fn(), emit: vi.fn(), events: new Map<string, Set<() => void>>() }));
vi.mock("@/lib/wails-shim-app", () => ({ GetDesktopTaskThread: mocks.get, ActOnDesktopTask: mocks.action, ReplyToDesktopTask: mocks.reply, AbortHandoff: mocks.stop, StartHandoffAgain: mocks.startAgain, AIReady: mocks.aiReady, DeleteHandoff: mocks.del }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({ EventsEmit: mocks.emit, EventsOn: (name: string, callback: () => void) => { const listeners = mocks.events.get(name) ?? new Set(); listeners.add(callback); mocks.events.set(name, listeners); return () => listeners.delete(callback); } }));
const thread = { id: "task", title: "Send the reply", group: "needs_you", status: "needs_review", updatedAt: "2026-10-30T12:00:00Z", origin: { kind: "up_next", at: "2026-10-30T12:00:00Z" }, events: [{ id: "q", kind: "isoform", text: "Send now?", at: "2026-10-30T12:00:00Z", choices: [{ id: "yes", label: "Send now" }] }, { id: "file", kind: "file", file: { name: "report.pdf" }, at: "2026-10-30T12:00:00Z" }], actions: ["mark_done", "rename", "not_now"], steps: [] };
beforeEach(async () => { resetPagePrivacyWithdrawal(); mocks.events.clear(); vi.clearAllMocks(); await i18n.changeLanguage("en"); mocks.get.mockResolvedValue(thread); mocks.action.mockResolvedValue(thread); mocks.reply.mockResolvedValue(thread); });
afterEach(() => { vi.useRealTimers(); setComputerClock("12h"); });
it("renders the thread and sends choices and replies to that task", async () => {
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText("Send now?")).toBeInTheDocument();
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.getByText(/From Up next/)).toBeInTheDocument();
    mocks.action.mockResolvedValueOnce({ ...thread, events: [{ ...thread.events[0], chosenId: "yes" }, thread.events[1], { id: "you", kind: "you", text: "Send now", at: thread.updatedAt }] });
    fireEvent.click(screen.getByRole("button", { name: "Send now" }));
    await waitFor(() => expect(mocks.action).toHaveBeenCalledWith("task", { action: "choose", eventId: "q", choiceId: "yes" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Send now" })).toBeNull());
    fireEvent.change(screen.getByRole("textbox", { name: "Reply about this task" }), { target: { value: "A note" } });
    fireEvent.submit(screen.getByRole("textbox", { name: "Reply about this task" }).closest("form")!);
    await waitFor(() => expect(mocks.reply).toHaveBeenCalledWith("task", "A note"));
});
it("edits a draft and waits five seconds, allowing Undo", async () => {
    const draft = { ...thread, id: "draft.one", actions: ["send"], draft: { channel: "mail", to: "Dana", body: "Original", state: "ready", recipients: { to: ["Dana"], notIncluded: ["Lee"] } } };
    mocks.get.mockResolvedValue(draft); mocks.action.mockResolvedValue(draft);
    render(<TaskThreadPane id="draft.one" />);
    expect(await screen.findByText("Original")).toBeInTheDocument();
    expect(screen.getByText(/Lee is on the thread/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Edit" }), { target: { value: "Edited reply" } });
    vi.useFakeTimers();
    fireEvent.click(within(screen.getByTestId("task-draft-card")).getByRole("button", { name: "Send" }));
    expect(screen.getByText(/Sent to Dana/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(mocks.action).not.toHaveBeenCalledWith("draft.one", expect.objectContaining({ action: "send" }));
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Edit" }), { target: { value: "Edited reply" } });
    fireEvent.click(within(screen.getByTestId("task-draft-card")).getByRole("button", { name: "Send" }));
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(mocks.action).toHaveBeenCalledWith("draft.one", { action: "send", text: "Edited reply" });
});
it("renders a completed outcome in Chinese", async () => {
    await i18n.changeLanguage("zh-CN");
    mocks.get.mockResolvedValue({ ...thread, group: "done", outcome: { kind: "marked_done", by: "you", at: thread.updatedAt } });
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText("你标为完成了。")).toBeInTheDocument();
});
it("withdraws the visible thread before rereading after a privacy change", async () => {
    let finish!: (value: unknown) => void;
    render(<TaskThreadPane id="task" />);
    await screen.findByText("Send now?");
    mocks.get.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    act(() => mocks.events.get("sensitive-apps:updated")?.forEach((listener) => listener()));
    expect(screen.queryByText("Send now?")).toBeNull();
    await act(async () => finish({ ...thread, events: [], title: "Available task" }));
    expect(screen.getByText("Available task")).toBeInTheDocument();
    expect(screen.queryByText("Send now?")).toBeNull();
});

it("reports an opened task only after its exact thread loads", async () => {
    const opened = vi.fn();
    mocks.get.mockRejectedValueOnce(new Error("offline")).mockResolvedValue(thread);
    render(<TaskThreadPane id="task" onOpened={opened} />);
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(1));
    expect(opened).not.toHaveBeenCalled();
    act(() => mocks.events.get("handoff:updated")?.forEach((listener) => listener()));
    await waitFor(() => expect(opened).toHaveBeenCalledTimes(1));
    act(() => mocks.events.get("handoff:updated")?.forEach((listener) => listener()));
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(3));
    expect(opened).toHaveBeenCalledTimes(1);
});
const at = "2026-10-31T22:04:00Z";
const clockAt = (lang: string) => new Date(at).toLocaleTimeString(lang, { hour: "numeric", minute: "2-digit" });
const stoppedThread = (cause: string, extra: Record<string, unknown> = {}) => ({
    ...thread, group: "needs_you", status: "aborted", stopCause: cause, actions: ["rename"],
    events: [{ id: "stop", kind: "isoform", at, code: cause === "no_assistant" ? "stopped_no_assistant" : "stopped", params: { cause, ...(cause === "assistant_error" ? { assistant: "codex" } : {}) }, text: "Mac wording" }], ...extra,
});
it("keeps the loop-guard stop in review until a person starts it again", async () => {
    mocks.get.mockResolvedValue(stoppedThread("loop_guard", { actions: ["mark_done", "rename"], steps: [{ text: "Finished work", state: "done" }] }));
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText("Finished work")).toBeInTheDocument();
    expect(screen.getByText("Needs you · couldn't finish")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start again" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Reply about this task" })).toBeInTheDocument();
    expect(mocks.startAgain).not.toHaveBeenCalled();
    fireEvent.pointerDown(screen.getByRole("button", { name: "More options" }), { button: 0, ctrlKey: false });
    expect(await screen.findByRole("menuitem", { name: "Mark as done" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Delete task" })).toBeInTheDocument();
    expect(mocks.startAgain).not.toHaveBeenCalled();
});
it.each([
    ["en", "no_assistant", (time: string) => `I started at ${time}, but no AI is signed in on this computer, so nothing was done. Set one up, then start again.`, ["Set up", "Start again"], "Needs you · couldn't finish"],
    ["en", "restarted", (time: string) => `I stopped at ${time} because Isoform restarted.`, ["Start again"], "Needs you · couldn't finish"],
    ["en", "timeout", (time: string) => `I stopped at ${time} because nothing happened for two hours.`, ["Start again"], "Needs you · couldn't finish"],
    ["en", "assistant_error", (time: string) => `I stopped at ${time} because Codex stopped with an error.`, ["Start again"], "Needs you · couldn't finish"],
    ["en", "loop_guard", (time: string) => `I stopped at ${time}: it took too many attempts without finishing. Starting again runs it from the start, so if it changed anything in another app, check that first.`, ["Start again"], "Needs you · couldn't finish"],
    ["zh-CN", "no_assistant", (time: string) => `我${time}开始了，但这台电脑上还没有登录 AI，所以什么都没做。设置好以后再重新开始。`, ["去设置", "重新开始"], "需要你 · 没能完成"],
    ["zh-CN", "restarted", (time: string) => `我在${time}停下了，因为 Isoform 重启了。`, ["重新开始"], "需要你 · 没能完成"],
    ["zh-CN", "timeout", (time: string) => `我在${time}停下了，因为两小时没有任何进展。`, ["重新开始"], "需要你 · 没能完成"],
    ["zh-CN", "assistant_error", (time: string) => `我在${time}停下了，因为 Codex 出错停止了。`, ["重新开始"], "需要你 · 没能完成"],
    ["zh-CN", "loop_guard", (time: string) => `我在${time}停下了：尝试了太多次也没完成。重新开始会从头再做一遍，所以如果它在别的应用里改过什么，请先看一下。`, ["重新开始"], "需要你 · 没能完成"],
] as const)("%s thread says why it stopped (%s) with its buttons", async (lang, cause, message, buttons, status) => {
    // These sentences are written for a computer set to the 24-hour clock.
    if (lang === "zh-CN") setComputerClock("24h");
    await i18n.changeLanguage(lang);
    mocks.aiReady.mockResolvedValue(false);
    mocks.get.mockResolvedValue(stoppedThread(cause));
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText(message(clockAt(lang)))).toBeInTheDocument();
    expect(screen.getByText(status)).toBeInTheDocument();
    const actions = within(screen.getByTestId("task-stop-actions"));
    expect(actions.getAllByRole("button").map((button) => button.textContent)).toEqual(buttons);
});
it("makes Set up the main button until an AI is signed in, then Start again", async () => {
    mocks.aiReady.mockResolvedValue(false);
    mocks.get.mockResolvedValue(stoppedThread("no_assistant"));
    const { unmount } = render(<TaskThreadPane id="task" />);
    const setUp = await screen.findByRole("button", { name: "Set up" });
    expect(setUp.className).toContain("bg-solid-btn-bg");
    fireEvent.click(setUp);
    expect(mocks.emit).toHaveBeenCalledWith("settings:open", "ai-provider");
    unmount();
    mocks.aiReady.mockResolvedValue(true);
    render(<TaskThreadPane id="task" />);
    await waitFor(() => expect(screen.getByRole("button", { name: "Start again" }).className).toContain("bg-solid-btn-bg"));
});
it("Start again shows Started · Undo for five seconds, then the thread gains Started again", async () => {
    mocks.get.mockResolvedValue(stoppedThread("restarted"));
    render(<TaskThreadPane id="task" />);
    const startAgain = await screen.findByRole("button", { name: "Start again" });
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mocks.startAgain.mockResolvedValue({ handoffId: "task", sessionId: "s" });
    fireEvent.click(startAgain);
    expect(within(screen.getByTestId("task-restart")).getByText("Started")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Undo" })).toBeInTheDocument();
    mocks.get.mockResolvedValue({ ...thread, group: "running", status: "in_progress", events: [...stoppedThread("restarted").events, { id: "again", kind: "line", at, code: "started_again" }] });
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(mocks.startAgain).toHaveBeenCalledWith("task");
    expect(await screen.findByText(`Started again · ${clockAt("en")}`)).toBeInTheDocument();
});
it("after you stop it, the outcome is gray and the last line is Stopped · Start over", async () => {
    mocks.get.mockResolvedValue({ ...thread, group: "done", status: "aborted", stoppedBy: "person", actions: ["rename"], events: [], outcome: { kind: "stopped", by: "you", at, params: { n: "2", m: "5" } } });
    render(<TaskThreadPane id="task" />);
    const outcome = await screen.findByTestId("task-outcome");
    expect(outcome.textContent).toBe(`You stopped it at ${clockAt("en")}, after 2 of 5 steps.`);
    expect(outcome.className).toContain("text-muted");
    const line = screen.getByTestId("task-stopped-line");
    expect(line.textContent).toContain("Stopped ·");
    expect(within(line).getByRole("button", { name: "Start over" })).toBeInTheDocument();
});
const waiting = { ...thread, id: "h1", title: "Fix the login bug", group: "needs_you", status: "active", actions: ["not_now"], events: [], plan: { will: "Fix the login redirect.", wont: "Touch billing.", need: "Read access to the repo." }, files: ["/repo/src/login.ts"], project: "isoform-web" };
it("shows a waiting task's plan, who does it, files and Do it with Started · Undo (K2)", async () => {
    mocks.get.mockResolvedValue(waiting);
    const onDoIt = vi.fn(async () => undefined);
    render(<TaskThreadPane id="h1" onDoIt={onDoIt} />);
    const plan = await screen.findByTestId("task-plan");
    expect(plan).toHaveTextContent("What I'll doFix the login redirect.What I won't doTouch billing.What I needRead access to the repo.");
    expect(screen.getByTestId("task-who")).toHaveTextContent("Claude Code on your computer does it. You review it.");
    expect(screen.getByTestId("task-project")).toHaveTextContent("In isoform-web.");
    expect(screen.getByTestId("task-files")).toHaveTextContent("login.ts");
    expect(screen.queryByRole("button", { name: "Open chat" })).toBeNull();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    fireEvent.click(screen.getByRole("button", { name: "Do it" }));
    expect(screen.getByText("Started")).toBeInTheDocument();
    expect(onDoIt).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(onDoIt).toHaveBeenCalled();
});
it("says the plan couldn't load and holds Do it", async () => {
    mocks.get.mockResolvedValue({ ...waiting, plan: { will: "", unavailable: true } });
    render(<TaskThreadPane id="h1" onDoIt={vi.fn()} />);
    expect(await screen.findByTestId("task-plan-unavailable")).toHaveTextContent("Couldn't load this task's plan. Try again");
    expect(screen.getByRole("button", { name: "Do it" })).toBeDisabled();
});
it("shows checks only when one wasn't met, and which", async () => {
    mocks.get.mockResolvedValue({ ...thread, checks: { met: 2, total: 3, criteria: ["Tests pass", "Docs updated", "Builds"], unmet: [false, true, false] } });
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText(/2 of 3 checks met ·/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "See which" }));
    expect(screen.getByText("Couldn't confirm: Docs updated")).toBeInTheDocument();
    expect(screen.getByText("Tests pass")).toBeInTheDocument();
});
it("says when nothing could be checked, and shows no checks when all were met", async () => {
    mocks.get.mockResolvedValue({ ...thread, checks: { met: 0, total: 0, criteria: [], unmet: [], unverifiable: true } });
    const { unmount } = render(<TaskThreadPane id="task" />);
    expect(await screen.findByText("Isoform couldn't check this result. Look it over before you mark it done.")).toBeInTheDocument();
    unmount();
    mocks.get.mockResolvedValue(thread);
    render(<TaskThreadPane id="task" />);
    await screen.findByText("Send now?");
    expect(screen.queryByTestId("task-checks")).toBeNull();
});
it("deletes a task from the ··· menu after the confirm", async () => {
    mocks.del.mockResolvedValue(undefined);
    const onDeleted = vi.fn();
    render(<TaskThreadPane id="task" onDeleted={onDeleted} />);
    await screen.findByText("Send now?");
    fireEvent.pointerDown(screen.getByRole("button", { name: "More options" }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitem", { name: "Delete task" }));
    expect(await screen.findByText("Delete “Send the reply”? Its thread goes too. Files it made stay on your computer.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(mocks.del).toHaveBeenCalledWith("task"));
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
});
it("tells the board when the task is gone, and offers Try again when the read fails", async () => {
    mocks.get.mockRejectedValueOnce(new Error("task_gone"));
    const onGone = vi.fn();
    const { unmount } = render(<TaskThreadPane id="task" onGone={onGone} />);
    await waitFor(() => expect(onGone).toHaveBeenCalled());
    unmount();
    mocks.get.mockRejectedValueOnce(new Error("disk"));
    render(<TaskThreadPane id="task" />);
    expect(await screen.findByText("Couldn't load this task.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Send now?")).toBeInTheDocument();
});
it("gives a draft task's thread no reply box and no unavailable line", async () => {
    mocks.get.mockResolvedValue({ ...thread, id: "draft.one", actions: ["send"], draft: { channel: "mail", to: "Dana", body: "Original", state: "ready" } });
    render(<TaskThreadPane id="draft.one" />);
    expect(await screen.findByText("Original")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Reply about this task" })).toBeNull();
    expect(screen.queryByText(/Replies aren/)).toBeNull();
});
