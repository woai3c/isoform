import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Check, ChevronDown, Package } from "lucide-react";
import { toast } from "sonner";
import { SearchIcon } from "@/assets/icons/SearchIcon";
// New bindings import from the shim directly: it's the real module Vite aliases
// to, and the generated wailsjs types only refresh on a full wails build.
import {
    ListDeliverables,
    OpenDeliverableFile,
    RevealDeliverableFile,
    OpenDeliverableFileWithApp,
    ListDeliverableFileOpenWithApps,
    DeliverableFileInfo,
    type DeliverableEntry,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import {
    RichDeliverableCard,
    type DeliverableActions,
} from "@/components/stream-card";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

type SourceFilter = "all" | "chat" | "automation" | "handoff";
type StatusFilter = "all" | "in_progress" | "completed" | "failed";
type ConcreteStatusFilter = Exclude<StatusFilter, "all">;

// Maps each raw status to one of the three coarse buckets the status filter
// exposes, and (via STATUS_DOT) to a dot color. Keeping classification here lets
// the filter and the badge agree.
const STATUS_BUCKET: Record<string, ConcreteStatusFilter> = {
    in_progress: "in_progress",
    active: "in_progress",
    running: "in_progress",
    pending: "in_progress",
    completed: "completed",
    ok: "completed",
    skipped: "completed",
    failed: "failed",
    error: "failed",
    aborted: "failed",
    stale: "failed",
};

const STATUS_DOT: Record<StatusFilter, string> = {
    all: "var(--color-muted)",
    in_progress: "var(--color-accent)",
    completed: "var(--color-success)",
    failed: "var(--color-error)",
};

function statusBucket(status: string): ConcreteStatusFilter {
    return STATUS_BUCKET[status] ?? "completed";
}

function statusLabel(status: string, t: TFunction<"app">): string {
    switch (statusBucket(status)) {
        case "in_progress":
            return t("deliverablesPage.status.in_progress");
        case "failed":
            return t("deliverablesPage.status.failed");
        case "completed":
            return t("deliverablesPage.status.completed");
    }
}

function StatusBadge({ status, label }: { status: string; label: string }) {
    const bucket = statusBucket(status);
    return (
        <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-ink/[0.05] px-2 py-0.5 text-[11px] font-medium text-ink/70">
            <span
                className="h-1.5 w-1.5 rounded-full"
                style={{ backgroundColor: STATUS_DOT[bucket] }}
            />
            {label}
        </span>
    );
}

/** Formats a modified timestamp: today / yesterday for recent items, an
 *  absolute "Mon D" for older ones. */
function formatModified(ts: number, now: number, locale: string): string {
    if (!ts) return "";
    const startOfDay = (t: number) => {
        const d = new Date(t);
        d.setHours(0, 0, 0, 0);
        return d.getTime();
    };
    const dayMs = 24 * 60 * 60 * 1000;
    const todayStart = startOfDay(now);
    if (ts >= todayStart) return "today";
    if (ts >= todayStart - dayMs) return "yesterday";
    return new Date(ts).toLocaleDateString(locale, {
        month: "short",
        day: "numeric",
    });
}

/** One deliverable card: header (source + status + time), summary, and the
 *  Cola-style file list. actions are memoized per source/sourceId so the inner
 *  RichDeliverableCard doesn't re-fetch file info on every parent render. */
function DeliverableCard({ entry }: { entry: DeliverableEntry }) {
    const { t, i18n } = useTranslation("app");

    const actions = useMemo<DeliverableActions>(
        () => ({
            open: (path) => {
                OpenDeliverableFile(entry.source, entry.sourceId, path).catch(
                    (err) =>
                        toast.error(
                            t("deliverablesPage.openFailed", {
                                error: String(err),
                            }),
                        ),
                );
            },
            reveal: (path) => {
                RevealDeliverableFile(entry.source, entry.sourceId, path).catch(
                    (err) =>
                        toast.error(
                            t("deliverablesPage.openFailed", {
                                error: String(err),
                            }),
                        ),
                );
            },
            openWith: (path, appPath) => {
                OpenDeliverableFileWithApp(
                    entry.source,
                    entry.sourceId,
                    path,
                    appPath,
                ).catch((err) =>
                    toast.error(
                        t("deliverablesPage.openFailed", {
                            error: String(err),
                        }),
                    ),
                );
            },
            listOpenWith: (path) =>
                ListDeliverableFileOpenWithApps(
                    entry.source,
                    entry.sourceId,
                    path,
                ),
            getInfo: (path) =>
                DeliverableFileInfo(entry.source, entry.sourceId, path),
        }),
        [entry.source, entry.sourceId, t],
    );

    const sourceLabel = t(`deliverablesPage.source.${entry.source}`, {
        defaultValue: entry.source,
    });
    const statusText = statusLabel(entry.status, t);
    const modified = formatModified(entry.updatedAt, Date.now(), i18n.language);
    const modifiedLabel =
        modified === "today"
            ? t("deliverablesPage.today")
            : modified === "yesterday"
              ? t("deliverablesPage.yesterday")
              : modified;

    return (
        <div className="rounded-2xl border border-border-input bg-panel p-5">
            <div className="flex items-start gap-3">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-ink/[0.04] text-muted">
                    <Package className="size-[18px]" />
                </div>
                <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                        <h3
                            className="min-w-0 truncate text-[15px] font-medium text-ink"
                            title={entry.title}
                        >
                            {entry.title || t("deliverablesPage.untitled")}
                        </h3>
                        <StatusBadge status={entry.status} label={statusText} />
                    </div>
                    <div className="mt-1 flex items-center gap-2 text-[12px] text-muted">
                        <span className="inline-flex items-center rounded-full bg-ink/[0.04] px-2 py-0.5 font-medium">
                            {sourceLabel}
                        </span>
                        {modifiedLabel && <span>{modifiedLabel}</span>}
                    </div>
                </div>
            </div>

            {entry.summary && (
                <p className="mt-3 line-clamp-2 text-[13px] leading-relaxed text-muted">
                    {entry.summary}
                </p>
            )}

            {entry.files.length > 0 && (
                <div className="mt-4 flex flex-col gap-2">
                    {entry.files.map((file) => (
                        <RichDeliverableCard
                            key={file.path}
                            path={file.path}
                            description={file.description}
                            actions={actions}
                            unavailableMode={
                                statusBucket(entry.status) === "in_progress"
                                    ? "pending"
                                    : "invalid"
                            }
                        />
                    ))}
                </div>
            )}
        </div>
    );
}

/**
 * Unified Deliverables hub. Aggregates chat / automation / handoff
 * outputs (ListDeliverables, a read-time projection) into one status-sorted
 * list with search plus source and status filters. Self-updates on the
 * `deliverables:updated` event emitted when a chat deliverable is recorded or
 * removed. File actions route back to the owning source for path validation.
 */
export function DeliverablesPage({ wideMode }: { wideMode?: boolean }) {
    const { t } = useTranslation("app");
    const [entries, setEntries] = useState<DeliverableEntry[]>([]);
    const [hasLoaded, setHasLoaded] = useState(false);
    const [searchQuery, setSearchQuery] = useState("");
    const [sourceFilter, setSourceFilter] = useState<SourceFilter>("all");
    const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
    useEffect(() => {
        // Generation guard: ListDeliverables scans can vary in cost (the
        // automation source re-parses stream logs), so a slow earlier request
        // can resolve after a faster later one. Tag each request and drop any
        // response that is no longer the latest, so stale data never overwrites
        // fresh state.
        let latestRequest = 0;
        const reload = () => {
            const reqId = ++latestRequest;
            ListDeliverables()
                .then((rows) => {
                    if (reqId !== latestRequest) return;
                    setEntries(rows ?? []);
                    setHasLoaded(true);
                })
                .catch(() => {
                    if (reqId !== latestRequest) return;
                    setEntries([]);
                    setHasLoaded(true);
                });
        };
        // ListDeliverables is a read-time projection that re-scans cron-run
        // stream logs, so it is not free. The triggers below (especially
        // cron:state and window focus) can fire in bursts, so coalesce them
        // into a single trailing-edge reload instead of one scan per event.
        let debounceTimer: ReturnType<typeof setTimeout> | null = null;
        const scheduleReload = () => {
            if (debounceTimer) clearTimeout(debounceTimer);
            debounceTimer = setTimeout(reload, 300);
        };
        reload(); // initial load is immediate; only event-driven reloads debounce
        const unsubs = [
            EventsOn("deliverables:updated", scheduleReload),
            EventsOn("handoff:updated", scheduleReload),
            EventsOn("cron:state", scheduleReload),
        ];
        const handleFocus = () => scheduleReload();
        const handleVisibility = () => {
            if (document.visibilityState === "visible") scheduleReload();
        };
        window.addEventListener("focus", handleFocus);
        document.addEventListener("visibilitychange", handleVisibility);
        return () => {
            if (debounceTimer) clearTimeout(debounceTimer);
            unsubs.forEach((unsub) => unsub?.());
            window.removeEventListener("focus", handleFocus);
            document.removeEventListener("visibilitychange", handleVisibility);
        };
    }, []);

    const sourceLabels: Record<SourceFilter, string> = {
        all: t("deliverablesPage.filterAll"),
        chat: t("deliverablesPage.source.chat"),
        automation: t("deliverablesPage.source.automation"),
        handoff: t("deliverablesPage.source.handoff"),
    };
    const statusLabels: Record<StatusFilter, string> = {
        all: t("deliverablesPage.filterAll"),
        in_progress: t("deliverablesPage.status.in_progress"),
        completed: t("deliverablesPage.status.completed"),
        failed: t("deliverablesPage.status.failed"),
    };

    const visible = useMemo(() => {
        let rows = entries ?? [];
        if (sourceFilter !== "all") {
            rows = rows.filter((e) => e.source === sourceFilter);
        }
        if (statusFilter !== "all") {
            rows = rows.filter((e) => statusBucket(e.status) === statusFilter);
        }
        if (searchQuery.trim()) {
            const q = searchQuery.toLowerCase().trim();
            rows = rows.filter(
                (e) =>
                    e.title.toLowerCase().includes(q) ||
                    e.files.some(
                        (f) =>
                            f.name.toLowerCase().includes(q) ||
                            f.description?.toLowerCase().includes(q),
                    ),
            );
        }
        return rows;
    }, [entries, searchQuery, sourceFilter, statusFilter]);

    const isFiltered =
        searchQuery.trim() !== "" ||
        sourceFilter !== "all" ||
        statusFilter !== "all";

    return (
        <div className="flex flex-1 min-h-0 flex-col">
            <div className="flex-1 overflow-y-auto">
                {/* Empty space under the title strip drags the window. */}
                <div aria-hidden className="window-drag h-12" />
                <div className={`animate-rise-in mx-auto w-full px-8 pb-16 motion-reduce:animate-none${wideMode ? '' : ' max-w-[860px]'}`}>
                    <h1 className="window-drag text-[36px] font-medium text-ink mb-[38px]">
                        {t("deliverablesPage.title")}
                    </h1>

                    {/* Search + filters on one row */}
                    <div className="flex items-center gap-3 mb-5">
                        <div className="relative flex-1 min-w-0">
                            <SearchIcon className="absolute left-5 top-1/2 -translate-y-1/2 size-[18px] text-muted" />
                            <input
                                type="text"
                                placeholder={t(
                                    "deliverablesPage.searchPlaceholder",
                                )}
                                value={searchQuery}
                                onChange={(e) => setSearchQuery(e.target.value)}
                                className="w-full h-[59px] pl-12 pr-5 rounded-[26px] border border-border-input bg-panel text-[14px] text-ink placeholder:text-muted outline-none focus:border-ink/20 focus:ring-1 focus:ring-ink/20 transition-colors"
                            />
                        </div>
                        <FilterDropdown
                            label={t("deliverablesPage.filterType")}
                            value={sourceFilter}
                            labels={sourceLabels}
                            onSelect={setSourceFilter}
                        />
                        <FilterDropdown
                            label={t("deliverablesPage.filterStatus")}
                            value={statusFilter}
                            labels={statusLabels}
                            onSelect={setStatusFilter}
                        />
                    </div>

                    {/* List */}
                    {visible.length === 0 ? (
                        <div className="flex min-h-[240px] flex-col items-center justify-center gap-1.5 text-center">
                            <p className="text-[13px] font-semibold text-ink/80">
                                {isFiltered
                                    ? t("deliverablesPage.emptySearch")
                                    : t("deliverablesPage.empty")}
                            </p>
                            {!isFiltered && (
                                <p className="text-[12px] text-muted max-w-[280px]">
                                    {t("deliverablesPage.emptyHint")}
                                </p>
                            )}
                        </div>
                    ) : (
                        <div className="flex flex-col gap-3">
                            {visible.map((entry) => (
                                <DeliverableCard key={entry.id} entry={entry} />
                            ))}
                        </div>
                    )}

                    {!hasLoaded && null}
                </div>
            </div>

        </div>
    );
}

/** Generic labeled filter dropdown shared by the type and status filters. */
function FilterDropdown<T extends string>({
    label,
    value,
    labels,
    onSelect,
}: {
    label: string;
    value: T;
    labels: Record<T, string>;
    onSelect: (value: T) => void;
}) {
    return (
        <div className="flex shrink-0 items-center gap-2">
            <span className="text-[14px] font-normal text-muted">{label}</span>
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <button className="flex items-center gap-1.5 rounded-[14px] border border-border-input bg-panel px-3 py-1.5 text-[14px] font-normal text-muted transition-colors hover:border-ink/20">
                        {labels[value]}
                        <ChevronDown className="size-3.5 text-muted" />
                    </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="min-w-[150px]">
                    {(Object.keys(labels) as T[]).map((key) => (
                        <DropdownMenuItem
                            key={key}
                            className="flex items-center justify-between text-[13px]"
                            onClick={() => onSelect(key)}
                        >
                            <span
                                className={
                                    value === key
                                        ? "text-ink font-medium"
                                        : "text-muted"
                                }
                            >
                                {labels[key]}
                            </span>
                            {value === key && (
                                <Check className="size-3.5 text-ink" />
                            )}
                        </DropdownMenuItem>
                    ))}
                </DropdownMenuContent>
            </DropdownMenu>
        </div>
    );
}
