import {
    GetModelChoices,
    GetModelDownloads,
    GetModelNetworkState,
    PlanModelDownload,
    SetModelChoice,
    StartModelDownloads,
} from "./wails-shim-app";
import type * as Wire from "./setup-backend";
import type {
    ModelChoices,
    ModelDownload,
    ModelDownloadPlan,
    ModelDownloadRequest,
    ModelJob,
    SetupModel,
} from "./setup-model-types";

const uiJob = (job: Wire.ModelJob): ModelJob =>
    job === "afterCall"
        ? "after-call"
        : job === "speakerPack"
          ? "speaker"
          : job;
const wireJob = (job: string): Wire.ModelJob =>
    (job === "after-call"
        ? "afterCall"
        : job === "speaker"
          ? "speakerPack"
          : job) as Wire.ModelJob;
function model(entry: Wire.ModelEntry): SetupModel {
    return {
        id: entry.id,
        job: uiJob(entry.job),
        name: entry.displayName,
        description: entry.line,
        evidence: entry.evidence,
        sizeBytes: entry.sizeBytes,
        files: entry.files.map((file) => ({
            path: file.path,
            sha256: file.sha256 ?? "",
        })),
        sources: entry.sources.map((source) => ({
            id: source.url,
            name: source.kind,
            host: new URL(source.url).host,
            url: source.url,
        })),
        licence: entry.licence,
        downloaded: entry.downloaded,
        recommended: entry.recommended,
    };
}
export function normalizeModelChoices(raw: Wire.ModelChoices): ModelChoices {
    return {
        models: raw.jobs.flatMap((job) => job.alternatives.map(model)),
        choices: Object.fromEntries(
            raw.jobs.map((job) => [uiJob(job.job), job.chosenId]),
        ) as ModelChoices["choices"],
        modelsFolder: raw.modelsFolder,
        evaluationURL: "",
        wireChoices: Object.fromEntries(
            raw.jobs.map((job) => [job.job, job.chosenId]),
        ),
    };
}
export async function readSetupModels(): Promise<{
    choices: ModelChoices;
    downloads: ModelDownload[];
    network: Wire.ModelNetworkState;
}> {
    const [raw, jobs, network] = await Promise.all([
        GetModelChoices(),
        GetModelDownloads(),
        GetModelNetworkState(),
    ]);
    const choices = normalizeModelChoices(raw);
    return {
        choices,
        network,
        downloads: (jobs ?? []).map((job) => ({
            modelId: job.id,
            name:
                choices.models.find((model) => model.id === job.id)?.name ??
                job.id,
            state: job.state === "done" ? "downloaded" : job.state,
            downloadedBytes: job.bytesDone,
            totalBytes: job.bytesTotal,
            error: job.error,
            pauseReason: job.pauseReason,
            deferred: job.deferred,
        })),
    };
}
export async function planSetupDownloads(
    request: ModelDownloadRequest,
    choices: ModelChoices,
): Promise<ModelDownloadPlan> {
    const wire = await PlanModelDownload(choices.wireChoices, request.sources);
    const items = wire.items.map((item) => {
        const entry = choices.models.find((model) => model.id === item.id);
        if (!entry)
            throw new Error(`Download plan contains unknown model ${item.id}`);
        const source = {
            id: item.source.url,
            name: item.source.kind,
            host: item.host,
            url: item.source.url,
        };
        return {
            ...entry,
            sizeBytes: item.bytes,
            fileCount: item.fileCount,
            licence: item.licence,
            sources: [
                source,
                ...entry.sources.filter(
                    (option) => option.url !== item.source.url,
                ),
            ],
        };
    });
    return {
        wire,
        items,
        totalBytes: wire.totalBytes,
        freeBytes: wire.freeBytes,
        enoughSpace: wire.enoughSpace,
        shortfallBytes: Math.max(0, wire.requiredBytes - wire.freeBytes),
        folder: wire.modelsFolder,
    };
}
export function changeSetupModel(job: string, id: string) {
    return SetModelChoice(wireJob(job), id);
}
/** Return the exact backend-issued plan. Its id and contents authorize only
 * the items the user reviewed; constructing a fresh plan here would lose that. */
export function startSetupDownloads(plan: ModelDownloadPlan) {
    return StartModelDownloads(plan.wire);
}
