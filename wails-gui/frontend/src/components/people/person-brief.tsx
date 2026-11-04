// Brief me before we talk and Draft a reply, in a person's detail. Both stay
// on Who you know: the brief becomes Today's "Before you talk" lines, and the
// draft is Today's reply draft under the reply owed. Nothing is sent until
// the person presses Send in the draft.
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../ui/button';
import { GetPersonBrief, OpenPersonReplyDraft, CancelTodayReplyDraft, type PersonBrief } from '@/lib/wails-shim-app';
import { TaskThreadPane } from '../tasks/TaskThreadPane';
import { BeforeYouTalkLine, lastTimeText } from '../today/TodayBeforeYouTalkCard';
import { PeopleFailureLine } from './people-failure';
import { type Translate } from './people-format';

function newRunId(): string {
    return typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

/** The button, then the brief in its place: up to three lines, each left
 *  out when Isoform has nothing for it. */
export function PersonBriefBlock({ personId }: { personId: string }) {
    const { t, i18n } = useTranslation('today');
    const { t: tp } = useTranslation('people');
    const tt: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const [state, setState] = useState<'idle' | 'loading' | 'ready' | 'failed'>('idle');
    const [brief, setBrief] = useState<PersonBrief | null>(null);
    const seq = useRef(0);
    useEffect(() => {
        seq.current++;
        setState('idle');
        setBrief(null);
    }, [personId]);
    const run = async () => {
        const mine = ++seq.current;
        setState('loading');
        try {
            const next = await GetPersonBrief(personId);
            if (mine !== seq.current) return;
            setBrief(next);
            setState('ready');
        } catch {
            if (mine === seq.current) setState('failed');
        }
    };
    if (state === 'idle') {
        return (
            <Button size="sm" onClick={() => void run()}>
                {tt('beforeYouTalk.briefMe')}
            </Button>
        );
    }
    if (state === 'failed') return <PeopleFailureLine text={tp('detail.briefFailed')} onRetry={run} />;
    if (state === 'loading') {
        return (
            <div role="status" aria-label={tt('beforeYouTalk.label')} aria-busy="true" className="flex w-full flex-col gap-2 rounded-xl border border-border bg-panel px-[18px] py-4">
                <span className="h-3 w-1/4 animate-pulse rounded bg-surface-active" />
                <span className="h-3 w-3/4 animate-pulse rounded bg-surface-active" />
                <span className="h-3 w-2/3 animate-pulse rounded bg-surface-active" />
            </div>
        );
    }
    const theyAsked = brief?.hasUnsentDraft
        ? brief.theyAsked
            ? tt('beforeYouTalk.withDraft', { question: brief.theyAsked })
            : tt('beforeYouTalk.draftOnly')
        : brief?.theyAsked;
    const lastTime = brief?.lastTime ? lastTimeText(brief, i18n.language, tt) : '';
    const empty = !brief?.youOwe?.trim() && !theyAsked?.trim() && !lastTime.trim();
    return (
        <section aria-label={tt('beforeYouTalk.label')} data-testid="person-before-you-talk" className="flex w-full flex-col gap-3 rounded-xl border border-border bg-panel px-[18px] py-4">
            <span className="text-[11px] font-medium uppercase tracking-wider text-muted">{tt('beforeYouTalk.label')}</span>
            {empty ? (
                <p className="text-[13.5px] text-muted">{tp('detail.briefNothing')}</p>
            ) : (
                <div className="flex flex-col gap-2">
                    <BeforeYouTalkLine label={tt('beforeYouTalk.youOwe')} text={brief?.youOwe} />
                    <BeforeYouTalkLine label={tt('beforeYouTalk.theyAsked')} text={theyAsked} />
                    <BeforeYouTalkLine label={tt('beforeYouTalk.lastTime')} text={lastTime} />
                </div>
            )}
        </section>
    );
}

/** Today's reply draft for one reply owed: "Writing the draft" with Stop,
 *  then the draft with Send and Edit. Closing it cancels a run still going. */
export function PersonReplyDraft({ personId, momentId, onClose }: { personId: string; momentId: string; onClose: () => void }) {
    const { t } = useTranslation('today');
    const { t: tp } = useTranslation('people');
    const [task, setTask] = useState<string | null>(null);
    const [state, setState] = useState<'writing' | 'failed' | 'stopped'>('writing');
    const [stopping, setStopping] = useState(false);
    const [stopFailed, setStopFailed] = useState(false);
    const [attempt, setAttempt] = useState(0);
    const stop = useRef<() => void>(() => {});
    useEffect(() => {
        let active = true;
        let finished = false;
        const runId = newRunId();
        setTask(null);
        setState('writing');
        setStopFailed(false);
        setStopping(false);
        stop.current = () => {
            if (!active || finished) return;
            setStopping(true);
            setStopFailed(false);
            void CancelTodayReplyDraft(runId)
                .then(() => {
                    if (!active || finished) return;
                    active = false;
                    setState('stopped');
                })
                .catch(() => {
                    if (active) setStopFailed(true);
                })
                .finally(() => setStopping(false));
        };
        void OpenPersonReplyDraft(personId, momentId, runId)
            .then((id) => {
                finished = true;
                if (active) setTask(id);
            })
            .catch(() => {
                finished = true;
                if (active) setState('failed');
            });
        return () => {
            if (active && !finished) void CancelTodayReplyDraft(runId).catch(() => {});
            active = false;
        };
    }, [personId, momentId, attempt]);
    const retry = () => setAttempt((n) => n + 1);
    if (task) {
        return (
            <div data-testid="person-reply-draft" className="mt-2 overflow-hidden rounded-xl border border-border bg-background-accent">
                <TaskThreadPane id={task} onClose={onClose} onGone={onClose} telemetryAction="T12" />
            </div>
        );
    }
    if (state === 'failed') return <PeopleFailureLine className="mt-2" text={tp('detail.draftFailed')} onRetry={retry} />;
    if (state === 'stopped') return <PeopleFailureLine className="mt-2 text-muted" text={tp('detail.draftStopped')} onRetry={retry} />;
    return (
        <div data-testid="person-reply-draft" className="mt-2 flex flex-col gap-3 rounded-xl border border-border bg-panel p-4">
            <div role="status" aria-label={t('replyDraft.writing')} aria-busy="true" className="flex flex-col gap-2">
                <span className="text-[12px] text-muted">{t('replyDraft.writing')}</span>
                <span className="block h-3 w-2/3 rounded bg-surface-active" />
                <span className="block h-3 w-full rounded bg-surface-active" />
                <span className="block h-3 w-4/5 rounded bg-surface-active" />
            </div>
            <div className="flex gap-2">
                <Button size="sm" disabled>
                    {t('replyDraft.send')}
                </Button>
                <Button size="sm" variant="secondary" disabled>
                    {t('replyDraft.edit')}
                </Button>
                <Button size="sm" variant="quiet" disabled={stopping} onClick={() => stop.current()}>
                    {t('replyDraft.stop')}
                </Button>
            </div>
            {stopFailed && <PeopleFailureLine text={tp('detail.draftStopFailed')} onRetry={() => stop.current()} />}
        </div>
    );
}
