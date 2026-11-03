import type { MobileBriefDocument, TodayDayShape } from '@/lib/wails-shim-app';

const at = (time: string) => `2026-10-23T${time}:00-07:00`;
export const dayShapeFixture: TodayDayShape = {
    rangeStart: at('08:00'), rangeEnd: at('19:00'), now: at('10:00'), calendarConnected: true,
    events: [
        { id: 'priya', start: at('10:15'), end: at('11:00'), title: '1:1 with Priya on Halcyon Meet', shortName: 'Priya', shortestName: 'Priya', initial: 'P', kind: 'meeting' },
        { id: 'maya', start: at('11:30'), end: at('12:00'), title: 'Check-in with Maya Chen', shortName: 'Maya', shortestName: 'Maya', initial: 'M', kind: 'meeting' },
        { id: 'article', start: at('12:00'), end: at('13:00'), title: 'Weekly article', shortName: 'Weekly article', shortestName: 'Article', kind: 'meeting' },
        { id: 'workout', start: at('17:30'), end: at('18:30'), title: 'Workout', shortName: 'Workout', shortestName: 'Workout', kind: 'personal' },
    ],
    hold: { start: at('15:45'), end: at('16:45'), label: 'memo' },
};

export const todayNextBrief: MobileBriefDocument = {
    version: 1, briefId: '2026-10-23-morning', slot: 'morning', localDate: '2026-10-23',
    asOf: at('07:30'), timezone: 'America/Los_Angeles', coldStart: false, narrativeStatus: 'ok',
    narrative: [
        { text: 'Good morning. Your weekly Tuesday 1:1 with Priya starts at 10:15 AM on Halcyon Meet, the first thing on your day, so it deserves your attention before anything else. Three of today’s meetings are worth preparing for, and this is the earliest. I can gather the recent work and open items you share with Priya into a short prep list, and nothing goes out without you.', refs: [], topicId: 'priya', topicName: 'Priya' },
        { text: 'Next is your check-in with Maya Chen at 11:30 AM on Halcyon Meet, and the weekly article is right after it. I can pull together a prep list for Maya in the same way, so you walk in with the open items fresh.', refs: [], topicId: 'maya', topicName: 'Maya Chen' },
        { text: 'The afternoon is lighter, with room to finish the work you started yesterday. Your workout is at 5:30 PM. I can prepare the next meeting once the morning is done. Nothing is held for the Halcyon launch memo yet; the hour after your last call is an open stretch. You can use that space for the memo if you like. I will keep the preparation short, so you can review the details and decide what to do next yourself.', refs: [], topicId: 'halcyon', topicName: 'Halcyon' },
        { text: 'Draft the Halcyon launch memo?', refs: [] },
    ],
    sections: { moved: [], doing: [], watching: [], meetings: [{ id: 'shape', key: 'shape', kind: 'day_shape', title: 'The shape of today', detail: 'Legacy shape times must not repeat.', status: '', at: '', refs: [] }], people: [], openLoops: [], learned: [], deliverables: [], schedules: [] },
    evidence: [], dayShape: null,
    audio: { mime: 'audio/mp4', bytes: 1200, durationSeconds: 48 },
    cards: [
        ...['Prep me for the Priya 1:1', 'Prep me for the Maya check-in', 'Review the Halcyon launch memo'].map((label, paragraph) => ({
            id: `prep-${paragraph}`, kind: 'question' as const, mode: 'send' as const, paragraph, label, message: `${label}.`, refs: [],
        })),
        { id: 'hold', kind: 'action', mode: 'send', label: 'Draft it', message: 'Draft the Halcyon launch memo.', answerTo: 'closing', refs: [] },
        { id: 'decline', kind: 'action', mode: 'send', label: 'Not today', message: 'Leave the memo time open today.', answerTo: 'closing', refs: [] },
        { id: 'opener', kind: 'opener', mode: 'speak_first', message: 'Let’s talk about your brief.', refs: [] },
    ],
    limits: { itemsPerSection: 8, evidenceRefs: 60, truncatedSections: [], excludedSensitiveItems: 1 },
};
