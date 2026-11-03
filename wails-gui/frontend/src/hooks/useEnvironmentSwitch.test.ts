import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../wailsjs/go/main/App", () => ({ GetEnvironment: mocks.get, SwitchEnvironment: vi.fn() }));
import { useEnvironmentSwitch } from "./useEnvironmentSwitch";

describe("Staging badge release gate", () => {
    it.each([
        [true, false, true],
        [true, true, false],
        [false, true, false],
    ])("staging=%s release=%s shows badge=%s", async (staging, release, visible) => {
        mocks.get.mockResolvedValue({ is_staging: staging, is_release: release, is_dev: false });
        const result = renderHook(() => useEnvironmentSwitch());
        await waitFor(() => expect(mocks.get).toHaveBeenCalled());
        await waitFor(() => expect(result.result.current.environment).toBe(staging ? "staging" : "production"));
        await waitFor(() => expect(result.result.current.showStagingBadge).toBe(visible));
        expect(result.result.current.isStaging).toBe(staging);
    });
});
