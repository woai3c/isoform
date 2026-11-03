import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({ get: vi.fn(), events: new Map<string, () => void>(), withdraw: null as (() => void) | null }));
vi.mock("@/lib/wails-shim-app", () => ({ GetDoneForYou: mocks.get }));
vi.mock("@/lib/page-privacy-withdrawal", () => ({ onPagePrivacyWithdrawal: (fn: () => void) => { mocks.withdraw = fn; return () => { mocks.withdraw = null; }; } }));
vi.mock("../../wailsjs/runtime/runtime", () => ({ EventsOn: (event: string, fn: () => void) => { mocks.events.set(event, fn); return () => mocks.events.delete(event); } }));
import { useTodayDone } from "./useTodayDone";
const item = (id: string) => ({ id, kind: "work_finished", sentence: id, at: 1, targetKind: "handoff", targetId: id });
beforeEach(() => { mocks.get.mockReset(); mocks.events.clear(); mocks.withdraw = null; });
afterEach(() => vi.clearAllMocks());
it("refreshes when a new brief arrives", async () => {
    mocks.get.mockResolvedValueOnce({ items: [item("one")], total: 1 }).mockResolvedValueOnce({ items: [], total: 0 });
    const hook = renderHook(() => useTodayDone());
    await waitFor(() => expect(hook.result.current.items).toHaveLength(1));
    act(() => mocks.events.get("today:done-changed")?.());
    await waitFor(() => expect(hook.result.current.items).toHaveLength(0));
    hook.unmount();
});
it("clears withdrawn data and ignores an earlier late read", async () => {
    let finish!: (page: unknown) => void;
    mocks.get.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; })).mockResolvedValueOnce({ items: [item("safe")], total: 1 });
    const hook = renderHook(() => useTodayDone());
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(1));
    act(() => mocks.withdraw?.());
    await waitFor(() => expect(hook.result.current.items.map((row) => row.id)).toEqual(["safe"]));
    await act(async () => finish({ items: [item("withdrawn")], total: 1 }));
    expect(hook.result.current.items.map((row) => row.id)).toEqual(["safe"]);
    hook.unmount();
});
