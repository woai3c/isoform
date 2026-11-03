import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import "@/i18n";
import { TodayNoticedAction } from "./TodayNoticedAction";

const mocks = vi.hoisted(() => ({ add: vi.fn(), undo: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ AddTodayNoticedSchedule: mocks.add, UndoTodayNoticedSchedule: mocks.undo }));
const target = { date: "2026-10-19", suggestionId: "notice", action: "follow_up" as const };
beforeEach(() => { mocks.add.mockReset(); mocks.undo.mockReset(); });

it("adds in place and undoes the exact saved schedule", async () => {
    const saved = { id: "schedule", revision: "v1", proof: "proof" };
    mocks.add.mockResolvedValue(saved); mocks.undo.mockResolvedValue(undefined);
    const edit = vi.fn();
    render(<TodayNoticedAction target={target} label="Add follow-up" primary onEdit={edit} />);
    fireEvent.click(screen.getByRole("button", { name: "Add follow-up" }));
    expect(await screen.findByText("Added to Scheduled")).toBeInTheDocument();
    expect(edit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await waitFor(() => expect(mocks.undo).toHaveBeenCalledWith(target, saved));
    expect(await screen.findByRole("button", { name: "Add follow-up" })).toBeInTheDocument();
});

it("leaves a failed action available for retry", async () => {
    mocks.add.mockRejectedValueOnce(new Error("unavailable")).mockResolvedValueOnce({ id: "schedule", revision: "v1", proof: "proof" });
    render(<TodayNoticedAction target={target} label="Add follow-up" primary onEdit={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Add follow-up" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't add the follow-up");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Added to Scheduled")).toBeInTheDocument();
});
