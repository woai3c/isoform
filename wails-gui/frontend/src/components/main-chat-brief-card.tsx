import { useEffect, useState } from 'react';
import type React from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { GetTodayBriefByID, type MobileBriefDocument } from '@/lib/wails-shim-app';
import { toggleBriefPlayback, useBriefPlayback } from '@/lib/brief-audio';
import { briefHasWords, briefKind, briefTitle } from '@/lib/brief-title';
import { mainChatBriefRelativeText } from '@/lib/main-chat-brief-relative';

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** Chat's brief card: the brief's title, its opening sentence, Play when it
 *  has a recording with its words, and Read it, which opens the brief in
 *  the card. The brief is read by its id from the same brief service
 *  Today reads. */
export function MainChatBriefCard({ briefId, weeklyRecap = false, display, onOpenBrief, trailing, meta }: {
    /** The card's ···, at the right of the button row. */
    trailing?: React.ReactNode;
    briefId: string;
    weeklyRecap?: boolean;
    display?: { slot: string; asOf: string; timezone: string };
    /** Source details appear between the words and the actions. */
    meta?: (brief: MobileBriefDocument | null) => React.ReactNode;
    /** Read it on a brief whose words aren't here yet opens it where it lives. */
    onOpenBrief?: () => void;
}) {
    const { t, i18n } = useTranslation('chat');
    const { t: tToday } = useTranslation('today');
    const [brief, setBrief] = useState<MobileBriefDocument | null>(null);
    const [failed, setFailed] = useState(false);
    const [attempt, setAttempt] = useState(0);
    const [reading, setReading] = useState(false);
    const [clock, setClock] = useState(() => new Date());
    useEffect(() => {
        const timer = window.setInterval(() => setClock(new Date()), 60_000);
        return () => window.clearInterval(timer);
    }, []);
    const playback = useBriefPlayback(briefId);

    useEffect(() => {
        let live = true;
        const timeout = window.setTimeout(() => { if (live) setFailed(true); }, 10000);
        // A retry keeps the lines already on the card (chat-review v1.0.10 H5).
        setBrief((previous) => (previous?.briefId === briefId ? previous : null));
        setFailed(false);
        Promise.resolve()
            .then(() => GetTodayBriefByID(briefId))
            .then((view) => {
                // The id is the one the card names, never a fallback brief.
                if (!live) return;
                window.clearTimeout(timeout);
                if (view?.brief?.briefId === briefId) { setBrief(view.brief); setFailed(false); }
                else setFailed(true);
            })
            .catch(() => { if (live) { window.clearTimeout(timeout); setFailed(true); } });
        return () => { live = false; window.clearTimeout(timeout); };
    }, [briefId, attempt]);
    useEffect(() => setReading(false), [briefId]);

    const language = i18n.resolvedLanguage ?? i18n.language;
    const tr: Translate = (key, options) => tToday(key as never, options as never) as unknown as string;
    const slot = /-morning$/.test(briefId) ? 'morning' : /-evening$/.test(briefId) ? 'evening' : 'on_request';
    const fallbackLabel = weeklyRecap && slot === 'on_request' ? tr('todayBrief.kind.week') : tr(`todayBrief.slot.${slot}`);
    const current = brief?.briefId === briefId ? brief : null;
    const titleSource = display ?? (current ? { slot: current.slot, asOf: current.asOf, timezone: current.timezone, displaySlot: current.displaySlot } : null);
    const title = titleSource ? briefTitle(titleSource as Parameters<typeof briefTitle>[0], language, tr) : fallbackLabel;
    const projected = current?.narrative?.map((paragraph) => mainChatBriefRelativeText(current, paragraph, clock, language, (key, options) => t(key as never, options as never) as unknown as string).trim()) ?? [];
    const changedWords = current?.narrative?.some((paragraph, index) => paragraph.text?.trim() !== projected[index]) ?? false;
    const paragraphs = projected.filter(Boolean);
    const lead = paragraphs[0] ?? '';
    // Read it opens the brief in the card (§5), not on Today.
    // The week's brief has its own sections beyond the words; it opens on Today.
    const week = current ? briefKind(current) === 'week' : false;
    // A press made while the week's brief loaded still opens it on Today.
    useEffect(() => {
        if (reading && week && onOpenBrief) { setReading(false); onOpenBrief(); }
    }, [reading, week, onOpenBrief]);
    // A recording never plays without its words.
    const playable = !failed && !changedWords && briefHasWords(current) && !!current?.audio && current.audio.bytes > 0;
    const canRead = !failed && !reading && (!current || paragraphs.length > 0 || !!onOpenBrief);
    // Every brief card has Read it or the failure line; one with nothing to open is not shown (H7).
    if (current && !failed && paragraphs.length === 0 && !onOpenBrief) return null;

    return (
        <div className="flex flex-col gap-2" data-testid="main-chat-brief-card" aria-busy={!current && !failed}>
            <h3 className="break-words text-[15px] font-semibold leading-snug text-ink">{title}</h3>
            {reading
                ? paragraphs.map((text, index) => <p key={index} className="whitespace-pre-wrap break-words text-[14px] leading-normal text-ink">{text}</p>)
                : lead && <p className="line-clamp-3 whitespace-pre-wrap break-words text-[14px] leading-normal text-ink">{lead}</p>}
            {(current || failed) && meta?.(current)}
            {failed && (
                // A brief that can't open says so in copper; Try again is the accent and Read it hides (H5).
                <p role="alert" className="text-[13px] text-copper" data-brief-open-failed>
                    {t('mainChat.view.briefOpenFailed')}{' '}
                    <button type="button" className="font-medium text-accent hover:underline focus-visible:underline focus-visible:outline-none" onClick={() => setAttempt((value) => value + 1)}>
                        {t('mainChat.view.retry')}
                    </button>
                </p>
            )}
            {(playable || canRead || trailing) && <div className="flex flex-wrap items-center gap-2">
                {playable && (
                    <Button
                        size="sm"
                        variant="default"
                        className="text-[12.5px]"
                        data-proactive-action="play"
                        aria-pressed={playback === 'playing'}
                        onClick={() => void toggleBriefPlayback(briefId).catch(() => {})}
                    >
                        {t('mainChat.card.button.play')}
                    </Button>
                )}
                {canRead && (
                    <Button
                        size="sm"
                        variant={playable ? 'quiet' : 'default'}
                        className="text-[12.5px]"
                        data-proactive-action="read_it"
                        aria-expanded={reading}
                        onClick={() => {
                            if (current) return paragraphs.length > 0 && !(week && onOpenBrief) ? setReading(true) : onOpenBrief?.();
                            return failed && onOpenBrief ? onOpenBrief() : setReading(true);
                        }}
                    >
                        {t('mainChat.card.button.readIt')}
                    </Button>
                )}
                {trailing && <span className="ml-auto">{trailing}</span>}
            </div>}
        </div>
    );
}
