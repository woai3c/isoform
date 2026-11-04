import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronLeft, Download, FileCode2, ListChecks, Loader2, MoreHorizontal, Trash2, Upload } from "lucide-react";
import { ActivityInlineNotice, errorNotice, type ActivityNotice } from "@/components/activity-inline-notice";
import {
    AcceptPlaybookSuggestion,
    DeclinePlaybookSuggestion,
    DeleteSOP,
    GetSOPScript,
    RunPlaybookNow,
    RunSOP,
    SaveSOP,
    type PlaybookChannels,
    type PlaybookView,
    type SopEntry,
    type SopRunResult,
} from "@/lib/wails-shim-app";
import { Button } from "@/components/ui/button";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { MarkdownContent } from "@/components/markdown-content";
import { PlaybookSuggestionCard } from "@/components/sops/PlaybookSuggestionCard";
import { PlaybookDeliveryPanel, isSitesChanged, isStepsUnapproved } from "@/components/sops/PlaybookDeliveryPanel";
import { usePlaybookTransfer } from "@/components/sops/playbook-io";
import { cn } from "@/lib/utils";

export interface PlaybookReaderProps {
    entry: SopEntry;
    plan: PlaybookView | null;
    channels: PlaybookChannels | null;
    defaultChannels: string[];
    /** Fills the page column instead of capping the reading width. */
    wide?: boolean;
    /** Shows the title-bar drag strip, for a page that sits at the top of the window. */
    dragStrip?: boolean;
    /** Shows a Back button. Omit it where the app header already goes back. */
    onBack?: () => void;
    /** Closes the reader. Called before a chat run starts, and after a delete. */
    onClose: () => void;
    /** Re-reads the playbooks and plans after an answer, a run or a schedule change. */
    onChanged: () => void;
    /** Starts a run in chat. Omit it and the Run in chat action is hidden. */
    onStartSop?: (result: SopRunResult) => Promise<void>;
    /** Opens another playbook here, as Import does with the one it brought in. */
    onOpenPlaybook?: (id: string) => void;
    /** The schedules didn't load: the run panel says so instead of showing the switch off (W21). */
    schedulesFailed?: boolean;
}

const STATUS_DOT: Record<string, string> = {
    draft: "var(--color-muted)",
    confirmed: "var(--color-success)",
};

function StatusBadge({ status, label }: { status: string; label: string }) {
    return (
        <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-ink/[0.05] px-2 py-0.5 text-[11px] font-medium text-ink/70">
            <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: STATUS_DOT[status] ?? "var(--color-muted)" }} />
            {label}
        </span>
    );
}

const INPUT_CLASS =
    "h-10 rounded-lg border border-border-input bg-panel px-3 text-[14px] font-normal text-ink outline-none focus:border-ink/20 focus:ring-1 focus:ring-ink/20 transition-colors";

/**
 * One playbook's detail: its steps, scripts, version and schedule, with Edit,
 * Run in chat, Run now, and an overflow menu for Export, Import and Delete.
 * Shared by the Library playbooks list and How you do things.
 */
export function PlaybookReader({
    entry,
    plan,
    channels,
    defaultChannels,
    wide,
    dragStrip,
    onBack,
    onClose,
    onChanged,
    onStartSop,
    onOpenPlaybook,
    schedulesFailed = false,
}: PlaybookReaderProps) {
    const { t } = useTranslation("knowledge");
    // Every outcome here is in place, never a toast (W10).
    const [notice, setNotice] = useState<ActivityNotice | null>(null);
    const [runStarted, setRunStarted] = useState(false);
    const { exportPlaybook, importPlaybook } = usePlaybookTransfer(setNotice, onOpenPlaybook);
    const [editing, setEditing] = useState(false);
    const [editTitle, setEditTitle] = useState("");
    const [editDescription, setEditDescription] = useState("");
    const [editTrigger, setEditTrigger] = useState("");
    const [editBody, setEditBody] = useState("");
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [running, setRunning] = useState(false);
    const [answering, setAnswering] = useState(false);
    const [scriptContents, setScriptContents] = useState<Record<string, string>>({});

    // Load the helper scripts' contents when the playbook bundles some: they are
    // shown in full so the user can see exactly what a run would execute.
    const scriptsKey = entry.scripts?.join("\n") ?? "";
    useEffect(() => {
        setNotice(null);
        setRunStarted(false);
    }, [entry.id]);
    useEffect(() => {
        setScriptContents({});
        if (!scriptsKey) return;
        let cancelled = false;
        const paths = scriptsKey.split("\n");
        Promise.all(
            paths.map((rel) =>
                GetSOPScript(entry.id, rel)
                    .then((content) => [rel, content] as const)
                    .catch(() => [rel, ""] as const),
            ),
        ).then((pairs) => {
            if (!cancelled) setScriptContents(Object.fromEntries(pairs));
        });
        return () => {
            cancelled = true;
        };
    }, [entry.id, scriptsKey]);

    const startEditing = useCallback(() => {
        setEditTitle(entry.title);
        setEditDescription(entry.description);
        setEditTrigger(entry.trigger);
        setEditBody(entry.body);
        setEditing(true);
    }, [entry]);

    const saveEdit = () => {
        SaveSOP(entry.id, editTitle, editDescription, editTrigger, editBody)
            .then(() => {
                setNotice(null);
                setEditing(false);
            })
            .catch(() => setNotice(errorNotice(t("playbooks.saveFailed"), saveEdit)));
    };

    const deleteEntry = () => {
        setConfirmDelete(false);
        DeleteSOP(entry.id)
            .then(() => onClose())
            .catch(() => setNotice(errorNotice(t("playbooks.deleteFailed"), deleteEntry)));
    };

    const runInChat = () => {
        if (running || !onStartSop) return;
        setRunning(true);
        RunSOP(entry.id)
            .then(async (result) => {
                onClose();
                await onStartSop(result);
            })
            .catch(() => setNotice(errorNotice(t("playbooks.runFailed"), runInChat)))
            .finally(() => setRunning(false));
    };

    const runNow = () => {
        if (running) return;
        setRunning(true);
        RunPlaybookNow(entry.id, entry.sites ?? [])
            .then(() => {
                setNotice(null);
                setRunStarted(true);
                onChanged();
            })
            .catch((err) => {
                setNotice(errorNotice(
                    isSitesChanged(err)
                        ? t("playbooks.sitesChanged")
                        : isStepsUnapproved(err)
                          ? t("playbooks.heldUnapproved")
                          : t("playbooks.runNowFailed"),
                    runNow,
                ));
            })
            .finally(() => setRunning(false));
    };

    const acceptSuggestion = (view: PlaybookView) => {
        if (answering) return;
        setAnswering(true);
        AcceptPlaybookSuggestion(entry.id)
            .then((result) => {
                setNotice({
                    tone: "done",
                    text: t("playbooks.suggestion.accepted", {
                        site: view.proposal?.siteName ?? entry.title,
                        runTime: result.schedule?.time ?? view.suggestedTime,
                    }),
                });
                onChanged();
            })
            .catch((err) => {
                setNotice(errorNotice(
                    isSitesChanged(err) ? t("playbooks.sitesChanged") : t("playbooks.saveFailed"),
                    () => acceptSuggestion(view),
                ));
            })
            .finally(() => setAnswering(false));
    };

    const declineSuggestion = (view: PlaybookView) => {
        if (answering) return;
        setAnswering(true);
        DeclinePlaybookSuggestion(entry.id)
            .then(() => {
                setNotice({ tone: "done", text: t("playbooks.suggestion.declined", { site: view.proposal?.siteName ?? entry.title }) });
                onChanged();
            })
            .catch(() => setNotice(errorNotice(t("playbooks.saveFailed"), () => declineSuggestion(view))))
            .finally(() => setAnswering(false));
    };

    const statusLabel = (status: string): string => {
        switch (status) {
            case "draft":
                return t("playbooks.statusDraft");
            case "confirmed":
                return t("playbooks.statusConfirmed");
            default:
                return status;
        }
    };
    const sourceLabel = (source: string): string => {
        switch (source) {
            case "triage":
                return t("playbooks.sourceTriage");
            case "manual":
                return t("playbooks.sourceManual");
            case "imported":
                return t("playbooks.sourceImported");
            case "observed":
                return t("playbooks.sourceObserved");
            default:
                return source;
        }
    };

    const pendingOffer = !!plan?.proposal && !plan.proposal.answer;

    return (
        <div key={`reader-${entry.id}`} className="flex flex-1 min-h-0 flex-col">
            <div className="flex-1 overflow-y-auto">
                {/* Empty space under the title strip drags the window. */}
                {dragStrip && <div aria-hidden className="window-drag h-10" />}
                <div className={cn("animate-rise-in mx-auto w-full px-8 pb-16 motion-reduce:animate-none", !wide && "max-w-[760px]")}>
                    {onBack && (
                        <Button variant="quiet" size="sm" className="-ml-2 mb-4" onClick={onBack}>
                            <ChevronLeft />
                            {t("playbooks.back")}
                        </Button>
                    )}
                    {editing ? (
                        <div className="flex flex-col gap-4">
                            <label className="flex flex-col gap-1.5 text-[12px] font-medium text-muted">
                                {t("playbooks.editTitleLabel")}
                                <input type="text" value={editTitle} onChange={(e) => setEditTitle(e.target.value)} className={INPUT_CLASS} />
                            </label>
                            <label className="flex flex-col gap-1.5 text-[12px] font-medium text-muted">
                                {t("playbooks.editDescriptionLabel")}
                                <input type="text" value={editDescription} onChange={(e) => setEditDescription(e.target.value)} className={INPUT_CLASS} />
                            </label>
                            <label className="flex flex-col gap-1.5 text-[12px] font-medium text-muted">
                                {t("playbooks.editTriggerLabel")}
                                <input type="text" value={editTrigger} onChange={(e) => setEditTrigger(e.target.value)} className={INPUT_CLASS} />
                            </label>
                            <label className="flex flex-col gap-1.5 text-[12px] font-medium text-muted">
                                {t("playbooks.editBodyLabel")}
                                <textarea
                                    value={editBody}
                                    onChange={(e) => setEditBody(e.target.value)}
                                    rows={22}
                                    className="rounded-lg border border-border-input bg-panel px-3 py-2.5 font-mono text-[13px] leading-relaxed text-ink outline-none focus:border-ink/20 focus:ring-1 focus:ring-ink/20 transition-colors resize-y"
                                />
                            </label>
                        </div>
                    ) : (
                        <>
                            <div className="window-drag mb-2 flex items-start gap-2.5">
                                <ListChecks size={22} className="mt-0.5 shrink-0 text-accent" />
                                <h1 className="flex-1 text-[26px] font-medium leading-tight text-ink">{entry.title}</h1>
                            </div>
                            <div className="mb-5 flex flex-wrap items-center gap-2">
                                <StatusBadge status={entry.status} label={statusLabel(entry.status)} />
                                <span className="inline-flex items-center rounded-full bg-ink/[0.05] px-2 py-0.5 text-[11px] text-ink/70">
                                    {t("playbooks.version", { version: entry.version })}
                                </span>
                                <span className="inline-flex items-center rounded-full bg-ink/[0.05] px-2 py-0.5 text-[11px] text-ink/70">
                                    {sourceLabel(entry.source)}
                                </span>
                            </div>

                            {entry.trigger && (
                                <p className="mb-6 -mt-2 text-[13px] leading-relaxed text-muted">
                                    <span className="font-medium">{t("playbooks.trigger")}:</span> {entry.trigger}
                                </p>
                            )}

                            {pendingOffer && plan ? (
                                <PlaybookSuggestionCard
                                    entry={entry}
                                    plan={plan}
                                    busy={answering}
                                    onAccept={() => acceptSuggestion(plan)}
                                    onDecline={() => declineSuggestion(plan)}
                                />
                            ) : (
                                <PlaybookDeliveryPanel
                                    entry={entry}
                                    plan={plan}
                                    schedulesFailed={schedulesFailed}
                                    onRetrySchedules={onChanged}
                                    channels={channels}
                                    defaultChannels={defaultChannels}
                                    onSaved={() => onChanged()}
                                />
                            )}

                            {entry.body.trim() ? (
                                <div className="text-[14px] leading-relaxed text-ink">
                                    <MarkdownContent content={entry.body} />
                                </div>
                            ) : (
                                <p className="text-[13px] text-muted">{t("playbooks.readerEmpty")}</p>
                            )}

                            {(entry.scripts?.length ?? 0) > 0 && (
                                <div className="mt-10">
                                    <h2 className="mb-1.5 text-[17px] font-semibold text-ink">{t("playbooks.scripts")}</h2>
                                    <p className="mb-4 text-[13px] leading-relaxed text-muted">{t("playbooks.scriptsHint")}</p>
                                    <div className="flex flex-col gap-3">
                                        {entry.scripts?.map((rel) => (
                                            <div key={rel} className="overflow-hidden rounded-lg border border-border-input">
                                                <div className="flex items-center gap-2 border-b border-border-input bg-ink/[0.03] px-3.5 py-2">
                                                    <FileCode2 className="size-3.5 shrink-0 text-muted" />
                                                    <span className="truncate font-mono text-[12px] font-medium text-ink/80">{rel}</span>
                                                </div>
                                                <pre className="overflow-x-auto px-3.5 py-3 font-mono text-[12px] leading-relaxed text-ink/90">
                                                    {scriptContents[rel] ?? ""}
                                                </pre>
                                            </div>
                                        ))}
                                    </div>
                                </div>
                            )}
                        </>
                    )}
                </div>
            </div>

            <div className="flex h-[52px] shrink-0 items-center justify-end gap-2 border-t border-border-input px-8 py-2">
                {editing ? (
                    <>
                        <Button variant="outline" onClick={() => setEditing(false)}>
                            {t("playbooks.cancel")}
                        </Button>
                        <Button onClick={saveEdit}>{t("playbooks.save")}</Button>
                    </>
                ) : (
                    <>
                        <Button variant="secondary" onClick={startEditing}>
                            {t("playbooks.edit")}
                        </Button>
                        {onStartSop && (
                            <Button variant="secondary" disabled={running} onClick={runInChat}>
                                {t("playbooks.runInChat")}
                            </Button>
                        )}
                        {runStarted ? (
                            <p role="status" className="m-0 text-[12.5px] text-muted">{t("playbooks.runNowStarted")}</p>
                        ) : (
                            <Button disabled={running} aria-busy={running} onClick={runNow}>
                                {running && <Loader2 className="animate-spin" />}
                                {t("playbooks.runNow")}
                            </Button>
                        )}
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                                <Button variant="outline" size="icon" aria-label={t("playbooks.more")}>
                                    <MoreHorizontal className="size-4" />
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                                <DropdownMenuItem onSelect={() => exportPlaybook(entry.id)}>
                                    <Download />
                                    {t("playbooks.export")}
                                </DropdownMenuItem>
                                <DropdownMenuItem onSelect={importPlaybook}>
                                    <Upload />
                                    {t("playbooks.import")}
                                </DropdownMenuItem>
                                <DropdownMenuSeparator />
                                <DropdownMenuItem destructive onSelect={() => setConfirmDelete(true)}>
                                    <Trash2 />
                                    {t("playbooks.delete")}
                                </DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </>
                )}
            </div>
            <ActivityInlineNotice notice={notice} className="mt-2" />

            <ConfirmDialog
                open={confirmDelete}
                onOpenChange={(open) => {
                    if (!open) setConfirmDelete(false);
                }}
                title={t("playbooks.deleteConfirmTitle")}
                description={
                    <>
                        {t("playbooks.deleteConfirmStart")}{" "}
                        <span className="font-medium text-ink">{entry.title || t("playbooks.deleteConfirmFallback")}</span>
                        {t("playbooks.deleteConfirmEnd")}
                    </>
                }
                confirmLabel={t("playbooks.delete")}
                variant="danger"
                onConfirm={deleteEntry}
            />
        </div>
    );
}
