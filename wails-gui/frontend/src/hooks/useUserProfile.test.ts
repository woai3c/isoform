import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ profile: vi.fn() }));

// wailsjs/go/main/App resolves to the shim, so this mock covers the hook.
vi.mock("@/lib/wails-shim-app", () => ({ GetUserProfile: mocks.profile }));

import { refreshUserProfile, useUserProfile } from "./useUserProfile";

describe("useUserProfile", () => {
    it("tells a consumer that mounted after the first fetch about a rename", async () => {
        mocks.profile.mockResolvedValue({ name: "Ada", display_name: "Ada" });
        const first = renderHook(() => useUserProfile());
        await waitFor(() => expect(first.result.current.profile?.display_name).toBe("Ada"));

        // Mounts with the cache warm.
        const later = renderHook(() => useUserProfile());
        expect(later.result.current.profile?.display_name).toBe("Ada");
        expect(mocks.profile).toHaveBeenCalledTimes(1);

        mocks.profile.mockResolvedValue({ name: "Ada L.", display_name: "Ada L." });
        await act(async () => {
            refreshUserProfile();
        });
        await waitFor(() => expect(later.result.current.profile?.display_name).toBe("Ada L."));
        expect(first.result.current.profile?.display_name).toBe("Ada L.");
    });
});
