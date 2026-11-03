import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import "@/i18n";
import { TodayReplyDraftPanel } from "./TodayReplyDraftPanel";

const mocks = vi.hoisted(() => ({ open: vi.fn(), cancel: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ OpenTodayReplyDraft: mocks.open, CancelTodayReplyDraft: mocks.cancel }));
vi.mock("./TodayNewMessageDraft", () => ({ TodayNewMessageDraft: () => <div data-testid="new-message-draft" /> }));
vi.mock("@/components/tasks/TaskThreadPane", () => ({ TaskThreadPane: ({ id }: { id: string }) => <div data-testid="opened-task">{id}</div> }));

beforeEach(() => { mocks.open.mockReset(); mocks.cancel.mockReset().mockResolvedValue(undefined); });

it("opens a draft frame at once and waits for the source-checked draft", async () => {
    let resolve!: (value: string) => void;
    mocks.open.mockReturnValue(new Promise<string>((done) => { resolve = done; }));
    render(<TodayReplyDraftPanel request={{ id: 1, briefId: "brief", cardId: "card", message: "Draft my reply", label: "Draft" }} onClose={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Edit" })).toBeDisabled();
    expect(mocks.open).toHaveBeenCalledWith("brief", "card", "Draft my reply", "Draft", expect.any(String));
    await act(async () => resolve("draft.task"));
    expect(screen.getByTestId("opened-task")).toHaveTextContent("draft.task");
});

it("does not ask for a destination when the reply route is unavailable", async () => {
    mocks.open.mockRejectedValueOnce(new Error("TODAY_REPLY_DRAFT_NO_ROUTE"));
    render(<TodayReplyDraftPanel request={{ id: 1, briefId: "brief", cardId: "card", message: "Draft", label: "Draft" }} onClose={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't finish the draft");
    expect(screen.queryByTestId("unrouted-draft")).not.toBeInTheDocument();
});

it("keeps a retry for a temporary reply generation failure", async () => {
    mocks.open.mockRejectedValueOnce(new Error("unavailable")).mockResolvedValueOnce("draft.task");
    render(<TodayReplyDraftPanel request={{ id: 1, briefId: "brief", cardId: "card", message: "Draft", label: "Draft" }} onClose={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't finish the draft");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.getByTestId("opened-task")).toHaveTextContent("draft.task"));
});

it("stops a draft run and ignores its late result", async () => {
    let resolve!: (value: string) => void;
    mocks.open.mockReturnValue(new Promise<string>((done) => { resolve = done; }));
    render(<TodayReplyDraftPanel request={{ id: 1, briefId: "brief", cardId: "card", message: "Draft", label: "Draft" }} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(mocks.cancel).toHaveBeenCalledWith(mocks.open.mock.calls[0][4]));
    expect(await screen.findByRole("alert")).toHaveTextContent("Draft stopped");
    await act(async () => resolve("draft.late"));
    expect(screen.queryByTestId("opened-task")).toBeNull();
});

it("keeps waiting when Stop cannot be confirmed", async () => {
    let resolve!: (value: string) => void;
    mocks.open.mockReturnValue(new Promise<string>((done) => { resolve = done; }));
    mocks.cancel.mockRejectedValueOnce(new Error("unavailable"));
    render(<TodayReplyDraftPanel request={{ id: 1, briefId: "brief", cardId: "card", message: "Draft", label: "Draft" }} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't stop the draft");
    await act(async () => resolve("draft.task"));
    expect(screen.getByTestId("opened-task")).toHaveTextContent("draft.task");
});

it("opens a new message without asking for a reply route when the card has no Mail thread", () => {
    render(<TodayReplyDraftPanel request={{ id: 1, briefId: "brief", cardId: "card", message: "Draft", label: "Draft", newMessage: true }} onClose={vi.fn()} />);
    expect(screen.getByTestId("new-message-draft")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "New message" })).toBeInTheDocument();
    expect(mocks.open).not.toHaveBeenCalled();
});
