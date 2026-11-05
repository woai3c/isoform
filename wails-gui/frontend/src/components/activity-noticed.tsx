import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { ListPlus, Repeat, Undo2 } from 'lucide-react';
import { ActivityInlineNotice, errorNotice, type ActivityNotice } from './activity-inline-notice';
import {
    AddActivityFollowUp,
    EditTodayNoticedSchedule,
    DismissNoticed,
    SaveNoticedRoutine,
    UndoActivityFollowUp,
    UndoDismissNoticed,
    type NoticedRoutineDraft,
    type NoticedSuggestion,
    type MobileBriefActivityTarget,
} from '@/lib/wails-shim-app';
import { GetDayNoticed, usePageQueryRefresh } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { formatTimeFromMs } from './timeline/useCalendarData';
import { pickUpBody, stitchedClock, type PickUpNotice } from './activity-stitched';

// "Isoform noticed" (backlog item 62, step 4): at most three suggestions from
// the day, made with the day's summary and checked by Go on every read. A
// follow-up is added with the same inline field as a moment's Add follow-up;
// Make a routine opens a draft that is saved only on Save. RC 1 adds "Pick up
// where you left off" first when a piece of work is unfinished; it only shows
// the thread, it never acts.

const sectionLabelClass = 'text-[11px] uppercase tracking-[0.08em] font-semibold text-muted/80 mb-2 px-1';
const actionClass =
    'inline-flex items-center gap-1 text-[11px] text-muted hover:text-ink transition-colors disabled:pointer-events-none';
const inputClass =
    'w-full px-3 py-1.5 text-xs rounded-md border border-border bg-panel text-ink placeholder:text-muted focus:outline-none focus:ring-1 focus:ring-accent';

const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;
type WhenMode = 'none' | 'daily' | 'weekdays' | 'weekly';

/** Splits Go's "when" ("", "daily HH:MM", "weekdays HH:MM",
 *  "weekly <day> HH:MM") into the dialog's fields. */
export function parseRoutineWhen(when: string): { mode: WhenMode; day: string; time: string } {
    const fields = when.trim().toLowerCase().split(/\s+/).filter(Boolean);
    const time = fields[fields.length - 1] ?? '';
    if (fields[0] === 'daily' && fields.length === 2) return { mode: 'daily', day: 'mon', time };
    if (fields[0] === 'weekdays' && fields.length === 2) return { mode: 'weekdays', day: 'mon', time };
    if (fields[0] === 'weekly' && fields.length === 3) return { mode: 'weekly', day: fields[1], time };
    return { mode: 'none', day: 'mon', time: '09:00' };
}

export function formatRoutineWhen(mode: WhenMode, day: string, time: string): string {
    switch (mode) {
        case 'daily':
            return `daily ${time}`;
        case 'weekdays':
            return `weekdays ${time}`;
        case 'weekly':
            return `weekly ${day} ${time}`;
        default:
            return '';
    }
}

const lines = (text: string) =>
    text
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean);

function newKey(): string {
    return typeof crypto !== 'undefined' && 'randomUUID' in crypto
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function ActivityNoticed({
    dateStr,
    onOpenMoment,
    focus,
    onFocusConsumed,
    pickUp,
    onPickUpShow,
    onPickUpLater,
}: {
    dateStr: string;
    focus?: MobileBriefActivityTarget | null;
    onFocusConsumed?: (focus: MobileBriefActivityTarget) => void;
    /** Opens the moment a suggestion points at in "What you worked on". */
    onOpenMoment: (momentId: string) => void;
    /** The unfinished piece of work to pick up, if any. */
    pickUp?: PickUpNotice | null;
    onPickUpShow?: (notice: PickUpNotice) => void;
    onPickUpLater?: (notice: PickUpNotice) => void;
}) {
    const { t, i18n } = useTranslation('activity');
    const [items, setItems] = useState<NoticedSuggestion[]>(() => pageQueryCache.peek('GetDayNoticed', [dateStr]) ?? []);
    const [followUpFor, setFollowUpFor] = useState<string | null>(null);
    const [followUpDraft, setFollowUpDraft] = useState('');
    const [followUpFailed, setFollowUpFailed] = useState(false);
    const [notice, setNotice] = useState<ActivityNotice | null>(null);
    const followUpKey = useRef('');
    const [routineFor, setRoutineFor] = useState<NoticedSuggestion | null>(null);
    const [editTarget, setEditTarget] = useState<MobileBriefActivityTarget | null>(null);
    const [busy, setBusy] = useState(false);
    const loadID = useRef(0);
    const [loadedDate, setLoadedDate] = useState('');
    const consumedFocus = useRef<MobileBriefActivityTarget | null>(null);

    const load = useCallback(() => {
        const request = ++loadID.current;
        GetDayNoticed(dateStr)
            .then((rows) => {
                if (request !== loadID.current) return;
                setItems(rows ?? []);
                setLoadedDate(dateStr);
            })
            .catch(() => {
                if (request !== loadID.current) return;
                setItems([]);
                setLoadedDate(dateStr);
            });
    }, [dateStr]);

    // The user's own writes re-read the day even while the cache is fresh.
    const reload = useCallback(() => {
        pageQueryCache.invalidate(['GetDayNoticed'], true, [dateStr]);
        load();
    }, [load, dateStr]);

    useEffect(() => {
        // A warm cache repaints the new day at once; a cold one clears the
        // previous day's rows while its read is out.
        setItems(pageQueryCache.peek('GetDayNoticed', [dateStr]) ?? []);
        setFollowUpFor(null);
        setRoutineFor(null);
        setEditTarget(null);
        load();
        return () => {
            ++loadID.current;
        };
    }, [load]);
    usePageQueryRefresh(['GetDayNoticed'], load, true, (args) => args[0] === dateStr);

    const startFollowUp = (item: NoticedSuggestion) => {
        setEditTarget(null);
        setFollowUpFor(item.id);
        setFollowUpDraft(item.text);
        setFollowUpFailed(false);
        followUpKey.current = newKey();
    };

    useEffect(() => {
        if (!focus || focus.date !== dateStr || loadedDate !== dateStr || consumedFocus.current === focus) return;
        const item = items.find((row) => row.id === focus.suggestionId && row.kind === focus.action);
        // A vanished suggestion still leaves the user on the requested day.
        consumedFocus.current = focus;
        onFocusConsumed?.(focus);
        if (!item) return;
        setEditTarget(focus.schedule ? focus : null);
        if (focus.action === 'follow_up') {
            setRoutineFor(null);
            setFollowUpFor(item.id);
            setFollowUpDraft(item.text);
            followUpKey.current = newKey();
        } else {
            setFollowUpFor(null);
            setRoutineFor(item);
        }
    }, [focus, dateStr, loadedDate, items, onFocusConsumed]);

    const addFollowUp = async (item: NoticedSuggestion) => {
        const text = followUpDraft.trim();
        if (!text || busy) return;
        setBusy(true);
        setFollowUpFailed(false);
        try {
            if (editTarget?.schedule && editTarget.suggestionId === item.id) {
                await EditTodayNoticedSchedule(editTarget, editTarget.schedule, text, null);
                setFollowUpFor(null);
                setEditTarget(null);
                reload();
                return;
            }
            const added = await AddActivityFollowUp(item.momentId, text, followUpKey.current);
            setFollowUpFor(null);
            reload();
            const undo = () => {
                UndoActivityFollowUp(added.openLoopId, added.eventId)
                    .then(() => {
                        reload();
                        setNotice({ tone: 'done', text: t('rowActions.followUpRemovedToast') });
                    })
                    .catch(() => setNotice(errorNotice(t('rowActions.undoFailed'), undo)));
            };
            setNotice({ tone: 'done', text: t('rowActions.followUpAddedToast'), actionLabel: t('rowActions.undo'), onAction: undo });
        } catch {
            setFollowUpFailed(true);
        } finally {
            setBusy(false);
        }
    };

    const notNow = async (item: NoticedSuggestion) => {
        if (busy) return;
        setBusy(true);
        try {
            await DismissNoticed(dateStr, item.id);
            setItems((current) => current.filter((i) => i.id !== item.id));
            const undo = () => {
                UndoDismissNoticed(dateStr, item.id)
                    .then(() => {
                        setNotice(null);
                        reload();
                    })
                    .catch(() => setNotice(errorNotice(t('rowActions.undoFailed'), undo)));
            };
            setNotice({ tone: 'done', text: t('noticed.notNowToast'), actionLabel: t('rowActions.undo'), onAction: undo });
        } catch {
            setNotice(errorNotice(t('noticed.notNowFailedLine'), () => void notNow(item)));
        } finally {
            setBusy(false);
        }
    };

    const keys = (onEnter: () => void) => (e: KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
            e.preventDefault();
            onEnter();
        } else if (e.key === 'Escape') {
            e.preventDefault();
            setFollowUpFor(null);
        }
    };

    if (items.length === 0 && !pickUp) return null;
    return (
        <section>
            <h3 className={sectionLabelClass}>{t('noticed.title')}</h3>
            <ul className="divide-y divide-border/60 rounded-xl border border-border bg-panel overflow-hidden">
                {pickUp && (
                    <li className="px-4 py-3 space-y-1.5" data-testid="noticed-pick-up">
                        <p className="text-[13px] font-medium text-ink">{t('stitched.pickUp.title', { title: pickUp.title })}</p>
                        <p className="text-[12px] text-muted">{pickUpBody(t, pickUp, i18n.language)}</p>
                        {pickUp.momentId && (
                            <button
                                type="button"
                                onClick={() => onOpenMoment(pickUp.momentId)}
                                className="block max-w-full truncate text-left text-[11px] text-muted/80 hover:text-ink transition-colors"
                            >
                                {t('stitched.pickUp.from', { time: stitchedClock(pickUp.fromMs, i18n.language) })}
                            </button>
                        )}
                        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 pt-1">
                            <button type="button" onClick={() => onPickUpShow?.(pickUp)} className={actionClass}>
                                <Undo2 className="h-3 w-3" />
                                {t('stitched.pickUp.show')}
                            </button>
                            <button type="button" onClick={() => onPickUpLater?.(pickUp)} className={actionClass}>
                                {t('stitched.pickUp.later')}
                            </button>
                        </div>
                    </li>
                )}
                {items.map((item) => (
                    <li key={item.id} className="px-4 py-3 space-y-1.5">
                        <p className="text-[13px] font-medium text-ink">{item.text}</p>
                        {item.why && <p className="text-[12px] text-muted">{item.why}</p>}
                        <button
                            type="button"
                            onClick={() => onOpenMoment(item.momentId)}
                            className="block max-w-full truncate text-left text-[11px] text-muted/80 hover:text-ink transition-colors"
                        >
                            {t('noticed.from', { time: formatTimeFromMs(item.momentStartMs), title: item.momentTitle })}
                        </button>
                        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 pt-1">
                            {item.kind === 'follow_up' ? (
                                <button type="button" onClick={() => startFollowUp(item)} disabled={busy} className={actionClass}>
                                    <ListPlus className="h-3 w-3" />
                                    {t('noticed.addFollowUp')}
                                </button>
                            ) : (
                                <button type="button" onClick={() => { setEditTarget(null); setRoutineFor(item); }} disabled={busy} className={actionClass}>
                                    <Repeat className="h-3 w-3" />
                                    {t('noticed.makeRoutine')}
                                </button>
                            )}
                            <button type="button" onClick={() => void notNow(item)} disabled={busy} className={actionClass}>
                                {t('noticed.notNow')}
                            </button>
                        </div>
                        {followUpFor === item.id && (
                            <div className="flex flex-wrap items-center gap-2 pt-1">
                                <input
                                    type="text"
                                    autoFocus
                                    aria-label={t('rowActions.followUpLabel')}
                                    className={inputClass}
                                    value={followUpDraft}
                                    onChange={(e) => setFollowUpDraft(e.target.value)}
                                    onKeyDown={keys(() => void addFollowUp(item))}
                                />
                                <Button size="sm" onClick={() => void addFollowUp(item)} disabled={busy || !followUpDraft.trim()}>
                                    {t(editTarget?.schedule ? 'rowActions.save' : 'rowActions.add')}
                                </Button>
                                <Button size="sm" variant="ghost" onClick={() => setFollowUpFor(null)}>
                                    {t('activityCard.cancel')}
                                </Button>
                                {followUpFailed && <p role="alert" className="basis-full text-[12px] text-copper">{t('noticed.followUpFailedInline')}</p>}
                            </div>
                        )}
                    </li>
                ))}
            </ul>
            <ActivityInlineNotice notice={notice} className="px-1 pt-1.5" />
            {routineFor && (
                <RoutineDraftDialog
                    dateStr={dateStr}
                    item={routineFor}
                    editTarget={editTarget}
                    onClose={() => { setRoutineFor(null); setEditTarget(null); }}
                    onSaved={(text) => {
                        setNotice(text ? { tone: 'done', text } : null);
                        setRoutineFor(null);
                        setEditTarget(null);
                        reload();
                    }}
                />
            )}
        </section>
    );
}

function RoutineDraftDialog({
    dateStr,
    item,
    editTarget,
    onClose,
    onSaved,
}: {
    dateStr: string;
    item: NoticedSuggestion;
    editTarget?: MobileBriefActivityTarget | null;
    onClose: () => void;
    onSaved: (text?: string) => void;
}) {
    const { t } = useTranslation('activity');
    const draft: NoticedRoutineDraft = item.routine ?? { name: item.text, when: '', steps: [], will: [], wont: [] };
    const initialWhen = parseRoutineWhen(draft.when);
    const [name, setName] = useState(draft.name || item.text);
    const [mode, setMode] = useState<WhenMode>(initialWhen.mode);
    const [day, setDay] = useState(initialWhen.day);
    const [time, setTime] = useState(initialWhen.time || '09:00');
    const [steps, setSteps] = useState(draft.steps.join('\n'));
    const [will, setWill] = useState(draft.will.join('\n'));
    const [wont, setWont] = useState(draft.wont.join('\n'));
    const [saving, setSaving] = useState(false);
    const [saveFailed, setSaveFailed] = useState(false);

    const missing = !name.trim() ? t('noticed.routine.needsName') : lines(steps).length === 0 ? t('noticed.routine.needsSteps') : '';

    const save = async () => {
        if (missing || saving) return;
        setSaving(true);
        setSaveFailed(false);
        let savedText: string | undefined;
        try {
            const nextDraft = {
                name: name.trim(),
                when: formatRoutineWhen(mode, day, time),
                steps: lines(steps),
                will: lines(will),
                wont: lines(wont),
            };
            if (editTarget?.schedule) {
                await EditTodayNoticedSchedule(editTarget, editTarget.schedule, '', nextDraft);
            } else {
                const saved = await SaveNoticedRoutine(dateStr, item.id, nextDraft);
                savedText = saved.home === 'schedule' ? t('noticed.routine.savedSchedule') : t('noticed.routine.savedKnowhow');
            }
            onSaved(savedText);
        } catch {
            setSaveFailed(true);
        } finally {
            setSaving(false);
        }
    };

    const field = 'block space-y-1';
    const label = 'text-[11px] text-muted';
    return (
        <Dialog open onOpenChange={(open) => !open && onClose()}>
            <DialogContent className="max-w-lg">
                <DialogHeader>
                    <DialogTitle>{t('noticed.routine.title')}</DialogTitle>
                    <DialogDescription>{t(editTarget?.schedule ? 'noticed.routine.editDescription' : 'noticed.routine.description')}</DialogDescription>
                </DialogHeader>
                <div className="space-y-3">
                    <label className={field}>
                        <span className={label}>{t('noticed.routine.name')}</span>
                        <input type="text" className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
                    </label>
                    <div className="grid grid-cols-[1fr_auto_auto] gap-2 items-end">
                        <label className={field}>
                            <span className={label}>{t('noticed.routine.when')}</span>
                            <select className={inputClass} value={mode} onChange={(e) => setMode(e.target.value as WhenMode)}>
                                <option value="none">{t(editTarget?.schedule ? 'noticed.routine.whenManual' : 'noticed.routine.whenNone')}</option>
                                <option value="daily">{t('noticed.routine.whenDaily')}</option>
                                <option value="weekdays">{t('noticed.routine.whenWeekdays')}</option>
                                <option value="weekly">{t('noticed.routine.whenWeekly')}</option>
                            </select>
                        </label>
                        {mode === 'weekly' && (
                            <label className={field}>
                                <span className={label}>{t('noticed.routine.day')}</span>
                                <select className={inputClass} value={day} onChange={(e) => setDay(e.target.value)}>
                                    {WEEKDAYS.map((d) => (
                                        <option key={d} value={d}>
                                            {t(`noticed.weekday.${d}`)}
                                        </option>
                                    ))}
                                </select>
                            </label>
                        )}
                        {mode !== 'none' && (
                            <label className={field}>
                                <span className={label}>{t('noticed.routine.time')}</span>
                                <input type="time" className={inputClass} value={time} onChange={(e) => setTime(e.target.value)} />
                            </label>
                        )}
                    </div>
                    <label className={field}>
                        <span className={label}>{t('noticed.routine.steps')}</span>
                        <textarea className={inputClass} rows={4} value={steps} onChange={(e) => setSteps(e.target.value)} />
                    </label>
                    <label className={field}>
                        <span className={label}>{t('noticed.routine.will')}</span>
                        <textarea className={inputClass} rows={2} value={will} onChange={(e) => setWill(e.target.value)} />
                    </label>
                    <label className={field}>
                        <span className={label}>{t('noticed.routine.wont')}</span>
                        <textarea className={inputClass} rows={2} value={wont} onChange={(e) => setWont(e.target.value)} />
                    </label>
                    {missing && <p className="text-[11px] text-muted">{missing}</p>}
                    {saveFailed && <ActivityInlineNotice notice={errorNotice(t('noticed.routine.failed'), () => void save())} />}
                </div>
                <div className="flex justify-end gap-2 pt-2">
                    <Button size="sm" variant="ghost" onClick={onClose}>
                        {t('noticed.routine.cancel')}
                    </Button>
                    <Button size="sm" onClick={() => void save()} disabled={!!missing || saving}>
                        {t('noticed.routine.save')}
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
}
