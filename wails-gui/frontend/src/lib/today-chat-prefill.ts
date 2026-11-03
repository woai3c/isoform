import type { TFunction } from 'i18next';
import type { TodayChatPrefill } from './today-chat-navigation';

/** These facts have already passed the current read gates in Go. Only
 * locale-specific phrasing and calendar formatting belong in the client. */
export function formatTodayChatPrefill(
    item: TodayChatPrefill,
    t: TFunction<'today'>,
    language: string,
    now = new Date(),
): string {
    const title = item.title;
    const validDate = (timestamp?: number): Date | null => {
        if (!timestamp || !Number.isFinite(timestamp)) return null;
        const date = new Date(timestamp);
        return Number.isNaN(date.getTime()) ? null : date;
    };
    const day = (date: Date): string => {
        if (date.toDateString() === now.toDateString()) return t('draft.day.today');
        const yesterday = new Date(now);
        yesterday.setDate(yesterday.getDate() - 1);
        if (date.toDateString() === yesterday.toDateString()) return t('draft.day.yesterday');
        const recent = new Date(now);
        recent.setDate(recent.getDate() - 6);
        return new Intl.DateTimeFormat(language, date >= recent && date <= now
            ? { weekday: 'long' }
            : { month: 'long', day: 'numeric', ...(date.getFullYear() !== now.getFullYear() ? { year: 'numeric' as const } : {}) }).format(date);
    };
    const occurredAt = validDate(item.occurredAt);
    const past = occurredAt && occurredAt <= now ? occurredAt : null;
    const people = item.people?.filter((person) => person.trim());
    const withPeople = people?.length
        ? t('draft.withPeople', { people: new Intl.ListFormat(language, { style: 'long', type: 'conjunction' }).format(people) }) : '';
    switch (item.kind) {
        case 'brief': return t('todayBrief.conversationOpener');
        case 'activity': {
            const when = past ? (past.toDateString() === now.toDateString()
                ? t('draft.earlierToday') : t('draft.onDay', { day: day(past) })) : '';
            const duration = item.durationMinutes && item.durationMinutes > 0
                ? t('draft.duration', { count: item.durationMinutes }) : '';
            return when || duration
                ? t('draft.activity', { title, when, duration })
                : t('draft.activityWithoutTime', { title });
        }
        case 'meeting': {
            const startsAt = validDate(item.startsAt);
            if (startsAt && startsAt > now) {
                const time = new Intl.DateTimeFormat(language, { hour: 'numeric', minute: '2-digit' }).format(startsAt);
                const when = startsAt.toDateString() === now.toDateString()
                    ? t('draft.atTime', { time }) : t('draft.onDayAtTime', { day: day(startsAt), time });
                return t('draft.meetingUpcoming', { title, when, withPeople });
            }
            const meetingDay = startsAt || past;
            return meetingDay
                ? t('draft.meetingPast', { title, day: day(meetingDay), withPeople })
                : t('draft.meetingUpcoming', { title, when: '', withPeople });
        }
        case 'open_loop': {
            const waitingSince = validDate(item.waitingSince);
            return waitingSince && waitingSince <= now
                ? t('draft.openLoopWaiting', { title, day: day(waitingSince) })
                : t('draft.openLoop', { title });
        }
        case 'purpose': return t('draft.purpose', { title });
        case 'file': return t('draft.file', { title });
        case 'news': return t('draft.news', { title });
        default: return t('draft.generic', { title });
    }
}
