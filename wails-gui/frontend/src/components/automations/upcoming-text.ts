import type { TFunction } from 'i18next';
import type { UpcomingItem } from '@/lib/wails-shim-app';
import { cronToHumanReadable, type ScheduleTextLabels } from './schedule-utils';
import { describeBriefTime } from '@/components/today/TodayStatus';

type AppT = TFunction<'app'>;
type TimeT = (key: string, options?: Record<string, unknown>) => string;

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

/** The row's name: the user's own for a schedule, the page's for the brief. */
export function upcomingName(item: UpcomingItem, t: AppT): string {
    switch (item.kind) {
        case 'brief':
            return item.slot === 'evening'
                ? t('automationsPage.names.briefEvening')
                : t('automationsPage.names.briefMorning');
        default:
            return item.name || t('automations.untitled');
    }
}

/** One line on what the row does. */
export function upcomingSummary(item: UpcomingItem, t: AppT): string {
    switch (item.kind) {
        case 'brief':
            return t('automationsPage.summaries.brief');
        default:
            return item.summary;
    }
}

function scheduleConfigValue(item: Pick<UpcomingItem, "scheduleConfig">, key: string): unknown {
    const config = item.scheduleConfig;
    if (!config || typeof config !== 'object') return undefined;
    return (config as Record<string, unknown>)[key];
}

function describeInterval(ms: number, t: AppT): string {
    if (ms >= DAY_MS && ms % DAY_MS === 0) return t('automationsPage.when.everyDays', { count: ms / DAY_MS });
    if (ms >= HOUR_MS && ms % HOUR_MS === 0) return t('automationsPage.when.everyHours', { count: ms / HOUR_MS });
    return t('automationsPage.when.everyMinutes', { count: Math.max(1, Math.round(ms / MINUTE_MS)) });
}

function describeMillis(ms: number, now: Date, timeT: TimeT): string {
    return describeBriefTime(new Date(ms).toISOString(), now, timeT) ?? '';
}

/** When the row runs, in words: "Daily 07:30", "Every day at 18:00". */
export function upcomingWhen(
    item: Pick<UpcomingItem, "kind" | "time" | "scheduleKind" | "scheduleConfig">,
    now: Date,
    t: AppT,
    timeT: TimeT,
    labels: Partial<ScheduleTextLabels>,
): string {
    if (item.kind === 'brief') return t('automationsPage.when.daily', { time: item.time });
    switch (item.scheduleKind) {
        case 'cron': {
            const expr = scheduleConfigValue(item, 'expr');
            return typeof expr === 'string' ? cronToHumanReadable(expr, labels) : '';
        }
        case 'every': {
            const every = scheduleConfigValue(item, 'every_ms');
            return typeof every === 'number' && every > 0 ? describeInterval(every, t) : '';
        }
        case 'at': {
            const at = scheduleConfigValue(item, 'at_ms');
            return typeof at === 'number' && at > 0
                ? t('automationsPage.when.once', { when: describeMillis(at, now, timeT) })
                : '';
        }
        case 'manual':
            return t('automationsPage.when.whenAsked');
        default:
            return '';
    }
}

/** The next run, or why there is none. */
export function upcomingNextLabel(item: UpcomingItem, now: Date, t: AppT, timeT: TimeT): string {
    if (item.nextRunAt > 0) return t('automationsPage.next', { when: describeMillis(item.nextRunAt, now, timeT) });
    if (!item.enabled) {
        return item.scheduleKind === 'at' && item.lastRunAt > 0
            ? t('automationsPage.finished')
            : t('automationsPage.paused');
    }
    if (item.scheduleKind === 'manual') return t('automationsPage.when.whenAsked');
    return '';
}
