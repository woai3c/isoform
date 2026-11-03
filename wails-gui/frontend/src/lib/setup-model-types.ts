// The setup read model is supplied by the desktop manifest. No model sizes,
// evaluation numbers, sources or download totals are duplicated in the UI.
export type ModelJob =
    | "live"
    | "after-call"
    | "voice"
    | "privacy"
    | "memory"
    | "speaker";
export interface SetupModelSource {
    id: string;
    name: string;
    host: string;
    url: string;
}
export interface SetupModel {
    id: string;
    job: ModelJob;
    name: string;
    description: string;
    evidence: string;
    sizeBytes: number | null;
    fileCount?: number;
    files: { path: string; sha256: string }[];
    sources: SetupModelSource[];
    licence: { name: string; url: string; nonCommercial: boolean };
    downloaded: boolean;
    recommended: boolean;
}
export interface ModelChoices {
    models: SetupModel[];
    choices: Record<"live" | "after-call" | "voice", string>;
    modelsFolder: string;
    wireChoices: Partial<Record<import("./setup-backend").ModelJob, string>>;
    evaluationURL: string;
}
export interface ModelDownloadRequest {
    folder: string;
    sources: Record<string, string>;
}
export interface ModelDownloadPlan {
    items: SetupModel[];
    totalBytes: number;
    freeBytes: number;
    shortfallBytes: number;
    folder: string;
    enoughSpace: boolean;
    wire: import("./setup-backend").ModelDownloadPlan;
}
export interface ModelDownload {
    modelId: string;
    name: string;
    state: "queued" | "downloading" | "paused" | "downloaded" | "failed";
    downloadedBytes: number;
    totalBytes: number;
    error?: string;
    pauseReason?: string;
    deferred?: boolean;
}
