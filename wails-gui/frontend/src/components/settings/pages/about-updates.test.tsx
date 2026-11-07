import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
    acknowledge: vi.fn(),
    about: {
        info: null as Record<string, unknown> | null,
        failed: false,
        refresh: vi.fn(),
    },
}));

vi.mock("@/lib/wails-shim-app", () => ({
    AcknowledgeRollbackNotice: mocks.acknowledge,
    CheckForUpdates: vi.fn(),
    GetVersion: vi.fn(),
    DownloadUpdate: vi.fn(),
    SetBackgroundUpdates: vi.fn(),
    RestartToUpdate: vi.fn(),
    SaveSupportLogs: vi.fn(),
    OpenDataFolder: vi.fn(),
    OpenExternalURL: vi.fn(),
}));
vi.mock("@/hooks/use-about-info", () => ({
    useAboutInfo: () => mocks.about,
}));

import i18n from "@/i18n";
import { AboutUpdates } from "./about-updates";

const NOTICE =
    "Isoform 0.2.0 didn't start, so you're back on 0.1.0. The next version installs as usual.";

function aboutInfo(rollbackNotice?: string) {
    return {
        version: "0.1.0",
        build: "release",
        buildTime: "",
        computer: "arm64",
        os: "darwin",
        phones: [],
        dataBytes: 0,
        backgroundUpdates: false,
        lastChecked: 0,
        later: false,
        releaseNotesVersion: "0.1.0",
        releaseNotes: "",
        releaseNotesUrl: "",
        update: { status: "idle" },
        ...(rollbackNotice ? { rollbackNotice } : {}),
    };
}

describe("About rollback notice", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
        mocks.acknowledge.mockResolvedValue(undefined);
        mocks.about.failed = false;
        mocks.about.refresh = vi.fn();
    });

    it("shows the notice once and acknowledges it once, not as a failure", async () => {
        mocks.about.info = aboutInfo(NOTICE);
        const { rerender } = render(<AboutUpdates />);

        expect(screen.getByText(NOTICE)).toBeInTheDocument();
        expect(screen.queryByText(/Could not check or download/)).toBeNull();
        await waitFor(() => expect(mocks.acknowledge).toHaveBeenCalledOnce());

        // The backend clears the notice after the acknowledgement; About keeps the
        // notice it already showed and does not acknowledge a second time.
        mocks.about.info = aboutInfo();
        rerender(<AboutUpdates />);
        expect(screen.getByText(NOTICE)).toBeInTheDocument();
        expect(mocks.acknowledge).toHaveBeenCalledOnce();
    });

    it("shows nothing and acknowledges nothing when there is no rollback", () => {
        mocks.about.info = aboutInfo();
        render(<AboutUpdates />);

        expect(screen.queryByText(NOTICE)).toBeNull();
        expect(mocks.acknowledge).not.toHaveBeenCalled();
    });
});
