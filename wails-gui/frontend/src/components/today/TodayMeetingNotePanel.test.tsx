import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import "@/i18n";
import { TodayMeetingNotePanel } from "./TodayMeetingNotePanel";

const fixtures = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn(), ask: vi.fn(), stop: vi.fn(), chat: { items: [] as Array<{ id: string; kind: string; content: string }>, pending: null, waiting: false, stopped: false, lastUserItemId: "", runId: "" } }));
vi.mock("@/lib/wails-shim-app", () => ({ GetTodayMeetingNote: fixtures.get, SendTodayMeetingNote: fixtures.send }));
vi.mock("@/hooks/useMainChatPanel", () => ({ useMainChatPanel: () => ({ ...fixtures.chat, send: fixtures.ask, stop: fixtures.stop }) }));
const request = { id: 1, prompt: "Draft a note to Mei", meeting: { meetingId: "m", meetingTitle: "Coffee", startAt: 1234, endAt: 2234, personId: "p", personName: "Mei" } };
const note = { answer: { answerItemId: "note-1", userItemId: "user-1", answer: "See you soon.", question: "Draft a note" }, name: "Mei", address: "mei@example.test", subject: "Coffee", state: "ready" as const };
beforeEach(() => { fixtures.get.mockReset().mockResolvedValue(note); fixtures.send.mockReset().mockResolvedValue(undefined); fixtures.ask.mockReset().mockResolvedValue("sent"); fixtures.stop.mockReset().mockResolvedValue(undefined); fixtures.chat.items = []; fixtures.chat.lastUserItemId = ""; fixtures.chat.waiting = false; fixtures.chat.runId = ""; });
afterEach(() => vi.useRealTimers());

it("opens the saved meeting draft, edits it, and sends only after Undo expires", async () => {
    render(<TodayMeetingNotePanel request={request} onClose={vi.fn()} />);
    expect(screen.getByText("For your meeting with Mei.")).toBeInTheDocument();
    expect(await screen.findByText("See you soon.")).toBeInTheDocument();
    expect(screen.getByText(/mei@example.test/)).toBeInTheDocument();
    expect(fixtures.ask).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Note text" }), { target: { value: "See you at coffee." } });
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(fixtures.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await act(async () => vi.advanceTimersByTime(5000));
    expect(fixtures.send).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await act(async () => vi.advanceTimersByTime(5000));
    expect(fixtures.send).toHaveBeenCalledWith(expect.objectContaining({ sourceKind: "calendar", sourceId: "m", meetingNote: true }), "note-1", "See you at coffee.");
    expect(screen.getByText("Sent to Mei")).toBeInTheDocument();
});

it("starts a source-bound Ask when no meeting note exists", async () => {
    fixtures.get.mockResolvedValueOnce(null).mockResolvedValueOnce({ ...note, answer: { ...note.answer, answerItemId: "note-2" } });
    fixtures.chat.lastUserItemId = "user-1";
    fixtures.chat.items = [{ id: "user-1", kind: "user", content: "Draft a note" }, { id: "answer", kind: "assistant", content: "See you soon." }];
    render(<TodayMeetingNotePanel request={{ ...request, id: 2 }} onClose={vi.fn()} />);
    await waitFor(() => expect(fixtures.ask).toHaveBeenCalledWith(request.prompt, expect.objectContaining({ meetingNote: true, sourceId: "m" }), true));
    expect(await screen.findByRole("button", { name: "Send" })).toBeEnabled();
    expect(fixtures.send).not.toHaveBeenCalled();
});

it("reports a failed Stop without claiming the draft stopped", async () => {
    fixtures.get.mockResolvedValueOnce(null);
    fixtures.chat.waiting = true;
    fixtures.chat.runId = "run-1";
    fixtures.stop.mockRejectedValueOnce(new Error("offline"));
    render(<TodayMeetingNotePanel request={{ ...request, id: 3 }} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't stop the draft");
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
});
