import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { RetryModelDownload, ResumeModelDownload } from "@/lib/wails-shim-app";
import { useSetupModels } from "@/hooks/useSetupModels";
import type { ModelDownload } from "@/lib/setup-model-types";
import type { ModelNetworkState } from "@/lib/setup-backend";

export function ModelNetworkNotice({
    network,
}: {
    network: ModelNetworkState | null;
}) {
    const { t } = useTranslation("onboarding");
    if (!network) return null;
    const key = !network.known
        ? "models.networkWaiting"
        : network.offline
          ? "models.networkOffline"
          : network.constrained
            ? "models.networkLowData"
            : network.expensive
              ? "models.networkMetered"
              : null;
    return key ? (
        <p
            role="status"
            className="setup-callout waiting text-xs"
            data-testid="model-network-state"
        >
            {t(key)}
        </p>
    ) : null;
}

function InterruptedDownload({
    job,
    network,
    onRetry,
}: {
    job: ModelDownload;
    network?: ModelNetworkState | null;
    onRetry?: () => void;
}) {
    const { t } = useTranslation("onboarding");
    const [busy, setBusy] = useState(false);
    const [actionFailed, setActionFailed] = useState(false);
    const paused = job.state === "paused";
    const key = !paused
        ? "models.downloadFailed"
        : job.pauseReason === "Paused while offline"
          ? "models.pausedOffline"
          : job.pauseReason === "Waiting for network status"
            ? "models.pausedWaiting"
            : job.pauseReason === "Paused on a metered connection"
              ? network?.constrained
                  ? "models.pausedLowData"
                  : "models.pausedMetered"
              : job.pauseReason === "not enough free disk space"
                ? "models.pausedSpace"
                : "models.paused";
    const retry = async () => {
        if (busy) return;
        setBusy(true);
        setActionFailed(false);
        try {
            await (paused
                ? ResumeModelDownload(job.modelId)
                : RetryModelDownload(job.modelId));
            onRetry?.();
        } catch {
            setActionFailed(true);
        } finally {
            setBusy(false);
        }
    };
    return (
        <div className="flex items-center gap-2">
            <span className={paused ? "text-copper" : "text-error"}>
                {t(
                    actionFailed
                        ? paused
                            ? "models.resumeFailed"
                            : "models.retryFailed"
                        : key,
                    { name: job.name },
                )}
            </span>
            <Button
                variant="quiet"
                size="sm"
                disabled={busy}
                onClick={() => void retry()}
            >
                {t(paused ? "models.resume" : "models.retry")}
            </Button>
        </div>
    );
}

export function DownloadProgressLine({
    downloads,
    network,
    onRetry,
    detailed = false,
}: {
    downloads: ModelDownload[];
    detailed?: boolean;
    network?: ModelNetworkState | null;
    onRetry?: () => void;
}) {
    const { t } = useTranslation("onboarding");
    const failures = downloads.filter((job) => job.state === "failed");
    const paused = downloads.filter((job) => job.state === "paused");
    const interrupted = detailed
        ? [...failures, ...paused]
        : [...failures, ...paused].slice(0, 1);
    // Jobs confirmed with Later wait for power and idle; their rows say so.
    const active = downloads.filter(
        (job) =>
            (job.state === "queued" || job.state === "downloading") &&
            !job.deferred,
    );
    // With several queued, name the one actually downloading and its percent
    // ("Downloading SenseVoice 41%", first run 1.0), not only a count.
    const current = active.find(
        (job) => job.state === "downloading" && job.totalBytes > 0,
    );
    if (!interrupted.length && !active.length) return null;
    return (
        <div
            role="status"
            className="flex flex-col gap-1 text-[12.5px] text-muted"
            data-testid="model-download-progress"
        >
            {interrupted.map((job) => (
                <InterruptedDownload
                    key={`${job.modelId}:${job.state}:${job.pauseReason}`}
                    job={job}
                    network={network}
                    onRetry={onRetry}
                />
            ))}
            {active.length > 0 && (detailed || !interrupted.length) && (
                <span>
                    {active.length === 1
                        ? active[0].state === "queued"
                            ? t("models.queued", { name: active[0].name })
                            : active[0].totalBytes > 0
                              ? t("models.progress", {
                                    name: active[0].name,
                                    percent: Math.floor(
                                        (active[0].downloadedBytes /
                                            active[0].totalBytes) *
                                            100,
                                    ),
                                })
                              : t("models.background", { count: active.length })
                        : current
                          ? t("models.progress", {
                                name: current.name,
                                percent: Math.floor(
                                    (current.downloadedBytes /
                                        current.totalBytes) *
                                        100,
                                ),
                            })
                          : t("models.background", { count: active.length })}
                </span>
            )}
        </div>
    );
}
export function ModelDownloadProgress() {
    const { downloads, network, refresh } = useSetupModels();
    return (
        <DownloadProgressLine
            downloads={downloads}
            network={network}
            onRetry={() => void refresh()}
        />
    );
}
