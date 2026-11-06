import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { TodayDayStrip, freeDaySpans } from './TodayDayStrip';
import { dayShapeFixture, todayNextBrief } from './today-next.fixture';
import { briefClockTime } from '@/lib/brief-time';

beforeEach(async () => {
    await i18n.changeLanguage('en');
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
        font: '',
        measureText: (text: string) => ({ width: text.length * 6 }),
    } as CanvasRenderingContext2D);
});
afterEach(() => vi.restoreAllMocks());
it('opens the exact meeting from the strip and leaves personal time and holds inert', () => {
    const open = vi.fn();
    render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} onOpenMeeting={open} />);
    const meeting = screen.getByRole('button', { name: /1:1 with Priya on Halcyon Meet.*open prep/ });
    fireEvent.click(meeting);
    expect(open).toHaveBeenCalledWith('priya');
    expect(screen.getAllByRole('button')).toHaveLength(3);
    expect(screen.getByTestId('today-day-hold').tagName).toBe('SPAN');
    const personal = screen.getAllByTestId('today-day-event').find((element) => element.dataset.kind === 'personal');
    expect(personal?.tagName).toBe('SPAN');
});
it('draws nothing and says nothing when calendar is disconnected', () => {
    const { container } = render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, calendarConnected: false }} />);
    expect(container).toBeEmptyDOMElement();
});
it('opens the held block menu and removes only on an explicit action', () => {
    const remove = vi.fn();
    render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} holdPreview="held" holdTitle="Q4 plan" onRemoveHold={remove} />);
    fireEvent.click(screen.getByRole('button', { name: 'Held for Q4 plan' }));
    expect(remove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Remove the hold' }));
    expect(remove).toHaveBeenCalledOnce();
});
it('opens the proposed hold without accepting it', () => {
    const open = vi.fn();
    render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} holdTitle="Q4 plan" onOpenHold={open} />);
    fireEvent.click(screen.getByRole('button', { name: 'Open hold offer for Q4 plan' }));
    expect(open).toHaveBeenCalledOnce();
});
it('opens a saved event menu and withdraws it with the visible event', () => {
    const remove = vi.fn();
    const openMeeting = vi.fn();
    const held = { ...dayShapeFixture.events[0], start: '2026-10-23T13:00:00-07:00', end: '2026-10-23T14:00:00-07:00', holdId: 'hold-1' };
    const shape = { ...dayShapeFixture, hold: null, events: [held] };
    const { rerender } = render(<TodayDayStrip requestedDate="2026-10-23" shape={shape} onRemoveHold={remove} onOpenMeeting={openMeeting} />);
    expect(screen.queryByRole('button', { name: /open prep/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Held for 1:1 with Priya on Halcyon Meet' }));
    expect(openMeeting).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Remove the hold' }));
    expect(remove).toHaveBeenCalledWith('hold-1');
    fireEvent.click(screen.getByRole('button', { name: 'Held for 1:1 with Priya on Halcyon Meet' }));
    rerender(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...shape, events: [] }} onRemoveHold={remove} />);
    expect(screen.queryByRole('button', { name: 'Remove the hold' })).not.toBeInTheDocument();
    expect(screen.queryByText('Held for 1:1 with Priya on Halcyon Meet')).not.toBeInTheDocument();
});
it('removes the hold menu when the event starts', () => {
    vi.useFakeTimers();
    try {
        const held = { ...dayShapeFixture.events[0], start: '2026-10-23T10:01:00-07:00', end: '2026-10-23T11:01:00-07:00', holdId: 'hold-1' };
        const remove = vi.fn();
        const shape = { ...dayShapeFixture, hold: null, events: [held] };
        const { rerender } = render(<TodayDayStrip requestedDate="2026-10-23" shape={shape} onRemoveHold={remove} />);
        expect(screen.getByRole('button', { name: 'Held for 1:1 with Priya on Halcyon Meet' })).toBeInTheDocument();
        act(() => vi.advanceTimersByTime(30_000));
        rerender(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...shape, events: [...shape.events] }} onRemoveHold={remove} />);
        act(() => vi.advanceTimersByTime(30_000));
        expect(screen.queryByRole('button', { name: 'Held for 1:1 with Priya on Halcyon Meet' })).not.toBeInTheDocument();
        expect(screen.getByTestId('today-day-event')).toBeInTheDocument();
    } finally { vi.useRealTimers(); }
});
it('draws no box while loading, with invalid data, or without events or a hold', () => {
    const { container, rerender } = render(<TodayDayStrip requestedDate="2026-10-23" shape={null} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, rangeStart: '' }} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [], hold: null }} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [] }} />);
    expect(screen.getByTestId('today-day-hold')).toBeInTheDocument();
});
function event(id: string, start: string, end: string) {
    return { ...dayShapeFixture.events[0], id, title: id, shortName: id, shortestName: id, initial: undefined,
        start: `2026-10-23T${start}:00-07:00`, end: `2026-10-23T${end}:00-07:00` };
}
it('keeps back-to-back events in one 40 px band', () => {
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, hold: null }} />);
    expect(screen.getByTestId('today-day-track')).toHaveStyle({ height: '40px' });
    for (const block of screen.getAllByTestId('today-day-event')) expect(block).toHaveStyle({ top: '6px', height: '28px' });
});
it('draws the shorter overlap above the longer event, inset 4 px with its label intact', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 1100 } as DOMRect);
    try {
        render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, hold: null,
            events: [event('short', '09:15', '09:45'), event('long', '09:00', '10:00')] }} />);
        expect(screen.getByTestId('today-day-track')).toHaveStyle({ height: '40px' });
        const blocks = screen.getAllByTestId('today-day-event');
        expect(blocks.map((block) => block.title.split(' · ')[0])).toEqual(['long', 'short']);
        expect(blocks[0]).toHaveStyle({ top: '6px', height: '28px' });
        expect(blocks[1]).toHaveStyle({ top: '10px', height: '20px' });
        expect(screen.getByText('short', { exact: true })).toBeVisible();
        expect(blocks[1].parentElement).toContainElement(screen.getByText('short', { exact: true }));
        expect(Number(blocks[1].parentElement!.style.zIndex)).toBeGreaterThan(Number(blocks[0].parentElement!.style.zIndex));
        expect(blocks[0]).toHaveAccessibleName(/long.*9:00 AM.*10:00 AM/);
    } finally { vi.restoreAllMocks(); }
});
it('keeps one full-span label when a shorter event extends beyond its longer overlap', () => {
    const measure = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 1500 } as DOMRect);
    try {
        render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, hold: null,
            events: [event('Planning', '10:00', '11:00'), event('Alexandra', '10:40', '11:20')] }} />);
        expect(screen.getAllByText('Alexandra', { exact: true })).toHaveLength(1);
        expect(screen.getByText('Alexandra', { exact: true })).toHaveStyle({ left: `${(160 / 660) * 100}%` });
    } finally { measure.mockRestore(); }
});
it.each([300, 2000])('fits a longer label without clipping at %i px', (width) => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width } as DOMRect);
    try {
        render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, hold: null,
            events: [event('long', '09:00', '12:00'), event('short', '10:00', '11:00')] }} />);
        const label = screen.getAllByTestId('today-day-event-label').find((node) => node.dataset.eventId === 'long');
        expect(label).toBeDefined();
        if (width === 300) expect(label).toHaveTextContent('');
        else expect(label).toHaveTextContent('long');
        expect(screen.getAllByTestId('today-day-event')[0].title).toContain('long');
    } finally { vi.restoreAllMocks(); }
});
it.each([3, 4])('draws the two shortest of %i simultaneous events and folds the others into the underlying tooltip', (count) => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 1100 } as DOMRect);
    try {
        render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, hold: null,
            events: [event('longest', '09:00', '13:00'), ...(count === 4 ? [event('longer', '09:00', '12:00')] : []),
                event('short', '09:00', '11:00'), event('shortest', '09:00', '10:00')] }} />);
        const atNine = screen.getAllByTestId('today-day-event').filter((block) => block.style.left === `${100 / 11}%`);
        expect(atNine).toHaveLength(2);
        expect(atNine[0].title).toContain(`and ${count - 2} more`);
        expect(atNine[0].title).toContain('longest');
        if (count === 4) expect(atNine[0].title).toContain('longer');
        expect(atNine[0]).toHaveStyle({ top: '6px', height: '28px' });
        expect(atNine[1]).toHaveStyle({ top: '10px', height: '20px' });
        expect(screen.getByText('shortest', { exact: true })).toBeVisible();
        expect(screen.getByTestId('today-day-track')).toHaveStyle({ height: '40px' });
    } finally { vi.restoreAllMocks(); }
});
it('does not fold events that overlap in a chain but never three at once', () => {
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, hold: null,
        events: [event('first', '09:00', '11:00'), event('middle', '10:00', '12:00'), event('last', '11:00', '13:00')] }} />);
    expect(new Set(screen.getAllByTestId('today-day-event').map((block) => block.title.split(' · ')[0])).size).toBe(3);
    for (const block of screen.getAllByTestId('today-day-event')) expect(block.title).not.toContain(' more');
});
it('preserves ink/personal distinction, ended opacity, grain and a two-pixel inter-block gap', () => {
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, now: '2026-10-23T11:15:00-07:00' }} />);
    const events = screen.getAllByTestId('today-day-event');
    expect(events[0]).toHaveClass('bg-ink', 'text-background');
    expect(events[0].parentElement).toHaveStyle({ opacity: '0.45' });
    expect(events[3]).toHaveClass('border-ink', 'bg-background', 'text-ink');
    expect(events[1].style.width).toMatch(/- 2px\)/);
    expect(screen.getByTestId('today-day-track')).toHaveClass('patina-ground');
    expect(screen.getByTestId('today-day-now')).toHaveClass('w-0.5', 'bg-accent');
});
it.each(['en', 'zh-CN'])('uses the shared Today clock for ticks, tooltips, free captions and generated prose (%s)', async (language) => {
    await i18n.changeLanguage(language);
    const tr = (key: string, options?: Record<string, unknown>) => String(i18n.t(key as never, { ...options, ns: 'today' } as never));
    render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} timeZone="America/Los_Angeles" />);
    const time = briefClockTime(dayShapeFixture.events[0].start, language, tr, 'America/Los_Angeles')!;
    expect(screen.getAllByTestId('today-day-event')[0].title).toContain(time);
    if (language === 'en') expect(todayNextBrief.narrative[0].text).toContain(time);
    expect(screen.getByTestId('today-day-hours')).toHaveTextContent(briefClockTime(dayShapeFixture.rangeStart, language, tr, 'America/Los_Angeles')!);
    expect(screen.queryByTestId('today-day-caption')).not.toBeInTheDocument();
});
it('unions overlaps and clips out-of-range events before labeling free stretches of at least 45 minutes', () => {
    expect(freeDaySpans(0, 10, [{ start: -2, end: 2 }, { start: 1, end: 4 }, { start: 6, end: 12 }])).toEqual([{ start: 4, end: 6 }]);
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} />);
    expect(screen.getByText('free 2h 45m')).toBeInTheDocument();
    expect(screen.queryByTestId('today-day-caption')).not.toBeInTheDocument();
});

it('uses the person names supplied by the day shape', () => {
    const measure = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    try {
        render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} />);
        expect(screen.getAllByTestId('today-day-event-label')[0]).toHaveTextContent('Priya');
        expect(screen.getAllByTestId('today-day-event-label')[1]).toHaveTextContent('Maya');
    } finally { measure.mockRestore(); }
});
it('keeps chip labels left aligned and never shows an email address', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 3000 } as DOMRect);
    try {
        const item = { ...event('xipeng.li@example.com', '09:00', '12:00'), shortName: 'Xipeng', shortestName: 'Xipeng', initial: 'X' };
        render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, hold: null, events: [item] }} />);
        const label = screen.getByTestId('today-day-event-label');
        expect(label).toHaveClass('overflow-hidden', 'justify-start', 'text-left', 'px-1.5');
        expect(label.textContent).not.toContain('@');
    } finally { vi.restoreAllMocks(); }
});
it('labels a preview hold as Hold? before confirmation and Held afterward', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 3000 } as DOMRect);
    try {
        const { rerender } = render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} holdPreview="proposed" />);
        expect(screen.getByTestId('today-day-hold-label')).toHaveTextContent('Hold?');
        rerender(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} holdPreview="held" />);
        expect(screen.getByTestId('today-day-hold-label')).toHaveTextContent('Held');
    } finally { vi.restoreAllMocks(); }
});
it.each(['en', 'zh-CN'])('names tomorrow and omits its marker (%s)', async (language) => {
    await i18n.changeLanguage(language);
    const tomorrow = JSON.parse(JSON.stringify(dayShapeFixture).replaceAll('2026-10-23T', '2026-10-24T'));
    tomorrow.now = dayShapeFixture.now;
    render(<TodayDayStrip requestedDate="2026-10-24" shape={tomorrow} timeZone="America/Los_Angeles" />);
    expect(screen.queryByTestId('today-day-now')).not.toBeInTheDocument();
    expect(screen.queryByTestId('today-day-caption')).not.toBeInTheDocument();
});
it('draws no now line after the strip\'s hours and dims all ended events (R53)', () => {
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, now: '2026-10-23T22:00:00-07:00' }} timeZone="America/Los_Angeles" />);
    expect(screen.queryByTestId('today-day-now')).not.toBeInTheDocument();
    expect(screen.getByTestId('today-day-hold-group')).toHaveStyle({ opacity: '0.45' });
    for (const group of screen.getAllByTestId('today-day-event-group')) expect(group).toHaveStyle({ opacity: '0.45' });
});
it.each(['en', 'zh-CN'])('uses the requested Friday even when an overnight event widens the range into Thursday (%s)', async (language) => {
    await i18n.changeLanguage(language);
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, rangeStart: '2026-10-22T22:00:00-07:00' }} timeZone="America/Los_Angeles" />);
    expect(screen.getByTestId('today-day-now')).toBeInTheDocument();
    expect(screen.queryByTestId('today-day-caption')).not.toBeInTheDocument();
});

it.each([
    ['2026-10-23', '2026-10-23T08:00:00-07:00', '0px'],
    ['2026-10-23', '2026-10-23T18:30:00-07:00', `clamp(0px, ${(10.5 / 11) * 100}%, calc(100% - 2px))`],
])('keeps the same-day marker inside the strip at %s / %s', (requestedDate, now, left) => {
    render(<TodayDayStrip requestedDate={requestedDate} shape={{ ...dayShapeFixture, now }} timeZone="America/Los_Angeles" />);
    expect(screen.getByTestId('today-day-now')).toHaveStyle({ left });
});
it.each([
    '2026-10-23T00:10:00-07:00',
    '2026-10-23T06:00:00-07:00',
    '2026-10-23T07:59:00-07:00',
    '2026-10-23T19:00:00-07:00',
    '2026-10-23T23:00:00-07:00',
])('draws no now line outside the strip\'s hours at %s (R53)', (now) => {
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, now }} timeZone="America/Los_Angeles" />);
    expect(screen.queryByTestId('today-day-now')).not.toBeInTheDocument();
    expect(screen.getByTestId('today-day-track')).toBeInTheDocument();
});
it('adds the now line when the clock reaches the strip\'s first hour (R53)', () => {
    vi.useFakeTimers();
    try {
        vi.setSystemTime(new Date('2026-10-23T07:58:30-07:00'));
        render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, now: '2026-10-23T07:58:30-07:00', hold: null }} timeZone="America/Los_Angeles" />);
        expect(screen.queryByTestId('today-day-now')).not.toBeInTheDocument();
        act(() => { vi.advanceTimersByTime(2 * 60_000); });
        expect(screen.getByTestId('today-day-now')).toBeInTheDocument();
    } finally {
        vi.useRealTimers();
    }
});
it.each(['2026-10-22', '2026-10-24', '2026-10-26'])('never draws a marker on another day (%s)', (requestedDate) => {
    render(<TodayDayStrip requestedDate={requestedDate} shape={dayShapeFixture} timeZone="America/Los_Angeles" />);
    expect(screen.queryByTestId('today-day-now')).not.toBeInTheDocument();
});
it('dims each ended event once around its background pieces and single label', () => {
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, now: '2026-10-23T22:00:00-07:00' }} />);
    const groups = [...screen.getAllByTestId('today-day-event-group'), screen.getByTestId('today-day-hold-group')];
    expect(groups).toHaveLength(dayShapeFixture.events.length + 1);
    for (const group of groups) {
        expect(group).toHaveStyle({ opacity: '0.45' });
        const label = group.querySelector('[data-testid$="-label"]')!;
        expect(label).toBeInTheDocument();
        expect((label as HTMLElement).style.opacity).toBe('');
        for (const piece of group.querySelectorAll<HTMLElement>('[data-inset]')) expect(piece.style.opacity).toBe('');
    }
});

it('draws nothing while its data is unavailable', () => {
    const { container } = render(<TodayDayStrip requestedDate="2026-10-23" shape={null} />);
    expect(container).toBeEmptyDOMElement();
});

it('does not re-render events for repeated identical refreshes', () => {
    const parse = vi.spyOn(Date, 'parse');
    try {
        const { rerender } = render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} />);
        const events = screen.getAllByTestId('today-day-event');
        parse.mockClear();
        rerender(<TodayDayStrip requestedDate="2026-10-23" shape={structuredClone(dayShapeFixture)} />);
        rerender(<TodayDayStrip requestedDate="2026-10-23" shape={structuredClone(dayShapeFixture)} />);
        expect(parse).not.toHaveBeenCalled();
        screen.getAllByTestId('today-day-event').forEach((event, index) => expect(event).toBe(events[index]));
    } finally { parse.mockRestore(); }
});

it('ticks the now-line without re-rendering events', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-23T10:00:00-07:00'));
    const parse = vi.spyOn(Date, 'parse');
    try {
        render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} timeZone="America/Los_Angeles" />);
        const events = screen.getAllByTestId('today-day-event');
        parse.mockClear();
        await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
        expect(screen.getByTestId('today-day-now')).toHaveAttribute('title', 'Now, 10:01 AM');
        expect(parse).not.toHaveBeenCalled();
        screen.getAllByTestId('today-day-event').forEach((event, index) => expect(event).toBe(events[index]));
    } finally {
        parse.mockRestore();
        vi.useRealTimers();
    }
});

it.each(['en', 'zh-CN'])('fits or drops free labels above the now line in %s', async (language) => {
    await i18n.changeLanguage(language);
    const measure = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect');
    measure.mockReturnValue({ width: 2000 } as DOMRect);
    const { rerender } = render(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} />);
    expect(screen.queryByTestId('today-day-caption')).not.toBeInTheDocument();
    expect(screen.getAllByTestId('today-day-free').some((label) => label.textContent)).toBe(true);
    expect(screen.getByTestId('today-day-now')).toHaveStyle({ zIndex: '0' });
    expect(screen.getAllByTestId('today-day-free')[0]).toHaveClass('z-[2]');
    measure.mockReturnValue({ width: 10 } as DOMRect);
    rerender(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, rangeEnd: '2026-10-23T19:01:00-07:00' }} />);
    expect(screen.getAllByTestId('today-day-free').every((label) => !label.textContent)).toBe(true);
});

it('shows the backend short name for a weekly meeting', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    const weekly = { ...dayShapeFixture.events[0], title: 'Weekly NMG Check In', shortName: 'Weekly', shortestName: 'NMG' };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [weekly], hold: null }} />);
    expect(screen.getByTestId('today-day-event-label')).toHaveTextContent('NMG Check In');
});
it('never cuts a free label down to its bare duration (today-review v1.1.13 D7)', async () => {
    await i18n.changeLanguage('zh-CN');
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 450 } as DOMRect);
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, now: '2026-10-23T09:00:00-07:00' }} />);
    expect(screen.queryByText('1小时15分')).toBeNull();
    expect(screen.queryByText('1小时15分钟')).toBeNull();
});

it('names a block by its title when it has no short name and the name fits (today-review v1.1.13 D7)', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    const dinner = { ...dayShapeFixture.events[0], kind: 'personal' as const, title: 'Dinner with Sam', shortName: '' };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [dinner], hold: null }} />);
    expect(screen.getByTestId('today-day-event-label')).toHaveTextContent('Dinner with Sam');
});


it('names a narrow block by its shortest name when its short name does not fit (today-review v1.1.13 D7)', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 1200 } as DOMRect);
    const dinner = { ...dayShapeFixture.events[0], id: 'dinner', kind: 'personal' as const, title: 'Dinner with Sam', shortName: 'Dinner with Sam', shortestName: 'Dinner' };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [dinner], hold: null }} />);
    expect(screen.getByTestId('today-day-event-label')).toHaveTextContent(/^Dinner$/);
});

it.each(['en', 'zh-CN'])('says only Private on a private item, in %s (today-review v1.1.13 D7)', async (lang) => {
    await i18n.changeLanguage(lang);
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    const hidden = { ...dayShapeFixture.events[0], id: 'hidden', title: '', shortName: '', shortestName: '', initial: '', private: true };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [hidden], hold: null }} onOpenMeeting={vi.fn()} />);
    const block = screen.getByTestId('today-day-event');
    const word = lang === 'en' ? 'Private' : '私人';
    expect(block.getAttribute('title')).toBe(word);
    expect(screen.getByTestId('today-day-event-label').textContent).toBe(lang === 'en' ? 'Private' : '私人');
    expect(block.tagName).toBe('SPAN');
    expect(screen.queryByRole('button')).toBeNull();
});

it.each(['en', 'zh-CN'])('reads Busy in and on hover for a block with no title, in %s (R30 v3)', async (lang) => {
    await i18n.changeLanguage(lang);
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    const untitled = { ...dayShapeFixture.events[0], id: 'untitled', title: '', shortName: '', shortestName: '', initial: '', private: false };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [untitled], hold: null }} />);
    const word = lang === 'en' ? 'Busy' : '忙碌';
    expect(screen.getByTestId('today-day-event').getAttribute('title')).toBe(word);
    expect(screen.getByTestId('today-day-event').getAttribute('aria-label')).toBe(word);
    expect(screen.getByTestId('today-day-event-label').textContent).toBe(word);
});

it('draws a private event as an outlined block, never a solid black bar, and reads Private on hover (R30)', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    const hidden = { ...dayShapeFixture.events[0], id: 'hidden', kind: 'meeting' as const, title: '', shortName: '', shortestName: '', initial: '', private: true };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [hidden], hold: null }} />);
    const block = screen.getByTestId('today-day-event');
    expect(block.className).not.toMatch(/\bbg-ink\b/);
    expect(block.className).toMatch(/\bborder-ink\b/);
    expect(block.getAttribute('title')).toBe('Private');
});

it('draws a Busy block outlined, never solid black, in the block and on hover (R30 v4)', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    const untitled = { ...dayShapeFixture.events[0], id: 'busy', kind: 'meeting' as const, title: '', shortName: '', shortestName: '', initial: '' };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [untitled], hold: null }} />);
    const block = screen.getByTestId('today-day-event');
    expect(block.className).not.toMatch(/\bbg-ink\b/);
    expect(block.className).toMatch(/\bborder-ink\b/);
    expect(screen.getByTestId('today-day-event-label').className).toMatch(/\btext-ink\b/);
    expect(block.getAttribute('title')).toBe('Busy');
});

it('keeps a narrow private block with no word inside it, but its hover and accessible name read Private (R30 v4)', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 300 } as DOMRect);
    const hidden = { ...dayShapeFixture.events[0], id: 'narrow', kind: 'meeting' as const, title: '', shortName: '', shortestName: '', initial: '', private: true, start: '2026-10-23T11:00:00-07:00', end: '2026-10-23T11:20:00-07:00' };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [hidden], hold: null }} />);
    expect(screen.getByTestId('today-day-event-label').textContent).toBe('');
    const block = screen.getByTestId('today-day-event');
    expect(block.getAttribute('title')).toBe('Private');
    expect(block.getAttribute('aria-label')).toBe('Private');
});

it('names an event the brief names with its first whole word that fits, never a cut name (R30 v3)', () => {
    const review = { ...dayShapeFixture.events[0], id: 'review', kind: 'meeting' as const, title: 'Design review with Maya', shortName: '', shortestName: '' };
    const shape = { ...dayShapeFixture, events: [review], hold: null };
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 1000 } as DOMRect);
    const { unmount } = render(<TodayDayStrip requestedDate="2026-10-23" shape={shape} briefText="Start with the 10:15 design review with Maya, then clear the memo." />);
    expect(screen.getByTestId('today-day-event-label')).toHaveTextContent(/^Design$/);
    unmount();
    // Too narrow for even the first word: nothing shows, and the full name stays on hover.
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 400 } as DOMRect);
    render(<TodayDayStrip requestedDate="2026-10-23" shape={shape} briefText="Start with the 10:15 design review with Maya." />);
    expect(screen.getByTestId('today-day-event-label').textContent).toBe('');
    expect(screen.getByTestId('today-day-event').getAttribute('title')).toContain('Design review with Maya');
});

it('never shows a brief-named title cut mid-word when no whole word fits (R30 v3)', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 1000 } as DOMRect);
    const long = { ...dayShapeFixture.events[0], id: 'long', kind: 'meeting' as const, title: 'Quarterlybusinessreviewmeeting', shortName: '', shortestName: '' };
    render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, events: [long], hold: null }} briefText="Quarterlybusinessreviewmeeting is at 10:15." />);
    expect(screen.getByTestId('today-day-event-label').textContent).toBe('');
});

it('says Tomorrow on a strip for tomorrow, with no now line (today-review v1.1.17 R21)', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 1200 } as DOMRect);
    const { rerender } = render(<TodayDayStrip requestedDate="2026-10-24" shape={dayShapeFixture} />);
    expect(screen.getByTestId('today-day-strip-day')).toHaveTextContent('Tomorrow');
    expect(screen.queryByTestId('today-day-now')).toBeNull();
    rerender(<TodayDayStrip requestedDate="2026-10-23" shape={dayShapeFixture} />);
    expect(screen.queryByTestId('today-day-strip-day')).toBeNull();
    expect(screen.getByTestId('today-day-now')).toBeInTheDocument();
});

it('labels only the part of a free stretch still ahead and updates as time passes', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-23T10:15:00-07:00'));
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
    const shape = { ...dayShapeFixture, now: '2026-10-23T10:15:00-07:00', hold: null, events: [dayShapeFixture.events[0], { ...dayShapeFixture.events[1], start: '2026-10-23T18:00:00-07:00', end: '2026-10-23T18:30:00-07:00' }] };
    try {
        const { rerender } = render(<TodayDayStrip requestedDate="2026-10-23" shape={shape} timeZone="America/Los_Angeles" />);
        expect(screen.getByText('free 7h')).toBeInTheDocument();
        expect(screen.queryByText('free 1h')).not.toBeInTheDocument();
        await act(async () => { await vi.advanceTimersByTimeAsync(120_000); });
        rerender(<TodayDayStrip key="later" requestedDate="2026-10-23" shape={{ ...shape, now: '2026-10-23T11:02:00-07:00' }} timeZone="America/Los_Angeles" />);
        expect(screen.getByText('free 6h 58m')).toBeInTheDocument();
    } finally { vi.useRealTimers(); }
});

it.each(['en', 'zh-CN'])('a strip read at 8:00 and shown at 11:29 keeps past free time plain and dims what ended, with the now line (today-review v1.1.22 R21) in %s', async (language) => {
    await i18n.changeLanguage(language);
    vi.useFakeTimers();
    try {
        vi.setSystemTime(new Date('2026-10-23T08:00:00-07:00'));
        vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 2000 } as DOMRect);
        render(<TodayDayStrip requestedDate="2026-10-23" shape={{ ...dayShapeFixture, now: '2026-10-23T08:00:00-07:00', hold: null }} />);
        const label = (duration: string) => screen.getAllByTestId('today-day-free').some((free) => free.textContent === duration);
        const morningFree = i18n.t('today:dayStrip.freeDuration', { duration: i18n.t('today:dayStrip.hoursMinutes', { hours: 2, minutes: 15 }) });
        expect(label(morningFree)).toBe(true);
        act(() => { vi.advanceTimersByTime(209 * 60_000); });
        expect(label(morningFree)).toBe(false);
        const priya = screen.getAllByTestId('today-day-event-group').find((group) => group.querySelector('[aria-label*="Priya"]'));
        expect(priya).toHaveStyle({ opacity: '0.45' });
        expect(screen.getByTestId('today-day-now')).toBeInTheDocument();
    } finally {
        vi.useRealTimers();
    }
});
