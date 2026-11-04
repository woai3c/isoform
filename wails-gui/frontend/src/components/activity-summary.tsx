import { useCallback, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Copy, RefreshCw, Loader2, MessageCircle } from 'lucide-react';
import { DigestMarkdown } from './digest-markdown';
import { ActivityAskInline } from './activity-ask-inline';
import type { TodayChatContextRef } from '@/lib/today-chat-navigation';
import { NativeClipboardSetText } from '../../wailsjs/go/main/App';
import { ClipboardSetText } from '../../wailsjs/runtime/runtime';

// The digest card shared by the Activity page's Day and Week views, plus the
// text helpers they use. The standalone Daily/Weekly summary tab lived here
// until the Activity page absorbed it (item 62); the digest card keeps its
// "Chat about this day/week", Copy and Regenerate actions.

// ── Helpers ──

export function cleanSummaryText(summary: string): string {
    return summary
        .split('\n')
        .filter((line) => line.trim() !== 'Anything specific you want to dig into?')
        .join('\n')
        .trim();
}

/** Copy via the cgo NSPasteboard bridge when available. Wails runtime
 *  ClipboardSetText shells out to `pbcopy`; when the app is launched from
 *  Finder the child process inherits no LANG/LC_CTYPE, so UTF-8 bytes get
 *  written to NSPasteboard as MacRoman — Chinese pastes as mojibake. The
 *  bridge is preferred; pbcopy is the non-darwin fallback. */
/** Resolves false when neither clipboard path took the text. */
export async function copyTextToClipboard(text: string): Promise<boolean> {
    try {
        await NativeClipboardSetText(text);
        return true;
    } catch { /* fall through to pbcopy path */ }
    try {
        await ClipboardSetText(text);
        return true;
    } catch {
        return false;
    }
}

/** The digest's first sentence as plain one-line text, for the Week view's
 *  per-day rows. Strips the trailing question line, heading markers and bold
 *  spans so the row never shows raw markdown. */
export function firstDigestSentence(summary: string): string {
    const clean = cleanSummaryText(summary);
    if (!clean) return '';
    const firstLine = clean.split('\n').find((line) => line.trim() !== '')?.trim() ?? '';
    const plain = firstLine.replace(/^#+\s*/, '').replace(/\*\*([^*]+)\*\*/g, '$1');
    const sentenceEnd = plain.search(/[.!?。！？]\s/);
    const sentence = sentenceEnd >= 0 ? plain.slice(0, sentenceEnd + 1) : plain;
    return sentence.length > 160 ? `${sentence.slice(0, 157).trimEnd()}…` : sentence;
}

function formatGeneratedAt(ms: number): string {
    if (!ms) return '';
    return new Date(ms).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

// ── Digest card ──

export function AISummaryCard({ isToday, summary, summaryStatus, cachedLanguage, generating, autoStatus, onGenerate, askContext, scope, summaryCreatedAt, children, emptyActionLabel, emptyText }: {
    isToday?: boolean; summary: string; summaryStatus: string;
    cachedLanguage?: string;
    generating: boolean;
    /** Go's AutoStatus from EnsureDailySummary / EnsureWeeklySummary:
     *  "refreshing" while it writes the summary in the background, "ready",
     *  or "unavailable" (no summary and none being made). */
    autoStatus?: string;
    onGenerate: () => void;
    /** Chat about this day/week, asked in place under the card. */
    askContext?: TodayChatContextRef; scope?: 'daily' | 'weekly';
    isPartial?: boolean; summaryCreatedAt?: number;
    /** Rendered at the bottom of the card (the Day view's numbers line and
     *  Details disclosure live there). */
    children?: ReactNode;
    /** When set, the no-digest state is one quiet button with this label
     *  instead of the explanatory text row. */
    emptyActionLabel?: string;
    /** The sentence beside emptyActionLabel ("This day isn't written yet."). */
    emptyText?: string;
}) {
    const { t, i18n } = useTranslation('activity');
    const [copied, setCopied] = useState(false);
    const [asking, setAsking] = useState(false);
    const nextSummary = cleanSummaryText(summary);
    // Isoform is writing the summary on its own: the cached one stays
    // readable with a quiet line, and nothing asks the user to write it. Its
    // run fires the same generating event a user's click does, so a run Go
    // started never looks like a user's regenerate.
    const autoWriting = autoStatus === 'refreshing';
    const userGenerating = generating && !autoWriting;
    const busy = generating || autoWriting;
    const heldSummary = useRef(nextSummary);
    if (!busy || !nextSummary || !heldSummary.current) heldSummary.current = nextSummary;
    const displaySummary = heldSummary.current;
    const writingLine = busy ? (
        <p className="flex items-center gap-1.5 text-[12px] text-muted">
            <Loader2 className="h-3 w-3 animate-spin" />
            {displaySummary ? t('summary.card.writingNewer') : scope === 'weekly' ? t('summary.card.writingWeek') : t('summary.card.writingDay')}
        </p>
    ) : null;
    const digestLabel = scope === 'weekly' ? (t('summary.card.weeklyDigest') as string) : (t('summary.card.digest') as string);
    // Cached rows from before the language column existed surface as
    // cachedLanguage='' — treat them as English (the practical reality for
    // pre-i18n installs). When the cached row's language differs from the
    // current UI language, show a soft hint plus a one-click Regenerate so
    // users can opt into a localized rewrite without it happening silently.
    const normalizeLang = (raw: string | undefined): 'en' | 'zh-CN' => {
        const s = (raw || '').toLowerCase();
        if (s.startsWith('zh')) return 'zh-CN';
        return 'en';
    };
    const uiLang = normalizeLang(i18n.resolvedLanguage || i18n.language);
    const cachedLang = normalizeLang(cachedLanguage);
    const showLanguageHint = !!displaySummary && cachedLang !== uiLang;
    const languageDisplayName = (lang: 'en' | 'zh-CN') =>
        (lang === 'zh-CN' ? t('summary.languageName.zh-CN') : t('summary.languageName.en')) as string;

    const handleCopy = useCallback(async () => {
        if (!displaySummary) return;
        await copyTextToClipboard(displaySummary);
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
    }, [displaySummary]);

    return (
        <div className="rounded-xl border border-border bg-panel p-6 space-y-4 shadow-[var(--shadow-soft)]">
            {busy && !displaySummary ? (
                <div className="space-y-3">
                    {writingLine}
                    <div className="space-y-2.5 animate-pulse">
                        <div className="h-3.5 bg-border/40 rounded w-5/6" />
                        <div className="h-3.5 bg-border/40 rounded w-full" />
                        <div className="h-3.5 bg-border/40 rounded w-4/5" />
                        <div className="h-3.5 bg-border/40 rounded w-2/3" />
                        <div className="h-3.5 bg-border/40 rounded w-0" />
                        <div className="h-3.5 bg-border/40 rounded w-full" />
                        <div className="h-3.5 bg-border/40 rounded w-3/4" />
                    </div>
                </div>
            ) : displaySummary ? (
                <>
                    {writingLine}
                    <DigestMarkdown text={displaySummary} />
                    <div className="flex items-center gap-2 pt-1 flex-wrap">
                        {askContext && (
                            <SmallButton
                                icon={MessageCircle}
                                label={scope === 'weekly' ? (t('summary.card.chatAboutWeek') as string) : (t('summary.card.chatAboutDay') as string)}
                                onClick={() => setAsking((open) => !open)}
                                disabled={false}
                            />
                        )}
                        {/* The cached summary stays usable while Isoform refreshes it. */}
                        <SmallButton icon={Copy} label={copied ? (t('summary.card.copied') as string) : (t('summary.card.copy') as string)} onClick={handleCopy} />
                        <SmallButton
                            icon={userGenerating ? Loader2 : RefreshCw}
                            label={t('summary.card.writeAgain')}
                            onClick={onGenerate}
                            disabled={busy}
                            spinning={userGenerating}
                        />
                    </div>
                    {asking && askContext && <ActivityAskInline context={askContext} onClose={() => setAsking(false)} />}
                    {!busy && summaryCreatedAt ? (
                        <DigestFreshnessFooter text={t('summary.card.writtenAt', { time: formatGeneratedAt(summaryCreatedAt) })} />
                    ) : null}
                    {showLanguageHint ? (
                        <DigestFreshnessFooter text={t('summary.card.languageHint', { lang: languageDisplayName(cachedLang) })} />
                    ) : null}
                </>
            ) : emptyActionLabel ? (
                <div className="flex items-center justify-between gap-3">
                    {emptyText && <p className="text-[13px] text-muted/80 leading-relaxed">{emptyText}</p>}
                    <button
                        type="button"
                        onClick={onGenerate}
                        disabled={busy}
                        className="inline-flex items-center gap-1.5 h-7 px-2.5 text-[12px] rounded-md text-muted hover:text-ink hover:bg-ink/[0.04] disabled:text-muted/50 disabled:cursor-not-allowed transition-colors"
                    >
                        {userGenerating && <Loader2 className="h-3 w-3 animate-spin" />}
                        {userGenerating ? (t('summary.generating') as string) : emptyActionLabel}
                    </button>
                </div>
            ) : (
                <div className="flex items-center justify-between gap-3">
                    <p className="text-[13px] text-muted/80 leading-relaxed">
                        {isToday ? (t('summary.card.todayUnfolding') as string)
                            : summaryStatus === 'none' ? (scope === 'weekly' ? (t('summary.card.weekNotWritten') as string) : (t('summary.card.noneDay') as string))
                            : (scope === 'weekly' ? (t('summary.card.unavailableWeek') as string) : (t('summary.card.unavailableDay') as string))}
                    </p>
                    <button onClick={onGenerate} disabled={busy}
                        className="shrink-0 flex items-center gap-1.5 h-8 px-3 text-[12px] rounded-md border border-border bg-transparent text-ink hover:bg-ink/[0.04] transition-colors">
                        {userGenerating ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
                        {userGenerating ? (t('summary.generating') as string) : scope === 'weekly' ? (t('summary.card.writeIt') as string) : (t('summary.card.generate', { label: digestLabel }) as string)}
                    </button>
                </div>
            )}
            {children}
        </div>
    );
}

function DigestFreshnessFooter({
    text,
    action,
    disabled,
    onAction,
}: {
    text: string;
    action?: string;
    disabled?: boolean;
    onAction?: () => void;
}) {
    return (
        <div className="flex items-center justify-between gap-3 rounded-md bg-ink/[0.035] px-3 py-2 text-[11px] text-muted/70">
            <span>{text}</span>
            {action && onAction && (
                <button
                    type="button"
                    onClick={onAction}
                    disabled={disabled}
                    className="shrink-0 text-accent hover:text-accent hover:underline disabled:text-muted/40 disabled:no-underline transition-colors"
                >
                    {action}
                </button>
            )}
        </div>
    );
}

function SmallButton({ icon: Icon, label, onClick, disabled, spinning }: {
    icon: React.ComponentType<{ className?: string }>; label: string; onClick: () => void;
    disabled?: boolean; spinning?: boolean;
}) {
    return (
        <button onClick={onClick} disabled={disabled}
            className={`flex items-center gap-1.5 h-7 px-2.5 text-[11px] rounded-md border border-border bg-transparent transition-colors ${disabled ? 'text-muted/50 cursor-not-allowed' : 'text-muted hover:bg-ink/[0.04] hover:text-ink'}`}>
            <Icon className={`h-3 w-3 ${spinning ? 'animate-spin' : ''}`} />{label}
        </button>
    );
}
