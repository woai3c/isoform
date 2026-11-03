import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useSnapshotFallback } from "./useSnapshotFallback";

describe("useSnapshotFallback", () => {
    it("shows the screenshot until it fails to load", () => {
        const { result } = renderHook(({ src }) => useSnapshotFallback(src), {
            initialProps: { src: "a.jpg" as string | null },
        });
        expect(result.current).toMatchObject({ visibleSrc: "a.jpg", missing: false });

        act(() => result.current.onError());

        expect(result.current).toMatchObject({ visibleSrc: null, missing: true });
    });

    it("retries a failed frame after navigating away and back", () => {
        const { result, rerender } = renderHook(({ src }) => useSnapshotFallback(src), {
            initialProps: { src: "a.jpg" as string | null },
        });
        act(() => result.current.onError());
        expect(result.current.missing).toBe(true);

        rerender({ src: "b.jpg" });
        expect(result.current).toMatchObject({ visibleSrc: "b.jpg", missing: false });

        rerender({ src: "a.jpg" });
        expect(result.current).toMatchObject({ visibleSrc: "a.jpg", missing: false });
    });

    it("treats no URL as nothing to show, not as missing", () => {
        const { result } = renderHook(() => useSnapshotFallback(null));
        act(() => result.current.onError());
        expect(result.current).toMatchObject({ visibleSrc: null, missing: false });
    });
});
