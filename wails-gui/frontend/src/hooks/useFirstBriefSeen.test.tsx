import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useFirstBriefSeen } from "./useFirstBriefSeen";

const mocks = vi.hoisted(() => ({
    tier: vi.fn<() => Promise<string>>(),
    seen: vi.fn(),
    observe: vi.fn(),
    disconnect: vi.fn(),
}));
vi.mock("@/lib/wails-shim-app", () => ({
    GetTelemetryTier: mocks.tier,
    RecordFirstBriefSeen: mocks.seen,
    RecordProductActivity: vi.fn(),
}));
vi.mock("../../wailsjs/runtime/runtime", () => ({ EventsOn: () => () => {} }));

function Harness({ hasBrief = true }: { hasBrief?: boolean }) {
    const ref = useFirstBriefSeen(hasBrief);
    return <section ref={ref} />;
}

describe("first brief seen", () => {
    let intersect!: (entries: Partial<IntersectionObserverEntry>[]) => void;
    let focused = true;
    beforeEach(() => {
        vi.clearAllMocks();
        focused = true;
        Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
        vi.spyOn(document, "hasFocus").mockImplementation(() => focused);
        mocks.tier.mockResolvedValue("detailed");
        mocks.seen.mockResolvedValue(undefined);
        vi.stubGlobal("IntersectionObserver", class {
            constructor(callback: typeof intersect) { intersect = callback; }
            observe = mocks.observe;
            disconnect = mocks.disconnect;
        });
    });
    afterEach(() => {
        cleanup();
        vi.restoreAllMocks();
        vi.unstubAllGlobals();
    });

    it("waits until a real brief is in the focused viewport and sends no content", async () => {
        render(<Harness />);
        await act(async () => {});
        expect(mocks.seen).not.toHaveBeenCalled();
        focused = false;
        act(() => intersect([{ isIntersecting: true }]));
        expect(mocks.seen).not.toHaveBeenCalled();
        act(() => {
            focused = true;
            window.dispatchEvent(new Event("focus"));
            intersect([{ isIntersecting: false }]);
            intersect([{ isIntersecting: true }]);
        });
        expect(mocks.seen.mock.calls).toEqual([[]]);
    });

    it("does not report an empty or loading brief", async () => {
        render(<Harness hasBrief={false} />);
        await act(async () => {});
        expect(mocks.observe).not.toHaveBeenCalled();
        expect(mocks.seen).not.toHaveBeenCalled();
    });

    it("records the local first-view milestone while sharing is off", async () => {
        mocks.tier.mockResolvedValue("off");
        render(<Harness />);
        await act(async () => {});
        expect(mocks.observe).toHaveBeenCalledTimes(1);
        act(() => intersect([{ isIntersecting: true }]));
        expect(mocks.seen.mock.calls).toEqual([[]]);
    });
});
