import { beforeEach, expect, it, vi } from "vitest";
import {
    fixtureModels,
    modelChoicesFixture,
    modelPlanFixture,
} from "@/components/onboarding/setup-fixtures";
import {
    normalizeModelChoices,
    planSetupDownloads,
    readSetupModels,
    startSetupDownloads,
} from "./setup-model-adapter";
const mocks = vi.hoisted(() => ({
    network: vi.fn(),
    resume: vi.fn(),
    choices: vi.fn(),
    downloads: vi.fn(),
    plan: vi.fn(),
    start: vi.fn(),
}));
vi.mock("./wails-shim-app", () => ({
    GetModelChoices: mocks.choices,
    GetModelNetworkState: mocks.network,
    ResumeModelDownload: mocks.resume,
    GetModelDownloads: mocks.downloads,
    PlanModelDownload: mocks.plan,
    StartModelDownloads: mocks.start,
}));
beforeEach(() => {
    vi.clearAllMocks();
    mocks.network.mockResolvedValue({
        known: true,
        offline: false,
        expensive: false,
        constrained: false,
    });
});
it("uses the backend job keys and returns the exact issued plan on confirmation", async () => {
    const raw = modelChoicesFixture();
    const wirePlan = modelPlanFixture(raw);
    mocks.plan.mockResolvedValue(wirePlan);
    const choices = normalizeModelChoices(raw);
    const plan = await planSetupDownloads(
        {
            folder: raw.modelsFolder,
            sources: { sensevoice: "https://mirror.example/models" },
        },
        choices,
    );
    expect(mocks.plan).toHaveBeenCalledWith(
        expect.objectContaining({ afterCall: "qwen3", speakerPack: "speaker" }),
        { sensevoice: "https://mirror.example/models" },
    );
    await startSetupDownloads(plan);
    expect(mocks.start).toHaveBeenCalledWith(wirePlan);
    expect(mocks.start.mock.calls[0][0]).toBe(wirePlan);
});
it("keeps a locked job to its selected entry rather than downloading all alternatives", () => {
    const raw = modelChoicesFixture();
    const privacy = raw.jobs.find((job) => job.job === "privacy")!;
    privacy.alternatives.push({
        ...privacy.alternatives[0],
        id: "privacy-alternative",
        downloaded: true,
    });
    const choices = normalizeModelChoices(raw);
    expect(choices.wireChoices.privacy).toBe("privacy");
    expect(choices.models).toHaveLength(fixtureModels(raw).length);
});
it("joins backend progress to manifest names and treats done as silent completion", async () => {
    mocks.choices.mockResolvedValue(modelChoicesFixture());
    mocks.downloads.mockResolvedValue([
        {
            id: "sensevoice",
            state: "done",
            bytesDone: 100,
            bytesTotal: 100,
            error: "",
            pauseReason: "",
        },
    ]);
    expect((await readSetupModels()).downloads).toEqual([
        {
            modelId: "sensevoice",
            name: "SenseVoice",
            state: "downloaded",
            downloadedBytes: 100,
            totalBytes: 100,
            error: "",
            pauseReason: "",
        },
    ]);
});
it("honors enoughSpace even when archive extraction, rather than transfer size, is the limit", async () => {
    const raw = modelChoicesFixture();
    mocks.plan.mockResolvedValue({
        ...modelPlanFixture(raw),
        enoughSpace: false,
        requiredBytes: 10_000_000_000,
        freeBytes: 9_000_000_000,
    });
    const plan = await planSetupDownloads(
        {
            folder: raw.modelsFolder,
            sources: {},
        },
        normalizeModelChoices(raw),
    );
    expect(plan.enoughSpace).toBe(false);
    expect(plan.shortfallBytes).toBe(1_000_000_000);
});

it.each(["paused", "queued", "downloading", "failed", "done"] as const)(
    "preserves native %s state and pause provenance",
    async (state) => {
        mocks.choices.mockResolvedValue(modelChoicesFixture());
        const network = {
            known: true,
            offline: true,
            expensive: false,
            constrained: false,
        };
        mocks.network.mockResolvedValue(network);
        mocks.downloads.mockResolvedValue([
            {
                id: "sensevoice",
                state,
                pauseReason: "Paused while offline",
                bytesDone: 41,
                bytesTotal: 100,
                error: "",
            },
        ]);
        const result = await readSetupModels();
        expect(result.network).toEqual(network);
        expect(result.downloads[0]).toMatchObject({
            state: state === "done" ? "downloaded" : state,
            pauseReason: "Paused while offline",
            downloadedBytes: 41,
        });
        expect(mocks.start).not.toHaveBeenCalled();
    },
);
