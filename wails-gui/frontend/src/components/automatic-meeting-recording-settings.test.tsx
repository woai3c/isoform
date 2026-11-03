import "@/i18n";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MeetingRecordingRow } from "./settings/pages/observation-page";

const mocks = vi.hoisted(() => ({
    getEnabled: vi.fn(),
    setEnabled: vi.fn(),
    toastError: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === "then") return undefined;
        if (name === "SetAutomaticMeetingRecordingEnabled") return mocks.setEnabled;
        return vi.fn();
    },
}));

vi.mock("@/lib/page-queries", () => ({
    GetAutomaticMeetingRecordingEnabled: mocks.getEnabled,
    GetBackgroundListening: vi.fn(),
}));

vi.mock("sonner", () => ({
    toast: {
        error: mocks.toastError,
    },
}));

const SWITCH_NAME = "Record meetings automatically";

describe("MeetingRecordingRow", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.getEnabled.mockResolvedValue(false);
        mocks.setEnabled.mockResolvedValue(undefined);
    });

    it("loads the persisted preference and enables recording from the switch", async () => {
        render(<MeetingRecordingRow listeningOn />);

        expect(
            screen.getByText(
                "When a Zoom, Teams, Slack or Google Meet call starts on your computer, Isoform quietly keeps its audio, transcript and notes with the call on Activity, and learns from the notes. Nothing announces the recording on your computer or phone. Turn this off to keep listening without call recordings or notes.",
            ),
        ).toBeInTheDocument();
        const toggle = screen.getByRole("switch", { name: SWITCH_NAME });
        await waitFor(() => expect(toggle).not.toBeDisabled());
        expect(toggle).not.toBeChecked();

        fireEvent.click(toggle);

        await waitFor(() => expect(mocks.setEnabled).toHaveBeenCalledWith(true));
        expect(toggle).toBeChecked();
    });

    it("rolls back the optimistic change when persistence fails", async () => {
        mocks.getEnabled.mockResolvedValue(true);
        mocks.setEnabled.mockRejectedValue(new Error("save failed"));
        render(<MeetingRecordingRow listeningOn />);

        const toggle = screen.getByRole("switch", { name: SWITCH_NAME });
        await waitFor(() => expect(toggle).not.toBeDisabled());

        fireEvent.click(toggle);

        await waitFor(() => expect(mocks.setEnabled).toHaveBeenCalledWith(false));
        await waitFor(() => expect(toggle).toBeChecked());
        expect(mocks.toastError).toHaveBeenCalledWith(
            "Failed to update meeting recording setting",
        );
    });

    it("keeps the setting disabled when the persisted preference cannot be read", async () => {
        mocks.getEnabled.mockRejectedValue(new Error("native binding unavailable"));
        render(<MeetingRecordingRow listeningOn />);

        const toggle = screen.getByRole("switch", { name: SWITCH_NAME });
        expect(toggle).toBeDisabled();

        expect(
            await screen.findByText(
                "Meeting recording setting is unavailable. Reopen Settings to try again.",
            ),
        ).toBeInTheDocument();
        expect(toggle).toBeDisabled();
        expect(toggle).not.toBeChecked();
        expect(mocks.setEnabled).not.toHaveBeenCalled();
    });

    it("is disabled with a reason while listening is off", async () => {
        mocks.getEnabled.mockResolvedValue(true);
        render(<MeetingRecordingRow listeningOn={false} />);

        const toggle = screen.getByRole("switch", { name: SWITCH_NAME });
        await waitFor(() => expect(mocks.getEnabled).toHaveBeenCalled());
        await waitFor(() => expect(toggle).toBeChecked());
        expect(toggle).toBeDisabled();
        expect(screen.getByText("Turn on Listen to use this.")).toBeInTheDocument();

        fireEvent.click(toggle);
        expect(mocks.setEnabled).not.toHaveBeenCalled();
    });
});
