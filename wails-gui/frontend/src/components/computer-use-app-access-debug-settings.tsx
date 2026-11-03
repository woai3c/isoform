import { useCallback, useEffect, useMemo, useState } from "react";
import {
    ChevronRight,
    Loader2,
    MonitorCog,
    Play,
    RefreshCw,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "./ui/button";
import { DebugMasterDetail } from "./debug-master-detail";
import {
    GetComputerUseAppAccessSnapshot,
    ProbeComputerUseAppAccess,
    type ComputerUseAppAccessProbe,
    type ComputerUseAppAccessSnapshot,
} from "@/lib/wails-shim-app";

export function ComputerUseAppAccessDebugSettings() {
    const [snapshot, setSnapshot] =
        useState<ComputerUseAppAccessSnapshot | null>(null);
    const [probe, setProbe] = useState<ComputerUseAppAccessProbe | null>(null);
    const [selectedBundle, setSelectedBundle] = useState<string | null>(null);
    const [loading, setLoading] = useState(true);
    const [probing, setProbing] = useState(false);
    const [compactDetailOpen, setCompactDetailOpen] = useState(false);

    const refresh = useCallback(async () => {
        setLoading(true);
        try {
            setSnapshot(await GetComputerUseAppAccessSnapshot());
        } catch (error) {
            toast.error(`Computer Use status failed: ${String(error)}`);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void refresh();
    }, [refresh]);

    const runProbe = useCallback(async () => {
        setProbing(true);
        try {
            const result = await ProbeComputerUseAppAccess();
            setProbe(result);
            setSnapshot(result.snapshot);
            setSelectedBundle((current) =>
                result.browsers.some((browser) => browser.bundle === current)
                    ? current
                    : result.browsers[0]?.bundle ?? null,
            );
        } catch (error) {
            toast.error(`Browser access test failed: ${String(error)}`);
        } finally {
            setProbing(false);
        }
    }, []);

    const selected = useMemo(
        () =>
            probe?.browsers.find(
                (browser) => browser.bundle === selectedBundle,
            ) ?? null,
        [probe?.browsers, selectedBundle],
    );

    return (
        <div className="min-h-0">
            <div className="settings-card--padded border-b border-border">
                <div className="flex items-start justify-between gap-3">
                    <div>
                        <h3 className="text-sm font-semibold text-ink">
                            Can Isoform use signed-in apps through the browser?
                        </h3>
                        <p className="mt-1 text-xs text-muted">
                            Check browser availability and Computer Use
                            permission without connecting OAuth.
                        </p>
                    </div>
                    <div className="flex items-center gap-2">
                        <Button
                            type="button"
                            size="icon"
                            variant="ghost"
                            aria-label="Refresh app access"
                            title="Refresh status"
                            onClick={() => void refresh()}
                            disabled={loading}
                        >
                            {loading ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                                <RefreshCw className="h-3.5 w-3.5" />
                            )}
                        </Button>
                        <Button
                            type="button"
                            size="sm"
                            onClick={() => void runProbe()}
                            disabled={probing}
                        >
                            {probing ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                                <Play className="h-3.5 w-3.5" />
                            )}
                            Test browser access
                        </Button>
                    </div>
                </div>

                {snapshot && (
                    <div className="mt-4 grid grid-cols-1 gap-y-2 border-y border-border py-2 sm:grid-cols-3">
                        {[
                            ["Access method", snapshot.oauthFallback ? "OAuth fallback" : "Computer Use"],
                            ["Accessibility permission", snapshot.accessibility ? "Ready" : "Unavailable"],
                            ["Desktop", snapshot.screenLocked ? "Locked" : "Unlocked"],
                        ].map(([label, value], index) => (
                            <div
                                key={label}
                                className={index === 0 ? "" : "border-l border-border pl-3"}
                            >
                                <div className="text-[9px] text-muted">{label}</div>
                                <div className="mt-0.5 break-all font-mono text-[10px] text-ink">
                                    {value}
                                </div>
                            </div>
                        ))}
                    </div>
                )}

                {snapshot && (
                    <div className="mt-2 flex items-start gap-2 text-[10px] leading-4 text-muted">
                        <MonitorCog className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                        <div className="min-w-0">
                            <p>{snapshot.statusReason}</p>
                            <details className="mt-1">
                                <summary className="cursor-pointer font-medium text-muted hover:text-ink">
                                    Technical details
                                </summary>
                                <p className="mt-1 break-all font-mono text-[9px] text-muted">
                                    Mode {snapshot.mode} · policy {snapshot.policyVersion}
                                    {snapshot.oauthFallback ? " · OAuth fallback enabled" : " · no OAuth fallback"}
                                </p>
                            </details>
                        </div>
                    </div>
                )}
            </div>

            <DebugMasterDetail
                compactDetailOpen={compactDetailOpen}
                detailKey={selectedBundle}
                detailLabel="Browser access details"
                listLabel="Browser access checks"
                onBack={() => setCompactDetailOpen(false)}
                minHeightClassName="min-h-[420px]"
                list={
                    !probe ? (
                        <div className="p-4 text-xs leading-5 text-muted">
                            Select Test browser access to inspect browser
                            sessions available to Computer Use. The test does
                            not navigate or read page content.
                        </div>
                    ) : probe.browsers.length === 0 ? (
                        <div className="p-4 text-xs text-muted">
                            No supported browser was found.
                        </div>
                    ) : (
                        probe.browsers.map((browser) => (
                            <button
                                key={browser.bundle}
                                type="button"
                                aria-current={
                                    selectedBundle === browser.bundle
                                        ? "true"
                                        : undefined
                                }
                                className={`w-full border-b border-border px-3 py-3 text-left hover:bg-ink/[0.03] ${
                                    selectedBundle === browser.bundle
                                        ? "bg-ink/[0.05]"
                                        : ""
                                }`}
                                onClick={() => {
                                    setSelectedBundle(browser.bundle);
                                    setCompactDetailOpen(true);
                                }}
                            >
                                <div className="flex items-center justify-between gap-2">
                                    <span className="text-xs font-medium text-ink">
                                        {browser.name}
                                    </span>
                                    <ChevronRight className="h-3.5 w-3.5 text-muted" />
                                </div>
                                <div className="mt-1 text-[10px] text-muted">
                                    running={String(browser.running)} · pid={browser.pid ?? "-"}
                                </div>
                            </button>
                        ))
                    )
                }
                detail={
                    !selected || !probe ? (
                        <div className="flex min-h-48 items-center justify-center text-xs text-muted">
                            Select a browser probe result.
                        </div>
                    ) : (
                        <div className="space-y-5 text-xs">
                            <section>
                                <h4 className="font-semibold text-ink">
                                    {selected.name}
                                </h4>
                                <div className="mt-1 break-all font-mono text-[10px] text-muted">
                                    {selected.bundle}
                                </div>
                            </section>
                            <dl className="grid grid-cols-2 gap-3 border-y border-border py-3 text-[10px]">
                                <div>
                                    <dt className="text-muted">PID</dt>
                                    <dd className="mt-1 text-ink">{selected.pid ?? "-"}</dd>
                                </div>
                                <div>
                                    <dt className="text-muted">Use count</dt>
                                    <dd className="mt-1 text-ink">{selected.useCount ?? 0}</dd>
                                </div>
                                <div>
                                    <dt className="text-muted">Running</dt>
                                    <dd className="mt-1 text-ink">{String(selected.running)}</dd>
                                </div>
                                <div>
                                    <dt className="text-muted">Active</dt>
                                    <dd className="mt-1 text-ink">{String(selected.active)}</dd>
                                </div>
                            </dl>
                            <section>
                                <h4 className="font-semibold text-ink">Windows</h4>
                                <div className="mt-2 border-t border-border">
                                    {(selected.windowTitles ?? []).length === 0 ? (
                                        <p className="py-2 text-muted">None reported.</p>
                                    ) : (
                                        selected.windowTitles?.map((title, index) => (
                                            <div
                                                key={`${title}-${index}`}
                                                className="border-b border-border py-2 text-[10px] text-muted"
                                            >
                                                {title}
                                            </div>
                                        ))
                                    )}
                                </div>
                            </section>
                            <div className="break-all font-mono text-[9px] text-muted">
                                receipt {probe.receiptHash}
                            </div>
                        </div>
                    )
                }
            />
        </div>
    );
}
