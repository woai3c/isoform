// Component fixture only: synthetic moments, no app or backend connection.
import React from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import i18n from '@/i18n';
import { ActivityDayView } from '@/components/activity-day-view';
import { stitchedFixture } from '@/components/activity-stitched.fixture';

const params = new URLSearchParams(location.search);
const date = new Date(2026, 9, 19);
const { activities, threads } = stitchedFixture(date);
const start = new Date(2026, 9, 19, 19, 44).getTime();
const end = new Date(2026, 9, 20, 12, 20).getTime();
const moment = { ...activities[0], id: 'overnight', title: 'Review public project', startMs: start, endMs: end, activeMs: end - start, inProgress: true };
activities.push(moment);
threads.push({ ...threads[0], id: 'overnight', title: moment.title, startMs: start, endMs: end, activeMs: end - start,
    timeSpentMs: end - start, activityIds: [moment.id], gaps: [],
    stretches: [{ start, end, activeMs: end - start, activityIds: [moment.id], resumePoint: null }] });
const fixture = window as unknown as Record<string, any>;
const noop = () => {};
fixture.runtime = { EventsOn: () => noop };
fixture.go = { main: { App: new Proxy({}, { get: (_, method: string) => async (dayStart: number, dayEnd: number) => {
    if (method === 'GetActivitiesInRange') return activities.map((a) => a.id === 'overnight' ? { ...a, activeMs: Math.max(0, Math.min(dayEnd, end) - Math.max(dayStart, start)) } : a);
    if (method === 'GetDayThreads') return { grouped: true, threads: threads.map((thread) => thread.id === 'overnight' ? { ...thread, activeMs: Math.max(0, Math.min(dayEnd, end) - Math.max(dayStart, start)), timeSpentMs: Math.max(0, Math.min(dayEnd, end) - Math.max(dayStart, start)), stretches: thread.stretches.map((s) => ({ ...s, activeMs: Math.max(0, Math.min(dayEnd, end) - Math.max(dayStart, start)) })) } : thread) };
    return [];
} }) } };
await i18n.changeLanguage(params.get('lang') ?? 'en');
createRoot(document.getElementById('root')!).render(<main className="bg-background h-screen flex flex-col">
    <ActivityDayView date={date} searchQuery="" bridgePort={0} dailyData={null} dayLoading={false}
        automationRuns={[]} knowledge={[]} memoryEntries={[]} hourRange={{ start: 0, end: 23 }} maxHourly={0}
        workedOnView="stitched" section="workedOn" reloadDailySummary={async () => {}}
        onOpenSession={noop} onOpenRunDetail={noop} />
</main>);
