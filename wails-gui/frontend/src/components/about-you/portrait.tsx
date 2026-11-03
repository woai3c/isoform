import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { Check } from 'lucide-react';
import { GetAboutYouPortrait, usePagePrivacyWithdrawal, usePageQueryRefresh } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { EventsEmit } from '../../../wailsjs/runtime/runtime';
import {
    AnswerAboutYouPortraitBelief,
    ConfirmAboutYouPortrait,
    ConfirmMemoryEntry,
    MarkAboutYouPortraitSeen,
    NotQuiteAboutYouPortrait,
    RetryAboutYouPortrait,
    ShowAboutYouPortraitPending,
    UndoAboutYouPortraitBelief,
    UndoConfirmAboutYouPortrait,
    UndoConfirmMemoryEntry,
    type AboutYouBelief,
    type AboutYouPortrait,
    type AboutYouPortraitCandidate,
    type AboutYouPortraitFailure,
    type AboutYouPortraitPart,
} from '@/lib/wails-shim-app';
import { copyTextToClipboard } from '@/components/activity-summary';
import { ConfidenceBar } from '@/components/about-you/belief-status';
import { UndoLink, answerWhen } from '@/components/about-you/new-this-week';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';

/** One belief behind a phrase, as the evidence popover lists it. */
export interface PortraitEvidence {
    id: string;
    text: string;
    status: AboutYouBelief['status'];
    confidence: number;
    sessionCount: number;
    dayCount: number;
    apps: string[];
}

const pill = 'h-7 rounded-full px-3 text-[12.5px] font-medium';

/**
 * The four sides as one figure: each point reaches out as far as the person
 * leans toward the first pole (top deep focus, right first principles,
 * bottom brief, left plans ahead). Without sides it is a dashed ring.
 */
export function PortraitShape({ sides, size = 44 }: { sides?: number[] | null; size?: number }) {
    const shape = sides && sides.length === 4;
    const points = shape
        ? sides.map((value, index) => {
            const angle = -Math.PI / 2 + (index * Math.PI) / 2;
            const radius = 6 + Math.max(0, (0.5 - value) * 2) * 30;
            return [42 + radius * Math.cos(angle), 42 + radius * Math.sin(angle)] as const;
        })
        : [];
    return (
        <svg viewBox="0 0 84 84" width={size} height={size} aria-hidden="true" className="flex-none" data-testid="portrait-shape" data-shape={shape ? 'figure' : 'ring'}>
            {shape ? (
                <>
                    <line x1="42" y1="6" x2="42" y2="78" className="stroke-border-strong" strokeWidth="1.2" />
                    <line x1="6" y1="42" x2="78" y2="42" className="stroke-border-strong" strokeWidth="1.2" />
                    <polygon
                        points={points.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(' ')}
                        className="fill-accent/20 stroke-accent"
                        strokeWidth="2"
                        strokeLinejoin="round"
                    />
                    {points.map(([x, y], index) => (
                        <circle key={index} cx={x.toFixed(1)} cy={y.toFixed(1)} r="3.4" className="fill-accent" />
                    ))}
                </>
            ) : (
                <circle cx="42" cy="42" r="34" fill="none" className="stroke-border-strong" strokeWidth="2" strokeDasharray="5 6" />
            )}
        </svg>
    );
}

function assistantName(t: TFunction<'memory'>, assistant: string) {
    switch (assistant) {
        case 'claude-code':
            return t('aboutYou.portrait.assistants.claudeCode');
        case 'codex':
            return t('aboutYou.portrait.assistants.codex');
        default:
            return t('aboutYou.portrait.assistants.fallback');
    }
}

/**
 * Why the AI couldn't be used, in words, with the time Isoform writes again,
 * never a duration (v2.0.0 section 3). Under the "Couldn't write it yet"
 * title an unreadable answer gives only the time, so the words don't repeat.
 */
function failureText(t: TFunction<'memory'>, language: string, failure: AboutYouPortraitFailure, titled: boolean) {
    const assistant = assistantName(t, failure.assistant);
    const time = new Intl.DateTimeFormat(language, { hour: 'numeric', minute: '2-digit' }).format(new Date(failure.retryAt));
    switch (failure.reason) {
        case 'limit':
            return t('aboutYou.portrait.reasons.limit', { assistant, time });
        case 'no_ai':
            return t('aboutYou.portrait.reasons.noAI');
        case 'unusable':
            return titled ? t('aboutYou.portrait.reasons.unusableTitled', { time }) : t('aboutYou.portrait.reasons.unusable', { time });
        default:
            return t('aboutYou.portrait.reasons.noAnswer', { assistant, time });
    }
}

/** Try now only where a retry can help: the AI didn't answer, or three answers couldn't be read. */
function canTryNow(failure: AboutYouPortraitFailure) {
    return failure.reason === 'no_answer' || failure.reason === 'unusable';
}

function openYourAI() {
    EventsEmit('settings:open', 'ai-provider');
}

/** The action a failure offers, if any: Try now, or Sign in when no AI is signed in. */
function FailureAction({ failure, onTryNow, link }: { failure: AboutYouPortraitFailure; onTryNow: () => void; link?: boolean }) {
    const { t } = useTranslation('memory');
    const label = failure.reason === 'no_ai' ? t('aboutYou.portrait.signIn') : canTryNow(failure) ? t('aboutYou.portrait.tryNow') : '';
    if (!label) return null;
    const onClick = failure.reason === 'no_ai' ? openYourAI : onTryNow;
    return link
        ? <button type="button" className="cursor-pointer border-0 bg-transparent p-0 font-semibold text-accent hover:underline" onClick={onClick}>{label}</button>
        : <Button size="sm" className={pill} onClick={onClick}>{label}</Button>;
}

/**
 * "Confirm these and Isoform can say more" (v2.0.0 section 6): up to three
 * beliefs whose confirmation would let dropped sentences pass, each with
 * That's me and Not quite at equal weight. An answer reads "Confirmed" or
 * "Won't use it" with Undo until the person leaves (answer feedback
 * section 5).
 */
function ConfirmRows({
    rows,
    writing,
    onAnswer,
    onUndo,
}: {
    rows: AboutYouPortraitCandidate[];
    writing: boolean;
    onAnswer: (id: string, thatsMe: boolean) => void;
    onUndo: (id: string) => void;
}) {
    const { t } = useTranslation('memory');
    return (
        <div className="mt-3 rounded-xl bg-panel-warm px-3 py-2.5" data-testid="portrait-confirm-rows">
            <div className="mb-1.5 text-[12.5px] font-semibold text-ink">{t('aboutYou.portrait.confirmRows.title')}</div>
            <ul className="m-0 list-none p-0">
                {rows.map((row) => (
                    <li
                        key={row.memoryId}
                        data-answer={row.answer || 'open'}
                        className={cn(
                            'grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 border-t border-border-divider py-[7px] text-[13px] leading-[1.4] text-ink first:border-t-0 max-[560px]:grid-cols-[minmax(0,1fr)_auto]',
                            row.answer && 'grid-cols-[minmax(0,1fr)_auto] max-[560px]:grid-cols-[minmax(0,1fr)_auto]',
                        )}
                    >
                        <span className={cn('min-w-0', row.answer === 'noted' && 'text-subtle')}>{row.text}</span>
                        {!row.answer && <span className="text-[11.5px] font-medium text-muted max-[560px]:hidden">{t('aboutYou.status.inferred')}</span>}
                        {row.answer ? (
                            <span role="status" className={cn('answer-line inline-flex items-center gap-1.5 text-[12px]', row.answer === 'confirmed' ? 'text-success' : 'text-muted')}>
                                {row.answer === 'confirmed' && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
                                <span className={cn(row.answer === 'confirmed' && 'font-semibold')}>
                                    {t(row.answer === 'confirmed' ? 'aboutYou.portrait.confirmRows.confirmed' : 'aboutYou.portrait.confirmRows.notUsed')}
                                </span>
                                <span aria-hidden="true" className="text-muted">·</span>
                                <UndoLink onClick={() => onUndo(row.memoryId)} />
                            </span>
                        ) : (
                            <span className="flex gap-1.5">
                                <Button size="sm" variant="outline" className="h-[26px] rounded-full px-3 text-[12px] font-medium text-ink" onClick={() => onAnswer(row.memoryId, true)}>
                                    {t('aboutYou.portrait.thatsMe')}
                                </Button>
                                <Button size="sm" variant="outline" className="h-[26px] rounded-full px-3 text-[12px] font-medium text-ink" onClick={() => onAnswer(row.memoryId, false)}>
                                    {t('aboutYou.portrait.notQuite')}
                                </Button>
                            </span>
                        )}
                    </li>
                ))}
            </ul>
            {writing && <p className="m-0 mt-1.5 text-[12px] text-accent">{t('aboutYou.portrait.confirmRows.writing')}</p>}
        </div>
    );
}

/** "today", "yesterday", a weekday within the week, else a short date. */
function updatedWhen(language: string, at: number) {
    const start = (time: number) => new Date(new Date(time).toDateString()).getTime();
    const days = Math.round((start(Date.now()) - start(at)) / 86_400_000);
    if (days <= 1) return new Intl.RelativeTimeFormat(language, { numeric: 'auto' }).format(-Math.max(0, days), 'day');
    if (days < 7) return new Intl.DateTimeFormat(language, { weekday: language.startsWith('zh') ? 'short' : 'long' }).format(new Date(at));
    return new Intl.DateTimeFormat(language, { month: 'short', day: 'numeric' }).format(new Date(at));
}

function EvidenceRows({ ids, describe }: { ids: string[]; describe: (id: string) => PortraitEvidence | undefined }) {
    const { t } = useTranslation('memory');
    return (
        <>
            {ids.map((id) => describe(id)).filter((row): row is PortraitEvidence => Boolean(row)).map((row) => {
                const seen = t('aboutYou.portrait.seen', {
                    count: Math.max(1, row.sessionCount),
                    days: t('aboutYou.portrait.days', { count: Math.max(1, row.dayCount) }),
                });
                return (
                    <li key={row.id} className="border-t border-border-divider py-2 first:border-t-0 first:pt-0">
                        <div className="leading-[1.4] text-ink">{t('aboutYou.portrait.quote', { text: row.text })}</div>
                        <div className="mt-1 flex flex-wrap items-center gap-2 text-[11.5px] text-muted">
                            <span className={cn(
                                'inline-flex h-5 items-center rounded-md px-2 text-[11px] font-semibold',
                                row.status === 'confirmed' ? 'bg-success-bg text-success' : row.status === 'learned_today' ? 'bg-accent-light text-accent' : 'bg-surface-active text-muted',
                            )}>
                                {t(`aboutYou.status.${row.status}`)}
                            </span>
                            <span className="inline-flex items-center gap-1.5">
                                {t('aboutYou.confidence', { count: Math.max(0, Math.min(4, Math.round(row.confidence))) })}
                                <ConfidenceBar level={row.confidence} />
                            </span>
                            <span>{row.apps.length ? t('aboutYou.portrait.seenWhere', { seen, apps: row.apps.join(t('aboutYou.portrait.appSeparator')) }) : seen}</span>
                        </div>
                    </li>
                );
            })}
        </>
    );
}

/** A phrase backed by beliefs: solid when any is confirmed, dotted when all are inferred. */
function Phrase({
    part,
    describe,
    lately,
    draw,
    onShowBelief,
    onNotQuiteBelief,
}: {
    part: AboutYouPortraitPart;
    describe: (id: string) => PortraitEvidence | undefined;
    lately?: { thisMonth: number; usual: number };
    /** The solid underline draws in, after this many milliseconds. */
    draw?: number;
    onShowBelief: (id: string) => void;
    onNotQuiteBelief: (id: string) => void;
}) {
    const { t } = useTranslation('memory');
    const [open, setOpen] = useState(false);
    const [confirming, setConfirming] = useState(false);
    // That's me here keeps the popover open with Undo (answer feedback section 3).
    const [confirmed, setConfirmed] = useState<string[] | null>(null);
    const ids = part.memoryIds ?? [];
    const first = ids.find((id) => describe(id));
    const confirm = async () => {
        setConfirming(true);
        const done: string[] = [];
        try {
            for (const id of ids) {
                if (describe(id)?.status !== 'confirmed') {
                    await ConfirmMemoryEntry(id);
                    done.push(id);
                }
            }
        } finally {
            setConfirming(false);
            setConfirmed(done);
        }
    };
    const undo = async () => {
        const done = confirmed ?? [];
        setConfirmed(null);
        for (const id of done) await UndoConfirmMemoryEntry(id).catch(() => {});
    };
    const drawing = confirmed !== null ? 120 : draw;
    const solid = part.confirmed || confirmed !== null;
    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                {/* A span, not a <button>: a button is inline-block, so a long
                    phrase could not wrap and every sentence would start a line. */}
                <span
                    role="button"
                    tabIndex={0}
                    onKeyDown={(event) => {
                        if (event.key !== 'Enter' && event.key !== ' ') return;
                        event.preventDefault();
                        setOpen(true);
                    }}
                    data-underline={solid ? 'solid' : 'dotted'}
                    data-drawing={drawing !== undefined || undefined}
                    style={drawing !== undefined ? ({ '--draw-delay': `${drawing}ms` } as React.CSSProperties) : undefined}
                    className={cn(
                        'cursor-pointer rounded-[3px] underline decoration-[1.5px] underline-offset-4 [-webkit-box-decoration-break:clone] [box-decoration-break:clone] hover:bg-accent-light focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus',
                        drawing !== undefined
                            ? 'answer-underline-draw decoration-dotted'
                            : solid ? 'decoration-solid decoration-accent/80' : 'decoration-dotted decoration-accent/55',
                        open && 'bg-accent-light',
                    )}
                >
                    {part.text}
                </span>
            </PopoverTrigger>
            <PopoverContent align="start" className="w-[340px] max-w-[calc(100vw-16px)] rounded-xl px-3.5 py-3 text-[12.5px]">
                <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.06em] text-muted">{t('aboutYou.portrait.evidenceTitle')}</div>
                {lately && (
                    <p className="m-0 mb-2 text-ink">{t('aboutYou.portrait.latelyFacts', lately)}</p>
                )}
                <ul className="m-0 list-none p-0">
                    <EvidenceRows ids={ids} describe={describe} />
                </ul>
                {!lately && first && (
                    <div className="mt-2 flex items-center gap-2 border-t border-border-divider pt-2.5">
                        {confirmed !== null ? (
                            <span role="status" className="answer-line inline-flex items-center gap-1.5 text-[12.5px] text-success">
                                <Check className="h-3.5 w-3.5" aria-hidden="true" />
                                <span className="font-semibold">{t('aboutYou.portrait.phraseConfirmed')}</span>
                                <span aria-hidden="true" className="text-muted">·</span>
                                <UndoLink onClick={() => void undo()} />
                            </span>
                        ) : (
                            <>
                                <Button size="sm" variant="outline" className="h-[26px] rounded-full px-3 text-[12px] font-medium" disabled={confirming} onClick={() => void confirm()}>
                                    {t('aboutYou.portrait.thatsMe')}
                                </Button>
                                <Button size="sm" variant="outline" className="h-[26px] rounded-full px-3 text-[12px] font-medium" onClick={() => { setOpen(false); onNotQuiteBelief(first); }}>
                                    {t('aboutYou.portrait.notQuite')}
                                </Button>
                            </>
                        )}
                        <button type="button" className="ml-auto cursor-pointer border-0 bg-transparent p-0 text-[12.5px] text-accent hover:underline" onClick={() => { setOpen(false); onShowBelief(first); }}>
                            {t('aboutYou.portrait.showInBeliefs')}
                        </button>
                    </div>
                )}
            </PopoverContent>
        </Popover>
    );
}

function sameVersion(a: AboutYouPortrait, b: AboutYouPortrait) {
    return a.generatedAt === b.generatedAt;
}

/**
 * Your portrait (handoff portrait v1.0.0, v2.0.0): a name from a fixed set of
 * 16 and up to four sentences, saved ahead so the card paints in the first
 * frame. It shows what passed Isoform's checks, one sentence or only the
 * shape, with the beliefs to confirm that would let it say more.
 * Each underlined phrase opens the beliefs it rests on. A rewrite that lands
 * while the person reads waits behind "Show it".
 */
export function AboutYouPortraitCard({
    describe,
    onShowBelief,
    onNotQuiteBelief,
}: {
    describe: (id: string) => PortraitEvidence | undefined;
    onShowBelief: (id: string) => void;
    onNotQuiteBelief: (id: string) => void;
}) {
    const { t, i18n } = useTranslation('memory');
    // A warm reopen shows the cached portrait while it revalidates.
    const [portrait, setPortrait] = useState<AboutYouPortrait | null>(() => pageQueryCache.peek<AboutYouPortrait>('GetAboutYouPortrait') ?? null);
    const [pending, setPending] = useState<AboutYouPortrait | null>(null);
    const [reload, setReload] = useState(0);
    const [said, setSaid] = useState<string[] | null>(null);
    // Phrases whose solid underline draws in, by "sentence-part", with their delay.
    const [drawn, setDrawn] = useState<Map<string, number>>(() => new Map());
    const beforeSaid = useRef<AboutYouPortrait | null>(null);
    const saidStatus = useRef<HTMLDivElement>(null);
    // Rows answered here keep their answer and Undo until the person leaves.
    const [rowAnswers, setRowAnswers] = useState<Record<string, AboutYouPortraitCandidate['answer']>>({});
    const answeredRows = useRef(new Map<string, AboutYouPortraitCandidate>());
    const accept = useRef(false);
    const [noting, setNoting] = useState(false);
    const [note, setNote] = useState('');
    const [copied, setCopied] = useState(false);
    const shown = useRef<AboutYouPortrait | null>(portrait);
    const settled = useRef(false);
    const refresh = useCallback(() => setReload((value) => value + 1), []);

    useEffect(() => {
        let current = true;
        GetAboutYouPortrait()
            .then((next: AboutYouPortrait) => {
                if (!current) return;
                const before = shown.current;
                // The first read after opening applies at once. Later, a new
                // version never replaces the one being read: it waits behind
                // "Show it", unless the person asked for it with Not quite.
                const hold = !accept.current && settled.current && before?.state === 'ready' && next.state === 'ready'
                    && !sameVersion(before, next) && !before.rewriting && !before.confirming;
                settled.current = true;
                accept.current = false;
                if (hold) {
                    setPending(next);
                    return;
                }
                shown.current = next;
                setPortrait(next);
                setPending((held) => (held && !sameVersion(held, next) ? held : null));
            })
            .catch(() => {
                // The backend writes a new portrait when the saved one can't
                // be read; the next event re-reads it.
            });
        return () => {
            current = false;
        };
    }, [reload]);

    usePageQueryRefresh(['GetAboutYouPortrait'], refresh);
    // A privacy transition withdraws the shown text first, then re-reads.
    usePagePrivacyWithdrawal(useCallback(() => {
        shown.current = null;
        settled.current = false;
        setPortrait(null);
        setPending(null);
        setReload((value) => value + 1);
    }, []));

    const generatedAt = portrait?.generatedAt ?? 0;
    useEffect(() => {
        setSaid(null);
        setDrawn(new Map());
    }, [generatedAt]);
    useEffect(() => {
        if (said) saidStatus.current?.focus();
    }, [said]);
    const changed = (portrait?.sentences ?? []).some((sentence) => sentence.changed);
    // Changed words stay tinted until the person has seen them.
    useEffect(() => {
        if (!changed) return;
        return () => {
            void MarkAboutYouPortraitSeen().catch(() => {});
        };
    }, [changed, generatedAt]);

    const sentences = portrait?.sentences ?? [];
    const ready = portrait?.state === 'ready' && sentences.length > 0;
    const shapeOnly = portrait?.state === 'shape';
    const named = ready || shapeOnly;
    const working = portrait ? !named : false;
    // A row answered on an earlier visit is gone; one answered here stays.
    const rows = [
        ...(portrait?.candidates ?? [])
            .filter((row) => !row.answer || row.memoryId in rowAnswers)
            .map((row) => (row.memoryId in rowAnswers ? { ...row, answer: rowAnswers[row.memoryId] } : row)),
    ];
    for (const [id, row] of answeredRows.current) {
        if (rowAnswers[id] && !rows.some((shown) => shown.memoryId === id)) rows.push({ ...row, answer: rowAnswers[id] });
    }
    const lines = portrait?.workWithMe ?? [];
    const progress = portrait?.progress;

    const workText = useMemo(
        () => [t('aboutYou.portrait.workWithMe.title'), '', ...lines.map((line) => `- ${line.text}`)].join('\n'),
        [lines, t],
    );
    useEffect(() => {
        if (!copied) return;
        const timer = setTimeout(() => setCopied(false), 1600);
        return () => clearTimeout(timer);
    }, [copied]);

    // That's me answers at once: the underlines draw solid one after another,
    // How to work with me follows, and the version is kept (answer feedback
    // section 3). Undo puts all of it back.
    const thatsMe = async () => {
        if (!portrait) return;
        const under = new Set<string>();
        const draws = new Map<string, number>();
        portrait.sentences?.forEach((sentence, si) => sentence.parts.forEach((part, pi) => {
            for (const id of part.memoryIds ?? []) under.add(id);
            if ((part.memoryIds?.length ?? 0) > 0 && !part.confirmed) draws.set(`${si}-${pi}`, 120 + 90 * draws.size);
        }));
        beforeSaid.current = portrait;
        const next: AboutYouPortrait = {
            ...portrait,
            sentences: portrait.sentences?.map((sentence) => ({ ...sentence, parts: sentence.parts.map((part) => ((part.memoryIds?.length ?? 0) > 0 ? { ...part, confirmed: true } : part)) })),
            workWithMe: portrait.workWithMe?.map((line) => ((line.memoryIds ?? []).length > 0 && (line.memoryIds ?? []).every((id) => under.has(id)) ? { ...line, confirmed: true } : line)),
        };
        shown.current = next;
        setPortrait(next);
        setDrawn(draws);
        setSaid([]);
        try {
            setSaid(await ConfirmAboutYouPortrait());
        } catch {
            putBack();
        }
    };
    const putBack = () => {
        const before = beforeSaid.current;
        beforeSaid.current = null;
        setSaid(null);
        setDrawn(new Map());
        if (before) {
            shown.current = before;
            setPortrait(before);
        }
    };
    const undo = async () => {
        const ids = said ?? [];
        putBack();
        await UndoConfirmAboutYouPortrait(ids).catch(() => {});
    };
    const showPending = async () => {
        if (pending) {
            shown.current = pending;
            setPortrait(pending);
            setPending(null);
        }
        if (portrait?.pending) {
            accept.current = true;
            await ShowAboutYouPortraitPending().catch(() => {});
            pageQueryCache.invalidate(['GetAboutYouPortrait']);
            refresh();
        }
    };
    const saveNote = async () => {
        setNoting(false);
        if (portrait) {
            const next = { ...portrait, rewriting: true };
            shown.current = next;
            setPortrait(next);
        }
        await NotQuiteAboutYouPortrait(note.trim()).catch(() => {});
        setNote('');
    };
    const tryNow = async () => {
        if (portrait) {
            const next = { ...portrait, writing: true, failure: named ? null : portrait.failure, state: portrait.state === 'failed' ? 'writing' as const : portrait.state };
            shown.current = next;
            setPortrait(next);
        }
        await RetryAboutYouPortrait().catch(() => {});
    };
    // A row answers at once on the card; That's me shows "Writing with what
    // you confirmed" until the rewrite lands, and that version applies at once.
    const answer = async (id: string, thatsMe: boolean) => {
        const row = rows.find((candidate) => candidate.memoryId === id);
        if (row) answeredRows.current.set(id, row);
        setRowAnswers((prev) => ({ ...prev, [id]: thatsMe ? 'confirmed' : 'noted' }));
        if (portrait && thatsMe) {
            const next = { ...portrait, confirming: true };
            shown.current = next;
            setPortrait(next);
        }
        await AnswerAboutYouPortraitBelief(id, thatsMe).catch(() => {});
    };
    const undoRow = async (id: string) => {
        const thatsMe = rowAnswers[id] === 'confirmed';
        setRowAnswers((prev) => ({ ...prev, [id]: '' }));
        await UndoAboutYouPortraitBelief(id, thatsMe).catch(() => {});
        pageQueryCache.invalidate(['GetAboutYouPortrait']);
        refresh();
    };

    // What changed says why, once, and goes with the tint (answer feedback
    // sections 4 and 5).
    let why = '';
    const causes = sentences.filter((sentence) => sentence.changed && sentence.cause).map((sentence) => sentence.cause!);
    const noteCause = causes.find((cause) => cause.notQuite);
    const confirmedIds = [...new Set(causes.flatMap((cause) => cause.confirm ?? []))];
    if (noteCause) {
        why = noteCause.note ? t('aboutYou.portrait.rewrittenNote', { note: noteCause.note }) : t('aboutYou.portrait.rewrittenNotQuite');
    } else if (confirmedIds.length === 1 && describe(confirmedIds[0])) {
        why = t('aboutYou.portrait.becauseOne', { belief: describe(confirmedIds[0])!.text });
    } else if (confirmedIds.length > 0) {
        why = t('aboutYou.portrait.becauseMany', { count: confirmedIds.length });
    }
    const saidCount = new Set(sentences.flatMap((sentence) => sentence.parts.flatMap((part) => part.memoryIds ?? []))).size;

    let name = '';
    if (portrait?.name?.length === 2) {
        name = t('aboutYou.portrait.name', {
            first: t(`aboutYou.portrait.words.${portrait.name[0]}` as 'aboutYou.portrait.words.blueprint'),
            second: t(`aboutYou.portrait.words.${portrait.name[1]}` as 'aboutYou.portrait.words.builder'),
        });
    }
    let title = name;
    switch (true) {
        case !portrait:
            title = '';
            break;
        case portrait?.state === 'learning':
            title = t('aboutYou.portrait.learning');
            break;
        case portrait?.state === 'writing':
            title = t('aboutYou.portrait.writingTitle');
            break;
        case portrait?.state === 'failed':
            title = t('aboutYou.portrait.failedTitle');
            break;
    }
    const groupList = new Intl.ListFormat(i18n.language, { style: 'long', type: 'conjunction' });

    const whyName = named && portrait?.name?.length === 2 && (
        <Popover>
            <PopoverTrigger asChild>
                <button type="button" className="cursor-pointer self-start whitespace-nowrap border-0 bg-transparent p-0 text-[12.5px] font-medium text-subtle hover:text-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus rounded-sm ml-auto max-[560px]:order-3 max-[560px]:-mt-1 max-[560px]:ml-[58px] max-[560px]:basis-full max-[560px]:text-left">
                    {t('aboutYou.portrait.whyName')}
                </button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-[340px] max-w-[calc(100vw-16px)] rounded-xl px-3.5 py-3 text-[12.5px]">
                <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.06em] text-muted">{t('aboutYou.portrait.whyName')}</div>
                <ul className="m-0 list-none p-0">
                    {portrait.name!.map((word, index) => {
                        const areas = portrait.nameAreas?.[index] ?? [];
                        return (
                            <li key={word} className="border-t border-border-divider py-2 first:border-t-0 first:pt-0">
                                <div className="leading-[1.4] text-ink">
                                    {t('aboutYou.portrait.whyRow', {
                                        word: t(`aboutYou.portrait.words.${word}` as 'aboutYou.portrait.words.blueprint'),
                                        meaning: t(`aboutYou.portrait.meanings.${word}` as 'aboutYou.portrait.meanings.blueprint'),
                                    })}
                                </div>
                                {areas.length > 0 && (
                                    <div className="mt-1 text-[11.5px] text-muted">
                                        {t('aboutYou.portrait.whyFrom', {
                                            groups: groupList.format(areas.map((area) => t(`aboutYou.portrait.groups.${area}` as 'aboutYou.portrait.groups.work'))),
                                        })}
                                    </div>
                                )}
                            </li>
                        );
                    })}
                </ul>
            </PopoverContent>
        </Popover>
    );

    let body;
    switch (true) {
        case !portrait:
            // Withdrawn for privacy and being re-read: an empty card, never a spinner.
            body = <div aria-busy="true" className="min-h-[96px]" />;
            break;
        case portrait?.state === 'learning':
            body = (
                <div>
                    <p className="m-0 mt-2.5 max-w-[58ch] text-[14.5px] leading-[1.55] text-muted">
                        {t('aboutYou.portrait.learningLine', { count: Math.max(1, progress?.moreDays ?? 1) })}
                    </p>
                    {progress && (
                        <div className="mt-3.5 grid max-w-[360px] gap-1.5 text-[12px] text-subtle">
                            <div className="h-1.5 overflow-hidden rounded-[3px] bg-surface-active">
                                <i
                                    data-testid="portrait-progress"
                                    className="block h-full w-full origin-left rounded-[3px] bg-accent"
                                    style={{ transform: `scaleX(${Math.min(1, Math.min(progress.beliefs / Math.max(1, progress.beliefsNeeded), progress.days / Math.max(1, progress.daysNeeded)))})` }}
                                />
                            </div>
                            <span>{t('aboutYou.portrait.learningBar', { ...progress })}</span>
                        </div>
                    )}
                </div>
            );
            break;
        case portrait?.state === 'writing':
            body = (
                <div>
                    <div aria-hidden="true" className="mt-3.5 grid max-w-[62ch] gap-[13px]">
                        {['96%', '90%', '94%', '62%'].map((width) => (
                            <i key={width} className="block h-[11px] rounded-md bg-surface-active" style={{ width }} />
                        ))}
                    </div>
                    <p className="m-0 mt-3 max-w-[58ch] text-[14.5px] leading-[1.55] text-muted">
                        <span aria-hidden="true" className="mr-2 inline-block h-[7px] w-[7px] rounded-full bg-accent align-[1px] motion-safe:animate-[portrait-breathe_1.4s_ease-in-out_infinite]" />
                        {t('aboutYou.portrait.writingLine', { assistant: assistantName(t, portrait!.assistant) })}
                    </p>
                </div>
            );
            break;
        case portrait?.state === 'failed':
            body = (
                <div>
                    <p className="m-0 mt-2.5 max-w-[58ch] text-[14.5px] leading-[1.55] text-muted">
                        {portrait!.failure ? failureText(t, i18n.language, portrait!.failure, true) : null}
                    </p>
                    {portrait!.failure && (
                        <div className="mt-3.5 empty:hidden">
                            <FailureAction failure={portrait!.failure} onTryNow={() => void tryNow()} />
                        </div>
                    )}
                </div>
            );
            break;
        case shapeOnly:
            // No sentence passed yet: the name and the shape are Isoform's own
            // count, so they show, with the beliefs that would unlock the most.
            body = (
                <>
                    <p className="m-0 mt-3 max-w-[58ch] text-[14.5px] leading-[1.55] text-muted" data-testid="portrait-shape-line">{t('aboutYou.portrait.shapeLine')}</p>
                    {rows.length > 0 && <ConfirmRows rows={rows} writing={portrait!.confirming} onAnswer={(id, thatsMe) => void answer(id, thatsMe)} onUndo={(id) => void undoRow(id)} />}
                    {portrait!.lately && (
                        <div className="mt-4 grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-2.5 border-t border-border-divider pt-3.5 max-[560px]:grid-cols-1 max-[560px]:gap-1">
                            <span className="text-[11px] font-semibold uppercase tracking-[0.07em] text-accent">{t('aboutYou.portrait.lately')}</span>
                            <p className="m-0 text-[14px] leading-[1.55]">
                                {portrait!.lately.parts.map((part, index) => (part.memoryIds?.length ?? 0) > 0
                                    ? <Phrase key={index} part={part} describe={describe} lately={{ thisMonth: portrait!.lately!.thisMonth, usual: portrait!.lately!.usual }} onShowBelief={onShowBelief} onNotQuiteBelief={onNotQuiteBelief} />
                                    : <span key={index}>{part.text}</span>)}
                            </p>
                        </div>
                    )}
                </>
            );
            break;
        default:
            body = (
                <>
                    <div className={cn('transition-opacity duration-300', portrait!.rewriting && 'opacity-55')}>
                    <p className="portrait m-0 mt-3.5 max-w-[62ch] text-ink" data-testid="portrait-paragraph">
                        {sentences.map((sentence, index) => {
                            // A sentence a confirmation brought back draws its underlines in.
                            const back = sentence.changed && (sentence.cause?.confirm?.length ?? 0) > 0;
                            let backIndex = 0;
                            const text = sentence.parts.map((part, partIndex) => (part.memoryIds?.length ?? 0) > 0
                                ? <Phrase key={partIndex} part={part} describe={describe} draw={drawn.get(`${index}-${partIndex}`) ?? (back && part.confirmed ? 120 + 90 * backIndex++ : undefined)} onShowBelief={onShowBelief} onNotQuiteBelief={onNotQuiteBelief} />
                                : <span key={partIndex}>{part.text}</span>);
                            return (
                                <span key={index} data-role={sentence.role} className={cn(sentence.role === 'action' && 'text-muted')}>
                                    {sentence.changed
                                        ? <ins data-testid="portrait-changed" className="rounded-[3px] bg-accent-light no-underline [-webkit-box-decoration-break:clone] [box-decoration-break:clone]">{text}</ins>
                                        : text}
                                    {index < sentences.length - 1 ? ' ' : ''}
                                </span>
                            );
                        })}
                    </p>
                    </div>
                    {why && <p className="answer-line m-0 mt-2.5 max-w-[62ch] text-[13px] leading-[1.45] text-accent" data-testid="portrait-why">{why}</p>}
                    {rows.length > 0 && <ConfirmRows rows={rows} writing={portrait!.confirming} onAnswer={(id, thatsMe) => void answer(id, thatsMe)} onUndo={(id) => void undoRow(id)} />}
                    {portrait!.forgotten && (
                        <p className="m-0 mt-2.5 text-[12.5px] text-copper">{t('aboutYou.portrait.forgotten')}</p>
                    )}
                    <div className="mt-2.5 flex flex-wrap gap-x-4 gap-y-1 text-[11.5px] text-muted">
                        <span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="inline-block w-[18px] border-b-[1.5px] border-solid border-accent" />{t('aboutYou.portrait.legendConfirmed')}</span>
                        <span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="inline-block w-[18px] border-b-[1.5px] border-dotted border-accent" />{t('aboutYou.portrait.legendInferred')}</span>
                    </div>
                    {noting ? (
                        <div className="mt-3 rounded-[10px] bg-panel-warm p-3">
                            <label htmlFor="portrait-not-quite" className="mb-1.5 block text-[12.5px] text-muted">{t('aboutYou.portrait.notQuiteLabel')}</label>
                            <textarea
                                id="portrait-not-quite"
                                rows={2}
                                autoFocus
                                value={note}
                                onChange={(event) => setNote(event.target.value)}
                                placeholder={t('aboutYou.portrait.notQuitePlaceholder')}
                                className="w-full resize-y rounded-lg border border-border bg-panel px-2.5 py-2 text-[13.5px] leading-[1.45] text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus"
                            />
                            <div className="mt-2 flex gap-1.5">
                                <Button size="sm" className={pill} onClick={() => void saveNote()}>{t('aboutYou.portrait.save')}</Button>
                                <Button size="sm" variant="outline" className={pill} onClick={() => { setNoting(false); setNote(''); }}>{t('aboutYou.portrait.cancel')}</Button>
                            </div>
                        </div>
                    ) : (
                        <div className="mt-3.5 flex min-h-7 flex-wrap items-center gap-1.5">
                            {portrait!.rewriting ? (
                                <span className="text-[12.5px] text-copper">{t('aboutYou.portrait.rewriting')}</span>
                            ) : said ? (
                                <span className="answer-line inline-flex items-center gap-1.5 text-[12.5px] text-success">
                                    <Check className="h-3.5 w-3.5" aria-hidden="true" />
                                    <span ref={saidStatus} tabIndex={-1} role="status" className="outline-none">
                                        <span aria-hidden="true">{t('aboutYou.portrait.saidThatsYou')}</span>
                                        <span className="sr-only">{t('aboutYou.portrait.saidAnnounce', { count: saidCount })}</span>
                                    </span>
                                    <span aria-hidden="true" className="text-muted">·</span>
                                    <UndoLink onClick={() => void undo()} />
                                </span>
                            ) : (portrait!.saidAt ?? 0) > 0 ? (
                                <span className="inline-flex items-center gap-1.5 text-[12.5px] text-success">
                                    <Check className="h-3.5 w-3.5" aria-hidden="true" />
                                    {t('aboutYou.portrait.saidThatsYouWhen', { when: answerWhen(t, i18n.language, portrait!.saidAt ?? 0) })}
                                    <span aria-hidden="true" className="text-muted">·</span>
                                    <button type="button" className="cursor-pointer rounded-sm border-0 bg-transparent p-0 font-semibold text-accent hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus" onClick={() => setNoting(true)}>{t('aboutYou.portrait.notQuite')}</button>
                                </span>
                            ) : (
                                <>
                                    <Button size="sm" variant="outline" className={cn(pill, 'text-ink')} onClick={() => void thatsMe()}>{t('aboutYou.portrait.thatsMe')}</Button>
                                    <Button size="sm" variant="outline" className={cn(pill, 'text-ink')} onClick={() => setNoting(true)}>{t('aboutYou.portrait.notQuite')}</Button>
                                </>
                            )}
                            <span className="ml-auto text-[11.5px] text-muted">
                                {t('aboutYou.portrait.meta', {
                                    beliefs: t('aboutYou.portrait.beliefs', { count: portrait!.beliefCount }),
                                    when: updatedWhen(i18n.language, portrait!.generatedAt),
                                })}
                            </span>
                        </div>
                    )}
                    {said && !noting && (
                        <p className="answer-line m-0 mt-1.5 text-[12.5px] text-muted" data-testid="portrait-keeps">{t('aboutYou.portrait.keeps')}</p>
                    )}
                    {portrait!.lately && (
                        <div className="mt-4 grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-2.5 border-t border-border-divider pt-3.5 max-[560px]:grid-cols-1 max-[560px]:gap-1">
                            <span className="text-[11px] font-semibold uppercase tracking-[0.07em] text-accent">{t('aboutYou.portrait.lately')}</span>
                            <p className="m-0 text-[14px] leading-[1.55]">
                                {portrait!.lately.parts.map((part, index) => (part.memoryIds?.length ?? 0) > 0
                                    ? <Phrase key={index} part={part} describe={describe} lately={{ thisMonth: portrait!.lately!.thisMonth, usual: portrait!.lately!.usual }} onShowBelief={onShowBelief} onNotQuiteBelief={onNotQuiteBelief} />
                                    : <span key={index}>{part.text}</span>)}
                            </p>
                        </div>
                    )}
                </>
            );
    }

    return (
        <div className="flex flex-col gap-4">
            <section aria-labelledby="about-you-portrait" data-state={portrait?.state ?? 'withdrawn'} className="rounded-2xl border border-border bg-panel px-[26px] pt-[22px] pb-[18px] max-[560px]:p-4">
                {ready && (pending || portrait?.pending) && (
                    <div role="status" className="mb-3 flex flex-wrap items-center gap-2 text-[12.5px] text-muted">
                        <span>{t('aboutYou.portrait.newVersion')}</span>
                        <button
                            type="button"
                            className="cursor-pointer border-0 bg-transparent p-0 font-semibold text-accent hover:underline"
                            onClick={() => void showPending()}
                        >
                            {t('aboutYou.portrait.showIt')}
                        </button>
                    </div>
                )}
                {named && portrait?.failure && !portrait.rewriting && (
                    <div role="status" className="mb-3 flex flex-wrap items-center gap-2 text-[12.5px] text-copper">
                        <span>{failureText(t, i18n.language, portrait.failure, false)}</span>
                        <FailureAction failure={portrait.failure} onTryNow={() => void tryNow()} link />
                    </div>
                )}
                <div className="flex items-center gap-3.5 max-[560px]:flex-wrap">
                    {portrait && <PortraitShape sides={portrait.state === 'learning' || portrait.state === 'failed' ? null : portrait.sides} />}
                    <div className="min-w-0">
                        <div className="text-[11px] font-semibold uppercase tracking-[0.07em] text-muted">{t('aboutYou.portrait.title')}</div>
                        {title && <h2 id="about-you-portrait" className="m-0 mt-0.5 text-[26px] font-semibold leading-[1.15] tracking-[-0.015em] text-ink max-[560px]:text-[22px]">{title}</h2>}
                    </div>
                    {whyName}
                </div>
                {body}
            </section>
            {named && !working && lines.length > 0 && (
                <section aria-labelledby="about-you-work-with-me" className="rounded-2xl border border-border bg-panel px-[26px] py-[18px] max-[560px]:p-4">
                    <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
                        <h3 id="about-you-work-with-me" className="m-0 text-[17px] font-semibold leading-[1.3] text-ink">{t('aboutYou.portrait.workWithMe.title')}</h3>
                        <span className="text-[12px] text-subtle">{t('aboutYou.portrait.workWithMe.sub')}</span>
                        <Button
                            size="sm"
                            variant="outline"
                            className={cn(pill, 'ml-auto text-ink')}
                            onClick={() => {
                                void copyTextToClipboard(workText);
                                setCopied(true);
                            }}
                        >
                            {copied ? t('aboutYou.portrait.workWithMe.copied') : t('aboutYou.portrait.workWithMe.copy')}
                        </Button>
                    </div>
                    <ul className="m-0 mt-3 list-none p-0">
                        {lines.map((line, index) => (
                            <li key={index} className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 border-t border-border-divider py-[9px] text-[14px] leading-[1.5] text-ink max-[560px]:grid-cols-1 max-[560px]:gap-0.5">
                                <span>{line.text}</span>
                                <span className="whitespace-nowrap text-[11.5px] text-muted">
                                    {line.confirmed
                                        ? t('aboutYou.portrait.workWithMe.confirmed')
                                        : t('aboutYou.portrait.workWithMe.from', { group: t(`aboutYou.portrait.groups.${line.area || 'other'}` as 'aboutYou.portrait.groups.work') })}
                                </span>
                            </li>
                        ))}
                    </ul>
                </section>
            )}
        </div>
    );
}
