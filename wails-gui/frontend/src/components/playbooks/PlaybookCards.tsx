import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import type { PlaybookView, SopEntry } from '@/lib/wails-shim-app';
import { cn } from '@/lib/utils';
import { ActivityInlineNotice, type ActivityNotice } from '@/components/activity-inline-notice';
import type { PlaybookListItem } from './playbook-lists';
import { formatClock, formatRunDay, playbookRunWhen } from './playbook-format';

export interface PlaybookCardsProps {
    /** Confirmed playbooks only; the caller filters drafts out. */
    items: PlaybookListItem[];
    locale: string;
    /** Opens the playbook's detail on this page. */
    onOpen: (id: string) => void;
    /** Runs the playbook in place. Omit it and the Run now button is hidden. */
    onRun?: (entry: SopEntry) => void;
    /** Per playbook: running (the button becomes the line) or a failure. */
    runState?: Record<string, PlaybookRunState>;
    /** The schedules didn't load: no when-line rather than an untrue one (W21). */
    whenUnknown?: boolean;
}

export type PlaybookRunState = 'running' | ActivityNotice;

/**
 * "Isoform can do these for you": confirmed playbooks as cards, each with its
 * name, one line on what it does, when it runs and when it last ran. Renders
 * nothing when there are none.
 */
export function PlaybookCards({ items, locale, onOpen, onRun, runState = {}, whenUnknown = false }: PlaybookCardsProps) {
    const { t } = useTranslation('knowledge');
    const headingId = useId();
    if (items.length === 0) return null;
    const now = Date.now();
    return (
        <section aria-labelledby={headingId} className="flex flex-col gap-2.5">
            <div className="flex items-baseline gap-2">
                <h2 id={headingId} className="m-0 text-[15px] font-semibold text-ink">
                    {t('playbooks.sectionTitle')}
                </h2>
                <span className="text-[12.5px] text-muted">{t('playbooks.count', { count: items.length })}</span>
            </div>
            <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
                {items.map(({ entry, plan }) => (
                    <li key={entry.id}>
                        <PlaybookCard
                            entry={entry}
                            plan={plan}
                            locale={locale}
                            now={now}
                            run={runState[entry.id]}
                            whenUnknown={whenUnknown}
                            onOpen={onOpen}
                            onRun={onRun}
                        />
                    </li>
                ))}
            </ul>
        </section>
    );
}

function PlaybookCard({
    entry,
    plan,
    locale,
    now,
    run,
    whenUnknown,
    onOpen,
    onRun,
}: {
    entry: SopEntry;
    plan: PlaybookView | null;
    locale: string;
    now: number;
    run?: PlaybookRunState;
    whenUnknown: boolean;
    onOpen: (id: string) => void;
    onRun?: (entry: SopEntry) => void;
}) {
    const { t } = useTranslation('knowledge');
    const what = entry.description.trim();
    const runWhen = playbookRunWhen(plan);
    const when =
        runWhen.kind === 'onRequest'
            ? t('playbooks.when.onRequest')
            : t(runWhen.kind === 'weekdays' ? 'playbooks.when.weekdays' : 'playbooks.when.daily', {
                  time: formatClock(runWhen.time, locale),
              });
    const lastAt = plan?.lastRun?.at ?? 0;
    let lastRun: string | null = null;
    if (lastAt > 0) {
        const day = formatRunDay(lastAt, now, locale);
        const dayText =
            day.key === 'today' ? t('playbooks.day.today') : day.key === 'yesterday' ? t('playbooks.day.yesterday') : day.text;
        lastRun = t('playbooks.lastRun', { day: dayText });
    }
    return (
        <div className="flex items-center gap-4 rounded-xl border border-border bg-panel p-4">
            <button
                type="button"
                data-playbook-id={entry.id}
                onClick={() => onOpen(entry.id)}
                className={cn(
                    'flex min-w-0 flex-1 flex-col items-start gap-1 rounded-md text-left text-ink',
                    'transition-colors hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40',
                )}
            >
                <span className="text-[14.5px] font-semibold leading-[1.35]">{entry.title}</span>
                {what && <span className="line-clamp-1 text-[13px] text-muted">{what}</span>}
                <span className="flex flex-wrap items-center gap-2 text-xs text-muted">
                    {!whenUnknown && <span>{when}</span>}
                    {lastRun && (
                        <>
                            {!whenUnknown && <span aria-hidden className="h-[3px] w-[3px] rounded-full bg-current opacity-60" />}
                            <span>{lastRun}</span>
                        </>
                    )}
                </span>
            </button>
            {onRun && run === 'running' && (
                <p role="status" className="m-0 max-w-[220px] text-right text-[12.5px] text-muted">{t('playbooks.runNowStarted')}</p>
            )}
            {onRun && run !== 'running' && (
                <div className="flex flex-col items-end gap-1">
                    <Button variant="secondary" size="sm" onClick={() => onRun(entry)}>
                        {t('playbooks.runNow')}
                    </Button>
                    <ActivityInlineNotice notice={run ?? null} />
                </div>
            )}
        </div>
    );
}
