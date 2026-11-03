export type SchedulePreset = 'none' | 'hourly' | 'daily' | 'weekdays' | 'weekly' | 'custom';

const WEEKDAY_ORDER = ['1', '2', '3', '4', '5', '6', '0'];

const WEEKDAY_LABELS: Record<string, string> = {
    '1': 'Mon', '2': 'Tue', '3': 'Wed', '4': 'Thu', '5': 'Fri', '6': 'Sat', '0': 'Sun',
};

export const WEEKDAY_TOGGLES = WEEKDAY_ORDER.map((value) => ({
    value,
    short: WEEKDAY_LABELS[value],
}));

export interface ScheduleTextLabels {
    runOnDemand: string;
    everyHourAt: string;
    daily: string;
    weekdays: string;
    weekdayLabels: Record<string, string>;
}

type TranslationFn = (key: any, options?: any) => string;

const DEFAULT_SCHEDULE_TEXT_LABELS: ScheduleTextLabels = {
    runOnDemand: 'Run on demand',
    everyHourAt: 'Every hour at :{{minute}}',
    daily: 'Daily {{time}}',
    weekdays: 'Weekdays {{time}}',
    weekdayLabels: WEEKDAY_LABELS,
};

export function getWeekdayToggles(labels?: Partial<ScheduleTextLabels>) {
    const weekdayLabels = labels?.weekdayLabels ?? WEEKDAY_LABELS;
    return WEEKDAY_ORDER.map((value) => ({
        value,
        short: weekdayLabels[value] ?? WEEKDAY_LABELS[value],
    }));
}

export function createScheduleTextLabels(t: TranslationFn): ScheduleTextLabels {
    return {
        runOnDemand: t('automations.runOnDemand'),
        everyHourAt: t('automations.scheduleText.everyHourAt'),
        daily: t('automations.scheduleText.daily'),
        weekdays: t('automations.scheduleText.weekdays'),
        weekdayLabels: {
            '1': t('automations.weekdaysShort.1'),
            '2': t('automations.weekdaysShort.2'),
            '3': t('automations.weekdaysShort.3'),
            '4': t('automations.weekdaysShort.4'),
            '5': t('automations.weekdaysShort.5'),
            '6': t('automations.weekdaysShort.6'),
            '0': t('automations.weekdaysShort.0'),
        },
    };
}

function formatLabel(template: string, values: Record<string, string>): string {
    return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) => values[key] ?? '');
}

function resolveLabels(labels?: Partial<ScheduleTextLabels>): ScheduleTextLabels {
    return {
        ...DEFAULT_SCHEDULE_TEXT_LABELS,
        ...labels,
        weekdayLabels: {
            ...WEEKDAY_LABELS,
            ...(labels?.weekdayLabels ?? {}),
        },
    };
}

function weekdaysSummary(selected: Set<string>, labels: ScheduleTextLabels): string {
    if (selected.size === 0) return '';
    return WEEKDAY_ORDER.filter(d => selected.has(d)).map(d => labels.weekdayLabels[d]).join(', ');
}

export function weekdaysToCron(selected: Set<string>): string {
    if (selected.size === 0) return '1';
    return WEEKDAY_ORDER.filter(d => selected.has(d)).join(',');
}

export function getPresetSummary(
    preset: SchedulePreset,
    hour: string,
    minute: string,
    weekdays: Set<string>,
    labels?: Partial<ScheduleTextLabels>,
): string {
    const textLabels = resolveLabels(labels);
    const hh = hour.padStart(2, '0');
    const mm = minute.padStart(2, '0');
    const timeStr = `${hh}:${mm}`;
    switch (preset) {
        case 'none': return textLabels.runOnDemand;
        case 'hourly': return formatLabel(textLabels.everyHourAt, { minute: mm });
        case 'daily': return formatLabel(textLabels.daily, { time: timeStr });
        case 'weekdays': return formatLabel(textLabels.weekdays, { time: timeStr });
        case 'weekly': return `${weekdaysSummary(weekdays, textLabels)} ${timeStr}`;
        default: return '';
    }
}

export function parseCronToPreset(cronExpr: string): {
    preset: SchedulePreset;
    hour: string;
    minute: string;
    weekdays: Set<string>;
    customCron: string;
} {
    const fallback = { preset: 'custom' as const, hour: '9', minute: '0', weekdays: new Set(['1']), customCron: cronExpr };
    const parts = cronExpr.trim().split(/\s+/);
    if (parts.length !== 5) return fallback;

    const [min, hr, day, month, dow] = parts;
    if (day !== '*' || month !== '*') return fallback;

    if (hr === '*' && dow === '*') {
        return { preset: 'hourly', hour: '0', minute: min, weekdays: new Set(['1']), customCron: '' };
    }
    if (dow === '*') {
        return { preset: 'daily', hour: hr, minute: min, weekdays: new Set(['1']), customCron: '' };
    }
    if (dow === '1-5') {
        return { preset: 'weekdays', hour: hr, minute: min, weekdays: new Set(['1']), customCron: '' };
    }
    const days = dow.split(',');
    if (days.every(d => /^[0-6]$/.test(d))) {
        return { preset: 'weekly', hour: hr, minute: min, weekdays: new Set(days), customCron: '' };
    }
    return fallback;
}

/** Convert a raw cron expression to a human-readable string. */
export function cronToHumanReadable(cronExpr: string, labels?: Partial<ScheduleTextLabels>): string {
    if (!cronExpr) return '';
    const parsed = parseCronToPreset(cronExpr);
    if (parsed.preset === 'custom') return cronExpr;
    return getPresetSummary(parsed.preset, parsed.hour, parsed.minute, parsed.weekdays, labels);
}
