import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ComputerUseAppAccessDebugSettings } from "./computer-use-app-access-debug-settings";

const mocks = vi.hoisted(() => ({
    getSnapshot: vi.fn(),
    probe: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetComputerUseAppAccessSnapshot: mocks.getSnapshot,
    ProbeComputerUseAppAccess: mocks.probe,
}));

const snapshot = {
    policyVersion: "computer-use-app-access-v1",
    mode: "computer_use_only" as const,
    oauthFallback: false as const,
    available: true,
    accessibility: true,
    screenRecording: true,
    screenLocked: false,
    status: "ready",
    statusReason: "Computer Use is ready.",
    apps: [],
};

describe("ComputerUseAppAccessDebugSettings", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.getSnapshot.mockResolvedValue(snapshot);
        mocks.probe.mockResolvedValue({
            snapshot,
            probedAt: 1,
            receiptHash: "probe-receipt-hash",
            browsers: [
                {
                    bundle: "com.apple.Safari",
                    name: "Safari",
                    pid: 101,
                    active: false,
                    running: true,
                    useCount: 42,
                    windowTitles: ["Inbox - Gmail"],
                },
            ],
        });
    });

    it("probes browser routing and supports compact detail Back and Escape", async () => {
        render(<ComputerUseAppAccessDebugSettings />);
        expect(await screen.findByText("Computer Use")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Test browser access" }));
        await waitFor(() => expect(mocks.probe).toHaveBeenCalledTimes(1));
        fireEvent.click(await screen.findByRole("button", { name: /Safari/ }));

        const detail = screen.getByRole("region", {
            name: "Browser access details",
        });
        expect(detail).toHaveClass("block");
        expect(screen.getByText("Inbox - Gmail")).toBeInTheDocument();
        expect(screen.getByText(/probe-receipt-hash/)).toBeInTheDocument();

        fireEvent.keyDown(detail, { key: "Escape" });
        expect(
            screen.getByRole("region", { name: "Browser access checks" }),
        ).toHaveClass("block");
    });
});
