import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ResumeObservation } from '../../../wailsjs/go/main/App';
import { computerClock } from '@/lib/brief-time';

export function MainChatObservationPause({ input }: { input: unknown }) {
    const { t, i18n } = useTranslation('chat');
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    if (!input || typeof input !== 'object') return null;
    const { startAt, endAt, resumedUnknown } = input as { startAt?: unknown; endAt?: unknown; resumedUnknown?: unknown };
    if (typeof startAt !== 'number' || !Number.isFinite(new Date(startAt).getTime()) || startAt <= 0) return null;
    const resumed = typeof endAt === 'number' && Number.isFinite(new Date(endAt).getTime()) && endAt > 0;
    const time = computerClock(new Date(startAt), i18n.resolvedLanguage ?? 'en');
    const source = t('mainChat.proactive.sourceFrom', { source: t('mainChat.proactive.source.observing') });
    // A past pause states what happened with its times, never a claim about now (chat-review v1.0.8).
    if (resumed) return <p className="m-0 text-sm text-muted">{t('mainChat.observationPause.pausedFromTo', { start: time, end: computerClock(new Date(endAt), i18n.resolvedLanguage ?? 'en') })} · {source}</p>;
    if (resumedUnknown === true) return <p className="m-0 text-sm text-muted">{t('mainChat.observationPause.pausedAt', { time })} · {source}</p>;
    return (
        <div className="rounded-lg border border-border bg-panel px-4 py-3 text-sm">
            <p className="m-0 font-medium text-ink">{t('mainChat.observationPause.paused', { time })}</p>
            <p className="m-0 mt-1 text-xs text-muted">{source}</p>
            <button type="button" disabled={busy} className="mt-2 text-sm font-medium text-ink underline disabled:opacity-50" onClick={() => {
                setBusy(true);
                setFailed(false);
                void ResumeObservation().catch(() => setFailed(true)).finally(() => setBusy(false));
            }}>{t('mainChat.observationPause.resume')}</button>
            {failed && <p className="m-0 mt-1 text-xs text-muted" role="alert">{t('mainChat.observationPause.failed')}</p>}
        </div>
    );
}
