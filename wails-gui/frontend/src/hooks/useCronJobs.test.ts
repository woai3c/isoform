import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { useCronJobs } from "./useCronJobs";

const mocks = vi.hoisted(() => ({
    listCronJobs: vi.fn(),
    updateCronJob: vi.fn(),
    deleteCronJob: vi.fn(),
    toastError: vi.fn(),
}));

vi.mock("../../wailsjs/go/main/App", () => ({
    ListCronJobs: () => mocks.listCronJobs(),
    CreateCronJob: vi.fn(),
    UpdateCronJob: (...args: unknown[]) => mocks.updateCronJob(...args),
    DeleteCronJob: (...args: unknown[]) => mocks.deleteCronJob(...args),
    RunCronJobNow: vi.fn(),
    GetCronRuns: vi.fn(),
    LoadCronRunLogs: vi.fn(),
    ReorderCronJobs: vi.fn(),
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: () => () => undefined,
}));

vi.mock("sonner", () => ({
    toast: {
        error: (...args: unknown[]) => mocks.toastError(...args),
    },
}));

const job = { id: "job-1", name: "Weekly review", enabled: false };

async function renderLoaded() {
    const hook = renderHook(() => useCronJobs());
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
    return hook;
}

beforeEach(async () => {
    vi.clearAllMocks();
    vi.spyOn(console, "log").mockImplementation(() => undefined);
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    await i18n.changeLanguage("en");
    mocks.listCronJobs.mockResolvedValue([{ ...job }]);
    mocks.updateCronJob.mockResolvedValue({ ...job, enabled: true });
    mocks.deleteCronJob.mockResolvedValue(undefined);
});

describe("useCronJobs refusals", () => {
    it("reports a refused turn-on with a translated lead and the reason", async () => {
        const reason =
            'this automation is covered by the related automation "Daily review"; turn that one on instead';
        mocks.updateCronJob.mockRejectedValueOnce(reason);
        const { result } = await renderLoaded();

        let turnedOn: boolean | undefined;
        await act(async () => {
            turnedOn = await result.current.toggleEnabled(job.id, true);
        });

        expect(turnedOn).toBe(false);
        expect(mocks.updateCronJob).toHaveBeenCalledWith(job.id, {
            enabled: true,
        });
        expect(mocks.toastError).toHaveBeenCalledWith(
            "This schedule couldn't be turned on.",
            { duration: 5000, description: reason },
        );
    });

    it("translates the lead line for the Chinese interface", async () => {
        await i18n.changeLanguage("zh-CN");
        mocks.updateCronJob.mockRejectedValueOnce("evidence expired");
        const { result } = await renderLoaded();

        await act(async () => {
            await result.current.toggleEnabled(job.id, true);
        });

        expect(mocks.toastError).toHaveBeenCalledWith("无法开启这个定时任务。", {
            duration: 5000,
            description: "evidence expired",
        });
    });

    it("reports a refused pause", async () => {
        mocks.updateCronJob.mockRejectedValueOnce(
            new Error("cron scheduler is unavailable"),
        );
        const { result } = await renderLoaded();

        let paused: boolean | undefined;
        await act(async () => {
            paused = await result.current.toggleEnabled(job.id, false);
        });

        expect(paused).toBe(false);
        expect(mocks.toastError).toHaveBeenCalledWith(
            "This schedule couldn't be paused.",
            { duration: 5000, description: "cron scheduler is unavailable" },
        );
    });

    it("resolves true without a notice when the toggle is accepted", async () => {
        const { result } = await renderLoaded();

        let turnedOn: boolean | undefined;
        await act(async () => {
            turnedOn = await result.current.toggleEnabled(job.id, true);
        });

        expect(turnedOn).toBe(true);
        expect(mocks.toastError).not.toHaveBeenCalled();
    });

    it("keeps a schedule whose delete was refused and says why", async () => {
        mocks.deleteCronJob.mockRejectedValueOnce(
            "load Agency schedule permission evidence: decision storage is unavailable",
        );
        const { result } = await renderLoaded();

        let deleted: boolean | undefined;
        await act(async () => {
            deleted = await result.current.deleteJob(job.id);
        });

        expect(deleted).toBe(false);
        expect(result.current.jobs.map((j) => j.id)).toEqual([job.id]);
        expect(mocks.toastError).toHaveBeenCalledWith(
            "This schedule couldn't be deleted.",
            {
                duration: 5000,
                description:
                    "load Agency schedule permission evidence: decision storage is unavailable",
            },
        );
    });

    it("removes a deleted schedule", async () => {
        const { result } = await renderLoaded();

        let deleted: boolean | undefined;
        await act(async () => {
            deleted = await result.current.deleteJob(job.id);
        });

        expect(deleted).toBe(true);
        expect(result.current.jobs).toEqual([]);
        expect(mocks.toastError).not.toHaveBeenCalled();
    });
});

// Scheduled's detail and Tasks say a refusal in place (K6): no toast.
describe("refusals said in place", () => {
    it("hands pause, delete and run refusals to the caller instead of a toast", async () => {
        mocks.listCronJobs.mockResolvedValue([job]);
        mocks.updateCronJob.mockRejectedValue(new Error("storage unavailable"));
        mocks.deleteCronJob.mockRejectedValue(new Error("busy"));
        const hook = await renderLoaded();
        const onRefused = vi.fn();
        mocks.toastError.mockClear();
        await act(async () => {
            expect(await hook.result.current.toggleEnabled(job.id, true, { onRefused })).toBe(false);
            expect(await hook.result.current.deleteJob(job.id, { onRefused })).toBe(false);
        });
        expect(onRefused).toHaveBeenNthCalledWith(1, "storage unavailable");
        expect(onRefused).toHaveBeenNthCalledWith(2, "busy");
        expect(mocks.toastError).not.toHaveBeenCalled();
    });
});
