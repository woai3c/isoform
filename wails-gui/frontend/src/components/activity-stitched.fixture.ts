import type { ActivityThreadView, ActivityView } from '@/lib/wails-shim-app';

// A public fixture morning shaped like the RC 1 Activity artboard: two pieces
// of work, each picked back up after an interruption, and two interruptions.
// Used by activity-stitched.test.tsx; no real activity.

export function stitchedFixture(day: Date): { activities: ActivityView[]; threads: ActivityThreadView[] } {
    const at = (h: number, m: number) => new Date(day.getFullYear(), day.getMonth(), day.getDate(), h, m).getTime();
    const moment = (id: string, title: string, start: number, end: number, apps: string[], extra: Partial<ActivityView> = {}): ActivityView => ({
        id,
        status: 'settled',
        title,
        description: '',
        latestChange: '',
        category: 'build',
        categoryColor: '',
        entities: [],
        apps: apps.map((appName) => ({ appKey: '', appName, activeMs: end - start })),
        activeMs: end - start,
        startMs: start,
        endMs: end,
        runCount: 1,
        thumbnails: [],
        inProgress: false,
        ...extra,
    });
    const activities = [
        moment('a1', 'Reviewing ERP connector v0.3', at(9, 12), at(10, 2), ['VS Code', 'GitHub'], { latestChange: 'Left review comment 1 of 2 in invoice_mapping.go' }),
        moment('a2', 'Northwind call', at(10, 2), at(10, 30), ['Halcyon Meet']),
        moment('a3', 'Resolving ERP review comments', at(10, 30), at(11, 40), ['VS Code', 'GitHub'], { latestChange: 'Both review comments resolved' }),
        moment('a4', 'Email triage', at(11, 40), at(11, 55), ['Mail']),
        moment('a5', 'Writing Halcyon launch memo', at(12, 0), at(12, 20), ['Google Docs']),
        moment('a6', 'Slack: candidate question', at(12, 20), at(12, 28), ['Slack']),
        moment('a7', 'Writing Halcyon launch memo', at(12, 28), at(12, 55), ['Google Docs', 'Slack'], { latestChange: 'Stopped in section 3, Integration risks' }),
    ];
    const base = { kind: 'work', userTitled: false, reviewed: false, unfinished: false, interruption: false };
    const threads: ActivityThreadView[] = [
        {
            ...base,
            id: 'hal',
            title: 'Reviewing ERP connector v0.3',
            startMs: at(9, 12),
            endMs: at(11, 40),
            timeSpentMs: 120 * 60_000,
            activeMs: 120 * 60_000,
            activityIds: ['a1', 'a3'],
            stretches: [
                { start: at(9, 12), end: at(10, 2), activeMs: 50 * 60_000, activityIds: ['a1'], resumePoint: { kind: 'pr', label: 'PR #212, invoice_mapping.go' } },
                { start: at(10, 30), end: at(11, 40), activeMs: 70 * 60_000, activityIds: ['a3'], resumePoint: { kind: 'pr', label: 'PR #212' } },
            ],
            gaps: [{ start: at(10, 2), end: at(10, 30), minutes: 28, label: 'Northwind call', threadId: 'nw' }],
            linkReason: { kind: 'rule', keys: ['pr:#212', 'file:invoice_mapping.go', 'file:erp_sync.go'], text: '', resumedWithinMinutes: 28 },
        },
        {
            ...base,
            id: 'mer',
            title: 'Writing Halcyon launch memo',
            startMs: at(12, 0),
            endMs: at(12, 55),
            timeSpentMs: 47 * 60_000,
            activeMs: 47 * 60_000,
            activityIds: ['a5', 'a7'],
            stretches: [
                { start: at(12, 0), end: at(12, 20), activeMs: 20 * 60_000, activityIds: ['a5'], resumePoint: { kind: 'path', label: 'Halcyon launch memo, section 2' } },
                { start: at(12, 28), end: at(12, 55), activeMs: 27 * 60_000, activityIds: ['a7'], resumePoint: { kind: 'path', label: 'Halcyon launch memo, section 3' } },
            ],
            gaps: [{ start: at(12, 20), end: at(12, 28), minutes: 8, label: 'Quick replies', threadId: 'msg' }],
            linkReason: { kind: 'model', keys: [], text: 'the same memo document', resumedWithinMinutes: 8 },
            unfinished: true,
        },
        {
            ...base,
            id: 'nw',
            title: 'Northwind call',
            startMs: at(10, 2),
            endMs: at(10, 30),
            timeSpentMs: 28 * 60_000,
            activeMs: 28 * 60_000,
            activityIds: ['a2'],
            stretches: [{ start: at(10, 2), end: at(10, 30), activeMs: 28 * 60_000, activityIds: ['a2'], resumePoint: null }],
            gaps: [],
            linkReason: { kind: 'rule', keys: [], text: '' },
            interruption: true,
        },
        {
            ...base,
            id: 'msg',
            title: 'Quick replies',
            startMs: at(11, 40),
            endMs: at(12, 28),
            timeSpentMs: 23 * 60_000,
            activeMs: 23 * 60_000,
            activityIds: ['a4', 'a6'],
            stretches: [
                { start: at(11, 40), end: at(11, 55), activeMs: 15 * 60_000, activityIds: ['a4'], resumePoint: null },
                { start: at(12, 20), end: at(12, 28), activeMs: 8 * 60_000, activityIds: ['a6'], resumePoint: null },
            ],
            gaps: [{ start: at(11, 55), end: at(12, 20), minutes: 25, label: 'away', threadId: '' }],
            linkReason: { kind: 'user', keys: [], text: '' },
            interruption: true,
        },
    ];
    return { activities, threads };
}

/** Ten qualifying threads, a five-minute boundary, and nine sub-two-minute
 *  moments (including a zero-length tick, twelve minutes active in total).
 *  The two-stretch thread deliberately carries three gap records to guard
 *  against overlapping labels even in an uneven projection. */
export function stitchedCleanupFixture(day: Date): { activities: ActivityView[]; threads: ActivityThreadView[] } {
    const base = stitchedFixture(day);
    const at = (h: number, m: number) => new Date(day.getFullYear(), day.getMonth(), day.getDate(), h, m).getTime();
    const activities: ActivityView[] = [];
    const threads: ActivityThreadView[] = [];
    for (let i = 0; i < 10; i++) {
        const minutes = i === 9 ? 5 : 50 - i * 4;
        const start = at(8 + Math.floor(i / 2), (i % 2) * 30);
        const end = start + minutes * 60_000;
        const id = `work-${i}`;
        const moment = { ...base.activities[0], id: `dense-${i}`, title: i === 0 ? 'OpenClaw cleanup: Remove settings and verify the fresh shell' : `Project ${i}: Claude Code sessions`, startMs: start, endMs: end, activeMs: minutes * 60_000 };
        activities.push(moment);
        threads.push({ ...base.threads[0], id, title: moment.title, startMs: start, endMs: end, timeSpentMs: moment.activeMs, activeMs: moment.activeMs, activityIds: [moment.id],
            stretches: [{ start, end, activeMs: moment.activeMs, activityIds: [moment.id], resumePoint: null }], gaps: [], unfinished: false });
    }
    const resumed = threads[0];
    resumed.stretches = [
        { ...resumed.stretches[0], end: at(8, 25), activeMs: 25 * 60_000 },
        { ...resumed.stretches[0], start: at(13, 19), end: at(13, 44), activeMs: 25 * 60_000 },
    ];
    resumed.endMs = at(13, 44);
    activities[0].endMs = at(8, 25);
    activities[0].activeMs = 25 * 60_000;
    const returned = { ...activities[0], id: 'dense-0-return', startMs: at(13, 19), endMs: at(13, 44) };
    activities.push(returned);
    resumed.activityIds.push(returned.id);
    resumed.stretches[1].activityIds = [returned.id];
    resumed.gaps = [
        { start: at(8, 25), end: at(10, 1), minutes: 96, label: 'Project 1: Claude Code sessions', threadId: 'work-1' },
        { start: at(10, 1), end: at(11, 37), minutes: 96, label: 'away', threadId: '' },
        { start: at(11, 37), end: at(12, 54), minutes: 77, label: 'away', threadId: '' },
    ];
    for (let i = 0; i < 9; i++) {
        const start = at(14, i * 2);
        activities.push({ ...base.activities[0], id: `short-${i}`, title: `Quick check ${i}`, startMs: start, endMs: start + (i === 0 ? 0 : 90_000), activeMs: i === 0 ? 0 : 90_000 });
    }
    return { activities, threads };
}
