// Component fixture only: the real ActivityPage over synthetic, walk-through-shaped
// reads (a Monday of ERP review and a launch memo). No app or backend connection.
// URL params: section, date (YYYY-MM-DD), case (writing | unwritten | sections | weekFail | weekEmpty), lang (en | zh-CN), theme (paper | onedark).
// Screen timeline frames load from a local stand-in for the host bridge on port 4317.
import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import i18n from '@/i18n';
import { ActivityPage, type ActivitySectionId } from '@/components/activity-page';
import { stitchedFixture } from '@/components/activity-stitched.fixture';
import type { CronJobsProp } from '@/components/timeline/useCalendarData';

const params = new URLSearchParams(location.search);
const scenario = params.get('case') ?? '';
const section = (params.get('section') ?? 'yourDay') as ActivitySectionId;
const initialDate = params.get('date') ?? '2026-11-02';
const noop = () => {};
const at = (d: number, h: number, m = 0) => new Date(2026, 10, d, h, m).getTime();
const ymd = (t: number) => { const d = new Date(t); return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`; };

const { activities, threads } = stitchedFixture(new Date(2026, 10, 2));
const descriptions: Record<string, string> = {
    a1: 'You read the ERP connector v0.3 diff and left the first of two review comments on PR #212.',
    a2: 'You joined the Northwind call about the invoice sync dates.',
    a3: 'You resolved both review comments on PR #212 and pushed the fix to invoice_mapping.go.',
    a4: 'You answered three emails and archived the rest.',
    a5: 'You drafted section 2 of the Halcyon launch memo.',
    a6: 'You answered a candidate question in Slack.',
    a7: 'You wrote section 3 of the Halcyon launch memo and stopped at Integration risks.',
};
activities.forEach((a) => { a.description = descriptions[a.id] ?? ''; });

const summaryText = 'You spent the morning on the ERP connector review. You left two comments on PR #212 and resolved both after the Northwind call. After lunch you wrote section 3 of the Halcyon launch memo.';
// The day written in sections, as the daily digest prompt asks for them.
const sectionsText = [
    'You spent the morning on the ERP connector review and the afternoon on the Halcyon launch memo.',
    '',
    '### ERP connector v0.3',
    '- Decision: keep invoice dates in the customer time zone, as agreed on the Northwind call.',
    '- Done: resolved both review comments on PR #212 and pushed the fix to invoice_mapping.go.',
    '- Open: PR #212 is waiting on one approval.',
    '',
    '### Halcyon launch memo',
    '- Done: wrote sections 2 and 3.',
    '- Next: finish the Integration risks section.',
    '- Blocked: the launch date needs Priya to confirm the Northwind cut-over.',
].join('\n');
const daily = (date: string) => {
    const none = scenario === 'unwritten' && date === initialDate;
    return {
        date, aiSummary: none ? '' : scenario === 'sections' ? sectionsText : summaryText, aiSummaryStatus: none ? 'none' : 'done', aiSummaryLanguage: 'en', isPartial: false,
        summaryCreatedAt: at(2, 12, 4), autoStatus: none ? 'unavailable' : scenario === 'writing' ? 'refreshing' : 'ready',
        // The rows' stretches: 2h + 47m + 28m + 23m.
        totalActiveMs: 3 * 3600_000 + 38 * 60_000, activeAvgMs: 0, sessionCount: 7, sessionAvg: 0, uniqueApps: 6, uniqueAppsAvg: 0,
        snapshotCount: 0, ocrPageCount: 0, hourlyActive: Array.from({ length: 24 }, (_, h) => (h >= 9 && h <= 13 ? 45 : 0)),
        appBreakdown: [{ appName: 'VS Code', bundleId: 'com.microsoft.VSCode', activeMs: 5400_000, percentage: 47 }, { appName: 'Google Docs', bundleId: 'com.google.Chrome', activeMs: 2820_000, percentage: 25 }],
        moments: [], topOcrKeywords: [], topWindowTitles: [],
    };
};
const weekHeadlines: Record<string, string> = {
    '2026-10-26': 'You planned the ERP connector v0.3 release with the Northwind team.',
    '2026-10-27': 'You wrote the invoice mapping and its tests.',
    '2026-10-28': 'You reviewed two design docs and answered candidate questions.',
    '2026-10-29': 'You fixed the invoice sync dates and shipped v0.3 to staging.',
    '2026-10-30': 'You started the Halcyon launch memo.',
};
const weekly = {
    weekStart: '2026-10-26', weekEnd: '2026-11-01', aiSummary: '', aiSummaryStatus: 'none', aiSummaryLanguage: 'en', autoStatus: 'unavailable',
    dailyStats: [
        ['2026-10-26', 'Mon', 4 * 3600_000 + 5 * 60_000], ['2026-10-27', 'Tue', 5 * 3600_000 + 40 * 60_000], ['2026-10-28', 'Wed', 3 * 3600_000 + 15 * 60_000],
        ['2026-10-29', 'Thu', 6 * 3600_000 + 2 * 60_000], ['2026-10-30', 'Fri', 2 * 3600_000 + 30 * 60_000], ['2026-10-31', 'Sat', 0], ['2026-11-01', 'Sun', 0],
    ].map(([date, weekday, activeMs]) => ({ date, weekday, activeMs, sessionCount: activeMs ? 4 : 0 })),
    totalActiveMs: 21 * 3600_000 + 32 * 60_000, dailyAvgMs: 0, prevWeekMs: 0, topApps: [],
};
const yourWeek = { briefId: '2026-10-30-evening', timeZone: 'America/Los_Angeles', cards: [],
    week: { start: '2026-10-26', end: '2026-10-30', lead: 'You shipped ERP connector v0.3 to staging and started the Halcyon launch memo. Next: finish its Integration risks section.' } };

const learnedTitles = [
    'You review a pull request in one sitting, then resolve every comment before you merge.', 'You keep launch memos in Google Docs, one section per risk.',
    'You prefer pnpm over npm for JavaScript projects.', 'You answer candidate questions in Slack the same day.', 'You run the full test suite before you push to staging.',
    'You write release notes for each connector version.', 'You keep a short agenda for every partner call.', 'You file follow-ups as tasks, not as email flags.',
    'You use VS Code for Go and TypeScript.', 'You check invoice dates against the partner export before a sync.', 'You prefer short daily summaries over long ones.',
    'You block focus time on Tuesday mornings.', 'You keep design reviews to thirty minutes.', 'You name branches after the ticket they fix.',
    'You read diffs file by file, tests first.', 'You draft memos alone, then share them for comments.', 'You archive email after you answer it.',
    'You keep one notes doc per partner.', 'You batch Slack replies around noon.', 'You track release dates in the team calendar.',
    'You write commit messages in the imperative.', 'You start the week by planning in the morning.', 'You end the day by listing what is left.',
];
const memoryEntries = learnedTitles.map((title, i) => ({ id: `m${i}`, title, content: `# ${title}\n\n${title}`, dimension: i % 3 ? 'workflow' : 'user_insight', source: 'activity', modTime: at(2, 13, 20) - i * 7 * 60_000, updatedAt: at(2, 13), sizeBytes: 40, apps: [], activitySessionIds: [] }));
const knowHow = [
    ['k1', 'Resolve ERP review comments in invoice_mapping.go before the sync job runs.'],
    ['k2', 'Check the Northwind export dates against the invoice sync before you send them.'],
    ['k3', 'Write the Halcyon launch memo one risk per section.'],
].map(([id, title], i) => ({ id, title, kind: 'how_to', takeaway: title, content: `# ${title}\n\n${title}`, date: '2026-11-02', sessionIds: [], points: [], apps: [], sessionCount: 1, savedAt: at(2, 13) - i * 60_000, reuseCount: 0, topicId: '', topicName: '' }));
const aboutYou = { beliefs: memoryEntries.map((m, i) => ({ id: m.id, title: m.title, area: i % 3 ? '' : 'work', source: i === 0 ? 'assistant-history' : 'activity', sourceAssistant: i === 0 ? 'claude' : '', sourceAt: m.modTime, status: 'inferred', confidence: 2 })), newThisWeek: [], totals: { beliefs: memoryEntries.length, confirmed: 0 } };

// Every session but Quick replies kept a first frame.
// A scrap of speech ("There the two.") is not a step.
const fixtureEvents = (a: (typeof activities)[number]) => [
    { kind: 'focus', focusLabel: 'connector/retry.go', timestamp: a.startMs + 60_000, appName: a.apps[0]?.appName ?? '' },
    { kind: 'speech', speechSource: 'microphone', speechText: 'There the two.', timestamp: a.startMs + 120_000, appName: a.apps[0]?.appName ?? '' },
    { kind: 'keyboard', typedText: 'retry on 429 with backoff', timestamp: a.startMs + 180_000, appName: a.apps[0]?.appName ?? '' },
    { kind: 'speech', speechSource: 'microphone', speechText: "Let's ship the connector on Friday.", timestamp: a.startMs + 240_000, appName: a.apps[0]?.appName ?? '' },
];
// Sessions with a first frame keep their screenshots, so their view has a replay above Steps (activity review v1.0.6 AR43).
const fixtureSnapshots = (a: (typeof activities)[number]) => a.id === 'a4' ? [] : [0, 1, 2].map((k) => ({
    id: k + 1, sessionId: '', timestamp: a.startMs + k * 60_000, filePath: `/Users/ana/Library/Isoform/frames/${a.id}.jpg`, width: 1440, height: 900,
    sizeBytes: 0, trigger: 'interval', storageTier: 'full', axText: '', browserUrl: '', app: a.apps[0]?.appName ?? '' }));
const groups = activities.map((a, i) => ({ id: `g${i}`, appKey: a.apps[0]?.appName ?? '', appName: a.apps[0]?.appName ?? '', startTime: a.startMs, endTime: a.endMs, activeMs: a.activeMs, eventCount: 3, sessionIds: [`s${i}`], triggerKinds: [], primarySessionId: `s${i}`, summaryStatus: 'done', summary: { title: a.title, description: a.description }, events: fixtureEvents(a),
    firstFrame: a.id === 'a4' ? undefined : { timestamp: a.startMs, path: `/Users/ana/Library/Isoform/frames/${a.id}.jpg`, focusX: 0, focusY: 0, focusW: 0, focusH: 0 } }));
const sessions = activities.map((a, i) => ({ id: `s${i}`, appName: a.apps[0]?.appName ?? '', bundleId: '', windowTitle: a.title, startTime: a.startMs, endTime: a.endMs, durationMs: a.activeMs, activeMs: a.activeMs, snapshotCount: 0, eventCount: 3, summary: a.description, title: a.title }));

let asked = false;
const userItemId = 'u-ask';
const reads: Record<string, (...args: any[]) => unknown> = {
    EnsureDailySummary: (date: string) => daily(date), GetDailySummary: (date: string) => ({ ...daily(date), aiSummary: weekHeadlines[date] ?? '' }),
    // case=workedOnFails: What you worked on can't be read.
    GetActivitiesInRange: (start: number) => (scenario === 'workedOnFails' ? Promise.reject(new Error('read failed')) : ymd(start) === '2026-11-02' ? activities : []),
    GetDayThreads: (start: number) => (scenario === 'workedOnFails' ? Promise.reject(new Error('read failed')) : { grouped: true, threads: ymd(start) === '2026-11-02' ? threads : [] }),
    GetDayNoticed: (date: string) => (date === '2026-11-02' ? [{ id: 'n1', kind: 'follow_up', momentId: 'a2', momentTitle: 'Northwind call', momentStartMs: at(2, 10, 2), text: 'Send Priya the corrected invoice sync dates', why: 'You told her on the Northwind call that you would send them today.' }] : []),
    GetMemoryEntries: () => memoryEntries, GetKnowledgeEntries: () => knowHow, GetAboutYou: () => aboutYou,
    GetActivityMonthStats: () => [], GetCronRunsByDateRange: () => [], GetRecentMeetingNotes: () => [], GetMeetingNotesForSessions: () => [],
    EnsureWeeklySummary: () => { if (scenario === 'weekFail') throw new Error('database is locked'); return weekly; },
    GetWeeklySummaryForDate: () => weekly, GetYourWeekForDate: () => (scenario === 'weekFail' || scenario === 'weekEmpty' ? { cards: [] } : yourWeek),
    GetCaptureCounts: () => ({ paused: false, notReady: false, todayStart: at(2, 0), weekStart: at(2, 0),
        sources: [{ source: 'screens', today: 385, week: 2140 }, { source: 'audioMinutes', today: 42, week: 160 }] }),
    GetActivityTimelineGroups: () => groups, GetActivitySessions: () => sessions, GetActivityTimelineGroupsForSessions: () => groups,
    GetHostBridgePort: () => 4317, GetHostBridgeMediaToken: () => 'fixture',
    GetActivitySessionDetail: (id: string) => { const a = activities[Number(id.slice(1))]; return { id, startTime: a.startMs, endTime: a.endMs, triggerKind: '', appNames: [a.apps[0]?.appName ?? ''], eventCount: 3, snapshotCount: fixtureSnapshots(a).length, triageStatus: '', ocrStatus: '', segments: [], snapshots: fixtureSnapshots(a),
        events: [] }; }, GetAppIconsForBundleIDs: () => ({}), ListSystemApps: () => [], GetActivityReminderChoices: () => [],
    GetMainChat: () => ({ session: { localId: 'main' } }),
    AskMainChatWithContext: () => { asked = true; return { sessionId: 'main', userItemId }; },
    LoadStreamItemsPage: () => ({ hasMore: false, nextCursor: 0, items: asked ? [
        { id: userItemId, kind: 'user', content: 'What was left on PR #212?', createdAt: at(2, 13, 24) },
        { id: 'a-ask', kind: 'assistant', content: 'Nothing. You resolved both review comments at 11:40 AM and pushed the fix to invoice_mapping.go. The PR is waiting on one approval.', createdAt: at(2, 13, 24) },
    ] : [] }),
    GetMainChatOverview: () => ({ conversations: 1, waiting: 0, finished: 0, routine: 0 }), ListMainChatWaitingItems: () => ({ items: [], hasMore: false, nextCursor: '' }),
    LoadUIState: () => '', IsClosedEyesModeEnabled: () => false,
};
(window as any).go = { main: { App: new Proxy({}, { get: (_, method: string) => async (...args: unknown[]) => {
    const read = reads[method];
    return read ? read(...args) : null;
} }) } };
const listeners = new Map<string, Set<(...a: unknown[]) => void>>();
(window as any).runtime = new Proxy({}, { get: (_, name: string) => name === 'EventsOnMultiple'
    ? (event: string, fn: (...a: unknown[]) => void) => { if (!listeners.has(event)) listeners.set(event, new Set()); listeners.get(event)!.add(fn); return () => listeners.get(event)?.delete(fn); }
    : name === 'EventsEmit' ? (event: string, ...a: unknown[]) => listeners.get(event)?.forEach((fn) => fn(...a)) : () => noop });
(window as any).__emit = (event: string, ...a: unknown[]) => listeners.get(event)?.forEach((fn) => fn(...a));

const cron = { jobs: [], loading: false } as unknown as CronJobsProp;
function Shot() {
    const [current, setCurrent] = useState<ActivitySectionId>(section);
    return <ActivityPage section={current} onSectionChange={setCurrent} cronJobs={cron} initialDate={initialDate} onOpenChat={noop} onStartChat={noop} />;
}
await i18n.changeLanguage(params.get('lang') ?? 'en');
document.documentElement.setAttribute('data-theme', params.get('theme') ?? 'paper');
createRoot(document.getElementById('root')!).render(<main className="bg-background h-screen flex flex-col"><Shot /></main>);
