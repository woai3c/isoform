import { useEffect, useState } from 'react';
import type React from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { GetTodayBriefByID, type MobileBriefDocument } from '@/lib/wails-shim-app';
import { EventsOn } from '@/lib/wails-shim-runtime';
import { toggleBriefPlayback, useBriefPlayback } from '@/lib/brief-audio';
import { briefHasWords, briefKind, briefTitle } from '@/lib/brief-title';
import { mainChatBriefRelativeText } from '@/lib/main-chat-brief-relative';

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** Chat's brief card: the brief's title, its opening sentence, Play when it
 *  has a recording with its words, and Read it, which opens the brief in
 *  the card. The brief is read by its id from the same brief service
 *  Today reads. */
export function MainChatBriefCard({ briefId, weeklyRecap = false, display, lead: leadWithCard, onOpenBrief, trailing, meta }: {
    /** The card's ···, at the right of the button row. */
    trailing?: React.ReactNode;
    briefId: string;
    weeklyRecap?: boolean;
    display?: { slot: string; asOf: string; timezone: string };
    /** The first words, read with the card, so it draws whole before the brief loads. */
    lead?: string;
    /** Source details appear between the words and the actions. */
    meta?: (brief: MobileBriefDocument | null) => React.ReactNode;
    /** Read it on a brief whose words aren't here yet opens it where it lives. */
    onOpenBrief?: () => void;
}) {
    const { t, i18n } = useTranslation('chat');
    const { t: tToday } = useTranslation('today');
    const [brief, setBrief] = useState<MobileBriefDocument | null>(null);
    // Why the words are held back, from the read (R40): store_unavailable while a store opens.
    const [wordsWithheld, setWordsWithheld] = useState('');
    const [reading, setReading] = useState(false);
    const [clock, setClock] = useState(() => new Date());
    useEffect(() => {
        const timer = window.setInterval(() => setClock(new Date()), 60_000);
        return () => window.clearInterval(timer);
    }, []);
    const playback = useBriefPlayback(briefId);

    // The card reads its words when it opens, and again when the store opens or a
    // brief is stored: on those events, never on a timer. A read that fails while
    // Isoform is still trying shows nothing; the next event reads again (R40).
    useEffect(() => {
        let live = true;
        const read = () => {
            if (!live) return;
            Promise.resolve()
                .then(() => GetTodayBriefByID(briefId))
                .then((view) => {
                    // The id is the one the card names, never a fallback brief.
                    if (!live || view?.brief?.briefId !== briefId) return;
                    setBrief(view.brief);
                    setWordsWithheld(view.wordsWithheld ?? '');
                })
                .catch(() => {});
        };
        // A new brief id starts empty, and a retry keeps the lines already shown.
        setBrief((previous) => (previous?.briefId === briefId ? previous : null));
        setWordsWithheld('');
        read();
        const offs = [EventsOn('app:ready', read), EventsOn('brief:stored', read)];
        return () => { live = false; offs.forEach((off) => off()); };
    }, [briefId]);
    useEffect(() => setReading(false), [briefId]);

    const language = i18n.resolvedLanguage ?? i18n.language;
    const tr: Translate = (key, options) => tToday(key as never, options as never) as unknown as string;
    const slot = /-morning$/.test(briefId) ? 'morning' : /-evening$/.test(briefId) ? 'evening' : 'on_request';
    const fallbackLabel = weeklyRecap && slot === 'on_request' ? tr('todayBrief.kind.week') : tr(`todayBrief.slot.${slot}`);
    const current = brief?.briefId === briefId ? brief : null;
    const titleSource = display ?? (current ? { slot: current.slot, asOf: current.asOf, timezone: current.timezone, displaySlot: current.displaySlot } : null);
    const title = titleSource ? briefTitle({ ...titleSource, slot: 'on_request' } as Parameters<typeof briefTitle>[0], language, tr) : fallbackLabel;
    const projected = current?.narrative?.map((paragraph) => mainChatBriefRelativeText(current, paragraph, clock, language, (key, options) => t(key as never, options as never) as unknown as string).trim()) ?? [];
    const changedWords = current?.narrative?.some((paragraph, index) => paragraph.text?.trim() !== projected[index]) ?? false;
    const paragraphs = projected.filter(Boolean);
    // Until the brief is read, the words that came with the card stand in.
    // Held: a store still opening withholds the words, so the card waits for its event.
    // Unreadable: the words are missing once nothing is opening (R40).
    const held = !!current && paragraphs.length === 0 && wordsWithheld === 'store_unavailable';
    const unreadable = !!current && paragraphs.length === 0 && !held;
    const lead = current ? (paragraphs[0] ?? '') : (leadWithCard ?? '');
    // Read it opens the brief in the card (§5), not on Today.
    // The week's brief has its own sections beyond the words; it opens on Today.
    const week = current ? briefKind(current) === 'week' : false;
    // A press made while the week's brief loaded still opens it on Today.
    useEffect(() => {
        if (reading && week && onOpenBrief) { setReading(false); onOpenBrief(); }
    }, [reading, week, onOpenBrief]);
    // A recording never plays without its words.
    const playable = !changedWords && briefHasWords(current) && !!current?.audio && current.audio.bytes > 0;
    // Read it and Hear it appear with the words, never on a held or failed card (design, PR #646).
    const canRead = !reading && paragraphs.length > 0;

    return (
        <div className="flex flex-col gap-2" data-testid="main-chat-brief-card" aria-busy={!current}>
            <h3 className="break-words text-[15px] font-semibold leading-snug text-ink">{title}</h3>
            {/* No reserved space: a held or failed card has no body, so the source
                line sits right under the title (design, PR #646). */}
            {reading
                ? paragraphs.map((text, index) => <p key={index} className="whitespace-pre-wrap break-words text-[14px] leading-normal text-ink">{text}</p>)
                : lead && <p data-brief-body className="line-clamp-3 whitespace-pre-wrap break-words text-[14px] leading-normal text-ink">{lead}</p>}
            {/* A failed read says so where the words would be, before the source line. */}
            {unreadable && (
                <p role="alert" className="text-[13px] text-copper" data-brief-unreadable>{t('mainChat.card.wordsUnreadable')}</p>
            )}
            {/* The source line comes from the message, so it renders while the brief loads. */}
            {meta?.(current)}
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
                        {t('mainChat.card.button.hearIt')}
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
                            return setReading(true);
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
