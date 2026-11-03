import { AlertCircle, CheckCircle2, Loader2, Search, ShieldCheck } from "lucide-react";
import { type FormEvent, useCallback, useMemo, useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import {
    GetMemoryContextDebugSnapshot,
    type MemoryContextDebugItem,
    type MemoryContextDebugSnapshot,
} from "@/lib/wails-shim-app";

function errorMessage(error: unknown): string {
    if (error instanceof Error && error.message.trim()) {
        return error.message;
    }
    return String(error || "Unknown error");
}

function sourceLabel(
    source: string,
    t: TFunction<"settings">,
): string {
    switch (source.toLowerCase()) {
    case "memory":
        return t("debug.memoryContext.sources.memory");
    case "knowledge":
        return t("debug.memoryContext.sources.knowledge");
    default:
        return t("debug.memoryContext.sources.other");
    }
}

function ContextItem({ item }: { item: MemoryContextDebugItem }) {
    const { t } = useTranslation("settings");
    const match = Math.max(0, Math.min(100, Math.round(item.score * 100)));
    const lines = item.startLine > 0
        ? item.endLine > item.startLine
            ? `${item.startLine}-${item.endLine}`
            : String(item.startLine)
        : t("debug.memoryContext.notAvailable");

    return (
        <details className="group border-t border-border py-3 first:border-t-0">
            <summary className="flex cursor-pointer list-none items-start gap-3 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring">
                <span className="mt-0.5 shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] font-medium uppercase text-muted">
                    {sourceLabel(item.source, t)}
                </span>
                <span className="min-w-0 flex-1 text-xs leading-5 text-foreground">
                    {item.preview || t("debug.memoryContext.noPreview")}
                </span>
                <span className="shrink-0 text-[11px] tabular-nums text-muted">
                    {t("debug.memoryContext.match", { percent: match })}
                </span>
            </summary>
            <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 pl-0 text-[11px] text-muted sm:pl-[72px]">
                <dt>{t("debug.memoryContext.path")}</dt>
                <dd className="break-all font-mono text-foreground/80">{item.path}</dd>
                <dt>{t("debug.memoryContext.lines")}</dt>
                <dd className="font-mono text-foreground/80">{lines}</dd>
                <dt>{t("debug.memoryContext.score")}</dt>
                <dd className="font-mono text-foreground/80">{item.score.toFixed(4)}</dd>
            </dl>
        </details>
    );
}

/**
 * Previews which About you entries and reusable know-how Isoform would add to
 * a chat message. `hideHeader` drops the component's own title, description
 * and top divider, for a host (Settings > Learning and memory) that already
 * shows them as a SettingsCard header; the section keeps the title as its
 * accessible name.
 */
export function MemoryContextDebugSettings({ hideHeader = false }: { hideHeader?: boolean } = {}) {
    const { t } = useTranslation("settings");
    const [query, setQuery] = useState("");
    const [snapshot, setSnapshot] = useState<MemoryContextDebugSnapshot | null>(null);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const inspect = useCallback(async (value: string) => {
        const trimmed = value.trim();
        if (!trimmed || loading) return;

        setLoading(true);
        setError(null);
        try {
            const next = await GetMemoryContextDebugSnapshot(trimmed);
            setSnapshot(next);
        } catch (nextError) {
            setSnapshot(null);
            setError(errorMessage(nextError));
        } finally {
            setLoading(false);
        }
    }, [loading]);

    const onSubmit = useCallback((event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        void inspect(query);
    }, [inspect, query]);

    const leftOut = useMemo(() => {
        if (!snapshot) return 0;
        const counts = snapshot.counts;
        return counts.correctedExcluded + counts.belowRelevance + counts.empty +
            counts.budgetOmitted;
    }, [snapshot]);

    return (
        <section
            className={hideHeader ? undefined : "mt-5 border-t border-border pt-5"}
            aria-labelledby={hideHeader ? undefined : "memory-context-debug-title"}
            aria-label={hideHeader ? t("debug.memoryContext.title") : undefined}
        >
            {!hideHeader && (
                <div className="flex items-start justify-between gap-4">
                    <div>
                        <h3 id="memory-context-debug-title" className="text-sm font-medium text-foreground">
                            {t("debug.memoryContext.title")}
                        </h3>
                        <p className="mt-1 max-w-2xl text-xs leading-5 text-muted">
                            {t("debug.memoryContext.description")}
                        </p>
                    </div>
                    <div className="flex shrink-0 items-center gap-1.5 text-[11px] text-muted">
                        <ShieldCheck className="h-3.5 w-3.5" />
                        {t("debug.memoryContext.previewOnly")}
                    </div>
                </div>
            )}

            <form className={hideHeader ? undefined : "mt-4"} onSubmit={onSubmit}>
                <label htmlFor="memory-context-debug-query" className="text-xs font-medium text-foreground">
                    {t("debug.memoryContext.queryLabel")}
                </label>
                <p className="mt-1 text-[11px] text-muted">
                    {t("debug.memoryContext.queryHelp")}
                </p>
                <Textarea
                    id="memory-context-debug-query"
                    className="mt-2 min-h-24 resize-y text-xs"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder={t("debug.memoryContext.queryPlaceholder")}
                    disabled={loading}
                />
                <div className="mt-3 flex items-center gap-3">
                    <Button type="submit" size="sm" disabled={!query.trim() || loading}>
                        {loading ? (
                            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                        ) : (
                            <Search className="mr-1.5 h-3.5 w-3.5" />
                        )}
                        {loading
                            ? t("debug.memoryContext.inspecting")
                            : t("debug.memoryContext.inspect")}
                    </Button>
                    <span className="text-[11px] text-muted">
                        {t("debug.memoryContext.readOnlyHelp")}
                    </span>
                </div>
            </form>

            {error && (
                <div role="alert" className="mt-4 flex items-start gap-2 border-l-2 border-error pl-3 text-xs text-error">
                    <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <div>
                        <div className="font-medium">{t("debug.memoryContext.loadFailed")}</div>
                        <div className="mt-0.5 break-words text-[11px]">{error}</div>
                    </div>
                </div>
            )}

            {!snapshot && !error && !loading && (
                <p className="mt-5 border-t border-dashed border-border pt-4 text-xs text-muted">
                    {t("debug.memoryContext.initial")}
                </p>
            )}

            {snapshot && (
                <div className="mt-5 border-t border-border pt-4">
                    <div className="grid grid-cols-2 gap-x-5 gap-y-4 md:grid-cols-4">
                        <div>
                            <div className="text-[10px] uppercase text-muted">
                                {t("debug.memoryContext.metrics.considered")}
                            </div>
                            <div className="mt-1 text-lg font-medium tabular-nums text-foreground">
                                {snapshot.counts.discovered.toLocaleString()}
                            </div>
                        </div>
                        <div>
                            <div className="text-[10px] uppercase text-muted">
                                {t("debug.memoryContext.metrics.added")}
                            </div>
                            <div className="mt-1 text-lg font-medium tabular-nums text-foreground">
                                {snapshot.counts.selected.toLocaleString()}
                            </div>
                        </div>
                        <div>
                            <div className="text-[10px] uppercase text-muted">
                                {t("debug.memoryContext.metrics.spaceUsed")}
                            </div>
                            <div className="mt-1 text-sm font-medium tabular-nums text-foreground">
                                {t("debug.memoryContext.metrics.characters", {
                                    used: snapshot.counts.selectedChars.toLocaleString(),
                                    total: snapshot.characterBudget.toLocaleString(),
                                })}
                            </div>
                        </div>
                        <div>
                            <div className="text-[10px] uppercase text-muted">
                                {t("debug.memoryContext.metrics.leftOut")}
                            </div>
                            <div className="mt-1 text-lg font-medium tabular-nums text-foreground">
                                {leftOut.toLocaleString()}
                            </div>
                        </div>
                    </div>

                    {!snapshot.countsReconcile && (
                        <div role="alert" className="mt-4 flex items-center gap-2 text-xs text-error">
                            <AlertCircle className="h-3.5 w-3.5" />
                            {t("debug.memoryContext.countMismatch")}
                        </div>
                    )}

                    <details className="mt-4 border-y border-border py-3 text-xs">
                        <summary className="cursor-pointer font-medium text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">
                            {t("debug.memoryContext.omissionsTitle")}
                        </summary>
                        <div className="mt-3 grid grid-cols-2 gap-x-6 gap-y-2 text-[11px] sm:grid-cols-4">
                            <div className="flex justify-between gap-2">
                                <span className="text-muted">{t("debug.memoryContext.omissions.corrected")}</span>
                                <span className="tabular-nums">{snapshot.counts.correctedExcluded.toLocaleString()}</span>
                            </div>
                            <div className="flex justify-between gap-2">
                                <span className="text-muted">{t("debug.memoryContext.omissions.notRelevant")}</span>
                                <span className="tabular-nums">{snapshot.counts.belowRelevance.toLocaleString()}</span>
                            </div>
                            <div className="flex justify-between gap-2">
                                <span className="text-muted">{t("debug.memoryContext.omissions.empty")}</span>
                                <span className="tabular-nums">{snapshot.counts.empty.toLocaleString()}</span>
                            </div>
                            <div className="flex justify-between gap-2">
                                <span className="text-muted">{t("debug.memoryContext.omissions.noSpace")}</span>
                                <span className="tabular-nums">{snapshot.counts.budgetOmitted.toLocaleString()}</span>
                            </div>
                        </div>
                        <p className="mt-3 text-[11px] text-muted">
                            {t("debug.memoryContext.technicalReceipt", {
                                duration: snapshot.durationMs.toLocaleString(),
                                threshold: snapshot.minScore.toFixed(2),
                                memory: snapshot.counts.selectedMemory.toLocaleString(),
                                knowledge: snapshot.counts.selectedKnowledge.toLocaleString(),
                                other: snapshot.counts.selectedOther.toLocaleString(),
                                truncated: snapshot.counts.truncated.toLocaleString(),
                                hidden: snapshot.counts.detailItemsOmitted.toLocaleString(),
                            })}
                        </p>
                    </details>

                    <div className="mt-4 flex items-center justify-between gap-4">
                        <div>
                            <h4 className="text-xs font-medium text-foreground">
                                {t("debug.memoryContext.selectedTitle")}
                            </h4>
                            <p className="mt-1 text-[11px] text-muted">
                                {t("debug.memoryContext.selectedDescription")}
                            </p>
                        </div>
                        {snapshot.readOnly && (
                            <CheckCircle2 className="h-4 w-4 shrink-0 text-accent-2" aria-label={t("debug.memoryContext.readOnlyConfirmed")} />
                        )}
                    </div>

                    {snapshot.selected.length === 0 ? (
                        <p className="mt-4 border-t border-dashed border-border pt-4 text-xs text-muted">
                            {t("debug.memoryContext.noneSelected")}
                        </p>
                    ) : (
                        <div className="mt-3">
                            {snapshot.selected.map((item, index) => (
                                <ContextItem
                                    key={`${item.source}:${item.path}:${item.startLine}:${index}`}
                                    item={item}
                                />
                            ))}
                        </div>
                    )}
                </div>
            )}
        </section>
    );
}
