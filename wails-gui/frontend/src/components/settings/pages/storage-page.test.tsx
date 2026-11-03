import "@/i18n";
import { Children, isValidElement, type ReactElement, type ReactNode } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import en from "@/i18n/locales/en/settings.json";
import zh from "@/i18n/locales/zh-CN/settings.json";
import storageSource from "./storage-page.tsx?raw";
import recordingStorageSource from "./recording-storage.ts?raw";
import { StoragePage } from "./storage-page";

const mocks = vi.hoisted(() => ({
    getRetention: vi.fn(),
    setRetention: vi.fn(),
    getMaxGB: vi.fn(),
    setMaxGB: vi.fn(),
    storageStats: vi.fn(),
    usage: vi.fn(),
    getScreenshotDays: vi.fn(),
    setScreenshotDays: vi.fn(),
    plan: vi.fn(),
    start: vi.fn(),
    status: vi.fn(),
    budget: vi.fn(),
    confirmLimit: vi.fn(),
    toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetActivityRetentionDays: mocks.getRetention,
    SetActivityRetentionDays: mocks.setRetention,
    GetActivityMaxStorageGB: mocks.getMaxGB,
    SetActivityMaxStorageGB: mocks.setMaxGB,
    GetActivityStorageStats: mocks.storageStats,
    GetActivityStorageUsage: mocks.usage,
    GetScreenshotRetentionDays: mocks.getScreenshotDays,
    SetScreenshotRetentionDays: mocks.setScreenshotDays,
    PlanActivityStorageCleanup: mocks.plan,
    StartActivityStorageCleanup: mocks.start,
    GetActivityStorageCleanupStatus: mocks.status,
    GetActivityStorageBudget: mocks.budget,
    ConfirmActivityStorageLimit: mocks.confirmLimit,
}));

vi.mock("@/components/onboarding/models-panel", () => ({ ModelsPanel: () => <div data-testid="storage-models" /> }));

vi.mock("sonner", () => ({ toast: mocks.toast }));

// Radix Select needs pointer APIs jsdom lacks; a native <select> keeps the
// same value / onValueChange contract and the trigger's accessible name.
vi.mock("@/components/ui/select", () => {
    const SelectTrigger = (_: { "aria-label"?: string; className?: string; children?: ReactNode }) => null;
    const SelectValue = () => null;
    const SelectContent = ({ children }: { children?: ReactNode }) => <>{children}</>;
    const SelectItem = ({ value, children }: { value: string; children?: ReactNode }) => (
        <option value={value}>{children}</option>
    );
    const Select = ({
        value,
        onValueChange,
        disabled,
        children,
    }: {
        value: string;
        onValueChange: (value: string) => void;
        disabled?: boolean;
        children?: ReactNode;
    }) => {
        const trigger = Children.toArray(children).find(
            (child): child is ReactElement<{ "aria-label"?: string }> =>
                isValidElement(child) && child.type === SelectTrigger,
        );
        return (
            <select
                aria-label={trigger?.props["aria-label"]}
                value={value}
                disabled={disabled}
                onChange={(event) => onValueChange(event.target.value)}
            >
                {children}
            </select>
        );
    };
    return { Select, SelectTrigger, SelectValue, SelectContent, SelectItem };
});

const GIB = 1024 * 1024 * 1024;

const USAGE = {
    screenshotBytes: 39 * GIB,
    screenshotFiles: 117239,
    screenshotDays: 144,
    oldestScreenshotMs: new Date(2026, 2, 5).getTime(),
    audioBytes: 18 * GIB,
    audioFiles: 442,
    databaseBytes: 8 * GIB,
    otherBytes: 0,
    totalBytes: 65 * GIB,
    measuredAtMs: Date.now(),
};

const EMPTY_ITEM = { count: 0, bytes: 0, afterBytes: 0, oldestMs: 0, newestMs: 0 };

const PLAN = {
    createdAtMs: Date.now(),
    limitBytes: 20 * GIB,
    screenshotDays: 0,
    usage: USAGE,
    unlinkedScreenshots: {
        count: 35747,
        bytes: 13 * GIB,
        afterBytes: 0,
        oldestMs: new Date(2026, 2, 6).getTime(),
        newestMs: new Date(2026, 9, 7).getTime(),
    },
    unlinkedRecordings: EMPTY_ITEM,
    expiredSessions: EMPTY_ITEM,
    retentionDays: 0,
    expiredScreenshots: EMPTY_ITEM,
    overLimitScreenshots: EMPTY_ITEM,
    overLimitRecordings: EMPTY_ITEM,
    shrinkScreenshots: { count: 80000, bytes: 26 * GIB, afterBytes: 9 * GIB, oldestMs: 1, newestMs: 2 },
    compressRecordings: EMPTY_ITEM,
    databaseSpace: { count: 0, bytes: 3 * GIB, afterBytes: 0, oldestMs: 0, newestMs: 0 },
    recordingsNeedFFmpeg: false,
    limitUnreachable: false,
    keptBytes: 0,
    freedBytes: 33 * GIB,
    afterBytes: 32 * GIB,
};

function leafKeys(value: unknown, prefix = ""): string[] {
    if (value === null || typeof value !== "object") return [prefix];
    return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) =>
        leafKeys(child, prefix ? `${prefix}.${key}` : key),
    );
}

async function renderPage() {
    render(<StoragePage />);
    await screen.findByRole("combobox", { name: "Storage limit" });
    await screen.findByRole("combobox", { name: "Keep screenshots for" });
    await screen.findByText("65 GB of 20 GB");
}

describe("StoragePage", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.getRetention.mockResolvedValue(0);
        mocks.getMaxGB.mockResolvedValue(20);
        mocks.setMaxGB.mockResolvedValue(undefined);
        mocks.storageStats.mockResolvedValue({ retainedBytes: 65 * GIB, retainedSessionCount: 6881, earliestRetainedMs: 0 });
        mocks.usage.mockResolvedValue(USAGE);
        mocks.getScreenshotDays.mockResolvedValue(30);
        mocks.setScreenshotDays.mockResolvedValue(undefined);
        mocks.plan.mockResolvedValue(PLAN);
        mocks.start.mockResolvedValue(undefined);
        mocks.status.mockResolvedValue({ running: false, phase: "" });
        mocks.budget.mockResolvedValue({ state: "ok", limitBytes: 20 * GIB, projectedBytes: 0, keptBytes: 0, screenshots: EMPTY_ITEM, recordings: EMPTY_ITEM, checkedAtMs: 1 });
        mocks.confirmLimit.mockResolvedValue(undefined);
    });

    it("shows what each kind of data takes against the limit", async () => {
        await renderPage();
        expect(screen.getByTestId("storage-models")).toBeInTheDocument();
        expect(screen.getByText("Screenshots")).toBeInTheDocument();
        expect(screen.getByText("144 days, since 2026-03-05")).toBeInTheDocument();
        expect(screen.getByText("39 GB")).toBeInTheDocument();
        expect(screen.getByText("Call recordings")).toBeInTheDocument();
        expect(screen.getByText("442 files")).toBeInTheDocument();
        expect(screen.getByText("Activity database")).toBeInTheDocument();
        expect(screen.getByText("8.0 GB")).toBeInTheDocument();
        expect(screen.getByText(/Over the limit/)).toBeInTheDocument();
    });

    it("asks before a lower storage limit removes screenshots", async () => {
        mocks.usage.mockResolvedValue({ ...USAGE, totalBytes: 7 * GIB });
        render(<StoragePage />);
        const select = await screen.findByRole("combobox", { name: "Storage limit" });
        await screen.findByText("7.0 GB of 20 GB");
        expect(within(select).getByRole("option", { name: "5 GB" })).toBeInTheDocument();

        fireEvent.change(select, { target: { value: "10" } });
        await waitFor(() => expect(mocks.setMaxGB).toHaveBeenCalledWith(10));

        fireEvent.change(select, { target: { value: "5" } });
        expect(await screen.findByText("Lower the storage limit to 5 GB?")).toBeInTheDocument();
        expect(mocks.setMaxGB).toHaveBeenCalledTimes(1);
    });

    it("asks before a shorter screenshot time and saves a longer one directly", async () => {
        await renderPage();
        const select = screen.getByRole("combobox", { name: "Keep screenshots for" });
        expect(within(select).getByRole("option", { name: "With recordings (Unlimited)" })).toBeInTheDocument();

        fireEvent.change(select, { target: { value: "90" } });
        await waitFor(() => expect(mocks.setScreenshotDays).toHaveBeenCalledWith(90));

        fireEvent.change(select, { target: { value: "14" } });
        expect(await screen.findByText("Keep screenshots for 14 days?")).toBeInTheDocument();
        expect(mocks.setScreenshotDays).toHaveBeenCalledTimes(1);
        fireEvent.click(screen.getByRole("button", { name: "Remove older screenshots" }));
        await waitFor(() => expect(mocks.setScreenshotDays).toHaveBeenCalledWith(14));
    });

    it("reports the cleanup first and runs it only after confirmation", async () => {
        await renderPage();
        fireEvent.click(screen.getByRole("button", { name: "Check" }));

        expect(await screen.findByText("Screenshots no activity refers to")).toBeInTheDocument();
        expect(screen.getByText("35,747 files, 13 GB, 2026-03-06 to 2026-10-07")).toBeInTheDocument();
        expect(screen.getByText("Store older screenshots smaller")).toBeInTheDocument();
        expect(screen.getByText("80,000 files, 26 GB to about 9.0 GB")).toBeInTheDocument();
        expect(
            screen.getByText("3.0 GB, given back when this computer is next locked, with enough free disk, at most every 12 hours"),
        ).toBeInTheDocument();
        expect(screen.getByText(/Frees about 33 GB now: from 65 GB to about 32 GB\./)).toBeInTheDocument();
        expect(screen.getByText(/The database can give back about 3\.0 GB more later\./)).toBeInTheDocument();
        expect(screen.queryByText(/older than/)).not.toBeInTheDocument();
        expect(mocks.start).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole("button", { name: "Free up 36 GB" }));
        expect(await screen.findByText("Free up 36 GB?")).toBeInTheDocument();
        expect(mocks.start).not.toHaveBeenCalled();

        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            mocks.status.mockResolvedValue({
                running: false,
                phase: "done",
                done: 0,
                total: 0,
                startedAtMs: 1,
                finishedAtMs: 2,
                beforeBytes: 65 * GIB,
                afterBytes: 32 * GIB,
                freedBytes: 33 * GIB,
                databasePending: true,
            });
            fireEvent.click(screen.getByRole("button", { name: "Free up space" }));
            await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
            expect(await screen.findByText("Removing files no activity refers to")).toBeInTheDocument();
            await act(async () => {
                vi.advanceTimersByTime(1100);
            });
            expect(await screen.findByText("Freed 33 GB. Isoform now uses 32 GB.")).toBeInTheDocument();
            expect(
                screen.getByText("The database gives back its unused space the next time this computer is locked."),
            ).toBeInTheDocument();
        } finally {
            vi.useRealTimers();
        }
    });

    it("says so when there is nothing to free", async () => {
        mocks.plan.mockResolvedValue({
            ...PLAN,
            unlinkedScreenshots: EMPTY_ITEM,
            shrinkScreenshots: EMPTY_ITEM,
            databaseSpace: EMPTY_ITEM,
            freedBytes: 0,
            afterBytes: USAGE.totalBytes,
        });
        await renderPage();
        fireEvent.click(screen.getByRole("button", { name: "Check" }));
        expect(await screen.findByText("Nothing to free up right now.")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Free up/ })).not.toBeInTheDocument();
    });
});

describe("StoragePage limit states", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.getRetention.mockResolvedValue(30);
        mocks.getMaxGB.mockResolvedValue(50);
        mocks.storageStats.mockResolvedValue({ retainedBytes: 65 * GIB, retainedSessionCount: 1, earliestRetainedMs: 0 });
        mocks.usage.mockResolvedValue(USAGE);
        mocks.getScreenshotDays.mockResolvedValue(-1);
        mocks.setScreenshotDays.mockResolvedValue(undefined);
        mocks.setMaxGB.mockResolvedValue(undefined);
        mocks.status.mockResolvedValue({ running: false, phase: "" });
        mocks.confirmLimit.mockResolvedValue(undefined);
    });

    it("asks before removing anything on a profile the old count let grow", async () => {
        mocks.budget.mockResolvedValue({
            state: "confirm",
            limitBytes: 50 * GIB,
            projectedBytes: 55 * GIB,
            keptBytes: 0,
            screenshots: { count: 1200, bytes: 4 * GIB, afterBytes: 0, oldestMs: new Date(2026, 2, 5).getTime(), newestMs: new Date(2026, 3, 1).getTime() },
            recordings: { count: 3, bytes: 1 * GIB, afterBytes: 0, oldestMs: new Date(2026, 2, 9).getTime(), newestMs: new Date(2026, 2, 20).getTime() },
            checkedAtMs: 1,
        });
        render(<StoragePage />);
        expect(await screen.findByText(/1,203 files, 5\.0 GB, 2026-03-05 to 2026-04-01/)).toBeInTheDocument();
        expect(mocks.confirmLimit).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole("button", { name: "Remove them" }));
        fireEvent.click(await screen.findByRole("button", { name: "Remove and keep the limit" }));
        await waitFor(() => expect(mocks.confirmLimit).toHaveBeenCalledTimes(1));
    });

    it("says when the limit cannot be reached", async () => {
        mocks.budget.mockResolvedValue({
            state: "unreachable",
            limitBytes: 5 * GIB,
            projectedBytes: 9 * GIB,
            keptBytes: 8 * GIB,
            screenshots: EMPTY_ITEM,
            recordings: EMPTY_ITEM,
            checkedAtMs: 1,
        });
        render(<StoragePage />);
        expect(await screen.findByText(/Nothing is removed for the limit\. Raise it to more than 8\.0 GB\./)).toBeInTheDocument();
    });

    it("asks before a lower limit while usage is unknown", async () => {
        mocks.usage.mockRejectedValue(new Error("busy"));
        mocks.budget.mockResolvedValue({ state: "ok", limitBytes: 0, projectedBytes: 0, keptBytes: 0, screenshots: EMPTY_ITEM, recordings: EMPTY_ITEM, checkedAtMs: 1 });
        render(<StoragePage />);
        const select = await screen.findByRole("combobox", { name: "Storage limit" });
        fireEvent.change(select, { target: { value: "20" } });
        expect(await screen.findByText("Lower the storage limit to 20 GB?")).toBeInTheDocument();
        expect(mocks.setMaxGB).not.toHaveBeenCalled();
    });

    it("says screenshots go with their recordings, text included, until a time is chosen", async () => {
        mocks.budget.mockResolvedValue({ state: "ok", limitBytes: 0, projectedBytes: 0, keptBytes: 0, screenshots: EMPTY_ITEM, recordings: EMPTY_ITEM, checkedAtMs: 1 });
        render(<StoragePage />);
        const select = await screen.findByRole("combobox", { name: "Keep screenshots for" });
        expect(within(select).getByRole("option", { name: "With recordings (30 days)" })).toBeInTheDocument();
        // Only times shorter than the recording limit are offered.
        expect(within(select).queryByRole("option", { name: "30 days" })).not.toBeInTheDocument();
        expect(within(select).queryByRole("option", { name: "90 days" })).not.toBeInTheDocument();
        expect(screen.getByText(/after 30 days the whole recording, its text included, is removed/)).toBeInTheDocument();
        fireEvent.change(select, { target: { value: "14" } });
        expect(await screen.findByText("Keep screenshots for 14 days?")).toBeInTheDocument();
    });
});

describe("Storage copy", () => {
    it("has every key in both languages", () => {
        expect(leafKeys(zh.storage).sort()).toEqual(leafKeys(en.storage).sort());
        expect(leafKeys(zh.pages.storage).sort()).toEqual(leafKeys(en.pages.storage).sort());
    });

    it("never falls back to inline English", () => {
        for (const source of [storageSource, recordingStorageSource]) {
            expect(source).not.toContain("defaultValue");
            expect(source).not.toMatch(/\bt\(\s*["'`][^"'`]+["'`]\s*,\s*["'`]/);
        }
    });
});
