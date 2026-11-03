import type {
    BackgroundUnderstandingRun,
    BackgroundUnderstandingSnapshot,
    BackgroundUnderstandingSource,
} from "@/lib/wails-shim-app";

const backgroundLearningStatusCopy: Record<
    BackgroundUnderstandingSnapshot["status"],
    string
> = {
    unavailable: "Background learning is unavailable right now.",
    paused: "Background learning is paused.",
    waiting: "Isoform is waiting for enough new context to learn from.",
    waiting_for_source:
        "Isoform is waiting for an allowed source to become available.",
    ready: "Isoform is ready to learn from allowed sources.",
    working: "Isoform is learning from allowed sources now.",
    limit_reached:
        "Today's learning checks are complete. Isoform will continue after the daily limit resets.",
};

export function backgroundLearningStatusText(
    status: BackgroundUnderstandingSnapshot["status"],
): string {
    return backgroundLearningStatusCopy[status];
}

export function backgroundLearningUsageText(
    background: Pick<
        BackgroundUnderstandingSnapshot,
        "todayRunCount" | "dailyRunLimit"
    >,
): string {
    const unit = background.dailyRunLimit === 1 ? "check" : "checks";
    return `${background.todayRunCount} of ${background.dailyRunLimit} ${unit} today`;
}

export function backgroundLearningSourceDescription(
    source: BackgroundUnderstandingSource,
): string {
    if (!source.available || !source.connected) {
        return source.source === "gmail"
            ? "Open Gmail in a signed-in browser window, then try again."
            : "Retained activity is unavailable right now.";
    }
    if (source.allowed && source.scopeLabel) return source.scopeLabel;
    if (source.source === "gmail") {
        return "Allow Isoform to learn from a limited Gmail view in your signed-in browser.";
    }
    return "Uses activity already retained on this device.";
}

export function backgroundLearningSourceLastResult(
    source: BackgroundUnderstandingSource,
): string | undefined {
    if (!source.lastRunState) return undefined;
    switch (source.lastRunState) {
        case "selected":
            return "Waiting to start.";
        case "running":
            return "Isoform is learning from this source now.";
        case "completed":
            return source.source === "gmail"
                ? "Isoform reviewed the allowed Gmail view."
                : "Isoform refreshed work patterns from retained activity.";
        case "skipped":
            return "No new learning was needed from this source.";
        case "failed":
            return source.source === "gmail"
                ? "Isoform could not read Gmail. Open it in a signed-in browser window, then try again."
                : "Isoform could not review retained activity. Try again later.";
        case "cancelled":
            return "Learning from this source stopped before it finished.";
        default:
            return undefined;
    }
}

function countLabel(count: number, singular: string, plural: string): string {
    return `${count} ${count === 1 ? singular : plural}`;
}

export function backgroundLearningRunSummary(
    run: BackgroundUnderstandingRun,
): string {
    switch (run.state) {
        case "selected":
            return "This learning check is waiting to start.";
        case "running":
            return "Isoform is learning from this source now.";
        case "completed":
            return `Reviewed ${countLabel(
                run.sourceItemCount,
                "source record",
                "source records",
            )} and added ${countLabel(
                run.materializationCount,
                "learning item",
                "learning items",
            )}.`;
        case "skipped":
            return `Reviewed ${countLabel(
                run.sourceItemCount,
                "source record",
                "source records",
            )}. No new learning item was needed.`;
        case "failed":
            return "Isoform could not finish this learning check.";
        case "cancelled":
            return "This learning check stopped before it finished.";
    }
}

export function backgroundLearningRunStatusLabel(
    state: BackgroundUnderstandingRun["state"],
): string {
    switch (state) {
        case "selected":
            return "Waiting";
        case "running":
            return "Learning";
        case "completed":
            return "Completed";
        case "skipped":
            return "No changes";
        case "failed":
            return "Could not finish";
        case "cancelled":
            return "Stopped";
    }
}

export function backgroundLearningRunTriggerText(
    run: BackgroundUnderstandingRun,
): string {
    return run.triggerKind === "manual"
        ? "You asked Isoform to learn from this source now."
        : "Isoform found a quiet window for allowed background learning.";
}
