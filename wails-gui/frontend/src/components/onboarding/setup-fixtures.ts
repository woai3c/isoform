import catalog from "../../../../models/manifest.json";
import type {
    ModelChoices,
    ModelDownloadPlan,
    ModelEntry,
    ModelJob,
    SetupAgentProbe,
} from "@/lib/setup-backend";
/** Wire fixtures, isolated from the application entry point. */
export const readyProbe: SetupAgentProbe = {
    agents: [
        {
            id: "claude",
            installed: true,
            signedIn: true,
            version: "v2.1.296",
            plan: "Claude Max",
            notFoundYet: false,
            probedAt: "",
        },
        {
            id: "codex",
            installed: true,
            signedIn: true,
            version: "codex-cli 0.163.0",
            plan: "ChatGPT account",
            notFoundYet: false,
            probedAt: "",
        },
        {
            id: "ollama",
            installed: false,
            signedIn: false,
            version: "",
            plan: "",
            notFoundYet: false,
            probedAt: "",
        },
    ],
    npm: true,
    git: { available: true, path: "/usr/bin/git", recoverable: false },
    probedAt: "",
    magpie: {
        kind: "bundled",
        version: "",
        binaryPath: "",
        configDir: "",
        serverUrl: "",
        healthy: true,
        activeGroup: "",
        groups: [],
        agentModels: {},
        installed: true,
        signedIn: true,
        plan: "",
        notFoundYet: false,
        probedAt: "",
    },
};
function model(
    id: string,
    job: ModelJob,
    displayName: string,
    sizeBytes: number,
): ModelEntry {
    return {
        id,
        job,
        displayName,
        sizeBytes,
        line: "",
        evidence: "",
        languages: [],
        files: [{ path: id, sha256: "fixture", bytes: sizeBytes, urls: [] }],
        sources: [
            { kind: "mirror", url: `https://model-assets.yansu.app/${id}` },
            { kind: "huggingface", url: `https://huggingface.co/${id}` },
        ],
        licence: {
            name: "Apache 2.0",
            url: "https://example.com/licence",
            nonCommercial: false,
        },
        downloaded: false,
        recommended: true,
    };
}
export function modelChoicesFixture(shipped = false): ModelChoices {
    if (shipped) {
        const entries = catalog.map((entry) => ({
            ...entry,
            downloaded: false,
        })) as ModelEntry[];
        const jobs = (
            [
                "live",
                "afterCall",
                "voice",
                "privacy",
                "memory",
                "speakerPack",
            ] as const
        ).map((job) => ({
            job,
            chosenId: entries.find(
                (entry) => entry.job === job && entry.recommended,
            )!.id,
            alternatives: entries.filter((entry) => entry.job === job),
            locked: ["privacy", "memory", "speakerPack"].includes(job),
        }));
        return {
            modelsFolder: "/tmp/isoform-fixture/models",
            freeBytes: 76_400_000_000,
            totalBytesToDownload: jobs.reduce(
                (sum, job) =>
                    sum +
                    (job.alternatives.find((model) => model.id === job.chosenId)
                        ?.sizeBytes ?? 0),
                0,
            ),
            jobs,
        };
    }
    const models = [
        model("sensevoice", "live", "SenseVoice", 228_000_000),
        model("speaker", "speakerPack", "Speaker models", 36_000_000),
        model("qwen3", "afterCall", "Qwen3-ASR 1.7B", 2_500_000_000),
        model("kokoro", "voice", "Kokoro", 330_000_000),
        model("privacy", "privacy", "Privacy filter", 665_000_000),
        model("memory", "memory", "Memory search", 90_000_000),
        {
            ...model("parakeet", "live", "Parakeet TDT v2", 600_000_000),
            recommended: false,
        },
    ];
    models.push(
        {
            ...model("whisper", "live", "Whisper large-v3 turbo", 809_000_000),
            recommended: false,
        },
        {
            ...model("mimo", "afterCall", "MiMo-V2.5-ASR", 4_500_000_000),
            recommended: false,
        },
        {
            ...model("none", "afterCall", "None", 0),
            files: [],
            recommended: false,
        },
        {
            ...model("melotts", "voice", "MeloTTS", 159_000_000),
            recommended: false,
        },
    );
    return {
        modelsFolder: "/tmp/isoform-fixture/models",
        totalBytesToDownload: 3_849_000_000,
        freeBytes: 76_400_000_000,
        jobs: (
            [
                "live",
                "afterCall",
                "voice",
                "privacy",
                "memory",
                "speakerPack",
            ] as const
        ).map((job) => ({
            job,
            chosenId: models.find((model) => model.job === job)!.id,
            alternatives: models.filter((model) => model.job === job),
            locked: ["privacy", "memory", "speakerPack"].includes(job),
        })),
    };
}
export function fixtureModels(choices: ModelChoices) {
    return choices.jobs.flatMap((job) => job.alternatives);
}
export function modelPlanFixture(
    choices = modelChoicesFixture(),
): ModelDownloadPlan {
    const items = choices.jobs
        .flatMap((job) =>
            job.alternatives.filter(
                (model) => model.id === job.chosenId && !model.downloaded,
            ),
        )
        .map((model) => ({
            id: model.id,
            bytes: model.sizeBytes ?? 0,
            fileCount: model.files.length,
            source: model.sources[0],
            host: new URL(model.sources[0].url).host,
            licence: model.licence,
            nonCommercial: model.licence.nonCommercial,
        }));
    return {
        planId: "fixture-plan",
        choices: Object.fromEntries(
            choices.jobs.map((job) => [job.job, job.chosenId]),
        ),
        requiredBytes: 4_500_000_000,
        items,
        totalBytes: items.reduce((total, item) => total + item.bytes, 0),
        freeBytes: 76_400_000_000,
        enoughSpace: true,
        modelsFolder: choices.modelsFolder,
    };
}
