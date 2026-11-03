import {
    PlanToolDownload,
    StartToolDownload,
    type ToolDownloadPlan,
} from "./wails-shim-app";

// Wire adapter for the shared tool download sheet. Only the shipped, pinned
// manifest can be planned; caller-supplied URLs are never accepted by the
// backend. The exact returned plan (token, fields and expiry) is the only
// thing that authorizes a Start, so the sheet never reconstructs one.
export function planToolDownload(id: string) {
    return PlanToolDownload(id);
}

export function startToolDownload(plan: ToolDownloadPlan) {
    return StartToolDownload(plan);
}
