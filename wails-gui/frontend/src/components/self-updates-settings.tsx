import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
    ListSelfDreamUpdates,
    ReviewSelfDreamOperation,
    type SelfDreamDecision,
    type SelfDreamUpdate,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { SettingsCard } from "./settings-page";
import { Button } from "./ui/button";
import { Textarea } from "./ui/textarea";

// Long statements, URLs and scope keys wrap inside their column.
const WRAP_CLASS = "break-words [overflow-wrap:anywhere]";

// A fresh key per click; the binding ignores a repeated key.
function newIdempotencyKey(): string {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
        return crypto.randomUUID();
    }
    return `${Date.now().toString(16)}-${Math.random().toString(16).slice(2, 10)}`;
}

// Compilation refuses a proposal whose target changed since the run; the
// binding's error then names the target as stale or no longer active.
function isStaleError(error: unknown): boolean {
    return /stale|no longer active/i.test(String(error));
}

/** A proposal that left the list while the user was working on it. */
export interface RetiredSelfUpdate {
    update: SelfDreamUpdate;
    // Where it was listed, so it stays in place.
    index: number;
    // The correction the user had typed, kept so it can be copied.
    correction: string;
}

export interface SelfUpdatesCardState {
    updates: SelfDreamUpdate[] | null;
    // Open correction editors by proposal id, with their typed text.
    corrections: Record<string, string>;
    // The proposal whose last decision did not save.
    failedId: string | null;
    retired: RetiredSelfUpdate[];
}

export const initialSelfUpdatesCardState: SelfUpdatesCardState = {
    updates: null,
    corrections: {},
    failedId: null,
    retired: [],
};

function withoutKey(record: Record<string, string>, key: string): Record<string, string> {
    if (!(key in record)) return record;
    const next = { ...record };
    delete next[key];
    return next;
}

/** Keeps a listed proposal in place after it leaves the list. */
export function retireSelfUpdate(
    state: SelfUpdatesCardState,
    id: string,
    correction: string,
): SelfUpdatesCardState {
    const index = state.updates?.findIndex((update) => update.id === id) ?? -1;
    if (index < 0 || !state.updates) return state;
    return {
        ...state,
        corrections: withoutKey(state.corrections, id),
        failedId: state.failedId === id ? null : state.failedId,
        retired: [
            ...state.retired.filter((item) => item.update.id !== id),
            { update: state.updates[index], index, correction },
        ],
    };
}

/**
 * Applies a fresh list. A proposal that leaves the list with its correction
 * open is retired in place with the typed text; a save error for a proposal
 * that left the list is dropped; a retired proposal listed again is shown
 * normally, with its correction editor reopened on the kept text.
 */
export function receiveSelfUpdates(
    state: SelfUpdatesCardState,
    next: SelfDreamUpdate[],
): SelfUpdatesCardState {
    const listed = new Set(next.map((update) => update.id));
    const corrections = { ...state.corrections };
    for (const item of state.retired) {
        if (listed.has(item.update.id) && item.correction) {
            corrections[item.update.id] = item.correction;
        }
    }
    let result: SelfUpdatesCardState = {
        ...state,
        corrections,
        retired: state.retired.filter((item) => !listed.has(item.update.id)),
    };
    for (const [id, text] of Object.entries(state.corrections)) {
        if (!listed.has(id)) result = retireSelfUpdate(result, id, text);
    }
    return {
        ...result,
        updates: next,
        failedId: result.failedId && listed.has(result.failedId) ? result.failedId : null,
    };
}

/**
 * The Self Dream proposals that wait for the user. Nothing changes what
 * Isoform understands about the user until they accept or correct one here.
 */
export function SelfUpdatesSettings() {
    const { t, i18n } = useTranslation("settings");
    const locale = i18n.resolvedLanguage ?? i18n.language;

    const [state, setState] = useState<SelfUpdatesCardState>(initialSelfUpdatesCardState);
    const [loadFailed, setLoadFailed] = useState(false);
    const [busyId, setBusyId] = useState<string | null>(null);

    const mounted = useRef(false);
    const busyRef = useRef(false);
    // Bumped by every read and write; a read whose version is stale when it resolves is dropped.
    const readVersion = useRef(0);

    const refresh = useCallback(async () => {
        if (busyRef.current) return;
        const version = ++readVersion.current;
        try {
            const next = await ListSelfDreamUpdates();
            if (mounted.current && version === readVersion.current) {
                setState((previous) => receiveSelfUpdates(previous, next ?? []));
                setLoadFailed(false);
            }
        } catch {
            if (mounted.current && version === readVersion.current) setLoadFailed(true);
        }
    }, []);

    useEffect(() => {
        mounted.current = true;
        void refresh();
        const offDream = EventsOn("self_dream:updated", () => void refresh());
        const offSelf = EventsOn("self_model:updated", () => void refresh());
        return () => {
            mounted.current = false;
            readVersion.current += 1;
            offDream();
            offSelf();
        };
    }, [refresh]);

    // One decision at a time. Only the decided proposal's editor and error are
    // cleared; the list is read again afterwards, so a proposal the decision
    // answered, or that went stale, drops out.
    const decide = async (update: SelfDreamUpdate, decision: SelfDreamDecision, note?: string) => {
        if (busyRef.current) return;
        busyRef.current = true;
        readVersion.current += 1;
        setBusyId(update.id);
        try {
            await ReviewSelfDreamOperation({
                operationId: update.id,
                idempotencyKey: newIdempotencyKey(),
                decision,
                ...(note ? { note } : {}),
            });
            if (mounted.current) {
                setState((previous) => ({
                    ...previous,
                    corrections: withoutKey(previous.corrections, update.id),
                    failedId: previous.failedId === update.id ? null : previous.failedId,
                }));
            }
        } catch (error) {
            if (mounted.current) {
                setState((previous) =>
                    isStaleError(error)
                        ? retireSelfUpdate(previous, update.id, previous.corrections[update.id] ?? "")
                        : { ...previous, failedId: update.id },
                );
            }
        } finally {
            busyRef.current = false;
            if (mounted.current) setBusyId(null);
        }
        await refresh();
    };

    const setCorrection = (id: string, text: string | null) =>
        setState((previous) => ({
            ...previous,
            corrections:
                text === null
                    ? withoutKey(previous.corrections, id)
                    : { ...previous.corrections, [id]: text },
        }));

    const dismissRetired = (id: string) =>
        setState((previous) => ({
            ...previous,
            retired: previous.retired.filter((item) => item.update.id !== id),
        }));

    const formatDate = (time: number) =>
        new Date(time).toLocaleDateString(locale, { month: "short", day: "numeric" });

    const operationText = (update: SelfDreamUpdate) => ({
        title: t(`selfUpdates.operations.${update.operation}.title` as never) as string,
        summary: t(`selfUpdates.operations.${update.operation}.summary` as never, {
            entries: update.before.length,
        }) as string,
    });

    const details = (update: SelfDreamUpdate) => {
        const parts = [t("selfUpdates.sources", { count: update.evidenceCount })];
        if (update.scope.kind !== "global" && update.scope.key) {
            parts.push(
                t("selfUpdates.scope", {
                    kind: t(`selfUpdates.scopeKinds.${update.scope.kind}` as never) as string,
                    key: update.scope.key,
                }),
            );
        }
        return parts.join(" · ");
    };

    const statements = (heading: string, items: string[], tone: string) => (
        <section className="min-w-0">
            <h4 className="text-[11px] font-medium text-muted">{heading}</h4>
            <ul className="mt-1 space-y-1">
                {items.map((item, index) => (
                    <li key={index} className={`text-xs leading-5 ${tone} ${WRAP_CLASS}`}>
                        {item}
                    </li>
                ))}
            </ul>
        </section>
    );

    const rowClass =
        "border-b border-[var(--s-divider)] py-4 first:pt-0 last:border-b-0 last:pb-0";

    const renderUpdate = (update: SelfDreamUpdate) => {
        const { title, summary } = operationText(update);
        const busy = busyId === update.id;
        const correction = state.corrections[update.id];
        const correcting = correction !== undefined;
        return (
            <article key={update.id} aria-label={title} className={rowClass}>
                <div className="flex items-start justify-between gap-4">
                    <div className="min-w-0">
                        <p className={`settings-row-title ${WRAP_CLASS}`}>{title}</p>
                        <p className={`settings-row-desc ${WRAP_CLASS}`}>{summary}</p>
                    </div>
                    <span className="shrink-0 text-[11px] tabular-nums text-muted">
                        {formatDate(update.createdAt)}
                    </span>
                </div>
                <div className="mt-3 grid gap-3 sm:grid-cols-2">
                    {update.before.length > 0 &&
                        statements(t("selfUpdates.current"), update.before, "text-muted")}
                    {statements(t("selfUpdates.suggested"), update.after, "text-ink")}
                </div>
                <p className={`mt-2 text-[11px] text-muted ${WRAP_CLASS}`}>{details(update)}</p>
                {update.uncertainty && (
                    <p className={`mt-1 text-[11px] text-muted ${WRAP_CLASS}`}>{update.uncertainty}</p>
                )}
                {correcting ? (
                    <div className="mt-3 space-y-2">
                        <label
                            htmlFor={`self-update-correction-${update.id}`}
                            className="block text-xs font-medium text-ink"
                        >
                            {t("selfUpdates.correctionLabel")}
                        </label>
                        <Textarea
                            id={`self-update-correction-${update.id}`}
                            value={correction}
                            className="min-h-20 resize-none text-xs"
                            placeholder={t("selfUpdates.correctionPlaceholder")}
                            disabled={busyId !== null}
                            autoFocus
                            onChange={(event) => setCorrection(update.id, event.target.value)}
                        />
                        <div className="flex items-center justify-end gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                disabled={busyId !== null}
                                onClick={() => setCorrection(update.id, null)}
                            >
                                {t("selfUpdates.cancel")}
                            </Button>
                            <Button
                                size="sm"
                                disabled={busyId !== null || !correction.trim()}
                                onClick={() => void decide(update, "correct", correction.trim())}
                            >
                                {busy && <Loader2 className="size-3.5 animate-spin" />}
                                {t("selfUpdates.saveCorrection")}
                            </Button>
                        </div>
                    </div>
                ) : (
                    <div className="mt-3 flex flex-wrap items-center justify-end gap-2">
                        {busy && <Loader2 className="size-3.5 animate-spin text-muted" />}
                        <Button
                            variant="outline"
                            size="sm"
                            disabled={busyId !== null}
                            onClick={() => void decide(update, "not_enough_evidence")}
                        >
                            {t("selfUpdates.needMoreEvidence")}
                        </Button>
                        <Button
                            variant="outline"
                            size="sm"
                            disabled={busyId !== null}
                            onClick={() => void decide(update, "keep_separate")}
                        >
                            {t("selfUpdates.keep")}
                        </Button>
                        <Button
                            variant="secondary"
                            size="sm"
                            disabled={busyId !== null}
                            onClick={() => setCorrection(update.id, "")}
                        >
                            {t("selfUpdates.correct")}
                        </Button>
                        <Button
                            size="sm"
                            disabled={busyId !== null}
                            onClick={() => void decide(update, "accept")}
                        >
                            {t("selfUpdates.accept")}
                        </Button>
                    </div>
                )}
                {state.failedId === update.id && (
                    <p role="alert" className="mt-2 text-right text-xs text-error">
                        {t("selfUpdates.saveFailed")}
                    </p>
                )}
            </article>
        );
    };

    const renderRetired = ({ update, correction }: RetiredSelfUpdate) => {
        const { title } = operationText(update);
        return (
            <article key={`retired-${update.id}`} aria-label={title} className={rowClass}>
                <p className={`settings-row-title ${WRAP_CLASS}`}>{title}</p>
                <p role="alert" className={`mt-1 text-xs text-warning ${WRAP_CLASS}`}>
                    {t("selfUpdates.stale")}
                </p>
                {correction && (
                    <div className="mt-3 space-y-2">
                        <label
                            htmlFor={`self-update-kept-${update.id}`}
                            className="block text-xs font-medium text-ink"
                        >
                            {t("selfUpdates.keptCorrection")}
                        </label>
                        <Textarea
                            id={`self-update-kept-${update.id}`}
                            value={correction}
                            className="min-h-20 resize-none text-xs"
                            readOnly
                        />
                    </div>
                )}
                <div className="mt-3 flex justify-end">
                    <Button variant="outline" size="sm" onClick={() => dismissRetired(update.id)}>
                        {t("selfUpdates.dismiss")}
                    </Button>
                </div>
            </article>
        );
    };

    let body: ReactNode;
    if (loadFailed) {
        body = (
            <div className="flex items-center justify-between gap-3">
                <p className="text-xs text-error" role="alert">
                    {t("selfUpdates.loadFailed")}
                </p>
                <Button variant="outline" size="sm" onClick={() => void refresh()}>
                    {t("selfUpdates.retry")}
                </Button>
            </div>
        );
    } else if (!state.updates) {
        body = (
            <p className="flex items-center gap-2 text-xs text-muted">
                <Loader2 className="size-3.5 animate-spin" />
                {t("selfUpdates.loading")}
            </p>
        );
    } else {
        // Retired proposals keep the place they had in the list.
        const rows: ReactNode[] = state.updates.map(renderUpdate);
        for (const item of [...state.retired].sort((left, right) => left.index - right.index)) {
            rows.splice(Math.min(item.index, rows.length), 0, renderRetired(item));
        }
        if (rows.length === 0) return null;
        body = (
            <>
                {rows.length > 0 && <div>{rows}</div>}
                {state.updates.length === 0 && (
                    <p className={`text-xs text-muted ${rows.length > 0 ? "pt-4" : ""}`}>
                        {t("selfUpdates.empty")}
                    </p>
                )}
            </>
        );
    }

    return (
        <SettingsCard title={t("selfUpdates.title")} description={t("selfUpdates.description")}>
            {body}
        </SettingsCard>
    );
}
