import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ get: vi.fn(), events: new Map<string, () => void>(), withdraw: null as (() => void) | null }));
vi.mock("@/lib/wails-shim-app", () => ({ GetNeedsYou: mocks.get }));
vi.mock("@/lib/page-privacy-withdrawal", () => ({ onPagePrivacyWithdrawal: (fn: () => void) => { mocks.withdraw = fn; return () => { mocks.withdraw = null; }; } }));
vi.mock("../../wailsjs/runtime/runtime", () => ({ EventsOn: (event: string, fn: () => void) => { mocks.events.set(event, fn); return () => mocks.events.delete(event); } }));
import { useTodayNeeds } from "./useTodayNeeds";

const item = (id: string) => ({ id, kind: "work_blocked", sentence: id, createdAt: 1, targetKind: "chat_item", targetId: id });
beforeEach(() => { mocks.get.mockReset(); mocks.events.clear(); mocks.withdraw = null; });
afterEach(() => vi.clearAllMocks());

it("shares one ordered projection, and refreshes both readers when waiting state changes", async () => {
    mocks.get.mockResolvedValueOnce({ items: [item("a"), item("b")], total: 2 }).mockResolvedValueOnce({ items: [item("b")], total: 1 });
    const first = renderHook(() => useTodayNeeds());
    const second = renderHook(() => useTodayNeeds());
    await waitFor(() => expect(first.result.current.items.map((row) => row.id)).toEqual(["a", "b"]));
    expect(second.result.current.items.map((row) => row.id)).toEqual(["a", "b"]);
    expect(mocks.get).toHaveBeenCalledTimes(1);
    act(() => mocks.events.get("today:needs-changed")?.());
    await waitFor(() => expect(second.result.current.items.map((row) => row.id)).toEqual(["b"]));
    expect(first.result.current.items.map((row) => row.id)).toEqual(["b"]);
    first.unmount(); second.unmount();
});

it("withdraws cached rows immediately and rejects a late earlier response", async () => {
    let finish!: (page: unknown) => void;
    mocks.get.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; })).mockResolvedValueOnce({ items: [item("safe")], total: 1 });
    const hook = renderHook(() => useTodayNeeds());
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(1));
    act(() => mocks.withdraw?.());
    await waitFor(() => expect(hook.result.current.items.map((row) => row.id)).toEqual(["safe"]));
    await act(async () => finish({ items: [item("withdrawn")], total: 1 }));
    expect(hook.result.current.items.map((row) => row.id)).toEqual(["safe"]);
    hook.unmount();
});

it("does not read while hidden and catches up on showing", async () => {
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    mocks.get.mockResolvedValue({ items: [item("safe")], total: 1 });
    const hook = renderHook(() => useTodayNeeds());
    await act(async () => { await Promise.resolve(); });
    expect(mocks.get).not.toHaveBeenCalled();
    visibility.mockReturnValue("visible");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await waitFor(() => expect(hook.result.current.items.map((row) => row.id)).toEqual(["safe"]));
    hook.unmount();
    visibility.mockRestore();
});

it("keeps the last good list after a failed refresh and retries", async () => {
    mocks.get.mockResolvedValueOnce({ items: [item("a")], total: 1 })
        .mockRejectedValueOnce(new Error("deadline"))
        .mockResolvedValueOnce({ items: [item("b"), item("c")], total: 2 });
    const hook = renderHook(() => useTodayNeeds());
    await waitFor(() => expect(hook.result.current.items.map((row) => row.id)).toEqual(["a"]));
    vi.useFakeTimers();
    act(() => mocks.events.get("today:needs-changed")?.());
    await act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); });
    expect(hook.result.current.items.map((row) => row.id)).toEqual(["a"]);
    expect(hook.result.current.failed).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(hook.result.current.items.map((row) => row.id)).toEqual(["b", "c"]);
    expect(hook.result.current.failed).toBe(false);
    hook.unmount();
    vi.useRealTimers();
});
